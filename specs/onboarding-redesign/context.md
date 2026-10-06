# Context — Onboarding redesign

Code and tests this feature touches (verified 2026-10-04).

- `internal/tui/setup_view.go` (new): the whole setup page: layout, step
  indicator, stage bodies, list rows, the framed key field, the theme preview,
  the pinned footer, and span-based row assembly (clip first, style once).
- `internal/tui/setup.go`: setup state (`themeCursor` split from `cursor`,
  `filter`, `envKey`, spinner `spin`/`ticking`), key handling, filtering,
  `setupAPIKey` (typed key, else `KeyEnv`), `setupDefaultModel`, the
  completion notice. The old `setupView`/`setupLines`/`setupActions` are
  removed.
- `internal/tui/tui.go`: `setupCheckMsg` (failure returns to `setupKey`;
  default-model preselect), `oauthLoginMsg` (preselect), `setupTickMsg`.
- Reused: `windowList`, `wrapWords`, `withBase`, `toolClipCells`,
  `hiddenReviewRune`, `humanTokens`, `activitySpinner`, `logo`,
  `likhaui.BlockGlyphs` (box corners, `⏺ ⎿ ✗`), theme roles.
- Runtime key resolution is unchanged: `internal/providers/provider.go`
  already reads `KeyEnv` before `providers.json`, which is what lets an
  environment key stay out of the file.
- Tests: `internal/tui/setup_view_test.go` (new); `tui_test.go` setup tests
  updated for the new copy and the stay-on-key-field behavior (assertions
  kept or strengthened).
