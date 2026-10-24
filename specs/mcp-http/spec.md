# Feature: MCP over HTTP (Streamable HTTP servers in `mcp.json`)

**Status:** planned — decisions recorded 2026-10-07 (user interview); no
implementation started. Requirement: [v1-spec FR-46](../v1-spec.md#attention-and-automation-amendment-user-approved-2026-10-07).
Refines [mcp-support](../mcp-support/spec.md) (FR-16) and
[approve-always](../approve-always/spec.md); never contradicts them.

## Context

Likha talks to MCP servers only over stdio: every `mcp.json` entry must
name a `command`, and Likha launches it as a child process
([mcp-support](../mcp-support/spec.md) decision 1). Many MCP servers are
now hosted services reached at a URL, using the Streamable HTTP transport
that replaced the older HTTP+SSE transport in MCP protocol 2025-03-26.
Claude Code, OpenCode V2 and OMP all accept such servers; Likha cannot, so
a user who pastes a URL-based entry from another harness gets
`server "<name>" must have a command` at startup.

Hosted servers usually expect a bearer token. Writing the token into
`mcp.json` works, since the file is private (0600), but users often keep
tokens in environment variables and other harnesses let a config refer to
them.

## Role

Let the user add a Streamable HTTP MCP server to the same `mcp.json`, with
the same trust, approval, `/mcp`, crash and exit behavior a stdio server
has. Keep secrets in headers out of everything Likha shows or stores. Never
send a token over plain HTTP to another machine.

## Scope

In scope: Streamable HTTP servers configured in `<stateDir>/mcp.json` with
a `url` and optional static `headers`, `${NAME}` environment substitution
in header values, startup and failure reporting, `/mcp`, the status-line
`mcp` segment, tool calls, cancellation, and exit.

Out of scope:

- OAuth (authorization discovery, PKCE, dynamic client registration,
  token refresh). A server that requires it fails with a message saying
  Likha does not support OAuth yet. A later spec covers it.
- The deprecated HTTP+SSE transport (protocol 2024-11-05). An entry marked
  as SSE is refused with a named reason; Likha does not probe for it.
- MCP resources, prompts, sampling, roots and elicitation, as for stdio
  (mcp-support decision 5).
- Per-server timeouts, persistent trust, per-repository `mcp.json`, client
  certificates, and custom CA files.
- Picking up a server's changed tool list during a session.
- Resuming a dropped response stream from where it broke.

## Decisions (user interview, 2026-10-07)

1. **Streamable HTTP joins stdio.** stdio servers behave exactly as today.
2. **Same file, same shape.** An HTTP server is an `mcpServers` entry with
   a `url` instead of a `command`, so configs from other harnesses paste in.
3. **Static headers with `${NAME}` substitution.** Header values may refer
   to environment variables, which is how bearer tokens are supplied.
4. **HTTPS only, except loopback.** Plain `http://` is accepted only for a
   loopback host.
5. **OAuth is deferred** to a later spec.
6. **Legacy HTTP+SSE is out of scope.**
7. **Trust and approval are unchanged.** Trust-on-first-use, **Approve** /
   **Approve always**, plan-mode refusal, `/tools` identities, `/mcp`, and
   the tool review behave exactly as for stdio servers.

## Functional changes

### Configuration

1. An `mcpServers` entry with a `url` is an HTTP server. An entry with a
   `command` is a stdio server, as today. An entry with both, or neither,
   is an invalid `mcp.json`: Likha fails at startup as it does for any
   invalid `mcp.json`, naming the server and the problem.
   Example:

   ```json
   {
     "mcpServers": {
       "docs": {
         "url": "https://mcp.example.com/mcp",
         "headers": { "Authorization": "Bearer ${DOCS_MCP_TOKEN}" }
       }
     }
   }
   ```

2. The `url` must be an absolute `https://` URL, or an `http://` URL whose
   host is `localhost`, an address in `127.0.0.0/8`, or `::1`. Any other
   scheme, a plain `http://` URL to another host, or a URL carrying a user
   name or password is invalid, and the error says why
   (`server "docs": plain http is only allowed for localhost; use https`).
3. `headers` is an optional map of header names to string values. A value
   may contain `${NAME}`, which is replaced with the environment variable
   `NAME` from Likha's environment when the server starts. Other text is
   kept as written.
4. If a referenced variable is unset or empty, the server does not start.
   `/mcp` shows it as failed with
   `environment variable DOCS_MCP_TOKEN is not set`. Likha never sends a
   header with the reference left in or with an empty value.
5. A header value cannot refer to Likha's own provider keys
   (`LIKHA_API_KEY`, `LIKHA_*_API_KEY`). Such an entry is invalid, with a
   reason naming the rule, so a model-provider key never reaches an MCP
   server (the same rule that keeps these variables out of commands,
   FR-08).
6. `command`, `args` and `env` keep their meaning for stdio entries. On an
   HTTP entry, `args` or `env` is invalid, with the reason
   `args and env apply only to stdio servers`.

### Starting and failing

7. An HTTP server starts when a stdio server would: lazily, at the first
   prompt. Starting means connecting and completing the MCP handshake. No
   tool call is made before the user approves one.
8. A server that cannot start shows as failed in `/mcp` with a reason in
   plain words, and stays failed until relaunch, as a crashed stdio server
   does. The reasons distinguish:
   - could not connect (with the network reason);
   - certificate not trusted;
   - rejected the credentials (HTTP 401 or 403). If the server asks for
     OAuth, the reason adds `OAuth sign-in is not supported yet`;
   - not an MCP endpoint (for example an HTML page or HTTP 404/405 on the
     handshake);
   - speaks another MCP protocol version (same wording as stdio);
   - an unset environment variable (point 4).
9. A failed server never blocks the TUI or the other servers, and its tools
   are absent, as for stdio.

### Calls

10. A tool call on an HTTP server shows the same review, needs the same
    approval, and returns its result to the model the same way as a stdio
    call. The 60-second limit per call applies.
11. If a single call fails because the connection dropped, timed out, or
    the server answered with an error status, that call returns a visible
    failed tool result naming the server and the cause, and the model
    continues. The server stays available for later calls; a network blip
    does not mark it failed for the session.
12. Esc during a call cancels the run as it does today. Likha tells the
    server the request was cancelled. Cancelling does not undo work the
    server already did, as for stdio.
13. Plan mode refuses every HTTP MCP call before any request is sent, as
    for stdio (FR-32).

### What the user sees

14. `/mcp` lists HTTP servers with stdio ones, marked `http` and showing
    the server's origin (`https://mcp.example.com`), its state, and its
    tools. The URL path and query and every header value are never shown.
15. The status-line `mcp` segment counts HTTP servers the same way it
    counts stdio servers.
16. Header values, including substituted tokens, never appear in `/mcp`,
    errors, tool results, the transcript, the session store, or logs.
    Server error text that echoes a header value has it removed.
17. When `mcp.json` has no servers, the `/mcp` hint shows both entry
    shapes, `command` and `url`.

### Exit

18. On exit Likha tells each HTTP server it is ending the session and does
    not wait more than a moment for an answer. Exit is never blocked by an
    unreachable server.

## Acceptance criteria

- A `url` entry for a server that answers with plain JSON responses starts,
  lists its tools in `/mcp`, and runs a tool after **Approve**.
- The same works for a server that answers each request with an SSE stream.
- **Approve always** trusts an HTTP server for the session; trust clears on
  session switch and relaunch, as for stdio.
- `Authorization: Bearer ${TOKEN}` sends the variable's value; with
  `TOKEN` unset, the server shows as failed naming `TOKEN` and receives no
  request.
- A header referring to `LIKHA_API_KEY` makes `mcp.json` invalid.
- `http://example.com/mcp` is refused at startup; `http://127.0.0.1:8080/mcp`
  and `http://localhost:8080/mcp` are accepted.
- 401, a bad certificate, an HTML page, and an unreachable host each fail
  with their own reason, and the TUI stays usable.
- A dropped connection during one call fails that call only; the next call
  on the same server succeeds.
- No header value appears in `/mcp`, error text, the transcript, or the
  session database, checked with a token the test server echoes back.
- `/plan` refuses an HTTP MCP call without sending any HTTP request.
- Exit with an unreachable HTTP server configured finishes promptly.
- Existing stdio behavior and tests are unchanged.

## Drafting defaults (confirm in review)

These were not part of the user interview. They follow the MCP transport
rules ([2025-03-26](https://modelcontextprotocol.io/specification/2025-03-26/basic/transports),
[2025-06-18](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports))
and Likha's existing conventions.

- **Protocol version.** Likha asks for `2025-03-26` in the handshake, as
  for stdio, and keeps refusing a server that answers with another version.
  Later requests carry `MCP-Protocol-Version: 2025-03-26`.
- **`type` field.** An optional `"type"` of `"http"` or
  `"streamable-http"` is accepted for pasted configs; `"sse"` is refused as
  legacy HTTP+SSE (out of scope); any other value is invalid.
- **Session ID.** If the handshake response sets `Mcp-Session-Id`, every
  later request carries it. If the server answers 404 to a request carrying
  it, Likha re-runs the handshake once. The call that hit the 404 returns a
  failed result saying the server session expired and the call was not
  retried (a resend could repeat a side effect); later calls use the new
  session. A changed tool list is not picked up until relaunch.
- **Response streams.** Likha accepts `application/json` and
  `text/event-stream` responses. It does not open the optional GET stream
  for server-initiated messages, and does not resume a broken stream with
  `Last-Event-ID`.
- **Server-initiated requests** arriving on a response stream (sampling,
  roots, elicitation) get a JSON-RPC "method not found" error, so the
  server is not left waiting. Notifications are ignored, as for stdio.
- **Timeouts.** Connecting and the handshake share the existing 60-second
  limit. Each tool call keeps 60 seconds. The exit DELETE waits at most
  2 seconds.
- **Cancellation.** Esc sends `notifications/cancelled` for the request
  (best effort, bounded) and stops waiting.
- **Redirects** are not followed. A redirect fails the request with
  `server redirected to <origin>; update the url`.
- **Response size.** One response larger than 16 MiB fails that request,
  matching the model stream cap.
- **Proxies.** The standard `HTTPS_PROXY`/`NO_PROXY` environment variables
  apply, as they do for model requests.
- **Review origin line.** The first-call review adds the server's origin
  (`remote MCP server https://mcp.example.com`) so the user sees that
  arguments leave the machine.
- **Error text** from a server is bounded to 2 KiB and passes through
  header-value redaction before display.
