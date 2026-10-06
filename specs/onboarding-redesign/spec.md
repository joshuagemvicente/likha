# Feature: Onboarding redesign (first-run setup presentation and UX)

**Status:** implemented (local, uncommitted): interactive walkthrough pending

Refines [first-run-setup](../first-run-setup/spec.md). The trigger, the stages
(provider → key/sign-in → check → model → theme), persistence, and precedence
stay exactly as that spec resolves them. This spec changes how setup looks and
fixes the defects found in its review. It changes nothing that is sent to a
provider and nothing in the session database.

## Context

A review on 2026-10-04 rendered every setup screen at several sizes and in
color. Findings:

| # | Finding | Kind |
| --- | --- | --- |
| 1 | The header row printed a literal `\u001B[1;94m` in any color terminal: `frame()` re-fits rows and escapes control runes, and the header was already styled. | bug |
| 2 | The provider list windowed by provider count but drew two rows per provider, so on terminals under 38 rows it pushed the footer and key hints off-screen. | bug |
| 3 | `setup.cursor` was shared by the provider list and the theme list. Esc from the theme stage back to the model stage, then Enter, made `finishSetup` read `Providers[themeIndex]`, storing the key and config under the wrong provider. Esc also left the previewed theme applied. | bug |
| 4 | A failed connection check stayed on a "Checking…" screen; the user had to press Esc to get back to the key field. | UX |
| 5 | The selected row was not highlighted (only `> `); the selected provider's URL was indented differently from the rest. | UX |
| 6 | No brand, no progress indication, no spinner, raw full base URLs, duplicated `Esc back` hints, and a `SETUP` status row with no information. | UX |
| 7 | No way to filter a long model list (OpenRouter reports hundreds), the provider's default model was not preselected, and context windows already fetched were not shown. | UX |
| 8 | A key already exported in the provider's `LIKHA_*_API_KEY` variable was ignored; the user had to paste it again, and it was then copied to disk. | UX |
| 9 | The theme stage had nothing styled on screen, so the "live preview" barely showed anything. | UX |

## User-visible behavior

### Page layout

- Full-screen page on the theme canvas. Content sits in a column at most 76
  cells wide, centered, with at least a 2-cell margin.
- From the top: the ASCII logo plus "Welcome to Likha. Let's connect a model."
  (30+ rows), or a one-row `Likha · first-run setup` wordmark; a step
  indicator; the stage title (`Title`) and a one-paragraph explanation
  (`Muted`); the stage body.
- The key-hint footer is pinned to the last row: keys in `Help`, actions in
  `Muted`. Hints drop from the end when the row is too narrow (the most
  important come first).
- Short terminals (< 20 rows) drop the top padding, the wordmark, and the
  explanation. The minimum size stays 40×12, and no row overflows at any size.

### Step indicator

`✓ Provider ── ● Connect ── ○ Model ── ○ Theme`: done steps use `Success`
for the glyph, the current step `Title`, upcoming steps `Muted`. Step 2 reads
`Sign in` for OAuth providers. When it does not fit: `Step 2 of 4 · Connect`.
ASCII (`--ascii`): `+`, `*`, `-`, `--`. Every step has a word, so progress
never depends on color.

### Lists (provider, model, theme)

- One row per item: `> ` marker plus label, details right-aligned. The cursor
  row uses the theme's selection tint (`Selected` background; the `default`
  theme falls back to its neutral user band) and `Selected` for the label.
- Provider details: the host (`Muted`), `browser sign-in` (`Accent`) for
  OAuth, or `key found in LIKHA_…` (`Success`) when that variable is set.
- Model details: `128k context` when the provider reported a window, and
  `recommended` (`Accent`) on the provider's `DefaultModel`, which is also
  preselected. On narrow rows details give way from the front, so
  `recommended` outlasts the context size.
- **Type to filter** (provider and model): typing filters by substring
  (case-insensitive; providers match display name, canonical name, or host).
  A `/` row shows the query and `N of M`. Backspace and Ctrl+U edit it; Esc
  clears an active filter before going back; Enter does nothing when no row
  matches. PgUp/PgDn page by the visible rows; Home/End jump to the ends.

### Key entry

- A rounded `API key` field (`Accent` border) shows the masked key (`•`,
  `*` in ASCII; `…` plus the tail when long) and a block caret, plus a
  `N characters` count that confirms a paste landed without revealing the
  key. Ctrl+U clears the field.
- **Environment key:** when the provider's `KeyEnv` is set, the empty field
  reads `press Enter to use $LIKHA_…` and the footer reads `enter use env key`.
  That key is checked like a typed one and is **not** written to
  `providers.json`; later runs resolve it from the environment as before. A
  typed key still wins and is stored as before.
- While checking: the field border turns `Muted` and an ASCII spinner reads
  `Checking the key with <host>…`.
- On failure: setup returns to the key field with the key kept and shows
  `✗ <classified error>` (`Error`) plus "Fix the key and press Enter to try
  again, or Esc to pick another provider." Typing clears the error.

### ChatGPT sign-in

Selecting ChatGPT automatically opens the system browser; there is no second
launch action or device code. While waiting, show a spinner and browser
sign-in progress. A manual authorization URL appears only if the browser
launcher fails. Listen on `127.0.0.1` at an available port, with the exact
callback path retained, and bound the attempt to five minutes. Esc and quit
cancel the listener; a retry starts a fresh attempt and ignores late results.
After validated identity and plan consent, fetch the account's eligible models
and confirm shared ChatGPT plan usage. The current authentication contract is
[chatgpt-plus](../chatgpt-plus/spec.md), superseding the historical Codex flow.

### Theme

The theme list beside a `Preview` box: a short sample transcript (user band,
tool dots, muted results, diff tints, inline code, an error) painted in the
highlighted theme, while the whole page restyles live. The cursor starts on
the current theme. The preview is dropped when the column or height cannot
hold it. Esc returns to the model list and restores the committed theme.

### Completion

Applying the theme opens the conversation with one `ℹ` notice:
`Connected to <Provider> · <model>. Change these later with /providers,
/models, and /themes.`

## Non-goals

- No change to stage order, persistence files, precedence, or the
  non-interactive behavior.
- No per-provider "get a key" URLs (they need verified links per provider;
  candidate follow-up).
- No theme-first ordering (candidate follow-up; see the open question in
  `checklist.md`).
- No new dependencies, theme roles, or glyph sets.
