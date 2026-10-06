// Package effortprobe runs the user-started reasoning-effort probe
// (specs/effort-probe, FR-40): a small, capped set of real model requests
// that shows whether each published effort level reaches a provider's model.
// It reports what it observed and never changes a setting or enables a
// provider. The composition root resolves credentials and hands over a ready
// client; this package imports only model and model/catalog.
package effortprobe

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"likha/internal/model"
	"likha/internal/model/catalog"
)

// ControlLevel is the invalid effort level sent once per shape. A provider
// that rejects it is reading the field (spec decision 5).
const ControlLevel = "likha-probe-invalid"

// Caps bound one run's spend (spec decision 8).
type Caps struct {
	MaxRequests    int
	TokenBudget    int64 // completion tokens; no new request starts once reached
	RequestTimeout time.Duration
	// AssumedOutput is the completion count charged for a request that
	// reports no usage, and the cost estimate's output per request.
	AssumedOutput int64
}

// DefaultCaps are the fixed caps of spec decision 8.
func DefaultCaps() Caps {
	return Caps{MaxRequests: 17, TokenBudget: 40_000, RequestTimeout: 180 * time.Second, AssumedOutput: 3_000}
}

// Shape is one way of carrying the effort field on the wire.
type Shape struct {
	// Name is the field as the report shows it: "reasoning_effort" or
	// "reasoning.effort".
	Name string
	Wire model.EffortWire
	// Responses marks the ChatGPT Responses body rather than chat completions.
	Responses bool
}

// Label names the shape with its wire, for the plan and the table.
func (s Shape) Label() string {
	if s.Responses {
		return s.Name + " (Responses)"
	}
	return s.Name
}

var (
	shapeTop       = Shape{Name: "reasoning_effort", Wire: model.EffortTopLevel}
	shapeObject    = Shape{Name: "reasoning.effort", Wire: model.EffortObject}
	shapeResponses = Shape{Name: "reasoning.effort", Wire: model.EffortObject, Responses: true}
)

// documentedShapes is the provider support matrix of
// specs/model-effort/context.md: the shape each provider documents. A
// provider missing here (the relays, or a row added later) documents none
// and is probed with both chat-completions shapes. Claude is listed with the
// OpenAI-compatible endpoint's documented field, which that endpoint
// ignores; its native output_config shape arrives with claude-native.
var documentedShapes = map[string]Shape{
	"openai": shapeTop, "gemini": shapeTop, "xai": shapeTop, "deepseek": shapeTop,
	"groq": shapeTop, "cerebras": shapeTop, "together": shapeTop, "mistral": shapeTop,
	"bedrock": shapeTop, "claude": shapeTop,
	"openrouter": shapeObject,
	"chatgpt":    shapeResponses,
}

// claudeCompatNote is printed first on claude while it uses the
// OpenAI-compatible endpoint (spec point 5).
const claudeCompatNote = "Anthropic's OpenAI-compatible endpoint ignores effort; expect ignored-suspected."

// Level is one effort level the plan sends.
type Level struct {
	Name string `json:"name"`
	// Published is false for a --probe-levels name the model does not
	// publish; such a level never counts toward eligibility.
	Published bool `json:"published"`
}

// Level sources, as the plan and report name them.
const (
	SourceLive    = "live model list"
	SourceCatalog = "catalog"
	SourceFlag    = "--probe-levels"
)

// Options are the user's choices for one probe.
type Options struct {
	// Wire is "", "top", "object" or "both" (--probe-wire). Empty probes
	// the documented shape, or both when none is documented.
	Wire string
	// Levels replaces the published list (--probe-levels).
	Levels []string
	// Endpoint is the base URL the client talks to.
	Endpoint string
	// LikhaVersion is recorded in the report.
	LikhaVersion string
	// Secrets are credentials removed from any error text the probe prints
	// or saves (API key, OAuth tokens). They are never written anywhere.
	Secrets []string
}

// Plan is everything decided before the first model request.
type Plan struct {
	Provider         model.Provider
	Model            string
	Endpoint         string // scheme and host only
	EndpointOverride bool
	Shapes           []Shape
	// Documented is the provider's documented shape among Shapes, if any;
	// it wins when more than one shape is eligible (spec point 20).
	Documented   *Shape
	Levels       []Level
	LevelSource  string
	Caps         Caps
	Notes        []string
	LikhaVersion string

	secrets []string
}

// ErrNoLevels reports a model with nothing to probe (spec point 4).
var ErrNoLevels = errors.New("no effort levels to probe")

