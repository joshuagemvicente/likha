package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
)

const claudeRejection = "messages.1.content.0: Invalid `signature` in `thinking` block"

// claudeUIServer plays Anthropic's Messages API for a client built from the
// claude row's real base URL: the default transport is rerouted to a local
// server for the test, so the client runs its real wire selection, request
// building and error classification. Each Messages request is handed to
// respond with its 1-based number.
type claudeUIServer struct {
	mu     sync.Mutex
	bodies []string
}

func (s *claudeUIServer) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

func newClaudeUIServer(t *testing.T, respond func(n int, w http.ResponseWriter)) *claudeUIServer {
	t.Helper()
	state := &claudeUIServer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5"}],"has_more":false}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		state.mu.Lock()
		state.bodies = append(state.bodies, string(raw))
		n := len(state.bodies)
		state.mu.Unlock()
		respond(n, w)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = oauthUITransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.anthropic.com" {
			return nil, errors.New("unexpected test network origin")
		}
		routed := req.Clone(req.Context())
		routed.URL.Scheme, routed.URL.Host, routed.Host = target.Scheme, target.Host, ""
		return previous.RoundTrip(routed)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return state
}

// claudeUIEvents writes Messages API events as server-sent events.
func claudeUIEvents(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &head)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, event)
	}
}

// claudeUIReadTurn is a signed thinking block, then a read call of path.
func claudeUIReadTurn(w http.ResponseWriter, path string) {
	partial, _ := json.Marshal(`{"path":"` + path + `"}`)
	claudeUIEvents(w,
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Read it."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-ui"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":`+string(partial)+`}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
		`{"type":"message_stop"}`,
	)
}

// claudeUIReject answers with Anthropic's JSON error shape and a request ID.
func claudeUIReject(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("request-id", "req_ui_400")
	w.WriteHeader(http.StatusBadRequest)
	body, _ := json.Marshal(map[string]any{
		"type":       "error",
		"error":      map[string]string{"type": "invalid_request_error", "message": claudeRejection},
		"request_id": "req_ui_400",
	})
	w.Write(body)
}

// A Messages-API 400 reaches the transcript through the real client and
// agent: a request that carried replayed thinking gets the /compact hint, with
// effort sent the effort hint before it, and a 400 on a request without
// replayed thinking gets no replay hint.
func TestClaudeReplayRejectionShowsHints(t *testing.T) {
	providerError := "model HTTP 400 Bad Request: invalid_request_error: " + claudeRejection + " (request req_ui_400)"
	effortHint := "Sent with effort high; use /variants → Default to remove the preset."
	cases := []struct {
		name     string
		effort   string // set on the client as the effort probe would allow
		rejectAt int    // 1-based Messages request that gets the 400
		want     string
	}{
		{"replayed thinking", "", 2, providerError + "\n" + replayRejectedHint},
		{"replayed thinking with effort", "high", 2, providerError + "\n" + effortHint + "\n" + replayRejectedHint},
		{"no replayed thinking", "", 1, providerError},
		{"no replayed thinking with effort", "high", 1, providerError + "\n" + effortHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "known.txt"), []byte("the answer is 42"), 0600); err != nil {
				t.Fatal(err)
			}
			repo, err := repository.New(root)
			if err != nil {
				t.Fatal(err)
			}
			server := newClaudeUIServer(t, func(n int, w http.ResponseWriter) {
				if n == tc.rejectAt {
					claudeUIReject(w)
					return
				}
				claudeUIReadTurn(w, "known.txt")
			})
			client, err := model.New("https://api.anthropic.com/v1", "claude-opus-5-5", "test-key")
			if err != nil {
				t.Fatal(err)
			}
			// One stored entry keeps the session non-fresh, so auto-naming
			// sends no request of its own.
			m := NewUI(root, repo, client, "claude-opus-5-5", providers.Connection{Provider: "Anthropic Claude", ProviderCanonical: "claude", Verified: true}, t.TempDir(), nil, session.Snapshot{ID: "claude-replay", Entries: []session.Entry{{Role: "You", Content: "seed"}}})
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			t.Cleanup(func() {
				if m.cancel != nil {
					m.cancel()
				}
			})
			if tc.effort != "" {
				// The claude row stays EffortUnsupported until the user's
				// FR-40 probe, so the level is set the way that flip would.
				client.SetEffort(model.EffortAnthropic, tc.effort)
			}

			steerE2Esubmit(m, "Find the answer")
			if !m.working {
				t.Fatal("submission did not start a run")
			}
			steerE2Edrive(t, m)

			bodies := server.all()
			if len(bodies) != tc.rejectAt {
				t.Fatalf("Messages requests = %d, want %d", len(bodies), tc.rejectAt)
			}
			rejected := bodies[tc.rejectAt-1]
			if carried := strings.Contains(rejected, `"signature":"sig-ui"`); carried != (tc.rejectAt > 1) {
				t.Fatalf("rejected body carries replayed thinking = %v:\n%s", carried, rejected)
			}
			if sent := strings.Contains(rejected, `"output_config":{"effort":"high"}`); sent != (tc.effort != "") {
				t.Fatalf("rejected body carries effort = %v:\n%s", sent, rejected)
			}
			var errorsShown []string
			for _, e := range m.entries {
				if e.role == "Error" {
					errorsShown = append(errorsShown, e.content)
				}
			}
			// Exact match: each hint shows once, effort before replay.
			if len(errorsShown) != 1 || errorsShown[0] != tc.want {
				t.Fatalf("error entries = %q, want one %q", errorsShown, tc.want)
			}
		})
	}
}
