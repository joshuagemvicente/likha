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
	// allowFocused reports the focused decision button. Focus starts on
	// Decline: unlike an approval review there is no read-to-end gate, so
	// a stray Enter (a steering prompt typed as the dialog opened) must
	// fail safe and make no request.
	allowFocused bool
	// statusBefore is the run's status when the dialog opened; answering
	// restores it so the working indicator returns.
	statusBefore string
	// activityBefore reports whether the Working row showed when the dialog
	// opened. Consent normally comes from inside a running web tool whose
	// item already replaced that row, so answering must not add one back.
	activityBefore bool
}

// errConsentUnavailable fails a consent request the interface could not
// show because another interaction holds the screen. The user was never
// asked, so this is not a decline: the web tool reports a failure and the
// model may retry later.
var errConsentUnavailable = errors.New("another interaction is active; consent was not asked")

// requestConsent blocks the calling tool goroutine on one consent decision.
// It mirrors the ask broker: the events channel carries the request, the UI
// replies at most once.
func (m *ui) requestConsent(ctx context.Context, events chan<- agent.TurnEvent, runID uint64, req *agent.ConsentRequest) (bool, error) {
	if req == nil || req.Reply == nil {
		return false, errors.New("web consent requires a reply channel")
	}
	select {
	case events <- agent.TurnEvent{RunID: runID, Kind: "consent", Consent: req}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case reply := <-req.Reply:
		return reply.Allowed, reply.Err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// errConsentRunEnding fails a consent request that arrived after its run
// began cancelling: the tool queued it before the cancel, and its run context
// has already ended its wait. The user was never asked, so this is not a
// decline either.
var errConsentRunEnding = errors.New("the run is ending; consent was not asked")

// openConsent shows the consent dialog for req and reports whether it
// opened. A request it cannot show is answered with an error, never a
// decline, and leaves no conversation event behind.
func (m *ui) openConsent(req *agent.ConsentRequest) bool {
	if req == nil {
		return false
	}
	switch {
	case !m.working || m.cancelling:
		// A web tool queued this before Ctrl+C landed. Opening it now would
		// show a dialog during the cancel, and Allow would grant an origin
		// no tool is waiting for.
		refuseConsent(req, errConsentRunEnding)
		return false
	case m.consent.pending || m.consent.responded || m.ask.pending:
		// One pending consent at a time; concurrent scopes queue through
		// their callers, whose tools serialize. A duplicate request, or one
		// arriving behind an open question, fails as unavailable rather
		// than being double-answered or hidden under the question. It is
		// not a decline: the user was never asked.
		refuseConsent(req, errConsentUnavailable)
		return false
	}
	activityBefore := m.hasActivity()
	m.hideActivity()
	// The dialog replaces historical inspection surfaces exactly like an
	// arriving approval or question does.
	m.resetToolInspection()
	m.resetAgentInspection()
	m.closePopups()
	m.consent = consentState{pending: true, request: req, statusBefore: m.status, activityBefore: activityBefore}
	m.entries = append(m.entries, entry{role: "Likha", content: "Network consent requested:\n" + consentRequestText(req)})
	m.status = "Web consent: allow or decline"
	m.jumpBottom()
	m.layoutWidth = 0
	m.persist()
	m.signalAttention(attentionApproval)
	return true
}

// refuseConsent answers a consent request the interface never showed.
func refuseConsent(req *agent.ConsentRequest, err error) {
	if req.Reply == nil {
		return
	}
	select {
	case req.Reply <- agent.ConsentReply{Err: err}:
	default:
	}
}

// closeConsent clears the consent dialog without answering it. Every caller
// either ends the run first (cancellation) or runs after it ended, so the
// waiting tool sees the run context end and reports the interruption; a
// reply here would reach the model as a decline the user never made.
func (m *ui) closeConsent() {
	m.consent = consentState{}
}

// answerConsent records the decision, replies exactly once, and hands the
// screen back to the run: its status returns while the web tool resumes,
// with the Working row only if it showed when the dialog opened; otherwise
// the live tool item's clock keeps its dot ticking.
func (m *ui) answerConsent(allowed bool) tea.Cmd {
	if !m.consent.pending || m.consent.responded || m.consent.request == nil {
		return nil
	}
	req, statusBefore, activityBefore := m.consent.request, m.consent.statusBefore, m.consent.activityBefore
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
		case req.Reply <- agent.ConsentReply{Allowed: allowed}:
		default:
		}
	}
	if !m.working || m.cancelling {
		return nil
	}
	m.status = statusBefore
	if !activityBefore {
		return m.armActivityTick()
	}
	m.showActivity()
	return m.activityTickCmd()
}

