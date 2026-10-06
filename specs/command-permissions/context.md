# Context: command permissions

Code paths this feature touches. No design decisions hidden in prose — the
behavior in [spec.md](spec.md) is the whole contract. Line numbers drift
while implementation is underway; function names are the anchors.

## Code

- `internal/cmdpolicy/` (new, pure; imports nothing from `tui` or `agent`):
  - `policy.go` — `Tier` (`Refuse`, `AlwaysAsk`, `Ask`, `Verify`,
    `ReadOnly`), `Decision` (`Tier`, `Reason`, `Check`, `Fingerprint`,
    `Script`), `Classify(command, root)`, repository-root `scope`, session
    `Grants` (mutex-guarded exact-string set), `RepoChecks(root)` (the
    fingerprint set recorded on trust), `package.json` script reading and
    `pre`/main/`post` fingerprinting, Makefile fingerprinting.
  - `danger.go` — Refuse patterns, the whole-string segment scan that ignores
    quoting, Always-ask command and path rules (secrets, outside the repo).
  - `simple.go` — the single-simple-command check, Read-only and
    Verification matching.
- `internal/agent/command_policy.go` — `authorizeCommand`: classify, refuse,
  auto-approve (read-only, trusted check, session grant), or build the
  review (`Always asks:` line, script text, third-button kind) and record the
  grant or trust after approval.
- `internal/agent/tool_registry.go` — the `run_command` registration:
  `Authorize` delegates to `authorizeCommand`; `Run` applies the 10-minute
  timeout to auto-approved commands and adds the
  `Approval: auto-approved (<reason>)` line after `Exit status: N`.
- `internal/agent/agent.go` — `ApprovalRequest` (gains the remember kind and
  the user's remember choice), `requestApproval`; `RunOptions` carries the
  session grants and the trust lookup/store callbacks.
- `internal/actions/command.go` — `executeCommand`: remove `LIKHA_API_KEY`
  and `LIKHA_*_API_KEY` from the child environment for every command.
- `internal/providers/config.go` — `StoredProviderConfig` gains
  `command_trust`, keyed by canonical repository path, mapping check keys to
  fingerprints.
- `internal/tui/composer.go` — the decision bar (`reviewActionLines`): third
  button, narrow (<56 columns) labels, focus across two or three buttons.
- `internal/tui/tui.go` — review key handling (`←`/`→`, `Tab`/`Shift+Tab`,
  `Enter`, gate), session switch/delete clearing the grants, wiring
  `RunOptions`.
- `internal/tui/view.go` — review header warnings (the two `WARNING:` lines
  stay); the `Always asks:` line.
- `internal/tui/tool_render.go` — the `⎿` outcome line
  (`Exit 0 · auto-approved`).

## Precedent

- `internal/mcp/tool_catalog.go` — MCP server trust-on-first-use: an
  in-memory grant checked before prompting, reset on relaunch.
- `internal/tui/web_consent.go` — `webGrants`: UI-owned, mutex-guarded
  grants passed into the run and cleared on session switch.
- `internal/tui/composer.go` decision bar and `scroll.go` `reviewSeen` gate
  from [permission-ui](../permission-ui/spec.md).

## Tests to add

- `internal/cmdpolicy` table tests: every tier, every bypass attempt in the
  spec's acceptance list, secret and outside-repo paths, `scripts.test`
  containing `rm -rf`, missing script, changed script/hook/Makefile
  fingerprint, npm flag rules.
- Registry tests (`internal/agent`): auto-approval emits no approval event;
  Always ask never offers a third button; plan mode still refuses; an
  untrusted repository prompts with the trust button; a session grant
  matches only the exact string.
- Executor test (`internal/actions`): `LIKHA_*_API_KEY` is absent from the
  child environment.
- TUI tests: two- and three-button bars at 80 and 40 columns, focus
  movement, gated Approve and third button, grants cleared on session switch
  and delete, `auto-approved` `⎿` line.

## Related specs

- [v1-spec.md](../v1-spec.md) — FR-08, §4 approval scope, §6 approval
  coordinator, §7 command flow and session lines, §10 command-approval row
  (all amended 2026-10-04).
- [agent-loop/](../agent-loop/spec.md) — the reversed "no allowlist"
  non-goal (amended).
- [tool-registry/](../tool-registry/spec.md) — saved shell allow-rules and
  the shell authorization step (amended).
- [permission-ui/](../permission-ui/spec.md) — decision bar, now two or three
  buttons (amended).
- [repository-tools/](../repository-tools/spec.md),
  [tooling-platform/](../tooling-platform/spec.md),
  [tooling-platform/decisions.md](../tooling-platform/decisions.md) — shell
  rows and "no classifier" notes (amended).
- [plan-mode/](../plan-mode/spec.md) — unchanged refusal; exit behavior note
  amended.
- [web-ui/](../web-ui/spec.md) — draft; its no-auto-approve decision stands
  (note added).
- [custom-commands/](../custom-commands/spec.md) — note added: a custom
  command cannot grant session or repository trust.
- [mcp-support/](../mcp-support/spec.md), [web-tools/](../web-tools/spec.md)
  — independent grant models, unchanged.
