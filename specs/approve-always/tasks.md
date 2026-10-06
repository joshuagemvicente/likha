# Tasks: Approve always (session grants for edits, commands, and MCP)

**Status:** implemented (local) — task 0, E1–E5, T1–T6, and W1 done
2026-10-05; W2 (real-TUI walkthrough) outstanding. Ordered; each task
carries its verification. Entry point: `go test ./...`
from the project root; `go vet ./...` and
`go test -race ./internal/agent ./internal/cmdpolicy ./internal/mcp ./internal/tui`
before calling the feature done.

Two slices build against one shared interface (below): the **engine slice**
(`internal/agent`, `internal/cmdpolicy`, `internal/mcp`, `internal/tools`)
and the **TUI slice** (`internal/tui`). Land the interface first (task 0) so
both slices compile independently; then they can proceed in parallel. The
TUI slice owns no engine file and the engine slice owns no `internal/tui`
file.

## Shared interface (task 0 — land first, stubs allowed)

```go
// internal/agent/agent.go — Remember options for ApprovalRequest.Remember.
// The UI sets ApprovalRequest.Remembered before sending true on Reply when
// the user chose the third button; the engine applies the grant.
const (
	RememberSession = "session" // command: "Approve always" (cmdpolicy grant scope)
	RememberTrust   = "trust"   // verification: "Trust repo checks" (unchanged)
	RememberEdits   = "edits"   // edit: "Approve always" for the session's edits
	RememberServer  = "server"  // MCP: "Approve always" trusts the server for the session
)

// EditGrant is the session's "Approve always" for repository edits. The UI
// owns one per session (replaced on session switch); edit tools read it
// from tool goroutines. Safe for concurrent use; a nil *EditGrant is never
// granted and Allow on nil is a no-op.
type EditGrant struct{ on atomic.Bool }

func (g *EditGrant) Allow()        // set by the engine after a Remembered edit approval
func (g *EditGrant) Allowed() bool // read by edit tools and by the status line

// AutoApprovalReason names a call that ran under an Approve always grant.
// Used in the "Approval: auto-approved (<reason>)" result line for both
// commands and edits (replaces "allowed for this session").
const AutoApprovalReason = "approved always for this session"

// internal/agent/tool_registry.go — RunOptions gains:
//	EditGrant *EditGrant // nil: every edit asks and no edit review offers Approve always

// internal/tools/types.go — Result gains (never model content):
//	// Diff is the unified diff an edit applied without a review (Approve
//	// always); the transcript renders it like a reviewed diff.
//	Diff string `json:"-"`

// internal/cmdpolicy/policy.go
// GrantScope is what an "Approve always" grant for one command covers.
type GrantScope struct {
	Key    string // the exact command, or the prefix words joined by single spaces
	Prefix bool   // false: exact-string grant
}

func ScopeFor(command string) GrantScope
// Describe renders the review line's subject: `commands starting with "go test"`
// or `this exact command`.
func (s GrantScope) Describe() string
// Grants keeps its type and method names. Allow now records ScopeFor(command);
// Allowed matches an exact key, or — only when command is a single simple
// command — a Prefix key whose words equal the command's leading words.
func (g *Grants) Allow(command string)
func (g *Grants) Allowed(command string) bool

// internal/mcp/tool_catalog.go
// Decision is the user's answer to a first-call MCP review.
type Decision int

const (
	Declined Decision = iota
	ApprovedOnce   // run this call; the server stays untrusted
	ApprovedAlways // run this call and trust the server for the session
)

func (m *McpManager) AuthorizeTool(ctx context.Context, server, tool, arguments string,
	approve func(server, tool, arguments string) Decision) error
// ResetTrust forgets every server trust and single-call approval (session
// switch, deletion of the active session). Safe on a nil manager.
func (m *McpManager) ResetTrust()
```

The TUI decides labels from `ApprovalRequest.Remember`:
`RememberSession`/`RememberEdits`/`RememberServer` → `Approve always`
(narrow `Always`); `RememberTrust` → `Trust repo checks` (narrow `Trust`).

*Verify:* `go build ./...` and `go vet ./...` green with stubs; existing
tests still compile (the `Call` legacy adapter keeps its `bool` callback,
see task E4).

## Engine slice

