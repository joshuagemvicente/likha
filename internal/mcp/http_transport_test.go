package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "s3cr3t-token-value-0123456789"

// fakeHTTPServer is a Streamable HTTP MCP server for tests. It answers
// initialize, tools/list and tools/call with JSON or SSE responses, issues a
// session ID, and records what Likha sent.
type fakeHTTPServer struct {
	t   *testing.T
	sse bool

	mu         sync.Mutex
	requests   []recorded
	session    string
	sessions   int
	expireNext bool   // answer the next tools/call with 404 (session expired)
	serverAsks string // tools/call first sends a server request with this method on the stream
	hangDelete chan struct{}
	answers    []string // responses Likha posted to server requests
	replies    chan json.RawMessage
	cancelled  chan float64
	deletes    chan string
}

type recorded struct {
	method        string
	session       string
	protocol      string
	authorization string
	accept        string
}

func newFakeHTTPServer(t *testing.T, sse bool) (*fakeHTTPServer, *httptest.Server) {
	t.Helper()
	f := &fakeHTTPServer{
		t:         t,
		sse:       sse,
		replies:   make(chan json.RawMessage, 4),
		cancelled: make(chan float64, 4),
		deletes:   make(chan string, 4),
	}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakeHTTPServer) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.method == method {
			n++
		}
	}
	return n
}

func (f *fakeHTTPServer) recordedRequests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

