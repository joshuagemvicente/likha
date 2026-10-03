package providers

import (
	"likha/internal/model"
)

// SwitchModelID resolves the model a switch lands on: the provider's
// documented default, or the first model the check reported when the
// provider defines no default. Model names never carry over across
// providers (spec §9.2: no guessed ID compatibility).
func SwitchModelID(p model.Provider, reported []string) string {
	if p.DefaultModel != "" {
		return p.DefaultModel
	}
	if len(reported) > 0 {
		return reported[0]
	}
	return ""
}

// CustomEndpointTarget builds the provider record for an unlisted
// OpenAI-compatible endpoint. Reserved for the hidden custom-endpoint
// source: it flows through applyProviderDirect and activateProvider exactly
// like a predefined row, so enabling it later is a one-line gate change.
func CustomEndpointTarget(endpoint string) model.Provider {
	return model.Provider{Name: "custom", DisplayName: "Custom endpoint", BaseURL: endpoint}
}
