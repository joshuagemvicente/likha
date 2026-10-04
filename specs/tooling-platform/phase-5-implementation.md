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

## Final implementation + verification push (2026-10-04)

### Slices implemented (eight-worker pattern, disjoint ownership)

| Worker | Slice | Files | Commit |
| --- | --- | --- | --- |
| A | `/sessions` Ctrl+D delete, two-step confirmation naming losses, blocked while a run/review is active | `internal/tui/dialog.go`, `internal/tui/session_delete.go` | `25ddef4` |
| B | `(*session.Store).Delete` (owner/root checks, FK cascades, `ErrSessionNotFound` / `ErrSessionOtherRepository`) and `tooloutput.RemoveSession` (containment + symlink checks, missing dir = success) | `internal/session/delete.go`, `internal/tooloutput/remove.go` | `25ddef4` |
| C | Per-node wait vs active time (`Record.WaitMs/ActiveMs`, accumulated at permit acquire/release; scheduling unchanged) | `internal/explore/{node,records,types}.go` | `8f18ae5` |
| D | `/agents` rows/detail show `wait Xs · active Ys`; completion tokens render unknown, never a fabricated 0 | `internal/tui/{agents_view,agent_detail}.go` | `8f18ae5` |
| E | `Client.LastRequestUsage()` with prompt/completion seen flags; old parsers kept as thin wrappers | `internal/model/{client,codex,usage_report}.go` | `8f18ae5` |
| F | Explore nodes consume the richer report (`RecordRequestUsage`); prompt-only responses mark completion unknown; limitation comment removed | `internal/explore/node.go` | `8f18ae5` |
| G | Phase 5 docs (CHANGELOG, README web paragraph, user guide web chapter) | docs | docs commit |
| Coordinator | `sessionDeleteRequestedMsg` handling in `tui.go` (a row delete that leaves output behind still completes and reports the leftover); `explore_loop.go`/`profile_runner.go` call `RecordRequestUsage`; probe-driven fixes below | `internal/tui/tui.go`, `internal/agent/*` | `25ddef4`, `8f18ae5` |

### Probe-driven fixes

- **Exa adapter never sent its key** — header was `x-api-1`; Exa answered
  402 even for valid keys. Fixed to `x-api-key` (`96ef0b9`).
- **Brave invalid key surfaced as availability error** — Brave answers a bad
  key with HTTP 422 `SUBSCRIPTION_TOKEN_INVALID`; now mapped to the
  credential-rejected error (bounded 4 KiB body check, body never echoed)
  (`96ef0b9`).

- **Web grants survived config changes** (found by the adversarial docs
  re-audit) — `decisions.md` requires grants to clear on backend
  configuration changes, but only session switch cleared them. `webHooks`
  now compares the loaded `tools.json` state at each run start and clears
  all grants on any change (`62e33d3`). Checks re-run green afterwards;
  interactive confirmation pending (§1 consent dialogs).
- **Known, documented rather than changed:** with a keyed backend and no
  key, `/tools` shows the generic "backend is not configured" reason; the
  specific missing-key message appears as a transcript entry at run start.

### Checks after integration (2026-10-04)

- `go build ./...` — pass
- `go vet ./...` — pass
- `go test ./...` — pass (all packages ok)
- `go test -race ./...` — pass (exit 0)
- `gofmt -l cmd internal tests` — clean
- `git diff --check` — clean
- Re-run after `62e33d3`: build, vet, test, `test -race`, gofmt, diff check — all pass.
- No new tests were added; no existing test was modified.

### Live / headless probe outcomes (2026-10-04)

