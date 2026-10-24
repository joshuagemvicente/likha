package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"likha/internal/model/catalog"
)

// VariantConfig supplies the private configuration layers for one exact pair.
type VariantConfig struct {
	Provider Overlay
	Model    Overlay
	Presets  []Variant
}

// Variants returns choices validated against provider/model baseline overlays.
// A configured lower output cap also bounds generated thinking budgets.
func (c *Client) Variants(p Provider, live *ReasoningCapabilities, config VariantConfig) ([]Variant, error) {
	if !c.SupportsVariants(p) {
		return nil, nil
	}
	if err := ValidateOverlay(config.Provider); err != nil {
		return nil, fmt.Errorf("provider options: %w", err)
	}
	if err := ValidateOverlay(config.Model); err != nil {
		return nil, fmt.Errorf("model options: %w", err)
	}
	body, err := resolveVariantLayers(p, c.model, c.wire, live, config.Provider, config.Model)
	if err != nil {
		return nil, fmt.Errorf("baseline model settings: %w", err)
	}
	resolvedLive := boundedVariantCapabilities(live, body)
	variants, err := ModelVariants(p, c.model, resolvedLive, config.Presets)
	if err != nil {
		return nil, err
	}
	available := make([]Variant, 0, len(variants))
	for _, variant := range variants {
		if _, err := resolveVariantLayers(p, c.model, c.wire, live, config.Provider, config.Model, variant.Overlay); err != nil {
			if len(config.Presets) > 0 {
				return nil, fmt.Errorf("variant %s with baseline: %w", variant.ID, err)
			}
			continue // Do not offer a generated control that conflicts with the configured baseline.
		}
		available = append(available, variant)
	}
	return available, nil
}

func boundedVariantCapabilities(live *ReasoningCapabilities, body map[string]any) *ReasoningCapabilities {
	for _, field := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens"} {
		if tokens, ok := integerSetting(body[field]); ok && tokens > 0 {
			resolved := ReasoningCapabilities{}
			if live != nil {
				resolved = *live
			}
			resolved.Output = tokens
			return &resolved
		}
	}
	return live
}

// VariantRejectedError attributes a rejected request without asserting that
// the variant caused the rejection. The original provider error remains matchable.
type VariantRejectedError struct {
	Variant string
	Err     error
}

func (e *VariantRejectedError) Error() string { return e.Err.Error() }
func (e *VariantRejectedError) Unwrap() error { return e.Err }

// SupportsVariants reports whether this client is on the provider's documented
// route. A provider label never certifies an endpoint override or a different auth.
func (c *Client) SupportsVariants(p Provider) bool {
	return c != nil && c.base == p.BaseURL && variantProviderSupported(p.Name) && (c.oauth != nil) == (p.Auth == AuthOAuth)
}

// SetVariant validates and installs an independently copied request configuration.
// Like SetModel, use it only on the composer client or a new, quiescent fork.
func (c *Client) SetVariant(p Provider, live *ReasoningCapabilities, config VariantConfig, id string) error {
	if c == nil {
		return errors.New("set variant: model client is nil")
	}
	if id == "default" {
		id = ""
	}
	if !c.SupportsVariants(p) {
		if id != "" || !emptyOverlay(config.Provider) || !emptyOverlay(config.Model) || len(config.Presets) > 0 {
			return errors.New("variants require a documented provider route and compatible authentication")
		}
		c.variantID, c.variantBody, c.variantHeaders, c.baselineBody, c.baselineHeaders = "", nil, nil, nil, nil
		c.SetEffort(EffortUnsupported, "")
		return nil
	}
	variants, err := c.Variants(p, live, config)
	if err != nil {
		return err
	}
	base := MergeOverlay(config.Provider, config.Model)
	var selected Overlay
	if id != "" {
		index := slices.IndexFunc(variants, func(v Variant) bool { return v.ID == id })
		if index < 0 {
			return fmt.Errorf("variant %s is not available for %s/%s", id, p.Name, c.model)
		}
		selected = variants[index].Overlay
	}
	baselineBody, err := resolveVariantLayers(p, c.model, c.wire, live, config.Provider, config.Model)
	if err != nil {
		return fmt.Errorf("baseline model settings: %w", err)
	}
	body, err := resolveVariantLayers(p, c.model, c.wire, live, config.Provider, config.Model, selected)
	if err != nil {
		return fmt.Errorf("variant request settings: %w", err)
	}
	c.variantID = id
	c.baselineBody, c.baselineHeaders = baselineBody, base.Headers
	c.variantBody, c.variantHeaders = body, MergeOverlay(base, selected).Headers
	if c.nativeAnthropic() {
		c.nativeThinking, c.nativeOutput = anthropicThinking(c.model), anthropicMaxTokens(c.model)
		if live != nil && live.Adaptive != nil {
			c.nativeThinking = thinkingNone
			if *live.Adaptive {
				c.nativeThinking = thinkingAdaptive
			}
		}
		if live != nil && live.Output > 0 {
			c.nativeOutput = live.Output
		}
		c.nativeSnapshot = true
	}
	c.SetEffort(EffortUnsupported, "")
	return nil
}

