# Context: Effort probe

Code, tests and docs this feature touches, with the facts that shaped the
spec. Decisions live in [spec.md](spec.md); sources in
[research.md](research.md).

## Request code the probe reuses

- `internal/model/client.go`
  - `Client.stream` builds the chat-completions body (`model`, `messages`,
    `tools`, `stream`, `stream_options`, and one of `reasoning_effort` or
    `reasoning` when an effort is set). Its `attempt` loop may send a second
    request without `stream_options` when the first is rejected for it; the
    probe counts that as one row.
  - A 400 or 422 on a request carrying effort returns
    `*EffortRejectedError`; 401/403 wrap `ErrUnauthorized`. Other statuses
    are plain errors with the status only in the text, so the probe reads
    the status from its own transport wrapper (tasks T2), not from the
    message. A 400/422 at Default (no effort) is a plain error too, which
    is why the recorder's status decides spec decision 4.
  - The body carries no `max_tokens` or `max_completion_tokens`, so a
    chat-completions probe request has no output cap (spec decision 7).
  - `stream` makes one attempt today. model-recovery (FR-36, planned) moves
    a transient-retry loop into `Client.stream` with a `model.WithoutRetry`
    context opt-out; the probe must use it once that lands (tasks T7).
  - `StreamUsage` returns `RequestUsage` (prompt, completion, reasoning,
    cost). `Reasoning` is 0 both when absent and when reported as 0; tasks
    T1 adds `ReasoningSeen`. On any error, and when the response had no
    usage object, it returns zero usage with `usageOK=false`, so failed and
    cut-off rows never report spend (hence the 3,000-token charge in spec
    decision 8).
  - `ListModelsWithDetails` returns `ModelDetails.Efforts` and
    `DefaultEffort` (OpenRouter live levels). It is a `GET` and is not
    billed, so it may run before consent.
- `internal/model/effort.go`
  - `EffortWire` (`EffortUnsupported`, `EffortTopLevel`, `EffortObject`),
    `SetEffort`, `Effort`, `EffortLevels`, `EffortRejectedError`.
  - `EffortLevels` returns nil for an unsupported provider row, so the probe
    passes a local copy of the row with `Effort` set to the shape under test.
- `internal/model/fork.go`: `Fork` and `ForkForTask`. `ForkForTask` already
  wraps the HTTP transport; `ForkWithTransport` (tasks T2) follows it. The
  ChatGPT path (`doOAuthRequest` in `client_oauth.go`) sends its POST
  through the same `c.http`, so the wrapper sees it; token refreshes go
  through the OAuth session's own client. `Fork` refuses an OAuth client
  whose base is not `ChatGPTResource`, so a ChatGPT test must redirect in
  the transport rather than change the endpoint.
- `internal/model/client.go` `SetSessionHeader`/`SetSession`: the OpenCode
  relays (`SessionHeader: "x-opencode-session"` in `provider.go`) expect a
  per-conversation ID; `Run` sets it from the SQLite session, which the
  probe does not open, so the probe supplies its own (tasks T13).
- `internal/model/codex.go`, `responses_stream.go`: the ChatGPT Responses
  path. `BuildCodexRequest(model, effort, …)` adds `reasoning: {effort}`;
  `parseCodexUsageDetailed` reads `output_tokens_details.reasoning_tokens`.