- **web_fetch (LIVE, real `webtools.Fetch`)** — PASS. `https://go.dev/doc/`
  → markdown, 13 315 bytes, not truncated, 399 ms; `https://pkg.go.dev/net/http`
  → 163 455 bytes, 717 ms. Refusals with policy reason and no connection:
  `http://example.com` and `http://localhost:<port>` ("only the https scheme
  is permitted"; local listener recorded 0 accepts), `https://127.0.0.1`,
  `https://[::1]`, `https://10.0.0.1`, `https://192.168.1.1`,
  `https://169.254.169.254` ("only global public unicast addresses are
  reachable", refused before consent), `https://example.com:8443` ("only the
  default port 443 is permitted"). `https://localhost` and the rebinding name
  `localtest.me` are rejected by the dialer Control hook before connect(2)
  (proved from the error path; binding :443 for a listener was not permitted).
- **Search bad-key paths (LIVE)** — after the fixes: Brave → "HTTP 422
  SUBSCRIPTION_TOKEN_INVALID; … credential was rejected"; Exa → "HTTP 401;
  the provided Exa API key was rejected"; Tavily → "HTTP 401 unauthorized;
  the configured API key was rejected". Happy paths **NOT RUN** — no
  `BRAVE_SEARCH_API_KEY` / `TAVILY_API_KEY` / `EXA_API_KEY` configured.
- **MCP refusals (headless, real stdio fake server, real registry/turn loop)**
  — PASS. Unsafe schemas (`$dynamicRef`, remote `$ref`, nonportable regex)
  stay unavailable with named reasons, are never advertised, and receive no
  `tools/call`. Plan mode refuses a trusted server's tool with
  `source={kind:mcp server:probesrv tool:safe_echo}` and zero `tools/call`.
  Observations (not failures): readable text carries identity only inside
  the truncated qualified name; a legacy alias in plan mode resolves to
  `unknown_tool` with empty source; refusal text appears twice in content.
- **Failure drills (headless approximations, real apply/journal/resume code)**
  — PASS. kill -9 mid 64-op edit batch: recovery inspection matched journal
  and disk exactly, nothing replayed (journal and disk fingerprints
  unchanged across relaunches). **Gap:** after a crash the stored status
  stays `publishing`, and `Unresolved` is only populated for `interrupted`,
  so the recovery report's "Unresolved:" line is empty even when files are
  published-but-unrestored or one original sits in a backup path; the
  applied/retained/boundary lines still carry the information. Not changed
  (journal semantics; needs approval). kill -9 during a depth-2 explore run:
  `ResumeTasks` marked both records interrupted, kept partial findings,
  repaired the parent sentinel, no replay. Model-request limit: exactly 32
  requests, `status=limited`, partial findings retained.
- **Publication (headless approximation)** — `scripts/release.sh` archive,
  `scripts/smoke-release.sh`, `curl | sh` install over loopback via
  `LIKHA_RELEASE_BASE`/`LIKHA_RELEASE_API`, checksum/tamper/version/insecure-base
  guards, and installed-binary smoke — all PASS. **FAIL:** the public
  one-liner, installer defaults, and `internal/update` endpoint all name
  `github.com/gem/likha` (404); the git remote is
  `joshuagemvicente/likha`, which has no releases yet. Pending user decision.
  Minor: `LIKHA_RELEASE_API` has no HTTPS check (tag is still regex-validated
  and downloads still require an HTTPS base); `--help` omits `--theme` and
  `--nerd-fonts`.
- **Claims audit** — findings (DeepSeek/Gemini shipped but documented as
  deferred, `claude` default-model mismatch, unlabeled interactive claims,
  CHANGELOG verification contradiction) were corrected in the docs commit and
  re-audited adversarially.

### Still unverified

- Every interactive TUI walkthrough (release-checklist §1), including the new
  session-delete dialog, wait/active display, and completion-unknown usage.
- Hosted-provider structured-tool probe (release gate) — user-run.
- Brave/Tavily/Exa happy paths — need keys.
- Published-binary probe — no published release exists yet.

## Follow-up fixes (2026-10-04, user-approved)

- **Streamed tool calls without `index`.** The user hit "invalid indexed
  function tool-call fragment" twice on Dialagram `nexum-router` (session
  history: the same session's other parallel-tool rounds succeeded, consistent
  with the router choosing upstreams per request). The parser now accepts
  index-less fragments (a new call id opens the next call; otherwise it
  continues the latest call), stops doubling ids/names repeated on every
  chunk, still rejects fragments with neither index nor id and non-`function`
  types, and names the broken rule in the error. The existing "missing
  structured index" test input was changed (with user approval) to the
  still-rejected no-index/no-id shape. A temporary harness (deleted) checked
  Gemini-style whole calls, repeated ids, continuations, mixed indexed and
  index-less, repeated id+name with an index, and both rejections. **Live
  confirmation against the router is pending** (user-run capture:
  `scratchpad/capture_toolcalls.py`).
- **Keyless DuckDuckGo default.** `web.search.enabled: true` with no backend
  now resolves to `duckduckgo` instead of a config error (`decisions.md` web
  search row revised by user approval). A named keyed backend without its key
  stays unavailable — no fallback. The generic `/tools` unavailable reason now
  points at `tools.json` and the transcript. Verified headlessly: config
  resolution for default/explicit/disabled/invalid, plus one live default
  search (3 ranked go.dev results, ~1 s).
