# Context: Headless print mode

Code, tests and docs this feature touches, with the facts that shaped the
spec. Behavior lives in [spec.md](spec.md); order of work in
[tasks.md](tasks.md). Paths verified against `main` on 2026-10-07.

## Requirements this amends or relies on

- **v1-spec §2 In scope** lists "Interactive TUI chat". FR-44 adds one
  non-interactive single run beside it; it does not add a server, a second
  concurrent main run, or any new way to approve an action.
- **FR-03** ("The TUI accepts prompts…") stays true for the TUI; headless
  is a second entry point to the same run, without slash commands.
- **FR-06/FR-08** (approval before every write and prompted command) are
  unchanged: headless refuses instead of approving. **FR-09** governs the
  refusal wording: refused is neither success nor a user rejection.
- **FR-10** sessions: headless runs are stored sessions; nothing pending
  replays on resume.
- **FR-16 / MCP trust-on-first-use** is session-scoped and in memory
  (`internal/mcp/manager.go` `trusted` map), so a fresh headless run can
  never have a trusted server (drafting default D7).
- **FR-30** `ask_user` needs a person; headless does not offer it.
- **FR-37** model effort: no CLI flag by design; D6 applies the stored level.
- The `--help` footer says "Likha needs an interactive terminal." and must
  change.

## Code

- `internal/app/run.go`
  - `Run` parses flags with the standard `flag` package. Existing
    non-TUI flows (`--login` → `browserLoginFlow`, `--probe-effort` →
    `effortProbeFlow`) return before the TUI starts; headless follows the
    same pattern. `probeFlagError` is the model for flag-combination errors.
  - `interactive := isatty(stdin) && isatty(stdout)`; the check
    `if !interactive { "interactive terminal required"; return 2 }` sits
    after provider resolution and MCP manager creation. Headless must branch
    before it, and before `mcp.NewMcpManager` (D7).
  - First-run setup: `setupNeeded && !interactive` already prints the
    "no provider configured" message and returns 2 — reuse it.
  - `oauthSetupRequired(selected, stateDir, interactive)` decides whether
    ChatGPT needs a browser login.
  - Startup connection check: `client.EnsureConnected` (API-key providers)
    or `discoverOAuthModel` (ChatGPT). In the TUI a failure is shown, not
    fatal; headless exits 1.
  - Session: `store.Create()` / `store.Load(*resumeID)`; the OpenCode
    session header (`client.SetSessionHeader`, `SetSession`) is set from the
    snapshot ID and must be set in headless too.
  - Only `cmd/likha` imports `internal/app`; `tui` never imports `app`.
- `internal/agent/turn_execution.go`
  - `RunTurnWithOptions(ctx, client, repo, root, prior, prompt, servers,
    steer, options, emit)` is the whole run. A nil `steer` channel disables
    steering (`drainSteer`). `servers` may be nil.
  - Emitted kinds: `notice`, `text`, `reasoning`, `usage`, `tool_start`,
    `tool_result`, `tool_checkpoint`, `task`, `task_runtime`, `context`,
    `stream_interrupted`, `steer`, `error`, `done` (with final `History`).
    The final answer is the last assistant message in `done.History`.
  - `ExpandFileReferences` expands `@path` mentions in the prompt; headless
    gets that for free.
- `internal/agent/agent.go`
  - `awaitApproval` is the single choke point for edit, command and MCP
    reviews: it emits `Kind: "approval"` and blocks on `Reply` or `ctx`.
    It does not see `RunOptions`, so the headless refusal needs the option
    threaded to it (tasks T1).
  - `TurnEvent`, `ApprovalRequest`, `AskInteraction`, `ConsentRequest`.
- `internal/agent/command_policy.go` `authorizeCommand`: Refuse →
  error; ReadOnly → auto; Verify → auto when `options.CommandTrusted`
  matches; otherwise builds an `ApprovalRequest`. Headless keeps the
  auto paths and refuses where the request would be emitted.
  `AutoCommandTimeout` (10 min) still bounds auto-approved commands.
- `internal/agent/tool_registry.go`
  - `RunOptions` (line 25): `Ask`, `WebSearch`, `WebFetch` nil → the tool
    registers with its unavailable reason (hidden from the model, forged
    calls refused). That already gives D8 for `ask_user` and web tools.
  - Edit approvals at lines ~218 (`edit_file`) and ~277 (`edit`), MCP trust
    review at ~411; `UnavailableReason` pattern used by plan mode
    (`refused (plan mode): …`) and `/init`.
  - `EditJournal` nil disables exact multi-file `edit` with a stated reason;
    headless must supply the journal (via the session store) so `edit`
    stays offered and refuses at review time rather than at registration.
- `internal/tui/tool_wiring.go` `toolRunOptions` assembles `RunOptions`
  for the TUI: outputs store, `SaveTask`, `PlanApply`, `EditJournal`,
  ask broker, skills (`skill_wiring.go`), task profiles
  (`profile_wiring.go`), web hooks (`web_wiring.go`), command grants and
  trust hooks (`providers.CommandTrusted`, `providers.TrustCommandChecks`).
  Headless needs the non-UI parts of this; they cannot be imported from
  `tui`. Extracting them is a behavior-preserving `refactor(...)` commit of
  its own (tasks T3).
- `internal/tui/tui.go` `persist()` (line ~2184) builds `session.Snapshot`
  from TUI entries (`entry.role` "You", "Likha", "Error", tool records) and
  calls `store.Save`. Headless writes `History` plus plain entries so the
  TUI can render the session on `--resume`.
- `internal/session/session.go`: `Store.Create`, `Load`, `Save`, `List`;
  `Snapshot` fields `History`, `Entries`, `ToolRecords`, `PromptHistory`.
- `internal/effortprobe/report.go` `Redact(text, secrets...)`: the existing
  credential scrubber for printed errors; headless reuses it or moves it to
  a shared place (it must not pull `effortprobe` into the run path).
- `internal/model`: `RequestUsage` and pricing for the summary/usage events.

## Import rules that constrain the design

- A new `internal/headless` package may import `agent`, `model`,
  `session`, `repository`, `providers`, `tooloutput`, `tools`; it must not
  import `tui`, `bubbletea` or `lipgloss`. `app` calls it.
- `agent` must stay free of `providers`, `tui` and Bubble Tea: the refusal
  belongs in `agent` as a `RunOptions` field, not as a TUI behavior.

## Tests

- `tests/integration/cli_test.go`, `effort_probe_test.go`: exec
  `go run ./cmd/likha` with a temp `LIKHA_STATE_DIR` and cleared
  `LIKHA_*` environment (`probeCommand`); `effortServer` is an `httptest`
  OpenAI-compatible fake. Headless end-to-end tests follow this shape.
- `internal/agent/approval_test.go`, `command_policy_test.go`: existing
  approval and classification coverage to extend for the refusal path.

## Docs

- `--help` text (`usageHeader`/`usageFooter` in `run.go`).
- `docs/usage.md` gains a "Headless runs" section; `README.md` mentions it.

## Known limits

- A headless `--resume ID` while a TUI has the same session open is
  last-writer-wins, as two TUIs on one session are today.
- Until FR-36 (model-recovery) lands, a provider that stops sending data
  hangs the run until `--max-time` (D4); that is why D4 has a default.
