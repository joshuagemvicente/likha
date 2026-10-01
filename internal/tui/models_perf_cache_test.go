package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
)

// perfCacheRow is one canned provider answer for the listModelsFunc seam.
type perfCacheRow struct {
	ids []string
	err error
}

// stubPerfCacheLists points the dialog seam at canned per-provider answers,
// counting one call per URL substring. It restores the previous seam on
// cleanup, so tests may re-stub mid-test to change answers.
func stubPerfCacheLists(t *testing.T, calls map[string]int, table map[string]perfCacheRow) {
	t.Helper()
	prev := listModelsFunc
	listModelsFunc = func(_ context.Context, base, _ string) ([]string, error) {
		for sub, row := range table {
			if strings.Contains(base, sub) {
				calls[sub]++
				return row.ids, row.err
			}
		}
		return nil, fmt.Errorf("unexpected base %q", base)
	}
	t.Cleanup(func() { listModelsFunc = prev })
}

// perfCacheUI builds a /models dialog host with a live client on the given
// provider, so the active-provider-first order is exercised.
func perfCacheUI(t *testing.T, stateDir, base, modelID, canonical, display string) *ui {
	t.Helper()
	client, err := model.New(base, modelID, "k-test")
	if err != nil {
		t.Fatal(err)
	}
	return modelsTestUI(t, client, modelID, providers.Connection{
		Provider:          display,
		ProviderCanonical: canonical,
		Verified:          true,
	}, stateDir)
}

// perfCacheOpenDeferred types /models and returns the open command without
// executing it, so cache-hit tests can assert the instant render before any
// background refresh runs.
func perfCacheOpenDeferred(t *testing.T, m *ui) tea.Cmd {
	t.Helper()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("models fetch did not start")
	}
	return cmd
}

// drainPerfCacheCmds executes an open command to completion: the per-section
// fan-out arrives as a tea.Batch whose sub-commands each yield one arrival
// message. A plain single message is accepted for forward-compat.
func drainPerfCacheCmds(t *testing.T, m *ui, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			if arrival := sub(); arrival != nil {
				m.Update(arrival)
			}
		}
		return
	}
	m.Update(msg)
}

// closePerfCacheDialog dismisses the open dialog via Esc.
func closePerfCacheDialog(t *testing.T, m *ui) {
	t.Helper()
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("dialog did not close")
	}
}

// perfCacheRowKeys flattens the dialog rows to provider/model pairs in order.
func perfCacheRowKeys(m *ui) string {
	var b strings.Builder
	for _, r := range m.dialogModelRows {
		b.WriteString(r.provider.Name + "/" + r.model + "\n")
	}
	return b.String()
}

// assertPerfCacheCursorOnLive fails unless the dialog cursor sits on the
// live pair.
func assertPerfCacheCursorOnLive(t *testing.T, m *ui, canonical, id string) {
	t.Helper()
	matches := m.dialogMatches()
	if len(matches) == 0 {
		t.Fatal("no selectable rows under the cursor")
	}
	if m.dialog.cursor < 0 || m.dialog.cursor >= len(matches) {
		t.Fatalf("cursor %d out of %d matches", m.dialog.cursor, len(matches))
	}
	row := m.dialogModelRows[matches[m.dialog.cursor]]
	if row.model != id || row.provider.Name != canonical {
		t.Fatalf("cursor on %s/%s, want live %s/%s", row.provider.Name, row.model, canonical, id)
	}
}

func clearPerfCacheCalls(calls map[string]int) {
	for k := range calls {
		delete(calls, k)
	}
}

