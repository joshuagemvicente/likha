# Phase 4 implementation evidence — 2026-10-04

**Status:** integrated; final checks and walkthroughs passed. Phase 4 covers
user-authored agent profiles and the built-in `review` role (FR-28–29) plus
`task` dispatch generalization from the single hardcoded `explore` enum to the
frozen run catalog. The user authorized eight parallel implementation workers
after the Phase 3 records.

## Baseline

- No new tests, no code-review pass, no commits. Existing checks remain intact.
- Phase 3 build/tests/race/final checks passed (see phase-3-implementation.md);
  its unverified interactive and live-service scenarios remain unverified here,
  including the web search live-backend probe (no real Brave traffic exercised).

## Eight concurrent ownership areas

| Worker | Owned area | Status |
| --- | --- | --- |
| 1 | Spec plus discovery — `AGENT.md` catalog load, validation, freeze, fingerprints | delivered |
| 2 | Policy — allowlist narrowing and the parent/user permission intersection | delivered |
| 3 | Runner — generic profile child loop reusing the explore machinery | delivered |
| 4 | Manager generalization — `task` dispatch from the explore enum to the frozen catalog | delivered |
| 5 | TUI profiles — `/agents` listing with ceilings, models, over-cap and error entries | delivered |
| 6 | TUI task rows — profile name alongside task ID, depth, status, provider/model | delivered |
| 7 | Docs — user guide chapter and this evidence file | delivered |
| 8 | Coordinator composition — registration, harness/task descriptions, persistence | delivered |

## Coordinator integration record

- `TaskDefinitionFor`/`TaskTool` generalized with a variadic frozen-catalog
  enum (Phase 2 callers unchanged via the nil default); unknown agents refuse
  budget-free at the tool boundary and again in the manager's `validateSpec`.
- `ExploreRegistry` forwards the run catalog to nested task tools; the
  registry's child ceiling now keys off any non-main agent scope, not the
  literal `explore` name.
- `RunOptions.TaskAgents`/`TaskProfiles`: the TUI freezes the catalog at each
  run boundary, drift-checks instructions via `ReadProfile`, refuses profiles
  whose optional `model` differs from the served model (no substitution), and
  reports catalog errors once per distinct set.
- `turn_execution` builds one runner per profile from
  `profiles.Resolve(profile, mainScope, 1)`, with max-depth nodes dropping
  `task` through `ProfileScopeForDepth`; explore keeps the legacy runner.
- Persistence: `validateTask` now accepts any catalog-shaped agent name, so
  review/profile records resume like explore records.
- TUI: `/agents` renders the profile catalog section and `[profile]`-labeled
  tree rows; queued task rows attribute the profile from call arguments;
  profile errors and model-mismatch notices surface as transcript entries.

The coordinator owns integration, the run-boundary catalog freeze, event
plumbing, and these records.

## Intended integrated contract

- Discovery loads only `<stateDir>/agents/<name>/AGENT.md` from the private
  global state directory with skills-equivalent path safety (real directories,
  non-symlink regular file, canonical resolution inside the agents directory);
  a missing directory is a normal empty catalog.
- Frontmatter allows only `name`, `description`, `model`, and `tools`; the name
  equals the directory name per the skills rule; unknown, missing, duplicate,
  oversized, symlink, and escape identities are named rejections.
- Reserved names `main`, `explore`, `task`, `review`, and `implement` are never
  advertised or invocable; at most 32 identities are advertised, with over-cap
  identities held out and surfaced by name; errors never prevent startup.
- `tools` is an allowlist over `glob`, `read`, `grep`, `task` that may only
  narrow; the effective set is parent effective capabilities ∩ user/mode
  policy ∩ profile allowlist; `ask_user` and unknown names refuse at
  discovery; an empty array is a definition error.
- `model` names one exact id served by the configured provider or the spawn
  refuses with a visible reason naming profile, model, and provider; `explore`
  keeps the same configured provider/model; no fallback or substitution.
- `review` joins `explore` as a read-only built-in with a compiled prompt and
  no LSP/semantic tools, the absence documented; `implement` stays
  unavailable and unlisted.
- Dispatch re-reads and re-validates the selected `AGENT.md`; changed-on-disk
  refusals request a catalog refresh and create no child; forged, unknown,
  reserved, over-cap, and post-freeze names refuse without consuming spawn
  budget.
- Profile instructions are attributed as untrusted user-authored data under the
  task-brief framing; child findings remain untrusted; depth, concurrency, and
  budget limits match the shared explore contract exactly.
- Task records keep the profile name; interrupted resume marks tasks
  interrupted regardless of profile, never replays, and never reloads a
  changed disk file to reconstruct history.

## Final checks

## Final checks

- `go build ./...` — passed.
- `go vet ./...` — passed.
- `go test ./...` — passed.
- `go test -race ./...` — passed.
- `git diff --check` and gofmt over changed files — clean.

## Walkthrough outcomes (composition probe, 2026-10-04)

A throwaway probe (`probe4/`, deleted after recording) drove discovery, the
composed registry, and the real profile runner end to end against a scripted
fake SSE provider, under `go run -race`:

- Discovery: a valid profile with a narrowed `tools: read, grep` allowlist
  loads with instructions; a reserved name (`explore`) and an unsupported
  frontmatter field (`allowed-tools`) are named rejections that never hide
  the valid identity.
- The task tool's schema enum lists explore, review, and the discovered
  profile; a forged agent name refuses budget-free (schema enum first, then
  the tool boundary).
- A live `review` child completed through the generic profile runner with the
  read tool, an attributed record (`Agent: review`), and a persisted record
  that survives `SaveTask`/`Tasks` round-trips under the generalized
  `validateTask`.
- An allowlist narrowed to `read` still completes while a forged `edit_file`
  call inside the child refuses; `grep` remains available to the full-ceiling
  review profile.
- A review parent nested an explore leaf at depth 2 (one depth-2 record);
  a depth-2 leaf's forged `task` call refused with the tool absent from its
  registry — no grandchild spawn.
- Every child request carried the inherited model name with fresh contexts
  (≤3 first-request messages) and no cross-task marker leaks.

## Unverified acceptance scenarios

- Interactive `/agents` walkthroughs of discovered profiles, ceilings, models,
  and over-cap/error entries.
- Changed-on-disk refusal exercised in a live run (edit after discovery, then
  dispatch).
- Over-cap catalog UX in the TUI with more than 32 valid identities.
- A live `review`-profile run against a real repository.
- Hosted-provider probe: a profile `model` served and refused by the
  configured provider, with visible refusal reasons.
- Standing Phase 3 gap: the web search live-backend probe remains unverified.
