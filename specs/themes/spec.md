# Feature: Themes and optional Nerd Font icons

**Status:** implemented (local). Palette registry covers all twenty-two families with
light/dark variants; the setup theme stage, `/themes` command, `--theme`/`LISA_THEME`
precedence, Nerd Font opt-in glyphs, and muted reasoning rendering are wired and
covered by tests (theme resolution, setup theme stage, reasoning stream/close rules).

## Context

Lisa's interface uses a handful of lipgloss styles (heading, status, plus
entry text). The user wants named themes — the usual terminal palette families
(catppuccin, habamax, gruvbox, tokyonight, nord, dracula, solarized, rose-pine,
kanagawa, everforest) — selectable without recompiling, plus optional Nerd Font
icon glyphs. The `ui` package already carries a `Theme` scaffold with five
style roles; this feature completes it and wires it into the TUI.

## Resolved decisions (user-confirmed)

1. **Theme set:** all listed families: `default` (Lisa's current styling),
   `catppuccin`, `habamax`, `gruvbox`, `tokyonight`, `nord`, `dracula`,
   `solarized`, `rose-pine`, `kanagawa`, `everforest`.
2. **Light/dark:** themes with light+dark variants pick a variant by
   auto-detecting the terminal background (lipgloss/termenv dark-background
   query). Single-variant themes apply as-is. Auto-detection failures fall
   back to dark.
3. **Nerd Fonts:** opt-in only — `--nerd-fonts` flag or `LISA_NERD=1`. Lisa
   cannot detect a patched font reliably; without the opt-in, output stays
   pure ASCII (spec: plain-text identity always legible, zero tofu risk).
4. **Selection UX:** a theme stage inside the first-run setup flow, plus a
   `/themes` **selection dialog** (2026-09-29 polish): the dialog is a
   centered bordered modal listing every theme with the highlighted row; the
   cursor is the selected state (starts on the applied theme); **↑/↓** move it
   (setter), **Enter** applies the highlighted theme and stores it, **Esc**
   closes without changing anything. While the modal is open, prompt input is
   routed to it — keys cannot leak into the draft. `/themes <n-or-name>`
   applies directly without the dialog. `LISA_THEME` env pre-selects and
   skips the stage.
5. **Icon surface:** status and review markers only (waiting/done/error/tool
   prefixes) — never the logo or conversation prose (spec: decoration must not
   compete with essential state).
6. **Style roles:** themes map onto seven roles — Title, Selected, Normal,
   Help, Border, Warning, Error. Conversation prose stays uncolored except
   Error entries; no gradients or color floods (visual-restraint rule).

## Functional changes (v1-spec.md)

- **FR-15 (new):** The user can choose a color theme from the predefined list
  (first-run setup stage, `/themes` command, or `LISA_THEME`). Palettes adapt
  to a detected light or dark terminal. Nerd Font icons are strictly opt-in
  (`--nerd-fonts`/`LISA_NERD=1`) and appear only as status and review markers;
  plain-text output remains complete and legible without them.

## Acceptance criteria

- [ ] Each listed theme resolves to a full palette; unknown names fall back to
      default with a clear note.
- [ ] Light/dark variants auto-select per terminal background where applicable.
- [ ] `LISA_THEME` skips the setup stage; the setup flow includes a theme
      stage after model selection; `/themes` changes it live and stores it.
- [ ] Without the nerd-font opt-in, output is identical to plain ASCII today.
- [ ] With the opt-in, status/review markers use icon glyphs; conversation
      prose and the logo remain plain text.
- [ ] Stored choice persists in `config.json` and survives restarts.
