package app

import (
	"fmt"
	"os"
	"strings"

	"lisa/internal/model"
)

// connection is the provider identity and startup health the UI displays.
type connection struct {
	provider          string                 // display name, e.g. "OpenRouter" or "Local OpenAI-compatible"
	providerCanonical string                 // canonical provider name ("chatgpt", "openai", ...); "" for custom endpoints and setup
	verified          bool                   // the endpoint matches the predefined accepted list
	err               error                  // startup connection-check result, nil when connected
	setup             bool                   // no provider configured; the TUI runs first-run setup
	theme             string                 // resolved theme name (env, flag, or stored config)
	composerStyle     string                 // stored composer preference; UI resolves unknown values
	statusLine        storedStatusLineConfig // optional status segments loaded from config.json
	nerd              bool                   // user opted into Nerd Font markers
	mcp               *mcpManager
}

// resolvedProvider is the concrete provider configuration after flag,
// environment, and stored-credential resolution.
type resolvedProvider struct {
	endpoint string
	verified bool
	display  string
	key      string
	creds    model.OAuthCredentials // set when oauth is true
	oauth    bool
}

// resolveProvider turns the --provider/--endpoint/--api-key flags and
// environment into a concrete endpoint, key, and verified status. Resolution
// order for hosted keys: the --api-key flag (which includes LISA_API_KEY), the
// provider's own LISA_<NAME>_API_KEY, then the private state-directory store.
// Keys are never read from the selected repository. An explicitly passed key is
// stored when persistKey is set, so the next run needs no flag. OAuth
// providers have no API key at all: they resolve to a stored login
// (providers.json), which must be created beforehand with --device-login or an
// interactive browser sign-in.
func resolveProvider(providerName, endpointFlag, apiKeyFlag string, persistKey bool, stateDir string) (resolvedProvider, error) {
	endpoint := strings.TrimSpace(endpointFlag)
	name := strings.TrimSpace(providerName)
	if name == "" {
		// No provider named: only an explicit endpoint gives anything to talk
		// to, and it runs as unverified. Lisa ships no default local server.
		if endpoint == "" {
			endpoint = os.Getenv("LISA_ENDPOINT")
		}
		if endpoint == "" {
			return resolvedProvider{}, fmt.Errorf("no provider configured; choose one with --provider (interactive runs offer the first-run setup)")
		}
		return resolvedProvider{endpoint: endpoint, display: "Custom endpoint"}, nil
	}
	p, ok := model.LookupProvider(name)
	if !ok {
		accepted := make([]string, 0, len(model.Providers))
		for _, candidate := range model.Providers {
			accepted = append(accepted, candidate.Name)
		}
		return resolvedProvider{}, fmt.Errorf("unknown provider %q; accepted providers: %s", name, strings.Join(accepted, ", "))
	}
	if p.Auth == model.AuthOAuth {
		// OAuth providers are hosted but keyless: no --api-key, env variable,
		// or stored key applies. The credential is the OAuth login in the
		// private state directory.
		if endpoint == "" {
			endpoint = p.BaseURL
		}
		creds, ok, err := storedOAuth(stateDir, p.Name)
		if err != nil {
			return resolvedProvider{}, err
		}
		if !ok {
			return resolvedProvider{}, fmt.Errorf("provider %s uses ChatGPT login; run lisa in an interactive terminal to sign in, or run lisa --provider %s --device-login", p.Name, p.Name)
		}
		return resolvedProvider{endpoint: endpoint, verified: true, display: p.DisplayName, creds: creds, oauth: true}, nil
	}
	if p.Hosted {
		key := apiKeyFlag
		if key == "" && p.KeyEnv != "" {
			key = os.Getenv(p.KeyEnv)
		}
		if key == "" {
			stored, err := storedKey(stateDir, p.Name)
			if err != nil {
				return resolvedProvider{}, err
			}
			key = stored
		}
		if key == "" {
			return resolvedProvider{}, fmt.Errorf("provider %s requires an API key; set --api-key, LISA_API_KEY, or %s, or pass --api-key once to store it", p.Name, p.KeyEnv)
		}
		if persistKey {
			if err := storeKey(stateDir, p.Name, key); err != nil {
				return resolvedProvider{}, err
			}
		}
		if endpoint == "" {
			endpoint = p.BaseURL
		}
		return resolvedProvider{endpoint: endpoint, verified: true, display: p.DisplayName, key: key}, nil
	}
	if endpoint == "" {
		endpoint = p.BaseURL
	}
	return resolvedProvider{endpoint: endpoint, verified: true, display: p.DisplayName}, nil
}
