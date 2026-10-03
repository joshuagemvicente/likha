package tui

// specs/models-perf slice A7: progressive-render dialog tests.
//
// Every test drives the real open path (/models + Enter) through the
// listModelsFunc seam and executes the returned tea.Batch sub-commands
// selectively to simulate staggered provider arrivals. Each test asserts
// user-visible dialog state: headers, rows, cursor, applied model,
// open/closed.

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
)

// openPerfModelsBatch opens /models and unwraps the fan-out command into its
// per-provider sub-commands in final display order (active first). The
// caller executes sub-commands selectively to simulate arrival order;
// unexecuted sub-commands model stragglers still in flight.
func openPerfModelsBatch(t *testing.T, m *ui) tea.BatchMsg {
	t.Helper()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("models fetch did not start")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("fetch cmd = %T, want tea.BatchMsg", cmd())
	}
	if len(batch) != 2 {
		t.Fatalf("fetch batch has %d cmds, want 2 (fast + straggler)", len(batch))
	}
	return batch
}

// feedPerfArrival executes one batch sub-command and feeds its message back
// into the update loop, returning the message for identity assertions.
func feedPerfArrival(t *testing.T, m *ui, batch tea.BatchMsg, i int) tea.Msg {
	t.Helper()
	msg := batch[i]()
	m.Update(msg)
	return msg
}

// perfGatedSeam serves the active (openrouter) provider instantly and gates
// every other provider behind release, respecting the fetch ctx bound so
// a mis-wired test fails instead of hanging forever.
func perfGatedSeam(release <-chan struct{}) func(context.Context, string, string) ([]string, error) {
	return func(ctx context.Context, base, apiKey string) ([]string, error) {
		if strings.Contains(base, "openrouter") {
			return []string{"router-a", "router-b"}, nil
		}
		select {
		case <-release:
			return []string{"o-a"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func perfTwoProviderUI(t *testing.T, stateDir, liveModel string) *ui {
	t.Helper()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	client, err := model.New("https://openrouter.ai/api/v1", liveModel, "k-router")
	if err != nil {
		t.Fatal(err)
	}
	return modelsTestUI(t, client, liveModel, providers.Connection{Provider: "OpenRouter", ProviderCanonical: "openrouter", Verified: true}, stateDir)
}

// TestModelsPerfStaggeredArrivalFinalOrder verifies the fast provider's
// section renders before the straggler resolves, and the final list keeps
// active-first order once the straggler lands.
func TestModelsPerfStaggeredArrivalFinalOrder(t *testing.T) {
	stateDir := t.TempDir()
	release := make(chan struct{})
	prev := listModelsFunc
	listModelsFunc = perfGatedSeam(release)
	t.Cleanup(func() { listModelsFunc = prev })

	m := perfTwoProviderUI(t, stateDir, "router-a")
	batch := openPerfModelsBatch(t, m)

	msg := feedPerfArrival(t, m, batch, 0)
	sec, ok := msg.(modelsSectionMsg)
	if !ok {
		t.Fatalf("first arrival = %T, want modelsSectionMsg", msg)
	}
	if sec.section.provider.Name != "openrouter" {
		t.Fatalf("first arrival = %q, want the fast openrouter section", sec.section.provider.Name)
	}
	if len(m.dialogModelRows) != 2 {
		t.Fatalf("partial rows = %+v, want the 2 fast rows", m.dialogModelRows)
	}
	plain := stripANSI(m.View())
	if !strings.Contains(plain, "OpenRouter") || !strings.Contains(plain, "router-a") {
		t.Fatalf("fast section not visible mid-flight: %q", plain)
	}
	if strings.Contains(plain, "OpenAI") {
		t.Fatalf("straggler header rendered before arrival: %q", plain)
	}

	close(release)
	msg = feedPerfArrival(t, m, batch, 1)
	sec, ok = msg.(modelsSectionMsg)
	if !ok {
		t.Fatalf("second arrival = %T, want modelsSectionMsg", msg)
	}
	if sec.section.provider.Name != "openai" {
		t.Fatalf("second arrival = %q, want the openai section", sec.section.provider.Name)
	}
	if len(m.dialogModelRows) != 3 {
		t.Fatalf("final rows = %+v, want 3", m.dialogModelRows)
	}
	got := []string{
		m.dialogModelRows[0].provider.Name + ":" + m.dialogModelRows[0].model,
		m.dialogModelRows[1].provider.Name + ":" + m.dialogModelRows[1].model,
		m.dialogModelRows[2].provider.Name + ":" + m.dialogModelRows[2].model,
	}
	want := []string{"openrouter:router-a", "openrouter:router-b", "openai:o-a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("final order = %v, want %v", got, want)
		}
	}
	plain = stripANSI(m.View())
	if strings.Index(plain, "OpenRouter") > strings.Index(plain, "OpenAI") {
		t.Fatalf("headers out of order: %q", plain)
	}
	if m.dialog.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (live router-a)", m.dialog.cursor)
	}
}

// TestModelsPerfMidFlightEnterApplies verifies Enter with one section
// present applies the visible row (same-provider switch, dialog closes,
// model stored), while Enter with zero sections is a no-op.
func TestModelsPerfMidFlightEnterApplies(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openrouter", Model: "router-a"}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	prev := listModelsFunc
	listModelsFunc = perfGatedSeam(release)
	t.Cleanup(func() { listModelsFunc = prev })

	m := perfTwoProviderUI(t, stateDir, "router-a")
	batch := openPerfModelsBatch(t, m)
	feedPerfArrival(t, m, batch, 0)

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.open {
		t.Fatal("dialog did not close on mid-flight Enter")
	}
	if m.modelName != "router-b" {
		t.Fatalf("applied model = %q, want router-b", m.modelName)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || cfg.Model != "router-b" || cfg.Provider != "openrouter" {
		t.Fatalf("stored model: %+v %v", cfg, err)
	}

	// Zero sections: Enter must not dismiss the loading dialog.
	m2 := perfTwoProviderUI(t, stateDir, "router-a")
	openPerfModelsBatch(t, m2)
	if len(m2.dialogModelRows) != 0 {
		t.Fatalf("cold rows = %+v, want none before any arrival", m2.dialogModelRows)
	}
	m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m2.dialog.open {
		t.Fatal("zero-section Enter closed the dialog")
	}
}

// TestModelsPerfCursorNoYank verifies arrivals jump to the live pair only
// before the user's first navigation; after navigating, a later arrival
// leaves the cursor where the user put it.
func TestModelsPerfCursorNoYank(t *testing.T) {
	stateDir := t.TempDir()
	release := make(chan struct{})
	prev := listModelsFunc
	listModelsFunc = perfGatedSeam(release)
	t.Cleanup(func() { listModelsFunc = prev })

	m := perfTwoProviderUI(t, stateDir, "router-b")
	batch := openPerfModelsBatch(t, m)

	// Pre-navigation arrival jumps to the live pair (router-b at index 1).
	feedPerfArrival(t, m, batch, 0)
	if m.dialog.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (live router-b before navigation)", m.dialog.cursor)
	}

	// After the user navigates, the next arrival must not yank it back.
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.dialog.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 after up", m.dialog.cursor)
	}
	close(release)
	feedPerfArrival(t, m, batch, 1)
	if len(m.dialogModelRows) != 3 {
		t.Fatalf("rows = %+v, want 3 after straggler", m.dialogModelRows)
	}
	if m.dialog.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (arrival yanked it after navigation)", m.dialog.cursor)
	}
}

