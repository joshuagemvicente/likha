package session

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lisa/internal/model"
)

func testStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "state")
	repo := t.TempDir()
	store, err := Open(base, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, base, repo
}

func TestCreateLoadListCanonicalRepositoryAndDurableReopen(t *testing.T) {
	store, base, repo := testStore(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	first, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if first.Root != canonical || !validID(first.ID) {
		t.Fatalf("unexpected created identity: %+v", first)
	}
	first.History = []model.Message{{Role: "user", Content: "History prompt"}, {Role: "assistant", Content: "Text", ToolCalls: []model.ToolCall{{ID: "call1", Name: "read_file", Arguments: `{\"path\":\"a.go\"}`}}}}
	first.Entries = []Entry{{Role: "activity", Content: "Read a.go"}, {Role: "user", Content: "  Fix this, please  "}, {Role: "assistant", Content: "Done"}}
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	if _, err := store.db.Exec("UPDATE sessions SET updated_ns = ? WHERE id = ?", older.UnixNano(), first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	second.History = []model.Message{{Role: "user", Content: "Second prompt"}}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(base, alias)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := reopened.Load(first.ID)
	if err != nil || !reflect.DeepEqual(loaded, first) {
		t.Fatalf("session did not survive reopen: %+v, %v", loaded, err)
	}
	listed, err := reopened.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != second.ID || listed[0].Title != "Second prompt" || listed[1].ID != first.ID || listed[1].Title != "Fix this, please" || !listed[1].Updated.Equal(older) || !listed[0].Updated.After(listed[1].Updated) {
		t.Fatalf("incorrect titles or newest-first order: %+v", listed)
	}
	if _, err := os.Stat(filepath.Join(base, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("old JSON sessions directory still exists: %v", err)
	}
}

func TestConcurrentSavesRequireReload(t *testing.T) {
	firstStore, base, repo := testStore(t)
	created, err := firstStore.Create()
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := Open(base, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()

	first, err := firstStore.Load(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondStore.Load(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	first.History = []model.Message{{Role: "user", Content: "First writer"}}
	first.Entries = []Entry{{Role: "user", Content: "First writer"}}
	if err := firstStore.Save(first); err != nil {
		t.Fatal(err)
	}
	before, err := firstStore.List()
	if err != nil {
		t.Fatal(err)
	}
	second.History = []model.Message{{Role: "user", Content: "Stale writer"}}
	second.Entries = []Entry{{Role: "user", Content: "Stale writer"}}
	if err := secondStore.Save(second); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save = %v; want actionable conflict", err)
	}
	saved, err := secondStore.Load(created.ID)
	if err != nil || !reflect.DeepEqual(saved, first) {
		t.Fatalf("stale save replaced the winner: %+v, %v", saved, err)
	}
	after, err := secondStore.List()
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("stale save changed metadata: before %+v, after %+v, %v", before, after, err)
	}

	saved.History = append(saved.History, model.Message{Role: "user", Content: "After reload"})
	saved.Entries = append(saved.Entries, Entry{Role: "user", Content: "After reload"})
	if err := secondStore.Save(saved); err != nil {
		t.Fatalf("reloaded session could not be saved: %v", err)
	}
	saved.History = append(saved.History, model.Message{Role: "assistant", Content: "Continued"})
	saved.Entries = append(saved.Entries, Entry{Role: "assistant", Content: "Continued"})
	if err := secondStore.Save(saved); err != nil {
		t.Fatalf("successive save of the same snapshot failed: %v", err)
	}
	if err := firstStore.Save(first); !errors.Is(err, ErrConflict) {
		t.Fatalf("first writer overwrote new history: %v", err)
	}
	final, err := firstStore.Load(created.ID)
	if err != nil || !reflect.DeepEqual(final, saved) {
		t.Fatalf("reloaded save lost history: %+v, %v", final, err)
	}
}

func TestRepositoryIsolationAndInvalidIDs(t *testing.T) {
	store, base, _ := testStore(t)
	first, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(base, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	listed, err := other.List()
	if err != nil || len(listed) != 0 {
		t.Fatalf("unrelated repository sees sessions: %+v, %v", listed, err)
	}
	if _, err := other.Load(first.ID); err == nil {
		t.Fatal("unrelated repository loaded another repository's session")
	}
	if err := other.Save(first); err == nil {
		t.Fatal("saved a snapshot for another repository")
	}
	for _, id := range []string{"", "../" + first.ID, "..", first.ID + "/file", strings.ToUpper(first.ID), strings.Repeat("a", 31)} {
		if _, err := store.Load(id); err == nil {
			t.Errorf("accepted invalid ID %q", id)
		}
		attempt := first
		attempt.ID = id
		if err := store.Save(attempt); err == nil {
			t.Errorf("accepted invalid saved ID %q", id)
		}
	}
	unknown := first
	unknown.ID = strings.Repeat("f", 32)
	if unknown.ID == first.ID {
		unknown.ID = strings.Repeat("e", 32)
	}
	if err := store.Save(unknown); err == nil {
		t.Fatal("Save created a session without Create")
	}
	if _, err := Open(base, filepath.Join(base, "missing-repository")); err == nil {
		t.Fatal("accepted a missing repository")
	}
}

func TestRejectedSaveRollsBackSnapshotAndMetadata(t *testing.T) {
	store, _, _ := testStore(t)
	original, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	original.Entries = []Entry{{Role: "user", Content: "Before"}}
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	before, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_session_update AFTER UPDATE ON sessions
		BEGIN SELECT RAISE(ABORT, 'reject this update'); END`); err != nil {
		t.Fatal(err)
	}
	attempt := original
	attempt.Entries = []Entry{{Role: "user", Content: "After"}}
	if err := store.Save(attempt); err == nil {
		t.Fatal("rejected update succeeded")
	}
	loaded, err := store.Load(original.ID)
	if err != nil || !reflect.DeepEqual(loaded, original) {
		t.Fatalf("rejected update changed snapshot: %+v, %v", loaded, err)
	}
	after, err := store.List()
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected update changed metadata: before %+v, after %+v, %v", before, after, err)
	}
}

func TestCorruptRowsAreIsolated(t *testing.T) {
	store, _, _ := testStore(t)
	healthy, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	healthy.Entries = []Entry{{Role: "user", Content: "Preserved"}}
	if err := store.Save(healthy); err != nil {
		t.Fatal(err)
	}
	broken, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{
		[]byte(`{"ID":`),
		[]byte(`{"ID":"wrong","Root":"` + broken.Root + `"}`),
		[]byte(`{"ID":"` + broken.ID + `","Root":"wrong"}`),
	} {
		if _, err := store.db.Exec("UPDATE sessions SET snapshot = ? WHERE id = ?", data, broken.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(broken.ID); err == nil {
			t.Fatal("loaded damaged session")
		}
		if err := store.Save(broken); err == nil {
			t.Fatal("overwrote damaged session")
		}
		listed, err := store.List()
		if err != nil || len(listed) != 1 || listed[0].ID != healthy.ID || listed[0].Title != "Preserved" {
			t.Fatalf("damaged row hid intact session: %+v, %v", listed, err)
		}
	}
	if _, err := store.db.Exec("UPDATE sessions SET id = ? WHERE id = ?", "not-an-id", broken.ID); err != nil {
		t.Fatal(err)
	}
	listed, err := store.List()
	if err != nil || len(listed) != 1 || listed[0].ID != healthy.ID {
		t.Fatalf("invalid row ID hid intact session: %+v, %v", listed, err)
	}
	loaded, err := store.Load(healthy.ID)
	if err != nil || !reflect.DeepEqual(loaded, healthy) {
		t.Fatalf("intact session lost: %+v, %v", loaded, err)
	}
}

func TestPrivateDatabaseAndPragmas(t *testing.T) {
	store, base, _ := testStore(t)
	if _, err := store.Create(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base, filepath.Join(base, "sessions.sqlite"), filepath.Join(base, "sessions.sqlite-wal"), filepath.Join(base, "sessions.sqlite-shm")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Errorf("session data is accessible to others: %s (%v)", path, info.Mode())
		}
	}
	for pragma, expected := range map[string]int{"user_version": schemaVersion, "journal_mode": -1, "synchronous": 2, "foreign_keys": 1, "busy_timeout": 5000} {
		if pragma == "journal_mode" {
			var mode string
			if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
				t.Errorf("journal mode = %q, %v", mode, err)
			}
			continue
		}
		var actual int
		if err := store.db.QueryRow("PRAGMA " + pragma).Scan(&actual); err != nil || actual != expected {
			t.Errorf("%s = %d, %v; want %d", pragma, actual, err, expected)
		}
	}
}

func TestSchemaVersionAndMigrationGuard(t *testing.T) {
	repo := t.TempDir()
	for _, tc := range []struct {
		name       string
		initialize string
		wantError  bool
	}{
		{name: "fresh", initialize: "", wantError: false},
		{name: "unknown unversioned schema", initialize: "CREATE TABLE unknown (id INTEGER)", wantError: true},
		{name: "future schema", initialize: "PRAGMA user_version = 2", wantError: true},
		{name: "missing versioned schema", initialize: "PRAGMA user_version = 1", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, "sessions.sqlite")
			if tc.initialize != "" {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(tc.initialize); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			store, err := Open(base, repo)
			if tc.wantError {
				if err == nil {
					store.Close()
					t.Fatal("accepted unsupported database schema")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(base, repo)
			if err != nil {
				t.Fatalf("versioned schema did not reopen: %v", err)
			}
			defer reopened.Close()
			var version int
			if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
				t.Fatalf("schema version = %d, %v", version, err)
			}
		})
	}
}

func TestDatabaseSymlinkRejected(t *testing.T) {
	base := t.TempDir()
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "sessions.sqlite")); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(base, repo); err == nil {
		store.Close()
		t.Fatal("followed symlinked database")
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != "secret" {
		t.Fatalf("external file changed: %q, %v", content, err)
	}
}

func TestTitleUsesFirstUserPrompt(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Entries = []Entry{{Role: "assistant", Content: "setup"}, {Role: "user", Content: "  First prompt  "}, {Role: "user", Content: "Later prompt"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	listed, err := store.List()
	if err != nil || len(listed) != 1 || listed[0].Title != "First prompt" {
		t.Fatalf("title not derived from first user prompt: %+v, %v", listed, err)
	}
}
