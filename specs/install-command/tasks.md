# Tasks: Install command (`/install`)

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code and test locations in [context.md](context.md). Depends on the
[ask-user questionnaire amendment](../ask-user/tasks.md) (tasks 5–8).

- [x] **1. Amend v1-spec.** FR-03 lists `/install`; FR-35 states the contract;
      README command reference gains a `/install <request>` row.
      **Verify:** FR-03 and FR-35 read as in spec.md; README row present.
- [x] **2. Embedded install prompt.** Write `internal/agent/install_prompt.md`
      (go:embed) with the detect → ask gaps → plan → execute → verify →
      summarize rules from spec.md items 4–10. `InstallPrompt(request)` in
      `internal/agent/install.go` appends the trimmed request under an
      "Install request" section. Amended: a Likha-detected "Environment"
      section (`DetectInstallEnv`: OS/arch, `$SHELL`, startup file names,
      package managers on PATH) precedes the request.
      **Verify:** tests check the key instructions and the request section.
- [x] **3. Install mode and plan gate.** Add `RunOptions.InstallMode` beside
      `InitMode` (`internal/agent/tool_registry.go`). While no `plan_update`
      call has succeeded in the turn, `run_command` calls whose
      `cmdpolicy.Classify` tier is not `ReadOnly`, and every Write tool, refuse
      before `requestApproval` with a result naming `/install` and the
      plan-first rule. MCP tools follow their normal rules.
      **Verify:** registry tests for ReadOnly allowed, Ask-tier refused, edit
      refused, both allowed after a successful `plan_update`, and no approval
      event for refused calls.
- [x] **4. TUI `/install` command.** `startInstall` in
      `internal/tui/install_command.go`, dispatched from `handleCommand`:
      refusals for empty request, no client, plan mode, active run, pending
      review or question; short transcript line, full prompt in history,
      `InstallMode` for that turn only, cleared in `finishRun`.
      **Verify:** tests per refusal (no request), transcript vs. history, and
      `InstallMode` false on the next turn.
- [x] **5. Discoverability.** `commandHelp` (`internal/tui/dialog.go`) and
      `internal/tui/commandcomplete.go`.
      **Verify:** `/help` output and the `/ins` autocomplete match.
- [ ] **6. Tests and probe.** `internal/agent/install_mode_test.go` and
      `internal/tui/install_command_test.go` with real assertions. The user runs
      the live `/install nvm posix` probe; record the outcome in
      `CHANGELOG.md`.
      **Status:** tests added and passing; live probe pending (user).
