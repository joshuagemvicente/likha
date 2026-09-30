# Context — Muted tool rendering and terminal-native composer keys

Code, tests, and docs this feature touches.

## Code

- `internal/app/tui.go` — `rebuild()` (`role == "Tool"` style branch, the
  muted-tools change), `Update` key dispatch (the chord wiring), `esc`/
  cancel handling (the Alt-prefix decay window must not break it),
  `tool_start`/`tool_result` entry creation.
- `ui/theme.go` — the Muted role; no palette changes expected.
- `specs/prompt-editor` (planned work) — the editor state (cursor, kill
  ring, kill operations, multi-line handling) these keys call into; this
  feature adds bindings, not editor state.

## Tests

- `internal/app/tui_test.go` — rebuild/Update test patterns to extend
  (entry-style assertions, chord dispatch, inertness cases).
- Theme legibility: mirror the themes-spec style × palette test pattern.
- Throwaway probe (M2 task 3) for `tea.KeyMsg` chord payloads on Ghostty.

## Docs

- `README.md` key bindings section, `--help` text, `CHANGELOG.md`.

## Research sources (checked 2026-09-30)

- OpenCode keybinds doc:
  https://github.com/anomalyco/opencode/blob/dev/packages/web/src/content/docs/keybinds.mdx
- OMP keybindings doc:
  https://github.com/YanwuZeng/omp/blob/main/docs/keybindings.md
- Claude Code docs:
  https://docs.anthropic.com/en/docs/claude-code/cli-usage (interactive
  mode) and https://docs.anthropic.com/en/docs/claude-code/terminal-config
- GNU bash reference, Commands for Text (readline emacs bindings):
  https://www.gnu.org/software/bash/manual/html_node/Commands-For-Text.html
