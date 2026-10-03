package session

import (
	"strconv"
	"strings"
	"testing"

	"likha/internal/model"
)

func benchSnapshot(messages int) Snapshot {
	history := make([]model.Message, 0, messages)
	entries := make([]Entry, 0, messages)
	paragraph := strings.Repeat("The repository walk opens each path component by descriptor. ", 20)
	for range messages {
		history = append(history, model.Message{Role: "user", Content: "question about the walk " + paragraph})
		history = append(history, model.Message{Role: "assistant", Content: paragraph})
		entries = append(entries, Entry{Role: "User", Content: "question about the walk"})
		entries = append(entries, Entry{Role: "Assistant", Content: paragraph})
	}
	return Snapshot{History: history, Entries: entries}
}

// BenchmarkStoreSave measures one persist: a full re-encode plus a compare
// and swap on the growing snapshot blob. Every tool result triggers it.
func BenchmarkStoreSave(b *testing.B) {
	store, err := Open(b.TempDir(), b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Create()
	if err != nil {
		b.Fatal(err)
	}
	bench := benchSnapshot(50)
	snapshot.History, snapshot.Entries = bench.History, bench.Entries
	b.ResetTimer()
	for range b.N {
		if err := store.Save(snapshot); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStoreSaveScaling shows the persist cost against conversation size.
func BenchmarkStoreSaveScaling(b *testing.B) {
	for _, size := range []int{10, 50, 200} {
		b.Run("messages="+strconv.Itoa(size), func(b *testing.B) {
			store, err := Open(b.TempDir(), b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			snapshot, err := store.Create()
			if err != nil {
				b.Fatal(err)
			}
			bench := benchSnapshot(size)
			snapshot.History, snapshot.Entries = bench.History, bench.Entries
			b.ResetTimer()
			for range b.N {
				if err := store.Save(snapshot); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
