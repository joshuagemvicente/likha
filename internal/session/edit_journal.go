package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EditJournalRecord retains private proposal/recovery state separately from
// conversation revisions. Reading a record never resumes or reverses a write.
type EditJournalRecord struct {
	ID       string
	Progress json.RawMessage
}

// RecordEditProgress creates a private journal on its first call, then replaces
// only that session-owned record. The FULL-synchronous SQLite commit must finish
// before the caller crosses the associated filesystem boundary.
func (s *Store) RecordEditProgress(sessionID, journalID string, progress json.RawMessage) (string, error) {
	if !validID(sessionID) || (journalID != "" && !validID(journalID)) {
		return "", errors.New("invalid edit journal identity")
	}
	var identity struct{ Root, Status string }
	if len(progress) > 76<<20 || json.Unmarshal(progress, &identity) != nil || identity.Root != s.root || identity.Status == "" {
		return "", errors.New("invalid or oversized private edit progress")
	}
	if journalID == "" {
		id, err := randomID()
		if err != nil {
			return "", fmt.Errorf("edit journal ID: %w", err)
		}
		result, err := s.db.Exec(`INSERT INTO edit_journal(id, session_id, repository, updated_ns, progress)
			SELECT ?, id, repository, ?, ? FROM sessions WHERE id = ? AND repository = ?`,
			id, time.Now().UTC().UnixNano(), []byte(progress), sessionID, s.root)
		if err != nil {
			return "", fmt.Errorf("create edit journal: %w", err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return "", errors.New("edit journal owner is unavailable")
		}
		return id, nil
	}
	result, err := s.db.Exec(`UPDATE edit_journal SET updated_ns = ?, progress = ?
		WHERE id = ? AND session_id = ? AND repository = ?`, time.Now().UTC().UnixNano(), []byte(progress), journalID, sessionID, s.root)
	if err != nil {
		return "", fmt.Errorf("persist edit boundary: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return "", errors.New("private edit journal is unavailable or belongs to another session")
	}
	return journalID, nil
}

func (s *Store) EditJournals(sessionID string) ([]EditJournalRecord, error) {
	if !validID(sessionID) {
		return nil, errors.New("invalid edit journal owner")
	}
	rows, err := s.db.Query(`SELECT id, progress FROM edit_journal WHERE session_id = ? AND repository = ?
		ORDER BY updated_ns DESC, id ASC`, sessionID, s.root)
	if err != nil {
		return nil, fmt.Errorf("load private edit journal: %w", err)
	}
	defer rows.Close()
	var records []EditJournalRecord
	for rows.Next() {
		var record EditJournalRecord
		if err := rows.Scan(&record.ID, &record.Progress); err != nil {
			return nil, fmt.Errorf("read private edit journal: %w", err)
		}
		if !validID(record.ID) || !json.Valid(record.Progress) {
			return nil, errors.New("private edit journal contains damaged state; no action was replayed")
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
