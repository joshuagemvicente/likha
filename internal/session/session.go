// Package session stores completed conversations for a selected repository.
package session

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"

	"lisa/internal/model"
)

const schemaVersion = 1

// ErrConflict means the session changed since it was loaded. Reload it and
// reconcile any unsaved conversation before trying again.
var ErrConflict = errors.New("session changed in another process; reload and reconcile before saving")

// Entry is a completed, displayable conversation event.
type Entry struct {
	Role    string
	Content string
}

type Snapshot struct {
	ID      string
	Root    string
	History []model.Message
	Entries []Entry

	// revision belongs to the loaded snapshot, not to the Store: two Store
	// instances must never assume that their copies are equally current.
	revision *int64
}

type Summary struct {
	ID      string
	Updated time.Time
	Title   string
}

// Store holds sessions for one canonical repository in a private local database.
// Close releases its database connection when the store is no longer needed.
type Store struct {
	root string
	db   *sql.DB
}

func Open(base, root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("repository path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("repository path: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("repository path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repository %q is not a directory", root)
	}
	absoluteBase, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("session directory: %w", err)
	}
	if err := os.MkdirAll(absoluteBase, 0700); err != nil {
		return nil, fmt.Errorf("session directory: %w", err)
	}
	info, err = os.Lstat(absoluteBase)
	if err != nil {
		return nil, fmt.Errorf("session directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("session directory %q is not a directory", absoluteBase)
	}
	if err := os.Chmod(absoluteBase, 0700); err != nil {
		return nil, fmt.Errorf("private session directory: %w", err)
	}

	path := filepath.Join(absoluteBase, "sessions.sqlite")
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open session database: %w", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err == nil && stat.Mode&unix.S_IFMT != unix.S_IFREG {
		err = errors.New("session database is not a regular file")
	}
	if err == nil {
		err = unix.Fchmod(fd, 0600)
	}
	closeErr := unix.Close(fd)
	if err != nil {
		return nil, fmt.Errorf("private session database: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close session database file: %w", closeErr)
	}

	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	// These pragmas apply per connection. A single connection keeps them in force
	// for every operation while SQLite's WAL still allows other Store instances.
	db.SetMaxOpenConns(1)
	if err := configure(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{root: canonical, db: db}, nil
}

func configure(db *sql.DB) error {
	for _, pragma := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("configure SQLite (%s): %w", pragma, err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin session schema setup: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read session schema version: %w", err)
	}
	switch version {
	case 0:
		// Version zero is only a new database. Never silently reinterpret an
		// existing unversioned schema as ours or overwrite its data.
		var existing int
		if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&existing); err != nil {
			return fmt.Errorf("inspect unversioned session schema: %w", err)
		}
		if existing != 0 {
			return errors.New("unversioned session database contains an unknown schema")
		}
		for _, statement := range []string{
			`CREATE TABLE sessions (
				id TEXT PRIMARY KEY NOT NULL,
				repository TEXT NOT NULL,
				updated_ns INTEGER NOT NULL,
				title TEXT NOT NULL,
				snapshot BLOB NOT NULL,
				revision INTEGER NOT NULL CHECK(revision >= 0)
			)`,
			`CREATE INDEX sessions_by_repository_updated ON sessions(repository, updated_ns DESC, id ASC)`,
			"PRAGMA user_version = 1",
		} {
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("initialize session schema: %w", err)
			}
		}
	case schemaVersion:
		if _, err := tx.Exec("SELECT id, repository, updated_ns, title, snapshot, revision FROM sessions LIMIT 0"); err != nil {
			return fmt.Errorf("invalid session schema: %w", err)
		}
	default:
		return fmt.Errorf("unsupported session schema version %d (expected %d)", version, schemaVersion)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session schema setup: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func randomID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Create writes an empty, resumable session before returning its identifier.