- `internal/model/usage_report.go`: `usageDetail` and
  `withAdditiveReasoning` (xAI reports reasoning on top of completion; the
  probe's completion count is the normalized one).
- `internal/model/pricing.go`: `ModelPrice`, `IsSubscription`,
  `RequestCost` for the estimate and per-row cost.
- `internal/model/context_estimate.go`: `EstimateInputTokens` for the
  estimate's input size.
- `internal/model/provider.go`: the `Providers` table. The probe reads it
  and never writes it.

## CLI and credentials

- `internal/app/run.go`
  - Flags are a single `flag.FlagSet`; standalone modes are flags
    (`--login`, `--sessions`, `--version`). `--login` branches before the
    repository is resolved; the probe branches at the same point.
  - `providers.ResolveProvider(name, endpoint, apiKey, persistKey,
    stateDir)` stores the key when `persistKey` is true. Normal runs set it
    when `--api-key` was passed; the probe always passes false. Key order:
    `--api-key` (whose default is `LIKHA_API_KEY`), then the row's `KeyEnv`,
    then the stored key. With an empty provider name it returns an endpoint
    and no key at all, one reason custom endpoints are out of scope.
  - `--provider` and `--model` default to `LIKHA_PROVIDER` and
    `LIKHA_MODEL`. A normal run falls back to the stored first-run provider
    and model (`LoadStoredConfig`); the probe does not (spec point 1).
  - `resolveStateDir` locates the private state directory
    (`LIKHA_STATE_DIR`, else `$XDG_CONFIG_HOME/likha` or `~/.config/likha`).
- `internal/app/oauth.go`: `bindOAuthStorage` (refresh writes rotated
  tokens through `providers.RefreshOAuth`) and `discoverOAuthModel`.
- `internal/app/model_metadata_log.go`: the pattern for a private file in
  the state directory (`MkdirAll` 0700, open 0600, `Chmod(0600)`).
- `tests/integration/cli_test.go`: execs `go run ./cmd/likha` with a temp
  `LIKHA_STATE_DIR`; the probe's end-to-end test goes here.

## Import rules

`internal/effortprobe` is new. It imports `model` and `model/catalog` only.
`app` (the composition root) resolves credentials and builds the client.
`agent`, `tui` and `providers` do not import it.

## Recording format

The coordinator records each user-run report in two places (spec points
26–30).

**`specs/model-effort/context.md`** gets a probe log under the support
matrix, one row per report:

| Date | Provider | Model | Shape | Likha | Report | Levels (verdict) | Control | Outcome |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `<date>` | `<provider>` | `<model>` | `reasoning_effort` | `<version>` | `<UTC time>-<provider>-<model>.json` | `none accepted, low accepted, …` | validated | passed |

(Template row only; no probe has run.) A report with an endpoint override
is logged with `(endpoint override)` after its file name and never has the
outcome `passed` unless the override is a documented alternative endpoint
of the same provider (spec point 27). The matrix's "Probe status" column
then reads `passed <date>`, `failed <date>`, or stays `pending-probe`.
Verdicts are written as the report's words (accepted, rejected,
ignored-suspected, inconclusive), never as symbols.

**`docs/providers.md` § Reasoning effort**: the "Effort status" column
reads `live-verified <date> (<model>)`, `failed <date>: <one-line reason>`,
or `pending-probe`. A `live-verified` value appears only together with the
enabling code change.

## Providers and expected outcomes

From [model-effort research](../model-effort/research.md); these are
expectations to check, not results.

| Provider | Shape(s) probed | Expected |
| --- | --- | --- |
| openai | `reasoning_effort` | accepted; unsupported levels 400; GPT-6 Astra and GPT-6.1 Sol reject tools on chat completions at every level (Default fails), GPT-6 Sol/Luna accept tools only at `none` |
| openrouter | `reasoning.effort` | accepted; control may be mapped, not rejected |
| gemini | `reasoning_effort` | accepted on Gemini 3 and 2.5 (mapped to budgets) |
| xai | `reasoning_effort` | accepted; unsupported values downgraded, so `same as` notes likely |
| deepseek | `reasoning_effort` | accepted; other values mapped |
| groq, cerebras, together, bedrock | `reasoning_effort` | unknown |
| mistral | `reasoning_effort` | inconclusive until chunk-list content parses |
| opencode-zen, opencode-go, dialagram | both | unknown; the control tells which shape is read |
| chatgpt | Responses `reasoning.effort` | unknown; catalog rows publish no levels, so `--probe-levels` is needed |
| claude (compat) | `reasoning_effort` | ignored-suspected (documented as ignored); `completion_tokens_details` is documented as always empty, so completion is the signal |
| claude (native, [claude-native](../claude-native/spec.md)) | `output_config.effort` (`EffortAnthropic`) | accepted; reasoning count probably not reported, so completion is the signal; requests carry `max_tokens` |

## Specs and docs

- [v1-spec.md](../v1-spec.md): FR-37 (model effort), FR-40 (this feature,
  to be amended), FR-36 (retries; the probe turns them off), FR-38 (native
  Claude client, [claude-native](../claude-native/spec.md); its effort wire
  waits for this probe), FR-39 (reasoning replay, out of scope here).
- [model-recovery](../model-recovery/tasks.md): T9 `WithoutRetry`, T1
  `StreamError`; the probe adopts both when they land.
- [chunked-content](../chunked-content/spec.md): Mistral's row stays
  inconclusive until chunk-list content parses.
- [model-effort](../model-effort/tasks.md): T20 is gated on this probe.
- `docs/providers.md`, `docs/usage.md`, `CHANGELOG.md`.
