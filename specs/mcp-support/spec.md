# Feature: MCP (Model Context Protocol) server support

**Status:** implemented (local). stdio transport with the 2025-03-26 handshake,
`mcpServers`-shaped config in the state dir, trust-on-first-use per server
(session-scoped), `/mcp` status view, crash surfacing, exit cleanup, and the
approval-flow integration (`MCP TOOL REVIEW` prompt) are covered by tests
running a real fake server as a child process.

Phase 1 qualified identities and common registry dispatch are implemented
locally. Existing MCP checks pass; new identity/conflict/schema walkthroughs
remain unverified. See [implementation evidence](../tooling-platform/implementation.md).

## Context

Likha is a BYOK agent harness (v1-spec.md §2/§5). To move from "toy" to
daily-driver, it needs the same extensibility surface top agent harnesses
have: MCP servers. An MCP server is a separate process/service the harness
talks to; its **tools** join Likha's built-in tool loop (`glob`, `read`, `grep`, `edit_file`, `run_command`) and go through the
same approval-gated execution path. Likha hosts no models and runs no MCP
servers of its own — servers come from the user's config.

This is a scope change: v1-spec §2 currently excludes plugins; MCP is the one
deliberate exception the user wants (config-driven, no plugin API surface).

## Existing behavior and resolved decisions

1. **Transport:** stdio only for v1 — launch each configured server as a
   child process (`command` + args + env), speak MCP over stdin/stdout
   (JSON-RPC 2.0, MCP protocol version negotiated at initialize). HTTP/SSE
   transports are deferred.
2. **Configuration:** `<stateDir>/mcp.json` (private directory, 0600 file like
   `providers.json`/`config.json`; never the repository) — shape follows the
   familiar convention:
   `{"mcpServers": {"name": {"command": "...", "args": [...], "env": {...}}}}`.
   Manual edit + `/mcp` status view; no TUI editor in v1.
3. **Lifecycle:** servers start lazily at first prompt (or on `/mcp refresh`)
   and are killed on exit. A crashed server surfaces as a visible failed tool
   result, never silently swallowed.
4. **Security posture:** first use of each server requires approval showing
   server, tool, arguments, and server-wide scope. Approval trusts that server
   for the running manager's lifetime, resetting on app relaunch. Later calls
   stay visible but do not prompt again. Configuration authorizes starting the
   server process for discovery before first tool-call approval; TOFU is not
   process/network sandboxing. Document inherited/configured environments as
   potentially carrying secrets rather than promising an empty environment.
5. **Capabilities:** tools only for v1. Resources and prompts are deferred.
   Tool results are returned to the model as tool messages, same as built-ins.
6. **UI:** `/mcp` lists configured servers with state (running/crashed/
   disabled) and their tools. No in-TUI editor; adding a server = editing
   `mcp.json` (documented).

## Open questions — resolved (2026-09-29, user decisions)

1. Config shape: **Claude-Desktop-style `mcpServers` map** — configs from other
   harnesses can be pasted verbatim.
2. Approval: **trust per server after first approval** — the first tool call
   from a server shows server + tool identity and requires approval; once
   approved, further calls from that server run for the rest of the session.
   Trust is **session-scoped**: it resets on relaunch (a deliberate safety
   default; persistent trust would be a later feature).
3. Config location: **state dir only** (`<stateDir>/mcp.json`, 0600). No
   per-repository overrides in v1.
4. Timeout per tool call: **60 s default** (context deadline), not
   per-server-configurable in v1.
5. Crash recovery: **relaunch-only restart** — a crashed server stays dead for
   the session; `/mcp` shows its state, no in-session restart.

## Functional changes (v1-spec.md, when implemented)

- **§2 In scope** gains MCP server tools; the "plugins" exclusion stays but is
  rewritten to "no generic plugin API — MCP servers are the extension surface."
- **FR-16:** The user can configure MCP servers over stdio; their tools join
  the agent's tool set. First approval grants server-wide trust until relaunch;
  calls retain server/tool identity and failures remain visible.

## Existing acceptance criteria

- [ ] A configured stdio MCP server's tools appear to the model with correct
      names/descriptions/schemas (initialize handshake verified).
- [ ] First tool use shows server/tool/arguments and trust scope; rejection
      prevents that call. Later calls from the approved server do not prompt;
      relaunch clears trust. Approval does not constrain server startup effects.
- [ ] A crashed or hung server produces a clear failed tool result; the TUI
      stays usable and other tools keep working.
- [ ] `/mcp` shows configured servers and their state; killed on exit, no
      orphaned children.
- [ ] mcp.json is 0600 in the private state directory; invalid config fails
      loudly at startup without crashing the TUI.
- [ ] Built-in tools and approval flow are unchanged.

## Planned Phase 1 enhancement: names and registry

**Enhancement status:** planned under FR-24; baseline status remains
implemented (local). Route configured tools through the
[registry](../tool-registry/spec.md). Qualify each model name with server
identity, retain original names in human views, reject post-qualification
collisions, and allow old raw-name resolution only when unambiguous. Saved
history keeps original identities and never redispatches calls on resume.

Keep stdio, timeout, crash behavior, and current in-memory trust scope. Use the
common authorization path to consult server trust; do not add a second bypass.
Plan mode blocks every MCP tool even when trusted; explore never receives MCP.
MCP cancellation may stop local waiting without stopping remote server work;
show that limitation and avoid claiming undone side effects. New transports,
persistent trust, and generic executable plugins remain deferred.
