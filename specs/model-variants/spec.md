# Feature: Model variants

**Status:** planned. Approved 2026-10-09; implementation explicitly requested
after this specification is written. Requirement: [FR-47](../v1-spec.md#model-variants-amendment-user-approved-2026-10-09).

This is the behavioral source of truth for replacing Likha's effort-only UI.
Read [tasks.md](tasks.md) for implementation order and interfaces,
[context.md](context.md) for affected code, and [research.md](research.md) for
the pinned upstream comparison. Completion evidence belongs in
[checklist.md](checklist.md), not in an inference from passing old tests.

## Purpose and agreed decisions

Let the user choose a model's supported request preset without retyping a
slash command or changing the prompt. Variants include reasoning effort,
thinking-token budgets and optional thinking toggles. The requested reference
is OpenCode's full-screen V2 TUI, not its legacy terminal client.

The user chose:

1. **Documented availability.** Official capabilities plus a compatible Likha
   protocol permit variants without a paid live probe. Availability and live
   verification are separate claims. Unknown routes remain gated.
2. **OpenCode's model-picker flow.** Selecting a model restores a valid named
   variant; otherwise the variant selector opens when variants exist, even
   when Default was explicitly remembered.
3. **No `/effort`.** OpenCode V2 retains that alias; Likha intentionally does
   not. `/variants` is the sole variant slash entry point.
4. **Stronger execution isolation.** Composer changes affect future work,
   never an already admitted run, its requests or its children.

## Scope

In scope: Ctrl+T, `/models`, `/variants`, first-run and reconnect restoration,
headless selection, model-reference parsing, preference migration, session
selection, variant request controls, provider protocol compatibility, queued
work, turn attribution, safe request overlays and error reporting.

The existing reasoning-options catalog work is an input. No model catalog is
downloaded by cycling. Authentication, approvals, plan-mode restrictions,
tool capabilities, and OAuth account isolation remain in force.

Out of scope: a general OpenCode command palette, customizable keybinding
system, new providers or agent profiles, private Codex endpoints, new reasoning
display UX, automatic live probes, or release verification without evidence.
The explicit `--probe-effort` diagnostic is retained; it is not the variant
availability gate or proof of a complete tool loop.

## Vocabulary and invariants

Product terms live in [CONTEXT.md](../../CONTEXT.md). These invariants apply
across all entry points:

- A model's variants are an **ordered list**, not a set or global enum.
- Default has no variant overlay. It is distinct from a named `none` preset.
- Variant IDs are case-sensitive. `default` is reserved for the Default
  choice and is not a user-defined named variant.
- Capabilities belong to an exact provider/model and compatible route;
  equal model slugs on different providers do not share support.
- A composer selection and an execution selection can differ.
- Every request in a run uses that run's resolved execution selection.
- Captured provider reasoning state is distinct from display reasoning.
- A preference-save failure cannot be reported as a durable success.
- Selection and presets grant no execution permission or credential access.

## 1. Cycling

MV-01. In the ordinary composer, Ctrl+T cycles Default → first named variant
→ remaining variants in definition order → Default. It does not sort names,
strengths or numeric budgets. One variant alternates with Default.

MV-02. Cycling preserves prompt runes, caret, input selection, kill ring,
history navigation and queued text. Ctrl+T no longer transposes characters.

MV-03. A model with no available variants is a silent no-op. No model request,
metadata fetch, paid probe or transcript success notice is caused by cycling.

MV-04. Setup, credential forms, completion popups, dialogs, questions, web
consent, tool/agent inspection and approval reviews retain keyboard ownership.
Ctrl+T does not cycle or edit input behind these surfaces. A run in the normal
composer does not itself suppress cycling.

MV-05. Cycling changes the composer selection and remembered preference.
The named variant appears beside the model; Default adds no variant label.
When a run is active, the composer selection is visibly a next-run choice
if it differs from execution. Narrow rendering still fits the terminal.

## 2. Selector and model-picker flow

MV-06. `/variants` with no argument opens a searchable **Select variant**
dialog for the composer model. Default is first, followed by named variants
in cycle order. Search filters the view, not the cycle order. The currently
selected choice is identified; arrows navigate, Enter applies, Esc dismisses.

MV-07. With no provider, `/variants` explains how to configure one. With no
variants it explains unavailability without opening an empty dialog. Arguments
are refused with usage guidance and the draft retained. Ordinary slash
commands remain inactive during a run; Ctrl+T is the during-run control.

MV-08. Enter in `/models` selects the highlighted provider/model immediately.
Reselecting the same pair preserves its valid composer selection. Selecting
a different pair restores that pair's preference. If a valid named variant
is restored, the model dialog closes. Otherwise Select variant opens if the
model has choices, including for an explicitly remembered Default. A model
without choices switches and closes without a second step.

MV-09. Esc in the replacement variant selector leaves the newly chosen model
selected. It does not return to the model list or restore the previous model.
Esc in a standalone `/variants` selector leaves its selection unchanged.

MV-10. `/effort` is absent from registered commands, autocomplete and help.
Neither `/effort`, `/effort high`, nor `/effor` is an alias or ordinary prompt.
The generic unknown-command path refuses them; literal slash text still uses
the existing `//` escape. Recovery guidance names `/variants`, not `/effort`.

## 3. Capabilities and availability

MV-11. Published live metadata takes precedence for the fields it actually
describes. The bundled catalog supplies unknown fields by exact pair. An
explicit live empty allowlist must not be replaced by catalog levels. For
OpenRouter, null means its documented unrestricted gateway allowlist, not
an empty list. Absent, null, empty, invalid and explicit false remain
distinguishable wherever the protocol gives them different meanings.

MV-12. Mandatory reasoning never offers a thinking-off preset. Default is
still available because it is not an off request. A malformed capability is
not affirmative support. Its diagnostic names the capability, not secret data.

MV-13. Preserve published effort order. When both effort and budget controls
exist, the default generated named list prefers effort, as OpenCode does.
An independently supported toggle-off choice may precede that list; duplicate
IDs appear once. Explicit configured presets can expose another supported
control without creating an invalid combination.

MV-14. For budget-only controls, generated `high` and `max` presets are bounded
by the model's documented budget range and output limit, leaving output room.
No budget below the minimum or at/above a conflicting output limit is offered.
Unknown required bounds or an impossible range produce no fabricated preset.
Descriptions distinguish budget presets from similarly named effort presets.

MV-15. First-party documented controls are available when their Likha adapter
supports them. The initial adapters cover OpenAI/ChatGPT Responses effort,
Claude Messages effort/manual budgets/toggles where published, OpenRouter's
unified reasoning controls, Gemini compatibility controls, and documented
top-level effort on other admitted first-party compatible protocols.
Undocumented relays and custom destinations get no generated variants merely
because the user supplied a known provider name.

MV-16. Account-specific ChatGPT model visibility still comes from its
authenticated public model list. Variant metadata may supplement a visible
model, but never makes an inaccessible model visible. Existing public SIWC
registration, scope enforcement, refresh and invalidation remain unchanged.

## 4. Request protocols and overlays

MV-17. A named variant lowers to the exact request protocol: Responses
`reasoning.effort`, compatible chat `reasoning_effort`, OpenRouter `reasoning`,
Claude `thinking`/`output_config`, or Gemini's documented compatibility fields.
Effort and budget/level fields that the provider declares mutually exclusive
are never sent together. Adaptive-only Claude models never receive manual
thinking; manual-only models never receive adaptive thinking.

MV-18. API-key OpenAI reasoning models can use Responses independently of
OAuth. Custom destinations do not silently inherit that protocol. The public
ChatGPT Responses path and its namespace/tool validation remain supported.
A successful variant requires a complete tool round-trip, including preserved
provider reasoning items/signatures when that protocol requires them.

MV-19. Private user configuration may define provider defaults, per-model
defaults and ordered named variant presets. Effective settings apply in that
order: provider → model → selected variant. Nested objects merge; arrays and
scalars replace; header names merge case-insensitively. Literal request body
fields stay literal and are not interpreted as logical settings.

MV-20. Overlays cannot replace destination, authentication, credential
headers, session routing, model ID, conversation/input, advertised tools,
streaming, or stateless-storage safety controls. Unsupported logical settings,
reserved IDs, duplicate preset IDs, invalid budgets and contradictory controls
fail validation before inference. A user-defined preset alone does not certify
an undocumented destination. Headers and raw settings never enter transcript,
session selection or diagnostics as unredacted secret values.

MV-21. Selecting Default removes only the variant layer. Baseline provider
or model settings can still request reasoning. With no configured overlays,
the ordinary default request retains the existing protocol's baseline behavior,
except the explicitly documented OpenAI Responses protocol change.

## 5. Preferences, migration and session selection

MV-22. Preferences use exact provider/model pairs and distinguish three states:
absent preference, explicit Default, named variant. Explicit Default wins over
configured variant defaults. Switching models does not carry a variant name
from another pair; switching away and back restores the target pair's choice.

MV-23. Valid legacy `efforts` values migrate to variant preferences without
overwriting an existing new preference. Unmapped legacy values are retained
for inspection and cause one stale-choice notice when relevant. Migration is
idempotent, uses a single successful private update, and preserves theme,
composer, context windows, web, attention, command trust and unknown settings.

MV-24. A saved session may retain its committed provider/model/variant.
Restoring that session uses its selection ahead of global preferences when the
route/account is available and launch flags do not explicitly override it.
Old sessions without a selection retain current startup behavior. Session
restore never restores approvals, executes queued work, or recreates live tasks.

MV-25. Submitting work commits its captured selection; blank Enter in an idle
existing session may commit a pending selection without making an inference
request. Ctrl+T during a run persists the preference but does not mutate that
run's committed execution selection. A stale completion acknowledges only the
captured revision and cannot clear newer composer edits.

MV-26. The canonical reference is `provider/model#variant`; the variant suffix
is omitted for Default. Existing raw model IDs, including IDs containing `/`,
still work with an explicitly supplied provider. An explicit `#default` clears
the variant. Empty/multiple suffixes and unknown explicit IDs fail visibly
before inference rather than silently selecting Default.

MV-27. A stale remembered or session variant falls back to Default with one
notice identifying the pair and choice. Explicit arguments/configuration are
validated as requests and fail on unknown IDs. A selected model changing its
metadata cannot change the frozen execution selection of an admitted run.

MV-28. Preferences are written atomically with private permissions and a
lock-protected read-modify-write operation. Concurrent updates preserve other
settings. Corrupt/unreadable files are reported and retained, not treated as
empty writable configuration. An in-memory selection may remain usable after
a persistence failure, but the error explicitly says it was not saved; no
success message claims otherwise.

## 6. Running, steering and queued work

MV-29. Before asynchronous prompt preparation, capture provider, model,
variant and the resolved request settings. The admitted run uses that immutable
execution selection for all tool rounds, retries and explore children. Shared
OAuth refresh state remains safe; mutable request configuration is not shared.

MV-30. Ctrl+T in the normal working composer edits the next selection only.
The current turn's footer still names its execution variant. Baseline naming
and compaction requests use independent request settings without the chosen
variant, so concurrent naming cannot race a composer change.

MV-31. Enter during a run captures the queued prompt's selection. Prompts
matching the active execution selection retain normal steering behavior.
Once a different-selection item is held, it and later items wait in FIFO order
for separate work; later matching prompts cannot overtake it.

MV-32. Ctrl+Enter can steer only work compatible with the active execution
selection. A different-selection prompt remains held for the next run with
visible guidance; it never interrupts or changes the active request. Cancel
retains queued text under the existing held-queue controls. Explicit clearing
drops the associated snapshots too. Capacity remains bounded.

MV-33. Flushing held work groups only consecutive prompts with the same
execution selection. It never collapses differently selected prompts into one
run or resolves their settings from a newer composer choice. Queued selections
contain no credential copies on disk and never auto-execute after restart.

## 7. Attribution, errors and compatibility

MV-34. A completed/cancelled/failed turn's persisted footer reports the model
and named variant actually used. Default adds no label. Existing footers with
an `effort` field remain readable; future variant labels need not be effort
names. Headless start/result metadata identifies the chosen variant without
changing the existing exit-code or tool-refusal contract.

MV-35. A provider rejection retains its original error and says which variant
was sent, with `/variants` → Default guidance. It does not claim every 400/422
was caused by a variant. A rejected request is not retried at another variant
or silently downgraded. Replay guidance remains independently attributable.

MV-36. No preference or variant operation changes approvals, plan mode, tool
policy, account ownership, request budgets, context accounting or pricing
identity. Usage belongs to the executing provider/model, not the newest draft.

## Acceptance scenarios

| ID | Scenario | Required observable result |
| --- | --- | --- |
| A01 | Variants `[low, high]`; press Ctrl+T three times | low, high, Default; unchanged input/caret |
| A02 | Zero/one/custom-order variants | no-op / alternate / preserve custom order |
| A03 | Default versus named `none` | Default removes overlay; `none` sends documented off control |
| A04 | Completion, review, credential, question or inspection open | no cycle and no hidden draft edit |
| A05 | Pick different model with saved named variant | switch immediately, restore exact-pair variant |
| A06 | Pick model with Default preference or no preference | chosen model plus Select variant, Default first |
| A07 | Dismiss replacement selector | chosen model remains; no old-model rollback |
| A08 | `/effort`, `/effor`, invalid `/variants` argument | visible command error, no inference |
| A09 | Effort, budget-only and toggle-only published controls | matching wire fragments, bounded valid presets |
| A10 | Live empty/null/mandatory/invalid metadata | distinct semantics, no false support/off preset |
| A11 | Settings/body/header overlays | deterministic merge, baseline immutable, protected fields refused |
| A12 | Legacy preferences and old sessions | valid migration, unrelated settings retained, old footers readable |
| A13 | Relaunch/resume/explicit launch override | documented precedence, no automatic work replay |
| A14 | Unknown explicit variant | fail before POST, no Default downgrade |
| A15 | Stale remembered variant | Default plus one notice, no corruption |
| A16 | Corrupt config, save failure, concurrent settings writes | visible unsaved result, intact/private file, no lost unrelated updates |
| A17 | Change draft during streaming, tool round, child or retry | execution remains unchanged; footer/usage match execution |
| A18 | Queue differently selected prompts, cancel, flush | FIFO and captured selection retained; no cross-variant grouping |
| A19 | Tool round-trip on each adapter | call executes once; follow-up sends valid provider replay state |
| A20 | Background naming/compaction while cycling | independent baseline settings and accurate usage |
| A21 | Headless explicit/default/invalid selection | matching selection and refusal/exit contracts, no interaction |
| A22 | Provider 400 with variant | original error plus variant hint; no fallback POST |

## Verification and completion

Automated entry point: `go test ./...`. New behavior must be exercised through
real request builders, local HTTP streams, the turn loop and Update routing,
not only fabricated provider table mutations. Race checks must cover selection,
background work, queued prompts and configuration updates. `go vet ./...` and
standard Go formatting remain required.

Automated checks establish **implemented (local)** only for the behaviors they
exercise. A real terminal walkthrough covers Ctrl+T delivery, completions,
dialogs, rendering and queued controls. User-authorized live provider checks
cover acceptance and complete tool replay, with caps and sanitized evidence.
Those outstanding checks must remain unchecked and must not be called
**verified (release)**. No request is made merely to finish a checklist.
