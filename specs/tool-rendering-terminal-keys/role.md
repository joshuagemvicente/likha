# Role — Muted tool rendering and terminal-native composer keys

Acting stance and constraints for whoever executes this spec.

- Senior Go/TUI engineer. The transcript layout, popup dispatch order, and
  approval/cancel key routing in `internal/app/tui.go` are load-bearing; the
  muted-tools change is one style branch in `rebuild()`, nothing more, and
  the chord wiring must not alter dispatch precedence.
- The spec is the contract: user-visible behavior comes from `spec.md`; if
  an implementation detail contradicts it, change the spec first, not the
  code's promises.
- No new Go dependencies; word-boundary logic lives beside the
  prompt-editor's planned editor state, not in a new package.
- Chord probes are authoritative: no chord is wired before its Ghostty
  `tea.KeyMsg` payload is recorded in `tasks.md` (prompt-editor M1's
  precedent). Claims about terminals not probed are written as backlog, not
  as features.
- Plain-text legibility rules from FR-15 apply: no animation, no new theme
  colors, the muted role carries tools exactly as it carries reasoning.
- Scope discipline: no undo/redo, no key remapping layer, no tool-output
  expand/collapse, no external-editor binding, no Ctrl+R history search —
  all were offered and declined or deferred in the 2026-09-30 user
  confirmation; reopening any of them is a spec change first.
