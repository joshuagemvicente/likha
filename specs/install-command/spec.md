# Feature: Install command (`/install`) — interview, plan, then install

**Status:** implemented (local) — automated suite green; live probe and
real-terminal walkthrough pending. **Product requirements:** FR-35 (new), FR-03 (reserved
command list amended to include `/install`), FR-30 (questionnaire amendment in
[ask-user](../ask-user/spec.md)).

## Context

Developers often know *what* they want installed but not every choice it
implies. `/install nvm posix` leaves open the install method, the default Node
version, and which shell startup file to change. Guessing wrong means undoing
changes in the user's home directory. The "Grill Me" pattern fixes this: the
agent interviews the user before acting, offers a recommended answer for every
choice, and only then proceeds.

Likha already has the pieces: `ask_user` asks questions (FR-30), `plan_update`
shows a checklist (FR-31), and `run_command` goes through command approval
(FR-08, [command-permissions](../command-permissions/spec.md)). `/install` puts
them together in one turn. It adds no new install mechanism: every change is an
approved command or an approved edit.

## Relation to v1-spec.md

- FR-03's reserved command list includes `/install` (amended for this feature).
- FR-35 (new) states the `/install` contract.
- FR-30 is amended by [ask-user](../ask-user/spec.md) to support a batch of up
  to four questions, each with an optional recommended option and option
  descriptions. `/install` relies on that amendment.
- Command approval (FR-08), edit review (FR-06/FR-07), and the
  no-blanket-permission rule (§5) apply with no exceptions. An answer to a
  question never grants permission.

## User-visible behavior

1. **Command.** `/install <request>` is a reserved slash command. `<request>`
   is free text naming what to install and any constraints, for example
   `/install nvm posix`, `/install postgres 16 for local dev`, or
   `/install laravel with sail`. `//install` sends a literal prompt (FR-03).
2. **Refusals.** `/install` is refused with a visible conversation entry,
   before any network call and without starting a turn, when:
   - the request is empty (the entry shows `Usage: /install <what to install>`);
   - no provider is configured;
   - plan mode is on (the entry tells the user to turn off `/plan`);
   - a run is active (FR-21) or a review or question is pending.
3. **One turn in the current conversation.** `/install` starts exactly one
   agent turn in the current conversation, as `/init` does. The transcript user
   line shows only what was typed; the model and saved history receive the full
   install prompt with the request under a labelled "Install request" section.
4. **Detect first.** Likha itself, without running a shell command, adds an
   "Environment (detected by Likha)" section to the prompt: OS and
   architecture, `$SHELL`, which shell startup files exist in the home
   directory (names only, never contents), and which common package managers
   are on `PATH`. The agent then checks the rest without asking (for example
   whether the requested tool or its prerequisites are installed) using
   repository read tools and commands in the read-only command tier, which run
   without a prompt. Other probes are refused until the plan exists (item 6);
   the agent asks or states an assumption instead, or makes the check a plan
   step.
5. **Ask only the gaps.** The agent then asks, in one `ask_user` questionnaire
   of one to four questions, only the choices that detection and the request
   did not settle and that would change what gets installed or modified
   (method, version, location, files to change, optional add-ons). Each
   question puts its recommended option first, marked **Recommended**, with a
   one-line reason. Free text and Skip stay available (FR-30). When nothing is
   ambiguous, the agent asks nothing and states its assumptions in the plan.
   A follow-up questionnaire is allowed only when an answer opens a new choice.
6. **Plan before changes.** Before any change, the agent posts the install plan
   as a `plan_update` checklist: the commands it will run, the files it will
   change (including files outside the repository, such as `~/.zshrc`), and the
   assumptions it made. Until that plan exists in the `/install` turn, every
   `run_command` call outside the read-only tier and every edit refuses with a
   result telling the model to finish the questions and post the plan first.
   No review is shown for these refusals.
