package tui

import (
	"database/sql"
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

	"likha/internal/model"
	"likha/internal/session"
)

// planResumeSession stores a session whose plan has a step in progress, as
// a run's plan_update and the following save leave it.
func planResumeSession(t *testing.T) (*session.Store, string, string, session.Snapshot) {
	t.Helper()
	root, stateDir := t.TempDir(), t.TempDir()
	store, err := session.Open(stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	plan := session.Plan{Steps: []session.PlanStep{
		{ID: "read", Title: "Read the code", Status: string(session.PlanCompleted)},
		{ID: "ask", Title: "Ask which fix to apply", Status: string(session.PlanInProgress)},
	}, UpdatedAt: time.Now().UTC()}
	if err := store.SavePlan(snapshot.ID, plan); err != nil {
		t.Fatal(err)
	}
	snapshot.Plan = &plan
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	return store, root, stateDir, snapshot
}

// relaunch opens the stored session in a new UI, as starting Likha again
// with it does.
func relaunch(t *testing.T, store *session.Store, root, stateDir, id string) *ui {
	t.Helper()
	snapshot, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	return hookUI(t, root, stateDir, nil, store, snapshot)
}

// A run that completes can leave a plan step in progress (it waits on the
// user's answer). Quitting and starting Likha again later must not report
// that step as interrupted; a run that died mid-step still is.
func TestPlanResumesInterruptedOnlyAfterAnUnfinishedRun(t *testing.T) {
	store, root, stateDir, snapshot := planResumeSession(t)
	var markedDuringRun atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		unfinished, err := store.RunUnfinished(snapshot.ID)
		markedDuringRun.Store(err == nil && unfinished)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Which fix should I apply?\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "hook-model", "")
	if err != nil {
		t.Fatal(err)
	}
	m := hookUI(t, root, stateDir, client, store, snapshot)
	m.input = []rune("fix the bug")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	permE2Epump(t, m, func() bool { return !m.working })
	if !markedDuringRun.Load() {
		t.Fatal("the run was not recorded as started while it worked")
	}

	resumed := relaunch(t, store, root, stateDir, snapshot.ID)
	if resumed.snapshot.Plan == nil || resumed.snapshot.Plan.Interrupted {
		t.Fatalf("plan after a completed run = %+v, want it resumed without an interruption", resumed.snapshot.Plan)
	}
	if view := resumed.planSummaryView(); strings.Contains(view, "interrupted") {
		t.Fatalf("/todo after a completed run reports an interruption: %s", view)
	}

	// A run that starts and never completes (the app was killed) leaves its
	// marker, so the same plan resumes interrupted.
	if err := store.MarkRunStarted(snapshot.ID, session.NewRunToken()); err != nil {
		t.Fatal(err)
	}
	killed := relaunch(t, store, root, stateDir, snapshot.ID)
	if killed.snapshot.Plan == nil || !killed.snapshot.Plan.Interrupted {
		t.Fatalf("plan after a killed run = %+v, want it marked interrupted", killed.snapshot.Plan)
	}
	if got := killed.snapshot.Plan.Steps[1].Status; got != string(session.PlanInProgress) {
		t.Fatalf("interrupted step status = %q, want it kept in progress", got)
	}
	if view := killed.planSummaryView(); !strings.Contains(view, "an earlier run was interrupted") {
		t.Fatalf("/todo after a killed run does not report the interruption: %s", view)
	}
}

