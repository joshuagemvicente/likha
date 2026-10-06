# Spec: TUI polish — status line, composer styles, branding

**Status:** Phases A–D implemented locally
(V1 → V2 surface redesign; direction confirmed with the user 2026-09-29).
Extends v1-spec FR-12/FR-14 and `specs/themes/spec.md`. Supersedes the
earlier sidebar draft: **no sidebar** — ambient state lives in the status
line under the composer.

## 1. Why

The V1 TUI is a development shell: a five-row ASCII logo, three header
lines, a paged conversation, three footer hint rows, and an input that is
indistinguishable from conversation text. Across the tools the user has
used (OpenCode, OMP, Claude Code TUI + GUI, DeepSeek Harness, Codex CLI,
Pi Agent), the shared terminal-UI principles are consistent:

- **The conversation is the interface.** Ambient state lives in fixed,
  thin furniture — never in the transcript.
- **The composer is a distinct region** with several acceptable shapes; the
  shape is a user setting, not a hard-coded opinion (OMP ships multiple
  composer shapes behind a setting).
- **The status line is a cockpit:** where am I, what is running, how much
  room is left, what did it cost — stable field order, readable without
  color, degrading gracefully on narrow terminals (Claude Code's model;
  OMP's `statusLine.preset` + segments is the same idea).
- **Identity retires.** OMP's welcome header stays as chrome until content
  overflows, then retires into history exactly once. Persistent logo
  furniture wastes conversation rows for the whole session.

Likha adopts these principles with its own execution: no screens copied, no
mouse-driven panels, no Nerd-Font requirements.

## 2. Overall layout

Single region, no sidebar. Top to bottom:

1. **Startup logo block** — the existing ASCII logo + repository line
   rendered once at session start as the first content block. It scrolls
   away naturally as the transcript grows (the OMP welcome-header
   retirement pattern): identity without permanent furniture. FR-12 keeps
   its "identify Likha" guarantee; the wide/narrow logo header variants are
   removed in favor of this block plus the wordmark in the status line.
2. **Transcript** — paged exactly as today (FR-14: no scrollback, PgUp/PgDn
   + Home/End + wheel, full-session coverage). Rendering stays
   **log-minimal** (Claude Code style): role-prefixed plain prose, glyph
   markers, no boxes. This is already Likha's style; the polish is
   consistency, not restyling — spacing, marker alignment, and a single
   rule of one blank line between blocks.
3. **Composer** — selectable style, §3.
4. **Status line** — one to two rows pinned under the composer, §4.

Same keys, same commands, same approval flow. This is a reskin of regions,
not new interaction (except choosing a composer style).

## 3. Composer styles (OMP's shape-setting pattern)

The composer shape is a **setting** (`composer.style` in `config.json`,
persisted like the theme), switchable at runtime with **Ctrl+G** while idle.
The shared selection dialog filters as you type; Enter applies and Esc
cancels. Four styles, one input engine behind them — only decoration differs:

| Style | Render |
| --- | --- |
| `minimal` (default) | Rule line (Border role) above; dim placeholder "Ask Likha… // escapes a slash" when empty; draft renders plainly. The Claude Code look. |
| `bordered` | Full rounded box around the input, Border role edges, placeholder inside. The Codex/OpenCode box. Costs two rows. |
| `borderless` | No rule, no box — just the placeholder and text, one blank line of separation from the transcript. Maximum flatness. |
| `chatter` | The draft renders inline with a role prefix (`You ›`) in the Selected role, blending the composer into the transcript like a chat message; the hint row stays beneath. |

Rules for every style: the draft is never obscured by status furniture; a
pending review keeps precedence (its banner replaces the placeholder); the
state hints row (§4) is common to all four styles; at the 40-col floor the
bordered/chatter styles degrade to `minimal` rather than clipping.

## 4. Status line composition

One line at the bottom (wrapping to a second on narrow terminals),
segmented with stable order and a plain ` · ` separator — the field
position of every segment is fixed so users can read it peripherally.

**Default segments** (always on, they answer the three cockpit questions):

- `model` — active provider · model
- `ctx <n>%` — context usage as a percentage of the model's window; bar
  omitted on the status line (percentage is the signal; the raw window
  size shows in `/models` and the session details). Warning role past
  ~80%. When usage is unavailable: `ctx —`, never a fabricated number.
- `cost $x.xx` — session cost from provider usage × known pricing; when
  pricing is unknown, the segment is hidden (not `$—`).

Phase C implemented (measured segments) and Phase D implemented (logo
retirement, wordmark, remaining catalog segments); the §7 phase plan and
§8 data dependencies record what shipped. The full segment order is
wordmark, provider · model, ctx, folder, branch, changes, staged, mcp,
session, minutes, tokens, version, update — every optional toggle off by
default, one row wide and two rows below 70 columns. The cost segment
remains hidden without pricing data. Existing ChatGPT plan rate-window
usage is separate from context usage and remains visible when reported and
space permits; it is never labeled `ctx`.

**Optional segments** (each a toggle in `config.json`, all off unless
listed in the default): the catalog exists so any of these can be flipped
on or cut without new plumbing. Default-on entries are marked.

| Segment | Content | Default |
| --- | --- | --- |
| folder | working directory (home abbreviated to `~`) | off |
| branch | current git branch | off |
| changes | dirty-file count ("3 changed") | off |
| staged | staged-file count ("1 staged") | off |
| mcp | connected MCP servers + last tool activity (the existing `conn.mcp.Status()`, condensed) | off |
| session | session title (falls back to short id) | off |
| minutes | minutes consumed this session | off |
| tokens | raw prompt+completion counts, secondary to the ctx % | off |
| version | `v0.1.0` | off |
| update | update-available notice (ties into the update-notification spec) | off |

Phase B implemented `folder`, `branch`, `session`, and `version` under
`status_line` in `config.json`, all off by default. Phase C added
`minutes` and `tokens` (measured only, from §7 Phase C usage plumbing);
Phase D added `changes`, `staged`, `mcp`, and `update` — the whole toggle
list under `status_line` is now implemented, all off by default. `changes`
and `staged` read a bounded `git status --porcelain` at session start and
after tool results; `mcp` hides when no servers are configured; `update`
renders `update → vX` when the throttled startup check finds a newer
release (see `specs/update-notification/` for the check details).

Review/working states override the segments' right side, exactly as the
current footer does today (`Y approve after review  N reject`, `Ctrl+C
cancel`) — mode hints never lose their slot.

## 5. Branding

- **Startup block** (§2.1) carries the logo — drawn once, scrolls away.
- **Status line** carries the persistent identity: the `version` segment
  and the `Likha` wordmark on the left of the default preset.
- The logo remains plain ASCII text; no font requirements.

## 6. Visual style guidelines

- **Roles only** (the eight in `ui/theme.go`): Title for the startup block,
  Selected for focus and the chatter prefix, Muted for placeholders and
  metadata segments, Warning for context pressure and reviews, Border for
  composer rules and boxes, Help for hints. No hardcoded colors; every
  theme must render every composer style legibly (theme tests extended per
  style × palette).
- **Typography:** terminal monospace only. New glyphs go through
  `ui/glyphs.go` with the existing Ascii/Nerd dual sets.
- **Spacing:** one blank line between transcript and composer region; no
  double rules, no background fills, no animation. Status line never
  blinks or pulses.
- **Density:** status line is one row wide-layout, two at the 40-col floor;
  composer adds at most two rows (bordered style).

## 7. Phase plan (implement → test → ship, one phase at a time)

1. **Phase A — composer styles (implemented).** Four styles behind
   `composer.style`; Ctrl+G runtime switch dialog; state-hints row
   consolidated from today's three footer rows. The draft is fixed outside
   transcript pages. At the 40-col floor, bordered/chatter display as minimal
   without changing the stored preference.
2. **Phase B — status line (implemented).** Stable model/provider,
   `ctx —`, and mode-hint segments plus optional `folder`, `branch`,
   `session`, and `version` config toggles. One row wide, two at the
   40-column floor; review decisions and pagination retain priority.
3. **Phase C — measured segments (implemented).** Usage plumbing from
   provider responses is in place (`internal/model`): the final usage of the
   last response — `prompt_tokens`/`completion_tokens`, with `input`/`output`
   aliases accepted — is captured per run, and a static, documentation-backed
   context-window catalog (`internal/model/windows.go`) supplies window sizes
   for publicly documented model IDs *(superseded by
   [model-metadata](../model-metadata/spec.md), user-approved 2026-10-04:
   the bundled catalog keyed by provider and model; spend is now priced
   per request)*. The status line renders `ctx <n>%`
   (warning role past 80%), `tokens <sum>`, and `minutes <N>m` under their
   `status_line` toggles; `ctx —` stays when usage is unmeasured or the
   window is unknown. The cost segment stays hidden: there is no pricing
   data, so no cost is computed or shown. The ChatGPT rate-window summary
   remains separate and is never labeled `ctx`.
4. **Phase D — branding + git segments (implemented).** The persistent
   five-row logo header is retired in favor of the compact three-line
   header; fresh sessions keep the ASCII logo as the first transcript block
   (skipped below 56 columns, never persisted or redrawn on resume). The
   status line gains the `Likha` wordmark (width ≥ 70, first segment dropped
   under width pressure), `changes`/`staged` counts read from
   `git status --porcelain` (`--no-optional-locks`, bounded at 3 s; read at
   session start and refreshed after tool results), the condensed `mcp`
   summary (hidden when no servers are configured), and the `update → vX`
   segment fed by the throttled startup check (`internal/update`: 24 h
   throttle file, `LIKHA_UPDATE_API` override, `LIKHA_UPDATE_CHECK=0` opt-out,
   silent on any failure, dev builds never notify). Every optional segment
   remains off by default; the full order is wordmark, provider · model,
   ctx, folder, branch, changes, staged, mcp, session, minutes, tokens,
   version, update, one row wide and two rows below 70 columns.

Each phase ships with its own rendering tests (composer styles render and
degrade at 40 cols; status line segment order is stable; logo retires on
content growth) and must leave all existing tests green.

## 8. Data dependencies (flagged before Phase A)

- **Usage reporting** (ctx %, tokens, minutes, cost): shipped in Phase C —
  the model client now surfaces the last response's final token usage and
  `internal/model/windows.go` carries the documented context-window catalog.
  The cost segment remains unshipped: it additionally requires a pricing
  table, which does not exist, so no cost is computed or displayed.
- **Git state:** shipped in Phase D — `changes`/`staged` read
  `git status --porcelain` (`--no-optional-locks`, 3-second bound) at
  session start and after tool results; the branch name still reads only
  Git HEAD when its optional toggle is enabled.
- **Update check:** shipped in Phase D — `internal/update` provides the
  read-only, throttled latest-release lookup feeding the `update` segment.
  The in-TUI banner (§4 narrow/wide variants) and the `likha update` command
  from `specs/update-notification/` remain open.
- Everything else is a re-arrangement of existing state.

## 9. Non-goals

No sidebar in this round. No mouse-driven panels, no in-status
conversation preview, no scrollback reintroduction (FR-14 stays app-managed
paging), no animation, no new theme or glyph requirements, no change to the
first-run setup flow, and no new slash commands.
