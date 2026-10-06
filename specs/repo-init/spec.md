# Feature: Repository init (`/init`) — generate a repository context file

**Status:** implemented (local). **Product requirement:** FR-03 (reserved
command list amended to include `/init`).

## Context

Coding agents behave noticeably better when the conversation already knows the
repository's build commands, layout, and conventions. Claude Code's `/init`
established the pattern: the agent surveys the repository and drafts a context
file (there, `CLAUDE.md`; here, the neutral `AGENTS.md` name) describing build
and test commands, code layout, project conventions, and gotchas. Likha
sessions are repo-scoped (v1-spec §2: one selected repository), and an
approved-write flow already exists (FR-06/FR-07), so a per-repository context
file is created once and then used on every later turn.

Loading the file is already done by [agent-harness](../agent-harness/spec.md):
the harness reads the repository-root `AGENTS.md` on every turn and ignores it
with a visible warning when it exceeds 32 KiB. This feature covers only the
*generation* step: `/init` starts one restricted agent turn that surveys the
repository and proposes `AGENTS.md` through the normal edit review.

## Relation to v1-spec.md

- FR-03's reserved command list includes `/init` (amended for this feature).
  The README command reference lists it.
- No other functional requirement changes. The drafted file is an ordinary
  edit proposal: FR-06 (diff before write), FR-07 (rejection and stale
  rules), and the no-blanket-permission rule from §5 apply with no
  exceptions. `/init` starts an agent turn; it is not a write path.

## User-visible behavior

1. **Command.** `/init [guidance]` is a reserved slash command. Text after
   `/init` is optional guidance for the survey (for example
   `/init focus on the test layout`). `/init` is never sent to the model as
   literal prompt text; `//init` still sends a literal `/init` prompt (FR-03).
2. **Refusals.** `/init` is refused with a visible conversation entry, before
   any network call and without starting a turn, when:
   - no provider is configured (same wording style as `/compact`'s
     no-provider refusal);
   - plan mode is on (the entry tells the user to turn off `/plan` to run
     `/init`);
   - a run is active (reserved commands stay inactive during a run, FR-21);
   - a review is pending (no command interrupts a review).
3. **One turn in the current conversation.** `/init` starts exactly one agent
   turn in the current conversation, with earlier history visible to the
   model, as `/skill` does. The transcript's user line shows only what was
   typed (`/init` or `/init <guidance>`); the model and the saved session
   history receive the full generated survey prompt.
4. **Restricted turn.** During the `/init` turn:
   - read-only tools (read, glob, grep, explore subagents, questions to the
     user, and similar inspection tools) work as in a normal turn, with no
     extra approvals and the same FR-05 path confinement;
   - shell commands (`run_command`) and every MCP tool are unavailable: they
     are hidden from the model and, if called anyway, refuse with a result
     that names `/init`, the same way plan mode refuses them;
   - every write tool other than `edit_file` is unavailable in the same way;
   - an `edit_file` proposal whose path is anything other than the
     repository-root `AGENTS.md` (`AGENTS.md` or `./AGENTS.md` after path
     cleaning) is refused with an error the model sees, and **no review is
     shown** to the user.
   The restriction applies only to the `/init` turn. Follow-up prompts are
   normal turns with normal tools and approvals.
5. **Survey instructions.** The generated prompt tells the agent to:
   - survey the repository with read-only tools only and run no commands;
   - read the existing root `AGENTS.md` first when present, plus `CLAUDE.md`,
     `.cursorrules`, `.cursor/rules/`, `.github/copilot-instructions.md`, and
     `README.md` as sources, carrying over only facts it can verify in the
     repository and never copying those files wholesale;
   - when `AGENTS.md` exists, improve it: keep user-authored content, fix
     stale facts, add missing ones; otherwise create it;
   - keep the content short, factual, and repo-specific: build/run/test
     commands, layout, verifiable conventions, gotchas; no marketing, no
     invented commands, every fact backed by a file read during this turn;
   - target about 150 lines / 8 KiB and never exceed 32 KiB;
   - write only the repository-root `AGENTS.md`, never a nested one;
   - finish by proposing the whole file in one `edit_file` call.
   Non-empty guidance is appended under a clearly labelled "User guidance"
   section.
6. **Approval.** The proposal goes through the same diff review as any edit:
   no write before approval (FR-06); rejection leaves the repository
   unchanged and is recorded like any rejected edit (FR-07, FR-09); a
   proposal made stale by a file change while pending is refused as usual
   (FR-07).
