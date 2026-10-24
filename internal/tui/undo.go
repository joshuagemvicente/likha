package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/actions"
	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/runsetup"
	"likha/internal/session"
	"likha/internal/tools"
)

// undoReviewKind is the pending-review kind of an /undo confirmation. It is
// the user's own action: Approve always never covers it (specs/edit-undo D2).
const undoReviewKind = "undo"

// undoReview is the change set a pending /undo review would revert.
type undoReview struct {
	record   session.ChangeSetRecord
	proposal *actions.UndoProposal
}

const (
	undoArgumentError  = "/undo takes no arguments; it reverts Likha's most recent file change."
	undoNothing        = "Nothing to undo."
	undoNothingDropped = "Nothing to undo. Older changes in this session can no longer be undone."
	undoCommandsRan    = "Commands ran after this change. Their effects are not undone."
)

// handleUndoCommand opens a review that reverts the session's most recent
// remaining change set (specs/edit-undo). It writes nothing by itself.
func (m *ui) handleUndoCommand(line, arg string) tea.Cmd {
	if arg != "" {
		m.input = []rune(line)
		m.edit.endCaret(m.input)
		m.entries = append(m.entries, entry{role: "Error", content: undoArgumentError})
		return nil
	}
	if m.working {
		m.entries = append(m.entries, entry{role: "Error", content: runDraftCommandRefusal})
		return nil
	}
	if m.pending != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "A review is pending; resolve it before /undo."})
		return nil
	}
	if m.store == nil || m.snapshot.ID == "" {
		m.entries = append(m.entries, entry{role: "Error", content: "No session store available; nothing to undo."})
		return nil
	}
	record, ok, err := m.store.LatestChangeSet(m.snapshot.ID)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Undo history unavailable: " + err.Error() + ". Nothing was reverted."})
		return nil
	}
	if !ok {
		message := undoNothing
		if dropped, err := m.store.ChangeSetsDropped(m.snapshot.ID); err == nil && dropped {
			message = undoNothingDropped
		}
		m.entries = append(m.entries, entry{role: "Likha", content: message})
		return nil
	}
	var set actions.ChangeSet
	if err := json.Unmarshal(record.Payload, &set); err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Undo history is damaged; nothing was reverted."})
		return nil
	}
	proposal, changed, err := actions.PrepareUndo(m.root, set)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Can't undo: " + err.Error() + ". Nothing was reverted."})
		return nil
	}
	if len(changed) > 0 {
		m.entries = append(m.entries, entry{role: "Error", content: changedSinceWritten(changed)})
		return nil
	}
	if err := proposal.SetJournal(runsetup.EditJournal(m.store, m.snapshot.ID)); err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Undo unavailable: " + err.Error() + ". Nothing was reverted."})
		return nil
	}
	m.openUndoReview(record, proposal)
	return nil
}

func changedSinceWritten(paths []string) string {
	if len(paths) == 1 {
		return "Can't undo: " + paths[0] + " changed since Likha wrote it. Nothing was reverted."
	}
	return "Can't undo: " + strings.Join(paths, ", ") + " changed since Likha wrote them. Nothing was reverted."
}

// openUndoReview shows the reverse diff in the pending review slot, with
// the same page-seen gate as an edit review and Undo/Cancel buttons.
func (m *ui) openUndoReview(record session.ChangeSetRecord, proposal *actions.UndoProposal) {
	request := &agent.ApprovalRequest{
		Kind:  undoReviewKind,
		Title: fmt.Sprintf("Undo: %d file(s) from %s", len(proposal.Paths), undoTime(record)),
		Body:  undoReviewBody(proposal),
		Reply: make(chan bool, 1),
	}
	if m.commandsRanAfter(record.ToolCallID) {
		request.Warning = undoCommandsRan
	}
	m.clearTextSelection()
	m.undo = &undoReview{record: record, proposal: proposal}
	m.pending = request
	m.scroll = 0 // the review starts at the top of the reverse diff
	m.following = false
	m.layoutWidth = 0
	m.reviewSeen = make([]bool, m.pageCount())
	m.reviewFocus = focusApprove
	m.markSeenFromScroll()
	m.status = "Review undo before applying"
}

func undoTime(record session.ChangeSetRecord) string {
	return record.Created.Local().Format("2006-01-02 15:04:05")
}