func (m *ui) consentVisible() bool {
	return m.consent.pending && !m.consent.responded && m.pending == nil && !m.keyModal.open && !m.dialog.open && m.mode != modeSetup
}

// handleConsentKey runs the consent decision like an approval review
// (FR-22): ← focuses Allow, → focuses Decline, Tab and Shift+Tab toggle
// between them, Enter confirms the focused button, and Esc declines. No
// letter grants or declines, so text typed while the dialog opens is
// swallowed, never treated as a decision. Below the minimum terminal size
// the dialog is not drawn, so Enter is ignored rather than deciding blind.
// Ctrl+C falls through to run cancellation.
func (m *ui) handleConsentKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !m.consentVisible() {
		return false, nil
	}
	switch msg.String() {
	case "ctrl+c":
		return false, nil // run cancellation stays live during consent
	case "left":
		m.consent.allowFocused = true
	case "right":
		m.consent.allowFocused = false
	case "tab", "shift+tab":
		// Two buttons: either direction lands on the other one.
		m.consent.allowFocused = !m.consent.allowFocused
	case "enter":
		if m.width < minWidth || m.height < minHeight {
			return true, nil
		}
		return true, m.answerConsent(m.consent.allowFocused)
	case "esc":
		return true, m.answerConsent(false)
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
		m.consentButtons(),
		m.theme.Muted.Render(fit("←/→ choose · Enter confirm · Esc decline · Ctrl+C cancels the run", max(20, m.width-4))),
	)
	return m.frame(lines)
}

// consentButtons renders the decision bar with exactly one focus marker,
// in the approval review's button style.
func (m *ui) consentButtons() string {
	button := func(label string, focused bool) string {
		if focused {
			return m.theme.Selected.Render("> [ " + label + " ]")
		}
		return m.theme.Muted.Render("  [ " + label + " ]")
	}
	return button("Allow", m.consent.allowFocused) + "  " + button("Decline", !m.consent.allowFocused)
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
// process: session switch (clearWebGrants), app exit, and any web
// configuration change (resetWebGrantsOnConfigChange) clear them. The
// mutex serializes UI-goroutine writes against tool-goroutine reads.
func (m *ui) grantWeb(key string) {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	if m.webGrants == nil {
		m.webGrants = map[string]bool{}
	}
	m.webGrants[key] = true
}

// clearWebGrants drops every grant on a session switch. A force-stopped
// run's web tool may still be checking a grant from its own goroutine.
func (m *ui) clearWebGrants() {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	m.webGrants = nil
}

func (m *ui) webGranted(key string) bool {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	return m.webGrants != nil && m.webGrants[key]
}

// resetWebGrantsOnConfigChange clears every web grant when the loaded web
// configuration differs from the one last seen (decisions: grants clear on
// backend configuration changes). Switching brave→tavily→brave therefore
// asks again for brave, and fetch origins re-prompt after a toggle. The
// first observation only records the state.
func (m *ui) resetWebGrantsOnConfigChange(state string) {
	m.grantsMu.Lock()
	defer m.grantsMu.Unlock()
	if m.webConfigSeen && m.webConfigState != state {
		m.webGrants = nil
	}
	m.webConfigSeen, m.webConfigState = true, state
}
