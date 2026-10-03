# Context: plan mode (`/plan`)

Code paths and precedent this feature touches. No code changes yet
(spec is the deliverable).

## Code paths to touch at implementation time

- `internal/app/agent.go` — `dispatchTool` (line ~119) is the single checkout
  for every tool call: reads (`read`, `read`, `grep`),
  mutation tools (`edit_file`, `run_command`), and the MCP fallback
  (`mcpManager.Call`). The plan-mode gate goes before the mutation cases so
  refusals return as failed tool results (FR-09 reporting) instead of
  reaching `requestApproval`. Note the history/system message construction
  in the same file (`history` slice built around line 43-45): that is where
  the read-only system message is injected while the mode is active — Likha
  currently sends no system role message, so plan mode introduces it.
- `internal/app/tui.go` — status line state (field `status`, rendered in
  `mainView` around line 1300 as `fmt.Sprintf("%s | Page %d/%d", ...)`);
  the plan-mode indicator is composed into that same footer row. The slash
  command list lives elsewhere in the file in `handleCommand` (`case "help":`
  etc.); `/plan` joins that dispatch, and `/help`'s `commandHelp` text
  mentions the new command.
- `internal/app/tui.go` — input dispatch path: `m.input` -> `handleCommand`
  for the leading-slash case, else `startTurn`. Plan mode state lives on the
  `ui` struct next to `status`/`mode`, toggled by `/plan` only when no run
  is active and no approval is pending (matches prompt inertness in FR-17).

## Tests that will pin the behavior

- Repository-read + refusal cases in `internal/app/agent`'s existing tests
  (the suite that already covers `dispatchTool`, approval, and cancellation).
- A TUI-level test for the `/plan` command: state toggles, footer carries
  the marker, refusal note in the conversation view, and exited mode
  restores the normal review flow.

## Spec/precedent links only

- [v1-spec.md](../v1-spec.md) — FR-03 (reserved `/commands` acting on the
  app, not the conversation), FR-05 (repository reads without per-call
  approval), FR-06/07/08 (edit and command approvals; no blanket session
  permission), FR-09 (failed/refused actions reported accurately to user
  and model), FR-10 (resume never replays pending permissions), FR-16 (MCP
  first-call approval then session trust — the reason plan mode's MCP
  stance is a decision rather than a given).
- Precedent: Claude Code plan mode (Shift+Tab) — a read-only survey mode
  that presents a plan for approval before any mutation. Likha's version is
  a per-session toggle plus a gate on dispatched tool calls, not new
  execution machinery.
- `specs/slash-commands/` — the existing reserved-command extension point
  (`/plan` joins the same list), if it is still present when this feature
  is implemented.
