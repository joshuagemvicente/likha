# Spec: Plan mode (`/plan`) — a read-only agent mode

**Status:** planned. **Phase:** 3. **Product requirement:** FR-32.

## Context

Users can survey a change without opening a mutation review. Phase 1 supplies
the compiled harness and common [tool policy](../tool-registry/spec.md); this
feature adds a live permission mode at main-run boundaries. It is distinct
from the persisted [plan/todo checklist](../plan-todo/spec.md), whose state
does not enable this mode, execute steps, or grant permissions.

Competitor plan modes have different shell/scratch-space behavior. Likha's
chosen boundary refuses workspace edits, all shell, and all MCP. See the
[CLI comparison](../tooling-landscape/cli-workflows.md), not an assumption that
another product's mode is an OS read-only sandbox.

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
   approval flow starts**: an `edit`, `edit_file`, or `run_command` call returns a
   refusal result to the model and Likha reports it to the user. A refusal is a
   failed/refused action recorded and reported per FR-09 — it is never
   silently dropped, never counted as success, and never surfaced as a diff
   review for approval. The refusal names the mode, so the user's status
   history reads `edit refused (plan mode)` / `command refused (plan mode)`
   rather than an opaque error.

4. Plan mode does **not** change read behavior. `glob`, `read`, and
   `grep` run exactly as in normal mode under the same FR-05
   repository-scoping rules, so the agent can still survey the codebase.

5. **Resolved 2026-10-03:** block every MCP call before approval/dispatch,
   including a trusted server's tools. Return `refused (plan mode)` with source
   identity. Arbitrary server annotations do not establish side-effect freedom.
   This gate concerns tool calls; it does not sandbox or undo a configured
   server process's startup or work already running outside this mode.

6. State the mode in the existing single harness system layer; do not add a
   second system prompt or persist runtime mode instructions. Allow bounded
   read-only explore, main questions, checklist updates, passive skills, and
   session-owned output inspection. Optional web uses its unchanged explicit
   conversation-scoped network consent. These operations do not authorize repo
   writes. Dispatch enforces the mode even if the model requests a hidden tool.

7. Exiting plan mode (typing `/plan` again) resumes exactly the
   approval-gated behavior Likha already has: every edit and command proposes
   to the user as before, there is no blanket permission and no backdoor
   write path (FR-06/07/08 unchanged). The toggle works in both directions
   while no run is active; while an agent run is executing or an approval is
   pending, the `/plan` line is ignored with a visible note (plan mode
   changes tool dispatch at turn boundaries, not mid-run, and prompt editing
   remains usable for steering while running, but reserved commands remain
   inactive under FR-21).

8. Sessions started in one mode can still be resumed in any other session
   (FR-10); plan mode is a live TUI state, not part of the conversation
   history, and a resumed session never inherits it.

## Acceptance criteria

- [ ] `/plan` toggles read-only mode on then off within the same session;
      a new or resumed Likha session is never still in plan mode.
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
- [ ] All MCP calls refuse before execution/approval, including trusted tools;
      refusals identify mode, server, and tool.
- [ ] Explore, questions, checklist, passive skills, and output inspection
      retain their allowed ceilings; web still requires its own scoped consent.
- [ ] After `/plan` is toggled back off, an edit proposal again shows its
      diff and requires explicit approval before applying; the refusal path
      is gone entirely.
- [ ] Plan-mode dispatch performs no workspace write, shell execution, or MCP
      tool call. Private transcript/checklist/artifact persistence remains
      harness-owned state, not an exception that grants model write access.