7. **Execute with approvals.** The agent then works through the plan. Every
   command and edit goes through its normal approval; the user can decline any
   step, and the agent adapts or stops. Files outside the repository change only
   through approved commands, because edit tools stay confined to the
   repository (FR-05).
8. **Verify and summarize.** The turn ends with a verification command where one
   exists (for example `nvm --version` in a fresh login shell) and a short
   summary: what was installed, what changed, what was skipped or declined, and
   any step the user must do themselves, such as opening a new terminal.
9. **Skips and cancel.** If the user skips a question, the agent uses the
   recommended option and says so in the plan. If the user skips the whole
   questionnaire, the agent stops without making any change and says how to
   retry. Esc cancels the run as usual (FR-04). Nothing replays on resume
   (FR-10, FR-30).
10. **No secrets through questions.** The agent never asks for passwords, API
    keys, or tokens through `ask_user`. A step that needs one is listed for the
    user to do themselves.
11. **Discoverability.** `/help` and the `/` autocomplete list show
    `/install <request>` with a one-line description.

## Decisions

Recorded 2026-10-05, user-confirmed (6–7 approved with the implementation
request; 8 chosen after the gate made shell probes impossible).

1. **Command shape.** One built-in `/install <request>` with free text, not one
   command per tool (`/install-laravel`).
2. **Questionnaire.** `ask_user` gains a batch of one to four questions, a
   recommended option per question, and option descriptions
   ([ask-user](../ask-user/spec.md) amendment).
3. **When to ask.** Detect first with read-only probes, then ask only what is
   still ambiguous. Obvious choices are not asked; they are stated as
   assumptions.
4. **Scope of the asking rules.** A lighter version of the asking rules applies
   to every turn (ask-user amendment, "When the agent asks"); `/install` adds
   the stricter detect-ask-plan sequence on top.
5. **Execution.** Plan first, then run each step through normal approvals, then
   verify.

6. **Plan mode.** `/install` is refused in plan mode, matching `/init`.
7. **Plan gate.** Changes refuse until the plan exists in the `/install` turn
   (item 6), so the plan cannot be skipped. Questions are not gated, because
   item 5 allows zero questions.
8. **Environment facts.** Because the gate refuses most probes before the plan,
   Likha injects the environment section itself (item 4) instead of loosening
   the gate. Startup files are reported by name only.

## Acceptance criteria

- [x] `/install <request>` starts exactly one agent turn in the current
      conversation; the transcript shows only the typed command, and the model
      and saved history receive the full install prompt with the request.
- [x] Empty request, no provider, plan mode, an active run, and a pending
      review or question each show a visible refusal and make no request.
- [x] The install prompt carries the Likha-detected environment section (OS,
      arch, shell, startup file names, package managers) and never startup
      file contents.
- [x] The install prompt tells the agent to detect first, ask only the gaps in
      one questionnaire with the recommended option first, post a plan, execute
      through approvals, verify, and summarize; tests check these instructions.
- [x] In the `/install` turn, read-only-tier commands and repository reads run
      before the plan exists; other commands and edits refuse with a
      plan-first message and show no review.
- [x] After `plan_update` posts the plan, commands and edits follow their normal
      approval rules.
- [x] The next prompt after `/install` is a normal turn with no plan gate.
- [ ] Skipping one question records that the recommended option was used;
      skipping the whole questionnaire ends the turn with no change.
- [ ] Resuming after an `/install` turn replays no question, plan step, or
      approval.
- [x] `/help` and autocomplete list `/install`.
- [ ] Live probe: `/install nvm posix` on macOS with zsh asks at most the
      undetectable choices, shows a plan naming `~/.zshrc`, and installs only
      after approvals.

## Non-goals

- Per-tool install commands or a bundled catalog of install recipes.
- Running anything without the normal command approval, or a new trust tier
  for installers.
- Uninstall, upgrade, or rollback flows.
- Asking for or storing credentials.
- Overriding plan mode.