// A cancelled run cut its in-progress step short, so resuming reports it
// interrupted (specs/plan-todo: cancel and resume).
func TestPlanResumesInterruptedAfterACancelledRun(t *testing.T) {
	store, root, stateDir, snapshot := planResumeSession(t)
	requested, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		select {
		case requested <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	client, err := model.New(server.URL+"/v1", "hook-model", "")
	if err != nil {
		t.Fatal(err)
	}
	m := hookUI(t, root, stateDir, client, store, snapshot)
	m.input = []rune("fix the bug")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case <-requested:
	case <-time.After(10 * time.Second):
		t.Fatal("the run sent no request")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	permE2Epump(t, m, func() bool { return !m.working })

	resumed := relaunch(t, store, root, stateDir, snapshot.ID)
	if resumed.snapshot.Plan == nil || !resumed.snapshot.Plan.Interrupted {
		t.Fatalf("plan after a cancelled run = %+v, want it marked interrupted", resumed.snapshot.Plan)
	}
}

// hangThenAnswerModel holds its first request until the client gives up on
// it and answers every later one at once; requested reports each request.
func hangThenAnswerModel(t *testing.T) (*model.Client, chan struct{}) {
	t.Helper()
	requested, done := make(chan struct{}, 4), make(chan struct{})
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		requested <- struct{}{}
		if count.Add(1) == 1 {
			select {
			case <-r.Context().Done():
			case <-done:
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(done) })
	client, err := model.New(server.URL+"/v1", "hook-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return client, requested
}

func waitFor(t *testing.T, signal chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal(what)
	}
}

// A run cancelled mid-step cut that step short. A later run that completes
// without a plan_update must not make it look live again: the interruption
// is saved with the plan when the cancelled run ends.
func TestCancelledRunInterruptionSurvivesALaterCompletedRun(t *testing.T) {
	store, root, stateDir, snapshot := planResumeSession(t)
	client, requested := hangThenAnswerModel(t)
	m := hookUI(t, root, stateDir, client, store, snapshot)
	m.input = []rune("fix the bug")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, requested, "the first run sent no request")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	permE2Epump(t, m, func() bool { return !m.working })
	if stored, _, err := store.LoadPlan(snapshot.ID); err != nil || !stored.Interrupted {
		t.Fatalf("stored plan after the cancel = %+v (err %v), want it interrupted", stored, err)
	}

	m.input = []rune("what is left?")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, requested, "the second run sent no request")
	permE2Epump(t, m, func() bool { return !m.working })
	if unfinished, err := store.RunUnfinished(snapshot.ID); err != nil || unfinished {
		t.Fatalf("run marker after the completed run = %v (err %v), want cleared", unfinished, err)
	}

	resumed := relaunch(t, store, root, stateDir, snapshot.ID)
	if resumed.snapshot.Plan == nil || !resumed.snapshot.Plan.Interrupted {
		t.Fatalf("plan after cancel then completion = %+v, want it still interrupted", resumed.snapshot.Plan)
	}
	if view := resumed.planSummaryView(); !strings.Contains(view, "an earlier run was interrupted") {
		t.Fatalf("/todo does not report the interruption: %s", view)
	}
}

// A run cancelled while its prompt_submit hook still runs started no work:
// it records no run marker, and its plan steps are not reported interrupted.
func TestRunCancelledDuringItsPromptHookRecordsNoMarker(t *testing.T) {
	store, root, stateDir, snapshot := planResumeSession(t)
	started := filepath.Join(t.TempDir(), "started")
	guard := hookShell(t, "touch '"+started+"'; sleep 5")
	writeHooks(t, stateDir, hooksJSON(t, "prompt_submit", "slow", guard, ""))
	client, count := hookModel(t, "")
	m := hookUI(t, root, stateDir, client, store, snapshot)
	m.input = []rune("fix the bug")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prompt_submit hook never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	permE2Epump(t, m, func() bool { return !m.working })
	if count.Load() != 0 {
		t.Fatalf("the cancelled run sent %d requests", count.Load())
	}
	if unfinished, err := store.RunUnfinished(snapshot.ID); err != nil || unfinished {
		t.Fatalf("run marker after a cancel during the prompt hook = %v (err %v), want none", unfinished, err)
	}
	if stored, _, err := store.LoadPlan(snapshot.ID); err != nil || stored.Interrupted {
		t.Fatalf("stored plan = %+v (err %v), want it not interrupted", stored, err)
	}
}

// entriesContain reports whether a transcript entry holds text.
func entriesContain(m *ui, text string) bool {
	for _, current := range m.entries {
		if strings.Contains(current.content, text) {
			return true
		}
	}
	return false
}

