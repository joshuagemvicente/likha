package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/providers"
	"likha/internal/session"
)

// Decision-bar and transcript tests for specs/command-permissions.

func openCommandReview(t *testing.T, m *ui, remember, warning string) *agent.ApprovalRequest {
	t.Helper()
	m.working, m.runID = true, 1
	m.events = make(chan agent.TurnEvent, 1)
	request := &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: "Command:\nnpm install", Remember: remember, Warning: warning, Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	return request
}

func TestCommandReviewThirdButtonFitsEveryWidth(t *testing.T) {
	for _, tc := range []struct {
		remember, wide, compact string
		breakpoint              int
	}{
		{agent.RememberSession, "[ Approve always ]", "[Always]", 53},
		{agent.RememberTrust, "[ Trust repo checks ]", "[Trust]", 56},
	} {
		for _, width := range []int{40, 49, 52, 53, 55, 56, 80, 120} {
			m := newKeysTestUI(t)
			m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			openCommandReview(t, m, tc.remember, "")
			bar := m.composerLines()
			for i, row := range bar {
				if got := plainWidth(row); got != width {
					t.Fatalf("%s at %d cols: bar row %d is %d cells", tc.remember, width, i, got)
				}
			}
			want := tc.wide
			if width < tc.breakpoint {
				want = tc.compact
			}
			if plain := stripANSI(bar[0]); !strings.Contains(plain, want) || strings.Count(plain, "> ") != 1 {
				t.Fatalf("%s at %d cols: bar = %q, want %q with one focus marker", tc.remember, width, plain, want)
			}
			assertViewport(t, m.View(), width, 24)
		}
	}
}

func TestCommandReviewTwoButtonsUnchanged(t *testing.T) {
	m := newKeysTestUI(t)
	openCommandReview(t, m, "", "")
	plain := stripANSI(m.composerLines()[0])
	if !strings.Contains(plain, "> [ Approve ]    [ Decline ]") {
		t.Fatalf("two-button bar changed: %q", plain)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.reviewFocus != focusDecline {
		t.Fatal("Right did not move from Approve to Decline")
	}
}

func TestCommandReviewRememberButtonSetsRemembered(t *testing.T) {
	m := newKeysTestUI(t)
	request := openCommandReview(t, m, agent.RememberSession, "")
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.reviewFocus != focusRemember {
		t.Fatalf("Right from Approve focused %d, want the session button", m.reviewFocus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // stops at Decline
	if m.reviewFocus != focusDecline {
		t.Fatal("focus did not stop at Decline")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(request.Reply) != 1 || !<-request.Reply || !request.Remembered {
		t.Fatalf("session button did not approve and remember: remembered=%v", request.Remembered)
	}
	if !strings.Contains(m.status, "(approve always)") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestCommandReviewApproveDoesNotRemember(t *testing.T) {
	m := newKeysTestUI(t)
	request := openCommandReview(t, m, agent.RememberTrust, "")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !<-request.Reply || request.Remembered {
		t.Fatal("plain Approve remembered the decision")
	}
}

func TestCommandReviewRememberIsGatedUntilRead(t *testing.T) {
	m := newKeysTestUI(t)
	m.working, m.runID = true, 1
	m.events = make(chan agent.TurnEvent, 1)
	request := &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: strings.Repeat("line\n", 80), Remember: agent.RememberSession, Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(request.Reply) != 0 || m.status != reviewGateStatus {
		t.Fatal("session button confirmed an unread review")
	}
}

func TestCommandReviewShowsAlwaysAskWarning(t *testing.T) {
	m := newKeysTestUI(t)
	openCommandReview(t, m, "", "Always asks: this command deletes files (rm).")
	if view := stripANSI(m.View()); !strings.Contains(view, "Always asks: this command deletes files (rm).") {
		t.Fatalf("warning missing from review:\n%s", view)
	}
}

func TestCommandToolItemLabelsAutoApproval(t *testing.T) {
	auto := session.ToolRecord{Name: "run_command", Status: "succeeded", Content: "Exit status: 0\n" + agent.CommandApprovalPrefix + "auto-approved (read-only inspection)\nhello"}
	summary, preview, _ := toolSuccessSummary(auto, "…", 3)
	if summary != "Exit 0 · auto-approved" || len(preview) == 0 || preview[0] != "hello" {
		t.Fatalf("auto summary = %q preview = %q", summary, preview)
	}
	prompted := session.ToolRecord{Name: "run_command", Status: "succeeded", Content: "Exit status: 0\nhello"}
	if summary, _, _ := toolSuccessSummary(prompted, "…", 3); summary != "Exit 0" {
		t.Fatalf("prompted summary = %q", summary)
	}
}

func TestCommandGrantsResetOnSessionSwitch(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), store, first)
	m.commandGrants.Allow("npm install")
	if options := m.toolRunOptions(0); !options.CommandGrants.Allowed("npm install") || options.CommandTrusted == nil || options.TrustChecks == nil {
		t.Fatal("run options did not carry the grant store and trust hooks")
	}
	m.resumeSession(next.ID)
	if m.commandGrants.Allowed("npm install") || m.toolRunOptions(0).CommandGrants.Allowed("npm install") {
		t.Fatal("a session grant carried into another session")
	}
}