func undoReviewBody(proposal *actions.UndoProposal) string {
	var body strings.Builder
	if len(proposal.Restored) > 0 {
		body.WriteString("Restore to before Likha's change:\n" + strings.Join(proposal.Restored, "\n") + "\n\n")
	}
	if len(proposal.Removed) > 0 {
		body.WriteString("Remove (created by Likha):\n" + strings.Join(proposal.Removed, "\n") + "\n\n")
	}
	if len(proposal.Directories) > 0 {
		body.WriteString("Remove if empty (created by Likha):\n" + strings.Join(proposal.Directories, "\n") + "\n\n")
	}
	body.WriteString(proposal.Diff)
	return body.String()
}

// commandsRanAfter reports whether a shell command ran after the change
// set's tool call. A call no longer in the records counts every command.
func (m *ui) commandsRanAfter(callID string) bool {
	start := 0
	for i, record := range m.toolRecords {
		if callID != "" && record.CallID == callID {
			start = i + 1
		}
	}
	for _, record := range m.toolRecords[start:] {
		if record.Name == "run_command" && record.Status != string(tools.Refused) {
			return true
		}
	}
	return false
}

// confirmUndo handles Enter on the undo review's decision bar.
func (m *ui) confirmUndo() {
	if m.reviewFocus != focusApprove {
		m.cancelUndo()
		return
	}
	if m.width < minWidth || m.height < minHeight {
		return
	}
	if !m.reviewReady() {
		m.status = reviewGateStatus
		return
	}
	m.applyUndo()
}

// closeUndoReview clears the review slot and returns what it held.
func (m *ui) closeUndoReview() *undoReview {
	review := m.undo
	m.undo, m.pending, m.reviewSeen = nil, nil, nil
	m.reviewFocus = focusApprove
	m.status = "Ready"
	m.jumpBottom()
	m.layoutWidth = 0
	return review
}

func (m *ui) cancelUndo() {
	m.closeUndoReview()
	m.entries = append(m.entries, entry{role: "Likha", content: "Undo cancelled; nothing was written."})
}

func (m *ui) applyUndo() {
	review := m.closeUndoReview()
	proposal := review.proposal
	result, err := proposal.Apply(context.Background())
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: undoFailure(result, err)})
		m.persist()
		return
	}
	text := fmt.Sprintf("Undid %d file(s) from %s: %s. Effects of shell commands are not undone.",
		len(proposal.Paths), undoTime(review.record), strings.Join(proposal.Paths, ", "))
	if len(result.KeptDirectories) > 0 {
		text += " Kept non-empty directories: " + strings.Join(result.KeptDirectories, ", ") + "."
	}
	m.entries = append(m.entries, entry{role: "Likha", content: text})
	if err := m.store.DeleteChangeSet(m.snapshot.ID, review.record.ID); err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "The undo was applied, but its history entry could not be removed: " + err.Error() +
			". A repeated /undo refuses because the files no longer match."})
	}
	// The note joins the conversation the next request sends; it names
	// paths only, never file content (specs/edit-undo point 13).
	m.history = append(m.history, model.Message{Role: "developer", Content: undoNote(proposal)})
	m.persist()
}

func undoFailure(result actions.UndoResult, err error) string {
	switch result.Status {
	case "unchanged", "recovered":
		if errors.Is(err, actions.ErrStaleEdit) {
			return "Can't undo: a file changed since the review. Nothing was reverted."
		}
		return "Undo failed: " + err.Error() + ". Nothing was reverted."
	default:
		return "Undo was interrupted: " + err.Error() + ". Some files may already be reverted; /tools recovery shows the private record. Nothing is replayed."
	}
}

func undoNote(proposal *actions.UndoProposal) string {
	var parts []string
	if len(proposal.Restored) > 0 {
		parts = append(parts, "restored "+strings.Join(proposal.Restored, ", ")+" to their content before Likha's edit")
	}
	if len(proposal.Removed) > 0 {
		parts = append(parts, "removed "+strings.Join(proposal.Removed, ", ")+" (created by Likha)")
	}
	return "The user ran /undo and reverted an earlier Likha file change: " + strings.Join(parts, "; ") +
		". Earlier tool results and file contents for these paths are out of date; read the files again before relying on them. Effects of shell commands were not undone."
}
