package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
)

// permE2EnewTurn starts a real turn against an httptest model whose first
// request proposes an edit_file and whose next request answers plainly, and
// returns the UI once the approval review is on screen. calls counts the
// provider completion requests the model received.
func permE2EnewTurn(t *testing.T) (m *ui, root, content string, calls *atomic.Int32) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "work.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	content = strings.Repeat("new line\n", 120)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 {
			call := map[string]any{"index": 0, "id": "action_1", "type": "function", "function": map[string]any{
				"name":      "edit_file",
				"arguments": fmt.Sprintf(`{"path":%q,"content":%q}`, "work.txt", content),
			}}
			chunk := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}, "finish_reason": "tool_calls"}}}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Acknowledged\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "e2e-model", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m = NewUI(root, repo, client, "e2e-model", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input = []rune("please edit work.txt")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})
	permE2Epump(t, m, func() bool { return m.pending != nil })
	return m, root, content, &count
}

// permE2Epump feeds turn events into the UI until done reports the expected
// state, mirroring the bubbletea event loop without a running program.
func permE2Epump(t *testing.T, m *ui, done func() bool) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for !done() {
		select {
		case ev, ok := <-m.events:
			if !ok {
				t.Fatal("turn event stream closed before the expected state")
			}
			m.Update(ev)
		case <-timeout:
			t.Fatal("timed out waiting for the turn event stream")
		}
	}
}

// permE2Eintercept replaces the pending proposal's reply channel with one the
// test owns, so the decision Update sends is observed deterministically before
// it is forwarded to the agent run waiting on the original channel.
func permE2Eintercept(m *ui) (*agent.ApprovalRequest, chan bool) {
	real := m.pending
	decisions := make(chan bool, 1)
	m.pending = &agent.ApprovalRequest{Kind: real.Kind, Title: real.Title, Body: real.Body, Reply: decisions}
	return real, decisions
}

// permE2Edecision waits for the decision Update sent to the interception
// channel.
func permE2Edecision(t *testing.T, decisions <-chan bool) bool {
	t.Helper()
	select {
	case v := <-decisions:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("decision was not recorded for the pending review")
		return false
	}
}

// permE2EhasAssistant reports whether a transcript row holds the streamed
// assistant prose.
func permE2EhasAssistant(m *ui, text string) bool {
	for _, e := range m.entries {
		if e.role == "Assistant" && strings.Contains(e.content, text) {
			return true
		}
	}
	return false
}

// TestPermE2EApprovalRendersDecisionButtons drives a real model edit request
// through Update: the review renders both actions with exactly one focused.
func TestPermE2EApprovalRendersDecisionButtons(t *testing.T) {
	m, _, _, _ := permE2EnewTurn(t)
	view := stripANSI(m.View())
	if !strings.Contains(view, "> [ Approve ]") || !strings.Contains(view, "[ Decline ]") {
		t.Fatalf("decision buttons missing from the review view: %q", view)
	}
	if got := strings.Count(view, "> ["); got != 1 {
		t.Fatalf("review view has %d focused buttons, want exactly 1: %q", got, view)
	}
}

// TestPermE2EGatedApproveSendsNoReply confirms Enter on the default Approve
// focus before the review is read to the end refuses, keeps the review open,
// and sends nothing back to the agent.
func TestPermE2EGatedApproveSendsNoReply(t *testing.T) {
	m, _, _, _ := permE2EnewTurn(t)
	request := m.pending
	if request == nil {
		t.Fatal("approval review did not open")
	}
	if m.reviewFocus != focusApprove {
		t.Fatalf("default review focus = %d, want Approve (%d)", m.reviewFocus, focusApprove)
	}
	if m.reviewReady() {
		t.Fatal("review did not paginate; the approve gate cannot be exercised")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.status != reviewGateStatus {
		t.Fatalf("gated approve status = %q, want %q", m.status, reviewGateStatus)
	}
	if len(request.Reply) != 0 {
		t.Fatal("gated approve sent a reply to the agent")
	}
	if m.pending != request {
		t.Fatal("gated approve closed the review")
	}
	if !strings.Contains(stripANSI(m.View()), reviewGateStatus) {
		t.Fatal("gated approve status is not visible in the view")
	}
}

// TestPermE2EDeclineLetsModelContinue focuses Decline with right and confirms
// with Enter: the reply is false, the rejection is visible, and the agent run
// continues to its follow-up request without touching the file.
func TestPermE2EDeclineLetsModelContinue(t *testing.T) {
	m, root, _, calls := permE2EnewTurn(t)
	if m.pending == nil {
		t.Fatal("approval review did not open")
	}
	// Edit reviews offer Approve always between Approve and Decline
	// (specs/approve-always); focus stops at Decline.
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.reviewFocus != focusDecline {
		t.Fatalf("right did not focus Decline: focus = %d", m.reviewFocus)
	}
	real, decisions := permE2Eintercept(m)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := permE2Edecision(t, decisions)
	real.Reply <- got
	if got {
		t.Fatal("decline sent an approval reply")
	}
	if m.pending != nil {
		t.Fatal("decline left the review open")
	}
	if m.status != "Rejected" {
		t.Fatalf("decline status = %q, want Rejected", m.status)
	}
	// The rejection record is the tool result the run appends once it resumes
	// with Reply=false; the status word itself is transient by design.
	permE2Epump(t, m, func() bool { return !m.working })
	if view := stripANSI(m.View()); !strings.Contains(view, "rejected by user") {
		t.Fatalf("rejection record not visible after the run continued: %q", view)
	}
	data, err := os.ReadFile(filepath.Join(root, "work.txt"))
	if err != nil || string(data) != "before\n" {
		t.Fatalf("declined edit changed the file: %q, %v", data, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("model requests = %d, want 2 (the run must continue after a decline)", got)
	}
	if !permE2EhasAssistant(m, "Acknowledged") {
		t.Fatal("model did not continue to a follow-up answer after the decline")
	}
}

// TestPermE2EApproveAfterReviewAppliesEdit reads the review to the end, then
// confirms the default Approve focus with Enter: the reply is true and the
// approved edit is applied.
func TestPermE2EApproveAfterReviewAppliesEdit(t *testing.T) {
	m, root, content, calls := permE2EnewTurn(t)
	if m.pending == nil {
		t.Fatal("approval review did not open")
	}
	if m.reviewFocus != focusApprove {
		t.Fatalf("default review focus = %d, want Approve", m.reviewFocus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !m.reviewReady() {
		t.Fatal("scrolling to the end did not arm the approve gate")
	}
	real, decisions := permE2Eintercept(m)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := permE2Edecision(t, decisions)
	real.Reply <- got
	if !got {
		t.Fatal("approve sent a rejection reply")
	}
	if m.pending != nil {
		t.Fatal("approve left the review open")
	}
	if m.status != "Executing approved edit" {
		t.Fatalf("approve status = %q, want %q", m.status, "Executing approved edit")
	}
	permE2Epump(t, m, func() bool { return !m.working })
	data, err := os.ReadFile(filepath.Join(root, "work.txt"))
	if err != nil || string(data) != content {
		t.Fatalf("approved edit was not applied: %q, %v", data, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("model requests = %d, want 2", got)
	}
	if !permE2EhasAssistant(m, "Acknowledged") {
		t.Fatal("model did not continue after the approved edit")
	}
}