func (s *Store) Create() (Snapshot, error) {
	for {
		id, err := randomID()
		if err != nil {
			return Snapshot{}, fmt.Errorf("session ID: %w", err)
		}
		revision := int64(0)
		snapshot := Snapshot{ID: id, Root: s.root, revision: &revision}
		data, err := json.Marshal(snapshot)
		if err != nil {
			return Snapshot{}, fmt.Errorf("encode session: %w", err)
		}
		result, err := s.db.Exec("INSERT OR IGNORE INTO sessions (id, repository, updated_ns, title, snapshot, revision) VALUES (?, ?, ?, ?, ?, ?)",
			id, s.root, time.Now().UTC().UnixNano(), title(snapshot), data, revision)
		if err != nil {
			return Snapshot{}, fmt.Errorf("create session: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return Snapshot{}, fmt.Errorf("verify created session: %w", err)
		}
		if affected == 1 {
			return snapshot, nil
		}
	}
}

func (s *Store) List() ([]Summary, error) {
	rows, err := s.db.Query(`SELECT id, updated_ns, title, snapshot FROM sessions
		WHERE repository = ? AND typeof(id) = 'text' AND typeof(updated_ns) = 'integer'
		AND typeof(title) = 'text' AND typeof(snapshot) = 'blob'
		ORDER BY updated_ns DESC, id ASC`, s.root)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var summaries []Summary
	for rows.Next() {
		var id, savedTitle string
		var updated int64
		var data []byte
		if err := rows.Scan(&id, &updated, &savedTitle, &data); err != nil {
			return nil, fmt.Errorf("read session summary: %w", err)
		}
		snapshot, err := decode(id, s.root, data)
		if err != nil || savedTitle != title(snapshot) {
			continue // A damaged row must not hide other sessions.
		}
		summaries = append(summaries, Summary{ID: id, Updated: time.Unix(0, updated).UTC(), Title: savedTitle})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return summaries, nil
}

func title(snapshot Snapshot) string {
	for _, entry := range snapshot.Entries {
		if entry.Role == "user" {
			return strings.TrimSpace(entry.Content)
		}
	}
	for _, message := range snapshot.History {
		if message.Role == "user" {
			return strings.TrimSpace(message.Content)
		}
	}
	return "Untitled session"
}

func decode(id, root string, data []byte) (Snapshot, error) {
	var snapshot Snapshot
	if !validID(id) || json.Unmarshal(data, &snapshot) != nil || snapshot.ID != id || snapshot.Root != root {
		return Snapshot{}, fmt.Errorf("session %q has corrupt data or mismatched identity", id)
	}
	return snapshot, nil
}

func (s *Store) Load(id string) (Snapshot, error) {
	if !validID(id) {
		return Snapshot{}, fmt.Errorf("invalid session ID %q", id)
	}
	var data []byte
	var revision int64
	if err := s.db.QueryRow("SELECT snapshot, revision FROM sessions WHERE id = ? AND repository = ?", id, s.root).Scan(&data, &revision); err != nil {
		return Snapshot{}, fmt.Errorf("load session %s: %w", id, err)
	}
	snapshot, err := decode(id, s.root, data)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.revision = &revision
	return snapshot, nil
}

// Save atomically replaces an existing session only if it has not changed
// since this snapshot was loaded. Rejected updates leave its prior snapshot,
// title and timestamp unchanged.
func (s *Store) Save(snapshot Snapshot) error {
	if !validID(snapshot.ID) || snapshot.Root != s.root {
		return errors.New("session ID or repository does not match store")
	}
	if snapshot.revision == nil {
		return errors.New("session must be loaded or created before saving")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	var previous []byte
	var revision int64
	if err := s.db.QueryRow("SELECT snapshot, revision FROM sessions WHERE id = ? AND repository = ?", snapshot.ID, s.root).Scan(&previous, &revision); err != nil {
		return fmt.Errorf("load session before save: %w", err)
	}
	if _, err := decode(snapshot.ID, s.root, previous); err != nil {
		return err
	}
	expected := *snapshot.revision
	if expected != revision {
		return fmt.Errorf("save session %s: %w", snapshot.ID, ErrConflict)
	}
	result, err := s.db.Exec(`UPDATE sessions SET snapshot = ?, title = ?, updated_ns = ?, revision = revision + 1
		WHERE id = ? AND repository = ? AND revision = ? AND snapshot = ?`,
		data, title(snapshot), time.Now().UTC().UnixNano(), snapshot.ID, s.root, expected, previous)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("verify saved session: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("save session %s: %w", snapshot.ID, ErrConflict)
	}
	*snapshot.revision = expected + 1
	return nil
}
