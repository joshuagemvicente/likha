# Tasks: Hooks

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md),
and code paths in [context.md](context.md). Each milestone leaves
`go test ./...` and `go vet ./...` green, and can ship on its own. Confirm
the drafting defaults (D1–D12) in review before M1 starts.

**Progress (2026-10-07):** M1–M3 implemented (local). T13 docs done
(`docs/usage.md` Hooks section, `docs/tools.md` pointer); its CHANGELOG
entry is added when the branch merges. T12 done after FR-44 merged
(`internal/headless/hooks.go`). W1 is not done. Implementation notes: the gate is `tools.Gate`
(`Registry.SetGate`); children get it through the new last parameter of
`ExploreRunner` and `ProfileRunner`; `post_tool` leaves the agent as a
`hook_observe` turn event that the interface fires off the run; the TUI's
`attentionMoment` (`internal/tui/hooks.go`) is the single point FR-41's bell
and OSC 9 should join.

Fixed values (drafting defaults D3, D6), defined once in `internal/hooks`:

| Name | Value |
| --- | --- |
| default hook timeout | 10s |
| maximum hook timeout | 60s |
| stdout / stderr read limit | 64 KiB each |
| block reason shown and sent | 1 KiB |
| `post_tool` result content | 16 KiB |
| payload `version` | 1 |

## M1 — Config and runner (`internal/hooks`, new leaf package)

- [x] **T1. Package and imports.** Create `internal/hooks`, importing only
      the standard library and `internal/actions` (for `CommandEnv`). It must
      not import `agent`, `tui`, `tools` or `providers`.
      *Verify:* `go list -deps ./internal/hooks` shows no project packages
      except `actions` and its dependencies.
- [x] **T2. Load `hooks.json`.** `Load(stateDir) (Config, error)`: a missing
      file gives an empty config and no error. Reject the whole file on
      invalid JSON, an unknown event, an empty `command`, an invalid `match`
      glob (`path.Match` syntax check), a timeout outside 1–60, an
      `on_error` other than `block`/`allow`, or file mode bits writable by
      group or others. Errors wrap with the file path and the entry
      (`hooks.json: pre_tool[1]: …`). Never create the file.
      *Verify:* table test over each rejection, plus a missing file and a
      valid file.
- [x] **T3. Run one hook.** `sh -c <command>` in the repository root, with
      `actions.CommandEnv(os.Environ())` plus `LIKHA_HOOK_EVENT` and
      `LIKHA_SESSION_ID`. Write the JSON payload to stdin, then close it. Use
      its own process group, killed on timeout or cancel, with `WaitDelay`.
      Bounded stdout/stderr readers discard past 64 KiB. Return a typed
      `Outcome{Block bool, Reason string, Warning string, Err error}`.
      *Verify:* a test hook that exits 2 with a reason; one that prints a
      block decision; one that prints `allow` (no block, warning); one that
      sleeps past a 1s test timeout (killed, error); one that writes 1 MiB
      (bounded); an env check that `LIKHA_API_KEY` is absent.
- [x] **T4. Run an event.** `Runner.Fire(ctx, event, payload) Outcome` runs
      matching hooks in file order and stops at the first block. A failure
      on a blocking event blocks unless that hook has `on_error: allow`
      (D4). Observe events never block. `Runner` is safe for concurrent use
      (parallel tool calls).
      *Verify:* ordering test, first block stops later hooks, fail-closed
      and fail-open, and an observe-event failure that does not block.

## M2 — Tool gate (`internal/tools`, `internal/agent`)

- [x] **T5. Registry gate.** Add an optional consumer-side `Gate`
      interface to `tools.Registry`. `Invoke` calls `BeforeTool` after schema
      validation and before `Authorize`. A non-nil error returns
      `callFailure(..., Refused, "hook_blocked", "Blocked by hook \"<name>\": <reason>", err)`
      and `Run` and `Authorize` are never called. Call `AfterTool` after the
      final status is set, without changing the result.
      *Verify:* registry tests showing that a blocked call never runs
      `Authorize` or `Run`, that a nil gate leaves behavior identical, and
      that `AfterTool` sees the final status.
