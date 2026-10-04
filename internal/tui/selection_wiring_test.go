package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/providers"
	"likha/internal/session"
)

func TestSelectionKeyboardExtensionAndCollapse(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "hello 世界")
	for range 2 {
		m.Update(tea.KeyMsg{Type: tea.KeyShiftLeft})
	}
	if got := m.selectedText(); got != "世界" {
		t.Fatalf("selected %q, want Unicode suffix", got)
	}
	if !strings.Contains(stripANSI(m.View()), "Alt+C copy") {
		t.Fatal("selection footer does not explain copying")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.textSelectionActive() || m.edit.caret != 6 {
		t.Fatalf("Left did not collapse to start: caret=%d", m.edit.caret)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	m.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.textSelectionActive() || m.edit.caret != len(m.input) {
		t.Fatal("Right did not collapse to selection end")
	}
}

func TestSelectionTypingPasteAndDeleteReplaceInput(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("X")},
		{Type: tea.KeyRunes, Runes: []rune("X\nY"), Paste: true},
		{Type: tea.KeyBackspace},
		{Type: tea.KeyDelete},
	} {
		m := newKeysTestUI(t)
		sendRunes(m, "hello world")
		m.edit.setSelection(m.input, 6, len(m.input))
		m.Update(key)
		want := "hello "
		if key.Type == tea.KeyRunes {
			want += strings.ReplaceAll(string(key.Runes), "\n", " ")
		}
		if got := string(m.input); got != want || m.textSelectionActive() {
			t.Fatalf("%s replaced selection with %q; want %q", key.String(), got, want)
		}
	}
}

func TestSelectionEscapePreservesDraftAndActiveRun(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "draft")
	m.edit.setSelection(m.input, 0, len(m.input))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.working, m.cancel = true, cancel
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.textSelectionActive() || string(m.input) != "draft" || m.cancelling || ctx.Err() != nil || m.escPrefix {
		t.Fatal("first Escape must only clear selection")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.cancelling || ctx.Err() == nil {
		t.Fatal("second Escape must retain run cancellation")
	}
}

func TestSelectionCtrlCStillCancelsRun(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "draft")
	m.edit.setSelection(m.input, 0, len(m.input))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.working, m.cancel = true, cancel
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.cancelling || ctx.Err() == nil {
		t.Fatal("selection intercepted Ctrl+C cancellation")
	}
}

func TestSelectionVerticalKeysNeverRecallHistory(t *testing.T) {
	m := newKeysTestUI(t)
	m.promptHistory.append("previous prompt")
	sendRunes(m, "draft")
	m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	if got := m.selectedText(); got != "draft" || string(m.input) != "draft" {
		t.Fatalf("Shift+Up changed history instead of selection: %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.edit.caret != 0 || m.textSelectionActive() || string(m.input) != "draft" {
		t.Fatal("ordinary Up did not collapse selection without recalling history")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if string(m.input) != "previous prompt" {
		t.Fatal("history navigation no longer works after selection collapses")
	}
}

func TestSelectionVerticalMovementUsesRenderedWideRuneBoundary(t *testing.T) {
	m := newKeysTestUI(t)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	m.composerStyle = "borderless"
	m.input = []rune(strings.Repeat("a", 39) + "界" + strings.Repeat("b", 80))
	m.edit.setSelection(m.input, 39, 39)
	m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	if m.edit.caret != 78 {
		t.Fatalf("Shift+Down caret = %d, want next rendered row start 78", m.edit.caret)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	if m.edit.caret != 39 || m.textSelectionActive() {
		t.Fatal("Shift+Up did not return to the wide-rune row start")
	}
}

func TestSelectionResizeOverlayAndSessionInvalidate(t *testing.T) {
	for _, change := range []string{"resize", "dialog", "session", "key", "inspection"} {
		t.Run(change, func(t *testing.T) {
			m := newKeysTestUI(t)
			sendRunes(m, "draft")
			m.edit.setSelection(m.input, 0, len(m.input))
			switch change {
			case "resize":
				m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
			case "dialog":
				m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
			case "session":
				// Actual in-place session changes occur inside Update; overlays
				// independently invalidate a selection before returning to it.
				m.snapshot.ID = "next-session"
				m.dialog.open = true
				m.Update(caretTickMsg{})
			case "key":
				m.keyModal.open = true
				m.Update(caretTickMsg{})
			case "inspection":
				m.toolInspector.open = true
				m.toolInspector.sessionID = m.snapshot.ID
				m.Update(caretTickMsg{})
			}
			if m.textSelectionActive() || string(m.input) != "draft" {
				t.Fatal("surface change left stale selection or changed the draft")
			}
		})
	}
}

func TestSelectionReviewKeysDoNotEditOrCopyDraft(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "draft")
	m.edit.setSelection(m.input, 0, len(m.input))
	m.working, m.runID = true, 1
	m.events = make(chan agent.TurnEvent, 1)
	request := &agent.ApprovalRequest{Kind: "command", Title: "Review", Body: strings.Repeat("command\n", 50), Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{Kind: "approval", RunID: 1, Approval: request})
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyShiftLeft},
		{Type: tea.KeyShiftRight},
		{Type: tea.KeyRunes, Runes: []rune("c"), Alt: true},
	} {
		_, cmd := m.Update(key)
		if cmd != nil || string(m.input) != "draft" || m.textSelectionActive() || len(request.Reply) != 0 {
			t.Fatal("selection key escaped review gate")
		}
	}
}