// TestModelsPerfEscDiscardsLateArrival verifies Esc after a partial
// arrival discards the dialog, and a late arrival fed afterwards is
// ignored without reopening, panicking, or repopulating.
func TestModelsPerfEscDiscardsLateArrival(t *testing.T) {
	stateDir := t.TempDir()
	release := make(chan struct{})
	prev := listModelsFunc
	listModelsFunc = perfGatedSeam(release)
	t.Cleanup(func() { listModelsFunc = prev })

	m := perfTwoProviderUI(t, stateDir, "router-a")
	batch := openPerfModelsBatch(t, m)
	feedPerfArrival(t, m, batch, 0)
	if len(m.dialogModelRows) != 2 {
		t.Fatalf("partial rows = %+v, want 2", m.dialogModelRows)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("dialog still open after Esc")
	}
	if len(m.dialogModelRows) != 0 {
		t.Fatalf("rows = %+v, want discarded after Esc", m.dialogModelRows)
	}

	close(release)
	late := batch[1]()
	m.Update(late) // must not panic
	if m.dialog.open {
		t.Fatal("late arrival reopened the dialog")
	}
	if len(m.dialogModelRows) != 0 {
		t.Fatalf("late arrival repopulated rows: %+v", m.dialogModelRows)
	}
	if strings.Contains(stripANSI(m.View()), "Model selection") {
		t.Fatalf("dialog rendered after Esc + late arrival: %q", stripANSI(m.View()))
	}
}

// TestModelsPerfFailedProviderNote verifies a mid-flight failure names
// the provider in the muted note while the surviving section stays
// listed and interactive.
func TestModelsPerfFailedProviderNote(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		if strings.Contains(base, "openrouter") {
			return []string{"router-a", "router-b"}, nil
		}
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { listModelsFunc = prev })

	client, err := model.New("https://openrouter.ai/api/v1", "router-a", "k-router")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "router-a", providers.Connection{Provider: "OpenRouter", ProviderCanonical: "openrouter", Verified: true}, stateDir)
	batch := openPerfModelsBatch(t, m)

	// Surviving section arrives first: dialog is interactive mid-flight.
	msg := feedPerfArrival(t, m, batch, 0)
	if _, ok := msg.(modelsSectionMsg); !ok {
		t.Fatalf("first arrival = %T, want modelsSectionMsg", msg)
	}
	if m.dialog.loading || len(m.dialogMatches()) != 2 {
		t.Fatalf("dialog not interactive mid-flight: loading=%v matches=%d", m.dialog.loading, len(m.dialogMatches()))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.dialog.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 after down+up", m.dialog.cursor)
	}

	// The failure lands: rows survive, note names the provider, no error path.
	msg = feedPerfArrival(t, m, batch, 1)
	if _, ok := msg.(modelsProviderFailedMsg); !ok {
		t.Fatalf("second arrival = %T, want modelsProviderFailedMsg", msg)
	}
	if len(m.dialogModelRows) != 2 {
		t.Fatalf("rows = %+v, want surviving section kept", m.dialogModelRows)
	}
	if m.dialog.loadErr != "" {
		t.Fatalf("loadErr = %q, want the error path only with zero rows", m.dialog.loadErr)
	}
	plain := stripANSI(m.View())
	if !strings.Contains(plain, "Unreachable: OpenAI") {
		t.Fatalf("muted note missing the failed provider: %q", plain)
	}
	if !strings.Contains(plain, "router-a") || !strings.Contains(plain, "OpenRouter") {
		t.Fatalf("surviving section lost after failure: %q", plain)
	}
}
