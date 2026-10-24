package providers

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"likha/internal/mcp"
	"likha/internal/model"
)

// ModelMetadataObserver receives sanitized summaries of model-list metadata
// when model diagnostics are enabled. It never receives credentials or prompt
// content.
type ModelMetadataObserver func(provider, operation string, elapsed time.Duration, details []model.ModelDetails, err error)

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
	EffortLevels           []string                    // live effort levels the startup /models fetch published for the active model, lowest first; nil when none
	DefaultEffort          string                      // the active model's live default effort level; "" when unknown
	Reasoning              *model.ReasoningCapabilities
	Variant                *string               // explicit launch suffix; nil restores remembered choice
	SelectionOverride      bool                  // explicit launch provider/model/endpoint prevents session restoration
	VariantResolved        bool                  // composition root already validated and installed the launch selection
	ModelMetadataObserver  ModelMetadataObserver // optional opt-in diagnostics for model-list metadata
	Nerd                   bool                  // user opted into Nerd Font markers
	ASCII                  bool                  // user selected the plain-ASCII block glyph set
	Mcp                    *mcp.McpManager
	Attention              StoredAttentionConfig // attention-signal settings from config.json (specs/attention-notifications)
	// AttentionOut receives the terminal bell and OSC 9 sequences. It must
	// share the renderer's write lock so a signal never splits a frame; nil
	// disables every signal (tests, non-terminal output).
	AttentionOut io.Writer
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
// providers have no API key at all: they resolve to a validated stored browser
// sign-in and cannot send credentials to an endpoint override.
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
		if strings.TrimSpace(apiKeyFlag) != "" || strings.TrimSpace(os.Getenv("LIKHA_API_KEY")) != "" || strings.TrimSpace(os.Getenv("LIKHA_CHATGPT_API_KEY")) != "" {
			return ResolvedProvider{}, fmt.Errorf("ChatGPT uses browser sign-in, not an API key; remove --api-key/LIKHA_API_KEY/LIKHA_CHATGPT_API_KEY or select --provider openai for separately billed API access")
		}
		for _, override := range []string{endpoint, strings.TrimSpace(os.Getenv("LIKHA_ENDPOINT"))} {
			if override != "" && strings.TrimRight(override, "/") != model.ChatGPTResource {
				return ResolvedProvider{}, fmt.Errorf("ChatGPT sign-in uses only %s; remove the custom --endpoint/LIKHA_ENDPOINT override", model.ChatGPTResource)
			}
		}
		creds, ok, err := StoredOAuth(stateDir, p.Name)
		if err != nil {
			return ResolvedProvider{}, err
		}
		if !ok {
			return ResolvedProvider{}, oauthLoginRequired("no validated ChatGPT registration is signed in; legacy Codex credentials require a fresh browser sign-in")
		}
		return ResolvedProvider{Endpoint: model.ChatGPTResource, Verified: true, Display: p.DisplayName, Creds: creds, OAuth: true}, nil
	}
	if endpoint == "" {
		endpoint = p.BaseURL
	}
	// An override that is neither the provider's own base URL nor its
	// documented endpoint in another region is outside the accepted list,
	// so it carries the unverified warning (v1-spec §3).
	verified := EndpointVerified(p, endpoint)
	if !p.Hosted {
		return ResolvedProvider{Endpoint: endpoint, Verified: verified, Display: p.DisplayName}, nil
	}
	key, err := hostedKey(p, apiKeyFlag, persistKey, stateDir)
	if err != nil {
		return ResolvedProvider{}, err
	}
	return ResolvedProvider{Endpoint: endpoint, Verified: verified, Display: p.DisplayName, Key: key}, nil
}

// hostedKey returns the first non-blank key in resolution order. Keys are
// trimmed because a pasted trailing newline or space would otherwise reach
// the provider as a rejected credential, and a blank variable must not hide
// a later source. Only a key that came from the flag is persisted.
func hostedKey(p model.Provider, apiKeyFlag string, persistKey bool, stateDir string) (string, error) {
	if key := strings.TrimSpace(apiKeyFlag); key != "" {
		if persistKey {
			if err := StoreKey(stateDir, p.Name, key); err != nil {
				return "", err
			}
		}
		return key, nil
	}
	if p.KeyEnv != "" {
		if key := strings.TrimSpace(os.Getenv(p.KeyEnv)); key != "" {
			return key, nil
		}
	}
	stored, err := StoredKey(stateDir, p.Name)
	if err != nil {
		return "", err
	}
	if key := strings.TrimSpace(stored); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("provider %s requires an API key; set --api-key, LIKHA_API_KEY, or %s, or pass --api-key once to store it", p.Name, p.KeyEnv)
}

// regionalHosts matches the documented per-region hosts of a provider whose
// base URL names one region. Amazon Bedrock's base is us-east-1; every other
// region is bedrock-runtime.<region>.amazonaws.com with the same path
// (https://docs.aws.amazon.com/bedrock/latest/userguide/endpoints.html).
var regionalHosts = map[string]*regexp.Regexp{
	"bedrock": regexp.MustCompile(`^bedrock-runtime\.[a-z]{2}(-gov)?-[a-z]+-[0-9]\.amazonaws\.com$`),
}

// EndpointVerified reports whether endpoint is one of p's accepted
// endpoints: an https URL equal to p's base URL (ignoring a trailing slash),
// or, for a provider with documented regional hosts, the same path on one of
// those hosts. User info, an explicit port, a query or a fragment make it
// unverified.
func EndpointVerified(p model.Provider, endpoint string) bool {
	if strings.TrimRight(endpoint, "/") == p.BaseURL {
		return true
	}
	got, err := url.Parse(endpoint)
	if err != nil || got.Scheme != "https" || got.User != nil || got.Port() != "" || got.RawQuery != "" || got.Fragment != "" {
		return false
	}
	base, err := url.Parse(p.BaseURL)
	if err != nil {
		return false
	}
	if strings.TrimRight(got.Path, "/") != strings.TrimRight(base.Path, "/") {
		return false
	}
	host := strings.ToLower(got.Hostname())
	if host == strings.ToLower(base.Hostname()) {
		return true
	}
	pattern, ok := regionalHosts[p.Name]
	return ok && pattern.MatchString(host)
}
