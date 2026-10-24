# Tasks: Model variants

**Status:** planned. Execute after reading [spec.md](spec.md); its MV/A IDs
are the completion criteria. Preserve the uncommitted reasoning-options
catalog work; generated `models.json` is not a hand-edit target.

## M1 — Variant definitions and resolution

- [ ] Add table-driven red tests for ordered cycling, Default versus `none`,
  exact-pair support, metadata presence, budgets and invalid presets (A01–A03,
  A09–A11, A14).
- [ ] Place the variants module inside `internal/model`: ordered `Variant`
  definitions, optional selection ID, logical settings/header/body overlays,
  capability normalization, and deterministic provider lowering. Keep UI and
  persistence imports out of this module.
- [ ] Consume `catalog.Model.ReasoningOptions`, not model-name heuristics or
  another provider's rows. Preserve live capability presence independently
  from legacy `Efforts` compatibility fields.
- [ ] For generated budgets compute an effective maximum below output capacity;
  validate minimum <= effective maximum before deriving `high` and `max`.
- [ ] Add one reference parser/formatter and suffix parser; explicit provider
  plus a slash-bearing model ID remains unambiguous. Reserve `default`.
- [ ] Implement logical settings validation, recursive immutable overlay
  merging, case-insensitive headers, and protected field rejection. Copy
  mutable map/slice values before retaining them as resolved configuration.
- [ ] Public private-config fields: `variants` stores exact-pair preferences
  (`"default"` means explicit Default), `variant_presets` stores ordered
  definitions, `provider_options` and `model_options` store baseline overlays.
  Unknown JSON keys survive read-modify-write. Document an actual example.

Completion: focused model tests cover every branch above; no unsupported
named preset can reach a request builder.

## M2 — Provider wire integration

- [ ] Add manual and disabled thinking to native Claude's request input,
  separate from its current adaptive baseline; make request settings explicit
  so a run does not consult changing global metadata for its chosen preset.
- [ ] Extend compatible chat bodies for OpenRouter budgets/enabled and Gemini
  compatibility thinking fields without conflicting controls.
- [ ] Decouple the Responses protocol from OAuth; route first-party OpenAI
  reasoning models explicitly while leaving custom endpoints on their current
  route. Reuse the Responses parser/tool safeguards, not private Codex auth.
- [ ] Capture completed Responses reasoning output items (including encrypted
  content) as provider-issued replay state and replay only to the same endpoint
  and model. Preserve assistant phase where returned. Never replay an incomplete
  tool response or display reasoning as a substitute.
- [ ] Normalize ChatGPT public visible-model metadata separately from exact
  documented reasoning capabilities. Discovery still gates model visibility.
- [ ] Add local HTTP tests for effort/budget/toggle fragments, full tool
  round-trips, provider errors, mismatched replay and Default baselines.
- [ ] Keep `--probe-effort` opt-in and compatibility wire support intact; its
  old eligibility verdicts remain diagnostic, not runtime feature admission.

Completion: A09, A19 and A22 exercise real adapter HTTP and turn-loop paths.

## M3 — Durable preferences and migration

- [ ] Add private atomic JSON persistence using same-directory temp files,
  Sync, rename and directory Sync. Use a context-aware process/file lock for
  read-modify-write; keep error causes wrapped.
- [ ] Introduce an update operation for config mutations. Convert production
  load/edit/save call sites that otherwise overwrite concurrent settings.
  Retain the simple Save helper for intentional whole-config creation/tests.
- [ ] Preserve unknown settings; corrupt reads must abort writes rather than
  marshal a zero configuration over the file.
- [ ] Migrate valid legacy effort preferences lazily for the selected exact
  pair, never overwriting a new preference; retain stale legacy data and use
  one notice. Make Default explicit in the new preference map.
- [ ] Add an optional committed selection to `session.Snapshot` without
  invalidating old JSON/database rows. Persist no credentials/headers/queued
  execution state. Restore through existing provider credential resolution.