// NewPlan applies spec points 3–5 and decisions 3, 8, 13 and 16. live is the
// provider's live level list for modelID, if it publishes one.
func NewPlan(p model.Provider, modelID string, live []string, opts Options) (Plan, error) {
	modelID = strings.TrimSpace(modelID)
	if strings.TrimSpace(p.Name) == "" {
		return Plan{}, errors.New("a named provider is required; custom endpoints cannot be probed")
	}
	if modelID == "" {
		return Plan{}, errors.New("a model is required")
	}
	shapes, documented, err := planShapes(p.Name, opts.Wire)
	if err != nil {
		return Plan{}, err
	}
	levels, source, err := planLevels(p, modelID, live, opts.Levels)
	if err != nil {
		return Plan{}, err
	}
	endpoint, override, err := planEndpoint(p, opts.Endpoint)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Provider: p, Model: modelID, Endpoint: endpoint, EndpointOverride: override,
		Shapes: shapes, Documented: documented, Levels: levels, LevelSource: source,
		Caps: DefaultCaps(), LikhaVersion: opts.LikhaVersion, secrets: nonEmpty(opts.Secrets),
	}
	if p.Name == "claude" {
		plan.Notes = append(plan.Notes, claudeCompatNote)
	}
	if n := len(plan.Requests()); n > plan.Caps.MaxRequests {
		return Plan{}, fmt.Errorf("the plan needs %d requests, above the cap of %d; probe fewer levels or one wire shape", n, plan.Caps.MaxRequests)
	}
	return plan, nil
}

// planShapes picks the shapes to probe (spec decision 3).
func planShapes(provider, wire string) ([]Shape, *Shape, error) {
	documented, known := documentedShapes[provider]
	if documented.Responses {
		if wire != "" {
			return nil, nil, fmt.Errorf("--probe-wire does not apply to %s: it has one shape, Responses reasoning.effort", provider)
		}
		return []Shape{documented}, &documented, nil
	}
	var doc *Shape
	if known {
		doc = &documented
	}
	switch wire {
	case "":
		if known {
			return []Shape{documented}, doc, nil
		}
		return []Shape{shapeTop, shapeObject}, nil, nil
	case "top":
		return []Shape{shapeTop}, matchShape(doc, shapeTop), nil
	case "object":
		return []Shape{shapeObject}, matchShape(doc, shapeObject), nil
	case "both":
		return []Shape{shapeTop, shapeObject}, doc, nil
	default:
		return nil, nil, fmt.Errorf("--probe-wire must be top, object or both, not %q", wire)
	}
}

func matchShape(doc *Shape, s Shape) *Shape {
	if doc != nil && *doc == s {
		return doc
	}
	return nil
}

// planLevels resolves the levels as /models does, with the effort wire
// forced on for a local copy of the provider row only (tasks T3).
func planLevels(p model.Provider, modelID string, live, requested []string) ([]Level, string, error) {
	probeRow := p
	probeRow.Effort = model.EffortTopLevel // any carrying wire; the row itself is untouched
	published := model.EffortLevels(probeRow, modelID, live)
	if len(requested) == 0 {
		if len(published) == 0 {
			return nil, "", fmt.Errorf("%w: %s publishes no effort levels on %s; nothing to probe (use --probe-levels to test candidate levels)", ErrNoLevels, modelID, p.Name)
		}
		source := SourceCatalog
		if len(live) > 0 {
			source = SourceLive
		}
		levels := make([]Level, 0, len(published))
		for _, name := range published {
			levels = append(levels, Level{Name: name, Published: true})
		}
		return levels, source, nil
	}
	if err := catalog.ValidateEfforts(requested); err != nil {
		return nil, "", fmt.Errorf("--probe-levels: %w", err)
	}
	if slices.Contains(requested, ControlLevel) {
		return nil, "", fmt.Errorf("--probe-levels: %q is the probe's own control level", ControlLevel)
	}
	// Published levels keep the model's order; unpublished ones follow in
	// the order given (spec point 8).
	var levels []Level
	for _, name := range published {
		if slices.Contains(requested, name) {
			levels = append(levels, Level{Name: name, Published: true})
		}
	}
	for _, name := range requested {
		if !slices.Contains(published, name) {
			levels = append(levels, Level{Name: name})
		}
	}
	return levels, SourceFlag, nil
}

// planEndpoint reduces the endpoint to scheme and host, and says whether it
// differs from the provider's own (decision 16).
func planEndpoint(p model.Provider, endpoint string) (string, bool, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = p.BaseURL
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false, fmt.Errorf("probe endpoint %q is not an absolute URL", endpoint)
	}
	override := strings.TrimRight(endpoint, "/") != strings.TrimRight(p.BaseURL, "/")
	return u.Scheme + "://" + u.Host, override, nil
}

func nonEmpty(values []string) []string {
	var out []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// RequestKind says what a planned request tests.
type RequestKind string

// Request kinds, in the order a shape sends them.
const (
	KindDefault RequestKind = "default"
	KindLevel   RequestKind = "level"
	KindControl RequestKind = "control"
)

// Request is one planned model request.
type Request struct {
	Kind      RequestKind
	Shape     Shape  // zero at Default
	Level     string // "" at Default
	Published bool
}

// Requests returns the ordered request list (spec point 8): Default once,
// then per shape each level in plan order followed by the control.
func (p Plan) Requests() []Request {
	requests := []Request{{Kind: KindDefault}}
	for _, shape := range p.Shapes {
		for _, level := range p.Levels {
			requests = append(requests, Request{Kind: KindLevel, Shape: shape, Level: level.Name, Published: level.Published})
		}
		requests = append(requests, Request{Kind: KindControl, Shape: shape, Level: ControlLevel})
	}
	return requests
}

// IsSubscription reports whether the provider bills a plan, not tokens.
func (p Plan) IsSubscription() bool { return model.IsSubscription(p.Provider.Name) }
