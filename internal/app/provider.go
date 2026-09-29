package app

import (
	"fmt"
	"os"
	"strings"

	"lisa/internal/model"
)

// connection is the provider identity and startup health the UI displays.
type connection struct {
	provider string // display name, e.g. "OpenRouter" or "Local OpenAI-compatible"
	verified bool   // the endpoint matches the predefined accepted list
	err      error  // startup connection-check result, nil when connected
	setup    bool   // no provider configured; the TUI runs first-run setup
}

// resolveProvider turns the --provider/--endpoint/--api-key flags and
// environment into a concrete endpoint, key, and verified status. Resolution
// order for hosted keys: the --api-key flag (which includes LISA_API_KEY), the
// provider's own LISA_<NAME>_API_KEY, then the private state-directory store.
// Keys are never read from the selected repository. An explicitly passed key is
// stored when persistKey is set, so the next run needs no flag.
func resolveProvider(providerName, endpointFlag, apiKeyFlag string, persistKey bool, stateDir string) (endpoint string, verified bool, display string, key string, err error) {
	endpoint = strings.TrimSpace(endpointFlag)
	name := strings.TrimSpace(providerName)
	if name == "" {
		// No provider named: only an explicit endpoint gives anything to talk
		// to, and it runs as unverified. Lisa ships no default local server.
		if endpoint == "" {
			endpoint = os.Getenv("LISA_ENDPOINT")
		}
		if endpoint == "" {
			return "", false, "", "", fmt.Errorf("no provider configured; choose one with --provider (interactive runs offer the first-run setup)")
		}
		return endpoint, false, "Custom endpoint", "", nil
	}
	p, ok := model.LookupProvider(name)
	if !ok {
		accepted := make([]string, 0, len(model.Providers))
		for _, candidate := range model.Providers {
			accepted = append(accepted, candidate.Name)
		}
		return "", false, "", "", fmt.Errorf("unknown provider %q; accepted providers: %s", name, strings.Join(accepted, ", "))
	}
	if p.Hosted {
		key = apiKeyFlag
		if key == "" && p.KeyEnv != "" {
			key = os.Getenv(p.KeyEnv)
		}
		if key == "" {
			key, err = storedKey(stateDir, p.Name)
			if err != nil {
				return "", false, "", "", err
			}
		}
		if key == "" {
			return "", false, "", "", fmt.Errorf("provider %s requires an API key; set --api-key, LISA_API_KEY, or %s, or pass --api-key once to store it", p.Name, p.KeyEnv)
		}
		if persistKey {
			if err := storeKey(stateDir, p.Name, key); err != nil {
				return "", false, "", "", err
			}
		}
	}
	if endpoint == "" {
		endpoint = p.BaseURL
	}
	return endpoint, true, p.DisplayName, key, nil
}
