# Feature: First-run provider setup in the TUI

**Status:** in progress

Presentation and setup UX are refined by [onboarding-redesign](../onboarding-redesign/spec.md) (2026-10-04); the decisions below still bind.

## Context

Likha is a BYOK agent harness: it hosts no models and talks directly to the
provider's OpenAI-compatible API route with the user's key. Until now the
provider and key had to arrive through CLI flags or environment variables
(`--provider`, `--api-key`, `LIKHA_*`). That is developer-hostile for a first
run. This feature makes Likha prompt for it: launch with no configuration and
the TUI walks the user through provider selection, masked API-key entry, a
connection check, and model selection, then stores the choice in the private
state directory so the next launch is zero-configuration.

This refines FR-02 (v1-spec.md); it does not change the model client, tools,
approval flow, or session storage.

## Resolved decisions

1. **Trigger:** setup starts when no provider is configured anywhere — no
   `--provider`, no `LIKHA_PROVIDER`, no stored config, no `--endpoint`/`LIKHA_ENDPOINT`.
   Anything configured skips setup entirely (flags and environment win over
   stored config).
2. **Non-interactive safety:** when stdin/stdout is not a TTY and no provider
   is configured, Likha exits 2 with a clear error instead of starting setup.
   `--sessions` and `--resume` never require setup.
3. **Stages:** provider picker (arrow keys + Enter) → masked key entry →
   connection check against the provider's `/models` route (bounded, read-only)
   → model picker from the returned list (a single model auto-selects). Esc
   goes back one stage; Ctrl+C/Ctrl+D quit. *(All predefined providers are
   hosted since the provider-only pivot; every selection asks for a key.)*
4. **Persistence:** provider + model go to `<stateDir>/config.json`; the key
   goes to `<stateDir>/providers.json` (both mode 0600, private state
   directory, never the repository or session database).
5. **Precedence after setup:** flag > environment > stored config. Stored
   config only supplies what was not given explicitly. There is no built-in
   local default; an unconfigured run always starts setup (interactive) or
   fails clearly (non-interactive).
6. **Re-running setup:** stored config is used until the user overrides it
   with flags; editing `config.json` (or removing it) re-triggers setup.

## Functional changes (wording to apply in v1-spec.md)

- **FR-02** gains: "If no provider is configured, the interactive TUI offers a
  first-run setup: provider selection, masked API-key entry, a connection
  check, and model selection; the choice is stored in the private state
  directory."

## Acceptance criteria

- [ ] Bare `likha` with no configuration opens the setup flow in an
      interactive terminal; a second launch after completing it starts
      straight into the conversation with the stored provider/model.
- [ ] Flag and environment settings skip setup and override stored config.
- [ ] Key entry is masked on screen and stored 0600; wrong keys produce the
      classified connection error and allow retry (Esc back, retype).
- [ ] Model picker lists what the provider reports; one model auto-selects.
- [ ] Non-interactive invocations without configuration fail clearly.
- [ ] `--sessions`/`--resume` work with or without stored config.
