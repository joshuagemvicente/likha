# Context — Transcript redesign

Code, tests, and docs this feature touches (verified 2026-10-04).

## Code

- `internal/tui/view.go` — `entry` (:16), `rebuild()` role→style switch
  (:143-162), the `role+": "` label (:177), `wrap()` (:465), `mainView`
  assembly (:218-275), `withBase` (:360), swatches (:328).
- `internal/tui/tools_view.go` — `RenderToolSummary` (:601-636),
  `toolEntryContent` (:692), `inspectableToolEntries` (:698), focus/scroll
  (:780-790), `toolEntryStartLine` (:828-830, duplicates the label
  arithmetic), inspector (:837+), key handling (:161-235).
- `internal/tui/tool_wiring.go` — `recordToolResult` (:180-215).
- `internal/tui/tui.go` — entry creation: `You` (:375, :884), `Queued`
  (:429, :470), `Reasoning` (:800), `Assistant` (:809), `Tool` (:833, :941),
  persistence filter (:1700-1704), `contentWidth`, `minWidth/minHeight`.
- `internal/tui/task_wiring.go` — `acceptTaskRecord` (`Agent` entries).
- `internal/agent/turn_execution.go` — `Request: <name> <args>` (:291) and
  `<name>: <content>` (:196) event text.
- `internal/tui/composer.go` — `composerStyles`, `composerLines()`.
- `internal/tui/status_line.go` — unchanged; `Page n/N` hint (:77).
- `internal/ui/theme.go`, `internal/ui/adaptive.go` — roles, `mix()`,
  `legible()`, band ratios (:32-40), `default` theme (:33-44).
- `internal/ui/glyphs.go` — Nerd/ASCII status glyphs (pattern for the new
  block glyph set).
- `internal/app/run.go` — `--nerd-fonts`/`LIKHA_NERD` (:159, :378): pattern
  for `--ascii`/`LIKHA_ASCII`.
- `internal/session` — `Entry{Role, Content}`, `ToolRecord`.

## Tests pinning current rendering (to rewrite to glyphs)

- `internal/tui/bands_test.go` — `TestBandRolesAcrossThemes`,
  `TestDefaultBandsAreNoOps`, `TestDegradationAcrossProfiles`,
  `TestDegradedBandsKeepWidths`.
- `internal/tui/tui_muted_tools_test.go` (6 label checks),
  `models_sessions_dialog_test.go:374`, `status_markers_test.go:80`,
  `tui_test.go:540`.
- Overflow invariants: `composer_overflow_test.go`, status/view tests.

## Specs amended

- `specs/adaptive-themes/{spec,role,checklist}.md`,
  `specs/tui-layout/spec.md`, `specs/themes/spec.md`,
  `specs/tool-rendering-terminal-keys/{spec,role}.md`, v1-spec FR-03/FR-15
  notes.

## Docs

- `README.md` (transcript/key sections, `--ascii`), `CHANGELOG.md`,
  `--help`.

## Research sources

- OpenCode `packages/tui/src/routes/session/index.tsx` (UserMessage,
  InlineTool, BlockTool, ReasoningPart, Task), `ui/border.ts`,
  `theme/assets/opencode.json` — anomalyco/opencode@907b3bc5.
- oh-my-pi `packages/tui/src/chat/{user-message,assistant-message,tool-execution}.ts`,
  `render/{status-line,output-block,tool-card,render-utils}.ts`,
  `tools/{grep,task,edit}.ts`, `theme/dark.json` — can1357/oh-my-pi@d4d49e71.
- Claude Code: https://code.claude.com/docs/en/tools-reference and observed
  behavior (unconfirmed details marked in spec § Research).
- goldmark: https://github.com/yuin/goldmark · chroma:
  https://github.com/alecthomas/chroma