E1. **Prefix scopes in `internal/cmdpolicy`.** Implement `ScopeFor`,
   `GrantScope.Describe`, and the new `Grants` matching (keep the mutex;
   store scopes, not bare strings). Rules, using `simpleWords`:
   - not simple, empty, or a leading assignment → exact;
   - `words[0]` (base name, so `/usr/bin/curl` counts as `curl`) in the
     exact-only table → exact. Table: shells and interpreters (`sh`,
     `bash`, `zsh`, `fish`, `dash`, `python`, `python3`, `node`, `deno`,
     `ruby`, `perl`, `php`, `lua`, `Rscript`), wrappers (`env`, `xargs`,
     `nohup`, `timeout`, `time`, `nice`, `watch`, `exec`, `command`,
     `sudo`, `doas`), executors (`npx`, `bunx`, `pnpx`, `uvx`, `pipx`), and
     network tools (`curl`, `wget`, `ssh`, `scp`, `sftp`, `rsync`, `nc`,
     `ncat`, `telnet`, `ftp`);
   - subcommand table (`git`, `go`, `npm`, `pnpm`, `yarn`, `bun`, `cargo`,
     `make`, `docker`, `kubectl`, `gh`, `pip`, `pip3`, `uv`, `poetry`,
     `brew`, `dotnet`, `mvn`, `gradle`, `swift`, `rustup`): requires
     `words[1]` to be a plain subcommand token (`^[A-Za-z][A-Za-z0-9:_-]*$`;
     a flag, path, or `=` falls back to exact). Key = `words[0] words[1]`;
     for `run` under `npm`/`pnpm`/`yarn`/`bun`, key = three words (needs a
     plain script token, else exact); `go run`, `cargo run`, `go generate`,
     `npm exec`, `pnpm exec`, `pnpm dlx`, `yarn dlx`, `bun x` → exact;
   - otherwise key = `words[0]`, Prefix.
   Matching: exact key equality on the raw string, or the command is simple
   and its leading words equal a Prefix key's words.
   *Verify:* table tests — `go test ./a` grant covers `go test ./b`, not
   `go build`; `npm run lint` covers `npm run lint --fix`, not
   `npm run deploy`; `mkdir a` covers `mkdir -p b`; `python a.py`, `npx x`,
   `curl x`, `/usr/bin/curl x`, `ls | wc -l`, `go run .`, `git -C x commit`
   are exact; a compound command never matches a prefix; `go testx` does
   not match `go test`; `go test` (no args) matches its own prefix;
   `go test -race ./internal/cmdpolicy`.

E2. **Command gate (`internal/agent/command_policy.go`).** Keep the
   classify-first order (danger scan, then Read-only/Verify/Ask), so a
   grant can only ever be consulted for Ask (and untrusted Verify without
   trust storage). Replace `"allowed for this session"` with
   `AutoApprovalReason`. When `request.Remember == RememberSession`, append
   to the body: `"Approve always" runs <ScopeFor(command).Describe()>
   without asking for the rest of this session.` Always-ask keeps no
   Remember option.
   *Verify:* update `command_policy_test.go` — a prefix grant auto-approves
   a sibling command with the new reason; `git commit` grant + `git push`
   still prompts with `Always asks:`; `npm test; rm -rf .` never matches;
   the review body names the scope.

E3. **Edit grant in both edit tools (`internal/agent/tool_registry.go`).**
   In `edit_file` and `edit` `Authorize`, after preparing the proposal and
   building the request (Warning included):
   - always ask when `request.Warning != ""` or `options.InitMode` (no
     Remember option);
   - else if `options.EditGrant.Allowed()`: skip `awaitApproval`, keep the
     prepared proposal, and mark the call auto-approved;
   - else set `request.Remember = RememberEdits` when
     `options.EditGrant != nil`, and after an approved, `Remembered` reply
     call `options.EditGrant.Allow()` (only after `approved == true` and
     `err == nil`; cancellation never grants).
   In `Run`, for an auto-approved call set `Result.Diff` to the proposal
   diff (`edit_file`: `proposal.Diff`; `edit`: the same body the review
   would show) and prefix `Content` with
   `CommandApprovalPrefix + "auto-approved (" + AutoApprovalReason + ")\n"`
   so the model and the persisted record both carry the label. Reset the
   auto flag in `Authorize` like `autoApproval` for commands (edit tools are
   interactive, so Authorize always precedes its own Run). Stale checks in
   `Apply` are untouched.
   *Verify:* registry tests — grant set: no approval event, file written,
   `Result.Diff` non-empty, content has the label; root `AGENTS.md`
   warning edit and an `InitMode` edit still emit an approval with empty
   `Remember`; a stale target with the grant set refuses and writes
   nothing; a cancelled review leaves `Allowed()` false; `Remembered`
   approval sets it.

E4. **MCP once vs always (`internal/mcp`, MCP registration in
   `tool_registry.go`).** `AuthorizeTool` takes the `Decision` callback:
   `ApprovedAlways` keeps today's trust grant; `ApprovedOnce` records a
   single-use permission for (server, tool) that `CallTool` consumes;
   `AuthorizeTool` clears any stale single-use entry for the same
   (server, tool) before asking. `CallTool` runs when the server is trusted
   or a single-use entry exists. Add `ResetTrust()`. The legacy `Call`
   keeps its `bool` callback and adapts `true` → `ApprovedAlways`
   (unchanged legacy semantics). In the registry, the MCP review sets
   `Remember = RememberServer` and the body says: `"Approve" runs this
   call only. "Approve always" trusts this server's tools for the rest of
   this session.` followed by the existing permissions/cancellation
   sentence; map the reply to `Decision` from `approved` and `Remembered`.
   *Verify:* mcp tests — once: call runs, second call asks again, a direct
   `CallTool` without authorization is refused; always: no second prompt;
   `ResetTrust` makes the next call ask; cancellation during the review
   grants nothing; `go test -race ./internal/mcp`.

