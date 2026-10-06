# Tasks: Effort probe

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md). Each milestone leaves `go test ./...`
and `go vet ./...` green and can land on its own. No test reaches the
network: every request goes to an `httptest` server. Probes against real
providers are run by the user (M5), never by an agent or by `go test`.

Before M1: the coordinator amends FR-40 into `v1-spec.md`.

## M1 — Seams in `internal/model`

- [ ] **T1. Reported-reasoning flag.** Add `reasoningSeen bool` to
      `usageDetail` and `ReasoningSeen bool` to `RequestUsage`, set when the
      usage object names a valid reasoning count
      (`completion_tokens_details.reasoning_tokens` or
      `output_tokens_details.reasoning_tokens`). Today a missing count and a
      reported zero are both `Reasoning: 0`; the probe must tell them apart
      (spec points 11–12).
      *Verify:* `usage_report` test cases: count present, count zero,
      field absent, on both the chat-completions and the Codex usage parser.
- [ ] **T2. Observed fork.** Add `(*Client).ForkWithTransport(wrap
      func(http.RoundTripper) http.RoundTripper) (*Client, error)` in
      `fork.go`, next to `ForkForTask`, which already wraps the transport.
      The probe uses it to see each request's status code, header latency
      and outgoing body. Document that the wrapper must not read or record
      request headers, and must restore the body it reads (the request is
      built from a `bytes.Reader`, so `GetBody` is set).
      *Verify:* fork test: the wrapper sees one round trip per `Stream`,
      plus one more for the `stream_options` fallback and for Codex's
      post-401 retry; the parent client's transport is unchanged; OAuth
      refresh requests (sent through the OAuth session's own HTTP client)
      do not pass through it.
- [ ] **T3. No other change to model-effort code.** The probe forces the
      wire with `SetEffort` on its own fork, and resolves levels by calling
      `EffortLevels` with a local copy of the provider whose `Effort` is set
      to the shape under test. `model.Providers` is never written. Claude's
      native shape uses claude-native's `EffortAnthropic` wire kind once
      that lands; the probe adds none of its own.
      *Verify:* T9's "provider table unchanged" assertion.

## M2 — Probe engine (`internal/effortprobe`, new package)

Imports only `model` and `model/catalog`. No `providers`, `tui` or
`bubbletea`: `app` resolves credentials and hands over a ready client.

- [ ] **T4. Plan.** `type Plan struct` with provider row, model ID, shapes,
      levels (each with `Published bool`), level source, endpoint override
      flag and caps. `NewPlan(p model.Provider, modelID string, live
      []string, opts Options) (Plan, error)` applies spec points 3–5 and
      decisions 3, 8, 13 and 16. `--probe-levels` names pass
      `catalog.ValidateEfforts` and may not be `likha-probe-invalid`. The
      documented shape per provider is a small table in this package,
      copied from the
      [support matrix](../model-effort/context.md#provider-support-matrix):
      `openai, gemini, xai, deepseek, groq, cerebras, together, mistral,
      bedrock` → top level; `openrouter` → object; `chatgpt` → Responses
      object; `opencode-zen, opencode-go, dialagram` → both; `claude` → top
      level on the compatibility endpoint (expected ignored), or
      `EffortAnthropic` on the native route. An empty provider name is an
      error (custom endpoints are out of scope). `Requests()` returns the
      ordered request list (spec point 8); more than 17 is an error.
      *Verify:* table test over documented, relay, `--probe-wire` override,
      `--probe-wire` on `chatgpt` (error), `--probe-levels` with unpublished
      and invalid names, no levels, no provider, an endpoint override, and a
      plan that exceeds 17.
- [ ] **T5. Probe task.** A versioned constant (`probeTaskVersion = 1`):
      a system message, the user prompt (sum of primes below 60, report it
      with the tool), and one tool `record_answer` with an integer
      `answer` parameter. `EstimateInputTokens` gives the cost estimate's
      input size.
      *Verify:* the tool definition passes `Client.stream`'s validation and
      `BuildCodexRequest`; the estimate is positive.
- [ ] **T6. Cost estimate.** `Estimate(plan) CostEstimate` uses
      `model.ModelPrice` and `model.IsSubscription` with the assumed 3,000
      output tokens per request. It returns one of: dollars, unknown price,
      or subscription. The text follows spec point 6.
      *Verify:* priced, unpriced and `chatgpt` cases.
- [ ] **T7. Runner.** `Run(ctx, client *model.Client, plan Plan, out
      Reporter) (Report, error)`:
      - forks the client per request with `ForkWithTransport`, wrapping a
        recorder that keeps the last status, time to headers and the effort
        fragment read from the outgoing JSON body (never headers);
      - calls `SetEffort(shape, level)` on the fork (nothing at Default);
      - calls `StreamUsage` under a 180 s per-request context; text and
        reasoning callbacks record time to first token. Once model-recovery
        (FR-36) lands, the context also carries `model.WithoutRetry`, so
        `Client.stream` makes one attempt;
      - classifies the error: `errors.As` for `*model.EffortRejectedError`,
        `errors.Is` for `model.ErrUnauthorized`, context deadline, and the
        recorder's status for every other HTTP failure; once model-recovery
        lands, its `StreamError` kind replaces the recorder status where
        both exist. No string matching;
      - stops on a Default failure other than 400/422 (exit 1) and on
        401/403 (exit 1); a Default 400/422 continues (spec decision 4);
      - before each request, stops once completion tokens reach 40,000,
        counting 3,000 for a row with no reported usage; the remaining rows
        are inconclusive with reason `skipped: <cause>`;
      - never retries a row (decision 10).
      *Verify:* httptest servers per case in T9.
- [ ] **T8. Verdicts.** A pure function over the finished rows implements
      spec points 12–20: per-shape signal choice, varied/flat/unmeasured
      (≤10% or ≤32 tokens), control result, `same as` notes, Default
      verdict, per-shape eligibility and the recommended shape.
      *Verify:* table test with synthetic rows: varied with validated
      control, varied with unvalidated control, flat with validated control
      (eligible), flat with unvalidated control (ignored-suspected), flat
      with inconclusive control, mixed reported/unreported reasoning
      (completion fallback for the whole shape), no usage (unmeasured), one
      rejected level, one inconclusive level, Default 400 with a later
      success, both shapes eligible.
- [ ] **T9. Engine tests with fake providers.** In
      `internal/effortprobe/probe_test.go`, an `httptest` chat-completions
      server parameterised by behavior: *honors* (reasoning tokens scale
      with level), *rejects* a named level with 400, *ignores* (constant
      tokens, accepts the control), *maps* (accepts the control, tokens
      vary), *validates-flat* (constant tokens, rejects the control),
      *no-usage*, *default-400* (400 without an effort field), *rate-limits*
      (429), *unauthorized* (401), *slow* (exceeds a test-shortened
      timeout), *echoes the key* in an error body. Assert the request count
      and order, the exact effort fragment per request, the verdicts, that
      `model.Providers` is unchanged, and that the key bytes are absent from
      the report and the table. A Responses (`chatgpt`) case uses a
      transport that redirects to the test server and checks
      `reasoning: {effort}` and `output_tokens_details` parsing.
      *Verify:* `go test ./internal/effortprobe/ -race`.

## M3 — Output

- [ ] **T10. Table.** Plain-text table on stdout, one row per request as it
      ends, then the per-shape summary and the heuristic disclaimer
      (spec point 19). No color, no Nerd Font glyphs.
      *Verify:* golden test at a fixed clock with the T9 *honors* and
      *ignores* runs.
- [ ] **T11. JSON report.** `report.go` with `formatVersion = 1`, fields of
      spec point 24, `json:"snake_case"` tags. Error text passes through
      `redact(text, secrets...)`, which replaces each secret (API key, OAuth
      access and refresh tokens) with `[redacted]`, then truncates to 1 KiB.
      The endpoint is reduced to scheme and host. Writes go to a temp file
      in `<state>/effort-probes/` (0700), `Chmod(0600)`, then `os.Link` to
      the final name (failing on an existing file, then trying `-2`, `-3`,
      …) and remove the temp file, so no report is overwritten; every error
      is wrapped and returned.
      *Verify:* round-trip test; mode check; redaction test with the key
      inside a JSON error body, a URL and a quoted string; the file name
      sanitizer with an OpenRouter-style `vendor/model` ID; two reports in
      the same second.

## M4 — CLI wiring (`internal/app`)

- [ ] **T12. Flags.** In `run.go` add `--probe-effort`, `--probe-wire`,
      `--probe-levels`, `--yes`, and their help text in `usageFooter`. Branch
      to `effortProbeFlow` right after the `--login` branch, before the
      repository is resolved. Enforce spec points 1 and 3, including a
      positional repository path.
      *Verify:* `run_test.go` cases for each usage error, each exiting 2,
      including a stored `config.json` provider and model with no
      `--provider`/`--model`.
- [ ] **T13. `effortProbeFlow`** in a new `internal/app/effort_probe.go`:
      - resolve the state directory; take the provider and model from the
        flags (which carry `LIKHA_PROVIDER`/`LIKHA_MODEL`) only, never from
        `providers.LoadStoredConfig`;
      - resolve credentials with `providers.ResolveProvider(...,
        persistKey=false, ...)`;
      - build the client as `Run` does (`model.New`, or `model.NewOAuth`
        plus `bindOAuthStorage` and `discoverOAuthModel` for `chatgpt`);
        when the row has a `SessionHeader` (the OpenCode relays), set it
        with a random per-run session ID;
      - fetch live levels with `ListModelsWithDetails` under a short
        timeout (failure falls back to the catalog with a stderr note);
      - build the plan, print it and the estimate, then confirm (`--yes`,
        or a `[y/N]` prompt on stderr when stdin and stdout are terminals);
      - run under a context cancelled by SIGINT; write the report whenever a
        model request was sent; map the outcome to spec's exit codes.
      *Verify:* `effort_probe_test.go` with an httptest server via
      `--endpoint`: `config.json` and `providers.json` bytes unchanged,
      `--api-key` not stored, declined prompt sends no `POST`, a 401 run
      still writes a report.
- [ ] **T14. Integration test.** In `tests/integration/cli_test.go`: exec
      `go run ./cmd/likha --probe-effort --provider openai --endpoint
      <httptest> --model gpt-5.5 --api-key test-key-… --yes` with a temp
      `LIKHA_STATE_DIR`; assert exit 0, a report under `effort-probes/` with
      mode 0600, no key in output or report, and 7 `POST`s. A second run
      without `--yes` asserts exit 2 and zero `POST`s.
      *Verify:* `go test ./tests/integration/ -run EffortProbe`.

## M5 — Docs, then user-run probes

- [ ] **T15. Docs.** `docs/providers.md` § Reasoning effort: how to run the
      probe, what it costs, what the verdicts mean, and that it changes no
      setting. `docs/usage.md`: the flags. `--help` text matches.
      `CHANGELOG.md` **Unreleased**: the probe mode, verified by automated
      checks only. The coordinator also updates model-effort T20 to say
      probes run with `likha --probe-effort`, and adds the probe-log table of
      [context.md § Recording format](context.md#recording-format) to
      model-effort context.md.
      *Verify:* `go run ./cmd/likha --help` lists the four flags with the
      same wording as `docs/usage.md`; the CHANGELOG entry names no provider
      as verified.
- [ ] **T16. Record user-run reports (coordinator, per provider).** When the
      user shares a report:
      1. add a row to the
         [probe log](../model-effort/context.md#provider-support-matrix):
         provider, model, shape, date, Likha version, report file name,
         per-level verdicts, outcome (`passed` / `failed` / `inconclusive`,
         spec point 27);
      2. update the provider's row in `docs/providers.md` § Reasoning effort
         from `pending-probe` to `live-verified <date> (<model>)` together
         with the enabling change, or to `failed <date>: <reason>`;
      3. for every `rejected` level, open a catalog override that removes it
         from that model and cites the report (spec point 29);
      4. only for `passed`, open a separate change that sets
         `Provider.Effort` to the eligible shape, citing the report
         (model-effort T20, spec point 28), with a `CHANGELOG.md` entry
         saying `user-run live probe`;
      5. never mark a row from an agent-run or simulated probe.
      *Verify:* each recorded row names a report file the user produced;
      each enabling change cites a `passed` report without an endpoint
      override.
