package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/session"
)

// TestModelsDialogSelection mirrors TestThemesModalSelection for the model
// dialog: the cursor starts on the live model, navigation changes selection
// only, Enter switches and stores, and Esc discards.
func TestModelsDialogSelection(t *testing.T) {
	stateDir := t.TempDir()
	if err := saveStoredConfig(stateDir, storedProviderConfig{Provider: "openai", Model: "beta"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"alpha"},{"id":"beta"},{"id":"gamma"}]}`)
			return
		}
		t.Errorf("unexpected path %q", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "beta", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, client, "beta", connection{provider: "OpenAI", verified: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// A bare /models opens the dialog with the cursor on the live model.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open || m.dialog.kind != dialogModels {
		t.Fatalf("dialog did not open: %+v", m.dialog)
	}
	if !strings.Contains(m.View(), "Model selection") {
		t.Fatalf("view missing dialog title: %q", m.View())
	}
	msg := cmd()
	list, ok := msg.(modelsListMsg)
	if !ok || list.err != nil || len(list.models) != 3 {
		t.Fatalf("models list = %+v", msg)
	}
	m.Update(msg)
	if len(m.dialogItems) != 3 || !strings.Contains(m.View(), "> beta (current)") {
		t.Fatalf("cursor not on the live model: items=%+v view=%q", m.dialogItems, m.View())
	}

	// Down moves the selection; nothing applies before Enter.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.dialog.cursor != 2 || !strings.Contains(m.View(), "> gamma") {
		t.Fatalf("cursor after down = %d", m.dialog.cursor)
	}
	if m.modelName != "beta" {
		t.Fatalf("model applied before Enter: %q", m.modelName)
	}

	// Enter switches the live model, stores it in config.json, and closes.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.open {
		t.Fatal("dialog did not close on Enter")
	}
	if m.modelName != "gamma" {
		t.Fatalf("applied model = %q, want gamma", m.modelName)
	}
	cfg, err := loadStoredConfig(stateDir)
	if err != nil || cfg.Model != "gamma" || cfg.Provider != "openai" {
		t.Fatalf("stored model: %+v %v", cfg, err)
	}

	// The cursor reopens on gamma (index 2); navigation changes it, Esc
	// discards without changing the applied model.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = cmd // the fetch runs; the reopened dialog keeps navigation responsive
	msg = cmd()
	list, ok = msg.(modelsListMsg)
	if !ok || list.err != nil {
		t.Fatalf("reopened dialog list = %+v", msg)
	}
	m.Update(msg)
	if m.dialog.cursor != 2 {
		t.Fatalf("cursor after reopen = %d, want 2 (gamma)", m.dialog.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.dialog.cursor != 1 || !strings.Contains(m.View(), "> beta") {
		t.Fatalf("cursor after up = %d", m.dialog.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("Esc did not close the dialog")
	}
	if m.modelName != "gamma" {
		t.Fatalf("Esc changed the applied model to %q", m.modelName)
	}
	cfg, err = loadStoredConfig(stateDir)
	if err != nil || cfg.Model != "gamma" {
		t.Fatalf("Esc persisted a model: %+v %v", cfg, err)
	}
}

func TestModelsDialogBlocksPromptInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"alpha"},{"id":"beta"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, client, "", connection{provider: "OpenAI", verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open {
		t.Fatal("dialog did not open")
	}
	// Typing filters the dialog rows; the draft stays untouched.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	if len(m.input) != 0 {
		t.Fatalf("prompt input leaked while dialog open: %q", string(m.input))
	}
	if m.dialog.query != "hello" || m.dialog.cursor != 0 {
		t.Fatalf("query not applied: q=%q cursor=%d", m.dialog.query, m.dialog.cursor)
	}
	// First Esc clears the query, the second closes; the draft stays empty.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open != true || m.dialog.query != "" {
		t.Fatalf("Esc did not clear the query: open=%t q=%q", m.dialog.open, m.dialog.query)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open || len(m.input) != 0 {
		t.Fatalf("dialog close state wrong: open=%t input=%q", m.dialog.open, string(m.input))
	}
}

// TestModelsDialogListError surfaces a failed fetch inside the dialog; Esc
// closes without changing the live model.
func TestModelsDialogListErrorStaysOpen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, client, "test", connection{provider: "OpenAI", verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	list, ok := msg.(modelsListMsg)
	if !ok || list.err == nil {
		t.Fatalf("expected a fetch error, got %+v", msg)
	}
	m.Update(msg)
	if !m.dialog.open || m.dialog.loadErr == "" {
		t.Fatalf("error not surfaced in dialog: %+v", m.dialog)
	}
	if !strings.Contains(m.View(), "Error:") {
		t.Fatalf("error not rendered: %q", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open || m.modelName != "test" {
		t.Fatalf("dialog close state wrong: open=%t model=%q", m.dialog.open, m.modelName)
	}
}

// TestSessionsDialogSelection mirrors the themes dialog flow for sessions:
// the cursor starts on the newest session, Enter resumes in place, Esc
// discards, and the numbered /sessions <n> form still works from the listing.
func TestSessionsDialogSelection(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	// Saving first after second's creation makes the listing order
	// deterministic (newest first): first is entry 1.
	first.Entries = []session.Entry{{Role: "You", Content: "earlier question"}}
	first.History = []model.Message{{Role: "user", Content: "earlier question"}}
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	m := newUI(root, nil, nil, "", connection{provider: "OpenAI", verified: true}, t.TempDir(), store, second)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// A bare /sessions opens the dialog listing both sessions.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/sessions")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open || m.dialog.kind != dialogSessions {
		t.Fatalf("dialog did not open: %+v", m.dialog)
	}
	if !strings.Contains(m.View(), "Session selection") {
		t.Fatalf("view missing dialog title: %q", m.View())
	}
	if len(m.sessionIDs) != 2 {
		t.Fatalf("sessions not listed: %+v", m.sessionIDs)
	}
	if !strings.Contains(m.View(), "> earlier question") {
		t.Fatalf("cursor not on the newest session: %q", m.View())
	}

	// Down selects the current (second) session; Esc discards without resume.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.dialog.cursor != 1 {
		t.Fatalf("cursor after down = %d", m.dialog.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open || m.status == "Resumed session" {
		t.Fatalf("Esc resumed unexpectedly: open=%t status=%q", m.dialog.open, m.status)
	}

	// Enter on the newest session resumes it in place.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/sessions")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.cursor != 0 {
		t.Fatalf("reopened cursor = %d, want 0", m.dialog.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.open || m.status != "Resumed session" || len(m.history) != 1 || m.history[0].Content != "earlier question" {
		t.Fatalf("dialog resume failed: status=%q history=%+v", m.status, m.history)
	}
	found := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "earlier question") {
			found = true
		}
	}
	if !found {
		t.Fatalf("resumed entries not shown: %+v", m.entries)
	}

	// The numbered form still resumes from the last listing.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/sessions 2")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.snapshot.ID != second.ID {
		t.Fatalf("numbered resume failed: id=%q", m.snapshot.ID)
	}
}

// TestSessionsDialogBlocksPromptInput verifies the sessions dialog captures
// keys the same way: typing filters, Esc clears then closes, and the prompt
// draft is untouched throughout.
func TestSessionsDialogBlocksPromptInput(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(); err != nil {
		t.Fatal(err)
	}
	m := newUI(root, nil, nil, "", connection{provider: "OpenAI", verified: true}, t.TempDir(), store, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/sessions")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open {
		t.Fatal("dialog did not open")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	if len(m.input) != 0 {
		t.Fatalf("prompt input leaked while dialog open: %q", string(m.input))
	}
	if m.dialog.query != "hello" || m.dialog.cursor != 0 {
		t.Fatalf("query not applied: q=%q cursor=%d", m.dialog.query, m.dialog.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dialog.open || m.dialog.query != "" {
		t.Fatalf("Esc did not clear the query: open=%t q=%q", m.dialog.open, m.dialog.query)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open || len(m.input) != 0 {
		t.Fatalf("dialog close state wrong: open=%t input=%q", m.dialog.open, string(m.input))
	}
}

// TestDialogDimsBackground verifies the dimmed-surroundings rendering: with a
// color profile that emits ANSI, rows outside the selection box carry the
// faint escape sequence, and the interior segment of every box row renders at
// full intensity.
func TestDialogDimsBackground(t *testing.T) {
	forceANSI(t)
	m := newUI("/sample", nil, nil, "", connection{provider: "OpenAI", verified: true, theme: "default"}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/themes")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open {
		t.Fatal("dialog did not open")
	}
	dimRows, boxRows := 0, 0
	for _, line := range strings.Split(m.View(), "\n") {
		start := strings.Index(line, "│ ")
		end := strings.Index(line, " │")
		if start >= 0 && end > start {
			// Interior of the box: never dimmed, and escape sequences must
			// render as SGR codes, not as escaped literal text.
			interior := line[start:end]
			if strings.Contains(interior, "\x1b[2m") {
				t.Fatalf("box interior is dimmed: %q", line)
			}
			boxRows++
			continue
		}
		if strings.Contains(line, "\x1b[2m") {
			dimRows++
		}
	}
	if boxRows < 5 {
		t.Fatalf("dialog box not rendered: %d box rows", boxRows)
	}
	if dimRows < 5 {
		t.Fatalf("expected dimmed rows around the box, got %d", dimRows)
	}
}

// TestDialogKeepsBaseContentBesideBox is the regression for the full-width
// solid band: overlaying the box must not wipe the rows it spans, so a
// conversation entry in the dialog's vertical band stays visible beside it.
func TestDialogKeepsBaseContentBesideBox(t *testing.T) {
	m := newUI("/sample", nil, nil, "", connection{provider: "OpenAI", verified: true, theme: "default"}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.entries = append(m.entries, entry{role: "You", content: "hello world"})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/themes")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open {
		t.Fatal("dialog did not open")
	}
	if !strings.Contains(m.View(), "You: hello world") {
		t.Fatalf("base content wiped behind the dialog: %q", m.View())
	}
}

// TestDialogRenderNoEscapedSequences is the regression for the visible
// \u001B[...m junk beside the ">" cursor: SGR escapes must never leak into the
// frame as escaped literal text.
func TestDialogRenderNoEscapedSequences(t *testing.T) {
	forceANSI(t)
	m := newUI("/sample", nil, nil, "", connection{provider: "OpenAI", verified: true, theme: "default"}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/themes")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	if strings.Contains(view, `\u001B`) || strings.Contains(view, `\u001b`) {
		t.Fatalf("escape sequences rendered as literal text: %q", view)
	}
	if !strings.Contains(view, "> default (current)") {
		t.Fatalf("cursor row not rendered cleanly: %q", view)
	}
}

// TestModelsDialogShowsProviderRight verifies the model rows carry the
// provider name right-aligned on the same row: model left, provider right,
// with the row still exactly the box's inner width.
func TestModelsDialogShowsProviderRight(t *testing.T) {
	forceANSI(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"alpha"},{"id":"beta"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, client, "alpha", connection{provider: "OpenCode Go", verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(cmd())
	if !strings.Contains(m.View(), "OpenCode Go") {
		t.Fatalf("provider name not shown: %q", m.View())
	}
	rows := 0
	for _, line := range strings.Split(m.View(), "\n") {
		plain := stripANSI(line)
		if !strings.Contains(plain, " │") {
			continue // not a dialog row (header, footer, borders)
		}
		for _, modelID := range []string{"alpha", "beta"} {
			if !strings.Contains(plain, modelID) {
				continue
			}
			rows++
			end := strings.LastIndex(plain, " │")
			if end < 0 || !strings.HasSuffix(strings.TrimRight(plain[:end], " "), "OpenCode Go") {
				t.Fatalf("provider not flush right on row %q", plain)
			}
			if strings.Contains(plain, "> "+modelID) && modelID == "beta" {
				t.Fatalf("non-cursor row carries the cursor marker: %q", plain)
			}
		}
	}
	if rows != 2 {
		t.Fatalf("model rows = %d, want 2", rows)
	}
}

// TestModelsDialogReopenDuringLoadNoPanic is the regression for the
// index-out-of-range panic: open, let the list arrive, Esc, reopen while the
// second fetch is in flight, and render. The loading view must not resolve
// matches against the stale previous list.
func TestModelsDialogReopenDuringLoadNoPanic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"alpha"},{"id":"beta"},{"id":"gamma"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, client, "alpha", connection{provider: "OpenCode Go", verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// First open: the list arrives and the dialog shows it.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, first := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(first())
	if len(m.dialogItems) != 3 {
		t.Fatalf("list not populated: %+v", m.dialogItems)
	}
	// Esc closes and drops the list.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open || m.dialogItems != nil {
		t.Fatalf("close state wrong: open=%t items=%+v", m.dialog.open, m.dialogItems)
	}

	// Reopen: still loading, so rendering must show the placeholder and never
	// index stale rows.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, second := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = second
	if !m.dialog.loading {
		t.Fatal("second open not loading")
	}
	view := m.View() // must not panic
	if !strings.Contains(view, "Fetching model list") {
		t.Fatalf("loading placeholder missing: %q", view)
	}
	if strings.Contains(view, "beta") || strings.Contains(view, "gamma") {
		t.Fatalf("stale rows rendered during loading: %q", view)
	}
	// The second fetch arrives; the dialog populates with the cursor on the
	// live model, exactly like the first open.
	m.Update(second())
	if m.dialog.loading || len(m.dialogItems) != 3 || m.dialog.cursor != 0 {
		t.Fatalf("second open failed to populate: loading=%t items=%+v cursor=%d", m.dialog.loading, m.dialogItems, m.dialog.cursor)
	}
	if !strings.Contains(m.View(), "> alpha (current)") {
		t.Fatalf("cursor not on the live model after reopen: %q", m.View())
	}
}

// TestModelsDialogLateResultRefreshesOpenDialog verifies a late result from an
// earlier open refreshes the currently open dialog instead of dumping text
// behind it, and resets the cursor when the list shrank.
func TestModelsDialogLateResultRefreshesOpenDialog(t *testing.T) {
	var responses []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":`+responses[len(responses)-1]+`}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "only", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, client, "only", connection{provider: "OpenCode Go", verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// First open: three models.
	responses = []string{`[{"id":"alpha"},{"id":"beta"},{"id":"gamma"}]`}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, first := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(first())
	if len(m.dialogItems) != 3 {
		t.Fatalf("first list missing: %+v", m.dialogItems)
	}
	// Esc, reopen: the second fetch reports only one model.
	responses = []string{`[{"id":"only"}]`}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, second := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// The stale first result arrives after the dialog reopened; it must not
	// panic on render, and the fresh (shrunken) list replaces it with the
	// cursor clamped back into range.
	m.Update(second())
	if m.dialog.loading || len(m.dialogItems) != 1 {
		t.Fatalf("shrunken list not applied: loading=%t items=%+v", m.dialog.loading, m.dialogItems)
	}
	if m.dialog.cursor != 0 {
		t.Fatalf("cursor out of range after shrink: %d", m.dialog.cursor)
	}
	if !strings.Contains(m.View(), "> only") {
		t.Fatalf("cursor row missing after refresh: %q", m.View())
	}
}

// TestModelsDialogEnterDuringLoadDoesNothing verifies the empty-selection
// guard: Enter while the list is loading leaves the dialog open.
func TestModelsDialogEnterDuringLoadDoesNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"alpha"},{"id":"beta"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, client, "test", connection{provider: "OpenAI", verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.loading {
		t.Fatal("dialog not loading")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open {
		t.Fatal("Enter dismissed the loading dialog")
	}
}
