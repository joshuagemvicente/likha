package providers

import (
	"fmt"
	"os"
	"strings"

	"likha/internal/mcp"
	"likha/internal/model"
)

// connection is the provider identity and startup health the UI displays.
type Connection struct {
	Provider               string                      // display name, e.g. "OpenRouter" or "Local OpenAI-compatible"
	ProviderCanonical      string                      // canonical provider name ("chatgpt", "openai", ...); "" for custom endpoints and setup
	Verified               bool                        // the endpoint matches the predefined accepted list
	Err                    error                       // startup connection-check result, nil when connected
	Setup                  bool                        // no provider configured; the TUI runs first-run setup
	Theme                  string                      // resolved theme name (env, flag, or stored config)
	ComposerStyle          string                      // stored composer preference; UI resolves unknown values
	StatusLine             StoredStatusLineConfig      // optional status segments loaded from config.json
	ContextWindows         map[string]int64            // positive provider-reported windows for the active provider
	ContextWindowOverrides map[string]map[string]int64 // user overrides by canonical provider and model ID
	Nerd                   bool                        // user opted into Nerd Font markers
	Mcp                    *mcp.McpManager
}

// resolvedProvider is the concrete provider configuration after flag,
// environment, and stored-credential resolution.
type ResolvedProvider struct {
	Endpoint string
	Verified bool
	Display  string
	Key      string
	Creds    model.OAuthCredentials // set when oauth is true
	OAuth    bool
}

// resolveProvider turns the --provider/--endpoint/--api-key flags and
// environment into a concrete endpoint, key, and verified status. Resolution
// order for hosted keys: the --api-key flag (which includes LIKHA_API_KEY), the
// provider's own LIKHA_<NAME>_API_KEY, then the private state-directory store.
// Keys are never read from the selected repository. An explicitly passed key is
// stored when persistKey is set, so the next run needs no flag. OAuth
// providers have no API key at all: they resolve to a stored login
// (providers.json), which must be created beforehand with --device-login or an
// interactive browser sign-in.
func ResolveProvider(providerName, endpointFlag, apiKeyFlag string, persistKey bool, stateDir string) (ResolvedProvider, error) {
	endpoint := strings.TrimSpace(endpointFlag)
	name := strings.TrimSpace(providerName)
	if name == "" {
		// No provider named: only an explicit endpoint gives anything to talk
		// to, and it runs as unverified. Likha ships no default local server.
		if endpoint == "" {
			endpoint = os.Getenv("LIKHA_ENDPOINT")
		}
		if endpoint == "" {
			return ResolvedProvider{}, fmt.Errorf("no provider configured; choose one with --provider (interactive runs offer the first-run setup)")
		}
		return ResolvedProvider{Endpoint: endpoint, Display: "Custom endpoint"}, nil
	}
	p, ok := model.LookupProvider(name)
	if !ok {
		accepted := make([]string, 0, len(model.Providers))
		for _, candidate := range model.Providers {
			accepted = append(accepted, candidate.Name)
		}
		return ResolvedProvider{}, fmt.Errorf("unknown provider %q; accepted providers: %s", name, strings.Join(accepted, ", "))
	}
	if p.Auth == model.AuthOAuth {
		// OAuth providers are hosted but keyless: no --api-key, env variable,
		// or stored key applies. The credential is the OAuth login in the
		// private state directory.
		if endpoint == "" {
			endpoint = p.BaseURL
		}
		creds, ok, err := StoredOAuth(stateDir, p.Name)
		if err != nil {
			return ResolvedProvider{}, err
		}
		if !ok {
			return ResolvedProvider{}, fmt.Errorf("provider %s uses ChatGPT login; run likha in an interactive terminal to sign in, or run likha --provider %s --device-login", p.Name, p.Name)
		}
		return ResolvedProvider{Endpoint: endpoint, Verified: true, Display: p.DisplayName, Creds: creds, OAuth: true}, nil
	}
	if p.Hosted {
		key := apiKeyFlag
		if key == "" && p.KeyEnv != "" {
			key = os.Getenv(p.KeyEnv)
		}
		if key == "" {
			stored, err := StoredKey(stateDir, p.Name)
			if err != nil {
				return ResolvedProvider{}, err
			}
			key = stored
		}
		if key == "" {
			return ResolvedProvider{}, fmt.Errorf("provider %s requires an API key; set --api-key, LIKHA_API_KEY, or %s, or pass --api-key once to store it", p.Name, p.KeyEnv)
		}
		if persistKey {
			if err := StoreKey(stateDir, p.Name, key); err != nil {
				return ResolvedProvider{}, err
			}
		}
		if endpoint == "" {
			endpoint = p.BaseURL
		}
		return ResolvedProvider{Endpoint: endpoint, Verified: true, Display: p.DisplayName, Key: key}, nil
	}
	if endpoint == "" {
		endpoint = p.BaseURL
	}
	return ResolvedProvider{Endpoint: endpoint, Verified: true, Display: p.DisplayName}, nil
}