7. **Size warning on any turn.** Any edit proposal, in any turn, that targets
   the repository-root `AGENTS.md` with new content larger than 32 KiB shows
   a highlighted warning on the review stating that the harness would ignore
   the file because it exceeds the 32 KiB root-instructions limit. The user
   can still approve or decline.
8. **No-proposal note.** If the `/init` turn ends (done, error, or cancelled)
   without having shown an `AGENTS.md` review, Likha appends a visible note:
   "/init finished without proposing AGENTS.md; nothing was written. Re-run
   /init or ask for the draft." There is no automatic retry. A shown and then
   rejected proposal produces no such note.
9. **Discoverability.** `/help` and the `/` autocomplete list show `/init`
   with a one-line description.

## Decisions

Recorded 2026-10-05, user-confirmed.

1. **Auto-injection into later turns.** Already handled by
   [agent-harness](../agent-harness/spec.md): the root `AGENTS.md` is loaded
   on every turn, capped at 32 KiB, and ignored with a warning above the cap.
   An approved file therefore applies from the next turn. No `/reload` and no
   separate injection feature.
2. **Existing `AGENTS.md`.** `/init` improves it rather than refusing or
   appending: the agent reads it first, keeps user-authored content, fixes
   stale facts, adds missing ones, and proposes one full-file edit through
   review.
3. **Turn shape.** A single turn (survey, draft, proposal), stoppable by
   cancel or rejection, in the current conversation. No staged flow and no
   new session.
4. **Tool restriction.** The `/init` turn uses a dedicated init mode: read-only
   tools stay; shell, MCP, and every write tool except `edit_file` are gated
   like plan mode; `edit_file` is limited to the root `AGENTS.md`, and other
   paths are refused without a review.
5. **Refusals.** No provider, plan mode on, active run, and pending review all
   refuse before any network call. Plan mode is not overridden by `/init`.
6. **Transcript and history.** The transcript shows the short command text;
   the model and saved history carry the full survey prompt.
7. **Size cap.** The survey targets about 150 lines / 8 KiB; any root
   `AGENTS.md` proposal above 32 KiB carries a review warning on every turn,
   not only `/init`.
8. **Missing proposal.** A visible note, no automatic retry.
9. **Sources.** Other agents' instruction files and `README.md` are read as
   sources; only repository-verifiable facts are carried over.

## Acceptance criteria

- [x] `/init` and `/init <guidance>` start exactly one agent turn in the
      current conversation; the transcript user line shows only the typed
      command, and the model and saved history receive the full survey
      prompt with guidance under a "User guidance" section when given.
- [x] With no provider configured, `/init` shows a visible refusal and makes
      no network call.
- [x] With plan mode on, `/init` shows a visible refusal telling the user to
      turn off `/plan`, and no turn starts.
- [x] During an active run or with a review pending, `/init` shows a visible
      refusal and never interrupts the run or the review.
- [x] In the `/init` turn, `run_command`, every MCP tool, and every write tool
      except `edit_file` are absent from the model's tool list and refuse
      with a result naming `/init` if called.
- [x] In the `/init` turn, an `edit_file` call for any path other than the
      root `AGENTS.md` returns an error to the model and shows no review.
- [x] In the `/init` turn, an `edit_file` call for `AGENTS.md` or
      `./AGENTS.md` shows the normal diff review; nothing is written before
      approval, rejection leaves the repository unchanged, and a stale
      proposal is refused.
- [x] Read-only tools work in the `/init` turn without extra approvals and
      stay confined to the repository (FR-05).
- [x] The next prompt after `/init` is a normal turn: shell, MCP, and edits to
      other paths are available under normal approval rules.
- [x] Any root `AGENTS.md` proposal larger than 32 KiB, in any turn, shows the
      highlighted warning that the harness would ignore the file.
- [x] An `/init` turn that ends (done, error, or cancel) without an
      `AGENTS.md` review appends the "finished without proposing AGENTS.md"
      note; a shown-then-rejected proposal does not.
- [x] The survey prompt instructs reading the existing root `AGENTS.md` and
      the listed instruction files, improving an existing file, keeping the
      content factual and backed by files read this turn, targeting about
      150 lines / 8 KiB and never above 32 KiB, root only, no commands, and
      ending with one `edit_file` call.
- [x] `/help` and the `/` autocomplete list include `/init` with a one-line
      description.

## Non-goals

- Nested or per-directory `AGENTS.md` files.
- Running build or test commands to verify drafted facts.
- Changes to how `AGENTS.md` is loaded into later turns (owned by
  agent-harness).
- Automatic retries when the turn ends without a proposal.
- Writing `CLAUDE.md` or any other instruction file, or editing the source
  files the survey reads.
- Overriding plan mode.
