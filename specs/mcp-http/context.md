# Context: MCP over HTTP

Code, docs and protocol rules this feature touches, with the facts that
shaped the spec. All decisions live in [spec.md](spec.md).

## Why now

The 2026-10-07 gap review (Likha vs Claude Code, OpenCode V2 and OMP)
listed MCP HTTP as missing: all three accept URL-based servers, Likha only
stdio ([tooling-landscape/feature-comparison.md](../tooling-landscape/feature-comparison.md)
§2.7, Tier 2 item 9). The user chose Streamable HTTP with static headers
and `${ENV}` substitution, HTTPS except loopback, OAuth and legacy SSE
deferred.

## Code

- `internal/mcp/config.go`
  - `ServerConfig` (line 13) has only `Command`, `Args`, `Env`.
  - `LoadConfig` (line 48) rejects any entry without a command
    (`server %q must have a command`, line 66). This is what a pasted URL
    entry hits today.
  - `EnsureSkeleton` writes the empty `{"mcpServers": {}}` file at 0600.
- `internal/mcp/mcp.go`
  - `protocolVersion = "2025-03-26"`; `handshake` refuses any other
    version the server answers with (exact match).
  - `CallTimeout` (line 34) is 60s and bounds both the handshake and each
    call.
  - `Start` (line 70) is stdio-only: `exec.Command`, stdin/stdout pipes,
    `LIKHA_USER_AGENT` in the child env, `clientInfo` from the user agent.
  - `request` writes a newline-delimited JSON-RPC message to stdin and
    waits on a per-ID channel fed by `readLoop` (line 224), which ignores
    notifications and server requests (line 233). Cancellation only stops
    waiting; no `notifications/cancelled` is sent today.
  - `Close` (line 254) kills the process. There is no transport seam;
    tasks T4 introduces one.
- `internal/mcp/manager.go`
  - `Status` builds the `/mcp` text. The empty-config hint (line 125)
    shows only the `command` shape.
  - `Summary` builds the status-line `mcp n/m` segment.
  - `Stop` (line 187) closes every client on exit.
- `internal/mcp/tool_catalog.go`
  - `CatalogTools` starts servers lazily (`Start`, line 52) and records a
    failure in `m.failed`, which keeps a server dead until relaunch.
  - `AuthorizeTool` holds trust-on-first-use and Approve/Approve always.
  - `CallTool` applies `CallTimeout` (line 285).
- `internal/agent/tool_registry.go` (around line 407): the MCP catalog
  entry and the first-call review body (`MCP server: … Tool: …
  Arguments: …`). The review origin line (drafting default) goes here.
- `internal/tui/status_line.go:262` renders `m.conn.Mcp.Summary()`.
- `internal/actions/command.go:108-114` is the rule that strips
  `LIKHA_API_KEY` and `LIKHA_*_API_KEY` from command environments; spec
  point 5 applies the same rule to header substitution.
- `internal/model/client.go:149` `noRedirectHTTPClient`: model requests
  refuse redirects and use the default transport (so proxy variables
  apply). The MCP HTTP client copies this posture with its own small
  `http.Client`; `mcp` may import `model`, but the helper is unexported
  and a local client keeps the transport self-contained.
- `internal/webtools/fetch.go:462` `fetchDialControl` is prior art for
  checking addresses at dial time. `mcp` must not import `webtools`
  (ARCHITECTURE.md: `mcp` imports only `model`), and MCP deliberately
  allows loopback, which `web_fetch` refuses.

## Tests to extend

- `internal/mcp/mcp_test.go`, `manager_trust_test.go`,
  `approve_always_test.go`: stdio behavior that must stay green after T4.
- `internal/tui/mcp_test.go`, `internal/tui/status_line_test.go`,
  `internal/tui/approve_always_test.go`: `/mcp`, segment and review
  rendering.

## Docs and specs to amend when implemented

- `docs/tools.md:46` says "stdio only for v1 … HTTP transports are
  deferred".
