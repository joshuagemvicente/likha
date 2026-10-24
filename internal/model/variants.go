package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"likha/internal/model/catalog"
)

// Selection identifies work without retaining credentials or request overlays.
type Selection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Variant  string `json:"variant,omitempty"`
}

// String formats the canonical provider/model#variant reference.
func (s Selection) String() string {
	ref := s.Provider + "/" + s.Model
	if s.Variant != "" && s.Variant != "default" {
		ref += "#" + s.Variant
	}
	return ref
}

// ParseModelReference parses a qualified reference; slashes inside model IDs survive.
func ParseModelReference(ref string) (Selection, error) {
	provider, modelID, ok := strings.Cut(ref, "/")
	if !ok || provider == "" || strings.ContainsAny(provider, "# \t\r\n") {
		return Selection{}, errors.New("model reference must be provider/model[#variant]")
	}
	id, variant, err := SplitModelVariant(modelID)
	if err != nil {
		return Selection{}, err
	}
	selected := Selection{Provider: provider, Model: id}
	if variant != nil {
		selected.Variant = *variant
	}
	return selected, nil
}

// SplitModelVariant preserves bare IDs and distinguishes no suffix from #default.
func SplitModelVariant(ref string) (string, *string, error) {
	id, choice, present := strings.Cut(ref, "#")
	if id == "" || strings.TrimSpace(id) != id || strings.ContainsAny(id, "\r\n\t") {
		return "", nil, errors.New("model ID is required and must not contain surrounding whitespace")
	}
	if !present {
		return id, nil, nil
	}
	if choice == "" || strings.ContainsAny(choice, "#/ \t\r\n") {
		return "", nil, errors.New("variant suffix must be one non-empty ID")
	}
	if choice == "default" {
		choice = ""
	}
	return id, &choice, nil
}

