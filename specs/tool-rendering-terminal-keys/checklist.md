# Checklist — Muted tool rendering and terminal-native composer keys

Observable outcomes. Mirrors v1-spec FR-03/FR-15 (muted roles) and FR-17
(word kills, motion, line kills, yank) with this feature's additions.

## Muted tool entries (FR-15)

- [x] Every `Tool` transcript entry renders in the theme's muted role,
      exactly as `Reasoning` entries do. (M1; `rebuild()` shares the muted
      branch with Reasoning.)
- [x] No other transcript role changes style; spacing, prefixes, and wrap
      width are unchanged. (`TestToolEntriesRenderMutedLikeReasoning`:
      You/Assistant remain plain.)
- [x] Tool entries render muted identically after a session resume. (Mute
      is applied at layout time in `rebuild()`, which persisted entries run
      through on resume; same code path asserted in the rebuild test.)
- [x] Every predefined theme renders tools legibly through the muted role
      (no hardcoded color path). (`TestToolEntriesMutedAcrossThemes` over
      all `likhaui.ThemeNames()`.)

## Word kills and motion (FR-17 + this feature)

- [x] Ctrl+W deletes the previous word with its preceding whitespace run in
      one keystroke.
- [x] Ctrl+Backspace and Alt+Backspace behave identically to Ctrl+W.
- [ ] Ctrl+Delete and Alt+D delete the next word with its whitespace run.
      (Alt+D and Alt+Delete delivered and verified; **Ctrl+Delete as a
      distinct chord is unbindable on bubbletea v1.3.10** — the terminal's
      CSI form never reaches Update. Carried by Alt+D until a bubbletea
      upgrade parses it; recorded in tasks.md probe table and spec open
      item 1.)
- [x] Alt+B and Alt+F move the cursor one word back/forward.

## Line kills and yank (FR-17)

- [x] Ctrl+U kills to the start of the draft; yankable.
- [x] Ctrl+K kills to the end of the draft; yankable.
- [x] Ctrl+Y restores the most recent kill; word and line kills share one
      ring.

## Text polish

- [x] Ctrl+T transposes the two characters before the cursor; at end of
      buffer the last two swap; with ≤1 rune before the cursor it is a no-op.

## Newline insertion ("add new spaces")

- [x] Esc-prefix Return inserts a newline; bare Return still submits.
- [x] Typed newlines flatten to spaces on submit, exactly as bracketed-paste
      newlines are flattened today.
- [x] Ctrl+Return and Shift+Return insert a newline on terminals that emit
      them as distinct events.
- [x] Esc-during-cancel and Esc-clears-draft survive the ~50 ms Alt-prefix
      decay window: a lone ESC still acts after expiry, and any chord other
      than Return arriving inside the window is processed as its own key.

## Inertness and dispatch

- [x] All new editing bindings are inert while a turn streams or an approval
      is pending.
- [x] With the @-mention or /-command popup open, popup navigation claims its
      keys first and the draft editing keeps working as today.
- [x] `go test ./...` passes with this feature's tests included.

## Probe gate (recorded before key wiring)

- [x] `tasks.md` carries the `tea.KeyMsg` payload table for every new chord
      (pty probe, cross-checked against the bubbletea sequences table)
      before the chords were wired; Ctrl+Delete/Ctrl+Enter CSI forms
      documented as dropped by bubbletea v1.3.10.