func (f *fakeHTTPServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		f.mu.Lock()
		hang := f.hangDelete
		f.mu.Unlock()
		if hang != nil {
			<-hang
		}
		f.deletes <- r.Header.Get("Mcp-Session-Id")
		w.WriteHeader(http.StatusOK)
		return
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, recorded{
		method:        message.Method,
		session:       r.Header.Get("Mcp-Session-Id"),
		protocol:      r.Header.Get("MCP-Protocol-Version"),
		authorization: r.Header.Get("Authorization"),
		accept:        r.Header.Get("Accept"),
	})
	session := f.session
	expire := f.expireNext && message.Method == "tools/call"
	if expire {
		f.expireNext = false
	}
	f.mu.Unlock()

	if message.Method == "" { // a response from Likha (to a server request)
		f.mu.Lock()
		f.answers = append(f.answers, fmt.Sprintf("id=%s result=%s error=%s", message.ID, message.Result, message.Error))
		f.mu.Unlock()
		f.replies <- message.Error
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if message.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != session {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	if expire {
		http.Error(w, "session expired", http.StatusNotFound)
		return
	}
	if len(message.ID) == 0 { // notification
		if message.Method == "notifications/cancelled" {
			var params struct {
				RequestID float64 `json:"requestId"`
			}
			json.Unmarshal(message.Params, &params)
			f.cancelled <- params.RequestID
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch message.Method {
	case "initialize":
		f.mu.Lock()
		f.sessions++
		f.session = fmt.Sprintf("session-%d", f.sessions)
		w.Header().Set("Mcp-Session-Id", f.session)
		f.mu.Unlock()
		f.reply(w, message.ID, `{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"fake-http","version":"1"}}`)
	case "tools/list":
		f.reply(w, message.ID, `{"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object"}},{"name":"slow","inputSchema":{"type":"object"}},{"name":"drop","inputSchema":{"type":"object"}},{"name":"leak","inputSchema":{"type":"object"}},{"name":"fail","inputSchema":{"type":"object"}}]}`)
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(message.Params, &params)
		switch params.Name {
		case "slow":
			<-r.Context().Done()
		case "drop":
			hijacker, _ := w.(http.Hijacker)
			conn, _, err := hijacker.Hijack()
			if err == nil {
				conn.Close()
			}
		case "leak":
			f.reply(w, message.ID, fmt.Sprintf(`{"content":[{"type":"text","text":"token is %s"}]}`, testToken))
		case "fail":
			f.raw(w, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"bad token %s"}}`, message.ID, testToken))
		default:
			content, _ := json.Marshal([]map[string]string{{"type": "text", "text": string(params.Arguments)}})
			f.reply(w, message.ID, fmt.Sprintf(`{"content":%s}`, content))
		}
	default:
		f.reply(w, message.ID, `{}`)
	}
}

func (f *fakeHTTPServer) reply(w http.ResponseWriter, id json.RawMessage, result string) {
	f.raw(w, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result))
}

func (f *fakeHTTPServer) raw(w http.ResponseWriter, message string) {
	if !f.sse {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, message)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher := w.(http.Flusher)
	// A notification precedes the response, as the transport allows.
	fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{\"level\":\"info\",\"data\":\"working\"}}\n\n")
	flusher.Flush()
	f.mu.Lock()
	asks := f.serverAsks
	f.mu.Unlock()
	if asks != "" && strings.Contains(message, `"content"`) {
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"srv-1\",\"method\":%q,\"params\":{}}\n\n", asks)
		flusher.Flush()
		select {
		case <-f.replies:
		case <-time.After(5 * time.Second):
			f.t.Error("Likha did not answer the server request")
		}
	}
	// Split the data across two lines to exercise multi-line events; SSE
	// joins them with a newline, which JSON allows between tokens.
	half := strings.Index(message, ",") + 1
	fmt.Fprintf(w, "id: 1\ndata: %s\ndata: %s\n\n", message[:half], message[half:])
	flusher.Flush()
}

func httpConfig(url string) ServerConfig {
	return ServerConfig{URL: url + "/mcp", Headers: map[string]string{"Authorization": "Bearer ${LIKHA_TEST_MCP_TOKEN}"}}
}

func TestHTTPServerHandshakeAndCall(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprintf("sse=%t", sse), func(t *testing.T) {
			t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
			fake, server := newFakeHTTPServer(t, sse)
			client, err := Start(context.Background(), "docs", httpConfig(server.URL), "likha/1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if len(client.Tools) != 5 || client.Tools[0].Name != "echo" {
				t.Fatalf("tools = %+v", client.Tools)
			}
			result, isError, err := client.Call(context.Background(), "echo", json.RawMessage(`{"message":"hi"}`))
			if err != nil || isError || !strings.Contains(result, `"message":"hi"`) {
				t.Fatalf("call: %q %t %v", result, isError, err)
			}
			requests := fake.recordedRequests()
			if requests[0].method != "initialize" || requests[0].session != "" || requests[0].protocol != "" {
				t.Fatalf("initialize carried session or protocol headers: %+v", requests[0])
			}
			for _, r := range requests {
				if r.authorization != "Bearer "+testToken {
					t.Fatalf("%s: Authorization = %q", r.method, r.authorization)
				}
				if r.accept != "application/json, text/event-stream" {
					t.Fatalf("%s: Accept = %q", r.method, r.accept)
				}
			}
			for _, r := range requests[1:] {
				if r.session != "session-1" || r.protocol != "2025-03-26" {
					t.Fatalf("%s: session=%q protocol=%q", r.method, r.session, r.protocol)
				}
			}
		})
	}
}

func TestHTTPServerRequestOnStreamIsAnswered(t *testing.T) {
	cases := []struct {
		method string
		want   string
	}{
		{"sampling/createMessage", `id="srv-1" result=null error={"code":-32601,"message":"method not found"}`},
		{"ping", `id="srv-1" result={} error=null`},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
			fake, server := newFakeHTTPServer(t, true)
			client, err := Start(context.Background(), "docs", httpConfig(server.URL), "likha/1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			fake.mu.Lock()
			fake.serverAsks = tc.method
			fake.mu.Unlock()
			if _, _, err := client.Call(context.Background(), "echo", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			fake.mu.Lock()
			answers := append([]string(nil), fake.answers...)
			fake.mu.Unlock()
			if len(answers) != 1 || answers[0] != tc.want {
				t.Fatalf("server request answered with %q, want [%q]", answers, tc.want)
			}
		})
	}
}

func TestHTTPSessionExpiryRestartsWithoutResending(t *testing.T) {
	t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
	fake, server := newFakeHTTPServer(t, false)
	client, err := Start(context.Background(), "docs", httpConfig(server.URL), "likha/1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	fake.mu.Lock()
	fake.expireNext = true
	fake.mu.Unlock()
	_, _, err = client.Call(context.Background(), "echo", json.RawMessage(`{"n":1}`))
	if err == nil || !strings.Contains(err.Error(), "session expired") || !strings.Contains(err.Error(), "not retried") {
		t.Fatalf("expired call error = %v", err)
	}
	if got := fake.count("tools/call"); got != 1 {
		t.Fatalf("tools/call sent %d times, want 1 (never resent)", got)
	}
	if got := fake.count("initialize"); got != 2 {
		t.Fatalf("initialize sent %d times, want a new handshake", got)
	}
	result, _, err := client.Call(context.Background(), "echo", json.RawMessage(`{"n":2}`))
	if err != nil || !strings.Contains(result, `"n":2`) {
		t.Fatalf("call after restart: %q %v", result, err)
	}
	requests := fake.recordedRequests()
	if last := requests[len(requests)-1]; last.session != "session-2" {
		t.Fatalf("call after restart used session %q", last.session)
	}
}

func TestHTTPDroppedCallFailsOnlyThatCall(t *testing.T) {
	t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
	_, server := newFakeHTTPServer(t, false)
	client, err := Start(context.Background(), "docs", httpConfig(server.URL), "likha/1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, _, err := client.Call(context.Background(), "drop", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "the connection dropped") {
		t.Fatalf("dropped call error = %v", err)
	}
	if _, _, err := client.Call(context.Background(), "echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("next call after a drop: %v", err)
	}
}

func TestHTTPCancelSendsNotificationAndCloseEndsSession(t *testing.T) {
	t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
	fake, server := newFakeHTTPServer(t, true)
	client, err := Start(context.Background(), "docs", httpConfig(server.URL), "likha/1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := client.Call(ctx, "slow", json.RawMessage(`{}`))
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-done; err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled call error = %v", err)
	}
	select {
	case id := <-fake.cancelled:
		if id != 3 { // initialize=1, tools/list=2, the slow call=3
			t.Fatalf("cancelled requestId = %v", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no notifications/cancelled reached the server")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case session := <-fake.deletes:
		if session != "session-1" {
			t.Fatalf("DELETE session = %q", session)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close sent no DELETE")
	}
}

func TestHTTPCloseIsBoundedForAHungServer(t *testing.T) {
	old := closeTimeout
	closeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { closeTimeout = old })
	t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
	fake, server := newFakeHTTPServer(t, false)
	client, err := Start(context.Background(), "docs", httpConfig(server.URL), "likha/1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	fake.mu.Lock()
	fake.hangDelete = release
	fake.mu.Unlock()
	start := time.Now()
	client.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %s with a hung server", elapsed)
	}
}

func TestHTTPStartFailuresNameTheirCause(t *testing.T) {
	t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
	status := func(code int, header, body string) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if header != "" {
				name, value, _ := strings.Cut(header, ": ")
				w.Header().Set(name, value)
			}
			w.WriteHeader(code)
			fmt.Fprint(w, body)
		}))
		t.Cleanup(server.Close)
		return server
	}
	closed := func() string {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		listener.Close()
		return "http://" + addr
	}()
	tlsServer := httptest.NewUnstartedServer(http.NotFoundHandler())
	tlsServer.Config.ErrorLog = log.New(io.Discard, "", 0) // the refused handshake is expected
	tlsServer.StartTLS()
	t.Cleanup(tlsServer.Close)
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html>hello</html>")
	}))
	t.Cleanup(html.Close)
	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
	}))
	t.Cleanup(mismatch.Close)
	redirect := status(http.StatusFound, "Location: https://elsewhere.example/mcp?key=1", "")
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		chunk := strings.Repeat("x", 1<<20)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"pad":"`)
		for i := 0; i < 17; i++ {
			fmt.Fprint(w, chunk)
		}
		fmt.Fprint(w, `"}}`)
	}))
	t.Cleanup(huge.Close)

	cases := []struct {
		name    string
		cfg     ServerConfig
		want    string
		notWant string
	}{
		{"unreachable", ServerConfig{URL: closed + "/mcp"}, "could not connect", ""},
		{"untrusted certificate", ServerConfig{URL: tlsServer.URL + "/mcp"}, "certificate not trusted", ""},
		{"oauth challenge", ServerConfig{URL: status(401, `WWW-Authenticate: Bearer resource_metadata="https://x/.well-known/oauth-protected-resource"`, "").URL}, "rejected the credentials (HTTP 401); OAuth sign-in is not supported yet", ""},
		{"bad token", httpConfig(status(403, "", "").URL), "rejected the credentials (HTTP 403)", "OAuth"},
		{"html page", ServerConfig{URL: html.URL}, "not an MCP endpoint", ""},
		{"404 on initialize", ServerConfig{URL: status(404, "", "").URL}, "not an MCP endpoint (HTTP 404)", ""},
		{"405 on initialize", ServerConfig{URL: status(405, "", "").URL}, "not an MCP endpoint (HTTP 405)", ""},
		{"protocol mismatch", ServerConfig{URL: mismatch.URL}, `server speaks protocol "2024-11-05"`, ""},
		{"redirect", ServerConfig{URL: redirect.URL}, "server redirected to https://elsewhere.example; update the url", "key=1"},
		{"too large", ServerConfig{URL: huge.URL}, "16 MiB", ""},
		{"server error echoes the token", httpConfig(status(500, "", "upstream rejected "+testToken).URL), "server answered HTTP 500: upstream rejected [redacted]", testToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Start(context.Background(), "docs", tc.cfg, "likha/1.2.3")
			if err == nil {
				t.Fatal("start succeeded")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want %q", err, tc.want)
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Fatalf("error = %q contains %q", err, tc.notWant)
			}
		})
	}
}