func TestSelectionReviewScrollbarRetainsCapturedDragAcrossFrames(t *testing.T) {
	m := newKeysTestUI(t)
	m.working, m.runID = true, 1
	m.events = make(chan agent.TurnEvent, 1)
	request := &agent.ApprovalRequest{Kind: "command", Title: "Review", Body: strings.Repeat("full proposal\n", 70), Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{Kind: "approval", RunID: 1, Approval: request})
	start := len(m.header())
	body := m.bodyHeight()
	m.Update(tea.MouseMsg{X: m.width - 1, Y: start + body/2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.View()
	if !m.selection.scrollbarDragging {
		t.Fatal("review frame cleared the scrollbar's captured press")
	}
	// A captured thumb continues dragging even when the pointer leaves its
	// original column; no text selection or approval can result from it.
	m.Update(tea.MouseMsg{X: m.width - 10, Y: start + body - 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m.View()
	if m.scroll != m.scrollMax() || m.textSelectionActive() || len(request.Reply) != 0 {
		t.Fatal("review scrollbar lost drag ownership or replied to the review")
	}
	m.Update(tea.MouseMsg{X: m.width - 10, Y: start + body - 1, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if m.selection.scrollbarDragging {
		t.Fatal("review scrollbar did not release mouse capture")
	}
}

func TestSelectionCopyIsAsyncImmutableAndNotAConversationTurn(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "hello world")
	m.edit.setSelection(m.input, 6, len(m.input))
	entriesBefore, historyBefore := len(m.entries), len(m.history)
	statusBefore := m.status
	written := ""
	m.clipboard.writer = func(text string) error {
		written = text
		return nil
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c"), Alt: true})
	if cmd == nil || written != "" {
		t.Fatal("copy must schedule clipboard work off the UI update")
	}
	// A draft edit while the command is queued must not change its payload.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("friend")})
	m.Update(cmd())
	if written != "world" || string(m.input) != "hello friend" || len(m.entries) != entriesBefore || len(m.history) != historyBefore || m.working || m.status != statusBefore {
		t.Fatal("copy changed the conversation or captured a mutable draft")
	}
	if !strings.Contains(stripANSI(m.View()), "Copied selection") {
		t.Fatal("successful copy has no visible feedback")
	}
}

func TestSelectionCopyWithoutSelectionDoesNotInsertAltC(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "draft")
	wrote := false
	m.clipboard.writer = func(string) error { wrote = true; return nil }
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c"), Alt: true})
	if wrote || string(m.input) != "draft" {
		t.Fatal("Alt+C inserted a character or copied an unselected draft")
	}
	if cmd != nil {
		m.Update(cmd())
	}
}

func TestSelectionPopupEscapeClearsRangeBeforeDismissingPopup(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "/help")
	if !m.commandPopup.open {
		t.Fatal("fixture did not open command popup")
	}
	m.edit.setSelection(m.input, 0, len(m.input))
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.textSelectionActive() || !m.commandPopup.open || string(m.input) != "/help" {
		t.Fatal("Escape bypassed selection and dismissed the popup")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.commandPopup.open {
		t.Fatal("second Escape did not dismiss the popup")
	}
}

func TestSelectionPopupArrowsCollapseRangeBeforeNavigatingCompletion(t *testing.T) {
	for _, popup := range []string{"command", "mention"} {
		for _, key := range []tea.KeyType{tea.KeyUp, tea.KeyDown} {
			t.Run(popup+"/"+(tea.KeyMsg{Type: key}).String(), func(t *testing.T) {
				m := newKeysTestUI(t)
				if popup == "command" {
					sendRunes(m, "/")
					m.commandPopup.cursor = 1
				} else {
					sendRunes(m, "@file")
					m.mention = mentionState{open: true, matches: []string{"file-a", "file-b", "file-c"}, cursor: 1}
				}
				m.edit.setSelection(m.input, 0, len(m.input))
				m.Update(tea.KeyMsg{Type: key})
				cursor := m.commandPopup.cursor
				if popup == "mention" {
					cursor = m.mention.cursor
				}
				if m.textSelectionActive() || cursor != 1 {
					t.Fatal("completion popup intercepted selection collapse")
				}
				m.Update(tea.KeyMsg{Type: key})
				cursor = m.commandPopup.cursor
				if popup == "mention" {
					cursor = m.mention.cursor
				}
				want := 0
				if key == tea.KeyDown {
					want = 2
				}
				if cursor != want {
					t.Fatal("arrow did not resume completion navigation after selection collapsed")
				}
			})
		}
	}
}

func TestSelectionApprovalRebuildsReviewBeforeCountingPages(t *testing.T) {
	m := newKeysTestUI(t)
	m.entries = append(m.entries, entry{role: "Assistant", content: "answer to copy"})
	m.View()
	y := len(m.header()) + m.entryLines[len(m.entries)-1] - m.scroll
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 2, Y: y})
	m.Update(tea.MouseMsg{Type: tea.MouseMotion, X: 8, Y: y})
	m.Update(tea.MouseMsg{Type: tea.MouseRelease, X: 8, Y: y})
	if !m.textSelectionActive() {
		t.Fatal("fixture did not select output")
	}
	m.working, m.runID = true, 1
	m.events = make(chan agent.TurnEvent, 1)
	request := &agent.ApprovalRequest{Kind: "command", Title: "Review", Body: strings.Repeat("full proposal\n", 70), Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{Kind: "approval", RunID: 1, Approval: request})
	if m.textSelectionActive() || len(m.reviewSeen) <= 1 || !strings.Contains(strings.Join(m.lines, "\n"), "full proposal") {
		t.Fatal("frozen output leaked into approval page accounting")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(request.Reply) != 0 {
		t.Fatal("output selection accidentally bypassed the review gate")
	}
}

func TestSelectionOutputSnapshotReleasesBeforePopupNavigation(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "/")
	m.entries = append(m.entries, entry{role: "Assistant", content: "answer to copy"})
	m.View()
	y := len(m.header()) + m.entryLines[len(m.entries)-1] - m.scroll
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 2, Y: y})
	m.Update(tea.MouseMsg{Type: tea.MouseMotion, X: 8, Y: y})
	m.Update(tea.MouseMsg{Type: tea.MouseRelease, X: 8, Y: y})
	if !m.textSelectionActive() || !m.commandPopup.open {
		t.Fatal("fixture did not select output with completion open")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.textSelectionActive() || m.commandPopup.cursor != 1 || string(m.input) != "/" {
		t.Fatal("completion navigation kept the output frame frozen")
	}
}

func TestSelectionSessionResumeInvalidatesRangeAndQueuedClipboardWrite(t *testing.T) {
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
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	sendRunes(m, "draft")
	m.edit.setSelection(m.input, 0, len(m.input))
	wrote := false
	m.clipboard.writer = func(string) error { wrote = true; return nil }
	queuedCopy := m.copyText(m.selectedText())
	m.dialog = dialogState{kind: dialogSessions, open: true}
	m.dialogItems = []string{"next session"}
	m.sessionIDs = []string{next.ID}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.snapshot.ID != next.ID || m.textSelectionActive() {
		t.Fatal("resuming a session retained the old copy target")
	}
	m.Update(queuedCopy())
	if wrote || m.clipboardHint() != "" {
		t.Fatal("a superseded session copy wrote the clipboard or showed stale feedback")
	}
}
