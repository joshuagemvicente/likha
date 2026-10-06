# Feature: Animated model-running status beneath the prompt input

**Status:** implemented (local) — full test suite and targeted race checks pass;
live-terminal feel-check pending.

## User-confirmed scope (2026-10-04)

- Replace the exact `Waiting for model` text in the status line beneath the
  editable prompt input with a compact Braille spinner beside `Working…`.
- Do not redesign or move the existing transcript activity row. The earlier
  transcript-dot change is reverted.
- This supersedes only the static-footer behavior in `working-indicator`;
  that feature's transcript, persistence, and setup behavior remain unchanged.
- Animate immediately on submit and continue through model output until the
  run ends or another phase takes over. Tool execution, review, cancellation,
  errors, and idle status retain their existing labels and controls.
- Keep the label still, the indicator width fixed, and cancellation/page/queue
  controls available at 40–200 columns. Use existing theme roles and an ASCII
  fallback; glyph changes make the motion visible without color.
- After implementing dots in the corrected placement, the user was offered
  alternatives and selected the Braille spinner. The final indicator is one
  cell, uses the existing 120 ms tick, and falls back to `| / - \` in ASCII.

## Acceptance

- [x] The bottom status line shows an animated spinner, not `Waiting for model`,
  while the model runs, including after transcript output starts.
- [x] Completion, error, cancellation, and review restore the appropriate status.
- [x] Transcript and setup rendering are unchanged.
- [x] Width, theme, Nerd Font, no-color, and ASCII checks pass.
- [x] Full tests and targeted race checks pass; live-terminal feel-check pending.
