package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

// variantSessionUI is variantTestUI on an existing, store-backed session:
// MV-25 lets blank Enter commit a selection only in an existing session.
func variantSessionUI(t *testing.T, id string) (*ui, *session.Store) {
	t.Helper()
	root, state := t.TempDir(), t.TempDir()
	store, err := session.Open(state, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	client, err := model.New("https://api.openai.com/v1", id, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, client, id, providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, state, store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m, store
}

func TestRestoreSessionVariantOverridesGlobalPreference(t *testing.T) {
	for _, choice := range []string{"low", "", "gone"} {
		t.Run("choice_"+choice, func(t *testing.T) {
			m := variantTestUI(t, "gpt-5.5")
			m.selectVariant("high")
			m.snapshot.Selection = &model.Selection{Provider: "openai", Model: "gpt-5.5", Variant: choice}
			before := len(m.entries)
			if cmd := m.restoreCommittedSelection(); cmd != nil {
				t.Fatal("same route restore became asynchronous")
			}
			want := choice
			if choice == "gone" {
				want = ""
				if len(m.entries) != before+1 || !strings.Contains(lastEntry(m).content, "Saved variant gone") {
					t.Fatal("stale restore not noticed once")
				}
			}
			if m.client.Variant() != want || m.working || len(m.queue) != 0 {
				t.Fatal("restore ignored session or started work")
			}
			cfg, err := providers.LoadStoredConfig(m.stateDir)
			if stored, _ := cfg.StoredVariant("openai", "gpt-5.5"); err != nil || stored != "high" {
				t.Fatal("restore overwrote global preference")
			}
		})
	}
}

func TestLateSessionRestorationCannotReplaceNewSelection(t *testing.T) {
	for _, cause := range []string{"session", "revision", "working"} {
		t.Run(cause, func(t *testing.T) {
			m := variantTestUI(t, "gpt-5.5")
			client, err := m.client.Fork()
			if err != nil {
				t.Fatal(err)
			}
			p, _ := model.LookupProvider("openai")
			result := selectionRestoredMsg{sessionID: m.snapshot.ID, revision: m.selectionRevision, client: client, provider: p, selection: model.Selection{Provider: p.Name, Model: "gpt-5.5"}}
			switch cause {
			case "session":
				result.sessionID = "other"
			case "revision":
				m.selectVariant("high")
			case "working":
				m.working = true
			}
			current := m.client
			m.Update(result)
			if m.client != current {
				t.Fatal("late restoration replaced the composer")
			}
		})
	}
}

func TestUnavailableSavedIdentityKeepsComposer(t *testing.T) {
	m := variantTestUI(t, "gpt-5.5")
	current := m.client
	m.Update(selectionRestoredMsg{sessionID: m.snapshot.ID, revision: m.selectionRevision, err: errors.New("saved provider unavailable")})
	if m.client != current || !strings.Contains(lastEntry(m).content, "unavailable") || m.working {
		t.Fatal("unavailable identity replaced current model or started work")
	}
	m.snapshot.Selection = &model.Selection{Provider: "unknown", Model: "missing"}
	cmd := m.restoreCommittedSelection()
	if cmd == nil {
		t.Fatal("foreign selection was not checked")
	}
	m.Update(cmd())
	if m.client != current || m.working {
		t.Fatal("unknown identity changed execution")
	}
}

func TestIdleBlankEnterCommitsSelectionWithoutRequest(t *testing.T) {
	m, store := variantSessionUI(t, "gpt-5.5")
	m.selectVariant("high")
	m.Update(key("enter"))
	if m.working || m.snapshot.Selection == nil || m.snapshot.Selection.Variant != "high" {
		t.Fatal("blank Enter did not commit selection without work")
	}
	saved, err := store.Load(m.snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Selection == nil || saved.Selection.Variant != "high" {
		t.Fatalf("committed selection was not saved with the session: %+v", saved.Selection)
	}
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := providers.ApplySavedVariant(context.Background(), m.stateDir, m.client, m.activeProvider(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if choice, _ := cfg.StoredVariant("openai", "gpt-5.5"); choice != "high" {
		t.Fatal("committed selection corrupted preference")
	}
}
