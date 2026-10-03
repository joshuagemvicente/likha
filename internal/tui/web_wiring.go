package tui

import (
	"context"

	"likha/internal/agent"
	"likha/internal/webtools"
)

// webHooks resolves the optional web tools from private configuration: no
// repo .env, no model credential reuse, no vendor fallback. Disabled tools
// return nil so they register unavailable; invalid configuration produces a
// visible issue entry and stays disabled for repository work.
func (m *ui) webHooks() (
	func(context.Context, webtools.SearchRequest) (webtools.SearchOutcome, error),
	func(context.Context, webtools.FetchRequest) (webtools.FetchOutcome, error),
	[]string,
) {
	config, err := webtools.LoadConfig(m.stateDir)
	if err != nil {
		return nil, nil, []string{"Web tools remain disabled: " + err.Error()}
	}
	var issues []string
	var search func(context.Context, webtools.SearchRequest) (webtools.SearchOutcome, error)
	var fetch func(context.Context, webtools.FetchRequest) (webtools.FetchOutcome, error)
	if config.Search.Enabled {
		key, kerr := webtools.LoadSearchKey(m.stateDir)
		if kerr != nil {
			issues = append(issues, "Web search disabled: "+kerr.Error())
		} else if key == "" {
			issues = append(issues, "Web search is enabled but no Brave key is configured; set BRAVE_SEARCH_API_KEY or the private tool-keys file to use it.")
		} else {
			search = func(ctx context.Context, req webtools.SearchRequest) (webtools.SearchOutcome, error) {
				reqScope := &agent.ConsentRequest{Kind: "search-backend", Backend: "brave", Query: req.Query}
				if m.webGranted(reqScope.ScopeKey()) {
					return webtools.Search(ctx, key, req)
				}
				bound := &agent.ConsentRequest{Kind: reqScope.Kind, Backend: reqScope.Backend, Query: req.Query, Privacy: searchPrivacyCopy()}
				allowed, err := m.requestConsent(ctx, bound)
				if err != nil {
					return webtools.SearchOutcome{}, err
				}
				if !allowed {
					return webtools.SearchOutcome{}, webtools.ErrConsentDeclined
				}
				return webtools.Search(ctx, key, req)
			}
		}
	}
	if config.Fetch.Enabled {
		fetch = func(ctx context.Context, req webtools.FetchRequest) (webtools.FetchOutcome, error) {
			return webtools.Fetch(ctx, m.fetchConsent(), req)
		}
	}
	return search, fetch, issues
}

func searchPrivacyCopy() string {
	return webtools.BraveDefaultRetention +
		". Brave receives the query and request metadata; selected results reach the configured model provider as untrusted excerpts. " +
		"Model-written queries can contain project details — inspect the query before allowing."
}

func fetchPrivacyCopy() string {
	return "The contacted site receives the URL and request metadata; converted text reaches the configured model provider as untrusted data. " +
		"No repository files, histories, provider keys, or credentials are attached. Cross-origin redirects ask before any new connection."
}

// fetchConsent is the per-origin consent hook webtools.Fetch invokes for the
// canonicalized original and each redirect origin before any connection.
func (m *ui) fetchConsent() func(context.Context, webtools.ConsentScope) (bool, error) {
	return func(ctx context.Context, scope webtools.ConsentScope) (bool, error) {
		reqScope := &agent.ConsentRequest{Kind: scope.Kind, Origin: scope.Origin}
		if m.webGranted(reqScope.ScopeKey()) {
			return true, nil
		}
		bound := &agent.ConsentRequest{
			Kind: scope.Kind, Backend: scope.Backend, Query: scope.Query,
			Origin: scope.Origin, URL: scope.URL, Privacy: fetchPrivacyCopy(),
		}
		allowed, err := m.requestConsent(ctx, bound)
		if err != nil {
			return false, err
		}
		// answerConsent recorded the grant through the same scope key.
		return allowed, nil
	}
}
