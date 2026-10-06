# Checklist: Repository init (`/init`)

Observable outcomes, mirroring [spec.md](spec.md) and
[v1-spec.md](../v1-spec.md) wording where they overlap. Status words per
[specs/README.md](../README.md).

- [x] `/init` is accepted in the prompt input as a reserved command, starts
      exactly one agent turn in the current conversation, and is never sent
      to the model as literal text; `//init` sends a literal prompt.
- [x] The transcript shows only `/init` or `/init <guidance>` as the user
      line; the saved session history holds the full survey prompt.
- [x] With no provider, plan mode on, a run active, or a review pending,
      `/init` shows a visible refusal and no request reaches the provider.
- [x] During the `/init` turn, repository reads, globs, searches, and explore
      tasks appear as normal tool activity with no extra approvals.
- [x] During the `/init` turn, shell commands, MCP tools, and write tools
      other than `edit_file` are not offered to the model and refuse with a
      message naming `/init` if called.
- [x] During the `/init` turn, an edit proposal for any path other than the
      root `AGENTS.md` is refused to the model and no review appears.
- [x] An `AGENTS.md` proposal appears as a readable diff with its path; no
      write occurs before approval.
- [x] Rejection leaves the repository unchanged and is recorded in the
      conversation (FR-07, FR-09).
- [x] A file change while the proposal is pending produces the stale-proposal
      refusal, never an overwrite (FR-07).
- [x] An existing root `AGENTS.md` is read first and the proposal improves it
      as one full-file edit.
- [x] Any root `AGENTS.md` proposal over 32 KiB, in any turn, shows a
      highlighted warning that the harness would ignore the file.
- [x] An `/init` turn that ends without an `AGENTS.md` review (done, error,
      or cancel) shows the "finished without proposing AGENTS.md; nothing
      was written" note; a rejected proposal does not.
- [x] The prompt after `/init` is a normal turn with normal tools and
      approvals.
- [x] An approved `AGENTS.md` is loaded by the harness from the next turn.
- [x] `/help` and the `/` autocomplete list show `/init` with a one-line
      description.
- [x] Cancelled `/init` turns leave no partial `AGENTS.md`; only approved
      writes touch the file.
- [x] Resuming a session after an `/init` turn shows completed turns and never
      replays a pending `AGENTS.md` proposal as approved (FR-10).
- [ ] Real-terminal walkthrough of `/init` on a repository with and without an
      existing `AGENTS.md`.
