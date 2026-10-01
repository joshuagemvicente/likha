# Spec: Plan mode (`/plan`) — a read-only agent mode

**Status:** draft

## Context

Lisa already separates repository reads from gated writes. In
`internal/app/agent.go`, `dispatchTool` executes `read`, `read`, and
`grep` directly (repository-scoped per
[v1-spec.md](../v1-spec.md) FR-05), while `edit_file`, `run_command`, and MCP
tool calls pass through the approval coordinator (FR-06/07/08) before anything
changes the machine. Plan mode therefore needs no new execution machinery: it
is a gate on the existing dispatch, plus a way to tell the model what the
current session's mode is.

The precedent is Claude Code's plan mode (Shift+Tab): the agent surveys the
codebase with read-only tools, then presents a plan in text for the user to
approve before any mutation happens. Lisa's equivalent makes that a one-shot,
per-session mode the user toggles from the prompt.

Why it is worth having: a user who wants to explore "what would you change?" —
before trusting the agent with a diff — currently has no mode that stops the
agent from proposing (and after approval, applying) edits in the same turn.

## User-visible behavior

1. Typing `/plan` at the prompt toggles plan mode **for the current session
   only**. `/plan` joins the reserved command list (FR-03): it acts on the
   application, is never sent to the model, and the existing FR-03 rules
   apply unchanged — unknown-command errors still name `/help`'s list, and
   `//plan` still escapes to a literal `/plan` sent as a prompt. Typing
   `/plan` while already in (or out of) the mode flips it. No configuration
   file, session record, or launch flag persists the mode; a restarted or
   resumed session always starts in normal mode.

2. The status footer (the line that already shows connection state, page
   position, and run state in the TUI footer) displays an unmissable,
   non-flashy indicator while plan mode is active — a persistent text marker
   in the status line styling, not a blinking or modal element. It remains
   in place during streaming, tool activity, reviews, and paging so the user
   never loses sight of the mode.

3. While plan mode is active, every proposed mutation is **refused before the
   approval flow starts**: an `edit_file` or `run_command` tool call returns a
   refusal result to the model and Lisa reports it to the user. A refusal is a
   failed/refused action recorded and reported per FR-09 — it is never
   silently dropped, never counted as success, and never surfaced as a diff
   review for approval. The refusal names the mode, so the user's status
   history reads `edit refused (plan mode)` / `command refused (plan mode)`
   rather than an opaque error.

4. Plan mode does **not** change read behavior. `read`, `read`, and
   `grep` run exactly as in normal mode under the same FR-05
   repository-scoping rules, so the agent can still survey the codebase.

5. MCP tool calls in plan mode: **open decision — mark as decision.** The safe
   stance blocks MCP tool calls entirely while plan mode is active: an MCP
   server is a separate process on the user's machine, its tools may carry
   arbitrary side effects that Lisa's approval gate cannot see ahead of time
   (FR-16 only gates the first call per server, then session-trusts the
   rest), so "approval-gated like a command" does not make an MCP call a read.
   The alternative — allowing already-trusted MCP servers to run as in normal
   mode — would let side effects through without any review. Resolve this
   before implementation; the safe default (`plan mode blocks all MCP tool
   calls with the same refusal reporting as mutation tools`) is preferred.

6. The model is told it is in a read-only mode. Lisa currently sends no
   system role message, so plan mode **adds one** for the duration of the
   mode; how that message is composed and whether it is applied per-prompt or
   per-run is an implementation detail (see tasks), but the user-visible
   consequence is fixed: the agent answers in text — surveying the repo,
   describing what it would change — instead of attempting edits that can only
   fail. The system message leave plan mode behavior matches re-entering
   normal mode: the mode is slot-replaced in the outgoing history, never
   persisted by the session store.

7. Exiting plan mode (typing `/plan` again) resumes exactly the
   approval-gated behavior Lisa already has: every edit and command proposes
   to the user as before, there is no blanket permission and no backdoor
   write path (FR-06/07/08 unchanged). The toggle works in both directions
   while no run is active; while an agent run is executing or an approval is
   pending, the `/plan` line is ignored with a visible note (plan mode
   changes tool dispatch at turn boundaries, not mid-run, and prompt editing
   is already inert during those states).

8. Sessions started in one mode can still be resumed in any other session
   (FR-10); plan mode is a live TUI state, not part of the conversation
   history, and a resumed session never inherits it.

## Acceptance criteria

- [ ] `/plan` toggles read-only mode on then off within the same session;
      a new or resumed Lisa session is never still in plan mode.
- [ ] While a session is in plan mode, the status footer carries a mode
      marker on every rendered frame, including during streaming, reviews,
      and paging.
- [ ] In plan mode, an agent attempt to edit a file produces a visible
      `refused (plan mode)` entry in the conversation; no approval prompt is
      offered and no file changes.
- [ ] In plan mode, an agent attempt to run a shell command produces a
      visible `refused (plan mode)` entry; no command executes.
- [ ] In plan mode, the model still successfully lists, reads, and searches
      repository files, and the user sees those tool calls report normally.
- [ ] MCP tool calls behave per the resolved decision, and the chosen
      behavior is visible in the conversation the same way refusals are
      (never silently dropped).
- [ ] After `/plan` is toggled back off, an edit proposal again shows its
      diff and requires explicit approval before applying; the refusal path
      is gone entirely.
- [ ] No path in plan mode writes to disk, runs a shell command, or calls an
      MCP server whose behavior would not be exactly the same without plan
      mode toggled on.
