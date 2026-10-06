# Checklist: Approve always (session grants for edits, commands, and MCP)

Observable outcomes; unchecked until seen in the real TUI. Mirrors
[spec.md](spec.md) acceptance criteria. Status words per
[specs/README.md](../README.md) rules.

- [ ] An edit review shows `[ Approve ]  [ Approve always ]  [ Decline ]`,
      and **Approve always** stays muted until the diff is scrolled to its
      end.
- [ ] After **Approve always** on an edit, later edits in the session apply
      without a review, each showing its diff and `· auto-approved`, and
      the status line shows `AUTO-EDIT`.
- [ ] With the grant on, a stale or invalid edit is still refused and
      nothing is written.
- [ ] The oversized root `AGENTS.md` warning edit and `/init` proposals
      still show `[ Approve ]  [ Decline ]`.
- [ ] An ordinary command review shows **Approve always** and a line saying
      what it covers; after it, `go test ./other` runs unprompted with
      `Exit 0 · auto-approved`.
- [ ] `npm run lint` does not cover `npm run deploy`; `python x.py`,
      `npx x`, `curl x`, and piped commands are granted only as exact
      strings.
- [ ] A `git commit` grant never lets `git push` (or any Always-ask or
      refused command) run without its usual handling.
- [ ] An MCP first call shows three buttons: **Approve** runs only that
      call (the next call asks), **Approve always** trusts the server.
- [ ] Switching sessions, deleting the active session, or relaunching
      clears every grant and the `AUTO-EDIT` marker; cancelling a review
      grants nothing.
- [ ] Below 53 columns the bar reads `[Approve] [Always] [Decline]`; at 40
      columns nothing overflows.
- [ ] `go test ./...` and the race run pass from the project root.
