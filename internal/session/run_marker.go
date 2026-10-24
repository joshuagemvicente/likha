package session

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// A run marker records that a run is working in a session, from its start
// until its session is saved at its end. A marker still set when the session
// is resumed means that run died with its process before that save, so the
// plan steps it left in progress were interrupted (specs/plan-todo). A run
// that was cancelled or failed records that on the plan itself
// (MarkPlanInterrupted) before it saves and clears its marker; a run that
// completed may still leave a step in progress, waiting on the user's next
// prompt, and that is no interruption. The marker is per session: two Likha
// instances working in one session share it, so the latest start replaces
// any earlier marker.

// This additive extension leaves the schema version at 1, mirroring plans.
func configureRunMarkers(tx schemaExecer) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS run_markers (
			session_id TEXT PRIMARY KEY NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			started_ns INTEGER NOT NULL
		)`,
		`SELECT session_id, started_ns FROM run_markers LIMIT 0`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("initialize run markers: %w", err)
		}
	}
	return nil
}

// RunToken names one run's marker: the run's start time in nanoseconds. A
// run passes the token it started with to MarkRunCompleted, which then
// clears only its own marker. Tokens are never ordered: the clock can step
// back between two runs.
type RunToken int64

// NewRunToken returns the token of a run starting now.
func NewRunToken() RunToken {
	return RunToken(time.Now().UTC().UnixNano())
}

// MarkRunStarted records that the run token began working in the session,
// replacing any marker there. Tokens are not compared: a marker stamped
// ahead of a clock later stepped back must not outlive every later run.
// A late write from an earlier run can replace a later run's marker, which
// then reports a completed run as unfinished: an over-report, never a
// cut-short run hidden.
func (s *Store) MarkRunStarted(sessionID string, token RunToken) error {
	return s.writeRunMarker(sessionID, `INSERT INTO run_markers(session_id, started_ns) VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET started_ns = excluded.started_ns`, int64(token))
}

// MarkRunCompleted clears the session's run marker once the run token's
// session was saved at its end. A marker another run wrote since stays, so a
// run that ended never clears the marker of one still working.
func (s *Store) MarkRunCompleted(sessionID string, token RunToken) error {
	return s.writeRunMarker(sessionID, `DELETE FROM run_markers WHERE session_id = ? AND started_ns = ?`, int64(token))
}

func (s *Store) writeRunMarker(sessionID, statement string, args ...any) error {
	if !validID(sessionID) {
		return errors.New("invalid run marker session ID")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin run marker write: %w", err)
	}
	defer tx.Rollback()
	if err := runMarkerOwner(tx, sessionID, s.root); err != nil {
		return err
	}
	if _, err := tx.Exec(statement, append([]any{sessionID}, args...)...); err != nil {
		return fmt.Errorf("write run marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit run marker: %w", err)
	}
	return nil
}

// RunUnfinished reports whether a run started in the session and never
// saved its end.
func (s *Store) RunUnfinished(sessionID string) (bool, error) {
	if !validID(sessionID) {
		return false, errors.New("invalid run marker session ID")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin run marker read: %w", err)
	}
	defer tx.Rollback()
	if err := runMarkerOwner(tx, sessionID, s.root); err != nil {
		return false, err
	}
	var started int64
	err = tx.QueryRow(`SELECT started_ns FROM run_markers WHERE session_id = ?`, sessionID).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read run marker: %w", err)
	}
	return true, nil
}

func runMarkerOwner(tx *sql.Tx, sessionID, root string) error {
	var owner string
	if err := tx.QueryRow(`SELECT repository FROM sessions WHERE id = ?`, sessionID).Scan(&owner); err != nil {
		return fmt.Errorf("run marker session owner is unavailable: %w", err)
	}
	if owner != root {
		return errors.New("run marker session belongs to another repository")
	}
	return nil
}
