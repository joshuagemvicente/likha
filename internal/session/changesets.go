package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Undo history bounds (specs/edit-undo D4). The oldest change sets are
// dropped first when either bound is exceeded.
const (
	MaxChangeSets     = 20
	MaxChangeSetBytes = 64 << 20
)

// ErrChangeSetTooLarge means a single change set exceeds MaxChangeSetBytes on
// its own; the edit stays applied but cannot be undone.
var ErrChangeSetTooLarge = errors.New("change set exceeds the undo history size limit")

// ChangeSetRecord is one saved, undoable change set. Payload holds the
// original content of replaced files: private session data, never sent to a
// provider and never written into the repository.
type ChangeSetRecord struct {
	ID         string
	Created    time.Time
	ToolCallID string
	Payload    json.RawMessage
}

func configureChangeSets(tx schemaExecer) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS edit_changesets (
			id TEXT PRIMARY KEY NOT NULL,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			repository TEXT NOT NULL,
			created_ns INTEGER NOT NULL,
			tool_call_id TEXT NOT NULL,
			size INTEGER NOT NULL CHECK(size >= 0),
			payload BLOB NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS edit_changesets_by_session ON edit_changesets(session_id, created_ns DESC)`,
		// One row per session whose older change sets were dropped, so /undo
		// can say why nothing older remains.
		`CREATE TABLE IF NOT EXISTS edit_changeset_drops (
			session_id TEXT PRIMARY KEY NOT NULL REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`SELECT id, session_id, repository, created_ns, tool_call_id, size, payload FROM edit_changesets LIMIT 0`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("initialize private undo history: %w", err)
		}
	}
	return nil
}

// SaveChangeSet records one applied change set for the session and prunes
// the session's history to MaxChangeSets and MaxChangeSetBytes in the same
// transaction. A payload larger than MaxChangeSetBytes on its own returns
// ErrChangeSetTooLarge and saves nothing.
func (s *Store) SaveChangeSet(sessionID, toolCallID string, payload json.RawMessage) (string, error) {
	if !validID(sessionID) {
		return "", errors.New("invalid undo history session ID")
	}
	if len(payload) > MaxChangeSetBytes {
		return "", ErrChangeSetTooLarge
	}
	if !json.Valid(payload) {
		return "", errors.New("invalid undo history payload")
	}
	id, err := randomID()
	if err != nil {
		return "", fmt.Errorf("undo history ID: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin undo history save: %w", err)
	}
	defer tx.Rollback()
	// /undo takes the set with the greatest created_ns, so it must grow with
	// every save even if the wall clock steps back between two edits.
	result, err := tx.Exec(`INSERT INTO edit_changesets(id, session_id, repository, created_ns, tool_call_id, size, payload)
		SELECT ?, id, repository,
			max(?, coalesce((SELECT max(created_ns) FROM edit_changesets WHERE session_id = ?), 0) + 1),
			?, ?, ? FROM sessions WHERE id = ? AND repository = ?`,
		id, time.Now().UTC().UnixNano(), sessionID, toolCallID, len(payload), []byte(payload), sessionID, s.root)
	if err != nil {
		return "", fmt.Errorf("save undo history: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return "", errors.New("undo history owner is unavailable")
	}
	if err := pruneChangeSets(tx, sessionID, s.root, MaxChangeSets, MaxChangeSetBytes); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit undo history: %w", err)
	}
	return id, nil
}

// pruneChangeSets drops the session's oldest change sets until at most
// maxSets remain within maxBytes, and marks the session when it dropped any.
func pruneChangeSets(tx *sql.Tx, sessionID, root string, maxSets int, maxBytes int64) error {
	rows, err := tx.Query(`SELECT id, size FROM edit_changesets WHERE session_id = ? AND repository = ?
		ORDER BY created_ns DESC, rowid DESC`, sessionID, root)
	if err != nil {
		return fmt.Errorf("read undo history: %w", err)
	}
	var drop []string
	kept, total := 0, int64(0)
	for rows.Next() {
		var id string
		var size int64
		if err := rows.Scan(&id, &size); err != nil {
			rows.Close()
			return fmt.Errorf("read undo history: %w", err)
		}
		if kept < maxSets && total+size <= maxBytes {
			kept++
			total += size
			continue
		}
		// Once one set is dropped every older one goes too: undo always
		// takes the newest remaining set, so history must stay contiguous.
		kept = maxSets
		drop = append(drop, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read undo history: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read undo history: %w", err)
	}
	if len(drop) == 0 {
		return nil
	}
	for _, id := range drop {
		if _, err := tx.Exec(`DELETE FROM edit_changesets WHERE id = ?`, id); err != nil {
			return fmt.Errorf("prune undo history: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO edit_changeset_drops(session_id) VALUES (?)`, sessionID); err != nil {
		return fmt.Errorf("mark pruned undo history: %w", err)
	}
	return nil
}

// LatestChangeSet returns the session's newest remaining change set; ok is
// false when none remains.
func (s *Store) LatestChangeSet(sessionID string) (record ChangeSetRecord, ok bool, err error) {
	if !validID(sessionID) {
		return ChangeSetRecord{}, false, errors.New("invalid undo history session ID")
	}
	var created int64
	err = s.db.QueryRow(`SELECT id, created_ns, tool_call_id, payload FROM edit_changesets
		WHERE session_id = ? AND repository = ? ORDER BY created_ns DESC, rowid DESC LIMIT 1`, sessionID, s.root).
		Scan(&record.ID, &created, &record.ToolCallID, &record.Payload)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangeSetRecord{}, false, nil
	}
	if err != nil {
		return ChangeSetRecord{}, false, fmt.Errorf("load undo history: %w", err)
	}
	if !validID(record.ID) || !json.Valid(record.Payload) {
		return ChangeSetRecord{}, false, errors.New("undo history contains damaged state; nothing was reverted")
	}
	record.Created = time.Unix(0, created).UTC()
	return record, true, nil
}

// DeleteChangeSet removes one session-owned change set after it was undone.
func (s *Store) DeleteChangeSet(sessionID, id string) error {
	if !validID(sessionID) || !validID(id) {
		return errors.New("invalid undo history identity")
	}
	result, err := s.db.Exec(`DELETE FROM edit_changesets WHERE id = ? AND session_id = ? AND repository = ?`, id, sessionID, s.root)
	if err != nil {
		return fmt.Errorf("delete undo history entry: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errors.New("undo history entry is unavailable or belongs to another session")
	}
	return nil
}

// ChangeSetsDropped reports whether the bounds dropped any of the session's
// older change sets.
func (s *Store) ChangeSetsDropped(sessionID string) (bool, error) {
	if !validID(sessionID) {
		return false, errors.New("invalid undo history session ID")
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM edit_changeset_drops d JOIN sessions s ON s.id = d.session_id
		WHERE d.session_id = ? AND s.repository = ?`, sessionID, s.root).Scan(&count); err != nil {
		return false, fmt.Errorf("read undo history bounds: %w", err)
	}
	return count > 0, nil
}
