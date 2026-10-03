package session

import (
	"database/sql"
	"errors"
	"fmt"
)

var (
	// ErrSessionNotFound means no session with the requested ID exists.
	ErrSessionNotFound = errors.New("session not found")
	// ErrSessionOtherRepository means the session exists but belongs to a
	// different repository than this store; it is never deleted from here.
	ErrSessionOtherRepository = errors.New("session belongs to another repository")
)

// Delete removes one session owned by this store's repository. Its plans,
// task_records and edit_journal rows go with it through ON DELETE CASCADE in
// the same transaction. Retained tool output lives outside the database; the
// caller removes it separately (tooloutput.RemoveSession).
func (s *Store) Delete(id string) error {
	if !validID(id) {
		return fmt.Errorf("invalid session ID %q", id)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin session delete: %w", err)
	}
	defer tx.Rollback()
	// A no-row write reserves the writer lock before the ownership check so
	// another process cannot reassign or save the row in between.
	if _, err := tx.Exec(`UPDATE sessions SET revision = revision WHERE 0`); err != nil {
		return fmt.Errorf("lock session for delete: %w", err)
	}
	// Cascades are the only cleanup for child rows; refuse rather than orphan
	// them if the connection somehow lost the per-connection pragma.
	var foreignKeys int
	if err := tx.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify session delete cascades: %w", err)
	}
	if foreignKeys != 1 {
		return errors.New("delete session: foreign key enforcement is off; refusing to orphan plans, task records or edit journal rows")
	}
	var repository string
	err = tx.QueryRow("SELECT repository FROM sessions WHERE id = ?", id).Scan(&repository)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("delete session %s: %w", id, ErrSessionNotFound)
	}
	if err != nil {
		return fmt.Errorf("delete session %s: %w", id, err)
	}
	if repository != s.root {
		return fmt.Errorf("delete session %s: %w", id, ErrSessionOtherRepository)
	}
	result, err := tx.Exec("DELETE FROM sessions WHERE id = ? AND repository = ?", id, s.root)
	if err != nil {
		return fmt.Errorf("delete session %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("verify deleted session %s: %w", id, err)
	}
	if affected != 1 {
		return fmt.Errorf("delete session %s: %w", id, ErrSessionNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session delete %s: %w", id, err)
	}
	return nil
}
