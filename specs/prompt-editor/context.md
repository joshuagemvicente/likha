# Context — Prompt line editor, native selection, image attachments

Code, tests, and docs this feature touches.

## Code

- `internal/app/tui.go` — input handling (`m.input []rune`, backspace, Enter,
  command dispatch), rendering (`Draft:` body line, footer rows), Update
  dispatch order, `tea.MouseMsg` branch (to be removed).
- `internal/app/run.go` — `tea.NewProgram(..., tea.WithMouseCellMotion(), ...)`
  (to remove the option), `--help` usage text.
- `internal/model/client.go` — `Message`, `requestMessage{Content string}`,
  `Stream` (chat-completions builder), `streamCodex` (Responses builder):
  both need multimodal content parts; text-only marshaling must not change.
- `internal/app/clipboard.go` (new) — per-platform clipboard bridge with an
  injectable command runner.
- `internal/session/session.go` — `Snapshot`/`Entry`: attachment metadata for
  persistence; blobs live beside the database in the private state directory,
  never inside SQLite.
- `specs/feature-test-plan.md` — the manual walkthrough record style M2/M3
  follow.

## Tests

- `internal/app/tui_test.go`, `internal/app/models_sessions_dialog_test.go` —
  existing Update/View test patterns to mirror.
- `internal/model/client_test.go` — payload assertion patterns to extend for
  content parts on both wire builders.
- `tests/integration/` — end-to-end entry point conventions.

## Docs

- `README.md` — TUI requirements section (wheel paging wording), limitations
  (image resend cost), command reference.
- `CHANGELOG.md` — unreleased entries per milestone.
- `specs/v1-spec.md` — FR-14 amendment, FR-17/FR-18 (done in this spec).

## In-flight code that interacts

- The provider dialogs (`internal/app/providers.go`, dialogs in `tui.go`)
  share the editor's Update dispatch; new bindings must not shadow dialog key
  routing (dialog keys are consumed before prompt handling — keep that order).
- `internal/model/oauth.go` / `codex.go` (parallel work): `streamCodex`
  changes coordinate with the codex surface.
