package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
)

// Web consent is conversation-scoped: grants live in m.webGrants for this app
// lifetime only, are cleared on session switch, and are never persisted as
// effective future grants. The consent dialog serializes with approval
// reviews, records every request and decision as conversation events, and a
// decline performs no request toward the pending target.
type consentState struct {
	pending   bool
	responded bool
	request   *agent.ConsentRequest
}

// requestConsent blocks the calling tool goroutine on one consent decision.
// It mirrors the ask broker: the events channel carries the request, the UI
// replies at most once.
func (m *ui) requestConsent(ctx context.Context, req *agent.ConsentRequest) (bool, error) {
	if req == nil || req.Reply == nil {
		return false, errors.New("web consent requires a reply channel")
	}
	select {
	case m.events <- agent.TurnEvent{RunID: m.runID, Kind: "consent", Consent: req}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case allowed := <-req.Reply:
		return allowed, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (m *ui) openConsent(req *agent.ConsentRequest) {
	m.hideActivity()
	if m.consent.pending || m.consent.responded || req == nil {
		// One pending consent at a time; concurrent scopes queue through
		// their callers, whose tools serialize. A duplicate request is
		// treated as declined rather than double-answered.
		if req != nil && req.Reply != nil {
			select {
			case req.Reply <- false:
			default:
			}
		}
		return
	}
	m.consent = consentState{pending: true, request: req}
	m.entries = append(m.entries, entry{role: "Likha", content: "Network consent requested:\n" + consentRequestText(req)})
	m.status = "Web consent: allow or decline"
	m.jumpBottom()
	m.layoutWidth = 0
	m.persist()
}

func (m *ui) closeConsent() {
	if m.consent.pending {
		if req := m.consent.request; req != nil && !m.consent.responded && req.Reply != nil {
			// An interrupted run settles pending consent as declined; no
			// request toward the target was made.
			select {
			case req.Reply <- false:
			default:
			}
		}
	}
	m.consent = consentState{}
}

func (m *ui) answerConsent(allowed bool) {
	if !m.consent.pending || m.consent.responded || m.consent.request == nil {
		return
	}
	req := m.consent.request
	m.consent.responded = true
	decision := "declined"
	if allowed {
		decision = "allowed"
		m.grantWeb(req.ScopeKey())
	}
	m.entries = append(m.entries, entry{role: "Likha", content: "Web consent " + decision + " for " + consentScopeText(req) + ". The decision applies to this conversation only; it clears on session switch or app exit."})
	m.layoutWidth = 0
	m.persist()
	m.consent = consentState{}
	if req.Reply != nil {
		select {
		case req.Reply <- allowed:
		default:
		}
	}
}

func (m *ui) consentVisible() bool {
	return m.consent.pending && !m.consent.responded && m.pending == nil && !m.keyModal.open && !m.dialog.open && m.mode != modeSetup
}

func (m *ui) handleConsentKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !m.consentVisible() {
		return false, nil
	}
	switch msg.String() {
	case "ctrl+c":
		return false, nil // run cancellation stays live during consent
	case "y", "enter", "a":
		m.answerConsent(true)
	case "n", "esc", "d":
		m.answerConsent(false)
	}
	return true, nil
}

func (m *ui) consentView() string {
	req := m.consent.request
	var lines []string
	title := "Web consent required for this conversation"
	lines = append(lines, m.theme.Selected.Render(title), "")
	text := consentRequestText(req)
	for _, line := range wrap(text, max(20, m.width-4)) {
		lines = append(lines, line)
	}
	lines = append(lines, "")
	for _, line := range wrap(req.Privacy, max(20, m.width-4)) {
		lines = append(lines, line)
	}
	lines = append(lines,
		"",
		"This grant touches neither repository files, provider keys, nor MCP trust decisions.",
		"",
		"y/Enter allow · n/Esc decline · Ctrl+C cancels the run",
	)
	return m.frame(lines)
}

func consentRequestText(req *agent.ConsentRequest) string {
	var builder strings.Builder
	switch req.Kind {
	case "search-backend":
		builder.WriteString("Backend: " + req.Backend + " (first use this conversation)\n")
		builder.WriteString("Query: " + req.Query + "\n")
		builder.WriteString("Scope: this one configured search backend, this conversation.")
	case "fetch-origin":
		builder.WriteString("Origin: " + req.Origin + " (first fetch this conversation)\n")
		builder.WriteString("URL: " + req.URL + "\n")
		builder.WriteString("Scope: this canonical origin, this conversation.")
	}
	return builder.String()
}

func consentScopeText(req *agent.ConsentRequest) string {
	if req.Kind == "search-backend" {
		return req.Backend
	}
	return req.Origin
}

// grantWeb records one conversation-scoped consent. Grants never leave this
// process: session switch (m.webGrants = nil) and app exit clear them. The
// mutex serializes UI-goroutine writes against tool-goroutine reads.
func (m *ui) grantWeb(key string) {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	if m.webGrants == nil {
		m.webGrants = map[string]bool{}
	}
	m.webGrants[key] = true
}

func (m *ui) webGranted(key string) bool {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	return m.webGrants != nil && m.webGrants[key]
}
