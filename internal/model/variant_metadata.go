package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"likha/internal/model/catalog"
)

// ReasoningCapabilities preserves live field presence. A missing field falls
// back to exact-pair documented metadata; an explicitly empty field does not.
type ReasoningCapabilities struct {
	ControlsKnown bool
	Controls      []catalog.ReasoningOption
	EffortsKnown  bool
	Efforts       []string
	Mandatory     *bool
	Budget        *bool
	Adaptive      *bool
	Toggle        *bool
	DefaultEffort string
	Output        int64
	// Invalid says why the provider's live reasoning object was ignored. A
	// malformed object carries no reasoning fields, so the exact-pair catalog
	// controls apply instead and Default stays selectable.
	Invalid string
}

func reasoningControls(p Provider, id string, live *ReasoningCapabilities) ([]catalog.ReasoningOption, int64, error) {
	row, _ := catalog.Lookup(p.Name, id)
	controls := slices.Clone(row.ReasoningOptions)
	output := row.Output
	if p.Name == "chatgpt" && controls == nil {
		if values := chatGPTDocumentedEfforts[id]; len(values) > 0 {
			controls = []catalog.ReasoningOption{{Type: catalog.OptionEffort, Values: slices.Clone(values)}}
		}
	}
	if live == nil {
		return controls, output, nil
	}
	if live.Output > 0 {
		output = live.Output
	}
	if live.Invalid != "" {
		return controls, output, nil
	}
	if live.ControlsKnown {
		controls = slices.Clone(live.Controls)
	}
	if live.EffortsKnown {
		controls = slices.DeleteFunc(controls, func(v catalog.ReasoningOption) bool { return v.Type == catalog.OptionEffort })
		if len(live.Efforts) > 0 {
			controls = append(controls, catalog.ReasoningOption{Type: catalog.OptionEffort, Values: slices.Clone(live.Efforts)})
		}
	}
	if live.Budget != nil {
		if !*live.Budget {
			controls = slices.DeleteFunc(controls, func(v catalog.ReasoningOption) bool { return v.Type == catalog.OptionBudgetTokens })
		} else if !slices.ContainsFunc(controls, func(v catalog.ReasoningOption) bool { return v.Type == catalog.OptionBudgetTokens }) {
			controls = append(controls, catalog.ReasoningOption{Type: catalog.OptionBudgetTokens})
		}
	}
	if live.Mandatory != nil {
		controls = slices.DeleteFunc(controls, func(v catalog.ReasoningOption) bool { return v.Type == catalog.OptionToggle })
		if !*live.Mandatory && p.Name == "openrouter" {
			controls = append(controls, catalog.ReasoningOption{Type: catalog.OptionToggle})
		}
	}
	if live.Toggle != nil {
		controls = slices.DeleteFunc(controls, func(v catalog.ReasoningOption) bool { return v.Type == catalog.OptionToggle })
		if *live.Toggle && (live.Mandatory == nil || !*live.Mandatory) {
			controls = append(controls, catalog.ReasoningOption{Type: catalog.OptionToggle})
		}
	}
	for _, option := range controls {
		if option.Type == catalog.OptionEffort && len(option.Values) > 0 {
			if err := catalog.ValidateEfforts(catalog.EffortLevels([]catalog.ReasoningOption{option})); err != nil {
				return nil, output, fmt.Errorf("invalid published effort controls: %w", err)
			}
		}
		if option.Type == catalog.OptionBudgetTokens && (option.Min != nil && *option.Min < 0 || option.Max != nil && *option.Max < 0 || option.Min != nil && option.Max != nil && *option.Min > *option.Max) {
			return nil, output, fmt.Errorf("invalid published budget bounds")
		}
	}
	return controls, output, nil
}

func decodeReasoningCapabilities(raw json.RawMessage) *ReasoningCapabilities {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	// A malformed object is reported with no partially decoded fields, so
	// nothing half-read can override the catalog fallback.
	invalid := func(reason string) *ReasoningCapabilities {
		return &ReasoningCapabilities{Invalid: reason}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return invalid("reasoning must be an object")
	}
	live := &ReasoningCapabilities{}
	for _, field := range []struct {
		key    string
		target **bool
	}{{"mandatory", &live.Mandatory}, {"supports_max_tokens", &live.Budget}} {
		if value, ok := fields[field.key]; ok {
			var flag bool
			if json.Unmarshal(value, &flag) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return invalid(field.key + " must be a boolean")
			}
			*field.target = &flag
		}
	}
	if value, ok := fields["supported_efforts"]; ok {
		live.EffortsKnown = true
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			live.Efforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
		} else if json.Unmarshal(value, &live.Efforts) != nil || live.Efforts == nil {
			return invalid("supported_efforts must be an array or null")
		} else {
			slices.Reverse(live.Efforts) // OpenRouter documents descending wire order.
		}
		if len(live.Efforts) > 0 && catalog.ValidateEfforts(live.Efforts) != nil {
			return invalid("supported_efforts contains invalid or repeated levels")
		}
	}
	if value, ok := fields["default_effort"]; ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		if json.Unmarshal(value, &live.DefaultEffort) != nil {
			return invalid("default_effort must be a string")
		}
	}
	return live
}

// SIWC visibility is discovered live. This table supplies only exact-model
// controls documented by OpenAI, never another provider's catalog rows:
// https://platform.openai.com/docs/guides/reasoning (checked 2026-10-09).
var chatGPTDocumentedEfforts = map[string][]string{
	"gpt-5.5":             {"none", "low", "medium", "high", "xhigh"},
	"gpt-5.4":             {"none", "low", "medium", "high", "xhigh"},
	"gpt-5.4-mini":        {"none", "low", "medium", "high", "xhigh"},
	"gpt-5.3-codex":       {"low", "medium", "high", "xhigh"},
	"gpt-5.3-codex-spark": {"low", "medium", "high", "xhigh"},
	"gpt-6.1-sol":         {"low", "medium", "high", "xhigh", "max"},
	"gpt-6-sol":           {"low", "medium", "high", "xhigh", "max"},
}
