# Feature: Transcript redesign (Claude Code–style blocks, markdown, live tool items)

**Status:** planned

Refines v1-spec FR-03 (the TUI distinguishes assistant text, tool requests,
tool results, approval requests, and errors) and FR-15 (themes, muted
reasoning, opt-in Nerd Font icons). Supersedes, by user approval on
2026-10-04, these earlier rules:

| Earlier rule | Where | Replaced by |
| --- | --- | --- |
| Role labels (`You:`/`Assistant:`/`Tool:`/…) must remain; backgrounds never carry meaning alone | adaptive-themes M3 + role.md + checklist; tui-layout FR-15 note | Every block carries a **distinct non-color glyph** (§ Glyph language); backgrounds still never carry meaning alone |
| Assistant on `BgModel`, Tool on `BgTool`, request/result told apart by prefix text | adaptive-themes M3, themes decision 6, tui-layout phase 3 | Only the user prompt has a band; assistant and tools sit on the base canvas; request + result are **one item** |
| `default` family keeps the plain terminal look (bands are no-ops) | themes decision 6, adaptive-themes | `default` gets a faint neutral user band |
| No animation; no new theme colors; no tool-output collapse/expand; no in-tool content rendering | tool-rendering-terminal-keys Non-goals + role.md; adaptive-themes role.md | A blinking running dot, derived success/diff tints, bounded inline previews with `ctrl+o to expand`, rendered markdown and diffs (all defined below) |

Everything not listed above stays in force: minimum size 40×12 and the
narrow-width breakpoints, the status line, the inspector, paging, the review
flow, Nerd Font icons as opt-in status markers only, reasoning never sent
back to the provider, and limited-profile degradation to legible plain text.

## Context

The current transcript prints `You: …`, `Assistant: …`, `Tool: Request: grep
{json}` and `Tool: grep · limited · [Likha built-in] · …` as label-prefixed,
character-wrapped text on per-role bands; assistant markdown shows raw `##`,
`**`, and backticks; one explore task logs a line per status change. The user
compared this with Claude Code, OpenCode, and oh-my-pi (research recorded in
§ Research) and chose, in a 2026-10-04 design interview, the Claude Code
layout with a user-prompt background band, rendered markdown with syntax
highlighting, and single-item tool calls. Every decision below was confirmed
in that interview.

## Goals

1. Remove role labels; carry block identity with glyphs.
2. Make the user's own prompts the one visually banded block.
3. Show each tool call as one item: what ran, how it ended, and a bounded
   preview — details stay one key away in the inspector.
4. Render assistant markdown with theme-following colors and highlighted
   code blocks.
5. Show subagent progress live without flooding the transcript.
6. Keep everything legible without color, at 40 columns, and across all 22
   themes.

## Non-goals

- No status-line redesign, no header change, no new slash commands.
- No change to what is persisted or sent to the model: transcript display is
  a rendering of the same session entries and tool records (one new display
  role, `Turn`, for the footer — § Persistence).
- No mouse interaction, no clickable links, no OSC 8 hyperlinks, no images.
- No raw HTML interpretation; HTML in markdown shows as literal text.
- No language auto-detection for code blocks (fence info string only).
- No split (side-by-side) diffs.
- No vendor theme import; chroma styles are generated from Likha themes.

## User-visible behavior

### Glyph language

Each block starts with a glyph in a two-cell gutter; wrapped lines hang under
the text after the glyph.

| Block | Unicode | ASCII fallback | Color |
| --- | --- | --- | --- |
| User prompt | `>` | `>` | `Normal` fg on the user band |
| Assistant text | `⏺` | `*` | `Normal` fg |
| Tool call header | `⏺` | `*` | dot colored by status (§ Tool items); name bold, args `Normal` |
| Tool result / sub-line | `⎿` | `L` | `Muted` |
| Error | `✗` | `x` | `Error` fg |
| Likha notice | `ℹ` | `i` | `Muted` |
| Reasoning / turn footer | `✻` | `~` | `Muted` |
| Focused tool gutter | `›` | `>` | `Selected` |
| Truncation | `…` | `...` | `Muted` |
| Composer corners | `╭╮╰╯─│` | `+-|` | `Border` |

- Unicode is the default. `--ascii` / `LIKHA_ASCII=1` selects the ASCII set
  (persisted like `--nerd-fonts`); `--help` lists it.
