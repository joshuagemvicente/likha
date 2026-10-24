package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func changeSetPayload(n int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"files":[{"path":"f%d.txt","new_sha256":"x","new_mode":420}],"created_dirs":[]}`, n))
}

func TestChangeSetsNewestFirstAndDelete(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.LatestChangeSet(snapshot.ID); ok || err != nil {
		t.Fatalf("empty history: ok %v err %v", ok, err)
	}
	first, err := store.SaveChangeSet(snapshot.ID, "call_1", changeSetPayload(1))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SaveChangeSet(snapshot.ID, "call_2", changeSetPayload(2))
	if err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.LatestChangeSet(snapshot.ID)
	if err != nil || !ok || latest.ID != second || latest.ToolCallID != "call_2" || string(latest.Payload) != string(changeSetPayload(2)) || latest.Created.IsZero() {
		t.Fatalf("latest = %+v ok %v err %v", latest, ok, err)
	}
	if err := store.DeleteChangeSet(snapshot.ID, second); err != nil {
		t.Fatal(err)
	}
	if latest, _, _ := store.LatestChangeSet(snapshot.ID); latest.ID != first {
		t.Fatalf("after delete latest = %q, want %q", latest.ID, first)
	}
	if err := store.DeleteChangeSet(snapshot.ID, second); err == nil {
		t.Fatal("deleted an already removed change set")
	}
	if dropped, err := store.ChangeSetsDropped(snapshot.ID); dropped || err != nil {
		t.Fatalf("dropped = %v, %v", dropped, err)
	}
}

func TestChangeSetsPruneOldestByCount(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, MaxChangeSets+2)
	for i := range MaxChangeSets + 2 {
		id, err := store.SaveChangeSet(snapshot.ID, "call", changeSetPayload(i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM edit_changesets WHERE session_id = ?`, snapshot.ID).Scan(&count); err != nil || count != MaxChangeSets {
		t.Fatalf("kept %d change sets, %v", count, err)
	}
	for _, id := range ids[:2] {
		var found int
		if err := store.db.QueryRow(`SELECT count(*) FROM edit_changesets WHERE id = ?`, id).Scan(&found); err != nil || found != 0 {
			t.Fatalf("oldest change set %s survived pruning", id)
		}
	}
	if dropped, err := store.ChangeSetsDropped(snapshot.ID); !dropped || err != nil {
		t.Fatalf("dropped = %v, %v", dropped, err)
	}
}

func TestPruneChangeSetsByBytesKeepsHistoryContiguous(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	insert := func(id string, created int64, size int) {
		t.Helper()
		if _, err := store.db.Exec(`INSERT INTO edit_changesets(id, session_id, repository, created_ns, tool_call_id, size, payload)
			VALUES (?, ?, ?, ?, 'call', ?, '{}')`, id, snapshot.ID, store.root, created, size); err != nil {
			t.Fatal(err)
		}
	}
	// Newest first: 40 + 50 exceeds 80, so the 50-byte set and the small,
	// older one behind it are both dropped.
	insert(strings.Repeat("a", 32), 3, 40)
	insert(strings.Repeat("b", 32), 2, 50)
	insert(strings.Repeat("c", 32), 1, 10)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := pruneChangeSets(tx, snapshot.ID, store.root, MaxChangeSets, 80); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := store.db.Query(`SELECT id FROM edit_changesets WHERE session_id = ?`, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kept []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		kept = append(kept, id)
	}
	if !reflect.DeepEqual(kept, []string{strings.Repeat("a", 32)}) {
		t.Fatalf("kept %v", kept)
	}
}

func TestChangeSetTooLargeIsRefused(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`"` + strings.Repeat("x", MaxChangeSetBytes) + `"`)
	if _, err := store.SaveChangeSet(snapshot.ID, "call", payload); !errors.Is(err, ErrChangeSetTooLarge) {
		t.Fatalf("oversized change set: %v", err)
	}
	if _, ok, _ := store.LatestChangeSet(snapshot.ID); ok {
		t.Fatal("an oversized change set was saved")
	}
}

func TestChangeSetsAreSessionAndRepositoryScoped(t *testing.T) {
	store, base, _ := testStore(t)
	first, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveChangeSet(first.ID, "call", changeSetPayload(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.LatestChangeSet(second.ID); ok {
		t.Fatal("another session sees the change set")
	}
	if err := store.DeleteChangeSet(second.ID, id); err == nil {
		t.Fatal("another session deleted the change set")
	}
	other, err := Open(base, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, ok, _ := other.LatestChangeSet(first.ID); ok {
		t.Fatal("another repository sees the change set")
	}
	if _, err := other.SaveChangeSet(first.ID, "call", changeSetPayload(2)); err == nil {
		t.Fatal("another repository saved into the session's history")
	}
}

func TestChangeSetsCascadeOnSessionDelete(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	for i := range MaxChangeSets + 1 {
		if _, err := store.SaveChangeSet(snapshot.ID, "call", changeSetPayload(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Delete(snapshot.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"edit_changesets", "edit_changeset_drops"} {
		var count int
		if err := store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s kept %d rows after session delete: %v", table, count, err)
		}
	}
}

// A database written before undo existed has no edit_changesets tables. It
// must open unchanged, keep its sessions, and gain the tables.
func TestDatabaseWithoutUndoHistoryMigrates(t *testing.T) {
	store, base, repo := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Entries = []Entry{{Role: "You", Content: "hello"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`DROP TABLE edit_changesets`, `DROP TABLE edit_changeset_drops`} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(base, repo)
	if err != nil {
		t.Fatalf("pre-undo database did not open: %v", err)
	}
	defer reopened.Close()
	loaded, err := reopened.Load(snapshot.ID)
	if err != nil || !reflect.DeepEqual(loaded.Entries, snapshot.Entries) {
		t.Fatalf("session lost across migration: %+v %v", loaded, err)
	}
	if _, err := reopened.SaveChangeSet(snapshot.ID, "call", changeSetPayload(1)); err != nil {
		t.Fatalf("migrated database cannot save undo history: %v", err)
	}
	var version int
	if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("schema version changed to %d: %v", version, err)
	}
}

// /undo must take the most recently saved set even when the wall clock
// stepped back between two edits (NTP correction, VM resume).
func TestChangeSetsStayNewestFirstWhenClockStepsBack(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.SaveChangeSet(snapshot.ID, "call_1", changeSetPayload(1))
	if err != nil {
		t.Fatal(err)
	}
	// Record the first set as saved an hour ahead of the clock used next.
	ahead := time.Now().Add(time.Hour).UTC().UnixNano()
	if _, err := store.db.Exec(`UPDATE edit_changesets SET created_ns = ? WHERE id = ?`, ahead, first); err != nil {
		t.Fatal(err)
	}
	second, err := store.SaveChangeSet(snapshot.ID, "call_2", changeSetPayload(2))
	if err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.LatestChangeSet(snapshot.ID)
	if err != nil || !ok {
		t.Fatalf("LatestChangeSet: ok %v, err %v", ok, err)
	}
	if latest.ID != second {
		t.Fatalf("latest change set = %q (call %s), want the later save %q", latest.ID, latest.ToolCallID, second)
	}
}