func TestHTTPUnsetVariableSendsNothing(t *testing.T) {
	fake, server := newFakeHTTPServer(t, false)
	cfg := ServerConfig{URL: server.URL + "/mcp", Headers: map[string]string{"Authorization": "Bearer ${LIKHA_TEST_UNSET_TOKEN}"}}
	_, err := Start(context.Background(), "docs", cfg, "likha/1.2.3")
	if err == nil || !strings.Contains(err.Error(), "environment variable LIKHA_TEST_UNSET_TOKEN is not set") {
		t.Fatalf("error = %v", err)
	}
	if got := len(fake.recordedRequests()); got != 0 {
		t.Fatalf("server received %d requests", got)
	}
}

func TestHTTPHeaderValuesNeverReachOutput(t *testing.T) {
	t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
	_, server := newFakeHTTPServer(t, false)
	client, err := Start(context.Background(), "docs", httpConfig(server.URL), "likha/1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, _, err := client.Call(context.Background(), "leak", json.RawMessage(`{}`))
	if err != nil || strings.Contains(result, testToken) || !strings.Contains(result, redactedText) {
		t.Fatalf("tool result = %q err=%v", result, err)
	}
	_, _, err = client.Call(context.Background(), "fail", json.RawMessage(`{}`))
	if err == nil || strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), redactedText) {
		t.Fatalf("RPC error = %v", err)
	}
}