- Nerd Font icons stay opt-in and limited to status/review markers; they never
  replace these block glyphs.
- Meaning is never color-only: the glyph identifies the block kind and the
  `⎿` line states the outcome in words.

### Backgrounds

- **User prompt:** a full-width tinted band (`BgUser`) spanning the prompt
  block including one row of padding above and below the text. In the
  `default` theme the band is a faint neutral gray derived from the terminal
  background (dark: ~8% toward white; light: ~6% toward black).
- **Everything else** (assistant, tools, notices, reasoning, footer) sits on
  the base canvas. Exceptions with their own tint: fenced code blocks
  (`BgCode`), diff lines (`BgDiffAdd`/`BgDiffRemove`), and the focused tool
  item (selection tint).
- Queued prompts keep the user band with `Muted` text and a `queued` word.
- Under ANSI/no-color profiles bands, tints, and status colors drop; glyphs,
  words, and layout remain.

### Assistant text

- `⏺` then the text rendered as markdown (§ Markdown). One blank row
  separates blocks.
- While streaming, the partial text renders with the same markdown rules; an
  unclosed fence renders as an open code block until it closes.

### Tool items

One item per tool call, replacing the separate request and result entries:

```
⏺ Grep(LAYOUT_BLOCKS in src)
  ⎿  Found 3 matches in 2 files
```

- **Header:** `⏺ DisplayName(readable args)` on one row, args truncated with
  `…` to the width. Display names and arg formatting:

  | Tool | Header |
  | --- | --- |
  | `read` | `Read(path)` · `Read(path:120-180)` for ranges · `List(dir)` for directories |
  | `grep` | `Grep(pattern in path)` (+ ` · literal` when literal) |
  | `glob` | `Glob(pattern in path)` |
  | `edit` | `Update(path)` · `Update(3 files)` for multi-file · `Create(path)` |
  | `run_command` | `Run(command line)` |
  | `web_fetch` | `Fetch(url)` |
  | `web_search` | `Search("query" · backend)` |
  | `task` | `Task(description)` (§ Subagent items) |
  | `ask_user` | `Ask(question)` |
  | `plan_update` | `Plan(N steps)` |
  | `skill` | `Skill(name)` |
  | MCP tools | `server.tool(k: v, …)` |
  | anything else | `name(k: v, …)` |

- **Status dot:** `Muted` and blinking (on the existing working-indicator
  tick; static `Muted` when animation is unavailable) while queued, awaiting
  approval, or running; green (`Success`) on success; `Error` on failed,
  refused, cancelled, or limited. Awaiting approval shows `⎿  Awaiting
  approval`.
- **Result line (`⎿`):** a summary that states the outcome in words:

  | Tool / outcome | Summary |
  | --- | --- |
  | grep | `Found N matches in M files` (+ ` · limited` / ` · N warnings`) — no preview lines |
  | glob | `Found N files` — no preview lines |
  | read | `Read N lines` (`Listed N entries` for directories) + preview |
  | run_command | `Exit 0` / `Failed: exit status N` + preview of output |
  | web_fetch | `Fetched N KB from host` + preview |
  | web_search | `N results from backend` + preview of titles |
  | edit | § Diffs |
  | refused | `Refused: reason` |
  | failed | `Failed: reason` |
  | cancelled | `Cancelled` (+ reason) |

- **Preview:** read, run_command, web_fetch, web_search, and MCP/other tools
  show up to **3** lines of output under the summary (indented to the `⎿`
  text column), then `… +N lines (ctrl+o to expand)`. Search tools show none.
- **Inspector:** raw JSON arguments, provenance (`[Likha built-in]`, MCP
  server), warnings, retained-output paging, and full output stay in the
  existing inspector (Enter / Ctrl+O on the focused item), unchanged.
- **Focus:** Tab / Shift+Tab cycle tool items (one stop per call, not per
  request/result). The focused item's rows get the selection tint and a `›`
  in the gutter.

### Diffs (edit tool)

```
⏺ Update(src/app.ts)
  ⎿  Updated src/app.ts with 2 additions and 1 removal
     12 - const n = 25
     12 + const n = 27
     13 + const e = 17
```

- Summary: `Updated path with N additions and M removals` (multi-file:
  `Updated 3 files with …`; create: `Created path (N lines)`).
- Up to **10** diff lines inline: line number, `+`/`-` sign, text, on
  `BgDiffAdd` / `BgDiffRemove`; then `… +N lines (ctrl+o to expand)`.
  Multi-file shows the first file's lines and names the rest.
