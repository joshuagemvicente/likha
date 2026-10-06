# Tasks: Model effort

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md). Each milestone leaves `go test ./...`
and `go vet ./...` green and can land on its own. Until M5 enables a provider,
the feature is dormant: no provider carries effort, so every step is skipped
and every request is unchanged.

## M1 — Level lists in the catalog (`internal/model/catalog`)

- [x] **T1. Read `reasoning_options` upstream.** In `gen/main.go`, add
      `ReasoningOptions []struct{ Type string; Values []string }` to
      `upstreamModel`. Keep only the `effort` entry's values, in upstream
      order. Ignore `budget_tokens` and `toggle` (spec: out of scope).
      *Verify:* gen table test with effort-only, budget-only, toggle+effort
      and missing-field fixtures.
- [x] **T2. `catalog.Model.Efforts`.** Add `Efforts []string
      json:"efforts,omitempty"`. Reject empty, duplicate or non-lower-case
      names at generation time, so a bad upstream row fails the run instead
      of reaching the picker.
      *Verify:* catalog round-trip test; generator rejects a malformed list.
- [x] **T3. Override support.** Add `Efforts []string` to `overrideModel`.
      When set, it replaces the upstream list and records `efforts` in
      `Overridden`, with `ref` and `verified` required as for every
      override. Add first overrides only where a provider documents the
      mapping (Gemini 2.5 rows: low, medium, high).
      *Verify:* override test; `go generate ./internal/model/catalog` and
      commit the regenerated `models.json` as its own `chore(catalog)`
      commit.

## M2 — Carry the level on the wire (`internal/model`)

- [x] **T4. Provider wire kind.** In `provider.go` add
      `type EffortWire uint8` with `EffortUnsupported` (zero value),
      `EffortTopLevel` (`reasoning_effort`) and `EffortObject`
      (`reasoning: {effort}`). Add `Provider.Effort EffortWire` and
      `Provider.EffortNote string`. Only `claude` sets the note:
      "Anthropic's OpenAI-compatible endpoint ignores effort". Every row
      stays `EffortUnsupported` until M5.
- [x] **T5. Level resolution.** Add `EffortLevels(p Provider, modelID string,
      live []string) []string`. It returns nil when `p.Effort` is
      unsupported. Otherwise it returns `live` when non-empty, else the
      catalog's `Efforts` for the exact (provider, model) pair. There is no
      prefix matching (AGENTS.md).
      *Verify:* table test over unsupported provider, live-wins, catalog
      fallback, unknown model.
- [x] **T6. Client state.** Add `effortWire EffortWire` and `effort string`
      to `Client`, `SetEffort(wire EffortWire, level string)` and
      `Effort() string`. `Fork` copies both, so explore and profile children
      inherit them. Add `WithoutEffort() (*Client, error)`, a fork with
      effort cleared, for background requests.
      *Verify:* fork test asserts inheritance; `WithoutEffort` asserts
      clearing.
- [x] **T7. Chat-completions body.** Add
      `ReasoningEffort string json:"reasoning_effort,omitempty"` and
      `Reasoning *reasoningParam json:"reasoning,omitempty"` to the body in
      `Client.stream`. Fill exactly one of them, by wire kind, when `effort`
      is non-empty.
      *Verify:* golden test that an empty effort gives bytes identical to
      the pre-feature body; body tests for both wire kinds.
- [x] **T8. Responses body.** Add `Reasoning *codexReasoning
      json:"reasoning,omitempty"` to `codexRequest` and pass the level into
      `BuildCodexRequest`. Update its doc comment: effort is sent only when
      set and only after the chatgpt probe.
      *Verify:* `codex_test.go` sibling of the "reasoning must never be
      sent" test, covering both an empty and a set level.
- [x] **T9. Typed rejection.** When a request carrying effort gets HTTP 400,
      return `*EffortRejectedError{Level string; Err error}` wrapping the
      existing error. It supports `errors.As`; no string matching. If
      [model-recovery](../model-recovery/tasks.md) T1 (`StreamError`) has
      landed, wrap that error instead; the rejected kind is already
      non-retryable.
      *Verify:* httptest 400 with effort gives `EffortRejectedError`; 400
      without effort does not.

## M3 — Background requests and storage

- [x] **T10. Background requests send Default.** `agent/compaction.go` and
      `agent/sessionname.go` call `client.WithoutEffort()` and use the
      result. `agent` imports only `model`, so the import rules hold.
      *Verify:* agent tests capture compaction and naming bodies at
      effort `high` and find no effort field.
- [x] **T11. Stored levels.** Add `Efforts map[string]map[string]string
      json:"efforts,omitempty"` (provider → model → level) to
      `providers.StoredProviderConfig`, with helpers to read, set and delete
      one pair. Choosing Default deletes the entry.
      *Verify:* config round-trip; saving another preference keeps
      `efforts`.
- [x] **T12. Apply at every model change.** Wherever the active model is
      set (app startup after `SetModel` or the connection check, setup
      completion, ChatGPT sign-in in `oauth_login.go`, `activateModelClient`,
      `applyModel`), resolve the stored level against `EffortLevels` and
      call `SetEffort`. A stored level not in the list gives Default and one
      notice entry (spec point 19).
      *Verify:* TUI test with a stored valid level, a stored stale level,
      and no stored level.

## M4 — TUI (`internal/tui`)

