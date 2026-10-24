package headless

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"likha/internal/session"
)

// runUnfinished reports whether the run's session holds a run marker.
func runUnfinished(t *testing.T, run testRun) bool {
	t.Helper()
	unfinished, err := run.store.RunUnfinished(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	return unfinished
}

// storedPlanInterrupted reports whether the stored plan row is marked
// interrupted.
func storedPlanInterrupted(t *testing.T, run testRun) bool {
	t.Helper()
	stored, found, err := run.store.LoadPlan(run.cfg.Snapshot.ID)
	if err != nil || !found {
		t.Fatalf("stored plan: found %v, err %v", found, err)
	}
	return stored.Interrupted
}

// A marker stamped ahead of the clock (the clock stepped back since) is
// replaced by the next run, which clears it when it completes.
func TestCompletedRunClearsAMarkerStampedAheadOfTheClock(t *testing.T) {
	run := newTestRun(t, newFakeProvider(t, textReply("done")), "continue")
	run.cfg.Snapshot = sessionWithPlan(t, run, time.Now().UTC())
	ahead := session.RunToken(time.Now().Add(time.Hour).UnixNano())
	if err := run.store.MarkRunStarted(run.cfg.Snapshot.ID, ahead); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runHeadless(t, context.Background(), run.cfg); code != ExitCompleted {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	if runUnfinished(t, run) {
		t.Fatal("the completed run left the marker stamped ahead of the clock")
	}
}

// A run that is cancelled saves its plan interrupted and then clears its own
// marker: the plan row carries the interruption from then on.
func TestCancelledRunClearsItsMarkerAfterItsSave(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	fake := newFakeProvider(t, hangReply(release))
	run := newTestRun(t, fake, "fix it")
	run.cfg.Snapshot = sessionWithPlan(t, run, time.Now().UTC())
	ctx, cancel := context.WithCancelCause(context.Background())
	markedDuringRun := make(chan bool, 1)
	go func() {
		for len(fake.requests()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		unfinished, err := run.store.RunUnfinished(run.cfg.Snapshot.ID)
		markedDuringRun <- err == nil && unfinished
		cancel(ErrInterrupted)
	}()
	if code, _, stderr := runHeadless(t, ctx, run.cfg); code != ExitInterrupted {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	if !<-markedDuringRun {
		t.Fatal("the run was not marked while it worked")
	}
	if !storedPlanInterrupted(t, run) {
		t.Fatal("the cancelled run did not save its plan interrupted")
	}
	if runUnfinished(t, run) {
		t.Fatal("the cancelled run left its marker after its save")
	}
}

// A run whose session could not be saved at its end keeps its marker, as a
// run that died does: resuming reports its plan steps interrupted.
func TestRunThatCannotSaveKeepsItsMarker(t *testing.T) {
	run := newTestRun(t, newFakeProvider(t, textReply("done")), "continue")
	run.cfg.Snapshot = sessionWithPlan(t, run, time.Now().UTC())
	db, err := sql.Open("sqlite", filepath.Join(run.cfg.StateDir, "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER reject_save BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected save failure'); END`); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runHeadless(t, context.Background(), run.cfg); code == ExitCompleted {
		t.Fatalf("a run whose save failed exited 0\n%s", stderr)
	}
	if !runUnfinished(t, run) {
		t.Fatal("a run that could not save its end cleared its marker")
	}
	resumed, found, err := run.store.ResumablePlan(run.cfg.Snapshot.ID)
	if err != nil || !found || !resumed.Interrupted {
		t.Fatalf("resumable plan = %+v (found %v, err %v), want it interrupted", resumed, found, err)
	}
}

// A run cancelled while its prompt_submit hook still runs began no work, as
// in the TUI: it records no marker, does not mark its plan interrupted, and
// exits as interrupted.
func TestRunCancelledDuringItsPromptHookCutsNothingShort(t *testing.T) {
	fake := newFakeProvider(t, textReply("never sent"))
	run := newTestRun(t, fake, "fix it")
	run.cfg.Snapshot = sessionWithPlan(t, run, time.Now().UTC())
	started := filepath.Join(t.TempDir(), "started")
	writeHooks(t, run.cfg.StateDir, map[string][]map[string]any{
		"prompt_submit": {{"name": "slow", "command": "touch '" + started + "'; sleep 5"}},
	})
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel(ErrInterrupted)
	}()
	code, stdout, stderr := runHeadless(t, ctx, run.cfg)
	if code != ExitInterrupted {
		t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitInterrupted, stdout, stderr)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatalf("the prompt_submit hook never started: %v", err)
	}
	if requests := fake.requests(); len(requests) != 0 {
		t.Fatalf("the cancelled run sent %d requests", len(requests))
	}
	if runUnfinished(t, run) {
		t.Fatal("a run cancelled during its prompt hook left a marker")
	}
	if storedPlanInterrupted(t, run) {
		t.Fatal("a run cancelled during its prompt hook marked its plan interrupted")
	}
}