// Variant returns the chosen named variant; empty means Default.
func (c *Client) Variant() string {
	if c == nil {
		return ""
	}
	return c.variantID
}

// ExecutionKey compares resolved request configuration in memory. It must never
// be logged or persisted: private headers can be part of the encoded key.
func (c *Client) ExecutionKey() string {
	if c == nil {
		return ""
	}
	raw, err := json.Marshal(struct {
		Base, Model, Variant, Effort string
		Body                         map[string]any
		Headers                      map[string]string
		Wire                         ProviderWire
		Replay                       ReplayWire
		NativeThinking               anthropicThinkingMode
		NativeOutput                 int64
	}{c.base, c.model, c.variantID, c.effort, c.variantBody, c.variantHeaders, c.wire, c.replayWire, c.nativeThinking, c.nativeOutput})
	if err != nil {
		return ""
	} // SetVariant validates JSON before admission.
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// WithoutVariant snapshots baseline settings for background work even when
// Default is already selected. No mutable composer configuration is shared.
func (c *Client) WithoutVariant() (*Client, error) {
	child, err := c.Fork()
	if err != nil {
		return nil, fmt.Errorf("baseline model client: %w", err)
	}
	child.variantID = ""
	child.variantBody, child.variantHeaders = child.baselineBody, child.baselineHeaders
	child.SetEffort(EffortUnsupported, "")
	return child, nil
}

func emptyOverlay(overlay Overlay) bool {
	return len(overlay.Settings) == 0 && len(overlay.Body) == 0 && len(overlay.Headers) == 0
}

func lowerVariantOverlay(p Provider, id string, wire ProviderWire, live *ReasoningCapabilities, overlay Overlay) (map[string]any, error) {
	return resolveVariantLayers(p, id, wire, live, overlay)
}

// Lower each layer before merging so a model's literal body control can override
// a provider's logical control (and vice versa). Only the effective body is sent.
func resolveVariantLayers(p Provider, id string, wire ProviderWire, live *ReasoningCapabilities, overlays ...Overlay) (map[string]any, error) {
	body := make(map[string]any)
	for _, overlay := range overlays {
		lowered, err := translateVariantOverlay(p, wire, overlay)
		if err != nil {
			return nil, err
		}
		body = mergeObjects(body, lowered)
	}
	if err := validateVariantBody(p, id, wire, live, body); err != nil {
		return nil, err
	}
	return body, nil
}

func translateVariantOverlay(p Provider, wire ProviderWire, overlay Overlay) (map[string]any, error) {
	if err := ValidateOverlay(overlay); err != nil {
		return nil, err
	}
	body := mergeObjects(nil, overlay.Body)
	if _, effort := overlay.Settings["effort"]; effort {
		if _, reasoningEffort := overlay.Settings["reasoningEffort"]; reasoningEffort {
			return nil, errors.New("use one logical effort setting, not both aliases")
		}
	}
	// A stable order also defines precedence between a compound logical setting
	// and a specific setting for the same wire field.
	keys := make([]string, 0, len(overlay.Settings))
	for key := range overlay.Settings {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		value := overlay.Settings[key]
		switch key {
		case "reasoningEffort", "effort":
			level, ok := value.(string)
			if !ok || level == "" {
				return nil, errors.New("effort must be a non-empty string")
			}
			switch {
			case p.Name == "claude":
				body = mergeObjects(body, map[string]any{"output_config": map[string]any{"effort": level}})
			case p.Name == "openrouter" || wire == WireResponses:
				body = mergeObjects(body, map[string]any{"reasoning": map[string]any{"effort": level}})
			default:
				body["reasoning_effort"] = level
			}
		case "thinking":
			thinking, ok := value.(map[string]any)
			if !ok || (p.Name != "claude" && p.Name != "deepseek") {
				return nil, errors.New("thinking is unsupported on this protocol or is not an object")
			}
			lowered := mergeObjects(nil, thinking)
			if tokens, ok := lowered["budgetTokens"]; ok {
				delete(lowered, "budgetTokens")
				lowered["budget_tokens"] = tokens
			}
			body["thinking"] = lowered
		case "thinkingConfig":
			thinking, ok := value.(map[string]any)
			if !ok || p.Name != "gemini" {
				return nil, errors.New("thinkingConfig requires Gemini compatibility and an object")
			}
			lowered := make(map[string]any)
			for key, value := range thinking {
				switch key {
				case "thinkingBudget":
					lowered["thinking_budget"] = value
				case "thinkingLevel":
					lowered["thinking_level"] = value
				case "includeThoughts":
					lowered["include_thoughts"] = value
				default:
					return nil, fmt.Errorf("unsupported thinkingConfig field %s", key)
				}
			}
			body = mergeObjects(body, map[string]any{"extra_body": map[string]any{"google": map[string]any{"thinking_config": lowered}}})
		case "reasoning":
			value, ok := value.(map[string]any)
			if !ok || p.Name != "openrouter" && wire != WireResponses {
				return nil, errors.New("reasoning object is unsupported on this protocol")
			}
			body = mergeObjects(body, map[string]any{"reasoning": value})
		case "reasoningSummary":
			if wire != WireResponses {
				return nil, errors.New("reasoningSummary requires Responses")
			}
			if value != "auto" && value != "concise" && value != "detailed" {
				return nil, errors.New("invalid reasoningSummary")
			}
			body = mergeObjects(body, map[string]any{"reasoning": map[string]any{"summary": value}})
		case "include":
			if wire != WireResponses {
				return nil, errors.New("include requires Responses")
			}
			if !stringArray(value) {
				return nil, errors.New("include must be an array of strings")
			}
			body["include"] = value
		case "maxOutputTokens":
			tokens, ok := integerSetting(value)
			if !ok || tokens <= 0 {
				return nil, errors.New("maxOutputTokens must be a positive integer")
			}
			field := "max_completion_tokens"
			if p.Name == "claude" {
				field = "max_tokens"
			}
			if wire == WireResponses {
				field = "max_output_tokens"
			}
			body[field] = tokens
		case "verbosity":
			if wire != WireResponses || value != "low" && value != "medium" && value != "high" {
				return nil, errors.New("verbosity requires a supported Responses value")
			}
			body = mergeObjects(body, map[string]any{"text": map[string]any{"verbosity": value}})
		}
	}
	return body, nil
}

func validateVariantBody(p Provider, id string, wire ProviderWire, live *ReasoningCapabilities, body map[string]any) error {
	controls, output, err := reasoningControls(p, id, live)
	if err != nil {
		return err
	}
	efforts := catalog.EffortLevels(controls)
	has := func(kind string) bool {
		return slices.ContainsFunc(controls, func(v catalog.ReasoningOption) bool { return v.Type == kind })
	}
	checkEffort := func(value any) error {
		level, ok := value.(string)
		if !ok || !slices.Contains(efforts, level) || live != nil && live.Mandatory != nil && *live.Mandatory && level == "none" {
			return errors.New("effort is not published for this model")
		}
		return nil
	}
	checkOff := func() error {
		if live != nil && live.Mandatory != nil && *live.Mandatory || !has(catalog.OptionToggle) && !slices.Contains(efforts, "none") {
			return errors.New("this model does not support disabling thinking")
		}
		return nil
	}
	for _, field := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens"} {
		if value, exists := body[field]; exists {
			tokens, ok := integerSetting(value)
			if !ok || tokens <= 0 || output > 0 && tokens > output {
				return errors.New("output token limit is invalid or exceeds the model limit")
			}
			output = tokens
		}
	}
	checkBudget := func(value any, dynamic bool) error {
		tokens, ok := integerSetting(value)
		if dynamic && ok && tokens == -1 {
			if !has(catalog.OptionBudgetTokens) {
				return errors.New("dynamic thinking budget is not published for this model")
			}
			return nil
		}
		if dynamic && ok && tokens == 0 {
			return checkOff()
		}
		if !ok || tokens <= 0 || !has(catalog.OptionBudgetTokens) || output <= 0 || tokens >= output {
			return errors.New("thinking budget is unsupported or does not fit the output limit")
		}
		for _, control := range controls {
			if control.Type == catalog.OptionBudgetTokens && (control.Min != nil && tokens < *control.Min || control.Max != nil && tokens > *control.Max) {
				return errors.New("thinking budget is outside the model's published range")
			}
		}
		if p.Name == "claude" && tokens < 1024 {
			return errors.New("Claude thinking budget must be at least 1024")
		}
		return nil
	}
	if value, ok := body["reasoning_effort"]; ok {
		if wire == WireResponses || p.Name == "claude" || p.Name == "openrouter" {
			return errors.New("reasoning_effort is not the effort field on this protocol")
		}
		if err := checkEffort(value); err != nil {
			return err
		}
	}
	if value, ok := body["output_config"]; ok {
		config, ok := value.(map[string]any)
		if !ok || p.Name != "claude" {
			return errors.New("output_config requires Claude and an object")
		}
		if value, exists := config["effort"]; exists {
			if err := checkEffort(value); err != nil {
				return err
			}
		}
	}
	if value, ok := body["reasoning"]; ok {
		if p.Name != "openrouter" && wire != WireResponses {
			return errors.New("reasoning object is not supported on this protocol")
		}
		reasoning, ok := value.(map[string]any)
		if !ok {
			return errors.New("reasoning must be an object")
		}
		_, effort := reasoning["effort"]
		_, budget := reasoning["max_tokens"]
		if budget && wire == WireResponses {
			return errors.New("reasoning.max_tokens is not a Responses control")
		}
		if effort && budget {
			return errors.New("reasoning effort and budget are mutually exclusive")
		}
		if effort {
			if err := checkEffort(reasoning["effort"]); err != nil {
				return err
			}
		}
		if value, exists := reasoning["summary"]; exists && (wire != WireResponses || value != "auto" && value != "concise" && value != "detailed") {
			return errors.New("invalid Responses reasoning summary")
		}
		if budget {
			if err := checkBudget(reasoning["max_tokens"], false); err != nil {
				return err
			}
		}
		if value, exists := reasoning["enabled"]; exists {
			enabled, ok := value.(bool)
			if !ok || p.Name != "openrouter" {
				return errors.New("reasoning.enabled requires OpenRouter and a boolean")
			}
			if !enabled {
				if budget || effort && reasoning["effort"] != "none" {
					return errors.New("disabled reasoning cannot also request effort or a budget")
				}
				if err := checkOff(); err != nil {
					return err
				}
			}
		}
	}
	if value, ok := body["thinking"]; ok {
		if p.Name != "claude" && p.Name != "deepseek" {
			return errors.New("thinking is not supported on this protocol")
		}
		thinking, ok := value.(map[string]any)
		if !ok {
			return errors.New("thinking must be an object")
		}
		if thinking["type"] != "enabled" {
			if _, budget := thinking["budget_tokens"]; budget {
				return errors.New("thinking budget requires enabled manual thinking")
			}
		}
		switch thinking["type"] {
		case "disabled":
			if _, effort := body["reasoning_effort"]; effort {
				return errors.New("disabled thinking cannot also request reasoning effort")
			}
			if err := checkOff(); err != nil {
				return err
			}
		case "adaptive":
			if p.Name != "claude" || !supportsAdaptiveThinking(id, live) {
				return errors.New("this model does not support adaptive thinking")
			}
		case "enabled":
			if p.Name == "claude" {
				if supportsAdaptiveThinking(id, live) {
					return errors.New("use adaptive thinking rather than a manual budget on this model")
				}
				if err := checkBudget(thinking["budget_tokens"], false); err != nil {
					return err
				}
			} else if p.Name != "deepseek" || !has(catalog.OptionToggle) {
				return errors.New("this model does not support a thinking toggle")
			}
			if p.Name == "deepseek" {
				if _, budget := thinking["budget_tokens"]; budget {
					return errors.New("DeepSeek thinking does not support a manual budget")
				}
			}
		default:
			return errors.New("unknown thinking mode")
		}
	}
	if value, ok := body["extra_body"]; ok && p.Name == "gemini" {
		extra, ok := value.(map[string]any)
		if !ok {
			return errors.New("extra_body must be an object")
		}
		google, googleOK := extra["google"].(map[string]any)
		if _, exists := extra["google"]; exists && !googleOK {
			return errors.New("extra_body.google must be an object")
		}
		thinking, thinkingOK := google["thinking_config"].(map[string]any)
		if _, exists := google["thinking_config"]; exists && !thinkingOK {
			return errors.New("thinking_config must be an object")
		}
		if len(thinking) > 0 {
			_, budget := thinking["thinking_budget"]
			_, level := thinking["thinking_level"]
			if _, effort := body["reasoning_effort"]; effort && (budget || level) {
				return errors.New("Gemini effort and thinking configuration are mutually exclusive")
			}
			if budget && level {
				return errors.New("Gemini budget and thinking level are mutually exclusive")
			}
			if value, exists := thinking["include_thoughts"]; exists {
				if _, ok := value.(bool); !ok {
					return errors.New("include_thoughts must be a boolean")
				}
			}
			if budget {
				if err := checkBudget(thinking["thinking_budget"], true); err != nil {
					return err
				}
			}
			if level {
				if err := checkEffort(thinking["thinking_level"]); err != nil {
					return err
				}
			}
		}
	}
	if value, exists := body["include"]; exists && (wire != WireResponses || !stringArray(value)) {
		return errors.New("include requires Responses and an array of strings")
	}
	return nil
}