E5. **`tools.Result.Diff` plumbing.** Add the field; confirm
   `finishToolResult`, output retention, and `BoundResult` preserve it and
   that `ModelContent` never includes it (`json:"-"`).
   *Verify:* a unit test that a result with `Diff` produces identical model
   content to one without.

## TUI slice

T1. **Decision bar (`internal/tui/composer.go`).** `reviewButtons` maps
   `RememberSession`, `RememberEdits`, `RememberServer` to
   `{label: "Approve always", narrow: "Always"}` and `RememberTrust` to
   `{"Trust repo checks", "Trust"}`. Replace the fixed `m.width < 56`
   compact test with "the wide row does not fit with three columns spare":
   wide width = Σ(2 marker + len("[ "+label+" ]")) + 2 per gap. With
   Approve always that is 13 + 20 + 13 + 4 = **50**, compact below **53**;
   with Trust repo checks 53, compact below **56** (unchanged). The narrow
   row `[Approve] [Always] [Decline]` is 36 cells, inside the 40-column
   minimum.
   *Verify:* bar tests at 40, 52, 53, 56, 80 columns for edit, command, MCP,
   and trust reviews; two-button reviews unchanged; no-overflow invariant
   green.

T2. **Enter handler and grant state (`internal/tui/tui.go`).** Add
   `editGrant *agent.EditGrant` to `ui`, created in the constructor next to
   `commandGrants`. The existing `focusRemember` path already sets
   `m.pending.Remembered = true` before the send; keep it and make the
   status read `Executing approved <kind> (approve always)` for the three
   Approve always kinds (trust keeps `(trust repo checks)`).
   *Verify:* tests — focusing the third button on an edit review and
   pressing Enter sends `true` with `Remembered`; gated before read-to-end;
   status text per kind.

T3. **Session reset (`internal/tui/dialog.go`).** In `resumeSession` (and
   wherever the active session is deleted), replace `m.editGrant` with a
   fresh `&agent.EditGrant{}` beside the `commandGrants` reset, and call
   `m.conn.Mcp.ResetTrust()`.
   *Verify:* test — grant edits and a command, switch session, both report
   not allowed and the next run's options carry the fresh grants; MCP
   trust reset is called.

T4. **Run options and auto-approved diffs (`internal/tui/tool_wiring.go`).**
   `toolRunOptions` passes `options.EditGrant = m.editGrant`.
   `recordToolResult`: when `approvedEdits` has no entry for the call and
   `result.Diff != ""` with a succeeded/limited status, set
   `record.Diff = reviewedDiff(result.Diff)`.
   *Verify:* a driven turn with the grant set renders the edit item with
   its diff; a refused auto edit shows no diff.

T5. **Auto-approved label on edit items (`internal/tui/tool_items.go`).**
   Strip the leading `Approval: auto-approved (…)` line from edit results
   with the same helper commands use (`toolCommandApproval`, generalized
   if needed) and append `· auto-approved` to the edit item's summary line.
   *Verify:* render test for an auto-approved edit (label present, no
   stray `Approval:` line) and a reviewed edit (no label).

T6. **`AUTO-EDIT` marker (`internal/tui/status_line.go`).** In
   `makeParts`, when `!m.planMode && m.editGrant.Allowed()`, prepend
   `{"AUTO-EDIT", m.theme.Selected}`; trimming retires the model name
   before the marker, as for `PLAN MODE`.
   *Verify:* status tests at wide and narrow widths; plan mode shows only
   `PLAN MODE`; marker gone after session switch.

## Wrap-up

W1. **Docs and status.** Flip the v1-spec amendments from planned once
   verified; update `README.md` ("Command approvals" section and edit
   approval wording), `docs/tools.md` (Allow for session references),
   `specs/README.md` row, and add a CHANGELOG **Unreleased** entry stating
   exactly what was verified (automated vs walkthrough).
   *Verify:* no remaining user-facing "Allow for session" string in code,
   README, or docs except dated historical spec text.

W2. **Real-TUI walkthrough (user).** Edit review → Approve always → later
   edits auto-apply with diffs and `AUTO-EDIT`; command prefix grant; MCP
   once vs always; session switch clears all three.

## Notes

- `RememberSession` keeps its value and name to avoid churn; only its label
  changes.
- No new dependencies; no session schema or config changes. The
  auto-approved label persists inside the tool record's content.
- `internal/cmdpolicy` stays pure (no agent/TUI imports).
- The engine applies every grant; the UI only reports the decision
  (`Remembered`) and owns the grant objects' lifetime.
