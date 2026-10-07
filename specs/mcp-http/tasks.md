# Tasks: MCP over HTTP

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md). Each milestone leaves `go test ./...`
and `go vet ./...` green and can merge on its own. `internal/mcp` may import
only `model` (ARCHITECTURE.md), so the transport uses `net/http` directly and
does not borrow `internal/webtools` helpers.

Fixed values (spec points and drafting defaults), defined once in
`internal/mcp`:

| Name | Value |
| --- | --- |
| protocol version requested | `2025-03-26` (unchanged) |
| connect + handshake limit | `CallTimeout` (60s) |
| per-call limit | `CallTimeout` (60s) |
| exit DELETE limit | 2s |
| response size cap | 16 MiB |
| displayed server error text | 2 KiB, redacted |

## M1 — Configuration (`internal/mcp/config.go`)

- [x] **T1. Entry shape.** Add `URL string`, `Headers map[string]string`
      and `Type string` to `ServerConfig` (JSON `url`, `headers`, `type`).
      Add `func (c ServerConfig) Remote() bool`. Keep the stdio fields and
      their JSON names.
      *Verify:* table test decoding stdio, HTTP, and pasted Claude Code
      (`"type": "http"`) entries.
- [x] **T2. Validation in `LoadConfig`.** Replace the
      `must have a command` check with a `validateServer(name, cfg) error`
      covering spec points 1, 2, 5, 6 and the `type` drafting default:
      exactly one of `command`/`url`; `https`, or `http` with a loopback
      host (`localhost`, `127.0.0.0/8`, `::1`, parsed with `net/netip`, not
      string prefixes); no userinfo; no `args`/`env` on HTTP entries;
      `type` empty, `http` or `streamable-http`, with `sse` refused by name;
      no `${LIKHA_API_KEY}` or `${LIKHA_*_API_KEY}` reference in headers.
      Errors wrap with the server name.
      *Verify:* table test, one case per rule, matching each error message.
- [x] **T3. Header substitution.** `expandHeaders(headers, lookupEnv)`
      returns the expanded map plus the list of substituted values (for
      redaction), or an error naming the first unset or empty variable.
      Only `${NAME}` with `NAME` matching `[A-Za-z_][A-Za-z0-9_]*` is
      substituted; other text is literal. Expansion runs at start, not at
      load, so `/mcp` can show the failure per server (spec point 4).
      *Verify:* table test with set, unset, empty, malformed `${`, and
      multiple references.

## M2 — Transport seam (`internal/mcp/mcp.go`)

- [x] **T4. Extract the stdio transport.** Introduce an unexported
      interface the `Client` uses for `send(ctx, message) error`, a
      delivery path for responses, and `close() error`. Move the process
      pipes and `readLoop` behind a `stdioTransport`. `Start` keeps its
      signature and dispatches on `cfg.Remote()`.
      *Verify:* existing `mcp_test.go`, `manager_trust_test.go`,
      `approve_always_test.go` and `internal/tui/mcp_test.go` pass
      unchanged; `go test -race ./internal/mcp/`.
- [x] **T5. HTTP transport: requests and responses.** `httpTransport`
      POSTs each JSON-RPC message to the URL with
      `Accept: application/json, text/event-stream`,
      `Content-Type: application/json`, the expanded headers, and the
      `User-Agent` Likha already uses. Handle `202` for notifications, a
      single JSON object, and an SSE stream that carries notifications or
      server requests before the response. Read through a counting reader
      capped at 16 MiB. The `http.Client` does not follow redirects
      (`CheckRedirect` returns `http.ErrUseLastResponse`) and keeps the
      default proxy behavior.
      *Verify:* `httptest` servers for JSON mode, SSE mode, SSE with a
      notification before the response, 202 on `notifications/initialized`,
      a 17 MiB body, and a 302.
- [x] **T6. Session ID and protocol header.** Store `Mcp-Session-Id` from
      the `initialize` response; send it and
      `MCP-Protocol-Version: 2025-03-26` on every later request. On 404
      with a session ID, re-run the handshake once and fail the triggering
      call with the "session expired; not retried" result.
      *Verify:* `httptest` server that issues an ID, checks it on every
      request, and expires it once; the tool call is not resent.