func stringArray(value any) bool {
	switch items := value.(type) {
	case []string:
		return true
	case []any:
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}

func supportsAdaptiveThinking(id string, live *ReasoningCapabilities) bool {
	if live != nil && live.Adaptive != nil {
		return *live.Adaptive
	}
	return anthropicThinking(id) == thinkingAdaptive
}

func (c *Client) variantPayload(payload []byte) ([]byte, error) {
	if len(c.variantBody) == 0 {
		return payload, nil
	}
	// UseNumber keeps every numeric literal exactly as encoded: a float64
	// round trip would round large integers and rewrite 1.0 as 1.
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("decode model request for variant: %w", err)
	}
	// Thinking modes have mutually exclusive fields; changing type must not
	// retain adaptive-only fields in a manual/disabled configuration.
	if thinking, exists := c.variantBody["thinking"].(map[string]any); exists && thinking["type"] != "adaptive" {
		delete(body, "thinking")
	}
	body = mergeObjects(body, c.variantBody)
	if c.wire == WireResponses && c.replayWire == ReplayResponsesItems {
		// Stateless reasoning tool loops require encrypted replay regardless of
		// optional include fields a preset requests.
		include, _ := body["include"].([]any)
		if typed, ok := body["include"].([]string); ok {
			for _, item := range typed {
				include = append(include, item)
			}
		}
		if !slices.ContainsFunc(include, func(value any) bool { return value == "reasoning.encrypted_content" }) {
			include = append(include, "reasoning.encrypted_content")
		}
		body["include"] = include
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode variant request: %w", err)
	}
	return encoded, nil
}

func (c *Client) rejectedVariant(status int, err error) error {
	if c.variantID != "" && effortRejectedStatus(status) {
		return &VariantRejectedError{Variant: c.variantID, Err: err}
	}
	return err
}
