# Tasks: Model-running status

1. Restore the mistaken transcript edits without touching other local work.
2. Render a fixed-width spinner in the existing status hint, fitting plain
   text before applying theme styles.
3. Share the existing 120 ms activity tick and re-arm it after transcript
   handoffs while the model-running status is active; stale clocks still drop.
4. Verify bottom placement, streaming lifecycle, controls, widths, fallbacks,
   unchanged transcript rendering, and the full test suite.
5. Offer replacement animation choices after the corrected implementation;
   implement the selected Braille spinner without changing placement.
