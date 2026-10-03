# Feature: Muted tool rendering and terminal-native composer keys

**Status:** in progress (M1 + M2 implemented locally; Ctrl+Delete chord
unavailable on bubbletea v1.3.10 — see tasks.md probe table)

Refines v1-spec FR-03 (tool/result distinction), FR-15 (muted role), and
FR-17 (line editor). Coordinates with
[prompt-editor](../prompt-editor/spec.md) (the editor engine this feature's
keys ride on) and the [tui-redesign](../tui-redesign/spec.md) visual rules.

## Context

Likha renders thinking-model reasoning in the theme's muted role (FR-15) but
renders tool transcripts — the `Tool:` entries appended for `tool_start` and
`tool_result` events in `internal/app/tui.go` — in plain foreground. The user
wants tools to read as fold-quiet machinery, not as assistant content: **tool
entries adopt the same muted role as reasoning** in this feature.

Second, the composer today handles only backspace/ctrl+h, enter, pgup/pgdn,
home/end, ctrl+c/esc/ctrl+d/ctrl+g. The user asked for the native terminal
editing keys their shell provides — Ctrl+W, Ctrl+Delete, and newline-inserting
Return variants — and, more broadly, for "most of the functionalities from my
terminal" in the Likha composer. The cited reference surfaces are OpenCode,
OMP, and Claude Code; their bindings were gathered while writing this spec
(§ Research) and the scope was confirmed with the user on 2026-09-30:

1. Tool entries render in `theme.Muted`, exactly like Reasoning entries.
2. Shortcut scope = **full core readline movement/edit set** (option 1 of the
   user's menu), plus Alt+Backspace handling and Enter-modifier newline keys
   (option 2's additions), but **not** the undo/redo marks of OpenCode's
   list (option 4 was not selected).
3. The "Alt+Alt+Return" phrasing is honored as its workable terminal
   equivalent: **Esc-prefix Return** (ESC then Return inserts a newline).
4. Inclusion of the readline-native text-polish keys (Ctrl+T transpose,
   Alt+D/F/B, Ctrl+Y yank) was confirmed.

Precedents (see § Research for sources): OpenCode binds
`input_newline: shift+return,ctrl+return,alt+return,ctrl+j` and
`input_delete_word_backward: ctrl+w,ctrl+backspace,alt+backspace`; OMP
distinguishes Return (send) from Ctrl+Enter/Shift+Enter and notes Windows
Terminal swallows Ctrl+Enter; Claude Code uses Shift+Enter for newline in a
configured terminal; bash readline separates `unix-word-rubout` (Ctrl+W) from
`backward-kill-word` (Alt+Backspace) by their whitespace handling, and zsh's
emacs map shrinks that difference — Likha adopts the OpenCode
collapse (both are word-back kill) with the readline whitespace-collapse
boundary rule kept as the underlying word rule.

## Non-goals

- No change to what the transcript IS: no tool-output collapse/expand UI, no
  Ctrl+O expand tool output (OMP's `app.tools.expand` is noted in research
  only), no in-tool content rendering. This spec changes color and keys, not
  content shape.
- No prompt-keyword sniffing, no new slash commands, no dialogs.
- No key remapping/rebindable keymap layer (declined in the user confirm;
  OpenCode-style config keybinds are a separate feature if ever asked).
- No claim that a terminal that never emits a chord can have that chord
  handled; the § Open items ledger is the fallback.

## User-visible behavior

### Muted tool entries

- Every transcript entry with role `Tool` (a `tool_start` or `tool_result`
  event, including cancel-era `Error: action not executed; run interrupted`
  tool rows) renders in `theme.Muted` instead of the plain style, matching
  Reasoning entries: same role, no other decoration.
- Layout, spacing, prefixes, and wrap width are exactly as today; the only
  change is the style applied to the wrapped `Tool: …` lines.
- The muted style comes from the theme's muted role (like reasoning) so a
  theme swap keeps tools legible; no hardcoded colors.
- The muted rendering is a transcript-render change only: entries persist as
  they do today; mute is applied at layout time (rebuild), so a resumed
  session renders tool history muted identically.

### Terminal-native composer keys

All bindings below work in the draft input, are inert during an active run or
a pending approval (FR-17 rule), and go through the same popup-aware dispatch
as today: with an @-mention or /-command popup open, navigation and completion
keys are claimed by the popup first, exactly as today in `tui.go`. The editor
state and cursor they operate on are the prompt-editor's (FR-17); this spec
adds keys only.

**Word kills**

| Key | Action |
| --- | --- |
| Ctrl+W | delete previous word, whitespace-run collapsing |
| Ctrl+Backspace | same action as Ctrl+W (alias) |
| Alt+Backspace | same action as Ctrl+W (alias) |
| Ctrl+Delete | delete next word, whitespace-run collapsing |
| Alt+D | delete next word, whitespace-run collapsing (alias) |

Word rule: a "word" is a maximal run of non-space characters; killing
backward to a word boundary consumes the run of spaces into one deletion, as
a native terminal does.

**Word motion**

| Key | Action |
| --- | --- |
| Alt+B | move back one word |
| Alt+F | move forward one word |

**Line kills and yank (readline core, from the user's shell)**

| Key | Action |
| --- | --- |
| Ctrl+U | kill to start of draft (whole one-line buffer — Likha's draft is single-line, so this is `kill-to-buffer-start`) |
| Ctrl+K | kill to end of draft |
| Ctrl+Y | yank: reinsert the most recent kill (last-kill length known; a small ring of the last 8 kills per prompt-editor) |
| Ctrl+C | unchanged: cancel/quit, never a kill alias |

**Newline insertion (the "add new spaces" ask)**

| Key | Action |
| --- | --- |
| Esc-prefix then Return (the terminal reality of "Alt+Alt+Return") | insert a newline into the draft (draft becomes multi-line; per prompt-editor's paste rule, pasted newlines still collapse to spaces — typed newlines do not) |
| Ctrl+Return (where the terminal emits it: iTerm2, Ghostty) | insert a newline (alias of the above action) |
| Shift+Return (where the terminal emits it) | insert a newline (alias) |
| Return (bare) | unchanged: submits the prompt |

Esc-prefix Return encodes `Alt+Return` as ESC then Return on terminals that
cannot send the chord as one press; terminals that do send it as one event
(iTerm2, Ghostty) also land on the same action via the chord match, so both
arrivals of "Alt+Return" do the same thing. Windows Terminal (which
documented-swallows Ctrl+Enter in OMP's research and is out of v1 targets
anyway) is not claimed.

**Text polish (readline core)**

| Key | Action |
| --- | --- |
| Ctrl+T | transpose the two characters before the cursor (drag cursor before swap when at end of buffer) |
| Ctrl+X Ctrl+E note | NOT implemented: draft-in-external-editor is OMP's Ctrl+G (`app.editor.external`) and is a separate feature; listed here only so the gap is written down |
| Ctrl+V | unchanged: clipboard paste per prompt-editor (text into the draft, image tokens); never becomes a kill alias |

**Kill ring sharing.** Every word kill, line kill, and yank shares the one
kill ring from prompt-editor's editor state; side-channel dead keys are
invented nowhere.

### Visual key notes

- No in-TUI "shortcut legend" change is required in this spec; the status
  line's mode hints keep their slot, and a `?`-style key help surface stays
  out of scope.
- When a chord is not delivered by the terminal (e.g. some hosts turn
  Ctrl+Delete into plain Delete or never send Ctrl+Return), the user sees the
  plain key's action, never a hang: the spec's binding table maps each action
  to the aliases that most terminals can deliver, and the § Open items
  ledger tracks the per-host probes.

## Functional changes (applied in v1-spec.md)

None this round: FR-03/FR-15/FR-17 already describe the muted-role rendering
and the editor kill/yank surface. This feature *implements* parts of them; if
implementation reveals an FR wording gap, v1-spec is amended first and this
folder's checklist mirrors it.

## Research findings (recorded 2026-09-30, verbatim chord names)

Sources: OpenCode keybinds doc
(github.com/anomalyco/opencode `packages/web/src/content/docs/keybinds.mdx`),
OMP keybindings doc (github.com/YanwuZeng/omp `docs/keybindings.md`), Claude
Code docs (docs.anthropic.com cli usage/terminal-config), bash readline
manual (gnu.org Commands for Text).

**OpenCode (TUI input defaults, 2026 dev branch):**

- `input_newline`: `shift+return,ctrl+return,alt+return,ctrl+j`
- `input_submit`: `return`
- `input_delete_word_backward`: `ctrl+w,ctrl+backspace,alt+backspace`
- `input_delete_word_forward`: `alt+d,alt+delete,ctrl+delete`
- `input_word_forward`: `alt+f,alt+right,ctrl+right`; `input_word_backward`:
  `alt+b,alt+left,ctrl+left`
- `input_delete_to_line_end`: `ctrl+k`; `input_delete_to_line_start`:
  `ctrl+u`
- `input_delete`: `ctrl+d,delete,shift+delete`; `input_backspace`:
  `backspace,shift+backspace`
- `input_undo`: `ctrl+-,super+z`; `input_redo`: `ctrl+.,super+shift+z`
  (undo/redo excluded from this feature's scope per user confirmation)
- `input_clear`: `ctrl+c` **in OpenCode's input, but Likha keeps ctrl+c =
  cancel/quit** (FR-04/FR-17 rule; noted deliberately as a divergence)

**OMP (Oh My Pi agent, keybindings.md):**

- `app.message.followUp`: `Ctrl+Q`, `Ctrl+Enter` — i.e., Ctrl+Enter is NOT
  send; it queues. Likha's Ctrl+Return inserts a newline instead (never a
  queue), matching the user's "to add new spaces" ask.
- OMP documents Windows Terminal swallowing Ctrl+Enter; the lesson Likha takes
  is aliasing across aliases (never one chord carrying an action alone), not
  a queue feature.
- `app.editor.external`: `Ctrl+G` — Likha's ctrl+g is the composer-style
  dialog today; Likha does not take OMP's external-editor binding.
- `app.tools.expand`: `Ctrl+O` (tool-output expansion) — non-goal here, noted
  for the tools panel's future.
- No Alt+Alt+Return concept exists in OMP; the "Esc then Return" encoding is
  the industry's way to survive terminals that deliver the Alt chord only as
  an ESC-prefixed sequence.

**Claude Code (docs.anthropic.com):**

- Shift+Enter inserts a newline (after `/terminal-setup` configures the
  terminal); backslash+Enter also inserts a newline; PageUp/PageDown
  multi-line navigation exists in their composer.
- No documented Alt+Backspace claim; their editing set defers to
  terminal/shell muscle memory — the pattern behind this spec's alias
  families rather than single-source exactness.

**bash readline (Commands for Text):**

- Ctrl+W = `unix-word-rubout`: kill backward to the nearest whitespace; the
  killed text joins the kill ring, yankable with Ctrl+Y.
- Alt+Backspace (`M-DEL`) = `backward-kill-word`: word-char boundary rules,
  i.e. same action family as Ctrl+W with a different boundary definition. In
  practice both feel like "delete the previous word"; the OpenCode default
  treats them as one action with three aliases, and Likha follows OpenCode.
- Alt+D = `kill-word` (forward), Alt+B = `backward-word`,
  Alt+F = `forward-word`, Ctrl+Y = `yank`, Ctrl+T = `transpose-chars`.
- Ctrl+Enter has no readline binding; it carries meaning only in TUI apps
  (OMP's queue, OpenCode's newline), which is exactly where Likha's own
  newline-insert decision sits.

### Answer to the user's question in the brief ("Ctrl+W is a Control+Delete, right?")

Nearly: Ctrl+W and Ctrl+Delete are the same *action family* (delete one word)
facing opposite directions — Ctrl+W kills the previous word, Ctrl+Delete
kills the next. In bash they differ slightly in boundary rules (Ctrl+W stops
at whitespace, Alt+Backspace stops at word chars); Likha fixes one word rule
(whitespace-run collapsing) and aliases all five chords to it, because the
reference surfaces the user cited (OpenCode, Claude Code muscle memory, and
modern shells) agree on the family and disagree only on boundary edge cases.

## Open items (probes still open; decisions recorded)

Confirmed with the user 2026-09-30, recorded so implementation cannot drift:
- **Esc-prefix Return = timed decay window** (item 2 below, resolved).
- **Typed newlines flatten on submit** — a draft containing newlines typed
  via the modifier chords is flattened to spaces when submitted with bare
  Return, exactly as bracketed-paste newlines are today; the prompt stays
  one line on the wire, and `/`-command/`//`-escape dispatch is unaffected.
  (Resolved; folded into the acceptance criteria below.)

1. **Key-encoding probe — RESOLVED (recorded in `tasks.md` M2 task 3,
   2026-09-30).** Probe table recorded from a pty-driven `tea.KeyMsg` logger
   cross-checked against the bubbletea v1.3.10 sequences table. Findings the
   spec absorbs: **Ctrl+Delete as a distinct chord is unbindable** on this
   bubbletea (legacy CSI `3;5~` and kitty/CSI-modified forms fall to the
   unexported `unknownCSISequenceMsg` and never reach the model's Update);
   Alt+D and Alt+Delete carry the forward-word kill instead. Ctrl+Enter and
   Shift+Enter deliver as the LF byte (`ctrl+j`) on legacy encodings — the
   CSI forms are likewise dropped. Alt+Return arrives either ESC-combined
   (`alt+enter`) or as ESC then Return, covered by the decay window. A live
   Ghostty pass by the user remains the confirmation step (the probe pty
   fed the byte sequences Ghostty emits in default mode).
2. **Esc-prefix Return capture ambiguity — RESOLVED (user-confirmed
   2026-09-30): timed decay window.** ESC is held for ~50 ms waiting for a
   following key: Return arriving inside the window means Esc-prefix Return
   (insert a newline); the window expiring with no following key means a bare
   ESC, keeping today's clear-draft/cancel-run behavior. The window must
   never swallow keys: any chord other than Return inside it is processed as
   its own key (the ESC is dropped). Must not break Esc-cancel of an active
   run — a lone ESC press still cancels after the decay expiry.
3. **Ctrl+U boundary semantics:** readline's Ctrl+U kills the whole line;
   it differs from "to buffer start" only because Likha's draft is single-line
   (prompt-editor caveat); the single-line rule stands unless the editor
   grows multi-line in prompt-editor, which would amend this spec.
4. **Muted styling of tool ID lines vs whole entry:** confirmed whole-entry
   muted, same as Reasoning; the collapse/expansion alternative was offered
   and not selected — reopening it is a spec change.

## Acceptance criteria

- [ ] Tool entries (role `Tool`) render in the theme's muted role exactly as
      Reasoning entries do; no other role in the transcript changes style.
- [ ] Theme swap keeps tool entries legible in every predefined theme
      (no hardcoded colors; same rule as FR-15 reasoning).
- [ ] Ctrl+W deletes the previous word; with multiple consecutive spaces
      before the cursor, the run collapses into one deletion.
- [ ] Ctrl+Backspace and Alt+Backspace behave identically to Ctrl+W.
- [ ] Ctrl+Delete and Alt+D delete the next word with its whitespace run.
      (Alt+D and Alt+Delete verified; Ctrl+Delete unbindable on bubbletea
      v1.3.10 — see tasks.md probe table.)
- [ ] Alt+B/Alt+F move the cursor by one word back/forward.
- [ ] Ctrl+U kills to the start of the draft; Ctrl+K kills to the end; both
      are yankable with Ctrl+Y.
- [ ] Ctrl+T transposes the two characters before the cursor (at end of
      buffer: the last two characters swap; at position 0 it is a no-op).
- [ ] Esc-prefix Return inserts a newline into the draft instead of
      submitting; bare Return still submits; typed newlines are flattened to
      spaces on submit, exactly as bracketed-paste newlines are today
      (user-confirmed 2026-09-30).
- [ ] Ctrl+Return and Shift+Return insert a newline on terminals that emit
      them as distinct events.
- [ ] Editing bindings are inert while a turn streams or an approval is
      pending (existing rule; no regression).
- [ ] With the @-mention or /-command popup open, popup navigation wins for
      its keys, and a word-kill/motion key edits the draft after the popup
      closes as the popups' existing claims dictate (no regression to the
      current popup dispatch order).
- [ ] A bare ESC while a draft has text and no popup is open still clears the
      draft (existing behavior) unless consumed as the Alt-prefix decay
      window (open item 2) — either way, no keypress is lost or swallowed on
      any path.
- [x] `go test ./...` passes with this feature's tests included. (M1+M2:
      `TestToolEntriesRenderMutedLikeReasoning`,
      `TestToolEntriesMutedAcrossThemes` in
      `internal/app/tui_muted_tools_test.go`; full suite green 2026-09-30.
      M2 criteria: suite green after the chord wiring; the Ctrl+Delete
      criterion stays open per the probe finding.)