- [x] **T13. Effort step in the models dialog.** Add step state to
      `dialogState`: the target row, the levels, the step cursor, the
      model-list cursor to return to, and whether the step applies effort
      only (`/effort`).
      - `confirmDialog` opens the step when `EffortLevels` is non-empty,
        otherwise applies as today.
      - In the step, Enter applies model and level together; Esc restores
        the list and its cursor.
      - Typing does not filter while the step is open.
      - `modelsRow` gains the live levels and default level from
        `ModelDetails`.

      *Verify:* dialog tests: open, preselect remembered level, Enter
      applies both, Esc returns unchanged, no-level model applies on first
      Enter.
- [x] **T14. Rendering.**
      - Title: `Effort · <model> (<provider>)`.
      - Rows: `Default` with `provider decides` (plus `(<level>)` when a
        live default exists), then each level with the spec's hint.
      - Footer: `↑/↓ navigate  Enter apply  Esc back to models`.
      - Readable without color.

      *Verify:* view tests at 40, 80 and 120 columns; no-color check.
- [x] **T15. Switch entries.** Extend the "Model switched to …" entry with
      `, effort <level>`. On a reasoning model whose provider cannot carry
      effort, add the reason: `EffortNote`, else
      `not yet verified for <provider>`.
      *Verify:* entry text tests for each case.
- [x] **T16. `/effort`.**
      - In `handleCommand`, a bare `/effort` opens the step in effort-only
        mode.
      - `/effort <level>` validates against `default` plus the current
        levels.
      - Errors name the accepted values and keep the draft.
      - Unavailable effort prints the reason.
      - It refuses during a run or pending review.
      - Add to `commandHelp` and the `commandcomplete.go` list.

      *Verify:* command tests for each spec point 8–13 case.
- [x] **T17. Status bar.** In `status_line.go`, append ` · <level>` to the
      model segment when the level is not Default. When fitting a narrow
      width, drop the level before shortening the model name.
      *Verify:* status tests at Default, at `high`, and at a width where
      the level drops.
- [x] **T18. Rejection hint.** When a turn error matches
      `EffortRejectedError` via `errors.As`, append
      `Sent with effort <level>; /effort default sends none.`
      *Verify:* TUI test with an httptest 400.

## M5 — Live levels and provider enablement

- [x] **T19. OpenRouter live levels.** Extend `decodeModelDetails` to read
      `reasoning.supported_efforts` and `reasoning.default_effort` when
      present into `ModelDetails` (new `Efforts []string`,
      `DefaultEffort string`). Absent fields leave both empty.
      *Verify:* decoder test with a captured OpenRouter fixture.
- [ ] **T20. Probe and enable, one provider per change.** Probes are run by
      the user. Per provider, record the result in
      [context.md](context.md#provider-support-matrix) and
      `docs/providers.md`, then set `Provider.Effort`. Suggested order:
      openai, openrouter, gemini, xai, deepseek, then the rest. Before
      enabling `openai`, the probe must cover a GPT-6 model with tools at a
      level above `none` (research.md). Mistral stays blocked until its
      chunk-list content parses.
- [x] **T21. Docs and changelog.** `docs/usage.md` covers `/effort` and the
      effort step; `docs/providers.md` gets an effort column. Add an
      **Unreleased** entry in `CHANGELOG.md` that states which providers were
      probed and what was verified.

## Implementation notes (2026-10-06)

Landed for M1–M4, T19 and T21. Where the result differs from the plan:

- **T3.** `models.json` was regenerated from the models.dev snapshot fetched
  on 2026-10-06 (`go run ./gen -in … -upstream https://models.dev/api.json`).
  It carries that day's upstream price and limit drift as well as the new
  `efforts` lists: 418 rows have levels. The three Gemini 2.5 rows get their
  levels from overrides that cite Google's `reasoning_effort` mapping.
- **T1.** An upstream level named `default` (Groq Qwen) is dropped, since
  Likha's Default already sends nothing.
- **T6.** `WithoutEffort` returns the client itself when no effort is set.
  At Default, compaction and naming therefore keep updating the session
  client's usage and rate-limit state exactly as before; a fork is made only
  when there is an effort to clear.
- **T9.** HTTP 422 counts as a rejection too, alongside 400.
  `agent.TurnEvent` gained `Err`, so the TUI can match the cause with
  `errors.As`.
- **T13.** The step is `dialogState.effort`, used both as a sub-state of
  `dialogModels` and, for `/effort`, under the new `dialogEffort` kind.
- **T16.** The run and review refusal comes from the existing gate that
  blocks every command during a run.
  `handleEffortCommand` keeps its own check for robustness.
- **T22 (added). Turn footer.** `transcript.TurnInfo.Effort` is saved as an
  `effort` field that is omitted at Default, so earlier footers and Default
  turns encode as before. The footer shows the level after the model.
- **Status bar.** The effort segment follows the model segment and is
  dropped before the model name is shortened.
- **Same-provider switch.** `applyModel` now resets the status to
  `Connected`, as `activateModelClient` already did, so a stale
  `Listing models` no longer shows under the `/effort` dialog.

Verification: `go test ./...`, `go vet ./...`, `gofmt` clean, and
`go test -race` on model, agent, providers, transcript and the TUI effort
tests. TUI tests enable a provider row in-process the way a passed probe
would; no shipped row is enabled. Outstanding: T20 live probes, and a
real-terminal walkthrough of the effort step.
