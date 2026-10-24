# Context: Hooks

The code, specs and prior art this feature touches, and the facts behind the
spec. All decisions live in [spec.md](spec.md). Line numbers were checked
against `main` at `269fc54` (2026-10-07).

## Requirement fit

- [v1-spec §2](../v1-spec.md) lists "executable plugins" as out of scope.
  FR-45 says what hooks are by contrast: commands the user configured in
  the private state directory. They add no tools, prompts or approvals,
  and can only observe or block. The amendment has to say this explicitly,
  so the out-of-scope line and FR-45 do not contradict each other.
- FR-06/FR-08 (edit and command approval, as amended by
  [command-permissions](../command-permissions/spec.md) and
  [approve-always](../approve-always/spec.md)) are unchanged. Hooks run
  before authorization and can only add refusals.
- FR-41 ([attention-notifications](../attention-notifications/spec.md))
  defines the moments the `attention` event reuses.
- FR-44 ([headless-print](../headless-print/spec.md)) runs without a
  human. D11 keeps hook behavior identical there.

## Code

- `internal/tools/registry.go`
  - `Registry.Invoke` (`:206`) is the single entry point for every tool call:
    identity checks, then availability, JSON/schema validation (`:229–:244`),
    then `tool.Authorize` (`:245`), then `runTool` (`:261`). The `pre_tool`
    gate goes between validation and `Authorize`. The `post_tool` notification
    goes after `runTool`, once `Status` is final.
  - The package imports only `model`. Add a small consumer-side interface,
    e.g. `type Gate interface { BeforeTool(ctx, Scope, Tool, json.RawMessage) error; AfterTool(ctx, Scope, Tool, Result) }`,
    set on the registry. A nil gate keeps behavior unchanged.
  - Refusals use `callFailure(..., Refused, code, message, cause)`. A hook
    block uses a new code, `hook_blocked`.
- `Registry.Invoke` callers. All of them go through the same gate, so
  children are covered with no extra wiring:
  - `internal/agent/turn_execution.go:464,481`: main-run calls, sequential
    and parallel;
  - `internal/agent/agent.go:156`: `dispatchTool`;
  - `internal/agent/explore_loop.go:294,323` and `internal/agent/profile_runner.go:292`:
    child scopes.
- `internal/agent/tool_registry.go`
  - `RunOptions` (`:25`) carries per-run hooks such as `Ask` and `WebSearch`.
    A `Hooks` field, a runner held behind an interface, belongs here. `agent`
    must not import `tui`, `bubbletea` or `providers` (AGENTS.md), so the
    runner lives in a leaf package.
  - The `run_command` registration (`:309`) shows the Authorize → Run split.
    `autoApproval` is set in Authorize, so a gate before Authorize leaves
    auto-approval untouched.
- `internal/agent/command_policy.go:24` `authorizeCommand` and
  `internal/agent/edit_grant.go:17` `authorizeEdit` hold the grants and
  auto-approval. Hooks never call into them.
- `internal/agent/agent.go:169` `awaitApproval` blocks on `request.Reply`.
  Nothing in the hook path may write to that channel (decision 3).
- `internal/agent/agent.go:60` `TurnEvent`. Hook warnings and blocks need an
  event kind (e.g. `hook`) for the transcript.
- `internal/actions/command.go`
  - `CommandEnv` (`:110`) strips `LIKHA_API_KEY` and `LIKHA_*_API_KEY`.
    Reuse it for D5.
  - `RunCommand` (`:67–:88`) puts the shell in its own process group
    (`Setpgid`, `:72`), kills the group on cancel, and uses `WaitDelay`.
    Hooks need the same handling for their timeout.
- `internal/mcp/config.go`
  - `LoadConfig` (`:48`) is the pattern to follow: a missing file is not an
    error, a corrupt file is, and validation rejects the whole file.
    `EnsureSkeleton` scaffolds `mcp.json`. Hooks deliberately do not
    scaffold (spec point 1).
- `internal/mcp/tool_catalog.go:107`: MCP tools are named
  `mcp__<server>__<tool>__<hash>`, so `mcp__github__*` matches every tool from one
  server (D2).
- `internal/tui/dialog.go:181` `handleCommand` holds the reserved commands
  (`tools`, `agents`, `plan`, `todo`, `skills`, …). `/hooks` is added here
  and to the autocomplete in `internal/tui/commandcomplete.go`.
- `internal/tui/tui.go:444` `startTurn` and `:512`
  (`agent.RunTurnWithOptions`) are where `prompt_submit` runs, and where the
  run's `RunOptions.Hooks` is set. Steering delivery lives in the same file
  (FR-21 queue).

## Prior art

From [agent-harness/research.md](../agent-harness/research.md) and
[tooling-landscape/feature-comparison.md](../tooling-landscape/feature-comparison.md):

- **Claude Code** ([4] code.claude.com/docs/en/hooks): about 30 lifecycle
  events with matchers, five handler types, and command hooks that exchange
  JSON over stdin/stdout. Exit code 2 blocks, with stderr as the reason.
  `PreToolUse` can return allow, deny or ask, and its allow skips the
  permission prompt. Likha deliberately does not copy that (decision 3).
  Settings come from user, project and local files. Likha takes the user
  file only (decision 1).
- **Gemini CLI** ([52] `docs/hooks/index.md`): ten events (`BeforeTool`,
  `AfterTool`, `Notification`, `PreCompress`, …), a stdin/stdout JSON
  contract, and a strict no-extra-stdout rule. Hooks can block turns or
  tools, rewrite arguments, filter tools and inject context.
- **OpenCode** ([34] `packages/plugin/src/index.ts`): typed plugin hooks
  `tool.execute.before/after`, `permission.ask`, `chat.message`. These are
  in-process plugins, which is the shape v1-spec keeps out of scope.
- **OMP** ([42] `omp://hooks.md`): `pi.on(event, handler)`. `tool_call` can
  block, rewrite or inject. `tool_result` can override. Hooks fail closed
  on handler errors, which D4 follows for blocking events.

Feature comparison §3 tier 2 item 10 ("Hooks surface before full plugins")
recommended a minimal event surface kept out of the approval path. This spec
is that slice.

## Docs to update when implemented

- `docs/usage.md`: hooks file format, events, payloads, exit codes, and the
  safety note (spec point 14).
- `docs/` configuration reference, next to `mcp.json`.
- `CHANGELOG.md` **Unreleased**, with what was verified.
