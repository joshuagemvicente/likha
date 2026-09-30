# Checklist: plan mode (`/plan`)

Observable outcomes, mirrors [spec.md](spec.md). Status words per
[specs/README.md](../README.md) rules.

- [ ] `/plan` toggles plan mode on and off within the same session, and
      nothing about the mode survives a restart or resume (resumed sessions
      always start in normal approval-gated behavior).
- [ ] The status footer shows the plan-mode marker whenever the mode is on,
      and is absent whenever it is off, on every frame regardless of
      streaming/review/paging state.
- [ ] In plan mode, the agent's edits and shell commands are refused before
      any approval prompt appears, and each refusal is recorded and reported
      per FR-09 — never silently dropped, never shown as a diff to approve.
- [ ] In plan mode, repository reads (list/search/read) still succeed with
      the same FR-05 scoping and reporting as in normal mode.
- [ ] In plan mode, MCP tool call behavior follows the decision recorded in
      spec.md, and any refusal there is reported the same way mutation
      refusals are.
- [ ] After leaving plan mode, one edit proposal and one shell command still
      round-trip through their normal FR-06/FR-08 approval flow unchanged.
- [ ] Refusals reach the model as tool results, so the agent reacts in text
      instead of retrying the same mutation in a visible loop.
- [ ] Failure of any step — a refusal, a failure of the read path, a save
      failure after a refusal — keeps the TUI usable per FR-11 (no crash, no
      silent hang).
- [ ] Status of this feature is only ever: planned, in progress,
      implemented (local), or verified (release), per the release gate in
      [v1-spec.md](../v1-spec.md).
