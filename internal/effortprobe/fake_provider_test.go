package effortprobe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"likha/internal/model"
)

// testKey is the credential every fake provider expects. Tests assert it
// never appears in the table, progress or report.
const testKey = "test-key-5f1c9e2ad04b7731"

// reply is what a fake provider answers to one request.
type reply struct {
	status     int    // 0 means 200 with a stream
	errBody    string // body of a non-200 answer
	reasoning  int64  // reported reasoning tokens; -1 leaves the field out
	completion int64
	noUsage    bool // send no usage object at all
	stall      bool // hold the response until the client gives up
	toolOnly   bool // answer with the tool call only: no text, no reasoning
}

// behavior decides the reply from the effort a request carried; carried is
// false when the body had no effort field (Default).
type behavior func(effort string, carried bool) reply

// fakeProvider is a chat-completions (or Responses) server that records the
// effort fragment of every POST it receives, in order.
type fakeProvider struct {
	t        *testing.T
	behave   behavior
	server   *httptest.Server
	wantAuth string // the Authorization header every POST must carry
	// refuseStreamOptions answers 400 to any body carrying stream_options,
	// as some OpenAI-compatible providers do; Likha's client then re-sends.
	refuseStreamOptions bool

	mu    sync.Mutex
	posts []string // effort field of each POST: "reasoning_effort=high", "reasoning.effort=low" or "none"
	rests []string // each POST body with its effort fields removed
}

func newFakeProvider(t *testing.T, behave behavior) *fakeProvider {
	t.Helper()
	f := &fakeProvider{t: t, behave: behave, wantAuth: "Bearer " + testKey}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeProvider) endpoint() string { return f.server.URL + "/v1" }

func (f *fakeProvider) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...)
}

// bodiesWithoutEffort returns every POST body minus its effort fields.
func (f *fakeProvider) bodiesWithoutEffort() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.rests...)
}

// withoutEffort re-encodes a body without the fields that carry effort, so
// requests can be compared on everything else.
func withoutEffort(t *testing.T, raw []byte) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Errorf("request body is not JSON: %v", err)
		return ""
	}
	delete(fields, "reasoning_effort")
	delete(fields, "reasoning")
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Errorf("re-encode body: %v", err)
	}
	return string(encoded)
}

func (f *fakeProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != f.wantAuth {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read body: %v", err)
		return
	}
	effort, label, carried := readEffort(f.t, raw)
	f.mu.Lock()
	f.posts = append(f.posts, label)
	f.rests = append(f.rests, withoutEffort(f.t, raw))
	f.mu.Unlock()
	if f.refuseStreamOptions && bytes.Contains(raw, []byte(`"stream_options"`)) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"Unsupported parameter: stream_options"}}`)
		return
	}
	answer := f.behave(effort, carried)
	switch {
	case answer.stall:
		<-r.Context().Done()
		return
	case answer.status != 0 && answer.status != http.StatusOK:
		w.WriteHeader(answer.status)
		fmt.Fprint(w, answer.errBody)
		return
	}
	if r.URL.Path == "/v1/responses" {
		writeResponsesStream(w, answer)
		return
	}
	writeChatStream(w, answer)
}

// readEffort finds the effort field of a request body.
func readEffort(t *testing.T, raw []byte) (effort, label string, carried bool) {
	var body struct {
		ReasoningEffort *string `json:"reasoning_effort"`
		Reasoning       *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Errorf("request body is not JSON: %v", err)
		return "", "unreadable", false
	}
	switch {
	case body.ReasoningEffort != nil:
		return *body.ReasoningEffort, "reasoning_effort=" + *body.ReasoningEffort, true
	case body.Reasoning != nil:
		return body.Reasoning.Effort, "reasoning.effort=" + body.Reasoning.Effort, true
	}
	return "", "none", false
}

func usageJSON(answer reply, chat bool) string {
	details, prompt, completion := "output_tokens_details", "input_tokens", "output_tokens"
	if chat {
		details, prompt, completion = "completion_tokens_details", "prompt_tokens", "completion_tokens"
	}
	usage := fmt.Sprintf(`{%q:120,%q:%d`, prompt, completion, answer.completion)
	if answer.reasoning >= 0 {
		usage += fmt.Sprintf(`,%q:{"reasoning_tokens":%d}`, details, answer.reasoning)
	}
	return usage + "}"
}

func writeChatStream(w http.ResponseWriter, answer reply) {
	w.Header().Set("Content-Type", "text/event-stream")
	// A role-only opening chunk and a keepalive comment carry no output.
	fmt.Fprint(w, ": keepalive\n\n")
	events := []string{`{"choices":[{"delta":{"role":"assistant","content":""}}]}`}
	if !answer.toolOnly {
		events = append(events, `{"choices":[{"delta":{"content":"The sum is 440."}}]}`)
	}
	events = append(events,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"record_answer","arguments":"{\"answer\":440}"}}]}}]}`)
	if !answer.noUsage {
		events = append(events, `{"choices":[],"usage":`+usageJSON(answer, true)+`}`)
	}
	events = append(events, "[DONE]")
	for _, event := range events {
		fmt.Fprintf(w, "data: %s\n\n", event)
	}
}

func writeResponsesStream(w http.ResponseWriter, answer reply) {
	w.Header().Set("Content-Type", "text/event-stream")
	if !answer.toolOnly {
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"delta\":\"The sum is 440.\"}\n\n")
	}
	fmt.Fprint(w, "event: response.output_item.added\n")
	fmt.Fprint(w, `data: {"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"record_answer","arguments":""}}`+"\n\n")
	fmt.Fprint(w, "event: response.function_call_arguments.delta\n")
	fmt.Fprint(w, `data: {"item_id":"fc_1","delta":"{\"answer\":440}"}`+"\n\n")
	usage := ""
	if !answer.noUsage {
		usage = `,"usage":` + usageJSON(answer, false)
	}
	fmt.Fprint(w, "event: response.completed\n")
	fmt.Fprint(w, `data: {"response":{"status":"completed","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","namespace":"likha","name":"record_answer","arguments":"{\"answer\":440}"}]`+usage+`}}`+"\n\n")
}

// redirectTransport sends every request for the public ChatGPT resource to
// a local test server, as the model package's own OAuth tests do: the
// OAuth client refuses any other endpoint.
type redirectTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	routed := req.Clone(req.Context())
	routed.URL.Scheme, routed.URL.Host, routed.Host = rt.target.Scheme, rt.target.Host, ""
	return rt.next.RoundTrip(routed)
}

// chatGPTClient builds a signed-in ChatGPT client whose requests reach f.
func chatGPTClient(t *testing.T, f *fakeProvider, modelID string) *model.Client {
	t.Helper()
	f.wantAuth = "Bearer access-secret-value"
	creds := model.OAuthCredentials{
		Refresh: "refresh-secret-value", Access: "access-secret-value", Expires: time.Now().Add(time.Hour).UnixMilli(),
		Issuer: model.ChatGPTIssuer, Subject: "probe-subject", ClientID: "issued-client-probe",
		HostID: "urn:uuid:22222222-2222-4222-8222-222222222222", TokenType: "Bearer",
		Scopes: []string{"openid", model.ChatGPTPlanScope},
	}
	base, err := model.NewOAuth(model.ChatGPTResource, modelID, creds.Issuer, creds.ClientID, creds)
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
