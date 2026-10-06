# Feature: Effort probe (live evidence for enabling reasoning effort)

**Status:** implemented (local) 2026-10-06, review fixes 2026-10-07 —
automated suite green; live probe outstanding (user-run, tasks T16), and
Claude's native `output_config` shape waits for claude-native. Decisions recorded 2026-10-06 (user delegated
defaults). Requirement: [v1-spec FR-40](../v1-spec.md).
Gates [model-effort](../model-effort/spec.md) task T20 and the effort wire of
[claude-native](../claude-native/spec.md) (FR-38, decision 8).

## Context

[Model effort](../model-effort/spec.md) (FR-37) is implemented, but dormant:
no provider carries an effort level until a live probe shows the provider
honors it ([provider support matrix](../model-effort/context.md#provider-support-matrix)).
Providers answer an effort field in three different ways. Some reject an
unsupported value with HTTP 400. Some map it to the nearest level without
saying so (OpenRouter, xAI). Some ignore the field completely (Anthropic's
OpenAI-compatible endpoint). Only a request to the real provider tells these
apart, and only the user holds the keys and pays for the requests.

Today there is no repeatable way to run that check. A probe written by hand
would not use Likha's real request code, could leak a key into a pasted log,
and would leave no record that the docs and code can cite. Sources are in
[research.md](research.md).

## Role

Give the user one command that spends a small, stated, capped amount of their
provider usage to answer one question per provider and model: does each
published effort level reach the model and change how it reasons? Use
Likha's real request code, so the answer holds for real turns. Report what
was observed, and say when a verdict is only a guess. Never change any
setting, and never enable a provider. Enabling stays a separate, reviewed
change that cites a report.

## Scope

In scope: a non-interactive `likha --probe-effort` mode for every named
API-key provider (chat completions), for ChatGPT sign-in (Responses), and
for Claude on whichever route the build uses (the OpenAI-compatible endpoint
today, the native Messages API once [claude-native](../claude-native/spec.md)
ships); the cost preview and confirmation; the stdout table; the JSON report
in the private state directory; and the steps that turn a report into a
recorded probe result.

Out of scope:

- Enabling effort for a provider. The probe prints whether a run supports
  enabling, and nothing else changes (decision 12).
- Custom endpoints (no `--provider`). Likha has no provider row and no
  catalog levels for them, so no report could enable anything there
  (decision 16).
- Multi-turn checks. Each request is a single user turn; no tool result is
  sent back. Replaying reasoning in tool loops belongs to
  [reasoning-replay](../reasoning-replay/spec.md) (FR-39).
- Any effort UI in the TUI. The probe has no slash command.
- Running probes in `go test`. Tests use local test servers only.
- Benchmarking answer quality. The probe measures whether effort reaches the
  model, not whether a level gives better answers.

## Decisions (user delegated defaults, 2026-10-06)

Each was an open choice; the recommended option was taken.

1. **A mode flag, `--probe-effort`.** Likha has no subcommands; standalone
   modes are flags (`--login`, `--sessions`). The probe follows `--login`:
   no TUI and no repository are needed.
2. **Real request code.** Requests go through the same client code as a real
   turn. The effort field is turned on for the probe's own client only; the
   provider table, `config.json` and `providers.json` are untouched.
3. **Wire shapes.** A provider whose field is documented in the support
   matrix is probed with that shape only. Relays and any provider marked
   "unknown" are probed with both `reasoning_effort` and
   `reasoning: {effort}`. `--probe-wire top|object|both` overrides this on
   chat-completions providers. ChatGPT has one shape, Responses
   `reasoning: {effort}`; Claude on its native route has one shape,
   `output_config: {effort}`. Reason: probe the documented shape cheaply;
   test both only where nobody documented one.
4. **Default first, sent once.** The first request carries no effort field.
   It is the baseline and is shared by both shapes, since it is identical for
   both. If Default fails for any reason other than HTTP 400 or 422, the run
   stops: nothing about effort can be learned when the model does not answer
   at all. A Default 400/422 does not stop the run, because the model's own
   default level can be the cause (OpenAI documents GPT-6 models that refuse
   tools on chat completions except at `none`, research.md); the levels are
   still sent, but no row can be `rejected` and the run is never eligible.
5. **One invalid control level per shape.** After the published levels, the
   probe sends the level `likha-probe-invalid`. A provider that rejects it is
   reading the field; one that accepts it is either ignoring the field or
   mapping unknown values. Reason: the cheapest strong signal for "ignored",
   and the only way to tell which of two shapes a relay actually parses.
6. **A fixed, small, tool-bearing task.** Every request carries one tool and
   asks the model to compute the sum of the primes below 60 and report it
   through that tool. Reason: tools are on every real Likha turn and change
   what some models accept (GPT-6 on chat completions, research.md); the
   task needs some reasoning, so levels can differ; and the expected answer
   is one short tool call. Known limit: an easy task may draw similar
   reasoning at every level, so a flat result alone never marks the field
   ignored (points 15–16).
7. **No extra output cap on the wire.** The probe sends the same request
   fields a real turn sends; only the messages and the tool differ. On chat
   completions and Responses that means no output-token cap; on Claude's
   native route it means the `max_tokens` every native request carries
   (claude-native decision 9). Reason: a smaller cap changes the request
   under test, is named differently per provider, and can cut reasoning off
   before any output, which would look like a failure (research.md).
8. **Hard caps instead.**
   - At most **17 requests** per run: 1 Default + 2 shapes × (7 levels +
     1 control). Any larger plan is refused.
   - At most **40,000 completion tokens** per run. Once reached, no new
     request starts; the remaining rows are recorded as skipped. The request
     in flight can exceed the budget. A request that may have generated
     output without reporting usage (an answer without usage, or one cut
     off by a timeout, a dropped stream or a lost connection) counts as the
     cost estimate's 3,000 tokens, and the total is marked as partly
     estimated. A request the provider answered with an HTTP error status
     (400, 401, 429, 5xx…) generated nothing and counts 0.
   - At most **180 seconds** per request. A request that runs longer is
     cancelled and recorded as inconclusive.
9. **Spend needs consent.** Before the first model request the probe prints
   the plan, the caps and the estimated cost. It sends nothing until the
   user passes `--yes` or answers `y` at an interactive prompt. Listing
   models (an unbilled `GET /models`) may happen before consent, to learn
   the levels, but only from the provider's own endpoint: an endpoint
   override (decision 16), which may come from an ambient
   `LIKHA_ENDPOINT`, receives nothing, and so no credential, before
   consent.
10. **No retries.** Each planned request is sent once. A rate limit, server
    error, timeout or dropped stream makes that row inconclusive. A 401 or
    403 stops the run. The automatic retries of FR-36 are turned off for the
    probe's requests. Reason: a retried row would hide flaky behavior the
    report should show, and would spend more than the plan stated.
    One exception keeps the probe on real request code (decision 2): Likha's
    client re-sends a request once, exactly as on a real turn, after a
    rejection that generated nothing, namely a provider refusing the
    optional `stream_options` field, or a ChatGPT token refreshed after
    HTTP 401. Such a row makes two HTTP attempts, spends nothing extra, and
    records both (point 11); the plan's caps line says so. The 17-request
    cap counts planned requests.
11. **The verdict heuristic is stated, not hidden.** See points 12–19. The
    report and the table both say that `ignored-suspected` is a heuristic
    from one sample per level.
12. **The probe never enables anything.** It prints whether the run supports
    enabling ("eligible"). Recording the result and enabling the provider are
    done afterwards in reviewed changes (points 26–30).
13. **`--probe-levels` may name unpublished levels.** They are probed and
    labelled `unpublished`, and they never count toward eligibility. Reason:
    the ChatGPT catalog rows publish no levels, and a probe is the evidence a
    catalog override needs. The path for such a model is two runs: a report
    with unpublished levels justifies a catalog override, and a second run
    on the published levels decides eligibility.
14. **Keys stay out.** An `--api-key` passed to the probe is never stored,
    even though a normal run stores it. The report and the table never
    contain an API key, an OAuth token or an authorization header, and
    provider error text is cleaned of them before it is printed or saved.
15. **ChatGPT token refresh may still save.** A probe with ChatGPT sign-in
    refreshes its token like a normal run. If the provider rotates the
    refresh token, the new one is saved. Reason: discarding a rotated token
    would sign the user out. This is the only stored state the probe can
    change, besides writing its report.
16. **A named provider is required, and an endpoint override is labelled.**
    The probe refuses to run without `--provider` (or `LIKHA_PROVIDER`).
    `--endpoint` (or `LIKHA_ENDPOINT`) may still point a named provider
    elsewhere, for example another Bedrock region or a local test server,
    but the report then says `endpoint override`, and the summary says the
    run is not evidence for the provider's own endpoint. Reason: effort is
    enabled per provider row, so evidence must come from that row's
    endpoint or a documented alternative of it.

## Functional changes

### Starting a probe

1. `likha --probe-effort --provider NAME --model ID [--endpoint URL]
   [--api-key KEY] [--probe-wire top|object|both] [--probe-levels a,b,c]
   [--yes]` runs a probe and exits. No repository path or terminal UI is
   needed. The provider comes from `--provider` or `LIKHA_PROVIDER`, and the
   model from `--model` or `LIKHA_MODEL`. The provider and model stored by
   first-run setup are never used, and a provider's default model is never
   probed by accident: a missing provider or model is a usage error.
2. Credentials resolve as in a normal run: `--api-key` or `LIKHA_API_KEY`,
   then the provider's own environment variable, then the stored key; or
   the stored ChatGPT sign-in. A missing credential is an error before
   anything is sent.
3. These are usage errors (exit 2): `--probe-effort` with `--sessions`,
   `--resume`, `--login` or a repository path; `--probe-wire`,
   `--probe-levels` or `--yes` without `--probe-effort`; `--probe-wire` on
   ChatGPT or on Claude's native route, which have one shape.
4. The levels come from the same place as in `/models`: the provider's live
   model list when it publishes them, else the bundled catalog, matched on
   the exact provider and model. With an endpoint override the live list is
   not fetched before consent (decision 9), so the catalog supplies them. `--probe-levels` replaces the list; its
   names follow the catalog's level rules (lower case, no spaces, no
   repeats, not `default`) and may not be `likha-probe-invalid`. A model
   with no levels and no `--probe-levels` exits 2 with
   `<model> publishes no effort levels on <provider>; nothing to probe (use
   --probe-levels to test candidate levels)`, before any model request.
5. On `claude` while it uses the OpenAI-compatible endpoint, the probe
   prints first: `Anthropic's OpenAI-compatible endpoint ignores effort;
   expect ignored-suspected.` Once Claude uses the native route, the probe
   sends `output_config: {effort}` there instead.

### Plan, cost and consent

6. Before any model request the probe prints:
   - provider, model, endpoint host (with `endpoint override` when it is not
     the provider's default), and each wire shape with its exact field (for
     example `reasoning_effort` or `reasoning.effort`);
   - the levels and where they came from (live list, catalog, or
     `--probe-levels`), with unpublished levels marked;
   - the number of requests (Default, levels, controls);
   - the estimated cost, as `about $0.31 (assumes up to 3,000 output tokens
     per request at $10.00/M; input about 350 tokens per request)`. When the
     catalog has no price: `cost unknown: no catalog price for <model> on
     <provider>`. On ChatGPT: `uses your ChatGPT plan allowance; no
     per-token charge`;
   - the caps of decision 8, and the line `A single request's output is not
     capped by the probe; the budget stops new requests only.`
7. With `--yes`, the probe starts. Without it, on an interactive terminal it
   asks on stderr `Send N requests to <provider> (<model>)? [y/N]` and starts
   only on `y` or `yes`. Anything else prints `Probe cancelled; no model
   request was sent.` and exits 1. Without `--yes` and without a terminal it
   exits 2 with `pass --yes to confirm spending`. Ctrl+C at the prompt sends
   nothing, writes no report and exits 130.

### Running

8. Requests are sent one at a time, in this order: Default; then for each
   shape, each level in published order (unpublished levels last), then the
   control level.
9. Each request is streamed and contains the probe task and its one tool
   (decision 6). The only difference between requests is the effort field.
10. Each row is printed as soon as its request ends, so the user sees
    progress. Ctrl+C after consent cancels the request in flight, writes the
    report with what was observed, marks it `interrupted`, and exits 130.

### What each row records

11. For each request: the level and shape; the exact effort fragment that was
    sent, as seen in the outgoing body (for example
    `"reasoning_effort":"high"`, or `none` at Default); the HTTP status, the
    number of attempts, and the first attempt's status when decision 10's
    re-send happened; the
    error text, cleaned of credentials and cut to 1 KiB; prompt, completion
    and reasoning tokens as the provider reported them (each marked
    `not reported` when the usage omits it); the reported or estimated cost;
    the time to the response headers, to the first streamed token, and to
    the end; whether the tool was called and whether its answer was 440; and
    the verdict with a one-line reason.
12. Points 13–19 define the verdicts. Each shape has one **signal**: its
    reasoning token count when every successful published-level row of that
    shape reported one, else its completion token count. The table and
    report name which one was used. A successful row with neither count has
    no signal.

### Verdicts

13. **inconclusive** — the row cannot be judged: a timeout, connection error,
    dropped or malformed stream, HTTP 401, 403, 408, 429, 5xx or any other
    HTTP error not covered by point 14, a request skipped by a cap or by an
    earlier stop, or a 400/422 on a level or on the control when Default
    did not succeed (decision 4): the model's own default may be what the
    provider refused.
14. **rejected** — the provider answered HTTP 400 or 422 to a request that
    carried an effort level, while Default succeeded.
15. A shape is **varied**, **flat** or **unmeasured**. It is unmeasured when
    fewer than two published levels succeeded with a signal. Otherwise it is
    flat when the highest signal among those levels is at most 10% above the
    lowest, or within 32 tokens of it, and varied when not.
16. A published or unpublished level row that succeeded gets:
    - **ignored-suspected** (heuristic) when its shape is flat and the
      control succeeded;
    - **inconclusive** when its shape is flat and the control was
      inconclusive (`flat; control inconclusive`);
    - **accepted** otherwise. Accepted rows carry informational notes:
      `field validated, no measurable difference` (flat or unmeasured, control
      rejected), `invalid level also accepted (provider maps or ignores
      unknown values)` (control succeeded), and `succeeded where Default
      failed` (decision 4).
17. A published level whose signal is within 5% of the level before it (or
    both zero) gets the note `same as <level> (possible downgrade)`. Default
    is never compared to a level, because a model's default level and
    Default are expected to match (research.md).
18. The Default row's verdict is `baseline` when it succeeded, `failed` on
    400/422, or inconclusive as in point 13. The control row's verdict is
    `validated` (HTTP 400 or 422 while Default succeeded), `unvalidated`
    (success), or inconclusive as in point 13.
19. The table and report state: `Verdicts come from one request per level;
    ignored-suspected is a heuristic, not proof. Re-run before relying on a
    borderline result.`

### Summary and eligibility

20. After the rows, the probe prints per shape: totals (requests, tokens,
    cost, with `partly estimated` when decision 8 applied), the
    varied/flat/unmeasured finding, the control result, and an eligibility
    line:
    - `Eligible to enable <shape> for <provider>: yes` when Default is
      `baseline`, every published level is `accepted`, and the control was
      validated or the shape is varied;
    - otherwise `Eligible …: no — <first reason>` (for example
      `xhigh rejected; a catalog override must remove it first`).
    Notes never block eligibility. When both shapes are eligible it
    recommends the documented shape, else the one whose control was
    validated, else `reasoning_effort`. With an endpoint override the line
    adds `(endpoint override: not evidence for <provider>)`.
21. Eligibility is advice. The probe changes no setting (decision 12).

### Output and report

22. The table goes to stdout, readable without color. The consent prompt,
    progress notes and errors go to stderr.
23. The JSON report is written to
    `<state dir>/effort-probes/<UTC time>-<provider>-<model>.json`, with the
    directory created mode 0700 and the file mode 0600. The model part of the
    name keeps only letters, digits, `.`, `_` and `-`; other characters
    become `-`. An existing report is never overwritten; a numeric suffix is
    added instead. The path is printed last. A report is written for every
    run that sent at least one model request, including runs stopped by
    point 4's Default failure or a 401/403. A report that cannot be written
    is an error (exit 1), and the table has already been printed.
24. The report holds: its format version, Likha's version, start and end
    times, provider, model, endpoint scheme and host (never credentials,
    path, query or fragment), whether the endpoint was overridden, shapes,
    levels with their source, the caps, the probe task's version, every row
    of point 11, the summary of point 20, why the run stopped early if it
    did, and whether it was interrupted. It never contains an API key, OAuth
    token or authorization header (decision 14).
25. The probe never prints or saves a request body beyond the effort
    fragment of point 11.

### Recording a result

26. The user runs the probe and shares the report (the JSON file or the
    printed table). Agents never claim a provider is verified on their own.
27. The coordinator records each report in
    [model-effort context.md](../model-effort/context.md#provider-support-matrix)
    and in `docs/providers.md`: provider, model, shape, date, Likha version,
    per-level verdicts, and the outcome `passed`, `failed` or
    `inconclusive`. A run is `passed` only when it printed an eligible shape
    without an endpoint override (or with a documented alternative endpoint
    of the same provider, such as another Bedrock region). The report file
    name is cited; the file itself stays in the user's state directory.
28. A provider's effort wire is enabled only in a separate, reviewed change
    that cites a `passed` report for that provider: an eligible shape on at
    least one reasoning model with tools present. For `openai`, a GPT-6
    model must also have been probed, and every level it rejected with
    tools must first be removed from the `openai` catalog rows by a
    hand-checked override, so the picker never offers a level that fails on
    a real turn (model-effort T20). Relays need a passed report per
    upstream family they route to, or stay disabled.
29. A level that a report shows `rejected` on a model is removed from that
    model's offered levels by a catalog override that cites the report,
    before the provider is enabled.
30. The `CHANGELOG.md` entry for an enabling change says `user-run live
    probe` and names the report, the model and the date.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | The probe ran to the end and the report was written, whatever the verdicts. |
| 1 | Declined at the prompt, Default failed with anything but 400/422, a 401/403 stopped the run, or the report could not be written. |
| 2 | Usage error (including a missing provider or model), unknown provider, missing credential, a plan above 17 requests, or no levels to probe. Nothing was sent. |
| 130 | Interrupted with Ctrl+C. A partial report was written if any model request had been sent. |

## Acceptance criteria

- Against a local test server that changes reasoning tokens by level,
  `likha --probe-effort --provider openai --endpoint http://127.0.0.1:… --model
  gpt-5.5 --yes` sends 1 + 5 + 1 = 7 requests: one with no effort field, one
  per published level carrying `"reasoning_effort":"<level>"`, and one with
  `likha-probe-invalid`. Every level is `accepted`, the control is
  `validated`, the summary says eligible, and it adds the endpoint-override
  remark.
- A server that returns 400 for `xhigh` only gives `rejected` for `xhigh`
  and `Eligible …: no`.
- A server that reports the same reasoning tokens at every level and accepts
  the control gives `ignored-suspected` on every level row, and the
  heuristic disclaimer is printed.
- A server that reports the same reasoning tokens at every level and rejects
  the control gives `accepted` rows with `field validated, no measurable
  difference`, and the summary says eligible.
- A server that reports no usage at all and accepts the control gives an
  `unmeasured` shape and `Eligible …: no`.
- A server that answers 400 at Default and succeeds at `none` keeps going:
  the `none` row is `accepted` with `succeeded where Default failed`, and
  the summary says not eligible.
- A relay provider with `--probe-wire both` and 3 levels sends 1 + 2 × 4 = 9
  requests, and Default is sent once.
- Without `--yes` and without a terminal, the probe exits 2 and the test
  server receives no `POST`.
- Without `--provider` and `LIKHA_PROVIDER`, or without `--model` and
  `LIKHA_MODEL`, the probe exits 2 even when `config.json` names a provider
  and model.
- A server that answers 429 gives an inconclusive row and exactly one
  request for that row; 401 stops the run with exit 1 and a report.
- After any probe, `config.json` and `providers.json` are byte-identical to
  before, the provider table in memory is unchanged, and an `--api-key`
  passed to the probe is not stored.
- The API key used in a test never appears in stdout, stderr or the report,
  even when the server echoes it in an error body.
- The report exists at the stated path with mode 0600, a second run in the
  same second gets a new file, and the report's rows and summary hold the
  same verdicts as the printed table.
- A plan above 17 requests is refused before anything is sent, and a
  completion-token budget of 40,000 stops new requests and records the rest
  as skipped; a row that answered without usage counts 3,000 tokens toward
  it, and a row answered with an HTTP error status counts 0.
- On ChatGPT, requests go to the Responses wire with `reasoning: {effort}`,
  and reasoning tokens come from `output_tokens_details`.