- [x] **T6. Agent adapter.** In `internal/agent`, adapt `hooks.Runner` to
      `tools.Gate`: build the `pre_tool`/`post_tool` payload (canonical name,
      source, effects, validated arguments, agent identity from `Scope`,
      bounded result content). Add `RunOptions.Hooks` and wire it into every
      registry built by `newToolRegistry`, so the explore and profile child
      registries share it.
      *Verify:* agent test with a fake runner: a blocked `run_command`
      emits no `approval` event; a session-granted command is still blocked;
      an explore child's `read` is blocked and its payload names the child.
- [x] **T7. Transcript event.** Emit a `TurnEvent{Kind: "hook"}` for blocks
      and warnings. The TUI renders a blocked tool item with the hook name
      and reason, and one warning row per hook failure.
      *Verify:* TUI golden/view test for a blocked call and a warning, in
      ASCII mode and with no color.

## M3 — Prompt, session, run and attention events (`internal/tui`)

- [x] **T8. Load at launch.** The TUI loads `hooks.Load(stateDir)` once.
      A load error shows one launch error entry and disables all hooks.
      *Verify:* TUI test with an invalid file, which shows the error and
      fires no hook.
- [x] **T9. `prompt_submit`.** Before `startTurn` sends a typed, `/init` or
      `/install` prompt, fire `prompt_submit` in a `tea.Cmd` (never on the
      Update goroutine). A block keeps the draft and shows the notice. A
      steering prompt fires it at delivery, and a blocked one is dropped
      with a notice while the others stay queued.
      *Verify:* TUI tests: a blocked prompt sends no request and the draft
      is restored; a blocked steering prompt is dropped and the rest are
      delivered.
- [x] **T10. Observe events.** Fire `session_start` (new or resumed),
      `run_end` (completed/failed/cancelled, error text) and `attention`
      (FR-41 moments) asynchronously. A slow hook never delays the next
      request.
      *Verify:* a `run_end` hook that sleeps 5s (1s timeout in the test)
      does not delay a following turn; payload fields for all three
      outcomes.
- [x] **T11. `/hooks`.** A dialog listing the path, the events, names,
      matchers, timeouts, and each hook's last outcome and time, or the load
      error, or "No hooks configured". Allowed during an active run. Add it
      to `/help` and the autocomplete.
      *Verify:* TUI tests for each of the three states, plus the
      autocomplete entry.

## M4 — Headless and docs

- [x] **T12. Headless (FR-44, D11).** The headless runner loads hooks and
      fires `session_start`, `prompt_submit`, `pre_tool`, `post_tool` and
      `run_end`, but not `attention`. A `pre_tool` block in `--json` output
      is a `tool_result` with `status` `refused` whose content is
      `Blocked by hook "<name>": <reason>` (FR-44 schema 1 has no separate
      `refusal` event, so none was added). A blocked `prompt_submit` sends
      no request, saves nothing, and exits 1 with the block message as the
      run's error. Hook warnings and an unusable `hooks.json` are `notice`
      events (stderr `likha: note:` lines in text mode). Observe-only hooks
      run in the background and are awaited before the result is written.
      *Verified:* `internal/headless/hooks_test.go` (real hook commands) and
      `tests/integration` `TestHeadlessHooksBlockPromptAndTool` with the
      built binary and a temp `LIKHA_STATE_DIR`.
- [ ] **T13. Docs and changelog.** `docs/usage.md` hooks section (format,
      events, payload schema v1, exit codes, safety note from spec point 14).
      Add a `CHANGELOG.md` **Unreleased** entry stating what was verified.
- [ ] **W1. Real-terminal walkthrough.** A `.env` read block, a
      `run_command` allow attempt that still prompts, a `run_end` sound hook,
      and `/hooks` with an invalid file. Record the results in
      [checklist.md](checklist.md).
