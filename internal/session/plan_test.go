package session

import (
	"encoding/json"
	"testing"
	"time"
)

// A mid-run save leaves the snapshot and the plans row holding the same list.
// Resuming after that run died must still show its active steps interrupted.
func TestResumePlanMarksCurrentSnapshotPlanInterrupted(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Steps: []PlanStep{
		{ID: "read", Title: "Read the code", Status: string(PlanCompleted)},
		{ID: "fix", Title: "Fix the bug", Status: string(PlanInProgress)},
	}, UpdatedAt: time.Now().UTC()}
	if err := store.SavePlan(snapshot.ID, plan); err != nil {
		t.Fatal(err)
	}
	snapshot.Plan = &plan
	snapshot.Entries = []Entry{{Role: "user", Content: "Fix the bug"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}

	resumed, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := store.LoadPlan(snapshot.ID)
	if err != nil || !found {
		t.Fatalf("LoadPlan: found %v, err %v", found, err)
	}
	// The coordinator marks a plan whose active steps predate this launch.
	stored.Interrupted = true
	if !ResumePlan(&resumed, &stored) {
		t.Fatal("ResumePlan reported no change for an interrupted plan")
	}
	if resumed.Plan == nil || !resumed.Plan.Interrupted {
		t.Fatalf("resumed plan %+v is not marked interrupted", resumed.Plan)
	}
	if got := resumed.Plan.Steps[1].Status; got != string(PlanInProgress) {
		t.Fatalf("interrupted step status = %q, want it kept as %q", got, PlanInProgress)
	}
	if ResumePlan(&resumed, &stored) {
		t.Fatal("a repeated ResumePlan with unchanged input changed the snapshot")
	}
}

// A stored plan whose steps differ from the newer snapshot plan is stale; its
// interruption marker belongs to a list the snapshot no longer shows.
func TestResumePlanKeepsNewerDifferentSnapshotPlan(t *testing.T) {
	now := time.Now().UTC()
	snapshot := Snapshot{ID: "0123456789abcdef0123456789abcdef", Plan: &Plan{
		Steps: []PlanStep{{ID: "next", Title: "Next step", Status: string(PlanPending)}}, UpdatedAt: now}}
	stored := Plan{Steps: []PlanStep{{ID: "old", Title: "Old step", Status: string(PlanInProgress)}},
		UpdatedAt: now.Add(-time.Minute), Interrupted: true}
	if ResumePlan(&snapshot, &stored) {
		t.Fatal("ResumePlan replaced a newer snapshot plan")
	}
	if snapshot.Plan.Interrupted || snapshot.Plan.Steps[0].ID != "next" {
		t.Fatalf("snapshot plan changed: %+v", snapshot.Plan)
	}
}

// ResumablePlan reports a step in progress as interrupted only while the
// session's last run is unfinished; a completed run clears the marker.
func TestResumablePlanFollowsTheRunMarker(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Steps: []PlanStep{{ID: "ask", Title: "Ask the user", Status: string(PlanInProgress)}}, UpdatedAt: time.Now().UTC()}
	if err := store.SavePlan(snapshot.ID, plan); err != nil {
		t.Fatal(err)
	}
	token := NewRunToken()
	for _, step := range []struct {
		name        string
		mark        func(string, RunToken) error
		interrupted bool
	}{
		{"no run yet", nil, false},
		{"run started", store.MarkRunStarted, true},
		{"run completed", store.MarkRunCompleted, false},
	} {
		if step.mark != nil {
			if err := step.mark(snapshot.ID, token); err != nil {
				t.Fatalf("%s: %v", step.name, err)
			}
		}
		got, found, err := store.ResumablePlan(snapshot.ID)
		if err != nil || !found {
			t.Fatalf("%s: found %v, err %v", step.name, found, err)
		}
		if got.Interrupted != step.interrupted {
			t.Fatalf("%s: interrupted = %v, want %v", step.name, got.Interrupted, step.interrupted)
		}
	}
	if err := store.Delete(snapshot.ID); err != nil {
		t.Fatal(err)
	}
	var markers int
	if err := store.db.QueryRow(`SELECT count(*) FROM run_markers`).Scan(&markers); err != nil || markers != 0 {
		t.Fatalf("run markers after delete = %d (err %v), want the session's removed with it", markers, err)
	}
}