func TestExpandHeaders(t *testing.T) {
	env := map[string]string{"TOKEN": "abc123", "OTHER": "zz", "EMPTY": ""}
	lookup := func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}
	cases := []struct {
		name    string
		in      map[string]string
		want    map[string]string
		wantErr string
	}{
		{"literal", map[string]string{"X-Api": "plain"}, map[string]string{"X-Api": "plain"}, ""},
		{"set", map[string]string{"Authorization": "Bearer ${TOKEN}"}, map[string]string{"Authorization": "Bearer abc123"}, ""},
		{"several", map[string]string{"X": "${TOKEN}-${OTHER}"}, map[string]string{"X": "abc123-zz"}, ""},
		{"malformed stays literal", map[string]string{"X": "${ not a ref ${TOKEN"}, map[string]string{"X": "${ not a ref ${TOKEN"}, ""},
		{"unset", map[string]string{"X": "${MISSING}"}, nil, "environment variable MISSING is not set"},
		{"empty", map[string]string{"X": "${EMPTY}"}, nil, "environment variable EMPTY is not set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := expandHeaders(tc.in, lookup)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range tc.want {
				if got[name] != value {
					t.Fatalf("%s = %q, want %q", name, got[name], value)
				}
			}
		})
	}
}

func TestValidateServer(t *testing.T) {
	cases := []struct {
		name string
		cfg  ServerConfig
		want string // "" means valid
	}{
		{"stdio", ServerConfig{Command: "npx"}, ""},
		{"stdio with pasted type", ServerConfig{Command: "npx", Type: "stdio"}, ""},
		{"https", ServerConfig{URL: "https://mcp.example.com/mcp"}, ""},
		{"type http", ServerConfig{URL: "https://mcp.example.com/mcp", Type: "http"}, ""},
		{"type streamable-http", ServerConfig{URL: "https://mcp.example.com/mcp", Type: "streamable-http"}, ""},
		{"loopback name", ServerConfig{URL: "http://localhost:8080/mcp"}, ""},
		{"loopback v4", ServerConfig{URL: "http://127.0.0.1:8080/mcp"}, ""},
		{"loopback v4 range", ServerConfig{URL: "http://127.4.5.6/mcp"}, ""},
		{"loopback v6", ServerConfig{URL: "http://[::1]:8080/mcp"}, ""},
		{"neither", ServerConfig{}, "must have a command or a url"},
		{"both", ServerConfig{Command: "npx", URL: "https://x/mcp"}, "both a command and a url"},
		{"plain http remote", ServerConfig{URL: "http://example.com/mcp"}, "plain http is only allowed for localhost; use https"},
		{"lookalike loopback", ServerConfig{URL: "http://127.0.0.1.example.com/mcp"}, "plain http is only allowed for localhost"},
		{"other scheme", ServerConfig{URL: "ftp://example.com/mcp"}, `url scheme "ftp" is not supported`},
		{"relative", ServerConfig{URL: "/mcp"}, "url must be absolute"},
		{"userinfo", ServerConfig{URL: "https://user:pw@example.com/mcp"}, "must not contain a user name or password"},
		{"sse type", ServerConfig{URL: "https://x/sse", Type: "sse"}, "legacy HTTP+SSE servers are not supported"},
		{"unknown type", ServerConfig{URL: "https://x/mcp", Type: "websocket"}, `unsupported type "websocket"`},
		{"args on http", ServerConfig{URL: "https://x/mcp", Args: []string{"a"}}, "args and env apply only to stdio servers"},
		{"env on http", ServerConfig{URL: "https://x/mcp", Env: map[string]string{"A": "b"}}, "args and env apply only to stdio servers"},
		{"provider key", ServerConfig{URL: "https://x/mcp", Headers: map[string]string{"Authorization": "Bearer ${LIKHA_API_KEY}"}}, "may not use LIKHA_API_KEY"},
		{"named provider key", ServerConfig{URL: "https://x/mcp", Headers: map[string]string{"X": "${LIKHA_OPENAI_API_KEY}"}}, "may not use LIKHA_OPENAI_API_KEY"},
		{"bad header name", ServerConfig{URL: "https://x/mcp", Headers: map[string]string{"Bad Name": "v"}}, `invalid header name "Bad Name"`},
		{"header line break", ServerConfig{URL: "https://x/mcp", Headers: map[string]string{"X": "a\r\nInjected: 1"}}, "contains a line break"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateServer("docs", tc.cfg)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadConfigAcceptsPastedHTTPEntries(t *testing.T) {
	dir := t.TempDir()
	config := `{"mcpServers":{
		"local":{"command":"npx","args":["-y","server"],"type":"stdio"},
		"docs":{"type":"http","url":"https://mcp.example.com/mcp","headers":{"Authorization":"Bearer ${DOCS_TOKEN}"}}
	}}`
	if err := writeConfig(dir, config); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if servers["local"].Remote() || !servers["docs"].Remote() || servers["docs"].Headers["Authorization"] != "Bearer ${DOCS_TOKEN}" {
		t.Fatalf("servers = %+v", servers)
	}
	if err := writeConfig(dir, `{"mcpServers":{"docs":{"url":"http://example.com/mcp"}}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), `mcp.json: server "docs": plain http is only allowed for localhost; use https`) {
		t.Fatalf("plain http accepted or wrong error: %v", err)
	}
}

func TestStatusShowsOriginOnly(t *testing.T) {
	t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
	_, server := newFakeHTTPServer(t, false)
	cfg := httpConfig(server.URL)
	cfg.URL += "?key=private"
	manager := &McpManager{
		path:    "/state/mcp.json",
		servers: map[string]ServerConfig{"docs": cfg, "down": {URL: "https://down.example/mcp?k=1", Headers: map[string]string{"Authorization": "Bearer ${LIKHA_TEST_UNSET_TOKEN}"}}},
		clients: map[string]*Client{},
		trusted: map[string]bool{},
		failed:  map[string]string{},
	}
	defer manager.Stop()
	if tools := manager.CatalogTools("likha/test"); len(tools) != 5 {
		t.Fatalf("catalog = %+v", tools)
	}
	status := manager.Status()
	if !strings.Contains(status, "- docs (http "+server.URL+"): running") {
		t.Fatalf("status = %q", status)
	}
	if !strings.Contains(status, "- down (http https://down.example): failed (mcp down: environment variable LIKHA_TEST_UNSET_TOKEN is not set)") {
		t.Fatalf("status = %q", status)
	}
	for _, private := range []string{server.URL + "/mcp", "down.example/mcp", "key=private", "k=1", testToken} {
		if strings.Contains(status, private) {
			t.Fatalf("status shows %q: %q", private, status)
		}
	}
	if got := manager.Origin("docs"); got != server.URL {
		t.Fatalf("Origin = %q", got)
	}
	if got := manager.Summary(); got != "mcp 1/2 crashed" {
		t.Fatalf("Summary = %q", got)
	}
}

func TestEmptyStatusHintShowsBothShapes(t *testing.T) {
	manager, err := NewMcpManager(t.TempDir(), "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if !strings.Contains(status, `"command"`) || !strings.Contains(status, `"url"`) {
		t.Fatalf("hint = %q", status)
	}
}

func TestHTTPCallThroughManagerKeepsServerAfterFailure(t *testing.T) {
	t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
	_, server := newFakeHTTPServer(t, false)
	manager := NewMcpManagerForTest(map[string]ServerConfig{"docs": httpConfig(server.URL)}, map[string]*Client{}, map[string]bool{"docs": true}, map[string]string{})
	defer manager.Stop()
	manager.CatalogTools("likha/test")
	if _, _, err := manager.CallTool(context.Background(), "docs", "drop", json.RawMessage(`{}`)); err == nil {
		t.Fatal("drop reported success")
	}
	if got := manager.Summary(); got != "mcp 1/1" {
		t.Fatalf("a failed call marked the server failed: %q", got)
	}
	if _, _, err := manager.CallTool(context.Background(), "docs", "echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("next call: %v", err)
	}
}

func TestHTTPCallIsBoundedByCallTimeout(t *testing.T) {
	t.Setenv("LIKHA_TEST_MCP_TOKEN", testToken)
	_, server := newFakeHTTPServer(t, false)
	manager := NewMcpManagerForTest(map[string]ServerConfig{"docs": httpConfig(server.URL)}, map[string]*Client{}, map[string]bool{"docs": true}, map[string]string{})
	defer manager.Stop()
	manager.CatalogTools("likha/test")
	old := CallTimeout
	CallTimeout = 300 * time.Millisecond
	t.Cleanup(func() { CallTimeout = old })
	start := time.Now()
	_, _, err := manager.CallTool(context.Background(), "docs", "slow", json.RawMessage(`{}`))
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("slow call error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("call took %s past a 300ms limit", elapsed)
	}
}

func writeConfig(dir, content string) error {
	return os.WriteFile(dir+"/mcp.json", []byte(content), 0o600)
}
