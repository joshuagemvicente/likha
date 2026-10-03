package session

// Phase 3 of the persisted plan/todo checklist (specs/plan-todo). The store
// keeps one checklist per private session in an additive plans table, and the
// snapshot blob carries the current optional pointer for the ordinary
// Load/Save flow. The plan is task data only: it never holds permissions,
// credentials, or live execution handles, and resuming never starts work.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"likha/internal/tools"
)

type PlanStep = tools.PlanStep

// Plan step statuses, mirroring the plan_update tool spelling.
const (
	PlanPending    = tools.PlanPending
	PlanInProgress = tools.PlanInProgress
	PlanCompleted  = tools.PlanCompleted
	PlanBlocked    = tools.PlanBlocked
)

const (
	maxPlanSteps      = 32
	maxPlanIDRunes    = 128
	maxPlanTitleRunes = 240
	maxPlanBytes      = 1 << 20
)

// ErrPlanCapacity means the persisted plan exceeded the storage bound; the
// previously persisted plan was left unchanged.
var ErrPlanCapacity = errors.New("private plan capacity exceeded")

// Plan is the persisted /todo checklist for one private session.
type Plan struct {
	// Steps is the complete ordered list. A nil or empty list is the cleared
	// state that an empty plan_update request produced.
	Steps []PlanStep `json:"steps,omitempty"`

	// UpdatedAt is the time the caller's update was last persisted. ResumePlan
	// lets the newer of the snapshot's plan and the stored plan win.
	UpdatedAt time.Time `json:"updated_at,omitempty"`

	// Interrupted reports that a previous run stopped while steps were still
	// in_progress or blocked. Those steps stay unconfirmed: resume never
	// restarts them, fabricates completion, or restores permissions.
	Interrupted bool `json:"interrupted,omitempty"`
}

