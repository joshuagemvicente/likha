package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/tools"
)

// TaskRecord is the credential-free, data-only explore inspection record.
type TaskRecord = explore.Record

const (
	maxTaskRecordBytes   = 32 << 20
	maxTaskSessionBytes  = 256 << 20
	maxTaskDatabaseBytes = 512 << 20
	maxSessionTasks      = 4096
	maxDatabaseTasks     = 16384
	interruptedToolText  = "Error: action not executed; run interrupted"
	interruptedTaskText  = "Run interrupted; this task was not resumed or replayed. Available findings may be incomplete."
)

var (
	// ErrTaskCapacity never evicts older records or silently clips a transcript.
	ErrTaskCapacity = errors.New("private task record capacity exceeded")
	// ErrTaskStale means a newer callback already persisted this task's progress.
	ErrTaskStale = errors.New("private task progress is older than the saved version")
)

// This additive extension deliberately leaves the existing schema version at 1.
func configureTaskRecords(tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS task_records (
			id TEXT PRIMARY KEY NOT NULL,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			repository TEXT NOT NULL,
			run_id TEXT NOT NULL,
			version INTEGER NOT NULL CHECK(version >= 0),
			payload BLOB NOT NULL CHECK(length(payload) <= 33554432),
			updated_ns INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS task_records_by_session_run ON task_records(session_id, run_id, id)`,
		`SELECT id, session_id, repository, run_id, version, payload, updated_ns FROM task_records LIMIT 0`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("initialize private task records: %w", err)
		}
	}
	return nil
}

// SaveTask durably commits one task without reading or advancing a Snapshot's
// optimistic revision. The store's single connection serializes callbacks;
// acquiring SQLite's writer lock before reading also protects other processes.
// IDs and accepted identity cannot be reassigned, even by a newer version.
func (s *Store) SaveTask(record explore.Record) error {
	if err := validateTask(record, record.SessionID, s.root); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin private task save: %w", err)
	}
	defer tx.Rollback()
	// A no-row write reserves the writer lock without modifying sibling records.
	if _, err := tx.Exec(`UPDATE task_records SET updated_ns = updated_ns WHERE 0`); err != nil {
		return fmt.Errorf("lock private task records: %w", err)
	}
	if err := taskOwner(tx, record.SessionID, s.root); err != nil {
		return err
	}
	if record.ParentID != "" {
		var parentSession, parentRoot, parentRun string
		var parentVersion int64
		var parentData []byte
		if err := tx.QueryRow(`SELECT session_id, repository, run_id, version,
			CASE WHEN typeof(payload) = 'blob' AND length(payload) <= ? THEN payload ELSE NULL END
			FROM task_records WHERE id = ?`, maxTaskRecordBytes, record.ParentID).
			Scan(&parentSession, &parentRoot, &parentRun, &parentVersion, &parentData); err != nil {
			return fmt.Errorf("private task parent is unavailable: %w", err)
		}
		parent, err := decodeTask(record.ParentID, parentSession, parentRoot, parentRun, parentVersion, parentData)
		if err != nil {
			return err
		}
		if !taskParentMatches(parent, record) {
			return errors.New("private task parent belongs to a different owner or branch")
		}
	}
	// Marshal only after acquiring the single connection, bounding temporary
	// allocation even when many callbacks arrive concurrently.
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode private task record: %w", err)
	}
	if len(data) > maxTaskRecordBytes {
		return fmt.Errorf("task record exceeds 32 MiB: %w", ErrTaskCapacity)
	}
	var sessionID, root, runID string
	var version, size int64
	var previous []byte
	err = tx.QueryRow(`SELECT session_id, repository, run_id, version, length(payload),
		CASE WHEN typeof(payload) = 'blob' AND length(payload) <= ? THEN payload ELSE NULL END
		FROM task_records WHERE id = ?`, maxTaskRecordBytes, record.ID).
		Scan(&sessionID, &root, &runID, &version, &size, &previous)
	existing := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read private task before save: %w", err)
	}
	if existing {
		if sessionID != record.SessionID || root != record.Root || runID != record.RunID {
			return errors.New("task ID belongs to a different session, repository, or run")
		}
		old, err := decodeTask(record.ID, sessionID, root, runID, version, previous)
		if err != nil {
			return err
		}
		if !sameTaskIdentity(old, record) {
			return errors.New("task accepted identity cannot be changed")
		}
		if record.Version < old.Version {
			return ErrTaskStale
		}
		if record.Version == old.Version {
			if sameTaskRecord(old, record) {
				return nil // An idempotent callback does not need another commit.
			}
			return errors.New("task progress changed without advancing its version")
		}
	}
	var count, total, sessionCount, sessionTotal, runCount int64
	if err := tx.QueryRow(`SELECT count(*), coalesce(sum(length(payload)), 0),
		coalesce(sum(CASE WHEN session_id = ? THEN 1 ELSE 0 END), 0),
		coalesce(sum(CASE WHEN session_id = ? THEN length(payload) ELSE 0 END), 0),
		coalesce(sum(CASE WHEN session_id = ? AND run_id = ? THEN 1 ELSE 0 END), 0)
		FROM task_records`, record.SessionID, record.SessionID, record.SessionID, record.RunID).
		Scan(&count, &total, &sessionCount, &sessionTotal, &runCount); err != nil {
		return fmt.Errorf("account private task capacity: %w", err)
	}
	if !existing {
		count++
		sessionCount++
		runCount++
	}
	delta := int64(len(data)) - size
	if count > maxDatabaseTasks || sessionCount > maxSessionTasks || runCount > explore.MaxChildren || total+delta > maxTaskDatabaseBytes || sessionTotal+delta > maxTaskSessionBytes {
		return fmt.Errorf("task storage is full; existing records were retained: %w", ErrTaskCapacity)
	}
	result, err := tx.Exec(`INSERT INTO task_records(id, session_id, repository, run_id, version, payload, updated_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET version = excluded.version, payload = excluded.payload, updated_ns = excluded.updated_ns
		WHERE task_records.session_id = excluded.session_id AND task_records.repository = excluded.repository
		AND task_records.run_id = excluded.run_id AND excluded.version >= task_records.version`,
		record.ID, record.SessionID, record.Root, record.RunID, int64(record.Version), data, time.Now().UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("persist private task progress: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errors.New("private task progress was not saved")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit private task progress: %w", err)
	}
	return nil
}

// replaceTaskRecords rewrites one session's task_records rows from a snapshot
// inside the caller's transaction, so a repaired ResumeTasks snapshot reaches
// the authoritative table atomically with the snapshot itself. A nil
// snapshot.Tasks rewrites nothing: legacy snapshots and saves built without
// task data carry no authoritative list and must never delete rows that a live
// runtime recorded through SaveTask. A non-nil Tasks is treated as the
// caller's complete list, so rows absent from it are deleted. Caveat: a caller
// holding a stale non-nil list can still drop rows it never saw, so callers
// must save snapshots built from their current records, as ResumeTasks and the
// coordinated UI writer do. Records that fail validation or the storage
// budgets stay blob-only instead of failing the whole save; Tasks skips them
// on read exactly as it skips other damaged rows, so blob and table can
// converge again through ResumeTasks.
func replaceTaskRecords(tx *sql.Tx, snapshot Snapshot) error {
	if snapshot.Tasks == nil {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM task_records WHERE session_id = ?`, snapshot.ID); err != nil {
		return fmt.Errorf("clear private task records: %w", err)
	}
	var count, bytes int64
	if err := tx.QueryRow(`SELECT count(*), coalesce(sum(length(payload)), 0) FROM task_records`).
		Scan(&count, &bytes); err != nil {
		return fmt.Errorf("account private task capacity: %w", err)
	}
	var sessionCount, sessionBytes int64
	for _, record := range snapshot.Tasks {
		if err := validateTask(record, snapshot.ID, snapshot.Root); err != nil {
			continue
		}
		data, err := json.Marshal(record)
		if err != nil || len(data) > maxTaskRecordBytes {
			continue
		}
		if sessionCount >= maxSessionTasks || count >= maxDatabaseTasks ||
			sessionBytes+int64(len(data)) > maxTaskSessionBytes || bytes+int64(len(data)) > maxTaskDatabaseBytes {
			continue
		}
		result, err := tx.Exec(`INSERT INTO task_records(id, session_id, repository, run_id, version, payload, updated_ns)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET version = excluded.version, payload = excluded.payload, updated_ns = excluded.updated_ns
			WHERE task_records.session_id = excluded.session_id AND task_records.repository = excluded.repository
			AND excluded.version >= task_records.version`,
			record.ID, record.SessionID, record.Root, record.RunID, int64(record.Version), data, time.Now().UTC().UnixNano())
		if err != nil {
			return fmt.Errorf("persist private task records: %w", err)
		}
		if inserted, err := result.RowsAffected(); err == nil && inserted == 1 {
			// Conflicts with another session's row are skipped and stay
			// blob-only, so the budget accounting tracks actual inserts.
			count++
			sessionCount++
			sessionBytes += int64(len(data))
			bytes += int64(len(data))
		}
	}
	return nil
}

func taskOwner(tx *sql.Tx, sessionID, root string) error {
	var owner string
	if err := tx.QueryRow(`SELECT repository FROM sessions WHERE id = ?`, sessionID).Scan(&owner); err != nil {
		return fmt.Errorf("private task session owner is unavailable: %w", err)
	}
	if owner != root {
		return errors.New("private task session belongs to another repository")
	}
	return nil
}

// Tasks returns a bounded, validated inspection snapshot in acceptance order.
// Live statuses are unchanged. Only ResumeTasks marks work interrupted, and
// neither operation restores providers, credentials, trust, or runtime handles.
// Damaged payload rows and children whose parent row is missing or
// inconsistent are skipped, so one bad row cannot hide the session's healthy
// records; the omission is visible only through the returned records. Owner
// and repository violations still fail the read.
func (s *Store) Tasks(sessionID string) ([]explore.Record, error) {
	if !validID(sessionID) {
		return nil, errors.New("invalid private task session ID")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin private task read: %w", err)
	}
	defer tx.Rollback()
	if err := taskOwner(tx, sessionID, s.root); err != nil {
		return nil, err
	}
	var count, total int64
	if err := tx.QueryRow(`SELECT count(*), coalesce(sum(length(payload)), 0) FROM task_records WHERE session_id = ?`, sessionID).Scan(&count, &total); err != nil {
		return nil, fmt.Errorf("account private task read: %w", err)
	}
	if count > maxSessionTasks || total > maxTaskSessionBytes {
		return nil, fmt.Errorf("oversized saved task collection: %w", ErrTaskCapacity)
	}
	rows, err := tx.Query(`SELECT id, repository, run_id, version,
		CASE WHEN typeof(payload) = 'blob' AND length(payload) <= ? THEN payload ELSE NULL END
		FROM task_records WHERE session_id = ? ORDER BY id ASC`, maxTaskRecordBytes, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load private task records: %w", err)
	}
	defer rows.Close()
	var records []explore.Record
	for rows.Next() {
		var id, root, runID string
		var version int64
		var data []byte
		if err := rows.Scan(&id, &root, &runID, &version, &data); err != nil {
			return nil, fmt.Errorf("read private task record: %w", err)
		}
		if root != s.root {
			return nil, errors.New("private task record belongs to another repository")
		}
		record, err := decodeTask(id, sessionID, root, runID, version, data)
		if err != nil {
			continue // A damaged row is skipped, not replayed; see Tasks above.
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load private task records: %w", err)
	}
	parents := make(map[string]explore.Record, len(records))
	for _, record := range records {
		parents[record.ID] = record
	}
	records = slices.DeleteFunc(records, func(child explore.Record) bool {
		if child.ParentID == "" {
			return false
		}
		parent, ok := parents[child.ParentID]
		return !ok || !taskParentMatches(parent, child)
	})
	sortTasks(records)
	return records, nil
}

func decodeTask(id, sessionID, root, runID string, version int64, data []byte) (explore.Record, error) {
	var record explore.Record
	if len(data) == 0 || len(data) > maxTaskRecordBytes || json.Unmarshal(data, &record) != nil || record.ID != id || record.RunID != runID || version < 0 || record.Version != uint64(version) {
		return record, errors.New("private task record contains damaged identity or progress; no task was replayed")
	}
	if err := validateTask(record, sessionID, root); err != nil {
		return record, fmt.Errorf("damaged private task record: %w", err)
	}
	return record, nil
}

// Runtime task/run identifiers are opaque, bounded tokens, not filesystem paths.
// Session identifiers retain their existing strict lowercase hexadecimal format.
func validTaskToken(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// validAgentName mirrors the frozen catalog's agent-name rule for persisted
// records: records from earlier phases carry "explore"; Phase 4 adds review
// and discovered profile names with the same shape.
func validAgentName(agent string) bool {
	if agent == "" || utf8.RuneCountInString(agent) > 64 || !utf8.ValidString(agent) {
		return false
	}
	return strings.IndexFunc(agent, unicode.IsControl) < 0
}

func validateTask(r explore.Record, sessionID, root string) error {
	if !validID(sessionID) || r.SessionID != sessionID || r.Root != root || !validTaskToken(r.ID) || !validTaskToken(r.RunID) || r.Version > math.MaxInt64 {
		return errors.New("invalid private task identity, version, or repository")
	}
	if !validAgentName(r.Agent) || r.Depth < 1 || r.Depth > explore.MaxDepth || (r.Depth == 1) != (r.ParentID == "") || r.ParentID == r.ID || r.ParentID != "" && !validTaskToken(r.ParentID) || strings.TrimSpace(r.ParentCallID) == "" || len(r.ParentCallID) > 1024 || !utf8.ValidString(r.ParentCallID) {
		return errors.New("invalid private task parent identity or role")
	}
	switch r.Status {
	case explore.Queued, explore.Running, explore.Waiting, explore.Completed, explore.Limited, explore.Failed, explore.Cancelled, explore.Interrupted:
	default:
		return errors.New("invalid private task status")
	}
	if r.AcceptedAt.IsZero() || r.Deadline.IsZero() || r.Rounds < 0 || r.Rounds > explore.MaxRequests || r.SpawnUsed < 0 || r.SpawnUsed > explore.MaxChildren || strings.TrimSpace(r.Description) == "" || utf8.RuneCountInString(r.Description) > explore.MaxDescriptionRunes || len(r.Prompt) > explore.MaxBriefBytes {
		return errors.New("invalid private task acceptance or budget metadata")
	}
	for _, date := range []time.Time{r.AcceptedAt, r.Deadline, r.StartedAt, r.FinishedAt} {
		_, offset := date.Zone()
		if date.Year() < 0 || date.Year() > 9999 || offset <= -24*60*60 || offset >= 24*60*60 {
			return errors.New("invalid private task timestamp")
		}
	}
	if r.Usage.PromptTokens < 0 || r.Usage.CompletionTokens < 0 || r.Usage.ReportedRequests < 0 || r.Usage.UnknownRequests < 0 || r.Usage.Cost < 0 || math.IsNaN(r.Usage.Cost) || math.IsInf(r.Usage.Cost, 0) {
		return errors.New("invalid private task usage")
	}
	// Account string escaping and structural overhead before Marshal, so a
	// caller cannot force an unbounded temporary JSON allocation. This is an
	// upper bound, not transcript clipping; capacity failure is explicit.
	remaining := int64(maxTaskRecordBytes - 4096)
	validUTF8 := true
	charge := func(text string) {
		if remaining < 0 || int64(len(text)) > remaining {
			remaining = -1
			return
		}
		if !utf8.ValidString(text) {
			validUTF8 = false
			return
		}
		remaining -= 16
		// Match encoding/json's string escaping without allocating a copy.
		for _, c := range text {
			switch c {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				remaining -= 2
			case '<', '>', '&', '\u2028', '\u2029':
				remaining -= 6
			default:
				if c < 0x20 {
					remaining -= 6
				} else {
					remaining -= int64(utf8.RuneLen(c))
				}
			}
			if remaining < 0 {
				return
			}
		}
	}
	for _, text := range []string{r.ID, r.RunID, r.SessionID, r.ParentID, r.ParentCallID, r.Agent, r.Description, r.Prompt, r.Root, r.Provider, r.Model, r.Findings, r.Reason} {
		charge(text)
	}
	for _, warning := range r.Warnings {
		charge(warning)
	}
	for _, message := range r.History {
		remaining -= 256
		for _, text := range []string{message.Role, message.Content, message.Reasoning, message.ToolCallID} {
			charge(text)
		}
		for _, call := range message.ToolCalls {
			charge(call.ID)
			charge(call.Name)
			charge(call.Arguments)
		}
	}
	for _, tool := range r.Tools {
		remaining -= 512
		for _, text := range []string{tool.CallID, tool.Name, tool.Arguments, string(tool.Result.Status), tool.Result.Content, tool.Result.Source.Kind, tool.Result.Source.Server, tool.Result.Source.Tool, tool.Result.ArtifactID, tool.Result.Cursor} {
			charge(text)
		}
		for _, warning := range tool.Result.Warnings {
			charge(warning)
		}
	}
	if remaining < 0 {
		return fmt.Errorf("task record JSON allocation would exceed 32 MiB: %w", ErrTaskCapacity)
	}
	if !validUTF8 {
		return errors.New("private task record contains invalid UTF-8")
	}
	return nil
}

func sameTaskIdentity(a, b explore.Record) bool {
	return a.ID == b.ID && a.RunID == b.RunID && a.SessionID == b.SessionID && a.Root == b.Root && a.ParentID == b.ParentID && a.ParentCallID == b.ParentCallID && a.Agent == b.Agent && a.Depth == b.Depth && a.Description == b.Description && a.Prompt == b.Prompt && a.Provider == b.Provider && a.Model == b.Model && a.AcceptedAt.Equal(b.AcceptedAt) && a.Deadline.Equal(b.Deadline)
}

func taskParentMatches(parent, child explore.Record) bool {
	return parent.ID == child.ParentID && parent.SessionID == child.SessionID && parent.Root == child.Root && parent.RunID == child.RunID && parent.Depth+1 == child.Depth && !child.AcceptedAt.Before(parent.AcceptedAt) && !child.Deadline.After(parent.Deadline)
}

func sameTaskRecord(a, b explore.Record) bool {
	if !sameTaskIdentity(a, b) || a.Status != b.Status || a.Version != b.Version || !a.StartedAt.Equal(b.StartedAt) || !a.FinishedAt.Equal(b.FinishedAt) || a.Rounds != b.Rounds || a.SpawnUsed != b.SpawnUsed || a.Findings != b.Findings || a.Reason != b.Reason || a.Usage != b.Usage || !slices.Equal(a.Warnings, b.Warnings) || len(a.History) != len(b.History) || len(a.Tools) != len(b.Tools) {
		return false
	}
	for i, left := range a.History {
		right := b.History[i]
		if left.Role != right.Role || left.Content != right.Content || left.Reasoning != right.Reasoning || left.ToolCallID != right.ToolCallID || !slices.Equal(left.ToolCalls, right.ToolCalls) {
			return false
		}
	}
	for i, left := range a.Tools {
		right := b.Tools[i]
		x, y := left.Result, right.Result
		if left.CallID != right.CallID || left.Name != right.Name || left.Arguments != right.Arguments || x.Status != y.Status || x.Content != y.Content || x.Source != y.Source || x.SourceTaskID != y.SourceTaskID || x.Truncated != y.Truncated || x.ArtifactID != y.ArtifactID || x.NextOffset != y.NextOffset || x.Cursor != y.Cursor || !slices.Equal(x.Warnings, y.Warnings) {
			return false
		}
	}
	return true
}

func sameTaskRecords(a, b []explore.Record) bool {
	return slices.EqualFunc(a, b, sameTaskRecord)
}

func sortTasks(records []explore.Record) {
	slices.SortStableFunc(records, func(a, b explore.Record) int {
		if cmp := a.AcceptedAt.Compare(b.AcceptedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// ResumeTasks merges the newest session-owned records, marks unfinished work
// interrupted, and resolves only identifiable accepted task calls. It never
// replays work or saves automatically. Call before rebuilding UI entries, then
// persist the repaired snapshot through the normal idle optimistic Save path.
// Malformed/ambiguous histories remain readable and are not invented or erased.
func ResumeTasks(snapshot *Snapshot, records []explore.Record) (changed bool) {
	if snapshot == nil || !validID(snapshot.ID) {
		return false
	}
	merged := slices.Clone(snapshot.Tasks)
	indices, ambiguous := make(map[string]int), make(map[string]bool)
	for i, record := range merged {
		if _, exists := indices[record.ID]; exists {
			ambiguous[record.ID] = true
		}
		indices[record.ID] = i
	}
	for _, record := range records {
		if validateTask(record, snapshot.ID, snapshot.Root) != nil || ambiguous[record.ID] {
			continue
		}
		if i, exists := indices[record.ID]; exists {
			if sameTaskIdentity(merged[i], record) && record.Version > merged[i].Version {
				merged[i] = record
			}
		} else {
			indices[record.ID] = len(merged)
			merged = append(merged, record)
		}
	}
	dirty := make(map[string]bool)
	valid := make(map[string]bool)
	now := time.Now().UTC()
	for i := range merged {
		r := &merged[i]
		if ambiguous[r.ID] || validateTask(*r, snapshot.ID, snapshot.Root) != nil {
			continue
		}
		valid[r.ID] = true
		switch r.Status {
		case explore.Queued, explore.Running, explore.Waiting:
			r.Status, r.Reason = explore.Interrupted, interruptedTaskText
			r.FinishedAt = now
			if !slices.Contains(r.Warnings, interruptedTaskText) {
				r.Warnings = append(slices.Clone(r.Warnings), interruptedTaskText)
			}
			dirty[r.ID] = true
		}
	}
	// Rebuild identity indices after merging; call identity is scoped to the
	// actual parent transcript, not shared across sibling child conversations.
	children := make(map[string][]explore.Record)
	for _, record := range merged {
		if valid[record.ID] && record.Status == explore.Interrupted {
			children[record.ParentID] = append(children[record.ParentID], record)
		}
	}
	history, _, repaired := repairTaskHistory(snapshot.History, children[""])
	if repaired {
		snapshot.History = history
		changed = true
	}
	for i := range merged {
		parent := &merged[i]
		if !valid[parent.ID] {
			continue
		}
		var nested []explore.Record
		for _, child := range children[parent.ID] {
			if taskParentMatches(*parent, child) {
				nested = append(nested, child)
			}
		}
		history, repairs, repaired := repairTaskHistory(parent.History, nested)
		if repaired {
			parent.History = history
			parent.Tools = repairTaskTools(parent.Tools, repairs)
			dirty[parent.ID] = true
		}
	}
	for i := range merged {
		if dirty[merged[i].ID] && merged[i].Version < math.MaxInt64 {
			merged[i].Version++
		}
	}
	sortTasks(merged)
	if !sameTaskRecords(snapshot.Tasks, merged) {
		snapshot.Tasks = merged
		changed = true
	}
	return changed
}

type taskHistoryRepair struct {
	call    model.ToolCall
	content string
}

func matchesTaskCall(call model.ToolCall, record explore.Record) bool {
	if call.ID != record.ParentCallID || call.Name != "task" || len(call.Arguments) > explore.MaxBriefBytes*6+4096 {
		return false
	}
	var spec explore.Spec
	decoder := json.NewDecoder(strings.NewReader(call.Arguments))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool, 3)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		var value string
		if decoder.Decode(&value) != nil {
			return false
		}
		switch key {
		case "agent":
			spec.Agent = value
		case "description":
			spec.Description = value
		case "prompt":
			spec.Prompt = value
		default:
			return false
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(seen) != 3 || decoder.Decode(new(any)) != io.EOF {
		return false
	}
	return spec.Agent == record.Agent && spec.Description == record.Description && spec.Prompt == record.Prompt
}

// Complete result groups may have their exact interruption sentinel replaced.
// A missing suffix is repaired only at the end of history when every missing
// call has a unique matching accepted record. Other missing/malformed groups
// remain untouched; no generic tool result or assistant call is manufactured.
func repairTaskHistory(history []model.Message, records []explore.Record) ([]model.Message, []taskHistoryRepair, bool) {
	if len(records) == 0 {
		return history, nil, false
	}
	byCall, ambiguous := make(map[string]explore.Record), make(map[string]bool)
	for _, record := range records {
		if _, exists := byCall[record.ParentCallID]; exists {
			ambiguous[record.ParentCallID] = true
		}
		byCall[record.ParentCallID] = record
	}
	callCount, resultCount := make(map[string]int), make(map[string]int)
	for _, message := range history {
		for _, call := range message.ToolCalls {
			callCount[call.ID]++
		}
		if message.Role == "tool" {
			resultCount[message.ToolCallID]++
		}
	}
	out := slices.Clone(history)
	var repairs []taskHistoryRepair
	for i, message := range history {
		if message.Role != "assistant" || len(message.ToolCalls) == 0 || message.ToolCallID != "" {
			continue
		}
		end := i + 1
		for end < len(history) && history[end].Role == "tool" {
			end++
		}
		valid := end-i-1 <= len(message.ToolCalls)
		for offset, call := range message.ToolCalls {
			if call.ID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) || callCount[call.ID] != 1 || resultCount[call.ID] > 1 {
				valid = false
				break
			}
			if i+1+offset < end {
				result := history[i+1+offset]
				if result.ToolCallID != call.ID || len(result.ToolCalls) != 0 || result.Reasoning != "" {
					valid = false
					break
				}
			} else {
				record, ok := byCall[call.ID]
				if end != len(history) || resultCount[call.ID] != 0 || !ok || ambiguous[call.ID] || !matchesTaskCall(call, record) {
					valid = false
					break
				}
			}
		}
		if !valid {
			continue
		}
		for offset, call := range message.ToolCalls {
			record, ok := byCall[call.ID]
			if !ok || ambiguous[call.ID] || !matchesTaskCall(call, record) {
				continue
			}
			index := i + 1 + offset
			if index < end && history[index].Content != interruptedToolText {
				continue
			}
			content := resumedTaskContent(record)
			if index < end {
				out[index].Content = content
			} else {
				out = append(out, model.Message{Role: "tool", ToolCallID: call.ID, Content: content})
			}
			repairs = append(repairs, taskHistoryRepair{call: call, content: content})
		}
	}
	return out, repairs, len(repairs) != 0
}

func repairTaskTools(records []explore.ToolRecord, repairs []taskHistoryRepair) []explore.ToolRecord {
	out := slices.Clone(records)
	for _, repair := range repairs {
		count, index := 0, -1
		for i, record := range out {
			if record.CallID == repair.call.ID {
				count++
				index = i
			}
		}
		result := tools.Result{Status: tools.Cancelled, Content: repair.content, Source: tools.Source{Kind: "builtin", Tool: "task"}}
		if count == 0 {
			out = append(out, explore.ToolRecord{CallID: repair.call.ID, Name: "task", Arguments: repair.call.Arguments, Result: result})
		} else if count == 1 && out[index].Name == "task" && out[index].Arguments == repair.call.Arguments && (out[index].Result.Content == interruptedToolText || out[index].Result.Content == "" && out[index].Result.Status == "") {
			out[index].Result = result
		}
	}
	return out
}

// The attributed outcome remains valid JSON, even when partial findings need
// clipping. Bound individual metadata first, then the escaped JSON byte size.
func resumedTaskContent(r explore.Record) string {
	warnings := make([]string, 0, 8)
	for _, warning := range r.Warnings {
		if len(warnings) == 7 {
			break
		}
		warnings = append(warnings, taskPrefix(warning, 512))
	}
	warnings = append(warnings, interruptedTaskText)
	outcome := explore.Outcome{TaskID: r.ID, Agent: r.Agent, Depth: r.Depth, Status: r.Status,
		Findings: taskPrefix(r.Findings, tools.MaxInlineBytes), Reason: taskPrefix(r.Reason, 2048), Warnings: warnings,
		Rounds: r.Rounds, SpawnUsed: r.SpawnUsed, Deadline: r.Deadline, Usage: r.Usage}
	data, _ := json.Marshal(outcome)
	if len(data) <= tools.MaxInlineBytes && len(outcome.Findings) == len(r.Findings) {
		return string(data)
	}
	outcome.Warnings[len(outcome.Warnings)-1] = "Run interrupted; partial findings were limited to the inline result size. This task was not replayed."
	findings := outcome.Findings
	low, high := 0, len(findings)
	for low < high {
		mid := low + (high-low+1)/2
		outcome.Findings = taskPrefix(findings, mid)
		data, _ = json.Marshal(outcome)
		if len(data) <= tools.MaxInlineBytes {
			low = mid
		} else {
			high = mid - 1
		}
	}
	outcome.Findings = taskPrefix(findings, low)
	data, _ = json.Marshal(outcome)
	return string(data)
}

func taskPrefix(text string, limit int) string {
	if len(text) > limit {
		for limit > 0 && !utf8.RuneStart(text[limit]) {
			limit--
		}
		text = text[:limit]
	}
	return strings.ToValidUTF8(text, "\uFFFD")
}
