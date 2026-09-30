# Tasks — Muted tool rendering and terminal-native composer keys

Ordered implementation tasks. Each ends with a verification step; no
checkmarks until the step is done. Automated entry point: `go test ./...`.

Depends on prompt-editor M1 (editor state with cursor and kill ring) for all
composer-key tasks; the muted-tools task is independent and can land first.

## M1 — Muted tool entries

1. **Muted style for role `Tool`.** [x] DONE 2026-09-30 — In
   `internal/app/tui.go` `rebuild()`, `entry{role: "Tool"}` shares the
   `Reasoning` muted branch (`e.role == "Reasoning" || e.role == "Tool"`).
   Verified: `internal/app/tui_muted_tools_test.go` —
   `TestToolEntriesRenderMutedLikeReasoning` asserts the rebuilt Tool lines
   carry `theme.Muted` with `m.lines`/`m.lineStyles` aligned, Reasoning
   stays muted, and You/Assistant stay plain; existing tui_test.go green.
2. **Theme-swap legibility check.** [x] DONE 2026-09-30 —
   `TestToolEntriesMutedAcrossThemes` renders a Tool entry under every
   predefined theme (dark variant, `lisaui.ThemeNames()` × `lisaui.Resolve`)
   and asserts the Tool line is visible and carries each theme's own Muted
   style — no hardcoded color path. Full `go test ./...` green (`go vet` +
   `gofmt` clean).

## M2 — Composer keys (after prompt-editor M1 editor state exists)

3. **Key-encoding probe (gate).** [x] DONE 2026-09-30 — Probe recorded.
   Method: throwaway `tea.KeyMsg` logger driven through a pty
   (TERM=xterm-256color, legacy terminal encodings, the byte sequences
   Ghostty emits for these chords in its default mode); cross-checked
   against the bubbletea v1.3.10 sequences table (module cache key.go).
   Recorded table:

   | Chord | Wire bytes | bubbletea `String()` | Note |
   | --- | --- | --- | --- |
   | Ctrl+W | `0x17` | `ctrl+w` | bound: delete-previous-word |
   | Ctrl+Backspace | `0x17` (or `0x08`) | `ctrl+w` / `ctrl+h` | no distinct chord exists; 0x17 rides ctrl+w, 0x08 is backspace |
   | Alt+Backspace | `ESC 0x7f` | `alt+backspace` | bound |
   | Ctrl+Delete | `ESC [3;5~` | **dropped** (`unknownCSISequenceMsg`, unexported) | UNBINDABLE on bubbletea v1.3.10; Alt+D/Alt+Delete carry the action |
   | Alt+Delete | `ESC [3;3~` | `alt+delete` | bound (alias) |
   | Alt+D / Alt+B / Alt+F | `ESC d/b/f` | `alt+d` / `alt+b` / `alt+f` | bound |
   | Ctrl+U / K / Y / T | `0x15 / 0x0b / 0x19 / 0x14` | `ctrl+u/k/y/t` | bound |
   | Ctrl+Enter / Shift+Enter (legacy) | `0x0a` (LF) | `ctrl+j` | bound: newline |
   | Ctrl+Enter / Shift+Enter (CSI `13;5~` / `13;2~`) | CSI | **dropped** | same unknown-CSI gap |
   | Alt+Return (one read) | `ESC CR` | `alt+enter` | bound: newline |
   | Alt+Return (two reads: ESC, then Return) | `ESC` then `CR` | `esc` then `enter` | handled by the 50 ms ESC decay window |
   | Return | `0x0d` | `enter` | submit (flattens typed newlines) |
   | Bracketed paste | `ESC [200~ … ESC [201~` | KeyRunes, `Paste=true` | inserted at caret, newlines→spaces |

   Caveat: the probe pty stands in for Ghostty's default encoding; a live
   Ghostty pass by the user remains the final word (backlog note in the
   spec's open item 1).
4. **Word kills.** [x] DONE 2026-09-30 — Wired via `internal/app/editor.go`
   (`killWordBack`: previous word + preceding whitespace run in one
   deletion; `killWordForward`: next word plus the run between caret and
   word). Bound: Ctrl+W, Ctrl+Backspace (delivers ctrl+w's byte), Alt+
   Backspace; forward on Alt+D, Alt+Delete; **Ctrl+Delete unbindable**
   (probe: bubbletea v1.3.10 drops the CSI form). Verified:
   `TestKillWordBackCollapsesWhitespaceRun`,
   `TestKillWordForwardMirrorsBack`, `TestComposerChordWiring`,
   `TestAltChordsDoNotInsertTheirRune`.
5. **Word motion.** [x] DONE 2026-09-30 — `moveWordBack`/`moveWordForward`
   with caret clamping; bound Alt+B/Alt+F. Verified: `TestMoveWordBackAndForward`.
6. **Line kills + yank.** [x] DONE 2026-09-30 — Ctrl+U / Ctrl+K / Ctrl+Y
   through the shared kill ring (8 entries); a repeat yank cycles, an edit
   or buffer clear ends the cycle. Verified: `TestKillToStartEndAndYankRing`,
   `TestKillRingCapsAtEight`.
7. **Transpose.** [x] DONE 2026-09-30 — Ctrl+T swaps the two runes before
   the caret; ≤1 rune before the caret is a no-op; caret unchanged.
   Verified: `TestTranspose`. (Multibyte runes: operations are rune-indexed,
   covered by the generic insert/kill tests.)
8. **Newline insertion.** [x] DONE 2026-09-30 — Bound Esc-prefix Return
   (50 ms decay window; lone ESC clears the draft at expiry, ESC-cancel of
   a running turn stays immediate), Ctrl+Return/Shift+Return via the LF
   byte `ctrl+j` and the direct chord names, `alt+enter` (ESC-combined).
   Bare Return submits with typed newlines flattened to spaces; bracketed
   paste inserts at the caret with newlines flattened. Verified:
   `TestEscPrefixReturnInsertsNewlineAndDecayClears`,
   `TestBracketedPasteFlattensAndInsertsAtCaret` plus the real-TUI pty smoke
   (ctrl+w kill visible, ctrl+y yank visible, ctrl+u clear, ESC+Return no
   submit, Enter submit + "ok" reply).
9. **Inert + popup dispatch regression.** [x] DONE 2026-09-30 — Editing is
   gated by `m.editable()`; popup re-filter runs through one
   `syncPopups()` helper used by every mutating path (existing popup
   semantics preserved). Full suite green: `go test ./...` (all packages ok),
   `go vet` + `gofmt` clean.
10. **Docs.** [x] DONE 2026-09-30 — README gained an "Editing keys"
    subsection; `--help` usage footer lists the bindings; CHANGELOG
    Unreleased carries the feature entries (editing + muted tools).

