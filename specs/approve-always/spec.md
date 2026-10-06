# Feature: Approve always (session grants for edits, commands, and MCP)

**Status:** implemented (local) — specified and implemented 2026-10-05;
automated suite green (including `-race` on agent, cmdpolicy, mcp, tui);
real-TUI walkthrough (tasks.md W2) outstanding.

Refines v1-spec FR-06 (edit approval), FR-08 as amended by
[command-permissions](../command-permissions/spec.md) (command grants),
FR-16 (MCP server trust), and FR-22 (the decision bar). The amendments are
written into [v1-spec.md](../v1-spec.md), marked planned until this feature
is implemented.

## Context

Likha asks before every repository edit, every command that is not
read-only or a trusted check, and the first call to each MCP server. During
a long change the same kind of review comes back again and again, and the
only alternative today is to keep pressing Approve.

What exists today:

| Review | Buttons | What is remembered |
| --- | --- | --- |
| Edit (`edit_file`, `edit`) | Approve / Decline | nothing; every edit asks |
| Command, Ask tier | Approve / Allow for session / Decline | the exact command string, in memory |
| Command, Verification tier | Approve / Trust repo checks / Decline | the repository's check fingerprints, in `config.json` |
| Command, Always-ask tier | Approve / Decline | never |
| MCP, first call to a server | Approve / Decline | Approve silently trusts the whole server until relaunch |

