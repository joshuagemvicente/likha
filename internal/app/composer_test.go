package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"lisa/internal/providers"
	"lisa/internal/session"
)

func composerTestUI(t *testing.T, style string, width, height int) *ui {
	t.Helper()
	m := newUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local", Verified: true, ComposerStyle: style}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func TestComposerStylesAndNarrowDegradation(t *testing.T) {
	wide := map[string]string{}
	for _, style := range composerStyles {
		m := composerTestUI(t, style, 100, 24)
		view := m.View()
		assertViewport(t, view, 100, 24)
		if !strings.Contains(stripANSI(view), "Ask Likha… // escapes a slash") {
			t.Fatalf("%s placeholder missing: %q", style, view)
		}
		wide[style] = stripANSI(strings.Join(m.composerLines(), "\n"))
		switch style {
		case "minimal":
			if !strings.Contains(wide[style], "\n─") {
				t.Fatalf("minimal rule missing: %q", wide[style])
			}
		case "bordered":
			if !strings.Contains(wide[style], "╭") || !strings.Contains(wide[style], "╰") || (!strings.Contains(wide[style], "│ Ask Likha") && !strings.Contains(wide[style], "│ █Ask Likha")) {
				t.Fatalf("bordered outline missing: %q", wide[style])
			}
		case "borderless":
			if strings.ContainsAny(wide[style], "─╭│") || !strings.HasPrefix(wide[style], strings.Repeat(" ", 100)) {
				t.Fatalf("borderless separator missing: %q", wide[style])
			}
		case "chatter":
			if !strings.Contains(wide[style], "You › Ask Likha") && !strings.Contains(wide[style], "You › █Ask Likha") {
				t.Fatalf("chatter prefix missing: %q", wide[style])
			}
		}
		for _, other := range composerStyles {
			if other != style && wide[other] != "" && wide[other] == wide[style] {
				t.Fatalf("%s and %s rendered identically", style, other)
			}
		}
		m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
		assertViewport(t, m.View(), 40, 12)
		if style == "bordered" || style == "chatter" {
			minimal := composerTestUI(t, "minimal", 40, 12)
			if got, want := strings.Join(m.composerLines(), "\n"), strings.Join(minimal.composerLines(), "\n"); got != want || m.composerStyle != style {
				t.Fatalf("%s at 40 cols should display minimal without changing selection", style)
			}
		}
	}
}

