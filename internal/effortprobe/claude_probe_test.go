package effortprobe

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"likha/internal/model"
)

// claudeModel publishes low, medium, high, xhigh and max on claude in the
// bundled catalog, so a run sends 1 + 5 + 1 = 7 requests.
const claudeModel = "claude-opus-5-5"

var claudeLevels = []string{"low", "medium", "high", "xhigh", "max"}

// claudeOutputTokens scales output with the level, as a model that honors
// output_config.effort would. The Messages API reports no separate
// reasoning count, so output is the probe's signal.
var claudeOutputTokens = map[string]int64{"": 1500, "low": 300, "medium": 900, "high": 2400, "xhigh": 4800, "max": 7000}

// fakeMessagesAPI plays Anthropic's Messages API: it answers POST
// /v1/messages with a thinking block and a record_answer tool call, and
// rejects any effort level it does not know with HTTP 400. It records the
// output_config.effort of every POST and each body without it.
type fakeMessagesAPI struct {
	t      *testing.T
	server *httptest.Server

	mu    sync.Mutex
	posts []string // "output_config.effort=<level>" or "none"
	rests []string // each body with output_config removed
}

func newFakeMessagesAPI(t *testing.T) *fakeMessagesAPI {
	t.Helper()
	f := &fakeMessagesAPI{t: t}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeMessagesAPI) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.posts)
}

func (f *fakeMessagesAPI) bodiesWithoutEffort() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.rests)
}

func (f *fakeMessagesAPI) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("x-api-key") != testKey || r.Header.Get("Authorization") != "" {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read body: %v", err)
		return
	}
	effort, label, rest := f.readOutputConfig(raw)
	f.mu.Lock()
	f.posts = append(f.posts, label)
	f.rests = append(f.rests, rest)
	f.mu.Unlock()
	tokens, known := claudeOutputTokens[effort]
	if !known {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"type":"error","error":{"type":"invalid_request_error","message":"output_config.effort: Input should be 'low', 'medium', 'high', 'xhigh' or 'max', got %q"}}`, effort)
		return
	}
	writeMessagesStream(w, tokens+40)
}

// readOutputConfig returns the effort a body carries, its label, and the body
// re-encoded without output_config. A chat-completions effort field on this
// wire is an error: the native client must never send one.
func (f *fakeMessagesAPI) readOutputConfig(raw []byte) (effort, label, rest string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		f.t.Errorf("request body is not JSON: %v", err)
		return "", "unreadable", ""
	}
	for _, name := range []string{"reasoning_effort", "reasoning"} {
		if _, ok := fields[name]; ok {
			f.t.Errorf("Messages API body carries chat-completions field %q", name)
		}
	}
	label = "none"
	if value, ok := fields["output_config"]; ok {
		var config struct {
			Effort string `json:"effort"`
		}
		if err := json.Unmarshal(value, &config); err != nil {
			f.t.Errorf("output_config is not an object: %s", value)
		}
		effort, label = config.Effort, "output_config.effort="+config.Effort
	}
	delete(fields, "output_config")
	encoded, err := json.Marshal(fields)
	if err != nil {
		f.t.Errorf("re-encode body: %v", err)
	}
	return effort, label, string(encoded)
}

// writeMessagesStream answers with thinking, then the record_answer call.
func writeMessagesStream(w http.ResponseWriter, outputTokens int64) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("request-id", "req_probe")
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5","usage":{"input_tokens":120,"output_tokens":1}}}`,
		`{"type":"ping"}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Primes below 60."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"record_answer","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"answer\":440}"}}`,
		`{"type":"content_block_stop","index":1}`,
		fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_details":null},"usage":{"output_tokens":%d}}`, outputTokens),
		`{"type":"message_stop"}`,
	}
	for _, event := range events {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &head)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, event)
	}
}

// claudeClient builds a client for the claude row's own base URL, so it
// speaks the native Messages API, with every request rerouted to f.
func claudeClient(t *testing.T, f *fakeMessagesAPI) *model.Client {
	t.Helper()
	p := provider(t, "claude")
	base, err := model.New(p.BaseURL, claudeModel, testKey)
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := base.ForkWithTransport(func(next http.RoundTripper) http.RoundTripper {
		return redirectTransport{target: target, next: next}
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRunClaudeSendsOutputConfigEffort(t *testing.T) {
	fake := newFakeMessagesAPI(t)
	plan, err := NewPlan(provider(t, "claude"), claudeModel, nil, Options{Secrets: []string{testKey}})
	if err != nil {
		t.Fatal(err)
	}
	run := runPlan(t, claudeClient(t, fake), plan, fake)

	wantPosts := []string{"none"}
	wantSent := []string{"none"}
	for _, level := range append(slices.Clone(claudeLevels), ControlLevel) {
		wantPosts = append(wantPosts, "output_config.effort="+level)
		wantSent = append(wantSent, `"output_config":{"effort":"`+level+`"}`)
	}
	if !slices.Equal(run.posts, wantPosts) {
		t.Fatalf("posts = %v, want %v", run.posts, wantPosts)
	}
	for i, row := range run.report.Rows {
		if row.Sent != wantSent[i] {
			t.Fatalf("row %d sent %q, want %q", i+1, row.Sent, wantSent[i])
		}
		if row.Kind != KindDefault && row.Shape != "output_config.effort" {
			t.Fatalf("row %d shape = %q", i+1, row.Shape)
		}
	}
	// Spec point 9: the effort field is the only difference between requests.
	rests := fake.bodiesWithoutEffort()
	for i, rest := range rests[1:] {
		if rest != rests[0] {
			t.Fatalf("request %d differs from Default beyond output_config:\n%s\nvs\n%s", i+2, rest, rests[0])
		}
	}

	want := []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictValidated}
	if got := verdicts(run.report.Rows); !slices.Equal(got, want) {
		t.Fatalf("verdicts = %v, want %v", got, want)
	}
	control := run.report.Rows[len(run.report.Rows)-1]
	if control.HTTPStatus != http.StatusBadRequest || control.Outcome != OutcomeHTTPError || control.BudgetTokens != 0 {
		t.Fatalf("control row = %+v", control)
	}
	for _, row := range run.report.Rows[:len(run.report.Rows)-1] {
		if !row.ToolCalled || !row.AnswerCorrect || row.FirstTokenMS == nil || row.ReasoningTokens != nil {
			t.Fatalf("row %d = %+v; want the tool answer, a first token and no reasoning count", row.Index, row)
		}
	}
	shape := run.report.Summary.Shapes[0]
	if shape.Shape != "output_config.effort" || !shape.Eligible || shape.Signal != SignalCompletion {
		t.Fatalf("summary = %+v", shape)
	}
	if run.report.Summary.Recommended != "output_config.effort" {
		t.Fatalf("recommended = %q", run.report.Summary.Recommended)
	}
	line := "Eligible to enable output_config.effort for claude: yes"
	if !strings.Contains(run.table, line) || strings.Contains(run.table, "endpoint override") {
		t.Fatalf("table lacks %q or claims an override:\n%s", line, run.table)
	}
}
