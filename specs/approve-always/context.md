# Context: Approve always (session grants for edits, commands, and MCP)

Code paths this feature touches. No design decisions hidden in prose — the
behavior in [spec.md](spec.md) is the whole contract and the shared
interface lives in [tasks.md](tasks.md). Line numbers drift; anchor on the
names.

## Code

- `internal/agent/agent.go` — `ApprovalRequest` (`Remember`, `Remembered`,
  `Reply`), the `Remember*` constants, `awaitApproval`. Gains
  `RememberEdits`, `RememberServer`, `EditGrant`, `AutoApprovalReason`.
- `internal/agent/command_policy.go` — `authorizeCommand`: classify first,
  then trusted Verify / session grant / prompt; `RememberSession` handling
  after approval; the `"allowed for this session"` reason.
- `internal/agent/tool_registry.go` — `RunOptions` (`CommandGrants`,
  `CommandTrusted`, `TrustChecks`; gains `EditGrant`); the `edit_file` and
  `edit` registrations (`Authorize` builds the `ApprovalRequest`, sets the
  root-instructions `Warning`, honors `InitMode`; `Run` applies);
  `run_command`'s `autoApproval` pattern; the MCP registration loop
  (`servers.AuthorizeTool` callback and review body).
- `internal/cmdpolicy/policy.go` — `Classify`, `Grants` (`Allow`,
  `Allowed`, mutex); `internal/cmdpolicy/simple.go` — `simpleWords`, the
  single-simple-command lexer the prefix rule reuses;
  `internal/cmdpolicy/danger.go` — the danger scan that always runs first.
- `internal/mcp/tool_catalog.go` — `AuthorizeTool` (trust check, approve
  callback, trust grant) and `CallTool` (refuses an untrusted server);
  `internal/mcp/manager.go` — `trusted` map, legacy `Call`.
- `internal/tools/types.go` — `Result` (gains `Diff`, `json:"-"`);
  `internal/tools/result.go` — `ModelContent` marshals the result when it
  is not plain, so the field must stay excluded.
- `internal/tui/composer.go` — `reviewButton`, `reviewButtons`,
  `reviewActionLines` (the fixed `m.width < 56` compact rule).
- `internal/tui/tui.go` — `focusApprove`/`focusDecline`/`focusRemember`,
  the review `Enter` handler (sets `Remembered`, status text), `ui` fields
  (`commandGrants`; gains `editGrant`), the constructor.
- `internal/tui/dialog.go` — `resumeSession` resets `commandGrants` and
  `webGrants` (gains the edit grant and MCP trust reset).
- `internal/tui/tool_wiring.go` — `toolRunOptions` (passes grants),
  `recordToolResult` (`approvedEdits` → `record.Diff`),
  `rememberApprovedEdit`, `reviewedDiff`.
- `internal/tui/tool_items.go` — `toolCommandApproval` (the
  `· auto-approved` label) and the edit item summary.
- `internal/tui/status_line.go` — `makeParts` (`PLAN MODE` marker; gains
  `AUTO-EDIT`).
- `internal/tui/tools_view.go` — `/tools` permission text for edits, MCP,
  and commands (mentions "remembered until app relaunch"); update wording.
- `internal/tui/init_command.go` — `isRootAgentsEdit` (the `/init`
  proposal the grant never covers).

## Tests to extend

- `internal/agent/command_policy_test.go` — the session-grant reason and
  exact-string assertions.
- `internal/cmdpolicy/policy_test.go` — tier tables; add `ScopeFor` and
  prefix-matching tables.
- `internal/mcp` tests using `NewMcpManagerForTest` and `Call`.
- `internal/tui/command_permissions_test.go` — `Allow for session` labels,
  compact `[Session]`, grant reset on session switch.
- `internal/tui/composer_test.go`, `composer_overflow_test.go` — decision
  bar widths and the no-overflow invariant.
- `internal/tui/status_line_test.go` — the plan-mode marker pattern.

## Related specs

- [v1-spec.md](../v1-spec.md) — FR-06, FR-16, FR-22 and the approval-scope
  paragraph carry planned amendments pointing here.
- [command-permissions/](../command-permissions/spec.md) — tiers, session
  grants (renamed and widened here), the auto-approved label and timeout.
- [permission-ui/](../permission-ui/spec.md) — the decision bar and the
  read-to-end gate.
- [plan-mode/](../plan-mode/spec.md) — refuses mutation before any grant.
- [repo-init/](../repo-init/spec.md) — the `/init` proposal that always
  asks.
- [web-ui/](../web-ui/spec.md) — "no always allow" stands.
- [steering-prompts/](../steering-prompts/spec.md) — auto-approved calls
  open no review; queue delivery is unchanged.
