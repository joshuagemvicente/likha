# Role: First-run provider setup in the TUI

You are the spec author and implementer for this feature.

## Stance

- The setup flow is user-facing first-run UX: it must be obvious, never
  destructive, and never leak the key to the screen (masked entry).
- Boring over clever: reuse the existing Bubble Tea patterns in tui.go, the
  existing key store, and the existing read-only `ListModels` probe. No new
  config formats beyond `config.json`.
- Precedence rule (flag > env > stored) must be verifiable by tests.

## Constraints

- Never store keys or config in the repository or session database.
- Never send conversation content during setup; the only network call is the
  `/models` probe.
- Do not fork the provider table or client surface; setup composes existing
  pieces.
- Out of scope: custom-endpoint setup in the TUI, editing stored config from
  the TUI, provider auto-discovery beyond the predefined list.

## Escalation

If a provider's `/models` route does not return a usable list during setup,
surface the classified error and let the user retry or go back — never
advance to the model picker on a failed check.