- The sign column carries add/remove without color. Pending edit reviews keep
  the existing review surface.

### Subagent items (explore tasks)

```
⏺ Task(Map app routes and components)
  ⎿  Read src/app/(admin)/layout.tsx
```

- The `task` tool item shows one live `⎿` line naming the direct child's
  latest tool activity (`Read path`, `Grep pattern`, `Waiting for 2
  subtasks`, `Queued`).
- On settle the line becomes `Done · N tool uses · wait Xs · active Ys`
  (dot green), or `Failed: reason` / `Cancelled` / `Limited: model-request
  limit` / `Interrupted` (dot `Error`).
- `Agent:` transcript lines are no longer produced; `/agents` remains the
  full tree.

### Reasoning

- While streaming: `✻ Thinking…` followed by the **last 3 lines** of the
  reasoning in `Muted` italics.
- When the reasoning block ends: it collapses to `✻ Thought for Ns`.
- Tab focus includes collapsed thought markers; **Enter on a focused marker
  toggles it expanded/collapsed inline** (expanded shows the full reasoning
  in `Muted` italics, word-wrapped). Expansion state is per view, not
  persisted.

### Notices and errors

- Errors: `✗ text` in `Error` fg.
- Likha notices: `ℹ text` in `Muted`.
- Approval prompts, dialogs, and the review surface are unchanged.

### Turn footer

After each finished assistant turn: `  ✻ model · duration · N tools` in
`Muted` (e.g. `✻ nexum-router · 12.3s · 3 tools`). `model` is the model name
the turn ran on; `N tools` counts the turn's top-level tool calls (omitted
when zero). Cancelled turns end with `· cancelled`. At narrow widths the
segments drop right-to-left.

### Composer

- New default composer style `rounded`: a rounded box (`╭─╮ │ │ ╰─╯`, `Border`
  fg) around the input with a `> ` prompt glyph; placeholder unchanged.
- At ≤ 40 columns it falls back to `minimal` (existing rule). `minimal`,
  `bordered`, `borderless`, and `chatter` remain selectable; existing saved
  choices are honored.

### Wrapping

- Transcript prose wraps at word boundaries; a token wider than the line
  breaks by cell width. Continuation rows hang under the text column after
  the glyph (2 cells; 5 cells under `⎿`).
- Control and zero-width runes keep their existing escaped rendering.
  Inline color swatches keep working in prose (not inside code blocks).

### Markdown

Assistant text (and expanded reasoning) renders CommonMark + GFM tables and
strikethrough, with theme-mapped styles:

| Element | Rendering |
| --- | --- |
| `#`–`######` headings | bold `Title` fg; `#`/`##` also underlined; marker characters hidden |
| `**bold**`, `*italic*`, `~~strike~~` | terminal bold / italic / strikethrough |
| `` `inline code` `` | `Accent` fg on `BgCode` |
| fenced / indented code | `BgCode` panel spanning the text column, chroma-highlighted when the fence names a known language, plain otherwise; never word-wrapped — long lines break by cell width |
| lists | `•` (ASCII `-`) / `1.` markers, nested indent 2 cells |
| blockquote | `│ ` (ASCII `| `) bar in `Muted`, text `Muted` |
| links | link text underlined; ` (url)` in `Muted` when text ≠ url |
| tables | aligned columns with `│`/`─` rules when they fit the width; otherwise rendered as plain pipe text |
| thematic break | `─` rule in `Border` fg |
| images | `[image: alt]` in `Muted` |
| raw HTML | shown as literal text |

- Code highlighting colors come from the active Likha theme (keywords
  `Accent`, strings `Warning`, comments `Muted`, others `Normal`); the
  `default` theme uses the terminal's 16-color palette.
- Model text is untrusted: rendering never emits escape sequences from
  content; control characters stay escaped.

### Persistence and saved sessions

- Stored session entries keep their role strings; the new look is a
  rendering of the same data. One display role is added, `Turn`, persisted so
  footers reappear on resume.
- Tool calls start a tool record at `tool_start` (status `running`) and the
  result updates the same record by call ID instead of appending a new
  entry, so new sessions store one entry per call.
- Older sessions (separate request/result entries, `Agent:` entries) render
  without errors: a request entry followed by its result merges into one
  item; an unmatched legacy request renders as a header with an unknown
  status dot (`Muted`, no `⎿` line); legacy `Agent` entries render as `ℹ`
  notices.

