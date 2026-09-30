# Checklist: Repository init (`/init`)

Observable outcomes, mirroring spec.md and v1-spec.md wording where they
overlap. All unchecked; this feature is draft.

- [ ] `/init` is accepted in the prompt input as a reserved command and starts
      exactly one agent turn; it is never sent to the model as prompt text.
- [ ] During the `/init` turn, repository reads/list/searches are shown as
      normal tool activity with no extra approvals.
- [ ] The turn ends in an edit proposal for `AGENTS.md` at the repository root,
      displayed as a readable diff with affected paths.
- [ ] No `AGENTS.md` write occurs of any kind without explicit approval of the
      displayed diff.
- [ ] Rejection leaves the repository unchanged: no `AGENTS.md` created, no
      partial write, rejection recorded in the conversation (FR-07, FR-09).
- [ ] A file change at the repository root while the proposal is pending
      produces the stale-proposal refusal, never an overwrite (FR-07).
- [ ] Drafted `AGENTS.md` content is short and factual: build/run/test
      commands, layout, conventions, gotchas; no invented commands, no
      marketing, nothing not backed by a file read during the turn.
- [ ] `/help` lists `/init` with a one-line description.
- [ ] Typing `/init` during an active run or a pending approval follows the
      same command inertness rule as other commands (never interrupts a
      review).
- [ ] With no configured provider, `/init` produces the normal
      unconfigured-provider error path, identical to any agent turn.
- [ ] A repository that already contains `AGENTS.md` follows the decided
      existing-file behavior, and the survey prompt instructs reading the
      existing file.
- [ ] Cancel/cancelled or retried `/init` turns leave no half-written
      `AGENTS.md` behind (only approved writes ever touch the file).
- [ ] Session resume after an `/init` turn shows the completed turns and does
      not replay any pending `AGENTS.md` proposal as approved (FR-10).
