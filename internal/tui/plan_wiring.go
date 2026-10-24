package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"likha/internal/session"
	"likha/internal/tools"
)

// restorePlan adopts the persisted plan into the loaded snapshot. Formerly
// active work is marked interrupted when the session's last run never
// completed (session.Store.ResumablePlan): it was cancelled, failed, or died
// with the app. Completed statuses are preserved, nothing restarts, and no
// permission is restored. Revisited work is re-declared by a fresh
// plan_update.
func (m *ui) restorePlan() {
	if m.store == nil || m.snapshot.ID == "" {
		return
	}
	stored, found, err := m.store.ResumablePlan(m.snapshot.ID)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Private plan load failed: " + err.Error() + ". No plan was resumed."})
		return
	}
	if found {
		session.ResumePlan(&m.snapshot, &stored)
		return
	}
	session.ResumePlan(&m.snapshot, nil)
}

// runMarker is one agent run's session run marker
// (session.Store.MarkRunStarted): its token, whether the run began work
// (passed its prompt hooks while still live), and whether its goroutine
// wrote the marker row. A run can begin without the row: the write may fail.
type runMarker struct {
	store     *session.Store
	sessionID string
	token     session.RunToken
	began     atomic.Bool
	written   atomic.Bool
}

// newRunMarker prepares the marker of the run about to start, so a run cut
// short leaves its plan steps reported as interrupted on resume. The run's
// goroutine records it (start); finishRun settles it.
func (m *ui) newRunMarker() *runMarker {
	m.runMarker = &runMarker{store: m.store, sessionID: m.snapshot.ID, token: session.NewRunToken()}
	return m.runMarker
}

// start records, from the run's goroutine, that the run began working. The
// run has begun even when the marker row cannot be written.
func (r *runMarker) start() error {
	r.began.Store(true)
	if r.store == nil || r.sessionID == "" {
		return nil
	}
	if err := r.store.MarkRunStarted(r.sessionID, r.token); err != nil {
		return err
	}
	r.written.Store(true)
	return nil
}

// endRunMarker ends the active run's marker before finishRun saves the
// session, and returns it for clearRunMarker. A run that began work and was
// cancelled, failed, or force-stopped marks its active plan steps
// interrupted on the snapshot, so the save records the interruption on the
// plan itself and a later run that completes cannot make them look live
// again. A run that completed on its own interrupted nothing: a step it left
// in progress waits on the user.
func (m *ui) endRunMarker(completed bool) *runMarker {
	marker := m.runMarker
	m.runMarker = nil
	if marker == nil {
		return nil
	}
	if !completed && marker.began.Load() && session.MarkPlanInterrupted(&m.snapshot) {
		m.layoutWidth = 0
	}
	return marker
}

// clearRunMarker clears the ended run's marker once its session was saved:
// the save holds what the marker stood for. A failed save leaves the marker,
// as a run that died does, so resuming still reports the run's active plan
// steps interrupted.
func (m *ui) clearRunMarker(marker *runMarker, saved bool) {
	if marker == nil || !saved || !marker.written.Load() {
		return
	}
	if err := marker.store.MarkRunCompleted(marker.sessionID, marker.token); err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Run end not recorded: " + err.Error() + ". Resuming this session will report its in-progress plan steps as interrupted."})
	}
}

// syncPlanFromCall mirrors an accepted plan_update into the snapshot. Valid
// calls replace the whole list exactly as the tool applied them, including
// the visible clear; refusals and persistence failures leave the previous
// plan untouched, so the transcript and the checklist stay in step.
func (m *ui) syncPlanFromCall(arguments string, succeeded bool) {
	if !succeeded {
		return
	}
	var payload struct {
		Steps []tools.PlanStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
		return
	}
	steps := payload.Steps
	if steps == nil {
		steps = []tools.PlanStep{}
	}
	m.snapshot.Plan = &session.Plan{Steps: steps, UpdatedAt: time.Now().UTC()}
	m.layoutWidth = 0
}

// planSummaryView renders the /todo listing: a compact progress summary plus
// the complete inspectable list with non-color status text. An interrupted
// marker reports formerly active work without restarting anything.
func (m *ui) planSummaryView() string {
	plan := m.snapshot.Plan
	if plan == nil || len(plan.Steps) == 0 {
		if m.store == nil {
			return "No persisted plan. The private session store is unavailable."
		}
		return "No plan. The model can maintain one with plan_update."
	}
	counts := map[string]int{}
	for _, step := range plan.Steps {
		counts[strings.ToLower(step.Status)]++
	}
	var builder strings.Builder
	builder.WriteString("Plan progress: " +
		countSummary(len(plan.Steps), counts["in_progress"], counts["completed"], counts["blocked"], counts["pending"]))
	if plan.Interrupted {
		builder.WriteString(" — an earlier run was interrupted before these steps could be confirmed; nothing was restarted.")
	}
	if !plan.UpdatedAt.IsZero() {
		builder.WriteString(" Last update " + plan.UpdatedAt.Local().Format(time.DateTime) + ".")
	}
	for _, step := range plan.Steps {
		status := step.Status
		if status == "" {
			status = "?"
		}
		builder.WriteString("\n[" + status + "] " + step.ID + ": " + step.Title)
	}
	return builder.String()
}

func countSummary(total, active, done, blocked, pending int) string {
	summary := "empty plan"
	if total == 1 {
		summary = "1 step"
	} else if total > 1 {
		summary = fmt.Sprintf("%d steps", total)
	}
	return fmt.Sprintf("%s (%d active, %d completed, %d blocked, %d pending)", summary, active, done, blocked, pending)
}

const planQuestionMarker = "Question from the model:"

const (
	planAnsweredText    = "ask answered (text):"
	planAnsweredChoice  = "ask answered (choice):"
	planSkippedQuestion = "question skipped"
	planInterruptedAsk  = "Question interrupted before an answer"
	// planResumedAsk starts the note reportUnansweredQuestions saves; it
	// settles the question too, so a later resume adds no second note.
	planResumedAsk = "A model question from the previous run was interrupted before an answer"
)

// reportUnansweredQuestions records an app-level interruption for an ask_user
// request that was still pending when the previous run ended: the request and
// any completed answer are conversation events, an interrupted run never
// reopens the question, and no answer is replayed or inferred.
func (m *ui) reportUnansweredQuestions() {
	lastQuestion := -1
	for index := len(m.entries) - 1; index >= 0; index-- {
		if strings.HasPrefix(m.entries[index].content, planQuestionMarker) {
			lastQuestion = index
			break
		}
	}
	if lastQuestion < 0 {
		return
	}
	for _, current := range m.entries[lastQuestion+1:] {
		text := current.content
		if strings.HasPrefix(text, planAnsweredText) || strings.HasPrefix(text, planAnsweredChoice) ||
			strings.HasPrefix(text, planSkippedQuestion) || strings.HasPrefix(text, planInterruptedAsk) ||
			strings.HasPrefix(text, planResumedAsk) {
			return
		}
	}
	m.entries = append(m.entries, entry{role: "Likha", content: planResumedAsk + "; no response was sent."})
	m.persist()
}
