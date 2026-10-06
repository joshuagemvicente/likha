package model

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"likha/internal/model/catalog"
)

// EffortWire is how a provider carries a reasoning-effort level
// (specs/model-effort). The zero value means the provider cannot carry one:
// Likha never sends effort to it and offers no effort choice. A provider row
// leaves the zero value until a live probe shows the field is honored.
type EffortWire uint8

const (
	// EffortUnsupported sends no effort field.
	EffortUnsupported EffortWire = iota
	// EffortTopLevel sends chat-completions "reasoning_effort": "<level>".
	EffortTopLevel
	// EffortObject sends "reasoning": {"effort": "<level>"}, the shape of
	// OpenRouter's unified parameter and of the Responses API.
	EffortObject
)

// reasoningParam is the body of an EffortObject field.
type reasoningParam struct {
	Effort string `json:"effort"`
}

// EffortLevels returns the effort levels Likha offers for modelID on p, in
// the model's published order. It is nil when p cannot carry effort or the
// model publishes no levels. A provider's live model list wins over the
// bundled catalog, whose lookup matches the exact (provider, model) pair.
func EffortLevels(p Provider, modelID string, live []string) []string {
	if p.Effort == EffortUnsupported {
		return nil
	}
	if len(live) > 0 {
		return live
	}
	row, ok := catalog.Lookup(p.Name, modelID)
	if !ok {
		return nil
	}
	return row.Efforts
}

// EffortUnavailableReason says why p carries no effort, for the switch entry
// and /effort. It is empty when p can carry effort.
func EffortUnavailableReason(p Provider) string {
	if p.Effort != EffortUnsupported {
		return ""
	}
	if p.EffortNote != "" {
		return p.EffortNote
	}
	name := p.DisplayName
	if name == "" {
		name = "this endpoint"
	}
	return "not yet verified for " + name
}

// IsReasoningModel reports whether the bundled catalog marks the exact
// (provider, model) pair as a reasoning model.
func IsReasoningModel(provider, modelID string) bool {
	row, ok := catalog.Lookup(provider, modelID)
	return ok && row.Reasoning
}

// EffortRejectedError marks a request that carried an effort level and was
// rejected by the provider as a bad request. Err is the provider error.
type EffortRejectedError struct {
	Level string
	Err   error
}

func (e *EffortRejectedError) Error() string { return e.Err.Error() }

func (e *EffortRejectedError) Unwrap() error { return e.Err }

// SetEffort sets the level sent on later requests and how the provider
// carries it. An empty level, or EffortUnsupported, sends no effort field.
// Like SetModel, call it only between runs.
func (c *Client) SetEffort(wire EffortWire, level string) {
	if c == nil {
		return
	}
	c.effortWire, c.effort = wire, level
}

// Effort returns the level sent on requests; "" means none is sent.
func (c *Client) Effort() string {
	if c == nil || c.effortWire == EffortUnsupported {
		return ""
	}
	return c.effort
}

// WithoutEffort returns a client that sends no effort, for background
// requests (compaction, session naming) that always use the provider's
// default (specs/model-effort decision 9). With no effort set it returns c
// itself, so those requests keep updating c's usage and rate-limit state
// exactly as before; otherwise it returns a fork with effort cleared.
func (c *Client) WithoutEffort() (*Client, error) {
	if c.Effort() == "" {
		return c, nil
	}
	child, err := c.Fork()
	if err != nil {
		return nil, fmt.Errorf("client without effort: %w", err)
	}
	child.SetEffort(EffortUnsupported, "")
	return child, nil
}

// effortRejectedStatus reports whether an HTTP status means the provider
// refused the request as invalid, which a request carrying effort may have
// caused. Any other failure is reported without the effort hint.
func effortRejectedStatus(status int) bool {
	return status == http.StatusBadRequest || status == http.StatusUnprocessableEntity
}

// liveEfforts reads OpenRouter's per-model reasoning object from a /models
// entry: supported_efforts (documented highest first) and default_effort.
// The levels are returned lowest first, matching the catalog's order. A
// missing, differently shaped, or malformed object yields no live levels, so
// the bundled catalog applies instead.
func liveEfforts(raw json.RawMessage) ([]string, string) {
	if len(raw) == 0 {
		return nil, ""
	}
	var reasoning struct {
		SupportedEfforts []string `json:"supported_efforts"`
		DefaultEffort    string   `json:"default_effort"`
	}
	if json.Unmarshal(raw, &reasoning) != nil || len(reasoning.SupportedEfforts) == 0 {
		return nil, ""
	}
	levels := make([]string, 0, len(reasoning.SupportedEfforts))
	for i := len(reasoning.SupportedEfforts) - 1; i >= 0; i-- {
		levels = append(levels, reasoning.SupportedEfforts[i])
	}
	if catalog.ValidateEfforts(levels) != nil {
		return nil, ""
	}
	if !slices.Contains(levels, reasoning.DefaultEffort) {
		return levels, ""
	}
	return levels, reasoning.DefaultEffort
}
