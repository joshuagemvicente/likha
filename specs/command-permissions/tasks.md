# Tasks: command permissions

**Status:** implemented (local) — automated suite green; real-terminal walkthrough pending. Ordered; each task
carries its verification. No checkmarks until the work is verified. Entry
point: `go test ./...` from the project root; `go vet ./...` and
`go test -race ./internal/agent ./internal/cmdpolicy ./internal/tui` before
calling the feature done.

1. **Spec and amendments.** Write this folder; amend v1-spec FR-08, §4
   approval scope, §6 approval coordinator, §7 command/session lines, §10
   command-approval row; amend agent-loop (reversed non-goal),
   tool-registry, permission-ui, repository-tools, tooling-platform
   (spec and decisions), plan-mode, web-ui, custom-commands, each labelled
   "Amended 2026-10-04 by command-permissions" with the original text kept.
   Add the `specs/README.md` row, the README "Command approvals" section,
   and the CHANGELOG bullet.
   *Verify:* every statement that said "every command asks" or "no
   allowlist" either still holds or carries an amendment note pointing here.
   **Done 2026-10-04.**

2. **Classifier package `internal/cmdpolicy`.** `Classify(command, root)`
   returning `Decision{Tier, Reason, Check, Fingerprint, Script}`; the
   whole-string danger scan (quoting ignored, every segment, case-insensitive
   names, `\rm`/`/bin/rm`/`command rm`); path rules for secrets and
   outside-repo paths; the single-simple-command check; Read-only and
   Verification tables; `package.json` script resolution with `pre`/`post`
   hooks (script text classified as Refuse/Always ask → Always ask; missing
   → Ask); `RepoChecks(root)` fingerprints; mutex-guarded session `Grants`.
   No new dependency.
   *Verify:* table tests for every tier and every bypass attempt in the
   spec's acceptance criteria; `go test -race ./internal/cmdpolicy`.
   **Done 2026-10-04: `internal/cmdpolicy` with table tests for every tier and bypass attempt; race test green.**

3. **Executor hardening.** In `executeCommand`
   (`internal/actions/command.go`), build the child environment without
   `LIKHA_API_KEY` and `LIKHA_*_API_KEY`, for every command.
   *Verify:* a test command prints its environment; none of those variables
   appears; other variables still do.
   **Done 2026-10-04: `actions.CommandEnv`; env test green.**

4. **Gate in the agent.** `authorizeCommand`
   (`internal/agent/command_policy.go`) classifies before any review:
   Refuse → refused result with the "user can run it themselves" message;
   Read-only, trusted Verification, and session-granted → no review, record
   the auto-approval reason; Always ask → review with `Always asks:` and
   no remember option; Ask → remember-for-session option; untrusted or
   changed Verification → trust option and, for scripts, the resolved script
   text. After approval with remember: add the exact string to the session
   grants, or store `RepoChecks(root)` as the repository's trust (notice on
   failure; the command still runs). In `run_command` `Run`, apply the
   10-minute timeout and the `Approval: auto-approved (<reason>)` line only
   to auto-approved commands. Plan mode refuses before this gate.
   *Verify:* registry tests — auto-approval emits no approval event; Always
   ask never offers a third option; plan mode refuses; an untrusted
   repository prompts with trust; a changed fingerprint prompts again; a
   session grant matches only the exact string.
   **Done 2026-10-04: `internal/agent/command_policy.go` with registry/authorize tests.**

5. **Trust storage.** `command_trust` in `StoredProviderConfig`
   (`internal/providers/config.go`), keyed by canonical repository path;
   lookup returns true only for an existing key with a matching
   fingerprint; trusting replaces the repository's record. Nothing is read
   from repository files to decide trust.
   *Verify:* config round-trip test preserving other `config.json` fields;
   a removed entry makes the next check prompt.
   **Done 2026-10-04: `internal/providers/command_trust.go`; round-trip and symlink tests green.**

6. **TUI decision bar and labels.** Third button **Allow for session** or
   **Trust repo checks**; narrow (<56 columns) labels `[Approve] [Session]`
   / `[Trust]` `[Decline]`; focus across the visible buttons with `←`/`→`
   and `Tab`/`Shift+Tab`; Approve and the third button gated on review end;
   `Always asks:` line; the two `WARNING:` lines kept. Session grants are
   UI-owned, passed through `RunOptions`, and cleared on session switch,
   session delete, and relaunch. The tool item `⎿` line reads
   `Exit 0 · auto-approved` for auto-approved commands.
   *Verify:* rendering tests at 80 and 40 columns for two- and three-button
   bars without overflow; focus/gate key tests; grants cleared on switch and
   delete; tool-render test for the auto-approved outcome.
   **Done 2026-10-04: three-button bar fits 40–120 columns (compact labels below 56); focus/gate, warning, label, grant-reset, and an end-to-end run test green.**

7. **Real-terminal walkthrough** with a fresh `LIKHA_STATE_DIR` on a sample
   Node repository: first `npm test` shows the trust prompt; after trusting
   it runs unprompted with the label; editing `scripts.test` prompts again;
   `rm file`, `git push`, and `npm install` prompt; `rm -rf /` is refused; a
   fresh untrusted repository prompts for `npm test`; **Allow for session**
   clears on session switch; deleting the `command_trust` entry restores the
   prompt.
   *Verify:* outcomes recorded in [checklist.md](checklist.md); status moves
   to implemented (local) only after the automated suite is green.

## Notes

- Plan mode, edit reviews, MCP trust, and web consent are unchanged.
- The allow tiers accept only trivially simple input; a classification bug
  must fail toward a prompt, never toward silent execution.
- No new dependencies.
