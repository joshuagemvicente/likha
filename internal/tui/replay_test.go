package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

func TestRejectedReplayTurnErrorCarriesHint(t *testing.T) {
	const providerError = "model HTTP 400 Bad Request: missing reasoning_content"
	replay := &model.ReplayRejectedError{Missing: 1, Err: errString(providerError)}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"replay alone", replay, providerError + "\n" + replayRejectedHint},
		{
			"replay and effort",
			&model.EffortRejectedError{Level: "max", Err: replay},
			providerError + "\nSent with effort max; /effort default sends none.\n" + replayRejectedHint,
		},
		{"neither", errString(providerError), providerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "DeepSeek", Verified: true}, t.TempDir(), nil, session.Snapshot{})
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			m.working, m.runID = true, 7
			m.events = make(chan agent.TurnEvent, 1)
			m.cancel = func() {}
			m.Update(agent.TurnEvent{Kind: "error", RunID: 7, Text: tc.err.Error(), Err: tc.err})
			var errorsShown []string
			for _, e := range m.entries {
				if e.role == "Error" {
					errorsShown = append(errorsShown, e.content)
				}
			}
			// Exact match: the hint shows once, after the effort hint.
			if len(errorsShown) != 1 || errorsShown[0] != tc.want {
				t.Fatalf("error entries = %q, want one %q", errorsShown, tc.want)
			}
		})
	}
}

// A 400 from Mistral on a request that replayed a reply's thinking chunk
// shows the /compact hint once (specs/chunked-content T10). The error comes
// from a real client on the shipped mistral row against a test server, not
// a hand-built value, so the row, the encoder's Sent count and the hint are
// checked together.
func TestMistralThinkingRejectionShowsCompactHint(t *testing.T) {
	const providerError = "invalid thinking chunk"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			http.Error(w, `{"error":{"message":"`+providerError+`"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"trace"}]},{"type":"text","text":"done"}]}}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	mistral := -1
	for i, p := range model.Providers {
		if p.Name == "mistral" {
			mistral = i
		}
	}
	if mistral < 0 {
		t.Fatal("no mistral provider row")
	}
	previous := model.Providers[mistral].BaseURL
	model.Providers[mistral].BaseURL = server.URL + "/v1"
	t.Cleanup(func() { model.Providers[mistral].BaseURL = previous })
	client, err := model.New(server.URL+"/v1", "magistral-medium-latest", "")
	if err != nil {
		t.Fatal(err)
	}
	history := []model.Message{{Role: "user", Content: "go"}}
	reply, err := client.Stream(context.Background(), history, nil, nil, nil)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	history = append(history, reply, model.Message{Role: "user", Content: "more"})
	_, err = client.Stream(context.Background(), history, nil, nil, nil)
	var rejected *model.ReplayRejectedError
	if !errors.As(err, &rejected) || rejected.Sent != 1 {
		t.Fatalf("second request error = %v (%+v), want a ReplayRejectedError with the thinking sent", err, rejected)
	}

	m := NewUI("/sample", nil, client, "", providers.Connection{Provider: "Mistral AI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.working, m.runID = true, 7
	m.events = make(chan agent.TurnEvent, 1)
	m.cancel = func() {}
	m.Update(agent.TurnEvent{Kind: "error", RunID: 7, Text: err.Error(), Err: err})
	var errorsShown []string
	for _, e := range m.entries {
		if e.role == "Error" {
			errorsShown = append(errorsShown, e.content)
		}
	}
	if want := err.Error() + "\n" + replayRejectedHint; len(errorsShown) != 1 || errorsShown[0] != want {
		t.Fatalf("error entries = %q, want one %q", errorsShown, want)
	}
	if !strings.Contains(errorsShown[0], providerError) {
		t.Fatalf("error entry %q does not carry Mistral's message", errorsShown[0])
	}
}

// At idle the next request starts with a new prompt, which starts a new
// Gemini turn, so a foreign tool call at the end of history gets no skip
// value and the estimate must not count one (spec rule 14).
func TestIdleContextEstimateCountsNoSkipValue(t *testing.T) {
	client, err := model.New("https://generativelanguage.googleapis.com/v1beta/openai", "gemini-3-pro-preview", "")
	if err != nil {
		t.Fatal(err)
	}
	// The last turn ran a tool call on another provider, then /models
	// switched to Gemini.
	history := []model.Message{
		{Role: "user", Content: "read the file"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"a.go"}`}}},
		{Role: "tool", ToolCallID: "c1", Content: "package a"},
		{Role: "assistant", Content: "done"},
	}
	want, ok := model.EstimateInputTokens(client.Model(), history, nil)
	if !ok {
		t.Fatal("base estimate unavailable")
	}
	m := NewUI("/sample", nil, client, "", providers.Connection{Provider: "Google Gemini", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.recalculateContext(history)
	if !m.contextSeen || m.contextTokens != want {
		t.Fatalf("idle estimate = %d (seen %v), want %d with no skip value", m.contextTokens, m.contextSeen, want)
	}
}
