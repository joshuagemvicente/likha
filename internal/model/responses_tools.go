package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

func validResponsesToolName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func validResponsesID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if unicode.IsSpace(c) || unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func responsesParametersObject(raw json.RawMessage) bool {
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil || schema == nil {
		return false
	}
	if rawType, ok := schema["type"]; ok {
		var kind string
		if json.Unmarshal(rawType, &kind) != nil || kind != "object" {
			return false
		}
	}
	return true
}

func validateResponsesCallHeader(callID, name, namespace string, allowed map[string]bool) error {
	if !validResponsesID(callID) || name == "" {
		return errors.New("Responses function_call item is missing a call ID or name, or has an invalid ID")
	}
	if namespace != "" && namespace != responsesToolNamespace {
		return fmt.Errorf("Responses function_call has unknown namespace %q", namespace)
	}
	// Names are unqualified in the public schema. Never strip a prefix from
	// untrusted names: that could accidentally dispatch a different local tool.
	if !validResponsesToolName(name) {
		return fmt.Errorf("Responses function_call has invalid tool name %q", name)
	}
	if allowed != nil && !allowed[name] {
		return fmt.Errorf("Responses function_call requests unadvertised tool %q", name)
	}
	return nil
}

func validateResponsesArguments(callID, arguments string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &object); err != nil || object == nil {
		return fmt.Errorf("Responses function_call %q has non-JSON-object arguments", callID)
	}
	return nil
}

func validateResponsesCall(callID, name, namespace, arguments string, allowed map[string]bool) error {
	if err := validateResponsesCallHeader(callID, name, namespace, allowed); err != nil {
		return err
	}
	return validateResponsesArguments(callID, arguments)
}

type responsesTextPart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

// Namespace is an official field on both response and replay function calls:
// https://developers.openai.com/api/reference/resources/responses/methods/create
type codexResponseOutput struct {
	Type      string              `json:"type"`
	ID        string              `json:"id"`
	CallID    string              `json:"call_id"`
	Name      string              `json:"name"`
	Namespace string              `json:"namespace"`
	Arguments string              `json:"arguments"`
	Status    string              `json:"status"`
	Content   []responsesTextPart `json:"content"`
	Summary   []responsesTextPart `json:"summary"`
}

type codexCallBuilder struct {
	item          codexResponseOutput
	arguments     strings.Builder
	argumentsDone *string
}

// Track streamed identities, but do not expose calls before completion. The
// completed response is authoritative; it must not silently omit streamed calls.
type codexCallTracker struct {
	calls   map[string]*codexCallBuilder
	callIDs map[string]string
	allowed map[string]bool
}

func (t *codexCallTracker) add(item codexResponseOutput) error {
	if !validResponsesID(item.ID) {
		return errors.New("Responses function_call item is missing an item ID or has an invalid ID")
	}
	if err := validateResponsesCallHeader(item.CallID, item.Name, item.Namespace, t.allowed); err != nil {
		return err
	}
	if t.calls == nil {
		t.calls = make(map[string]*codexCallBuilder)
		t.callIDs = make(map[string]string)
	}
	if _, ok := t.calls[item.ID]; ok {
		return fmt.Errorf("duplicate Responses function_call item %q", item.ID)
	}
	if _, ok := t.callIDs[item.CallID]; ok {
		return fmt.Errorf("duplicate Responses function_call call ID %q", item.CallID)
	}
	b := &codexCallBuilder{item: item}
	b.arguments.WriteString(item.Arguments)
	t.calls[item.ID] = b
	t.callIDs[item.CallID] = item.ID
	return nil
}

func (t *codexCallTracker) builder(itemID string) (*codexCallBuilder, error) {
	if !validResponsesID(itemID) {
		return nil, errors.New("Responses function_call arguments are missing an item ID or have an invalid ID")
	}
	b := t.calls[itemID]
	if b == nil {
		return nil, fmt.Errorf("Responses function_call arguments reference unknown item %q", itemID)
	}
	return b, nil
}

func (t *codexCallTracker) appendArguments(itemID, delta string) error {
	b, err := t.builder(itemID)
	if err != nil {
		return err
	}
	if b.argumentsDone != nil {
		return fmt.Errorf("Responses function_call arguments arrived after done for item %q", itemID)
	}
	b.arguments.WriteString(delta)
	return nil
}

func (t *codexCallTracker) setArguments(itemID, arguments string) error {
	b, err := t.builder(itemID)
	if err != nil {
		return err
	}
	if err := validateResponsesArguments(b.item.CallID, arguments); err != nil {
		return err
	}
	// Both function_call_arguments.done and output_item.done carry full,
	// authoritative arguments, not another fragment to concatenate.
	b.argumentsDone = &arguments
	b.arguments.Reset()
	return nil
}

func (t *codexCallTracker) done(item codexResponseOutput) error {
	if item.Status != "" && item.Status != "completed" {
		return fmt.Errorf("Responses function_call %q has invalid done status %q", item.CallID, item.Status)
	}
	if err := validateResponsesCall(item.CallID, item.Name, item.Namespace, item.Arguments, t.allowed); err != nil {
		return err
	}
	if t.calls[item.ID] == nil {
		if err := t.add(item); err != nil {
			return err
		}
	}
	b, err := t.builder(item.ID)
	if err != nil {
		return err
	}
	if b.item.CallID != item.CallID || b.item.Name != item.Name {
		return fmt.Errorf("Responses function_call item %q changed its identity", item.ID)
	}
	return t.setArguments(item.ID, item.Arguments)
}

func (t *codexCallTracker) completed(output []codexResponseOutput) ([]ToolCall, error) {
	var calls []ToolCall
	seen := make(map[string]bool)
	seenItems := make(map[string]bool)
	for _, item := range output {
		if item.Type != "function_call" {
			continue
		}
		if item.Status != "" && item.Status != "completed" {
			return nil, fmt.Errorf("Responses function_call %q has invalid completion status %q", item.CallID, item.Status)
		}
		if err := validateResponsesCall(item.CallID, item.Name, item.Namespace, item.Arguments, t.allowed); err != nil {
			return nil, err
		}
		if seen[item.CallID] {
			return nil, fmt.Errorf("duplicate Responses function_call call ID %q", item.CallID)
		}
		seen[item.CallID] = true
		if item.ID != "" {
			if !validResponsesID(item.ID) {
				return nil, errors.New("Responses function_call has an invalid item ID")
			}
			if seenItems[item.ID] {
				return nil, fmt.Errorf("duplicate Responses function_call item %q", item.ID)
			}
			seenItems[item.ID] = true
			if b := t.calls[item.ID]; b != nil && b.item.CallID != item.CallID {
				return nil, fmt.Errorf("Responses function_call item %q changed its identity", item.ID)
			}
		}
		if b := t.calls[t.callIDs[item.CallID]]; b != nil {
			if b.item.Name != item.Name || item.ID != "" && item.ID != b.item.ID {
				return nil, fmt.Errorf("Responses function_call %q changed its identity", item.CallID)
			}
		}
		calls = append(calls, ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
	}
	for id := range t.callIDs {
		if !seen[id] {
			return nil, fmt.Errorf("response.completed omitted streamed function_call %q", id)
		}
	}
	return calls, nil
}
