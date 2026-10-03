package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"likha/internal/session"
	"likha/internal/tools"
)

// restorePlan adopts the persisted plan into the loaded snapshot and marks
// formerly active work as interrupted when that plan was last updated before
// this app launch — a mid-run update survived the run but the run itself did
// not. Completed statuses are preserved, nothing restarts, and no permission
// is restored. Revisited work is re-declared by a fresh plan_update.
func (m *ui) restorePlan() {
	if m.store == nil || m.snapshot.ID == "" {
		return
	}
	stored, found, err := m.store.LoadPlan(m.snapshot.ID)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Private plan load failed: " + err.Error() + ". No plan was resumed."})
		return
	}
	if found {
		stored.Interrupted = stored.Interrupted ||
			(planHasActiveStep(stored.Steps) && stored.UpdatedAt.Before(m.started))
		session.ResumePlan(&m.snapshot, &stored)
		return
	}
	session.ResumePlan(&m.snapshot, nil)
}

func planHasActiveStep(steps []tools.PlanStep) bool {
	for _, step := range steps {
		status := tools.PlanStepStatus(step.Status)
		if status == tools.PlanInProgress || status == tools.PlanBlocked {
			return true
		}
	}
	return false
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
			strings.HasPrefix(text, planSkippedQuestion) || strings.HasPrefix(text, planInterruptedAsk) {
			return
		}
	}
	m.entries = append(m.entries, entry{role: "Likha", content: "A model question from the previous run was interrupted before an answer; no response was sent."})
	m.persist()
}