- `ARCHITECTURE.md` `mcp` row: "MCP stdio core".
- [mcp-support/spec.md](../mcp-support/spec.md) decision 1 ("HTTP/SSE
  transports are deferred").
- `specs/v1-spec.md`: FR-16 says "over stdio", and §2 Out of scope says
  "Configured stdio MCP is the existing external-tool exception". FR-46
  must amend both, or the new requirement contradicts the overarching
  contract.

## MCP transport rules relied on

From the Streamable HTTP transport
([2025-03-26](https://modelcontextprotocol.io/specification/2025-03-26/basic/transports),
[2025-06-18](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)),
fetched 2026-10-07:

- One MCP endpoint supports POST and GET. Every client message is a new
  POST carrying `Accept: application/json, text/event-stream`.
- A notification or response body gets `202 Accepted` with no body. A
  request gets either one `application/json` object or a
  `text/event-stream`; "The client MUST support both these cases." The
  stream may carry server requests and notifications before the response.
- "Disconnection SHOULD NOT be interpreted as the client cancelling its
  request. To cancel, the client SHOULD explicitly send an MCP
  `CancelledNotification`." (spec point 12, task T8)
- A server may assign `Mcp-Session-Id` on the initialize response; the
  client "MUST include it … on all of their subsequent HTTP requests". On
  404 for a request carrying it, the client "MUST start a new session by
  sending a new `InitializeRequest`". Clients "SHOULD send an HTTP DELETE"
  to end a session; the server may answer 405.
- 2025-06-18 adds `MCP-Protocol-Version` on every request after
  initialization; a server without it "SHOULD assume protocol version
  2025-03-26". Likha negotiates 2025-03-26, so sending the header with that
  value is correct under both versions.
- 2025-03-26 also permits JSON-RPC batches in a POST body; Likha never
  sends batches, and 2025-06-18 removed them.
- Resumability (`Last-Event-ID`) and the GET listening stream are optional
  (MAY); Likha uses neither in this slice.
- Backwards compatibility with HTTP+SSE is a client choice (probe POST,
  fall back to GET for an `endpoint` event); out of scope here.

## Open points for review

The "Drafting defaults (confirm in review)" section of spec.md lists the
choices the interview did not cover. The ones most worth a second look:
the 404 session-expiry behavior (fail the call rather than resend), the
review origin line, and refusing `args`/`env` on HTTP entries rather than
ignoring them.

## Implementation notes (2026-10-07)

Implemented (local) on branch `impl/mcp-http`: `internal/mcp/config.go`
(entry shape, validation, `${NAME}` expansion), `internal/mcp/mcp.go`
(transport seam, session restart), `internal/mcp/stdio_transport.go`
(unchanged stdio behavior, moved), `internal/mcp/http_transport.go`
(Streamable HTTP), `internal/mcp/manager.go` (`/mcp`, `Origin`), and the
review origin line in `internal/agent/tool_registry.go`. Tests:
`internal/mcp/http_transport_test.go`, `internal/agent/mcp_http_test.go`,
`internal/tui/mcp_http_test.go`.

Deviations from the drafting defaults, each chosen to keep existing
configs working or to keep secrets safer:

- **`type` is checked only on `url` entries.** A stdio entry's `type` (and
  any `headers`) stays ignored, as before this feature, so pasted Claude
  Code entries with `"type": "stdio"` keep loading.
- **Redaction runs before the 2 KiB cut**, not after, so a cut never leaves
  part of a secret behind.
- **Redaction scope.** Every configured header value (literal or
  substituted) is redacted, plus each whitespace-separated part of 8 bytes
  or more (a server may echo the token without `Bearer`). Values shorter
  than 4 bytes are not redacted: they are not secret-shaped and replacing
  them would mangle ordinary text. A tool result is redacted too; if the
  redacted result is no longer valid JSON, the call fails instead of
  returning the unredacted text.
- **Wording.** A failed HTTP server reads `failed (…)` in `/mcp`; stdio
  servers keep `crashed (…)`, and the status-line suffix stays `crashed`
  for both. A connection that drops mid-call reads `the connection
  dropped`, distinct from `could not connect`.
- **Cancellation notice is HTTP-only.** `notifications/cancelled` is sent
  for HTTP calls (in the background, bounded by 2 s, awaited by `Close`);
  stdio cancellation behaves exactly as before.

Not verified: no real remote MCP server (T16) and no real-TUI walkthrough.
