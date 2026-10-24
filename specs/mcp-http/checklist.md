# Checklist: MCP over HTTP

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Configuration

- [ ] An `mcpServers` entry with `url` starts as an HTTP server; one with
      `command` still starts as a stdio server.
- [ ] An entry with both `command` and `url`, or neither, fails startup
      naming the server.
- [ ] `https://` URLs are accepted; `http://` is accepted only for
      `localhost`, `127.0.0.0/8` and `::1`; anything else is refused with a
      reason.
- [ ] A URL with a user name or password is refused.
- [ ] `args` or `env` on an HTTP entry is refused.
- [ ] `${NAME}` in a header value is replaced from the environment.
- [ ] An unset or empty variable leaves the server failed, naming the
      variable, and no request is sent.
- [ ] A header referring to `LIKHA_API_KEY` or `LIKHA_*_API_KEY` makes
      `mcp.json` invalid.

## Starting and calls

- [ ] HTTP servers start lazily at the first prompt, like stdio servers.
- [ ] Start failures show distinct reasons: unreachable, untrusted
      certificate, rejected credentials (with the OAuth note when the
      server asks for OAuth), not an MCP endpoint, protocol mismatch, unset
      variable.
- [ ] A failed HTTP server does not block the TUI or other servers.
- [ ] Servers answering with JSON and servers answering with SSE both work.
- [ ] The first call shows the usual review; **Approve** runs one call,
      **Approve always** trusts the server for the session.
- [ ] Trust clears on session switch, deletion of the active session, and
      relaunch.
- [ ] A dropped connection fails only that call; later calls work.
- [ ] Each call is limited to 60 seconds.
- [ ] Esc cancels the run and the server receives a cancel notification.
- [ ] `/plan` refuses HTTP MCP calls with no HTTP request sent.

## What the user sees

- [ ] `/mcp` marks HTTP servers `http` with their origin, state and tools.
- [ ] No header value or URL path/query appears in `/mcp`.
- [ ] The status-line `mcp` segment counts HTTP servers.
- [ ] A token echoed by the server never appears in errors, results, the
      transcript, the session store, or logs.
- [ ] The empty-config `/mcp` hint shows both entry shapes.

## Exit and regressions

- [ ] Exit finishes promptly even when an HTTP server is unreachable.
- [ ] All existing stdio MCP tests pass unchanged.

## Verification record

- [ ] Automated suite green (`go test ./...`, `go vet ./...`, `-race` on
      `internal/mcp` and `internal/tui`).
- [ ] Live probe against a local Streamable HTTP server recorded.
- [ ] Live probe against a hosted HTTPS server with a bearer token recorded.
- [ ] Real-TUI walkthrough recorded.
