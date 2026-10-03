# Release verification checklist — 2026-10-04

**Status:** the working checklist for converting "implemented, probe-verified"
into "verified (release)". Phases 1–4 are committed (`f39f896` core,
`5100342` TUI, `75ac1f2` docs). Headless probes already recorded: Phase 2
runtime (14 scenarios), Phase 3 composition (28), Phase 4 profiles (18) — all
under `-race`. User-verified in the live TUI so far: **plan mode** and the
**`/agents` task tree/profile display**.

Each item below names what to do, what to expect, and where to record the
outcome. Mark `[x]` with a date and one-line result; anything not exercised
stays honestly labeled unverified in the phase evidence files.

## 1. Interactive TUI walkthroughs (no credentials needed)

- [ ] **ask_user dialog** — prompt the model with something that triggers a
  question (or temporarily add one to a task). Expect: centered dialog with
  the complete question, numbered choices + free text, ↑/↓ and digits, Tab
  toggles text mode, Enter answers, Ctrl+S skips (refused result), Esc
  cancels the run, typed text never leaks into the composer or the steering
  queue, and the answer event appears exactly once in the transcript.
- [ ] **ask_user while steering** — hold a queued steering prompt, answer the
  question, then let the run continue. Expect: the queued row survives
  untouched and delivers after the tool group settles.
- [ ] **Consent dialogs (web)** — with `web_search` enabled in
  `tools.json`, first search shows backend + query + privacy copy; y allows,
  n/Esc declines (no request is issued); the grant lasts the conversation and
  resets after switching sessions or restarting.
- [ ] **Edit recovery** — start an edit approval, kill the app mid-review,
  relaunch, run `/tools recovery`. Expect: the journal is offered read-only,
  nothing replays automatically, and you can apply or discard explicitly.
- [ ] **`/todo` checklist** — ask the model to plan work with plan_update;
  open `/todo` mid-run and while idle. Expect: compact summary + full list,
  statuses render without color dependence, invalid updates never change the
  list, and an interrupted run marks active steps interrupted on resume.
- [ ] **`/skills` and `/skill`** — author a valid skill, one with an
  unsupported frontmatter field, and (temporarily) 33 skills. Expect: named
  errors, over-cap identities listed but not advertised, `/skill <name>`
  loads the body with provenance and starts a turn; edit the file after
  discovery and invoke again — expect the changed-on-disk refusal.
- [ ] **`/agents` deep walk** — verified 2026-10-04 (tree + profiles).
  Remaining: open a child's detail page (transcript, tool results, usage),
  page a large retained output with o/←/→, and cancel a branch with `c` from
  the confirmation while a run is active; Esc must still cancel the whole run.
- [ ] **Narrow terminal + resize** — run every surface above at 60×16, then
  resize mid-dialog and mid-review. Expect: no clipped controls, dialogs
  re-flow, the review restarts at its top.
- [ ] **Session switch hygiene** — with grants, a plan, and artifacts present,
  `/sessions` to another session and back. Expect: web grants reset, the plan
  restores with interruption marking, artifacts stay session-scoped.

## 2. Live provider probes (credentials required)

- [ ] **Hosted provider structured-tool probe (release gate)** — run a turn
  against ChatGPT (OAuth) or an API-key provider that exercises `read`,
  `grep`, `task` (a nested explore), and one refused call. Record provider,
  model, date, and outcomes in `specs/tooling-platform/implementation.md`.
  README/provider claims must stay limited to probed providers.
- [ ] **Brave search live probe** — configure `backend: "brave"` with a real
  key; search something time-sensitive; verify ranked titles/URLs/snippets,
  consent copy, and a 401 path with a deliberately bad key.
- [ ] **Tavily / Exa live probes** — same shape with their keys (Phase 5
  adapters; wire behavior labeled unverified until this runs).
- [ ] **DuckDuckGo live probe** — keyless; coordinator runs it headlessly
  once the adapter lands (see phase-5 evidence). If the HTML endpoint blocks
  the probe, record that outcome — it is exactly the fragility the consent
  copy discloses.
- [ ] **web_fetch live probe** — fetch a public docs page (markdown format),
  then a deliberately private URL (`http://`, `localhost`, an IP in a private
  range) and confirm each refuses with the policy reason and no connection.

## 3. Failure drills

- [ ] **Crash mid-edit-batch** — approve a multi-file proposal and kill -9
  mid-apply; relaunch and recover via `/tools recovery`; verify
  applied/restored/unresolved reporting matches the journal.
- [ ] **Crash mid-child-run** — kill during an active explore task; relaunch;
  expect interrupted records, repaired parent sentinels, and no replay.
- [ ] **Model-request exhaustion** — a task that loops tool calls stops at 32
  with `limited` and partial findings (probe-verified; spot-check live).
- [ ] **MCP refusal paths** — unsafe-schema server stays unavailable; plan
  mode refuses a trusted server's tool with server+tool identity.

## 4. Publication

- [ ] **Published-binary probe** — install a built archive via the curl path
  and repeat the smoke walkthrough against the installed binary.
- [ ] **README/provider claims audit** — every claim matches a recorded probe;
  unverified features keep their labels.

## Done means

Every box either checked with a dated result or explicitly deferred with a
reason in the phase evidence files. Existing checks passing alone never
substitutes for a checked box.
