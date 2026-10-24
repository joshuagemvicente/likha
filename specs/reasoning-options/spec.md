# Feature: Reasoning options and tool support in the bundled catalog

**Status:** implemented (local) 2026-10-07. Decisions recorded 2026-10-07
(user interview); drafting defaults D1–D7 implemented as written and still
open to review. Verified by automated checks only (`go test ./...`,
`go vet ./...`, `gofmt`) and an old-versus-new generator comparison on the
same models.dev input; nothing at runtime reads the new fields.
Refines [model-metadata](../model-metadata/spec.md) (the catalog's fields)
and [model-effort](../model-effort/spec.md) (where effort levels come from).
No new functional requirement: nothing a user sees or sends changes, so
v1-spec is not amended.

## Context

The bundled catalog (`internal/model/catalog/models.json`) is generated from
models.dev. For reasoning controls it keeps only one thing today: the named
levels of a model's `effort` option, flattened into `efforts`. models.dev
publishes three kinds of control in `reasoning_options`:

- `{"type": "effort", "values": ["low", "medium", "high", "xhigh", "max"]}`
  for named levels,
- `{"type": "budget_tokens", "min": 1024, "max": 63999}` for a thinking-token
  budget, where either bound may be missing,
- `{"type": "toggle"}` for reasoning that can be switched on and off.

The generator drops the budget and toggle entries, so the catalog cannot say
that Claude Haiku 4.5 accepts a thinking budget of at least 1,024 tokens, or
that a model's reasoning can be switched off. It also never writes
`tool_call`. Every row is tool-capable because the generator keeps only
`tool_call: true` models, but the file does not say so.

The stencil.so catalog (`catalog.stencil.so/models.json`) was checked again
on 2026-10-07 at the user's request. For all 13 upstream providers Likha
maps, it matches models.dev on limits, cost, `reasoning`,
`reasoning_options` and `tool_call`. It drops `last_updated`, and adds
`int`, `tps` and `kind`. The model-metadata decision (upstream is models.dev)
stands. See [context.md](context.md).

## Role

Make the catalog record every reasoning control a model publishes, plus its
tool support, exactly as upstream or a cited document states it, so a later
feature can offer budget or toggle control without another catalog change.
Change nothing Likha shows or sends.

## Scope

In scope: the generator, `overrides.json`, the `models.json` row format, the
catalog's Go types and validation, and regenerating `models.json`.

Out of scope:

- Any runtime use of budget or toggle data. Picking a thinking budget or
  switching reasoning off stays out of scope, as in model-effort. Doing
  either needs its own spec and a v1-spec amendment.
- Showing the new fields in `/models`, `/effort`, the status line or
  `--debug-models`.
- Stencil's `int` (intelligence score), `tps` (tokens per second) and
  `kind`. The user declined them on 2026-10-07.
- Switching the upstream to stencil.so, or reading two upstreams.
- Rows for models without tool support. The generator still keeps only
  `tool_call: true` models.

## Resolved decisions (user, 2026-10-07)

1. **Upstream stays models.dev.** Stencil carries the same values for every
   field this spec adds, and lacks `last_updated`, which feeds `updated`.
2. **Store the full list.** Each row carries `reasoning_options` as
   upstream publishes them: type, values, min and max, in upstream order.
   `efforts` stays, derived from the `effort` entry, so every current
   reader keeps working unchanged.
3. **Catalog data only.** No user-visible or request-side change.
4. **`tool_call` is explicit.** Every row writes `"tool_call": true`. The
   tool-capable-only filter is unchanged.

## Behavior

### Row format

A `models.json` row gains two fields. Every existing field keeps its meaning.

```json
"claude-opus-4-5": {
  "name": "Claude Opus 4.5 (latest)",
  "context": 200000,
  "output": 64000,
  "reasoning": true,
  "tool_call": true,
  "reasoning_options": [
    {"type": "effort", "values": ["low", "medium", "high"]},
    {"type": "budget_tokens", "min": 1024}
  ],
  "efforts": ["low", "medium", "high"],
  "source": "models.dev"
}
```

1. **`tool_call`** is `true` on every row, generated and curated.
2. **`reasoning_options`** lists the model's reasoning controls in upstream
   order. Each entry has a `type` and only that type's keys:
   - `effort`: `values`, a non-empty list of level names, in upstream order,
     exactly as published (including an upstream `default`).
   - `budget_tokens`: optional `min` and `max`, whole token counts. A
     missing bound is unknown, never zero.
   - `toggle`: no other keys.
3. **Absent and empty differ.** A missing `reasoning_options` means the
   source lists no controls for the model. An empty list `[]` means the
   source says the model reasons with no control (always on). On the
   2026-10-07 data, 75 reasoning rows carry `[]`.
4. **`efforts`** is the `effort` entry's `values` without `default`. This is
   the same rule as today, so `efforts` is unchanged on every row (decision
   2). A row without an `effort` entry has no `efforts`.

### Overrides

5. An override may set `reasoning_options` to replace the row's whole list.
   The row's `overridden` names `reasoning_options`, and `ref`/`verified`
   vouch for the whole list.
6. An override may still set `efforts` alone (the three Gemini 2.5 rows do).
   It replaces the `effort` entry's values, or appends an `effort` entry
   when the row has none. The other entries stay upstream values, and
   `overridden` names `efforts`. An override that sets both `efforts` and
   `reasoning_options` is an error.
7. Curated rows (no upstream, e.g. `chatgpt`) carry `reasoning_options` only
   when the override writes it, and always carry `"tool_call": true`.

### Validation

The catalog refuses to load, and the generator refuses to write, when:

8. a row has `tool_call` missing or `false`;
9. a row has `reasoning_options` but `reasoning` is not `true`;
10. an entry has an unknown `type`, a key that does not belong to its type,
    or a type that appears twice in one row;
11. an `effort` entry has no values, a duplicate value, or (after dropping
    `default`) a value `ValidateEfforts` rejects;
12. a `budget_tokens` bound is negative, not a whole number, or `min`
    exceeds `max`;
13. `efforts` differs from what rule 4 or rule 6 derives.

14. **Unknown upstream types and keys stop generation.** If models.dev
    publishes a reasoning-option type other than the three above, or a key
    other than `type`, `values`, `min` and `max`, or a fractional budget
    bound, the generator fails and names the provider and model. It never
    drops or rounds the entry silently. Supporting a new type or key is a
    spec change.

### What users see

15. Nothing changes: `/models`, `/effort`, the effort step, the status line,
    `--debug-models`, `--probe-effort`, spend and the context meter behave
    exactly as before, and every provider request is byte-identical.

## Drafting defaults (await review)

- **D1.** The schema version stays `1`. The new fields are additive, and the
  reader and file always ship in the same build.
- **D2.** `reasoning_options` keeps upstream `default` in effort values; only
  `efforts` drops it (rule 4).
- **D3.** Rule 9 is checked on output. On 2026-10-07, no upstream row has
  options without `reasoning: true`. If one appears, generation fails and
  the row needs an override.
- **D4.** Budget bounds are stored as published, with no normalization. A
  `max` of 63,999 stays 63,999.
- **D5.** `tool_call` is checked on every row (rule 8), not omitted when
  true. The extra bytes are the price of a self-describing file.
- **D6.** The generator's report gains counts of rows with an `effort`,
  `budget_tokens` or `toggle` entry, and with `[]`.
- **D7.** The spec folder is named `reasoning-options` after the upstream
  field.

## Acceptance criteria

- Regenerating from the same models.dev input before and after the change
  leaves every existing field of every row identical, `efforts` included.
- Every row has `"tool_call": true`.
- `claude/claude-haiku-4-5` carries `[{"type": "budget_tokens", "min":
  1024}]`. `claude/claude-sonnet-5` carries a `toggle` entry then an
  `effort` entry, in that order.
- The three Gemini 2.5 rows keep their override `efforts`, and keep the
  entries upstream publishes next to them: `gemini-2.5-pro` keeps
  `budget_tokens` (128–32,768); `gemini-2.5-flash` and
  `gemini-2.5-flash-lite` keep `toggle` and `budget_tokens`. Each gains an
  appended `effort` entry (rule 6).
- Each validation rule 8–13 has a failing-input test, and an unknown
  upstream type fails generation with the type named.
- `go test ./...` and `go vet ./...` pass. No test outside
  `internal/model/catalog` needs a change.
