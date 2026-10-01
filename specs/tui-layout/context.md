# Context: TUI layout adjustment

Phase 1 code (all verified present and exercised by the current suite).

| Path | Relevance |
| --- | --- |
| `internal/app/tui.go` | `header()` now returns `nil` at ≥56 cols and one `Lisa · <basename>` line below; `rebuild()` wraps per rune at `m.width-2`, treats the `Logo` entry as a raw never-persisted block, and skips it below 56 cols; `bodyHeight()`/page math tolerate the zero-line header; `autoNameAfterFirstTurn` + `nameGeneratedMsg` drive the one naming call after the first completed turn of a fresh session and apply `snapshot.NamedTitle`. |
| `internal/app/status_line.go` | `statusIdentity` (provider + display-name model at bottom), `statusLeft` (gated optional segments, no wordmark — it moved right), `statusLineOpts` segment gates (`flagEnabled` for the `*bool` Folder/Branch pair), `statusFolder`, `contextSegment` (`ctx 34% · 68k/200k` wide, `ctx 34%` narrow, `ctx —` when unmeasured/unknown), `envSegment`, `spendSegment`, `tokensSegment`, `statusMarkWidth`/`renderStatusRow` (bottom-right Lisa mark, ≥70 cols), `refreshStatusSessionTitle`/`statusSessionTitle` (returns `NamedTitle` when set, else derived). |
| `internal/app/status_sources.go` | `gitStatus` — ONE bounded (3 s) `git status --porcelain -b` parsed by `parseGitStatus` into `gitState`{Branch, Detached, Ahead, Behind, Staged, Dirty, Untracked}; `detachedHead` reads the raw `.git`/HEAD only when the porcelain header says detached; `statusSessionTitle`/`singleLineTitle` for the derived title. `currentBranch` and `gitStatusCounts` are gone. |
| `internal/model/naming.go` | Curated display-name catalog + `ModelDisplayName(modelName)` with `catalogSlug` fallback (exact IDs, then family prefixes; shared matching rule with the pricing table). |
| `internal/model/pricing.go` | `TurnCost(model, prompt, completion)` per-Mtok pricing with `ok=false` → hide; ChatGPT subscription rows price at exactly 0 (`$0.00` rendered, never fabricated for unknown models). |
| `internal/app/sessionname.go` | `generateSessionName(ctx, client, history)` (fire-and-forget stream, no transcript entry) and `sanitizeSessionName` (2–6 words, quotes/periods stripped, rejects junk). |
| `internal/app/provider.go` | `resolvedProvider.providerCanonical` (e.g. "chatgpt") — drives the subscription `$0.00` row and the naming model choice. |
| `internal/app/config.go` | `storedStatusLineConfig` with `Folder`/`Branch` as `*bool` (`nil` = default-on) and `flagEnabled` as the single resolution point. |
| `internal/app/run.go` | Bare ASCII `logo` constant (no repository line); builds the fresh `ui` with the flipped defaults. |
| `internal/session/session.go` | Snapshot `NamedTitle` storage; naming writes through the existing Save path. |
| `internal/app/status_view_test.go`, `status_line_test.go`, `status_sources_test.go`, `sessionname_test.go` | The automated phase-1 coverage: header collapse, pure logo, mark placement/retirement, ctx formats, display name, env, git-state rendering/parsing, spend pricing, pointer defaults, and the four naming tests (apply+persist, silent failure, resumed-skip, exit-before-completion). |
| `internal/ui/theme.go` (moved from `ui/` by structure-refactor Phase 1) | Seven style roles (`Named`/`Resolve`/`fromPalette`, `HasDarkBackground`), per-family light/dark palettes — phase 3 adds `BgBase`/`BgUser`/`BgTool`/`BgModel` here plus `internal/ui/adaptive.go` (`mix`, `legible`, AdaptiveColor constructors) per adaptive-themes M2. |
| `specs/v1-spec.md` | FR-12 and §7 amended before implementation (zero-line header, pure logo, bottom-right mark, narrow identity line). |

## Related specs

- `specs/themes/spec.md` — theme role conventions phase 3 must extend.
- `specs/prompt-editor/spec.md` (planned) — composer behavior overlap; phase 2 composer wrapping must not conflict.

## Facts that shape what remains

- `wrap()` hard-breaks per rune (zero-width/control runes escaped), so body
  content cannot overflow today; the raw `Logo` entry is the one
  pre-formatted exception and it is fixed-content ASCII.
- Resize re-layout already exists (`layoutWidth = 0`), so breakpoints are a
  pure function of `m.width` at rebuild time — phase 2 is a pure
  render-path audit.
- Phase 3 entry styling lands in `rebuild()`'s role switch (`internal/app/tui.go:1415-1430`; `"You"`/`"Assistant"`/`"Tool"`/`"Reasoning"`/`"Error"`/`"Lisa"`/`"Logo"` — role strings in `tui.go:244,309,768,776,795,839`), mapped to the new band roles; full-width bands need no layout math (`mainView` already renders `style.Render(fit(line, width))`); the phase-2 overflow suite is the regression gate. Status row count depends only on width (`statusLineRows`), so phase 3 background bands cannot shift page boundaries. `specs/adaptive-themes/spec.md` is the buildable contract: M1 live preview (`updateDialog`/`confirmDialog` at `tui.go:295,358`, `applyTheme` at `tui.go:406`, `applySetupTheme` in `setup.go:270`), M2 adaptive helpers, M3 band map.
- Phase 1's only unvalidated surface is real-terminal rendering (mark
  position, narrow-degradation glyph widths, scrollbar absence); the
  README/CHANGELOG wording already matches the tested behavior.