- [x] **T7. Server-initiated requests.** A JSON-RPC request arriving on a
      response stream is answered by POSTing a `-32601` error response; a
      notification is dropped.
      *Verify:* `httptest` server that sends `sampling/createMessage` mid-
      stream and checks the error reply arrives.
- [x] **T8. Cancellation and exit.** When the call context is cancelled,
      POST `notifications/cancelled` with the request ID (bounded by a
      short detached context). `Close` sends `DELETE` with the session ID
      under a 2s limit and ignores 405.
      *Verify:* `httptest` server records the cancel notification and the
      DELETE; a test with an unroutable host shows `Close` returns within
      the limit.
- [x] **T9. Failure classification.** Wrap start failures in errors that
      tell apart: dial/unreachable, TLS (`errors.As` with
      `*tls.CertificateVerificationError` / `x509` types, never string
      matching), 401/403 (with `WWW-Authenticate` naming a Bearer resource
      metadata or OAuth flagged for the "OAuth not supported yet" suffix),
      non-MCP response (HTML content type, 404/405 on `initialize`),
      protocol mismatch, unset variable. Per-call network failures return
      an error without marking the server failed (spec point 11).
      *Verify:* table test over `httptest` and TLS test servers matching
      each wording.
- [x] **T10. Redaction.** One `redactor` built from the substituted header
      values (and literal header values) is applied to every error string
      and server error body before it leaves `internal/mcp`; server bodies
      are cut to 2 KiB first.
      *Verify:* test server echoes the token in a 401 body and in a JSON-RPC
      error; neither the returned error nor `Status()` contains it.

## M3 — Manager and UI

- [x] **T11. `/mcp` and status line.** `Status()` marks HTTP servers
      `http <origin>`, never path, query or header values; the empty-config
      hint shows both shapes (spec point 17). `Summary()` is unchanged in
      form and counts HTTP servers.
      *Verify:* `internal/mcp` status tests; `internal/tui/status_line_test.go`
      with a mixed config.
- [x] **T12. Review origin line.** The MCP first-call review shows the
      origin for HTTP servers (drafting default). The review body is built
      in `internal/agent/tool_registry.go` (the `AuthorizeTool` callback);
      add `McpManager.Origin(server) string` ("" for stdio) and one line to
      the body when it is non-empty. stdio reviews are unchanged.
      *Verify:* agent test on the body for both kinds; TUI test rendering
      the HTTP review below 50 columns.
- [x] **T13. Docs.** `docs/tools.md` MCP section: the `url`/`headers`
      shape, `${NAME}`, the loopback rule, OAuth and SSE not supported,
      that tool arguments go to a remote server. Remove "HTTP transports
      are deferred". Update `ARCHITECTURE.md`'s `mcp` row
      ("MCP stdio core" → stdio and Streamable HTTP).

## M4 — Verification

- [x] **T14. End-to-end TUI test.** A TUI test with an `httptest` MCP
      server in `mcp.json`: first call shows the review with the origin,
      **Approve always** trusts the server, the next call runs without a
      review, `/plan` refuses with zero HTTP requests, session switch clears
      trust.
      *Done as split tests rather than one model-driven TUI run:*
      `internal/agent/mcp_http_test.go` (review origin line through the real
      registry and an `httptest` server, plan-mode refusal with zero
      `tools/call`), `internal/tui/mcp_http_test.go` (`/mcp` through the TUI,
      the review at 40/48/80 columns, Approve always then session switch
      clears trust) and `internal/mcp` (Approve always is transport-agnostic
      trust code already covered for stdio).
- [x] **T15. Secret sweep.** After T14, scan the session database, the
      transcript render and every status string for the test token.
      *Done at the `internal/mcp` boundary:* every error, RPC error message
      and tool result leaving the HTTP transport is redacted
      (`TestHTTPHeaderValuesNeverReachOutput`, the HTTP 500 echo case), and
      `/mcp` plus the TUI view are scanned for the token. The session
      database is not scanned directly; it stores only what crossed that
      boundary.
- [ ] **T16. Live probe (pending).** Run Likha against a real Streamable
      HTTP server, for example the reference "everything" server started in
      Streamable HTTP mode on localhost, and one hosted HTTPS server with a
      bearer token. Record date, server, and what was exercised in
      [context.md](context.md) and `CHANGELOG.md`. Until then the feature
      is implemented (local) only.