- [ ] Test concurrency, permissions, read/write failures, unknown fields,
  migration idempotence, old snapshots and explicit launch precedence.

Completion: A12–A16 pass through actual files and session storage.

## M4 — TUI flow

- [ ] Replace the old effort-only dialog/handler with a searchable variant
  selector, reusing the dialog layout/list machinery.
- [ ] Keep Update delegation order unchanged. The Ctrl+T branch explicitly
  checks completion/base-surface ownership; no edit-state mutation occurs.
- [ ] Model selection applies first. Valid named target selection closes;
  otherwise Select variant replaces the list if choices exist. Dismissal
  keeps the target model, matching the user's chosen upstream behavior.
- [ ] `/variants` replaces `/effort` in registration/help/autocomplete and
  error guidance. Unknown old commands retain generic command safety.
- [ ] Replace editor/effort UX tests with the new behavior's assertions, retain
  adapter effort tests, and cover no-provider/unavailable/argument errors.
- [ ] Show the composer named variant and next-run distinction during work;
  update the persisted footer with optional variant while decoding legacy effort.
- [ ] A model/preset read or save failure is reported without saying stored;
  selected in-memory versus durable outcomes remain distinguishable.

Completion: A01–A08 pass through `ui.Update`, not only direct helper calls.

## M5 — Execution snapshots and queueing

- [ ] Capture the resolved client configuration in the UI goroutine before
  prompt hooks or model work begin. Give each run an independent fork. The
  mutable composer client is never read by an executing run.
- [ ] Store the admitted provider/model/variant separately for usage/footer
  attribution; explore forks inherit execution, not draft, settings.
- [ ] Fork background naming/compaction at launch even when no variant/replay
  is selected; clear only the variant layer, retaining baseline settings.
- [ ] Keep queued prompt text compatibility, but associate each admitted item
  with a captured resolved selection. Compatible items can steer; the first
  incompatible item holds itself and later FIFO work for another run.
- [ ] Flushing held queues groups only consecutive equal selections and keeps
  other groups. Clear/cancel/blocked hooks clear or retain matching snapshots
  alongside text. Capacity and cancellation remain bounded.
- [ ] Guard acknowledgments by selection revision/run identity. A late save or
  naming result cannot overwrite a newer draft or another session.
- [ ] Commit selection at submission/idle blank Enter; restore sessions through
  explicit flag precedence without auto-submitting held work.
- [ ] Add race/local HTTP tests for cycling while a response/tool/child runs,
  mixed-variant queue FIFO, cancellation/flush, baseline background requests
  and recorded execution variant (A17–A20).

Completion: active request bodies, later tool rounds and footer all retain the
admitted selection even when the UI is changed repeatedly.

## M6 — Headless, docs and final gates

- [ ] Share selection resolution with interactive startup. Explicit suffixes
  override remembered choice; unknown named variants fail before POST.
- [ ] Report variant in headless start/result metadata and preserve existing
  exit codes, no-interaction policy, redaction and account visibility.
- [ ] Add CLI integration tests using built Likha and local servers for changed
  command/selection behavior; use actual HTTP adapter tests for native wires
  that cannot be certified by a custom endpoint override.
- [ ] Update `docs/usage.md`, `docs/providers.md`, glossary/index and superseded
  spec pointers. Keep paid diagnostics documented separately.
- [ ] Run `gofmt`, package checks during implementation, then full
  `go test ./...`, `go test -race ./...`, `go vet ./...`, and build.
- [ ] Review standards and spec compliance. Fix findings before completion;
  list any genuinely incomplete MV/A requirement explicitly.
- [ ] Update checklist/status/CHANGELOG with actual automated evidence.
  Leave live-provider, real-terminal and release gates unchecked.

Completion: every A-ID maps to automated evidence or an explicitly outstanding
manual gate; no feature is claimed by a skipped or mock-only check.
