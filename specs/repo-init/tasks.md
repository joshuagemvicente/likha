# Tasks: Repository init (`/init`)

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code and test locations in [context.md](context.md).

- [x] **1. Amend FR-03.** Add `/init` to the reserved command list in
      [v1-spec.md](../v1-spec.md) and to the README command reference.
      **Verify:** FR-03 lists `/init`; README's slash-command table has a
      `/init [guidance]` row.
- [x] **2. Agent init mode.** Add `RunOptions.InitMode` (after `PlanMode` in
      `internal/agent/tool_registry.go`). In the registry's register step,
      gate every MCP tool, every Exec tool (`run_command`), and every Write
      tool except `edit_file` with an `UnavailableReason` naming `/init`, so
      they are hidden from model requests and refuse if called. Add init-mode
      instructions next to `planModeInstructions` in
      `internal/agent/turn_execution.go`.
      **Verify:** a test builds the registry with `InitMode` and checks the
      model tool list and refusal results.
- [x] **3. `AGENTS.md`-only edits.** In init mode, `edit_file` cleans the
      proposed path and refuses anything other than `AGENTS.md` with an
      error result before `requestApproval`, so no review is shown.
      **Verify:** tests for `AGENTS.md`, `./AGENTS.md` (review shown) and
      `src/x.go`, `docs/AGENTS.md`, `../AGENTS.md` (error, no approval event).
- [x] **4. 32 KiB review warning.** When any `edit_file` proposal targets the
      root `AGENTS.md` with new content above `maxRootInstructionsBytes`
      (`internal/agent/harness.go`), set `ApprovalRequest.Warning` to say the
      harness would ignore the file because it exceeds the 32 KiB
      root-instructions limit. Applies in every turn, not only `/init`.
      **Verify:** a test with a >32 KiB proposal sees the warning; a small
      proposal and a non-root path do not.
- [x] **5. Embedded survey prompt.** Write `internal/agent/init_prompt.md`
      (go:embed) with the survey rules from spec.md item 5;
      `InitPrompt(guidance)` in `internal/agent/init.go` returns it, appending
      a "User guidance" section only for non-empty, trimmed guidance.
      **Verify:** tests check key instructions are present and that guidance
      appears only when given.
- [x] **6. TUI `/init` command.** Handle `/init [guidance]` in
      `handleCommand` (`internal/tui/dialog.go`): refuse visibly for no
      client, plan mode, active run, or pending review; otherwise start one
      turn whose transcript line is the typed command while `m.history`
      receives `agent.InitPrompt(guidance)`. Set `InitMode` for that turn
      only through `toolRunOptions` (`internal/tui/tool_wiring.go`) and clear
      it when the run finishes.
      **Verify:** tests for each refusal (no request made), the short
      transcript line, the full prompt in history, and `InitMode` false on
      the next turn.
- [x] **7. No-proposal note.** Track whether the `/init` turn emitted an
      approval for `AGENTS.md` (the `approval` event handling in
      `internal/tui/tui.go`); in `finishRun`, append the Likha note when it did
      not, for done, error, and cancel endings.
      **Verify:** tests for each ending without a proposal (note shown) and a
      rejected proposal (no note).
- [x] **8. Discoverability.** Add `/init [guidance]` to `commandHelp` in
      `internal/tui/dialog.go` and to the autocomplete list in
      `internal/tui/commandcomplete.go`.
      **Verify:** tests check `/help` output and the `/in` autocomplete match.
- [x] **9. Tests.** `internal/agent/init_mode_test.go` and
      `internal/tui/init_command_test.go` cover tasks 2–8 with real
      assertions (no skipped or always-passing tests), following the
      `/compact` test patterns in `internal/tui/tui_test.go`.
      **Verify:** `go build ./...` and `go test ./internal/agent/...
      ./internal/tui/...` pass.
- [x] **10. Docs.** Update this folder, [specs/README.md](../README.md)'s
      feature index, [slash-commands](../slash-commands/spec.md), README, and
      CHANGELOG.
      **Verify:** each lists `/init` consistently with spec.md.
