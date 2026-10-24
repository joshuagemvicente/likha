# Tasks: Headless print mode

**Progress (2026-10-07):** T1–T12 implemented (local); T12's
`CHANGELOG.md` entry is added when the branch merges. T13 (user-run live
probe) is outstanding. Commits: `refactor(effortprobe)` (T8's scrubber moved
to `internal/redact`), `refactor(tui)` (T3, `internal/runsetup`),
`feat(agent)` (T1–T2), `feat(app)` (T4–T11), `docs`. Notes from
implementation:

- T1: the refusal is `agent.NoReviewerError` returned by a shared
  `review` helper at the three review sites; the message reads
  `refused (<kind> needs review): no reviewer is present in headless mode,
  so … .` An MCP refusal reaches the model through the registry's generic
  authorization message (as an MCP decline always has); headless never
  starts MCP servers, so no headless run hits that path.
- T4: signal handling lives in `internal/app` (`interruptContext`); the
  driver only reads the cancellation cause.
- T11: integration tests run a binary built once in `TestMain`, because
  `go run` reports every child exit status as 1.
- Follow-up fixes (2026-10-19, automated checks only): D10 automatic
  compaction is wired (`internal/app` resolves the window like the TUI and
  passes `auto_compact`; the recorder stores the TUI divider and the
  `compacted` footer flag; renderers report a notice plus the summarize
  `usage`). A stopped run (signal, time limit, unwritable output) stores a
  Likha stop notice in place of the cancellation error, the TUI's
  `Run cancelled; …` wording for a signal. The first failed output write
  cancels the run (`errOutputFailed`). `session.Store.Save` names the
  session once, so callers no longer re-wrap it. `redact` ignores secrets
  shorter than 8 bytes.
- Follow-up (2026-10-19, automated checks only): the `--json` delta
  redactor releases each stream only up to its last whitespace not after
  "bearer" (`deltaRedactor`, `internal/headless/render.go`), so every chunk
  redacts as the whole response; tool_start, tool_result, usage,
  auto_compacted, done, error and stream_interrupted end a response and
  release the rest. A secret form holding whitespace makes the stream wait
  for the response's end; 64 KiB without whitespace releases through
  `redact.SplitBefore`. `redact.Secrets` merges overlapping occurrences and
  treats a byte another redaction replaces as a word edge.
- Follow-up (2026-10-19, automated checks only): the `auto_approved` reason
  and the text label come from `tools.Result.AutoApproved`, which the agent
  sets where it writes the `Approval: auto-approved (…)` line, and are
  redacted; tool names and call IDs are redacted in both renderers. The
  redaction list is dynamic: `Config.secrets` adds every token the client's
  sign-in has held (`model.Client.OAuthTokens`, append-only, shared by
  forks), and `deltaRedactor` recomputes its forms when the list grows.
  `redact` also matches lowercase-hex percent escapes and JSON with `\/`
  (not partly or doubly escaped spellings). `redact.SplitBefore` searches as
  if an open bearer's value had arrived, so a short key right before
  "bearer" and a long run of spaces is never split by a cap release; its
  tail must exceed the longest whole-token form plus the longest form.

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md). Each milestone leaves `go test ./...`
and `go vet ./...` green. Nothing user-visible ships before M3; M1 and M2
change no interactive behavior.

Fixed values (spec decisions and drafting defaults):

| Name | Value |
| --- | --- |
| stdin prompt limit (`-p -`) | 256 KiB, UTF-8 |
| default `--max-time` | 30m (`0` = none) |
| exit codes | 0 completed · 1 failed/time limit · 2 usage/config · 130 signal |
| `--json` schema version | 1 |
| bounded `arguments` / `content` in events | 4 KiB each, UTF-8 safe |

## M1 — Refuse instead of asking (`internal/agent`)

- [x] **T1. `RunOptions.NoReviewer`.** Add a string field (empty = a person
      can review) holding the refusal reason, e.g. "no reviewer is present
      in headless mode". Thread it to `awaitApproval` (via the registry's
      options) so that, when set, no `approval` event is emitted and a typed
      `*NoReviewerError` (kind: edit, command, mcp) is returned. Callers map
      it to a refused tool result, never to "rejected by user" (FR-09).
      *Verify:* table test over the three approval sites: no event emitted,
      result status refused, file unchanged, command not run.
