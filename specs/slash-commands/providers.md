# Spec: `/providers` — live provider switching; retire `/model <n-or-id>`

**Status:** implemented (local). Extends
[spec.md](spec.md) (slash commands) and v1-spec FR-02/FR-03.

## 1. Overview and rationale

Every agent IDE and terminal agent exposes in-session provider switching:
the user picks which provider this terminal talks to, without leaving the
TUI and without editing config files. Lisa today has the storage (one API
key **per** predefined provider in `providers.json`, `keyfile.go`) but only
one activation path: first-run setup / `--provider` / `LISA_PROVIDER`, fixed
at launch and written into `config.json`. Two gaps follow:

1. A user with keys for several providers cannot switch mid-session.
2. The ability to change providers is invisible inside the TUI — the
   provider line is display-only.

This feature adds `/providers`: a selection dialog (same modal machinery as
`/themes`, `/models`, `/sessions`) listing all predefined providers with
their configured/not-configured state, filterable by typed query, with
inline key entry for unconfigured providers. In the same change, the typed
`/model <n-or-id>` command is removed: model selection stays with `/models`
(list + dialog), and provider switching is the natural place to land on a
model, so two overlapping live-switch commands are not kept in sync.

## 2. Resolved decisions (user-confirmed, 2026-09-29)

| Decision | Choice |
| --- | --- |
| Model-selection surface | Keep `/models`; **remove** `/model <n-or-id>` entirely |
| `/providers` persistence | **Session-only** — live switch only, `config.json` untouched; setup flow remains the way to change the stored default |
| Provider with no stored key | **Inline key prompt** — a dedicated modal focused on that provider's API key |
| Custom endpoint (`--endpoint`/`LISA_ENDPOINT`) | **Implemented but hidden** — plumbing exists, not surfaced in `/providers` yet |
| Interaction pattern | **Shared selection modal** with a typed **query filter** for provider names |

## 3. `/providers` command semantics

| Invocation | Behavior |
| --- | --- |
| `/providers` | Opens the selection dialog (below). |
| `/providers <n-or-name>` | Opens the dialog pre-filtered is **not** spec'd; instead: resolves like `/themes` does — a number indexes the predefined list shown by the dialog, an exact canonical name applies directly. An unresolvable argument restores the draft and shows the error with `/providers` usage. |
| During active run or pending approval | Inert, same rule as every other command: the Enter key is already ignored while working; no command can interrupt a review. |
| `//providers` | Escape: sent to the model as a literal prompt (unchanged). |

Asymmetry note: `/providers <name>` applying directly (skipping the dialog)
is included for parity with `/themes`. Keep it; it costs nothing beside the
dialog path and is already the established pattern.

## 4. Selection dialog

Reuses `dialogState` (tui.go:103) with a new kind `dialogProviders`:

- **Items** — one row per predefined provider in `model.Providers` order,
  each showing: display name, canonical name, and configured state:
  - `✔ <DisplayName> (<name>)` when `storedKey(stateDir, p.Name)` is
    nonempty — a **key exists on disk**; whether it still works is not
    known until the connection check on switch (§6).
  - `<DisplayName> (<name>) — not configured` otherwise. Selectable, not
    hidden (§2: inline prompt, not hiding).
- **Active marker** — the current session's provider is marked (cursor also
  starts there, mirroring the themes dialog's behavior of starting on the
  applied theme).
- **Query filter (new capability)** — while a dialog is open, printable
  characters typed extend a query; Backspace deletes. Matching is a
  case-insensitive substring match against **both** display name and
  canonical name (`opencode` matches "Opencode Zen" and "Opencode Go";
  `openai` matches "OpenAI" and "OpenRouter"). Rules:
  - Cursor resets to the first visible match whenever the query changes;
    the active provider is not re-selected by filtering.
  - Empty match set renders a "No providers match <query>" row; Enter does
    nothing while the set is empty.
  - The query renders on the dialog's title or header line, e.g.
    `Select provider — q: "op"`; Esc clears the query first if nonempty,
    then closes the dialog on a second Esc (same double-retreat the setup
    flow uses).
  - **Conflict check passed:** in normal mode the input line is not used by
    the dialog (keys are intercepted while `m.dialog.open`), so printable
    keys are free to become the query. Arrow/PgUp/PgDn/Enter/Esc semantics
    are unchanged; the query applies to all dialog kinds, so `/models` and
    `/sessions` dialogs gain filtering too — consistent, one code path.
  - Filter operates on the **in-memory list only**; it never re-queries a
    provider or the network. Sessions dialog: query matches titles. Models
    dialog: query matches model IDs and only applies once the list has
    loaded (while `loading`, typing is captured but has no visible effect
    until rows exist).
- **Live models list while switching provider** — not part of this dialog.
  Selecting a provider switches the provider; the user then runs `/models`
  (or uses the new provider's default model). One dialog, one decision.

## 5. Key-entry modal (not-yet-configured provider)

Selecting a row whose provider has no stored key does **not** switch
immediately. It opens a key-entry modal focused on that provider:

- Reuses the first-run setup's masked input machinery (setupKey stage
  behavior: paste dots, Enter submits); the modal is dedicated, not a
  setup-stage reuse in place.
- Header names the provider: `API key for <DisplayName> — paste, Enter to
  check, Esc to cancel`. Cancel returns to the provider dialog with nothing
  changed.
