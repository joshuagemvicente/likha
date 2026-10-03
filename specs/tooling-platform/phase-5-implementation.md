# Phase 5 implementation evidence — 2026-10-04

**Status:** integrated; final checks and the live DuckDuckGo probe passed.
Phase 5 revises the web search tool from the Brave-only pin to a pluggable
provider model (the user's research direction: OpenCode-style `websearch`
with one configured provider) and starts the Option A release-verification
push.

## Baseline

- Phases 1–4 are committed (`6999539` chore, `4226992` specs, `f39f896` core,
  `5100342` TUI, `93bb2a2` cleanup, `75ac1f2` docs). Working tree clean except
  the pre-existing untracked `lisa` file.
- User-verified in the live TUI on 2026-10-04: plan mode and the `/agents`
  task tree/profile display. All other walkthrough scenarios remain tracked in
  the new `specs/tooling-platform/release-checklist.md`.

## Scope

1. **Pluggable search providers** — `tools.json` `search.backend` accepts
   `brave`, `tavily`, `exa`, or `duckduckgo` (keyless). One configured
   backend, no vendor fallback, per-provider keys (env wins over the 0600
   tool-keys file), per-provider honest consent copy, identical result
   normalization. Firecrawl/Parallel/TinyFish deliberately out of scope.
2. **Release verification push** — `specs/tooling-platform/release-checklist.md`
   enumerates every remaining manual, live, and failure-drill scenario with
   instructions and recording rules.

## Worker ownership

| Worker | Owned area | Status |
| --- | --- | --- |
| 1 | Revised `specs/web-tools/spec.md` — pluggable provider contract | delivered |
| 2 | `internal/webtools/provider.go` + config enum/key generalization | delivered |
| 3 | Tavily adapter (`tavily.go`) | delivered |
| 4 | Exa adapter (`exa.go`) | delivered |
| 5 | DuckDuckGo keyless adapter (`duckduckgo.go`) | delivered |
| 6 | TUI wiring, consent copy, tool description, backend key flow | delivered |
| 7 | Coordinator: evidence, live DuckDuckGo probe, checklist integration | delivered |

The coordinator also updated the two Brave-only rows in
`specs/tooling-platform/decisions.md` under the user's approval (backend
enum, per-provider disclosure, backend-switch consent note).

## Intended integrated contract

- One configured backend at a time; unknown or missing backend names are
  config errors; a keyed backend without a key is a visible issue entry and
  the tool stays unavailable.
- Every adapter returns the same bounded `SearchOutcome` (provider, query,
  ranked hits in backend order, elapsed, truncation, notes) with control
  sequences sanitized and snippets never invented.
- Consent copy is per provider and honest: Brave keeps the 90-day retention
  disclosure; Tavily/Exa disclose query/request-metadata flow with retention
  per their policies (no fabricated numbers); DuckDuckGo discloses the
  unofficial-endpoint fragility and that blocks/markup changes can happen at
  any time. Declines never issue a request; no vendor fallback ever.
- Wire behavior for every provider stays labeled unverified until its live
  probe is recorded in this file.

## Final checks

- `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race ./...`,
  gofmt, and the scoped whitespace check — all passed after integration.

## Live probe outcomes (2026-10-04)

- **DuckDuckGo (keyless, real endpoint):** two live searches through the real
  adapter returned correctly ranked results with real URLs (go.dev, GitHub,
  dev.to…), present snippets, unwrapped `/l/?uddg=` redirect wrappers (6/6 on
  the first query), backend/elapsed/truncation metadata set, and the
  fragility/unofficial-endpoint disclosure present in every outcome's Notes.
  Total wall time ~1.5 s for both queries. **DuckDuckGo is the first
  live-verified search backend**; its fragility disclosure remains accurate —
  a future block or markup change must surface as the named zero-results
  error, which the probe also validated structurally.
- Brave/Tavily/Exa wire behavior stays labeled unverified until the user
  supplies keys for live probes.

## Unverified acceptance scenarios

- Live Brave/Tavily/Exa probes (require API keys from the user).
- Per-backend consent copy rendering in the live dialog.
- Backend switch mid-life (config change at a run boundary).
- All standing release-checklist items (interactive TUI, hosted provider,
  fetch policy probes, failure drills, published binary).
