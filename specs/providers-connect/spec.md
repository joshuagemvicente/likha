# Spec: Providers as connection manager (auth only, no activation)

**Status:** implemented (local) — automated suite green; real-TUI walkthrough outstanding.

## Context

Today selecting a row in `/providers` switches the live provider
(verify-then-activate, `internal/tui/providers.go`), and the dialog's
purpose is ambiguous with model picking. The product target mirrors
OpenCode's account flow: `/connect → authenticate → /models → select`.
Lisa keeps the `/providers` name; the dialog becomes a connection
manager: one row per predefined provider, Enter opens that provider's
auth surface, and **Enter never switches the live provider**. Provider
and model selection happen exclusively through `/models` (its rows
already move the session across providers, specs/all-models).

## User-visible behavior

1. Bare `/providers` opens the provider dialog exactly as today: every
   predefined provider, ✔ "connected" marker on rows holding a stored
   key/sign-in, "not configured" hint otherwise, `[connected]` on the
   live session's row, cursor on the live row, typing filters.
2. **Enter on a row opens its auth surface; nothing switches:**
   - API-key provider with no stored key → the masked key-entry modal
     (as today). The key is stored only after a connection check passes.
     Crucially, success **stores the key and returns to the dialog** —
     the live provider/model stay untouched.
   - API-key provider with a stored key → an auth-state view: provider
     name, where the key came from (private state directory, or env var
     key `LISA_<PROVIDER>_API_KEY` when both contexts resolve), and
     `Enter again to replace` / `Esc back`. A second Enter re-opens the
     key modal to replace the key (re-checked before storing).
   - `chatgpt` → auth-state view: signed in / not signed in; a not-signed-in
     row points at first-run setup or `lisa --provider chatgpt --device-login`
     (the switcher still cannot sign in interactively — the modal is for
     keys only). No swap.
3. The direct form `$ /providers <n-or-name> [key]` re-points at auth:
   with an argument it starts the auth surface for that provider; with a
   trailing key argument it stores that key after the check (flag-style
   non-interactive convenience), never switching. `//providers 2` never
   activates provider 2.
4. Activation is `/models`-only. Entering a model row from another
   provider keeps building the client from the stored credential and
   moving the session (specs/all-models §4) — unchanged.
5. Replacing a stored key re-verifies against the stored provider URL;
   failure keeps the OLD key stored and shows a visible error
   (the modal's existing check failure rule).

## Errors

- Key-modal check failure: nothing stored, dialog stays open, error
  visible (today's rule).
- Env-var-sourced providers show their source; when neither state-dir
  nor env carries a key the row is "not configured" as today.
- Schema/persistence unchanged (`providers.json`, `config.json`).

## Non-goals

- No provider-activation from this dialog (removed, not discouraged).
- No `omc`-style command-auth or browser OAuth for the key providers
  (chatgpt keeps its device-login flow).
- No new commands, no rename away from `/providers`; no
  stored-config/session-table schema changes.

## Acceptance criteria

- [ ] Enter on any `/providers` row never changes the live provider or
      model; the client, `config.json`, and session are untouched by
      navigation alone.
- [ ] Unconfigured API-key row → key modal; success stores the key,
      re-verifies, and stays in the dialog (row shows ✔ afterwards).
- [ ] Configured API-key row → auth-state view naming the key source;
      second Enter replaces the key after a passing check; a failed
      check keeps the old key and shows the error.
- [ ] `chatgpt` row → auth-state view only; not signed in points at
      setup/`--device-login`.
- [ ] `/providers <n-or-name> [key]` opens auth for that provider (and
      stores a given key after check) without switching.
- [ ] `/models` cross-provider switching and the first-run setup flow
      are unchanged; `go test ./...` green with the feature's tests.
