package providers

import (
	"context"
	"fmt"
	"strings"

	"likha/internal/model"
)

// ApplyVariant resolves one exact pair and validates before changing the client.
// Explicit choices fail on unknown IDs; remembered stale choices are noticed.
// Valid legacy effort preferences migrate without overwriting a newer choice.
func ApplyVariant(ctx context.Context, stateDir string, client *model.Client, p model.Provider, live *model.ReasoningCapabilities, explicit *string) (string, error) {
	cfg, err := LoadStoredConfig(stateDir)
	if err != nil {
		return "", err
	}
	config := cfg.VariantConfig(p.Name, client.Model())
	var choices []model.Variant
	if client.SupportsVariants(p) {
		choices, err = client.Variants(p, live, config)
		if err != nil {
			return "", err
		}
	}
	choice, exists := cfg.StoredVariant(p.Name, client.Model())
	legacy := false
	if !exists && explicit == nil {
		choice = cfg.StoredEffort(p.Name, client.Model())
		legacy = choice != "" && model.HasVariant(choices, choice)
		if choice == "" {
			choice = cfg.DefaultVariants[p.Name][client.Model()]
			if choice == "default" {
				choice = ""
			}
			if choice != "" && !model.HasVariant(choices, choice) {
				return "", fmt.Errorf("configured default variant %s is not available for %s/%s", choice, p.Name, client.Model())
			}
		}
	}
	notice := liveMetadataNotice(p, client.Model(), live)
	if explicit != nil {
		choice = *explicit
	} else if choice != "" && !model.HasVariant(choices, choice) {
		notice = joinNotices(notice, fmt.Sprintf("Saved variant %s is not offered for %s/%s; using Default.", choice, p.Name, client.Model()))
		choice = ""
	}
	if err := client.SetVariant(p, live, config, choice); err != nil {
		return notice, err
	}
	if legacy && explicit == nil {
		if err := UpdateStoredConfig(ctx, stateDir, func(latest *StoredProviderConfig) error {
			if _, ok := latest.StoredVariant(p.Name, client.Model()); !ok {
				latest.SetStoredVariant(p.Name, client.Model(), choice)
			}
			return nil
		}); err != nil {
			return notice, fmt.Errorf("variant is usable but migration was not saved: %w", err)
		}
	}
	return notice, nil
}

// ApplySavedVariant restores session identity, with noticed stale fallback.
func ApplySavedVariant(ctx context.Context, stateDir string, client *model.Client, p model.Provider, live *model.ReasoningCapabilities, choice string) (string, error) {
	cfg, err := LoadStoredConfig(stateDir)
	if err != nil {
		return "", err
	}
	var choices []model.Variant
	if client.SupportsVariants(p) {
		choices, err = client.Variants(p, live, cfg.VariantConfig(p.Name, client.Model()))
	}
	if err != nil {
		return "", err
	}
	notice := liveMetadataNotice(p, client.Model(), live)
	if choice != "" && !model.HasVariant(choices, choice) {
		notice = joinNotices(notice, fmt.Sprintf("Saved variant %s is not offered for %s/%s; using Default.", choice, p.Name, client.Model()))
		choice = ""
	}
	return notice, client.SetVariant(p, live, cfg.VariantConfig(p.Name, client.Model()), choice)
}

// liveMetadataNotice tells the user that the provider's live reasoning
// metadata was malformed and ignored, so the variants offered come from the
// bundled catalog rather than from what the provider reported.
func liveMetadataNotice(p model.Provider, modelID string, live *model.ReasoningCapabilities) string {
	if live == nil || live.Invalid == "" {
		return ""
	}
	return fmt.Sprintf("Ignored malformed reasoning metadata from %s for %s (%s); using documented controls.", p.Name, modelID, live.Invalid)
}

func joinNotices(notices ...string) string {
	kept := make([]string, 0, len(notices))
	for _, notice := range notices {
		if notice != "" {
			kept = append(kept, notice)
		}
	}
	return strings.Join(kept, " ")
}