- On Enter: store via `storeKey(stateDir, p.Name, key)` (0600,
  `providers.json`), then run the connection check (same shape as the
  setup flow's check: base URL, key, list models). On failure the modal
  shows the error and keeps the entered key in the field for correction;
  the key is only persisted after the check passes — matching the setup
  flow's store-after-verify order.
- After success: build the live client for that provider (§6) and close
  back to the conversation view with a confirmation entry:
  `Provider switched to <DisplayName> for this session.`

This modal is a whole screen like the other dialogs; Esc is the only exit
besides success.

## 6. Switching mechanics (live only)

Selecting a provider (or completing its key modal) performs, in order:

1. **Build client** — construct the `model.Client` for the selected
   provider: its `BaseURL`, the stored/delivered key, and its
   `DefaultModel` when the user has not overridden a model; the user's
   stored config (`config.json`) is **not** read or written.
2. **Verify** — connection check against the provider before the switch
   becomes visible; failure leaves the previous provider active and shows
   the error in the conversation view (the same guarantee the startup
   check makes: a dead/revoked key never silently strands the session).
   The key modal path verifies before storing (§5); the switch-path
   verification errors are surfaced identically.
3. **Activate** — swap `m.client`, set `m.modelName` (checked provider
   default or carried model), reset dialogs/stream buffers, append the
   confirmation entry, update the footer status to the equivalent of
   startup's `Connected`.
4. **Header** — the `Provider:` line and `Model:` line reflect the new
   session state immediately.

Explicitly **not** done by `/providers`:

- Writes `config.json` (session-only; setup remains the defaults path —
  contrasts with `applyModel`, which persists, and that difference is
  intentional).
- Touches `providers.json` except through the key modal (§5).
- Mid-run switching: commands are inert during a run or pending approval;
  the dialog cannot open while `m.pending != nil` or the model is working.

### 6.1 Custom endpoints — built now, hidden

The plumbing for unlisted OpenAI-compatible endpoints is prepared but not
surfaced: a reserved internal dialog kind of source (custom-endpoint rows)
and the client-build path for an arbitrary base URL exist behind an
internal flag so §6's build/verify/activate steps do not special-case
"predefined only" later. **Not visible:** no `/providers` row, no in-TUI
URL editing, no argument accepting a URL. Enabling it later is a removal
of a gate, not new plumbing. The `LISA_ENDPOINT` flag/env startup path is
unchanged and remains exactly as broad as today.

## 7. Removal: `/model <n-or-id>`

- The typed command is gone: `/model …` becomes an unknown command —
  visible error, draft restored, never sent to the model (existing
  unknown-command handling applies verbatim).
- Deleted: `switchModel` (tui.go:707) and the `/model <n>` argument path.
  Kept: `applyModel` — the `/models` dialog's Enter still applies the
  highlighted model, exactly as today, including its current persisting
  behavior (`config.json` model field). That persisting behavior is
  pre-existing and out of this feature's scope; if it should also become
  session-only, that is a separate decision recorded under Open questions.
- `commandHelp` (tui.go:447) drops `/model <n-or-id>` and gains
  `/providers`; the hint line above the models list
  ("Provider models (switch with /model <n> or /model <id>)") is rewritten
  to reference dialog-Enter selection, since no typed `/model` remains.
- README command reference updated in the same change.

Removing the typed path tightens the model surface to one mental rule:
**dialog navigation selects; typed commands never index**. `/themes <n>`
keeps its number semantics (parity with `/providers <n>`); `/models`
resolves the inconsistency the removal creates: its number-remembering
purpose (`lastModels`) becomes dialog-internal numbering instead of a
cross-command memory.

## 8. Acceptance criteria

- [ ] `/providers` opens the dialog; rows show every predefined provider
      with configured/not-configured state; cursor starts on the active
      provider.
- [ ] Query typing filters case-insensitively across display and canonical
      names for all dialog kinds; Esc clears the query before closing;
      empty matches cannot be selected.
- [ ] Selecting a configured provider rebuilds and verifies the client;
      verification failure keeps the previous provider active with a clear
      error.
- [ ] Selecting an unconfigured provider opens the key modal; submit stores
      the key only after the connection check passes; cancel leaves
      provider and providers.json untouched.
- [ ] `/providers <n>` and `/providers <canonical-name>` apply directly;
      bad arguments restore the draft and print usage.
- [ ] `config.json` is byte-identical before/after any `/providers`
      switch; the footer, header, and live client all reflect the new
      provider.
- [ ] `/model`, `/model 2`, `/model gpt-4o-mini` all produce the
      unknown-command error and never reach the model.
- [ ] `/models` dialog Enter still switches the live model (behavior
      unchanged); its hint text no longer mentions `/model`.
- [ ] During an active run or pending approval, `/providers` is inert and
      the dialog cannot open; Esc/Approve/Decline in a review behave
      identically with the dialog closed.
- [ ] Custom-endpoint internals compile and are covered by a build-time
      test, but no user-visible surface references them.

## 9. Resolved open questions

1. **`applyModel` persistence:** kept as-is — the `/models` dialog's Enter
   still writes the model to `config.json`. This was left unchanged
   intentionally: provider switching via `/providers` is session-only, so
   the two surfaces persist differently and that asymmetry is documented
   rather than "fixed" by this feature.
2. **Model carry-over across providers:** none. A switch lands on the new
   provider's documented `DefaultModel`, or — when no default exists — on
   the first model that provider's verification check reports. IDs are
   never assumed compatible across providers.
3. **`/providers <n>` indexing:** indexes the fixed predefined list order —
   position-stable on every call, independent of any dialog filter state
   the user may have typed.