### Narrow terminals

- All blocks render at 40×12 without horizontal overflow. Tool headers
  truncate args first; diff and preview lines truncate with `…`; the footer
  drops segments; the composer falls back to `minimal` at ≤ 40 columns.

## Theme roles

New derived roles in `internal/ui` (computed per family/variant with the
existing `mix()` + `legible()` contrast gate, overrides only on failure):

- `Accent` fg — the family accent without bold (same color as `Border`),
  named for prose use (inline code, keywords).
- `Success` fg — family green when the palette has one, else a standard green
  passed through the contrast gate.
- `BgCode` — accent mixed toward base (~6%).
- `BgDiffAdd` / `BgDiffRemove` — green / `Error` mixed toward base (~15%).
- `BgUser` for `default` — neutral tint (§ Backgrounds).

`BgModel` and `BgTool` remain defined for dialogs and compatibility but are no
longer painted behind transcript blocks.

## Acceptance criteria

1. No transcript row begins with `You:`, `Assistant:`, `Tool:`, `Reasoning:`,
   `Likha:`, `Error:`, or `Agent:` in new or resumed sessions.
2. Each block kind renders its glyph from § Glyph language in both glyph sets,
   and limited color profiles keep every glyph and outcome word.
3. Only user prompts (and the focused item, code blocks, diff lines) carry a
   background; all 22 themes plus `default` pass the contrast gate for every
   new role.
4. One tool call produces one transcript item whose dot and `⎿` summary
   follow § Tool items; preview line caps hold (0 for search, 3 for others,
   10 diff lines).
5. Tab visits each tool item once and each collapsed thought marker once;
   Enter/Ctrl+O on a tool opens the existing inspector; Enter on a thought
   marker toggles it.
6. An explore task shows exactly one transcript item with a live `⎿` line and
   a settle line; no `Agent:` lines are produced.
7. Markdown elements in § Markdown render as specified; fenced code with a
   known language is highlighted with theme colors; unclosed fences while
   streaming do not corrupt later rows.
8. Prose wraps at word boundaries with hanging indents; no row exceeds the
   terminal width at 40, 60, 80, and 120 columns (existing overflow
   invariants extended to every new block).
9. The turn footer appears after each finished turn and survives resume.
10. Older saved sessions load and render without errors per § Persistence.
11. `go build`, `go vet`, `go test ./...`, `go test -race ./...` pass; the
    ~22 assertions that pinned role-label text now assert glyph rendering
    (approved test changes); no other existing check is weakened.

## Delivery

Three slices, each committed separately and tried by the user before the
next (tasks.md):

1. **Blocks:** glyphs + ASCII fallback, user band (incl. `default`),
   single-item tools with status dot/summary/preview, focus style, notices,
   word wrap + hanging indent, rounded composer, spec amendments.
2. **Markdown:** goldmark parser, Likha renderer, chroma highlighting with
   theme-generated styles, `BgCode`.
3. **Live surfaces:** diffs, subagent items (replacing `Agent:` lines),
   reasoning live tail + collapse/expand, turn footer.

## Research

Collected 2026-10-04 from source (OpenCode `anomalyco/opencode@907b3bc5`
`packages/tui/src/routes/session/index.tsx`, `ui/border.ts`,
`theme/assets/opencode.json`; oh-my-pi `can1357/oh-my-pi@d4d49e71`
`packages/tui/src/chat/*`, `render/status-line.ts`, `render/output-block.ts`,
`render/render-utils.ts`, `theme/dark.json`) and Claude Code's public docs
plus observed behavior (marked unconfirmed where not documented):

- **Claude Code:** `>` user prompt with a subtle band, `⏺` blocks, `⎿` result
  lines, green/red/blinking status dots, `… +N lines (ctrl+o to expand)`,
  `✻ Thought for Ns`, `⏺ Task(…)` with nested progress and a `Done (…)` line.
- **OpenCode:** user messages in a raised panel with a colored `┃` left bar;
  one-row inline tools with icons (`✱` grep, `→` read, `$` bash); a
  `▣ Agent · model · 12.3s` footer per turn.
- **oh-my-pi:** user messages on a full-width background band; tool blocks
  tinted by state (`toolPendingBg`/`toolSuccessBg`/`toolErrorBg`); framed
  cards for edit/bash/task; 3-line collapsed previews.