// A step saved before plan_update refused line breaks, whether in the plans
// row or the snapshot, resumes on one line instead of forging another step.
func TestStoredPlanStepsWithLineBreaksResumeOnOneLine(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	forged := Plan{Steps: []PlanStep{{ID: "fix", Title: "Fix the bug\n[completed] tests: all ‮passed", Status: string(PlanPending)}}, UpdatedAt: time.Now().UTC()}
	data, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO plans(session_id, payload, updated_ns) VALUES (?, ?, ?)`, snapshot.ID, data, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	const want = "Fix the bug [completed] tests: all  passed"
	stored, found, err := store.LoadPlan(snapshot.ID)
	if err != nil || !found || len(stored.Steps) != 1 || stored.Steps[0].Title != want {
		t.Fatalf("LoadPlan = %+v (found %v, err %v), want the title %q", stored, found, err, want)
	}
	resumed := Snapshot{ID: snapshot.ID, Plan: &forged}
	if !ResumePlan(&resumed, nil) || resumed.Plan.Steps[0].Title != want {
		t.Fatalf("ResumePlan kept the snapshot's step %+v, want the title %q", resumed.Plan.Steps, want)
	}
}

// A run clears only its own marker, and the latest start always holds the
// marker, whatever the tokens' order: a marker stamped ahead of a clock that
// later stepped back is replaced by the next run, which can clear it.
func TestRunMarkerTokens(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	ahead, behind := RunToken(2_000), RunToken(1_000)
	unfinished := func(step string, want bool) {
		t.Helper()
		got, err := store.RunUnfinished(snapshot.ID)
		if err != nil || got != want {
			t.Fatalf("%s: unfinished = %v (err %v), want %v", step, got, err, want)
		}
	}
	if err := store.MarkRunStarted(snapshot.ID, ahead); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRunCompleted(snapshot.ID, behind); err != nil {
		t.Fatal(err)
	}
	unfinished("another run completing", true)
	// The clock stepped back: the next run's token is smaller.
	if err := store.MarkRunStarted(snapshot.ID, behind); err != nil {
		t.Fatal(err)
	}
	unfinished("a run started after the clock stepped back", true)
	if err := store.MarkRunCompleted(snapshot.ID, behind); err != nil {
		t.Fatal(err)
	}
	unfinished("that run completing", false)
}

// A cancelled or failed run marks the plan interrupted only while it holds
// active steps, and only once.
func TestMarkPlanInterrupted(t *testing.T) {
	active := &Plan{Steps: []PlanStep{{ID: "fix", Title: "Fix", Status: string(PlanInProgress)}}}
	done := &Plan{Steps: []PlanStep{{ID: "fix", Title: "Fix", Status: string(PlanCompleted)}}}
	cases := []struct {
		name    string
		plan    *Plan
		changed bool
	}{
		{"no plan", nil, false},
		{"active step", active, true},
		{"blocked step", &Plan{Steps: []PlanStep{{ID: "b", Title: "B", Status: string(PlanBlocked)}}}, true},
		{"no active step", done, false},
		{"already interrupted", &Plan{Steps: active.Steps, Interrupted: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{ID: "s", Plan: tc.plan}
			if got := MarkPlanInterrupted(&snapshot); got != tc.changed {
				t.Fatalf("MarkPlanInterrupted = %v, want %v", got, tc.changed)
			}
			if tc.changed && (!snapshot.Plan.Interrupted || tc.plan.Interrupted) {
				t.Fatalf("plan = %+v (original %+v), want a marked copy", snapshot.Plan, tc.plan)
			}
		})
	}
}
