# Tasks: plan/todo

1. Register one bounded full-list update schema and immutable session plan record.
   **Done:** invalid updates preserve prior list and return accurate errors.
2. Add backward-compatible private persistence and interruption annotation via
   the coordinated session writer; distinguish declared completion from evidence.
   **Done:** resumed/legacy plans display without scheduling or permissions.
3. Add compact progress, `/todo`, active Inspect, and full-list help text.
   **Done:** users can inspect all steps without losing composer/queue state.
4. Connect read-only mode eligibility and run existing checks under
   [shared constraints](../tooling-platform/role.md); record unverified cases.

Implementation notes (2026-10-19, automated checks only):

- The run marker (`session.Store.MarkRunStarted`/`MarkRunCompleted`) carries
  a `session.RunToken` (start time in ns). Completion clears only the
  marker with its own token; a start always replaces the marker (tokens are
  not compared, so a marker stamped ahead of a clock later stepped back
  cannot outlive every later run). Both the TUI and headless write the
  marker only while the run's context is live, after `prompt_submit`; a run
  cancelled during that hook neither writes it nor marks its plan
  interrupted (headless `began`).
- A cancelled, failed or force-stopped run that began work calls
  `session.MarkPlanInterrupted` on the snapshot before its save (TUI
  `endRunMarker`, headless `save`), so the row itself carries the
  interruption. Every run that began then clears its own marker once that
  save succeeded (TUI `clearRunMarker` after `persist`, headless
  `clearRunMarker`); a failed save keeps it, so only a run that dies or
  cannot save leaves a marker. The TUI tracks "began" apart from "marker
  row written", so a failed marker write still marks a cancelled run's plan
  interrupted.
- `reportUnansweredQuestions` treats its own saved note as settling the
  question, so repeated `--resume` launches add one note in total.
- `plan_update` (and `SanitizePlanText`) keeps tag characters only in the
  three RGI subdivision flags (gbeng, gbsct, gbwls); every other tag
  sequence, well-formed or not, is refused, since it renders as a plain
  black flag hiding its tag letters.