func TestComposerDraftStaysFixedWhileTranscriptPages(t *testing.T) {
	m := composerTestUI(t, "bordered", 100, 24)
	for range 70 {
		m.entries = append(m.entries, entry{role: "Assistant", content: "older conversation"})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("editable draft")})
	if m.pageCount() < 2 || strings.Contains(strings.Join(m.lines, "\n"), "editable draft") {
		t.Fatal("draft is not independent from transcript pagination")
	}
	for page := range m.pageCount() {
		view := m.View()
		assertViewport(t, view, 100, 24)
		if !strings.Contains(view, "editable draft") || !strings.Contains(view, "Page ") {
			t.Fatalf("draft or page indicator missing at page %d: %q", page, view)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("wrap", 180))})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	assertViewport(t, m.View(), 40, 12)
	if m.bodyHeight() < 1 || !strings.Contains(m.View(), "wrap") || !strings.Contains(m.View(), "Page ") {
		t.Fatalf("long draft obscured transcript/footer: %q", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if !strings.HasSuffix(string(m.input), "wra") {
		t.Fatal("long draft ceased being editable")
	}
}

func TestComposerReviewFooterAndPageGate(t *testing.T) {
	for _, size := range [][2]int{{100, 24}, {40, 12}} {
		m := composerTestUI(t, "bordered", size[0], size[1])
		m.working, m.runID = true, 1
		m.events = make(chan turnEvent, 1)
		request := &approvalRequest{Kind: "command", Title: "Shell command", Body: strings.Repeat("command argument\n", 45), Reply: make(chan bool, 1)}
		m.Update(turnEvent{runID: 1, kind: "approval", approval: request})
		if m.pageCount() < 2 {
			t.Fatal("expected multiple review pages")
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
		if m.pending == nil || len(request.Reply) != 0 || !strings.Contains(m.View(), "Read all pages") {
			t.Fatalf("unseen review page was approved at %dx%d", size[0], size[1])
		}
		for page := range m.pageCount() {
			view := m.View()
			assertViewport(t, view, size[0], size[1])
			footer := strings.Split(view, "\n")[size[1]-1]
			for _, label := range []string{"Page ", "Y", "N", "PgUp/PgDn"} {
				if !strings.Contains(footer, label) {
					t.Fatalf("review footer lost %s at page %d: %q", label, page, footer)
				}
			}
			m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
		if m.pending != nil || len(request.Reply) != 1 || !<-request.Reply {
			t.Fatalf("fully reviewed command was not approvable at %dx%d", size[0], size[1])
		}
	}
}

func TestComposerChooserFiltersCancelsAndPersists(t *testing.T) {
	stateDir := t.TempDir()
	original := providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini", Theme: "habamax"}
	if err := providers.SaveStoredConfig(stateDir, original); err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, nil, original.Model, providers.Connection{Provider: "OpenAI", Verified: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if !m.dialog.open || m.dialog.kind != dialogComposer || m.dialog.cursor != 0 || !strings.Contains(m.View(), "Composer selection") {
		t.Fatalf("chooser did not start at active minimal style: %q", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("chatt")})
	if len(m.dialogMatches()) != 1 || m.dialog.query != "chatt" || m.composerStyle != "minimal" {
		t.Fatal("filter changed the active style or missed chatter")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // clear filter, not selection
	if !m.dialog.open || m.dialog.query != "" {
		t.Fatal("first Escape did not clear filter")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // cancel
	if m.dialog.open || m.composerStyle != "minimal" {
		t.Fatal("second Escape applied a selection")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("chatt")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || m.dialog.open || m.composerStyle != "chatter" || cfg.Composer == nil || cfg.Composer.Style != "chatter" || cfg.Provider != original.Provider || cfg.Model != original.Model || cfg.Theme != original.Theme {
		t.Fatalf("composer preference failed to preserve configuration: %+v, %v", cfg, err)
	}
	reopened := newUI("/sample", nil, nil, cfg.Model, providers.Connection{Provider: "OpenAI", ComposerStyle: cfg.Composer.Style}, stateDir, nil, session.Snapshot{})
	reopened.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	reopened.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if reopened.dialog.cursor != 3 || !strings.Contains(reopened.View(), "> chatter (current)") {
		t.Fatal("persisted selection did not reopen on chatter")
	}
	reopened.Update(tea.KeyMsg{Type: tea.KeyEsc})
	reopened.working = true
	reopened.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if reopened.dialog.open {
		t.Fatal("composer chooser opened while a turn was running")
	}
	reopened.working = false
	reopened.pending = &approvalRequest{Kind: "edit"}
	reopened.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if reopened.dialog.open {
		t.Fatal("composer chooser opened during review")
	}
}

func TestComposerPersistenceErrorKeepsSelection(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "config.json"), []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, nil, "local", providers.Connection{ComposerStyle: "minimal"}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.composerStyle != "minimal" || m.status != "Error" || !strings.Contains(m.View(), "Store composer style:") {
		t.Fatalf("failed config read changed active style or hid error: %q", m.View())
	}
}

func TestFinishSetupPreservesComposerPreference(t *testing.T) {
	stateDir := t.TempDir()
	cfg := providers.StoredProviderConfig{Composer: &providers.StoredComposerConfig{Style: "borderless"}}
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, nil, "", providers.Connection{Setup: true, ComposerStyle: "borderless"}, stateDir, nil, session.Snapshot{})
	m.setup.cursor = 0
	m.finishSetup("gpt-4o-mini")
	if m.setup.err != "" {
		t.Fatal(m.setup.err)
	}
	stored, err := providers.LoadStoredConfig(stateDir)
	if err != nil || stored.Composer == nil || stored.Composer.Style != "borderless" || stored.Provider == "" || stored.Model != "gpt-4o-mini" {
		t.Fatalf("setup lost composer setting: %+v %v", stored, err)
	}
}

func TestComposerNarrowFooterShowsCancellation(t *testing.T) {
	m := composerTestUI(t, "minimal", 40, 12)
	m.working = true
	m.status = "Cancelling"
	hints := strings.Split(m.View(), "\n")[11]
	if !strings.Contains(hints, "Cancelling") || !strings.Contains(hints, "^C") {
		t.Fatalf("cancellation state or key missing at 40 columns: %q", hints)
	}
}
