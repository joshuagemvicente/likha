# Phase 3 implementation evidence — 2026-10-03

**Status:** integrated; final checks passed. Phase 3 covers `ask_user`
(FR-30), the persisted plan/todo checklist (FR-31), `/plan` read-only mode
(FR-32), strict global Markdown skills (FR-33), and optional Brave search +
public HTTPS fetch (FR-34). The user authorized another eight parallel
implementation workers after the Phase 2 walkthroughs were recorded.

## Baseline

- No new tests, no code-review pass, no commits. Existing checks remain intact.
- Phase 2 build/tests/race/walkthroughs passed (see phase-2-implementation.md);
  its unverified TUI/service scenarios remain unverified here.
- `golang.org/x/text` promoted to a direct require (no version change) for
  IDNA in the fetch address policy.

## Eight concurrent ownership areas

| Worker | Owned area | Status |
| --- | --- | --- |
| 1 | `internal/tools/ask_user.go` — question tool, bounds, skip/answer/cancel | delivered |
| 2 | `internal/tui/ask_view.go` — question dialog, exactly-once reply | delivered |
| 3 | `internal/tools/plan_update.go` — checklist tool and validation | delivered |
| 4 | `internal/session/plan.go` — plan persistence and interrupted resume | delivered |
| 5 | `internal/skills/discovery.go`, `parse.go` — strict catalog and fingerprints | delivered |
| 6 | `internal/tools/skill.go` — on-demand body loading with provenance | delivered |
| 7 | `internal/webtools/config.go`, `search.go`, `internal/tools/web_search.go` | delivered |
| 8 | `internal/webtools/fetch.go`, `internal/tools/web_fetch.go` — address policy | delivered |

The coordinator owns tool registration/main-registry composition, the `/plan`
mode gate (pre-approval refusals for edits, shell, and all MCP plus the
persistent footer indicator), `/todo` and `/skills`/`/skill` commands, the web
consent dialog, event plumbing, and these records.

## Delivered so far

- **plan_update tool (worker 3):** strict replace-all schema (≤32 steps,
  unique ids ≤64 runes, titles ≤240 runes, four statuses); invalid calls are
  refused before the apply hook so the live plan never changes; empty list
  clears; persistence failure is a visible Failed result with the old plan
  preserved; zero-effect registration permitted via the new harness-side
  target rule (see Recorded decisions).
- **skill tool (worker 6):** on-demand loading with strict name validation,
  provenance header naming the origin and provider disclosure, ≤64 KiB inline
  bound with visible truncation warnings, and refusal attribution for
  undiscovered/changed skills so the parent run continues.

## Recorded decisions

- `tools.Registry.Register` previously rejected every zero-effect builtin;
  it now permits empty effects only for harness-side targets (`user`,
  `session`) that carry no repository, provider, or network side effects to
  disclose and stay outside the authorization gate. `ask_user` and
  `plan_update` register under this rule; skill (Read) and the web tools
  (Network) declare effects normally.
- Plan-mode gate rule (coordinator composition): gated tools (every MCP tool
  regardless of annotations, plus built-ins with Write/Exec effects) are
  registered with a mode-named unavailable reason, so the definition is
  hidden from model requests while every attempted call still resolves to a
  refusal naming the mode — no approval flow can ever start, and `/tools`
  shows the same reason. Reads, ask_user, plan_update, skills, and
  consent-gated builtin web keep their ceilings. Plan-mode instructions
  extend the compiled harness's single system layer per run; explore
  children keep the ordinary harness.
- The ask broker and consent broker block the tool handler, never the UI:
  each sends a `TurnEvent` (`ask` / `consent`) through the working run's
  events channel with a capacity-one reply channel, so exactly-once answers
  and cancellation are structural, not bookkeeping. An unanswered question
  or pending consent dies with the run context and is recorded as an
  interruption, never replayed; resume scans the transcript for an
  unanswered question marker and records the interrupted note once.
- Plan persistence: `SavePlan` writes through to an additive `plans` table
  mid-run; `Store.Save` rewrites the row only when the snapshot carries a
  non-nil Plan, so a stale snapshot never wipes a mid-run update. On restore,
  `ResumePlan` merges highest-`UpdatedAt`, preserves completed statuses, and
  marks `Interrupted` when active steps were last updated before this app
  launch — revisited work is re-declared by a fresh plan_update.
- `/plan` toggles live per-session state (never persisted, reset on session
  switch) with a visible note when a run or review blocks the change; the
  footer identity carries a persistent PLAN MODE marker on every frame.
- Skill discovery freezes the catalog at each run boundary: the harness
  advertises names/descriptions/origin only, `/skills`/`/skill` read fresh
  metadata at the invocation boundary, changed-on-disk files refuse with a
  refresh request, and over-cap or malformed identities surface named
  errors once rather than repeating every turn.
- Web configuration follows the spec contract: `tools.json` enablement,
  `BRAVE_SEARCH_API_KEY` or 0600 `tool-keys.json` (never chmod'ed), env wins,
  keys never enter prompts or errors. Search consent binds to the configured
  backend, fetch consent to each canonical origin (fresh consent per
  redirect origin), grants live only in this process, declines return typed
  refusal results with no request toward the target, and consent dialogs
  serialize with approval reviews while Ctrl+C keeps run-cancellation
  semantics.
- IDNA canonicalization uses a local NFKC + RFC 3492 punycode implementation
  because `x/net` (which ships the public `idna` package) is a forbidden new
  dependency; `x/text` was promoted to a direct require for NFKC only. The
  fail-safe fallback: canonical hosts must still pass strict LDH checks and
  verify against the site's TLS certificate.

## Final checks

- `go build ./...` — passed.
- `go vet ./...` — passed.
- `go test ./...` — passed.
- `go test -race ./...` — passed.
- `git diff --check` and gofmt over changed files — clean.

## Walkthrough outcomes (composition probe, 2026-10-03)

A throwaway probe (`probe2/`, deleted after recording) drove the composed
registry headlessly under `go run -race`:

- Catalog availability: nil hooks register ask_user/plan_update/skill/web
  tools unavailable with named reasons; the task tool is unavailable without
  session persistence — all visible via `/tools`.
- plan_update through the real registry: duplicate ids, over-cap lists, and
  invalid statuses refuse before the apply hook (the live plan is never
  touched because the schema validator and handler gate before applying);
  valid updates reach the hook with all steps; the explicit empty list
  clears; summaries count correctly.
- ask_user with a real hook answers exactly once with mapped content; empty
  questions, nine options, and duplicate options refuse.
- Skill invocation of an undiscovered name refuses and asks for a catalog
  refresh.
- Plan mode: edit_file and run_command definitions are hidden from model
  requests, attempted calls still refuse with a plan-mode-named reason, and
  read tools plus checklist updates keep working under the gate.

## Unverified acceptance scenarios

- ~~`/plan` toggle, refusal naming, and the persistent footer marker.~~
  **User-verified 2026-10-04:** plan mode currently works as intended in the
  live TUI. The remaining plan-mode sub-scenarios (refusal text during an
  active run, MCP refusals with server identity, resume-without-inheritance)
  stay unverified.
- Interactive ask_user dialog keys, exactly-once answer flow, and draft/queue
  preservation in a real terminal.
