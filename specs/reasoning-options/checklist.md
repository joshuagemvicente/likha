# Checklist: Reasoning options and tool support in the bundled catalog

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md). Checked items are covered by
automated tests (named in brackets) or by the old-versus-new generator
comparison recorded in [tasks.md](tasks.md#outcome-2026-10-07).

## Catalog contents

- [x] Every row in `models.json` has `"tool_call": true`. [`TestEmbeddedReasoningOptions`]
- [x] A model that publishes effort levels, a thinking budget or a toggle
      carries each of them in `reasoning_options`, in upstream order.
      [`TestBuildKeepsReasoningOptions`, `TestEmbeddedReasoningOptions`]
- [x] Budget bounds appear exactly as published. A bound upstream omits is
      absent, never `0`. [`TestBuildKeepsReasoningOptions`]
- [x] A reasoning model upstream lists with no controls carries `[]`. A
      model with no upstream list carries no `reasoning_options`.
      [`TestBuildKeepsReasoningOptions`, `TestReasoningOptionsRoundTrip`]
- [x] `efforts` on every row matches the file before this change, given the
      same models.dev input. [generator comparison, 829 rows]
- [x] The Gemini 2.5 override rows keep their documented `efforts` and the
      upstream budget/toggle entries beside them. [`TestEmbeddedReasoningOptions`]

## Refusals

- [x] A catalog with a row missing `tool_call`, options on a non-reasoning
      row, an unknown or repeated option type, a bad effort list, a
      negative or inverted budget, or `efforts` out of step with its effort
      entry fails to load and names the provider and model.
      [`TestParseRejectsInvalidReasoningOptions`]
- [x] An upstream reasoning-option type the spec does not name stops
      generation and names the type, provider and model.
      [`TestBuildRejectsBadReasoningOptions`]
- [x] An override that sets both `efforts` and `reasoning_options` stops
      generation. [`TestOverridesEditReasoningOptions`]

## Nothing else changes

- [x] `/models`, `/effort`, the status line, `--debug-models`,
      `--probe-effort`, spend and the context meter behave as before. [by
      inspection: no code outside `internal/model/catalog` reads the new
      fields, and `efforts` is unchanged; full suite green]
- [x] Provider requests are unchanged. [by inspection, as above]
- [x] `go test ./...` and `go vet ./...` pass.