- [x] **T2. Refusal wording.** Edit refusals tell the model to describe the
      change in its answer; command refusals name the tier
      (ask / always-ask / untrusted check) and that the user can run it;
      MCP refusals name the server. Auto paths (read-only, trusted check)
      are untouched.
      *Verify:* `command_policy_test.go` cases for every tier with
      `NoReviewer` set; trusted check still auto-runs; changed fingerprint
      refuses.

## M2 — Shared run assembly (behavior-preserving refactor)

- [x] **T3. Extract non-UI run options.** Move the parts of
      `tui.toolRunOptions` that do not touch UI state (outputs store,
      `SaveTask`, `PlanApply`, `EditJournal`, skill catalog, task profiles,
      command trust hooks) into a package both `tui` and `headless` can
      import (e.g. `internal/runsetup`). Ask broker, web consent and steer
      stay in `tui`. Separate `refactor(tui): …` commit; no behavior change.
      *Verify:* existing TUI suite green unchanged; import grep shows the new
      package imports no `tui`/`bubbletea`/`lipgloss`.

## M3 — Headless driver (`internal/headless`)

- [x] **T4. Driver.** `Run(ctx, Config, stdout, stderr) int` builds
      `RunOptions` from T3 with `NoReviewer` set, `Ask`/`WebSearch`/
      `WebFetch` nil, `servers` nil, `steer` nil, then calls
      `agent.RunTurnWithOptions`. Context carries `--max-time` and signal
      cancellation (SIGINT/SIGTERM; a second signal kills hook process
      groups and exits within 2 seconds, the session already saved; a
      third exits at once).
      *Verify:* unit test with a fake `model.Client` endpoint (`httptest`)
      covering completed, failed, cancelled, time limit.
- [x] **T5. Text renderer.** stderr progress lines (start, tool start and
      outcome, refusals, notices, summary); stdout gets only the final
      assistant text from `done.History`, written once. No ANSI codes.
      *Verify:* golden stderr/stdout for a run with one read, one refused
      edit and a final answer.
- [x] **T6. JSON renderer.** Map `TurnEvent` kinds to the spec's event types;
      exactly one `result` last, also on failure/cancel/timeout; bounded
      fields; `encoding/json` per line with flush.
      *Verify:* decode every line; first is `start` with `schema: 1`, last is
      `result`; refused edit appears as `tool_result` `status: refused`.
- [x] **T7. Session persistence.** Create or load the session, title from the
      prompt's first line (no naming request), save `History` plus plain
      entries renderable by the TUI, save on every ending. Save failure →
      exit 1 with a visible error.
      *Verify:* `store.List` shows the session; TUI `--resume` model renders
      it (tui test loading the saved snapshot).
- [x] **T8. Redaction.** Pass every printed error and `error` field through
      the credential scrubber (move `effortprobe.Redact` to a shared package
      if needed, as its own refactor commit).
      *Verify:* fake provider echoing the key in a 400 body; neither stream
      contains it.

## M4 — CLI wiring (`internal/app`)

- [x] **T9. Flags.** `-p`, `--json`, `--max-time`; `-p -` reads stdin
      (256 KiB cap). Combination errors (D5), empty prompt, leading `/` →
      exit 2 before any request. Branch before the interactive-terminal
      check and before `mcp.NewMcpManager`; print the one-line MCP note when
      servers are configured.
      *Verify:* app tests for each usage error; no request reaches the fake.
- [x] **T10. Startup.** Reuse provider/model/key resolution; no setup
      (exit 2); ChatGPT login needed → exit 2 with the `--login` hint;
      failed connection check → exit 1; stored effort applied (D6); OpenCode
      session header set.
      *Verify:* app tests mirroring the existing startup error tests.

## M5 — End-to-end and docs

- [x] **T11. Integration tests** (`tests/integration/headless_test.go`):
      `go run ./cmd/likha -p …` against an `httptest` provider, no TTY:
      completed run, refused edit (file unchanged), read-only command,
      refused command, `--json` parse, SIGINT → 130, `--max-time` → 1,
      unconfigured state dir → 2, `--resume` continuation.
- [x] **T12. Docs.** `--help` text (drop "needs an interactive terminal",
      add the three flags), `docs/usage.md` "Headless runs" section with the
      event table and exit codes, `README.md` pointer, `CHANGELOG.md`
      Unreleased entry stating what was verified.
- [ ] **T13. Live probe (user).** One headless run on a real provider,
      text and `--json`, recorded in the CHANGELOG entry.