// Overlay holds logical settings and literal request additions. Values are copied
// before resolution; callers must never mutate a retained execution overlay.
type Overlay struct {
	Settings map[string]any    `json:"settings,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     map[string]any    `json:"body,omitempty"`
}

// Variant is one ordered, model-specific request preset. Default is represented
// by no selected ID, not by a Variant named default.
type Variant struct {
	ID string `json:"id"`
	Overlay
	Description string `json:"-"`
}

// CycleVariant preserves definition order and includes an unset/default state.
func CycleVariant(current string, variants []Variant) string {
	if current == "default" {
		current = ""
	}
	for i, variant := range variants {
		if variant.ID == "default" {
			continue
		}
		if current == "" {
			return variant.ID
		}
		if variant.ID == current {
			for _, next := range variants[i+1:] {
				if next.ID != "default" {
					return next.ID
				}
			}
			return ""
		}
	}
	return ""
}

// HasVariant reports exact, case-sensitive named availability.
func HasVariant(variants []Variant, id string) bool {
	return slices.ContainsFunc(variants, func(v Variant) bool { return v.ID == id })
}

// ModelVariants resolves documented controls. Configured definitions replace the
// generated list in their own order, inheriting matching generated overlays.
func ModelVariants(p Provider, modelID string, live *ReasoningCapabilities, presets []Variant) ([]Variant, error) {
	if !variantProviderSupported(p.Name) {
		if len(presets) > 0 {
			return nil, fmt.Errorf("variants are not documented for %s", p.DisplayName)
		}
		return nil, nil
	}
	controls, output, err := reasoningControls(p, modelID, live)
	if err != nil {
		return nil, err
	}
	var toggle, main []Variant
	for _, control := range controls {
		if control.Type == catalog.OptionToggle {
			toggle = toggleVariants(p, modelID, live, controls)
		}
	}
	if values := catalog.EffortLevels(controls); len(values) > 0 {
		for _, value := range values {
			if live != nil && live.Mandatory != nil && *live.Mandatory && value == "none" {
				continue
			}
			main = append(main, effortVariant(p, value))
		}
	} else {
		for _, control := range controls {
			if control.Type == catalog.OptionBudgetTokens {
				main = budgetVariants(p, control, output)
				break
			}
		}
		if len(main) == 0 {
			main = toggle
		}
	}
	var variants []Variant
	for _, variant := range append(slices.Clone(toggle), main...) {
		if len(main) > 0 && len(toggle) > 0 && variant.ID == "thinking" && !slices.ContainsFunc(main, func(v Variant) bool { return v.ID == "thinking" }) {
			continue
		}
		if !HasVariant(variants, variant.ID) {
			variants = append(variants, variant)
		}
	}
	seen := make(map[string]bool)
	var configured []Variant
	wire := wireForModel(p.BaseURL, modelID)
	if p.Auth == AuthOAuth {
		wire = WireResponses
	}
	for _, preset := range presets {
		if preset.ID == "" || preset.ID == "default" || strings.ContainsAny(preset.ID, "#/ \t\r\n") || seen[preset.ID] {
			return nil, errors.New("variant definitions require unique non-reserved IDs")
		}
		seen[preset.ID] = true
		if err := ValidateOverlay(preset.Overlay); err != nil {
			return nil, fmt.Errorf("variant %s: %w", preset.ID, err)
		}
		idx := slices.IndexFunc(variants, func(v Variant) bool { return v.ID == preset.ID })
		if idx < 0 {
			preset.Overlay = MergeOverlay(Overlay{}, preset.Overlay)
			configured = append(configured, preset)
			continue
		}
		body, err := resolveVariantLayers(p, modelID, wire, live, variants[idx].Overlay, preset.Overlay)
		if err != nil {
			return nil, fmt.Errorf("variant %s: %w", preset.ID, err)
		}
		preset.Overlay = Overlay{Body: body, Headers: MergeOverlay(variants[idx].Overlay, preset.Overlay).Headers}
		if preset.Description == "" {
			preset.Description = variants[idx].Description
		}
		configured = append(configured, preset)
	}
	if len(presets) > 0 {
		variants = configured
	}
	available := make([]Variant, 0, len(variants))
	for _, variant := range variants {
		if _, err := lowerVariantOverlay(p, modelID, wire, live, variant.Overlay); err != nil {
			if len(presets) > 0 {
				return nil, fmt.Errorf("variant %s: %w", variant.ID, err)
			}
			// A generated control the model's metadata cannot carry is simply
			// not offered; failing the list would also make Default unusable.
			continue
		}
		available = append(available, variant)
	}
	return available, nil
}

func variantProviderSupported(name string) bool {
	switch name {
	case "openai", "chatgpt", "claude", "openrouter", "gemini", "deepseek", "xai", "groq", "cerebras", "together", "mistral":
		return true
	default:
		return false
	}
}

func effortVariant(p Provider, level string) Variant {
	key := "reasoningEffort"
	if p.Name == "claude" {
		key = "effort"
	}
	settings := map[string]any{key: level}
	if p.Name == "deepseek" {
		settings["thinking"] = map[string]any{"type": "enabled"}
	}
	return Variant{ID: level, Overlay: Overlay{Settings: settings}, Description: "reasoning effort " + level}
}

func toggleVariants(p Provider, id string, live *ReasoningCapabilities, controls []catalog.ReasoningOption) []Variant {
	var off, on map[string]any
	switch p.Name {
	case "claude", "deepseek":
		onType := "enabled"
		if p.Name == "claude" {
			onType = "adaptive"
		}
		off = map[string]any{"thinking": map[string]any{"type": "disabled"}}
		on = map[string]any{"thinking": map[string]any{"type": onType}}
	case "openrouter":
		off = map[string]any{"reasoning": map[string]any{"enabled": false}}
		on = map[string]any{"reasoning": map[string]any{"enabled": true}}
	case "gemini":
		off = map[string]any{"thinkingConfig": map[string]any{"thinkingBudget": int64(0)}}
		// Gemini's "on" is the dynamic budget (-1), which only a model that
		// publishes budget_tokens accepts; a toggle-only row gets no on-variant.
		if slices.ContainsFunc(controls, func(v catalog.ReasoningOption) bool { return v.Type == catalog.OptionBudgetTokens }) {
			on = map[string]any{"thinkingConfig": map[string]any{"thinkingBudget": int64(-1)}}
		}
	default:
		return nil
	}
	choices := []Variant{{ID: "none", Overlay: Overlay{Settings: off}, Description: "disable optional thinking"}}
	if on == nil || p.Name == "claude" && !supportsAdaptiveThinking(id, live) {
		return choices
	}
	return append(choices, Variant{ID: "thinking", Overlay: Overlay{Settings: on}, Description: "enable thinking"})
}

func budgetVariants(p Provider, option catalog.ReasoningOption, output int64) []Variant {
	if output <= 1 {
		return nil
	}
	maximum := output - 1
	if p.Name == "claude" {
		maximum = min(maximum, int64(31_999))
	}
	if option.Max != nil {
		maximum = min(maximum, *option.Max)
	}
	minimum := int64(0)
	if option.Min != nil {
		minimum = *option.Min
	}
	if p.Name == "claude" {
		minimum = max(minimum, int64(1024))
	}
	if maximum <= 0 || minimum > maximum {
		return nil
	}
	var variants []Variant
	for _, budget := range []struct {
		id     string
		tokens int64
	}{{"high", max(minimum, (maximum+1)/2)}, {"max", maximum}} {
		var settings map[string]any
		switch p.Name {
		case "claude":
			settings = map[string]any{"thinking": map[string]any{"type": "enabled", "budgetTokens": budget.tokens}}
		case "gemini":
			settings = map[string]any{"thinkingConfig": map[string]any{"thinkingBudget": budget.tokens}}
		case "openrouter":
			settings = map[string]any{"reasoning": map[string]any{"max_tokens": budget.tokens}}
		default:
			return nil
		}
		variants = append(variants, Variant{ID: budget.id, Overlay: Overlay{Settings: settings}, Description: fmt.Sprintf("thinking budget %d tokens", budget.tokens)})
	}
	return variants
}
