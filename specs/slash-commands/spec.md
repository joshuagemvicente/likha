# Feature: Slash commands in the prompt input

**Status:** planned — deferred until the release-gate chain (publish → walkthrough → installer verification) is complete. This spec is written now so the design is captured and future work cannot drift.

## Context

Lisa's prompt input currently sends everything to the model. Users of agent TUIs
(OpenCode, OMP, Claude Code) expect leading-slash commands for session, model,
and application control. The features needed for a daily driver already exist
under the surface — session listing/resume (`--sessions`/`--resume`),
provider/model configuration, quit — but they are CLI-only. This feature exposes
them inside the TUI as typed commands, intercepted before the prompt reaches
the model.

## Resolved decisions (user-confirmed direction)

- Commands are typed in the same prompt input and begin with `/`.
- Desired initial set: `/sessions`, `/models`, `/quit`, `/skills` (placeholder —
  see open questions), plus `/help`.
- Implementation timing is flexible: after the release gate, not before.

## Proposed command semantics (to confirm before implementation)

| Command | Behavior |
| --- | --- |
| `/sessions` | List saved sessions for the current repository in the conversation view; selecting one resumes it in place (no restart required). |
| `/models` | List the configured provider's reported models in the conversation view; selecting one switches the active model for subsequent turns (no provider change; provider switch is a setup-flow concern). |
| `/quit` | Identical to Ctrl+D: drains pending state, never replays pending approvals, restores the terminal. |
| `/skills` | **Placeholder** — Lisa has no skills system in any spec. Excluded from scope until a skills feature exists; reserving the name without implementing it is not useful. |
| `/help` | Print the command list in the conversation view (cheap, high value). |
| Unknown `/word` | Visible error in the conversation view; **never sent to the model** — a bare-word catch-all would corrupt conversations when the user legitimately asks the model about "slash commands". |

## Functional changes (to apply in v1-spec.md when started)

- **FR-03 extension:** "A prompt beginning with `/` is interpreted as a
  command from the reserved list; commands act on the application (sessions,
  model, quit) and are never sent to the model. Unknown commands produce a
  visible error."
- A new reserved-words list must be documented in the README command reference.

## Open items to resolve before implementation

1. Does `/sessions` resume in place, or print IDs (requiring a relaunch with
   `--resume`)? In-place resume touches the session store's create/load flow.
2. Does `/models` switching mid-session change anything persisted
   (`config.json` model field), or only the live client? (Recommend: live
   client only; setup flow remains the way to change the stored default.)
3. Escaping: a user who genuinely wants to send a literal "/word" to the
   model — supported via a `//` escape, or accepted as a limitation?
4. During an active run or pending approval, commands must be inert except
   `/quit`-equivalent behavior — confirm the safety rule.

## Acceptance criteria (draft)

- [ ] Each reserved command acts as specified and is never forwarded to the model.
- [ ] Unknown slash commands produce a visible error and leave the draft intact.
- [ ] `/quit` behaves exactly like Ctrl+D, including interrupted-approval safety.
- [ ] `/models` switching takes effect on the next turn without touching stored config.
- [ ] Session listing from `/sessions` matches `--sessions` output.
