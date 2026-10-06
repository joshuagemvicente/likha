# Checklist: Effort probe

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Starting and consent

- [x] `likha --probe-effort --provider NAME --model ID` runs without a
      repository path or terminal UI, and exits when done.
- [x] A missing provider or model (the stored first-run provider and model
      do not count), a missing credential, a model with no levels, a plan
      above 17 requests, or a bad flag combination exits 2 before any model
      request.
- [x] The plan, levels and their source, wire fields, request count, caps,
      estimated cost and any endpoint override are printed before the first
      model request.
- [x] Without `--yes`, nothing is sent until the user answers `y`; without
      `--yes` and without a terminal, the probe exits 2 and nothing is sent.

## Requests

- [x] Default is sent first and once, with no effort field. A Default
      failure other than 400/422 stops the run; a Default 400/422 does not.
- [x] Each published level is sent once per shape, in published order,
      followed by the `likha-probe-invalid` control.
- [x] Each request carries the probe task and its one tool, and the only
      difference between requests is the effort field.
- [ ] Documented providers are probed with their documented shape; relays
      with both shapes; ChatGPT with Responses `reasoning: {effort}`; Claude
      on its native route with `output_config: {effort}`.
- [ ] No row is retried, including by FR-36's automatic retries; 429, 5xx
      and timeouts are inconclusive; 401/403 stops the run.
- [x] A run never exceeds 17 requests, starts no request after 40,000
      completion tokens (a row without usage counts 3,000), and cancels any
      request after 180 s.

## Verdicts and output

- [x] Each row shows the exact effort fragment sent, HTTP status, cleaned
      error text, prompt/completion/reasoning tokens (or `not reported`),
      cost, latencies, tool result and verdict.
- [x] Verdicts follow spec points 12–18, and the heuristic disclaimer is
      printed with every run.
- [x] The summary states per shape whether it varied, was flat or was
      unmeasured, and whether the run supports enabling; it recommends one
      shape when both qualify and flags an endpoint override.
- [x] A JSON report with the same verdicts is written under
      `<state dir>/effort-probes/` with mode 0600, never overwrites an
      earlier report, and its path is printed.
- [ ] Ctrl+C after consent writes a partial report marked interrupted and
      exits 130; Ctrl+C at the prompt sends nothing and writes nothing.

## Safety

- [x] `config.json` and `providers.json` are unchanged after a probe, and an
      `--api-key` passed to the probe is not stored.
- [x] No API key, OAuth token or authorization header appears on stdout,
      stderr or in the report, even when a provider echoes the key.
- [ ] The probe enables no provider; every enabled effort wire cites a
      user-run `passed` report against the provider's own endpoint.

## Evidence

- [x] `go test ./...` passes with the probe's tests, all against local test
      servers; no test reaches a real provider.
- [ ] Every probe result recorded in
      [model-effort context.md](../model-effort/context.md#provider-support-matrix)
      and `docs/providers.md` names a report the user produced.
- [ ] Every level a report shows `rejected` is removed by a cited catalog
      override before its provider is enabled.

Open items as of 2026-10-06: Claude's native `output_config` shape (waits for
claude-native), FR-36's retry opt-out (model-recovery has not landed; no
retry exists today), a real Ctrl+C walkthrough, and every item that depends
on a user-run report.
