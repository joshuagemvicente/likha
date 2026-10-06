# Spec: All-provider models in `/models`

**Status:** implemented (local) — behavior covered by the automated suite (`internal/tui/models_all_test.go` plus the updated dialog tests); real-TUI walkthrough outstanding.

## Context

Bare `/models` opens the model selection dialog over the **active** provider
only: `openDialog` fetches `model.ListModels` against the live client's base
URL with a 5 s bound, the cursor lands on the live model, and Enter switches
through `applyModel` (live client plus the stored `config.json` model). The
dialog's right-aligned provider column names that one provider
(`internal/tui/dialog.go`; `handleModelsResult` / `applyModel` in
`internal/tui/tui.go`). Provider switching lives separately in `/providers`:
session-only, verified before anything becomes visible, with an inline key
modal for unconfigured rows (`internal/tui/providers.go`).

A user with keys for several predefined providers cannot compare or jump
across them: every list but the active one costs a `/providers` switch
first. On the `chatgpt` provider `/models` is worse than narrow — it fetches
the Codex backend URL, which has no OpenAI-shaped list route, and lands in
the dialog error path instead of offering the curated `ChatGPTModels`.

## User-visible behavior

1. Bare `/models` lists the models of **every configured provider** —
   API-key providers with a stored key, the `chatgpt` row with a stored
   sign-in — grouped under one **non-selectable section header per
   provider** showing that provider's display name:
   ```text
   Opencode Go
     <model id>
     <model id>
   OpenAI
     <model id>
     <model id>
   ```
   The active provider's section comes first, the rest follow in
   predefined-list order, reported order inside each section. Model rows
   show ids only — the headers replace the old right-aligned per-row
   provider column, so there is no column to drop on narrow terminals.
2. Dialog mechanics are unchanged except for the headers: the cursor starts
   on the live model row and never lands on a header; ↑/↓ with PgUp/PgDn
   move across model rows only, skipping headers. Typing filters model
   rows over model **and** provider text, so a provider name narrows the
   list to its section; a section (header included) shows if and only if at
   least one of its rows matches, so filtering never leaves an orphan
   header. Enter while anything is still loading does nothing, Esc clears
   the query first and then discards with nothing applied.
3. Enter on a row from the **active** provider keeps today's exact behavior:
   live switch, stored for later runs.
4. Enter on a row from **another** provider moves the session to that
   provider **and** model in one step: the next turn runs on the new pair,
   one conversation entry names both, and `config.json` stores the pair for
   future runs. Nothing is stored and the previous client keeps serving when
   activation fails.
5. A provider that errors or reports nothing contributes no rows. When at
   least one model is listed the dialog still opens, with a muted first line
   naming the unreachable providers. When nothing is listed the dialog shows
   the error, as today.
6. Providers without a stored key or sign-in are absent from the list —
   `/providers` stays the configure path and `/models` never asks for a key.
   The `chatgpt` row fetches the active account's eligible models through
   authenticated public SIWC model discovery when signed in. It never falls
   back to a curated entitlement list (see `chatgpt-plus`).
7. `/models` with no configured provider at all keeps today's refusal ("No
   provider configured; complete first-run setup first.") with zero network
   traffic.

## Errors

- Every fetch fails, or the combined list is empty → the dialog error path
  (`loadErr`); Esc closes; the session is untouched.
- Cross-provider activation fails (client construction) → the dialog closes,
  a visible error names the provider, the previous provider/model stay live.
  A dead key surfaces through behavior 5, at fetch time, not here.
- A corrupt `providers.json` reads as unconfigured for that row (the
  `/providers` dialog's existing stance); the failure surfaces in full only
  if the row is acted on.

## Non-goals

- No key entry inside `/models`; no unauthenticated probing of unconfigured
  providers to fill their rows.
- No custom-endpoint rows (`customEndpointsEnabled` stays off).
- No prefetching or caching: lists are fetched on every open, as today.
- No change to `/providers` (stays session-only) or to stored-config /
  session-table schemas.

## Acceptance criteria

- [ ] Bare `/models` groups every configured provider's models under its
      own non-selectable display-name header, active provider's section
      first, model ids only on the rows, with no per-row provider column.
- [ ] The cursor starts on the live model row and never lands on a header;
      navigation skips headers; typing filters over model and provider
      text with header visibility following its rows; the loading guard and
      Esc-discard behave as before.
- [ ] Enter on an active-provider row switches live plus stored, exactly as
      today.
- [ ] Enter on another provider's row moves the session to that
      provider+model, stores the pair, names both in one entry, and the next
      turn uses the new pair.
- [ ] A failed provider contributes no rows but is named in a muted note
      when other rows list; total failure shows the dialog error; Esc
      applies nothing.
- [ ] Unconfigured providers are absent; `chatgpt` contributes its authenticated
      account-specific list when signed in; no configured provider keeps today's refusal
      with zero network traffic.
- [ ] A failed cross-provider activation closes the dialog with a visible
      error and leaves the previous provider/model live.
- [ ] `go test ./...` from the project root passes with the feature's tests
      included; no mock-only test claims the dialog works.

Refines v1-spec FR-03 (reserved-command behavior) without amending it.
README command reference and CHANGELOG are updated at implementation time.
