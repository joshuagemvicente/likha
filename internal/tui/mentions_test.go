package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
)

func TestMentionPopupCompletes(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("x"), 0600)
	os.WriteFile(filepath.Join(root, "beta.md"), []byte("x"), 0600)
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, repo, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Typing @ schedules the index build; the message populates the popup.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !m.mention.open {
		t.Fatal("popup did not open on @")
	}
	// The walk cmd is discarded; the index message is fed synchronously.
	m.Update(fileIndexMsg{files: []string{"alpha.txt", "beta.md"}})
	if len(m.mention.matches) != 2 {
		t.Fatalf("matches = %v", m.mention.matches)
	}
	if view := m.View(); !strings.Contains(view, "alpha.txt") {
		t.Fatalf("popup rows missing from view: %q", view)
	}

	// Runes narrow the list; Enter completes into the draft.
	for _, r := range []rune("al") {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.mention.matches) != 1 || m.mention.matches[0] != "alpha.txt" {
		t.Fatalf("matches after query = %v", m.mention.matches)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mention.open {
		t.Fatal("popup stayed open after completion")
	}
	if string(m.input) != "@alpha.txt " {
		t.Fatalf("draft after completion = %q", string(m.input))
	}
}

func TestMentionPopupIgnoredWithoutMatches(t *testing.T) {
	root := t.TempDir()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, repo, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	m.Update(fileIndexMsg{files: []string{"alpha.txt"}})
	for _, r := range []rune("zzz") {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// No matches: Enter is not claimed by the popup and submits normally.
	if m.mentionActive() {
		t.Fatalf("popup claims input with no matches: %v", m.mention.matches)
	}
}