// This additive extension deliberately leaves the existing schema version at 1,
// mirroring edit_journal and task_records.
func configurePlans(tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS plans (
			session_id TEXT PRIMARY KEY NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			payload BLOB NOT NULL CHECK(length(payload) <= 1048576),
			updated_ns INTEGER NOT NULL
		)`,
		`SELECT session_id, payload, updated_ns FROM plans LIMIT 0`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("initialize private plans: %w", err)
		}
	}
	return nil
}

// SavePlan durably commits the checklist without touching the snapshot's
// optimistic revision, mirroring SaveTask: acquire the single writer lock,
// verify session ownership, then commit one row. An invalid plan is rejected
// and leaves the previously persisted plan unchanged. Caveat: the following
// Save of a snapshot whose Plan field still predates this update rewrites the
// row from snapshot.Plan in its own transaction, so callers must keep their
// in-flight snapshot.Plan current (or nil-free) the way ResumeTasks callers
// keep snapshot.Tasks current.
func (s *Store) SavePlan(sessionID string, plan Plan) error {
	if !validID(sessionID) {
		return errors.New("invalid private plan session ID")
	}
	if err := validatePlan(plan); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin private plan save: %w", err)
	}
	defer tx.Rollback()
	// A no-row write reserves the writer lock without modifying sibling rows.
	if _, err := tx.Exec(`UPDATE plans SET updated_ns = updated_ns WHERE 0`); err != nil {
		return fmt.Errorf("lock private plans: %w", err)
	}
	if err := planOwner(tx, sessionID, s.root); err != nil {
		return err
	}
	// Marshal only after acquiring the single connection, bounding temporary
	// allocation even when many updates arrive concurrently.
	data, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode private plan: %w", err)
	}
	if len(data) > maxPlanBytes {
		return fmt.Errorf("private plan exceeds %d bytes: %w", maxPlanBytes, ErrPlanCapacity)
	}
	if _, err := tx.Exec(`INSERT INTO plans(session_id, payload, updated_ns) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET payload = excluded.payload, updated_ns = excluded.updated_ns`,
		sessionID, data, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("persist private plan: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit private plan save: %w", err)
	}
	return nil
}

// LoadPlan returns the persisted plan for the session. found=false with a nil
// error means the session has no stored plan (new or legacy). A damaged
// payload returns a non-nil error together with found=false: callers surface
// the damage instead of silently treating it as "no plan", and LoadPlan never
// invents or repairs a checklist. Steps that fall outside the plan_update
// contract (unknown statuses, duplicate or overlong IDs/titles, more than 32
// steps) are dropped from the returned value so callers cannot act on data
// the tool could not have written.
func (s *Store) LoadPlan(sessionID string) (Plan, bool, error) {
	if !validID(sessionID) {
		return Plan{}, false, errors.New("invalid private plan session ID")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Plan{}, false, fmt.Errorf("begin private plan read: %w", err)
	}
	defer tx.Rollback()
	if err := planOwner(tx, sessionID, s.root); err != nil {
		return Plan{}, false, err
	}
	var payload []byte
	err = tx.QueryRow(`SELECT payload FROM plans WHERE session_id = ?`, sessionID).Scan(&payload)
	if err == sql.ErrNoRows {
		return Plan{}, false, nil
	}
	if err != nil {
		return Plan{}, false, fmt.Errorf("load private plan: %w", err)
	}
	var plan Plan
	if len(payload) == 0 || len(payload) > maxPlanBytes || json.Unmarshal(payload, &plan) != nil {
		return Plan{}, false, errors.New("persisted private plan is damaged; no plan was returned")
	}
	return sanitizePlan(plan), true, nil
}

func planOwner(tx *sql.Tx, sessionID, root string) error {
	var owner string
	if err := tx.QueryRow(`SELECT repository FROM sessions WHERE id = ?`, sessionID).Scan(&owner); err != nil {
		return fmt.Errorf("private plan session owner is unavailable: %w", err)
	}
	if owner != root {
		return errors.New("private plan session belongs to another repository")
	}
	return nil
}

// replacePlan rewrites one session's plans row from a snapshot inside the
// caller's transaction, so the authoritative row commits together with the
// snapshot blob. A nil snapshot.Plan deletes the row (legacy snapshots and
// "no plan ever" carry no authoritative list); a pointer to an empty Steps
// list is an intentional clear and keeps/recreates the row with an empty
// payload. Steps outside the plan_update contract are dropped before
// persisting, matching LoadPlan's reader-side sanitization, so the row never
// stores them. The 1 MiB CHECK can only trip through a misuse of the
// pointer's direct assignment in Go (not JSON), and that returns an error to
// fail the whole save visibly rather than writing a partial checklist.
func replacePlan(tx *sql.Tx, snapshot Snapshot) error {
	// A snapshot with no Plan never deletes the stored row: a plan written
	// mid-run through SavePlan survives any snapshot save whose in-memory
	// copy predates it, mirroring replaceTaskRecords. A non-nil Plan,
	// including an intentionally cleared empty-steps list, is the
	// authoritative rewrite.
	if snapshot.Plan == nil {
		return nil
	}
	_, err := tx.Exec(`DELETE FROM plans WHERE session_id = ?`, snapshot.ID)
	data, err := json.Marshal(sanitizePlan(*snapshot.Plan))
	if err != nil {
		return fmt.Errorf("encode private plan: %w", err)
	}
	if len(data) > maxPlanBytes {
		return fmt.Errorf("private plan exceeds %d bytes: %w", maxPlanBytes, ErrPlanCapacity)
	}
	if _, err := tx.Exec(`INSERT INTO plans(session_id, payload, updated_ns) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET payload = excluded.payload, updated_ns = excluded.updated_ns`,
		snapshot.ID, data, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("persist private plan: %w", err)
	}
	return nil
}

// ResumePlan merges the persisted plan into the snapshot before the resumed
// session displays it. It does not read the database: the coordinator passes
// the row loaded through LoadPlan as stored. The persisted version wins when
// the snapshot carries none or an older UpdatedAt; otherwise the snapshot's
// current plan stays and nothing changes. Completed statuses are preserved,
// and any step still in_progress or blocked in the adopted (persisted-before-
// this-resume) plan marks Plan.Interrupted: formerly active work is reported
// interrupted/unconfirmed, never restarted and never fabricated as completed.
// Snapshot-less builds pass a snapshot with a nil Plan; a session with no
// stored plan and no snapshot plan is legacy and stays nil, which renders as
// the empty state. Repeated calls with unchanged input change nothing. Like
// ResumeTasks it never replays work or grants permissions; persist the
// repaired snapshot through the normal idle optimistic Save path.
func ResumePlan(snapshot *Snapshot, stored *Plan) (changed bool) {
	if snapshot == nil || !validID(snapshot.ID) {
		return false
	}
	if stored == nil {
		return false // Nothing persisted: keep the snapshot's own plan state.
	}
	if snapshot.Plan != nil && !snapshot.Plan.UpdatedAt.Before(stored.UpdatedAt) {
		return false // The snapshot's plan is current or newer; stored is stale.
	}
	adopted := sanitizePlan(*stored)
	if snapshot.Plan == nil || !samePlanValue(*snapshot.Plan, adopted) {
		for _, step := range adopted.Steps {
			status := tools.PlanStepStatus(step.Status)
			if status == PlanInProgress || status == PlanBlocked {
				adopted.Interrupted = true
				break
			}
		}
		value := adopted
		snapshot.Plan = &value
		return true
	}
	return false
}

// samePlan compares the optional *Plan the way the other durable fields are
// compared inside sameConversation. nil equals only nil: a pointer to a plan
// with empty Steps records an explicit clear and is different state. Non-nil
// pointers are compared field-wise, so Steps' nil and empty listings remain
// equivalent while the fields themselves must agree.
func samePlan(a, b *Plan) bool {
	if a == nil || b == nil {
		return a == b
	}
	return samePlanValue(*a, *b)
}

func samePlanValue(a, b Plan) bool {
	return a.UpdatedAt.Equal(b.UpdatedAt) && a.Interrupted == b.Interrupted && slices.Equal(a.Steps, b.Steps)
}

// validatePlan enforces the frozen plan_update contract at the store
// boundary: at most 32 uniquely identified steps with bounded plain titles.
func validatePlan(plan Plan) error {
	if len(plan.Steps) > maxPlanSteps {
		return fmt.Errorf("plan holds %d steps; the limit is %d: %w", len(plan.Steps), maxPlanSteps, ErrPlanCapacity)
	}
	seen := make(map[string]bool, len(plan.Steps))
	for i, step := range plan.Steps {
		if !validPlanStep(step) {
			return fmt.Errorf("plan step %d is outside the plan_update contract", i)
		}
		if seen[step.ID] {
			return fmt.Errorf("plan step %d repeats ID %q", i, step.ID)
		}
		seen[step.ID] = true
	}
	return nil
}

func validPlanStep(step PlanStep) bool {
	if !utf8.ValidString(step.ID) || !utf8.ValidString(step.Title) ||
		step.ID == "" || utf8.RuneCountInString(step.ID) > maxPlanIDRunes ||
		step.Title == "" || utf8.RuneCountInString(step.Title) > maxPlanTitleRunes {
		return false
	}
	switch tools.PlanStepStatus(step.Status) {
	case PlanPending, PlanInProgress, PlanCompleted, PlanBlocked:
		return true
	default:
		return false
	}
}

// sanitizePlan drops steps that fall outside the plan_update contract and
// keeps the first 32 otherwise-valid steps when a persisted payload somehow
// holds more, so callers cannot display data the tool could not write. A
// checklist with no remaining steps cannot report an interruption, so the
// flag is cleared when every step is dropped.
func sanitizePlan(plan Plan) Plan {
	steps := make([]PlanStep, 0, min(len(plan.Steps), maxPlanSteps))
	seen := make(map[string]bool, len(plan.Steps))
	for i, step := range plan.Steps {
		if i >= maxPlanSteps {
			break
		}
		if !validPlanStep(step) || seen[step.ID] {
			continue
		}
		seen[step.ID] = true
		steps = append(steps, step)
	}
	plan.Steps = steps
	if len(steps) == 0 {
		plan.Interrupted = false
	}
	return plan
}