// injectFailure makes every statement the trigger watches fail in the
// session database at stateDir, as a broken store would, until the test
// ends or drop is called.
func injectFailure(t *testing.T, stateDir, trigger string) (drop func()) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TRIGGER injected_failure ` + trigger + ` BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := db.Exec(`DROP TRIGGER injected_failure`); err != nil {
			t.Fatal(err)
		}
	}
}

// A cancelled run saves its plan interrupted and then clears its own run
// marker: the plan row carries the interruption, so only a run that dies
// before its save leaves a marker.
func TestCancelledRunClearsItsMarkerAfterItsSave(t *testing.T) {
	store, root, stateDir, snapshot := planResumeSession(t)
	client, requested := hangThenAnswerModel(t)
	m := hookUI(t, root, stateDir, client, store, snapshot)
	m.input = []rune("fix the bug")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, requested, "the run sent no request")
	if unfinished, err := store.RunUnfinished(snapshot.ID); err != nil || !unfinished {
		t.Fatalf("run marker while the run works = %v (err %v), want set", unfinished, err)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	permE2Epump(t, m, func() bool { return !m.working })
	if stored, _, err := store.LoadPlan(snapshot.ID); err != nil || !stored.Interrupted {
		t.Fatalf("stored plan after the cancel = %+v (err %v), want it interrupted", stored, err)
	}
	if unfinished, err := store.RunUnfinished(snapshot.ID); err != nil || unfinished {
		t.Fatalf("run marker after the cancelled run's save = %v (err %v), want cleared", unfinished, err)
	}
}

// A run whose marker could not be written still began work, so cancelling
// it cuts its active plan steps short all the same.
func TestCancelledRunMarksItsPlanInterruptedWithoutAMarkerRow(t *testing.T) {
	store, root, stateDir, snapshot := planResumeSession(t)
	injectFailure(t, stateDir, "BEFORE INSERT ON run_markers")
	client, requested := hangThenAnswerModel(t)
	m := hookUI(t, root, stateDir, client, store, snapshot)
	m.input = []rune("fix the bug")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, requested, "the run sent no request")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	permE2Epump(t, m, func() bool { return !m.working })
	if !entriesContain(m, "Run start not recorded") {
		t.Fatal("the failed marker write was not reported")
	}
	if stored, _, err := store.LoadPlan(snapshot.ID); err != nil || !stored.Interrupted {
		t.Fatalf("stored plan after the cancel = %+v (err %v), want it interrupted", stored, err)
	}
}

// A completed run whose session save fails keeps its run marker: the
// interruption resume marked in memory was never saved, so clearing the
// marker first would lose it.
func TestFailedSaveKeepsTheRunMarker(t *testing.T) {
	store, root, stateDir, snapshot := planResumeSession(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "hook-model", "")
	if err != nil {
		t.Fatal(err)
	}
	// An earlier run died mid-step, so this launch resumes the plan
	// interrupted, in memory only until the next save.
	if err := store.MarkRunStarted(snapshot.ID, session.NewRunToken()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := hookUI(t, root, stateDir, client, store, loaded)
	if m.snapshot.Plan == nil || !m.snapshot.Plan.Interrupted {
		t.Fatalf("resumed plan = %+v, want it interrupted", m.snapshot.Plan)
	}
	drop := injectFailure(t, stateDir, "BEFORE UPDATE ON sessions")
	m.input = []rune("what is left?")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	permE2Epump(t, m, func() bool { return !m.working })
	if !entriesContain(m, "Session was not saved") {
		t.Fatal("the failed save was not reported")
	}
	drop()
	if unfinished, err := store.RunUnfinished(snapshot.ID); err != nil || !unfinished {
		t.Fatalf("run marker after a failed save = %v (err %v), want kept", unfinished, err)
	}
	resumed := relaunch(t, store, root, stateDir, snapshot.ID)
	if resumed.snapshot.Plan == nil || !resumed.snapshot.Plan.Interrupted {
		t.Fatalf("plan after a run whose save failed = %+v, want it still interrupted", resumed.snapshot.Plan)
	}
}
