# Feature: Repository init (`/init`) — generate a repository context file

**Status:** draft

## Context

Coding agents behave noticeably better when the conversation already knows the
repository's build commands, layout, and conventions. Claude Code's `/init`
established the pattern: the agent surveys the repository and drafts a context
file (there, `CLAUDE.md`; here, the neutral `AGENTS.md` name) describing build
and test commands, code layout, project conventions, and gotchas. Likha sessions
are repo-scoped (v1-spec §2: one selected repository), and an approved-write
flow already exists (FR-06/FR-07), so a per-repository context file compounds
value: it is created once, and other agent features can consume it later.

The user-visible behavior of this feature is only the *generation* step: the
`/init` command starts an agent turn whose job is to survey the repository and
draft `AGENTS.md`. Reading, reusing the normal edit approval, and normal
session persistence are Likha behaviors this spec must not change.

## Relation to v1-spec.md

- This feature adds a `/init` command to the FR-03 reserved command list.
  **Before implementation**, FR-03's list and the README command reference
  (see [../slash-commands/spec.md](../slash-commands/spec.md) "Functional
  changes") must be amended to include `/init`. Amendment of v1-spec.md is a
  prerequisite task, not part of this spec's behavior.
- No other functional requirement changes. The draft file is an ordinary edit
  proposal: FR-06 (diff before write), FR-07 (rejection/stale rules), and the
  no-blanket-permission rule from §5 apply with zero exceptions. `/init` is a
  convenience for *starting* an agent turn, not a write path.

## User-visible behavior

1. Typing `/init` in the prompt input starts one agent turn (the same loop as
   any normal prompt). The task given to the agent in that turn is: survey the
   repository using read-only tools, then draft a repository context file named
   `AGENTS.md` at the repository root, and propose it as a file edit. During
   the survey, streamed agent text and read/list/search tool activity appear
   exactly as in a normal turn; `/init` itself never writes anything.
2. The proposed `AGENTS.md` goes through the **same diff-approval flow as any
   edit**. No bypass exists: the user reviews the full diff with affected
   paths, and no write proceeds without explicit approval (v1-spec FR-06).
   The file is never created without explicit approval.
3. Rejecting the proposal leaves the repository unchanged: `AGENTS.md` is not
   created, no partial content reaches the file (FR-07), and the conversation
   records the rejection normally (FR-09). A stale proposal (the root file
   changed while approval was pending) is refused as usual (FR-07).
4. The drafted content is **short, factual, and repo-specific**: build/run/test
   commands, important directories/files and the repository layout,
   code conventions the agent could verify in the repository, and gotchas
   (such as required environment or known pitfalls visible from repo files).
   It must not contain marketing language, claims about the project not
   observable in the repository, or invented commands. If a fact cannot be
   backed by something the agent read, it must not be written.
5. `/help` lists `/init` with a one-line description. Unknown-command handling,
   the `//` escape, and command inertness during an active run or pending
   approval follow FR-03 unchanged: `/init` cannot start a run while another
   run or an approval is blocking, and it is never sent to the model as text.
6. The user can cancel or reject the `/init` turn like any other turn, or
   correct the draft's direction in follow-up prompts and re-approve an
   amended proposal through the same approval flow.
7. `/init` requires a configured, working provider as any agent turn does; if
   no provider is set up, it produces the normal first-run/unconfigured error,
   not a special case.

## Open decisions (resolve before implementation)

1. **Auto-injection into later sessions.** Does Likha read `AGENTS.md` at
   session start and append its content as part of system context? This is
   explicit in scope only if a later feature decides it; the tradeoffs are
   real: token cost on every turn, staleness when `AGENTS.md` drifts from the
   repository, and what a `/reload`-style behavior would do. Generation of the
   file is valuable even if injection is never implemented.
2. **Existing `AGENTS.md` interaction.** When the repository root already has
   an `AGENTS.md`, `/init` must do exactly one of: refuse with a clear error,
   offer to rewrite it from scratch (still through approval), or propose an
   appended/updated section. The survey prompt must tell the agent to read the
   existing file either way. Decision needed; no behavior is decided here.
3. **Prompt shape.** Whether the survey is one turn (survey + draft + proposal)
   or a staged flow the user steers. Recommendation: single turn, cancel/
   reject-stoppable, matching Likha's turn model.

## Acceptance criteria

- [ ] `/init` starts exactly one agent turn whose final action is an edit
      proposal for `AGENTS.md` at the repository root.
- [ ] The draft proposal is displayed as a readable diff with affected paths
      and requires explicit approval; no write happens before approval.
- [ ] Rejecting the proposal leaves the repository unchanged (no `AGENTS.md`
      is created); the conversation records the rejection like any rejected
      edit.
- [ ] An existing file at the repository root racing the pending proposal
      produces the normal stale-proposal refusal (FR-07), never an overwrite.
- [ ] The drafted content contains only commands and facts backed by files the
      agent read this turn; no invented commands, no marketing language.
- [ ] `/init` during an active run or pending approval behaves per FR-03 (inert
      or deferred as commands are), never interrupts a review, and is never
      sent to the model as literal prompt text.
- [ ] `/help` lists `/init`; unknown/slash-command rules are unchanged.
- [ ] A repository with already-present `AGENTS.md` follows the decision taken
      for existing-file interaction (once decided), and the survey reads the
      existing file.
- [ ] All reads performed during `/init` stay inside the repository (FR-05
      path confinement unchanged).