// TestModelsPerfCacheHitServesInstantRows verifies a repeat open within the
// TTL renders the cached rows immediately — no loading placeholder, cursor
// on the live pair — with zero fetch calls, and that running the background
// refresh to completion leaves the rows correct.
func TestModelsPerfCacheHitServesInstantRows(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	calls := map[string]int{}
	stubPerfCacheLists(t, calls, map[string]perfCacheRow{
		"openrouter": {ids: []string{"router-a", "router-b"}},
		"openai.com": {ids: []string{"o-a"}},
	})
	m := perfCacheUI(t, stateDir, "https://openrouter.ai/api/v1", "router-a", "openrouter", "OpenRouter")

	drainPerfCacheCmds(t, m, perfCacheOpenDeferred(t, m))
	if len(m.dialogModelRows) != 3 {
		t.Fatalf("first-open rows = %+v", m.dialogModelRows)
	}
	want := perfCacheRowKeys(m)
	clearPerfCacheCalls(calls)
	closePerfCacheDialog(t, m)

	cmd := perfCacheOpenDeferred(t, m)
	if m.dialog.loading {
		t.Fatal("repeat open shows loading; expected the instant cached render")
	}
	if len(m.dialogModelRows) != 3 {
		t.Fatalf("cached rows = %+v", m.dialogModelRows)
	}
	if len(calls) != 0 {
		t.Fatalf("cached open fetched before refresh ran: %v", calls)
	}
	if got := stripANSI(m.View()); strings.Contains(got, "Fetching") {
		t.Fatalf("loading placeholder rendered over cached rows: %q", got)
	}
	assertPerfCacheCursorOnLive(t, m, "openrouter", "router-a")

	drainPerfCacheCmds(t, m, cmd)
	if got := perfCacheRowKeys(m); got != want {
		t.Fatalf("rows after background refresh =\n%s, want\n%s", got, want)
	}
	if m.dialogModelsNote != "" {
		t.Fatalf("refresh added a failure note over healthy rows: %q", m.dialogModelsNote)
	}
	assertPerfCacheCursorOnLive(t, m, "openrouter", "router-a")
}

// TestModelsPerfCacheExpiredTTLRefetches verifies an open past the TTL
// fetches again (loading state first) and picks up the new model ids.
func TestModelsPerfCacheExpiredTTLRefetches(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	calls := map[string]int{}
	stubPerfCacheLists(t, calls, map[string]perfCacheRow{
		"openai.com": {ids: []string{"o-a"}},
	})
	m := perfCacheUI(t, stateDir, "https://api.openai.com/v1", "o-a", "openai", "OpenAI")

	drainPerfCacheCmds(t, m, perfCacheOpenDeferred(t, m))
	if len(m.dialogModelRows) != 1 {
		t.Fatalf("first-open rows = %+v", m.dialogModelRows)
	}
	closePerfCacheDialog(t, m)

	m.modelsCache.fetchedAt = time.Now().Add(-modelsCacheTTL - time.Minute)
	next := map[string]int{}
	stubPerfCacheLists(t, next, map[string]perfCacheRow{
		"openai.com": {ids: []string{"o-a", "o-b"}},
	})
	cmd := perfCacheOpenDeferred(t, m)
	if !m.dialog.loading {
		t.Fatal("expired cache rendered instantly; expected a refetch with loading state")
	}
	drainPerfCacheCmds(t, m, cmd)
	if len(next) == 0 {
		t.Fatal("expired cache did not refetch")
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "o-b") {
		t.Fatalf("new model id missing after refetch: %q", got)
	}
}

// TestModelsPerfCacheRemovedCredentialDropsSection verifies a provider whose
// credential was removed contributes no ghost section once the refresh
// completes.
func TestModelsPerfCacheRemovedCredentialDropsSection(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	calls := map[string]int{}
	stubPerfCacheLists(t, calls, map[string]perfCacheRow{
		"openrouter": {ids: []string{"router-a"}},
		"openai.com": {ids: []string{"o-a"}},
	})
	m := perfCacheUI(t, stateDir, "https://api.openai.com/v1", "o-a", "openai", "OpenAI")

	drainPerfCacheCmds(t, m, perfCacheOpenDeferred(t, m))
	if len(m.dialogModelRows) != 2 {
		t.Fatalf("first-open rows = %+v", m.dialogModelRows)
	}
	closePerfCacheDialog(t, m)

	if err := providers.StoreKey(stateDir, "openrouter", ""); err != nil {
		t.Fatal(err)
	}
	drainPerfCacheCmds(t, m, perfCacheOpenDeferred(t, m))
	for _, r := range m.dialogModelRows {
		if r.provider.Name == "openrouter" {
			t.Fatalf("ghost row survived credential removal: %+v", m.dialogModelRows)
		}
	}
	if got := stripANSI(m.View()); strings.Contains(got, "OpenRouter") {
		t.Fatalf("ghost section survived credential removal: %q", got)
	}
	if len(m.dialogModelRows) != 1 {
		t.Fatalf("rows after removal = %+v", m.dialogModelRows)
	}
}