Every reference agent offers a "remember this" decision: Claude Code's
accept-edits mode and per-command allow rules, OpenCode's **Allow always**
(saves the tool's proposed patterns), and OMP's approval modes (see
[permission-ui](../permission-ui/spec.md) § Research). Likha takes one
consistent label, **Approve always**, with a scope fixed per review kind and
a lifetime of one session.

## User-visible behavior

### 1. One label, one decision bar

Every review that can be remembered shows three buttons:

```
> [ Approve ]  [ Approve always ]  [ Decline ]
  ←/→ choose · Enter confirm · Esc cancel run
```

- **Approve** approves this one proposal, exactly as today.
- **Approve always** approves this proposal and grants the scope described
  below for the rest of the session.
- **Decline** rejects, exactly as today.
- **Approve always** shares Approve's read-to-end gate: it stays muted and
  cannot be confirmed until every page of the review has been seen
  (`Scroll to the end to approve`). Decline is always available.
- Focus starts on **Approve**, moves with `←`/`→` and `Tab`/`Shift+Tab`, and
  `Enter` confirms (FR-22). No letter keys decide anything.
- When the wide bar does not fit with three columns to spare (below 53
  columns), it shortens to `[Approve] [Always] [Decline]`.
- Reviews that can never be remembered keep two buttons,
  `[ Approve ]  [ Decline ]`: Always-ask commands, edits that carry a
  warning, and `/init` proposals.
- Verification checks keep their existing third button,
  `[ Approve ]  [ Trust repo checks ]  [ Decline ]` (`[Trust]` below 56
  columns), unchanged.

### 2. Edits

- **Approve always** on any edit review applies that edit and lets every
  later repository edit (`edit_file` and `edit`) in this session apply
  without a review.
- An edit that runs without a review is still fully checked first: invalid,
  ambiguous, or overlapping operations are refused, and a target that
  changed since the proposal was prepared is refused as stale (FR-07).
  Nothing about what an edit may touch changes; only the review is skipped.
- It still appears as an ordinary edit item with its full diff, and the
  item's summary line ends with `· auto-approved`.
- Two kinds of edit always ask, even with the grant active, and never offer
  **Approve always**:
  - an edit that carries a warning (today: the oversized root `AGENTS.md`
    warning);
  - a `/init` proposal for the root `AGENTS.md`.
- While the edit grant is active, the status line shows a persistent
  `AUTO-EDIT` marker before the provider name, styled like `PLAN MODE`.
  Plan mode refuses edits regardless of the grant; while both are on, only
  `PLAN MODE` shows.

### 3. Commands

**Approve always** replaces **Allow for session** on Ask-tier command
reviews (and on Verification reviews where trust storage is unavailable,
which offered **Allow for session** before). Its scope is the command's
**prefix**, not only the exact string:

- **Tools with subcommands** (`git`, `go`, `npm`, `pnpm`, `yarn`, `bun`,
  `cargo`, `make`, `docker`, `gh`, `pip`, `uv`, and similar): the grant
  covers the program plus its subcommand. Approving `go test ./internal/x`
  lets any later `go test …` run without a prompt; `git commit -m "a"`
  covers `git commit …`.
- **Script runners** (`npm run`, `pnpm run`, `yarn run`, `bun run`): the
  grant covers that one script. Approving `npm run lint:fix` covers
  `npm run lint:fix …`, not `npm run deploy`.
- **Other programs**: the grant covers the program. Approving `mkdir docs`
  covers later `mkdir …`.
- **Exact string only**: the grant falls back to the exact command string
  when the command is not a single simple command (pipes, `&&`, `;`,
  redirects, substitution, globs, variables), or when the program runs
  arbitrary code or reaches the network: interpreters and shells (`python`,
  `node`, `bash`, `sh`, `ruby`, `perl`, …), wrappers (`env`, `xargs`,
  `nohup`, `timeout`, `time`, …), package executors and code runners
  (`npx`, `bunx`, `uvx`, `go run`, `cargo run`, …), and network tools
  (`curl`, `wget`, `ssh`, `scp`, `rsync`, …).
- The review body states what **Approve always** will cover, for example
  `"Approve always" runs commands starting with "go test" without asking
  for the rest of this session.` or `"Approve always" runs this exact
  command without asking for the rest of this session.`
- A grant never loosens classification. Every later command is classified
  first: a command that matches a granted prefix but classifies as Refuse
  is refused, and one that classifies as Always ask still asks every time
  (`git push` is never covered by a `git commit` grant, and a `git` prefix
  is never granted because `git` always records its subcommand). Only a
  later command that is itself a single simple command can match a prefix
  grant; anything else needs an exact-string grant.
- Commands that ran under a grant stay visible tool items with
  `Approval: auto-approved (approved always for this session)`; the item
  reads, for example, `Exit 0 · auto-approved`, and the 10-minute
  auto-approved timeout applies (command-permissions).
- Always-ask and Refuse commands can never be granted. **Trust repo
  checks** is unchanged.

### 4. MCP

The first call to an untrusted MCP server now offers three buttons:

- **Approve** runs this one call. The server stays untrusted, so its next
  call asks again.
- **Approve always** runs this call and trusts the server's tools for the
  rest of the session.
- **Decline** rejects, as today.

The review body says which is which, replacing today's "Approving trusts
this server's tools until app relaunch, not just this call."

### 5. Lifetime

- Every Approve always grant — edits, commands, and MCP servers — lives in
  memory only. Grants are cleared on session switch, when the active
  session is deleted, and on relaunch. They are never written to the
  session database or `config.json`.
- MCP trust follows the same rule: it is now cleared on session switch as
  well as on relaunch (before, it lasted until relaunch).
- Resuming a session never restores a grant (FR-10). A cancelled run, or a
  review interrupted by cancellation, never creates a grant.
- Grants are not inherited by explore or profile child agents; children
  have no edit, command, or MCP tools.

## Interactions and edge cases

- **Read-to-end gate:** a long diff must still be scrolled to its end before
  **Approve always**, exactly as for **Approve**.
- **Plan mode (FR-32):** refuses edits, commands, and MCP calls before any
  grant is consulted; grants survive plan mode and apply again when it is
  turned off, within the same session.
- **Cancellation:** pressing Esc while a review is open cancels the run and
  grants nothing.
- **Steering (FR-21):** an auto-approved edit or command opens no review,
  so steering prompts queued during it are delivered at the next boundary
  as usual.
- **Stale edits:** the grant skips the review, never the stale check; a
  stale target produces the existing conflict result.
- **Concurrency:** grants are written on the UI side when the user decides
  and read by tool goroutines; a grant takes effect for the next tool call
  after the decision.
- **Web UI ([web-ui](../web-ui/spec.md), draft):** its "no auto-approve
  path, no always allow" decision stands until that spec says otherwise.

## Non-goals

- No persistent or per-repository grants (beyond the existing **Trust repo
  checks**), and no user-editable allow or deny rules.
- No command to list or revoke grants in this slice; switching sessions or
  relaunching revokes them. The `AUTO-EDIT` marker makes the edit grant
  visible.
- No per-file or per-path edit grants.
- No "approve always" for web consent; its conversation-scoped grants
  (FR-34) are unchanged.
- No change to what any tool may do; only whether the user is asked.

## Resolved decisions (user-confirmed, 2026-10-05)

1. **Edit scope:** all repository edits for the session (warning-carrying
   edits and `/init` proposals still ask). Alternatives declined: same
   files only; no edit grant.
2. **Command scope:** command prefix for single simple commands, exact
   string otherwise. Alternative declined: exact string only (rename).
3. **MCP:** split into Approve (this call) and Approve always (trust the
   server). Alternative declined: leave MCP as is.
4. **Lifetime:** this session only, in memory. Alternative declined: persist
   per repository.

## Acceptance criteria

- [ ] An edit review shows `[ Approve ]  [ Approve always ]  [ Decline ]`;
      **Approve always** is muted until the diff has been scrolled to its
      end.
- [ ] After **Approve always** on an edit, later `edit_file` and `edit`
      calls in the session apply with no review, each rendering its diff
      with `· auto-approved`; the status line shows `AUTO-EDIT`.
- [ ] With the edit grant active, an invalid edit is refused and a stale
      target produces the conflict result; nothing is written.
- [ ] With the edit grant active, an edit carrying the root `AGENTS.md`
      warning and a `/init` proposal still show a two-button review.
- [ ] An Ask-tier command review shows **Approve always** with a line
      naming its scope; after it, `go test ./a` → `go test ./b` runs
      unprompted with `Exit 0 · auto-approved`; `npm run lint` does not
      cover `npm run deploy`; `mkdir a` covers `mkdir b`.
- [ ] `python a.py`, `npx x`, `curl x`, and `ls | wc -l` grants cover only
      the exact string.
- [ ] A granted prefix never covers a command that classifies as Always
      ask or Refuse (`git commit` grant; `git push` still asks), and a
      compound command never matches a prefix grant.
- [ ] An MCP first-call review shows three buttons; **Approve** runs the
      call and the next call to that server asks again; **Approve always**
      trusts the server for the session.
- [ ] Every grant (edit, command, MCP) is gone after session switch,
      deletion of the active session, and relaunch; a resumed session
      starts with none; cancelling a review grants nothing.
- [ ] Below 53 columns the bar reads `[Approve] [Always] [Decline]`; at 40
      columns nothing overflows.
- [ ] `go test ./...` and `go test -race ./internal/agent ./internal/cmdpolicy
      ./internal/mcp ./internal/tui` pass from the project root.
