# Tasks: Reasoning options and tool support in the bundled catalog

Ordered implementation tasks. T1–T9 done 2026-10-07 (automated checks
only). Behavior wording lives in [spec.md](spec.md), code paths in [context.md](context.md). Each milestone
leaves `go test ./...` and `go vet ./...` green. Nothing outside
`internal/model/catalog` changes.

## M1 — Catalog types and validation (`internal/model/catalog`)

- [x] **T1. Types.** Add `ReasoningOption{Type string; Values []string;
      Min, Max *int64}` with the type constants `effort`, `budget_tokens`
      and `toggle`. On `Model`, add `ToolCall bool` (`json:"tool_call"`, no
      omitempty) and `ReasoningOptions []ReasoningOption`. Keep `nil` (absent)
      distinct from `[]ReasoningOption{}` (`[]`) through marshal and
      unmarshal (spec rule 3). A plain `omitempty` slice collapses the two;
      the field uses `omitzero` (Go 1.24), which omits only nil.
      *Verify:* round-trip test: absent, `[]` and a three-entry list each
      survive `json.Marshal`/`Parse` unchanged.
- [x] **T2. Validation.** Extend `validateModel` with spec rules 8–13.
      `efforts` consistency (rule 13): `efforts` must equal
      `catalog.EffortLevels(reasoning_options)` (effort values without
      `default`). One rule covers override rows too, because an `efforts`
      override writes the same levels into the effort entry.
      *Verify:* table test with one failing row per rule (8, 9, 10 ×3
      cases, 11 ×3, 12 ×3, 13), plus a passing row per entry type.

## M2 — Generator (`internal/model/catalog/gen`)

- [x] **T3. Read every option.** Extend `upstreamReasoningOption` with
      `Min`, `Max *float64`. Convert to `catalog.ReasoningOption`, rejecting
      non-whole bounds. Fail on an unknown `type` with
      `provider/model: unknown reasoning option type %q`, and on an unknown
      key through a strict `UnmarshalJSON` (rule 14). Keep upstream
      presence: `null`/missing → nil, `[]` → empty.
      *Verify:* `main_test.go` fixtures: all three types, missing bounds,
      `[]` versus missing, and an unknown type that errors.
- [x] **T4. Derive `efforts` from the list.** Rewrite `upstreamEfforts` to
      read the converted list. Behavior is identical (drop `default`).
      *Verify:* the existing effort tests pass unchanged.
- [x] **T5. `tool_call`.** Set `ToolCall = true` on every kept upstream row
      and every curated row.
      *Verify:* fixture asserts it on both kinds.
- [x] **T6. Overrides.** Add `ReasoningOptions` to `overrideModel`.
      `reasoning_options` replaces the list and records
      `"reasoning_options"` in `overridden`. `efforts` alone edits or
      appends the effort entry (rule 6). Both on one row → error.
      *Verify:* fixtures for replace, edit, append and the both-set error.
- [x] **T7. Report counts (D6).** Add rows with effort, budget_tokens,
      toggle and `[]` to the generator report.
      *Verify:* report fixture.

## M3 — Regenerate and check

- [x] **T8. Regenerate.** Run the generator on a saved models.dev snapshot
      (`go run ./gen -in <snapshot> -upstream https://models.dev/api.json`),
      so the old generator could be run on the same input. Review the
      diff: only `tool_call` and `reasoning_options` added, and `generated`
      and models.dev drift changed. Before committing, diff `efforts` on
      every row against the previous file with a throwaway script. Expect
      zero changes. Commit as `chore(catalog): regenerate models.json`,
      separate from T1–T7 (not committed yet).
      *Verify:* `go test ./...`; the acceptance rows in spec.md checked by
      `catalog_test.go` assertions against the embedded file.
- [x] **T9. Docs.** Update the `models.json` field list in
      `specs/model-metadata/spec.md` (§ Fields) and the `Model` doc comment.
      Add a CHANGELOG entry under Unreleased that states the verification
      honestly (automated checks only, no runtime change).
      *Verify:* review.

No live probe or terminal walkthrough applies: nothing at runtime reads the
new fields (spec rule 15).

## Outcome (2026-10-07)

- Old (HEAD `18663da`) and new generators run on the same models.dev
  snapshot (fetched 2026-10-07) give identical values for every pre-existing
  field on all 829 rows, `efforts` included. The new file adds
  `"tool_call": true` to all 829 rows and `reasoning_options` to 657.
- Report: 425 effort, 73 budget_tokens, 244 toggle entries; 75 rows with no
  control.
- The committed file also picks up one day of models.dev drift since
  2026-10-06: 7 models added, 1 removed (`openrouter/kwaipilot/kat-coder-pro-v2.5`),
  25 rows changed (mostly OpenRouter prices, some Mistral context windows).
  The generator printed no change list for it, because the previous file
  fails the new `tool_call` rule and so cannot be compared. The drift was
  diffed by hand instead.
- Tests: catalog `TestParseRejectsInvalidReasoningOptions`,
  `TestReasoningOptionsRoundTrip`, `TestEmbeddedReasoningOptions`; gen
  `TestBuildKeepsReasoningOptions`, `TestBuildRejectsBadReasoningOptions`,
  `TestOverridesEditReasoningOptions`.
