package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/mcp"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/tools"
)

// Decision-bar, grant, and transcript tests for specs/approve-always.

func openReview(t *testing.T, m *ui, kind, remember string) *agent.ApprovalRequest {
	t.Helper()
	m.working, m.runID = true, 1
	m.events = make(chan agent.TurnEvent, 1)
	request := &agent.ApprovalRequest{Kind: kind, Title: kind, Body: "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n", Remember: remember, Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	return request
}

func TestApproveAlwaysBarForEveryRememberableKind(t *testing.T) {
	for _, tc := range []struct{ kind, remember string }{
		{"edit", agent.RememberEdits},
		{"command", agent.RememberSession},
		{"mcp", agent.RememberServer},
	} {
		for _, width := range []int{40, 52, 53, 56, 80} {
			m := newKeysTestUI(t)
			m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			openReview(t, m, tc.kind, tc.remember)
			bar := m.composerLines()
			for i, row := range bar {
				if got := plainWidth(row); got != width {
					t.Fatalf("%s at %d cols: bar row %d is %d cells", tc.kind, width, i, got)
				}
			}
			want := "> [ Approve ]    [ Approve always ]    [ Decline ]"
			if width < 53 {
				want = "> [Approve]    [Always]    [Decline]"
			}
			if plain := stripANSI(bar[0]); !strings.Contains(plain, want) {
				t.Fatalf("%s at %d cols: bar = %q, want %q", tc.kind, width, plain, want)
			}
			assertViewport(t, m.View(), width, 24)
		}
	}
}

func TestReviewBarWidthMatchesBreakpoints(t *testing.T) {
	m := newKeysTestUI(t)
	m.pending = &agent.ApprovalRequest{Remember: agent.RememberEdits}
	if got := reviewBarWidth(m.reviewButtons()); got != 50 {
		t.Fatalf("Approve always bar = %d cells, want 50", got)
	}
	m.pending = &agent.ApprovalRequest{Remember: agent.RememberTrust}
	if got := reviewBarWidth(m.reviewButtons()); got != 53 {
		t.Fatalf("Trust bar = %d cells, want 53", got)
	}
}

func TestEditReviewApproveAlwaysSetsRemembered(t *testing.T) {
	m := newKeysTestUI(t)
	request := openReview(t, m, "edit", agent.RememberEdits)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(request.Reply) != 1 || !<-request.Reply || !request.Remembered {
		t.Fatalf("Approve always did not approve and remember: remembered=%v", request.Remembered)
	}
	if m.status != "Executing approved edit (approve always)" {
		t.Fatalf("status = %q", m.status)
	}
}

func TestMCPReviewApproveOnceDoesNotRemember(t *testing.T) {
	m := newKeysTestUI(t)
	request := openReview(t, m, "mcp", agent.RememberServer)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !<-request.Reply || request.Remembered {
		t.Fatal("plain Approve remembered the MCP decision")
	}
	if m.status != "Executing approved mcp" {
		t.Fatalf("status = %q", m.status)
	}
}

func TestEditReviewApproveAlwaysIsGatedUntilRead(t *testing.T) {
	m := newKeysTestUI(t)
	m.working, m.runID = true, 1
	m.events = make(chan agent.TurnEvent, 1)
	request := &agent.ApprovalRequest{Kind: "edit", Title: "Edit: a.go", Body: strings.Repeat("+line\n", 80), Remember: agent.RememberEdits, Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(request.Reply) != 0 || m.status != reviewGateStatus {
		t.Fatal("Approve always confirmed an unread review")
	}
}

func TestEditReviewWithoutRememberKeepsTwoButtons(t *testing.T) {
	m := newKeysTestUI(t)
	openReview(t, m, "edit", "")
	if plain := stripANSI(m.composerLines()[0]); !strings.Contains(plain, "> [ Approve ]    [ Decline ]") || strings.Contains(plain, "always") {
		t.Fatalf("warning or /init edit bar = %q", plain)
	}
}

func TestEditGrantReachesRunOptions(t *testing.T) {
	m := newKeysTestUI(t)
	options := m.toolRunOptions(0)
	if options.EditGrant == nil || options.EditGrant != m.editGrant {
		t.Fatal("run options did not carry the session edit grant")
	}
	m.editGrant.Allow()
	if !m.toolRunOptions(0).EditGrant.Allowed() {
		t.Fatal("a granted edit grant did not reach the run options")
	}
}

func TestSessionGrantsResetOnSessionSwitch(t *testing.T) {
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
	servers := mcp.NewMcpManagerForTest(nil, nil, map[string]bool{"docs": true}, nil)
	m := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Mcp: servers}, t.TempDir(), store, first)
	m.editGrant.Allow()
	m.commandGrants.Allow("go test ./a")
	m.resumeSession(next.ID)
	if m.editGrant.Allowed() || m.toolRunOptions(0).EditGrant.Allowed() {
		t.Fatal("the edit grant carried into another session")
	}
	if m.commandGrants.Allowed("go test ./a") {
		t.Fatal("a command grant carried into another session")
	}
	if servers.Trusted("docs") {
		t.Fatal("MCP server trust carried into another session")
	}
}

