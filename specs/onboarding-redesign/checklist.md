# Checklist — Onboarding redesign

Observable outcomes. Automated coverage: `internal/tui/setup_view_test.go`
and the setup tests in `internal/tui/tui_test.go`.

## Automated (passing 2026-10-04)

- [x] Every stage fits exactly at 40×12, 60×24, 80×24, 120×40, Unicode and ASCII.
- [x] Under a color profile no row contains an escaped `\u001B`, and the footer is the last row.
- [x] Model IDs and error text with control runes render escaped, never raw.
- [x] The step indicator shows words in every form (full, narrow, ASCII).
- [x] Theme navigation never changes the stored provider; Esc restores the committed theme.
- [x] The theme cursor starts on the current theme.
- [x] Provider and model filtering, Esc-clears-first, Enter on no match does nothing.
- [x] The provider's default model is preselected and marked `recommended`.
- [x] An environment key is offered, checked, and not written to `providers.json`.
- [x] A failed check returns to the key field with the key kept; typing clears the error; Enter re-checks.
- [x] The spinner ticks only while a check or sign-in is in flight.
- [x] Completion leaves the `Connected to …` notice.

## Interactive walkthrough (pending)

- [ ] Bare `likha` with no config, dark and light terminal: the logo, steps, and highlight look right in `default` and two color themes.
- [ ] Paste a real key (bracketed paste) and confirm the character count matches.
- [ ] A wrong key shows the error in place; fixing it and pressing Enter succeeds.
- [ ] With `LIKHA_<PROVIDER>_API_KEY` exported, setup completes without typing a key and `providers.json` has no entry for it.
- [ ] OpenRouter: filtering the model list stays responsive.
- [ ] Resize during each stage; nothing overflows or flickers.

## Open question for the user

- Should the theme step move first (as Claude Code does), so the rest of
  setup is shown in the chosen theme? It changes first-run-setup decision 3's
  order, so it needs approval.