// TestModelsPerfCacheRefreshFailureKeepsRows verifies a refresh that fails
// everywhere keeps the cached rows visible with the muted Unreachable note
// instead of the error path.
func TestModelsPerfCacheRefreshFailureKeepsRows(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	calls := map[string]int{}
	stubPerfCacheLists(t, calls, map[string]perfCacheRow{
		"openrouter": {ids: []string{"router-a", "router-b"}},
		"openai.com": {ids: []string{"o-a"}},
	})
	m := perfCacheUI(t, stateDir, "https://openrouter.ai/api/v1", "router-a", "openrouter", "OpenRouter")

	drainPerfCacheCmds(t, m, perfCacheOpenDeferred(t, m))
	if len(m.dialogModelRows) != 3 {
		t.Fatalf("first-open rows = %+v", m.dialogModelRows)
	}
	closePerfCacheDialog(t, m)

	stubPerfCacheLists(t, calls, map[string]perfCacheRow{
		"openrouter": {err: fmt.Errorf("boom-router")},
		"openai.com": {err: fmt.Errorf("boom-openai")},
	})
	clearPerfCacheCalls(calls)
	cmd := perfCacheOpenDeferred(t, m)
	if m.dialog.loading || len(m.dialogModelRows) != 3 {
		t.Fatalf("failed refresh hid cached rows on open: loading=%t rows=%+v", m.dialog.loading, m.dialogModelRows)
	}
	drainPerfCacheCmds(t, m, cmd)
	if len(m.dialogModelRows) != 3 {
		t.Fatalf("failed refresh cleared cached rows: %+v", m.dialogModelRows)
	}
	if m.dialog.loadErr != "" {
		t.Fatalf("failed refresh took the error path over visible rows: %q", m.dialog.loadErr)
	}
	if !strings.Contains(m.dialogModelsNote, "Unreachable") {
		t.Fatalf("muted failure note missing: %q", m.dialogModelsNote)
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "router-a") || !strings.Contains(got, "Unreachable") {
		t.Fatalf("cached rows + note missing from view: %q", got)
	}
}

// TestModelsPerfCacheTotalFailureWithoutCacheShowsError verifies a cold open
// where every fetch fails still lands on the dialog error path.
func TestModelsPerfCacheTotalFailureWithoutCacheShowsError(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	calls := map[string]int{}
	stubPerfCacheLists(t, calls, map[string]perfCacheRow{
		"openai.com": {err: fmt.Errorf("boom")},
	})
	m := perfCacheUI(t, stateDir, "https://api.openai.com/v1", "o-a", "openai", "OpenAI")

	drainPerfCacheCmds(t, m, perfCacheOpenDeferred(t, m))
	if len(m.dialogModelRows) != 0 {
		t.Fatalf("failed cold open listed rows: %+v", m.dialogModelRows)
	}
	if m.dialog.loadErr == "" {
		t.Fatal("failed cold open did not surface the dialog error")
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "Error:") {
		t.Fatalf("dialog error missing from view: %q", got)
	}
}

// TestModelsPerfCacheAddedProviderAppears verifies a newly configured
// provider shows up once the refresh completes.
func TestModelsPerfCacheAddedProviderAppears(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	calls := map[string]int{}
	stubPerfCacheLists(t, calls, map[string]perfCacheRow{
		"openrouter": {ids: []string{"router-a"}},
		"openai.com": {ids: []string{"o-a"}},
	})
	m := perfCacheUI(t, stateDir, "https://api.openai.com/v1", "o-a", "openai", "OpenAI")

	drainPerfCacheCmds(t, m, perfCacheOpenDeferred(t, m))
	if len(m.dialogModelRows) != 1 {
		t.Fatalf("first-open rows = %+v", m.dialogModelRows)
	}
	closePerfCacheDialog(t, m)

	storedProviderKey(t, stateDir, "openrouter", "k-router")
	drainPerfCacheCmds(t, m, perfCacheOpenDeferred(t, m))
	found := false
	for _, r := range m.dialogModelRows {
		if r.provider.Name == "openrouter" && r.model == "router-a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("added provider missing from rows: %+v", m.dialogModelRows)
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "OpenRouter") || !strings.Contains(got, "router-a") {
		t.Fatalf("added provider missing from view: %q", got)
	}
}