func TestAutoEditMarker(t *testing.T) {
	for _, width := range []int{56, 80, 120} {
		m := newKeysTestUI(t)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		if strings.Contains(stripANSI(m.View()), "AUTO-EDIT") {
			t.Fatalf("%d cols: marker shown without a grant", width)
		}
		m.editGrant.Allow()
		m.layoutWidth = 0
		view := stripANSI(m.View())
		if !strings.Contains(view, "AUTO-EDIT") {
			t.Fatalf("%d cols: marker missing with the edit grant:\n%s", width, view)
		}
		assertViewport(t, m.View(), width, 24)
		m.planMode = true
		m.layoutWidth = 0
		view = stripANSI(m.View())
		if strings.Contains(view, "AUTO-EDIT") || !strings.Contains(view, "PLAN MODE") {
			t.Fatalf("%d cols: plan mode must show only PLAN MODE:\n%s", width, view)
		}
	}
}

const autoEditDiff = "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"

func TestAutoApprovedEditRendersDiffAndLabel(t *testing.T) {
	m := newKeysTestUI(t)
	call := model.ToolCall{ID: "call-1", Name: "edit_file", Arguments: `{"path":"a.go","content":"new\n"}`}
	content := agent.CommandApprovalPrefix + "auto-approved (" + agent.AutoApprovalReason + ")\nApplied edit to a.go"
	m.recordToolResult(call, tools.Result{Status: tools.Succeeded, Content: content, Diff: autoEditDiff}, "edit_file: "+content)
	record := m.toolRecords[len(m.toolRecords)-1]
	if record.Diff == "" {
		t.Fatal("auto-approved edit lost its diff")
	}
	m.layoutWidth = 0
	view := stripANSI(m.View())
	if !strings.Contains(view, "· auto-approved") {
		t.Fatalf("auto-approved label missing:\n%s", view)
	}
	if strings.Contains(view, agent.CommandApprovalPrefix) {
		t.Fatalf("stray approval line in the transcript:\n%s", view)
	}
}

func TestReviewedEditHasNoAutoLabel(t *testing.T) {
	m := newKeysTestUI(t)
	call := model.ToolCall{ID: "call-2", Name: "edit_file", Arguments: `{"path":"a.go","content":"new\n"}`}
	m.approvedEdits = map[string]string{call.ID: autoEditDiff}
	m.recordToolResult(call, tools.Result{Status: tools.Succeeded, Content: "Applied edit to a.go"}, "edit_file: Applied edit to a.go")
	if m.toolRecords[len(m.toolRecords)-1].Diff == "" {
		t.Fatal("reviewed edit lost its diff")
	}
	m.layoutWidth = 0
	if view := stripANSI(m.View()); strings.Contains(view, "auto-approved") {
		t.Fatalf("reviewed edit labelled auto-approved:\n%s", view)
	}
}

func TestRefusedAutoEditShowsNoDiff(t *testing.T) {
	m := newKeysTestUI(t)
	call := model.ToolCall{ID: "call-3", Name: "edit_file", Arguments: `{"path":"a.go","content":"new\n"}`}
	m.recordToolResult(call, tools.Result{Status: tools.Refused, Content: "stale proposal", Diff: autoEditDiff}, "edit_file: stale proposal")
	if diff := m.toolRecords[len(m.toolRecords)-1].Diff; diff != "" {
		t.Fatalf("refused auto edit claims a diff: %q", diff)
	}
}

func TestAutoApprovedEditFallbackSummaryStripsApprovalLine(t *testing.T) {
	record := session.ToolRecord{Name: "edit", Status: "succeeded", Content: agent.CommandApprovalPrefix + "auto-approved (" + agent.AutoApprovalReason + ")\nApplied 1 operation"}
	summary, preview, _ := toolSuccessSummary(record, "…", 3)
	if !strings.HasSuffix(summary, " · auto-approved") {
		t.Fatalf("summary = %q", summary)
	}
	for _, line := range preview {
		if strings.HasPrefix(line, agent.CommandApprovalPrefix) {
			t.Fatalf("preview kept the approval line: %q", preview)
		}
	}
}
