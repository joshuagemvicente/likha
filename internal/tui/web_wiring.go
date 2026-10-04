package tui

import (
	"context"
	"fmt"
	"strings"

	"likha/internal/agent"
	"likha/internal/webtools"
)

// webHooks resolves the optional web tools from private configuration: no
// repo .env, no model credential reuse, no vendor fallback. Disabled tools
// return nil so they register unavailable; invalid configuration produces a
// visible issue entry and stays disabled for repository work. The configured
// backend (web.search.backend) selects both the key source and the search
// path; consent copy is per backend via webtools.BackendPrivacyCopy.
func (m *ui) webHooks(events chan<- agent.TurnEvent, runID uint64) (
	func(context.Context, webtools.SearchRequest) (webtools.SearchOutcome, error),
	func(context.Context, webtools.FetchRequest) (webtools.FetchOutcome, error),
	[]string,
) {
	config, err := webtools.LoadConfig(m.stateDir)
	state := fmt.Sprintf("%+v", config)
	if err != nil {
		state = "invalid: " + err.Error()
	}
	m.resetWebGrantsOnConfigChange(state)
	if err != nil {
		return nil, nil, []string{"Web tools remain disabled: " + err.Error()}
	}
	var issues []string
	var search func(context.Context, webtools.SearchRequest) (webtools.SearchOutcome, error)
	var fetch func(context.Context, webtools.FetchRequest) (webtools.FetchOutcome, error)
	if config.Search.Enabled {
		backend := config.Search.Backend
		key, kerr := webtools.LoadSearchKey(m.stateDir, backend)
		envVar, keyed := searchKeyEnvName(backend)
		switch {
		case !searchBackendSupported(backend):
			// Config validation already rejects unknown names; this is
			// defensive so contract drift surfaces visibly instead of
			// silently disabling the tool.
			issues = append(issues, "Web search is enabled but the configured backend "+backend+" is not supported; supported backends are: "+strings.Join(webtools.SupportedBackends, ", ")+".")
		case kerr != nil:
			issues = append(issues, "Web search disabled: "+kerr.Error())
		case keyed && key == "":
			issues = append(issues, "Web search is enabled but no "+backend+" key is configured; set "+envVar+" or the private tool-keys file to use it.")
		default:
			search = func(ctx context.Context, req webtools.SearchRequest) (webtools.SearchOutcome, error) {
				reqScope := &agent.ConsentRequest{Kind: "search-backend", Backend: backend, Query: req.Query}
				if m.webGranted(reqScope.ScopeKey()) {
					return webtools.SearchWithBackend(ctx, backend, key, req)
				}
				bound := &agent.ConsentRequest{Kind: reqScope.Kind, Backend: reqScope.Backend, Query: req.Query, Privacy: webtools.BackendPrivacyCopy(backend)}
				allowed, err := m.requestConsent(ctx, events, runID, bound)
				if err != nil {
					return webtools.SearchOutcome{}, err
				}
				if !allowed {
					return webtools.SearchOutcome{}, webtools.ErrConsentDeclined
				}
				return webtools.SearchWithBackend(ctx, backend, key, req)
			}
		}
	}
	if config.Fetch.Enabled {
		fetch = func(ctx context.Context, req webtools.FetchRequest) (webtools.FetchOutcome, error) {
			return webtools.Fetch(ctx, m.fetchConsent(events, runID), req)
		}
	}
	return search, fetch, issues
}

// searchBackendSupported reports whether the configured backend name is in
// webtools.SupportedBackends. LoadConfig already rejects unknown names; this
// keeps the wiring defensive against contract drift.
func searchBackendSupported(backend string) bool {
	for _, name := range webtools.SupportedBackends {
		if name == backend {
			return true
		}
	}
	return false
}

// searchKeyEnvName names the environment variable that supplies one keyed
// search backend's credential, for the visible missing-key issue. It mirrors
// webtools' key sources (config.go searchKeyEnv); DuckDuckGo needs no key and
// reports keyed=false, so an empty key proceeds without an issue.
func searchKeyEnvName(backend string) (envVar string, keyed bool) {
	switch backend {
	case "brave":
		return webtools.SearchKeyEnv, true
	case "tavily":
		return webtools.TavilyKeyEnv, true
	case "exa":
		return webtools.ExaKeyEnv, true
	default:
		return "", false
	}
}

func fetchPrivacyCopy() string {
	return "The contacted site receives the URL and request metadata; converted text reaches the configured model provider as untrusted data. " +
		"No repository files, histories, provider keys, or credentials are attached. Cross-origin redirects ask before any new connection."
}

// fetchConsent is the per-origin consent hook webtools.Fetch invokes for the
// canonicalized original and each redirect origin before any connection.
func (m *ui) fetchConsent(events chan<- agent.TurnEvent, runID uint64) func(context.Context, webtools.ConsentScope) (bool, error) {
	return func(ctx context.Context, scope webtools.ConsentScope) (bool, error) {
		reqScope := &agent.ConsentRequest{Kind: scope.Kind, Origin: scope.Origin}
		if m.webGranted(reqScope.ScopeKey()) {
			return true, nil
		}
		bound := &agent.ConsentRequest{
			Kind: scope.Kind, Backend: scope.Backend, Query: scope.Query,
			Origin: scope.Origin, URL: scope.URL, Privacy: fetchPrivacyCopy(),
		}
		allowed, err := m.requestConsent(ctx, events, runID, bound)
		if err != nil {
			return false, err
		}
		// answerConsent recorded the grant through the same scope key.
		return allowed, nil
	}
}
