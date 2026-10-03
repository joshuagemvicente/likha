# Feature: Slash commands in the prompt input

**Status:** implemented (local). Tests cover dispatch, unknown-command error
handling, the `//` escape, `/quit`/Ctrl+D parity, `/models` + live `/model`
switching (verified through a real client request), and in-place `/sessions`
resume.

## Context

Likha's prompt input currently sends everything to the model. Users of agent TUIs
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

## Resolved command semantics (decided 2026-09-29 before implementation)

| Command | Behavior |
| --- | --- |
| `/sessions` | No argument: open the **session selection dialog** (dimmed conversation behind it; newest first). With a number (`/sessions 2`): resume that session **in place** — history, entries, and snapshot swap without a restart. Only completed snapshots load; a pending approval is never replayed (v1-spec FR-10 rules take precedence). |
| `/models` | Open the **model selection dialog**: the provider's reported models are fetched and shown with the cursor on the live model; the numbered list is remembered for `/model <n>`. |
| `/model <n-or-id>` | Switch the **live client** to that model (a number from the last `/models` listing, or an exact model ID) for subsequent turns. The `/model` form keeps the stored configuration untouched; applying a model **inside the dialog** (Enter) additionally stores provider+model in `config.json` (decided 2026-09-29 with the dialog integration, superseding the earlier live-only rule). |
| `/quit` | Identical to Ctrl+D: drains pending state, never replays pending approvals, restores the terminal. |
| `/skills` | **Placeholder** — Likha has no skills system in any spec. Excluded from scope until a skills feature exists; reserving the name without implementing it is not useful. |
| `/help` | Print the command list in the conversation view. |
| Unknown `/word` | Visible error in the conversation view; **never sent to the model**, and the draft text is restored so nothing is lost. |
| `//word` | Escape: the leading slash is stripped and `/word` is sent to the model as a normal prompt. |
| During an active run or pending approval | Commands are inert (the Enter key is already ignored while working); `/quit` is likewise deferred to the run's own cancellation path — no command can interrupt a review. |

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
