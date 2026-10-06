# Context: Repository init (`/init`)

Code, tests, and specs this feature touches.

## Code

| Path | Relevance |
| --- | --- |
| `internal/agent/init.go` | `InitPrompt(guidance string) string`: builds the survey prompt from the embedded markdown and appends the "User guidance" section. |
| `internal/agent/init_prompt.md` | Embedded (go:embed) survey instructions: sources to read, improve-or-create rules, content limits, root-only, finish with `edit_file`. |
| `internal/agent/tool_registry.go` | `RunOptions.InitMode`; init-mode gating in `newToolRegistry`'s register step (MCP, Exec, Write except `edit_file`); `edit_file` root-`AGENTS.md` restriction and the 32 KiB review warning. |
| `internal/agent/turn_execution.go` | Init-mode instructions alongside `planModeGates` / `planModeInstructions`. |
| `internal/agent/harness.go` | `maxRootInstructionsBytes` (32 KiB) and per-turn root `AGENTS.md` loading; the warning reuses the cap. |
| `internal/tui/init_command.go` | `startInit`: the refusals (no provider, review pending, run active, plan mode), setting the one-run `initRun` flag that becomes `InitMode`; `initNoProposalNote` wording; `isRootAgentsEdit` root-`AGENTS.md` review matcher. |
| `internal/tui/dialog.go` | `handleCommand` only dispatches `/init` to `startInit`; `commandHelp` lists `/init`. |
| `internal/tui/tui.go` | `startTurnDisplay` (short transcript line, full prompt in history, `displayPrompts` map), `approval` event sets `initProposed`, `finishRun` appends the no-proposal note and clears the init flags. |
| `internal/tui/status_line.go` | Status title uses `displayPrompts` so the `/init` run shows the short command, not the full survey prompt. |
| `internal/tui/tool_wiring.go` | `toolRunOptions` passes `InitMode` for the `/init` turn only. |
| `internal/tui/commandcomplete.go` | `/` autocomplete entry for `/init`. |

## Tests

| Path | Covers |
| --- | --- |
| `internal/agent/init_mode_test.go` | Init-mode tool gating and refusals, `AGENTS.md`-only edits, 32 KiB warning, `InitPrompt` content and guidance section. |
| `internal/tui/init_command_test.go` | `/init` refusals, short transcript line vs. full history prompt, one-turn `InitMode`, no-proposal note, `/help` and autocomplete. |

Run with `go test ./internal/agent/... ./internal/tui/...`, or `go test ./...`.

## Related specs and requirements

- [v1-spec.md](../v1-spec.md): FR-03 (reserved commands, amended with
  `/init`), FR-05 (read confinement), FR-06/FR-07 (edit approval, stale
  proposals), FR-09 (failure reporting), FR-10 (no approval replay on
  resume), FR-21 (commands inactive during a run), FR-32 (plan mode).
- [agent-harness](../agent-harness/spec.md): per-turn root `AGENTS.md`
  loading and the 32 KiB cap.
- [plan-mode](../plan-mode/spec.md): the gating pattern init mode reuses.
- [slash-commands](../slash-commands/spec.md): command interception, `//`
  escape, inertness.
- [conversation-compaction](../conversation-compaction/spec.md): `/compact`
  refusal wording and test patterns.
- [markdown-skills](../markdown-skills/spec.md): `/skill`, the precedent for a
  command that starts one turn in the current conversation.

## Precedent

- Claude Code `/init`: surveys the repository and writes `CLAUDE.md` at the
  root. Likha uses the neutral `AGENTS.md` name, keeps the write behind the
  normal diff review, and restricts the turn to read-only tools plus that one
  edit.
