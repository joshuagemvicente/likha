# Feature: Headless print mode (`likha -p`)

**Status:** implemented (local) 2026-10-07 — automated suite green
(unit, app, TUI-resume and `tests/integration` tests against local
`httptest` providers; headless probe of the built binary against local
fakes only). Drafting defaults D1–D10 adopted as written. Outstanding: a
user-run live probe on a real provider (tasks T13). Requirement: [v1-spec FR-44](../v1-spec.md#attention-and-automation-amendment-user-approved-2026-10-07).

## Context

Likha runs only as a full-screen TUI. `internal/app/run.go` exits with
`likha: interactive terminal required` when stdin or stdout is not a
terminal, so Likha cannot be used from a script, a CI job, an editor task or
a repeatable evaluation. Claude Code (`claude -p`), OpenCode (`opencode run`)
and OMP (`omp -p`, `--mode json`) all offer a one-shot, non-interactive run;
the [feature comparison](../tooling-landscape/feature-comparison.md) ranks
it as the first step toward any server or SDK surface, and the
[2026-10-04 harness review](../agent-harness/improvement-review-2026-10-04.md)
needs a scripted driver for repeatable harness evaluations.

The agent core is already separate from the TUI (`ARCHITECTURE.md`): a run
is `agent.RunTurnWithOptions` plus an event callback. What is missing is a
driver that is not Bubble Tea, and a rule for what happens when the agent
wants a decision that only a person can make.

## Role

Let a script run one Likha prompt against a repository and read the answer,
without a terminal and without ever weakening the approval gate. Anything
that would ask a person is refused, and the refusal is reported honestly to
the model and to the caller. A script can rely on stable output and exit
codes.

## Scope

In scope: one non-interactive run started by `likha -p`, its output on
stdout and stderr, the `--json` event stream, exit codes, cancellation, and
storing the run as an ordinary session.

Out of scope:

- Any flag that approves edits, commands, MCP calls or web access without a
  person (`--allow-edits`, `--yes` for runs, `--dangerously-…`). Rejected in
  the user interview (decision 2).
- A long-lived server, RPC, SDK, ACP or web UI
  ([web-ui](../web-ui/spec.md) is a separate draft).
- Multi-turn conversations over stdin, steering prompts, and answering
  `ask_user` from stdin.
- Slash commands (`/compact`, `/init`, `/install`, `/plan`, …) in headless
  runs.
- Granting repository trust, MCP trust or web consent from a headless run.
- Windows (v1-spec scope).

## Decisions (user interview, 2026-10-07)

1. **`likha -p "<prompt>"` runs exactly one non-interactive run** in the
   selected repository and exits when it ends.
2. **Nothing prompts; it refuses instead.** Repository reads, read-only
   commands, and verification checks in a repository the user already
   trusted run exactly as they do interactively. Every action that would
   open a review or a question — an edit, any other command, an MCP call
   that needs trust, `ask_user`, a web consent — returns a refusal result to
   the model. No flag changes this.
3. **Output: the final answer on stdout, progress on stderr.** `--json`
   instead streams one JSON event per line on stdout for scripts.

## Drafting defaults (confirm in review)

Chosen while drafting, not in the interview. Each is stated as behavior
below; change them here first if review disagrees.

- **D1. Prompt source.** `-p TEXT` takes the prompt; `-p -` reads it from
  stdin (UTF-8, at most 256 KiB). Piped stdin is never read otherwise.
- **D2. Exit codes.** 0 completed; 1 the run failed (model or connection
  error, time limit, session not saved, output not written); 2 usage or
  configuration error before any model request; 130 interrupted by a signal.
  Refusals do not change the exit code: a run that completed with refusals
  exits 0 and reports them.
- **D3. Sessions.** Each headless run is saved as a normal session for the
  repository, titled from the prompt's first line without a naming request,
  and can be opened later with `likha --resume ID`. `-p` with
  `--resume ID` continues that session headlessly.
- **D4. Time limit.** `--max-time DURATION` bounds the whole run, default
  `30m`; `0` removes the bound.
- **D5. Flags.** `-p` combines with `--provider`, `--model`, `--api-key`,
  `--endpoint`, `--resume`, `--json`, `--max-time` and the repository path.
  It cannot combine with `--sessions`, `--login` or `--probe-effort`.
  Display flags (`--theme`, `--nerd-fonts`, `--ascii`) are accepted and
  have no effect. `--json` and `--max-time` without `-p` are usage errors.
- **D6. Effort.** The stored effort level for the provider and model
  applies, as at an interactive launch (FR-37). There is no effort flag
  (model-effort keeps CLI effort out of scope).
- **D7. MCP.** Configured MCP servers are not started in a headless run,
  because MCP trust is session-scoped and never granted headlessly, so every
  call would be refused. stderr says so once.
- **D8. Tools offered.** Edit tools stay offered so the model can still
  propose a change, and each proposal is refused with an instruction to
  describe the change in the answer. `ask_user` and the web tools are not
  offered; a call to them anyway is refused. Explore children (`task`) run
  as usual: they are read-only and never prompt.
- **D9. Reasoning in `--json`.** Reasoning deltas are emitted as
  `reasoning` events; the default text mode never prints reasoning.
- **D10. Other features.** Attention notifications (FR-41) never fire.
  Hooks (FR-45), once implemented, run as in an interactive run.
  Automatic compaction (FR-42) and model recovery (FR-36) apply once
  implemented.

## Functional changes

### Starting a run

1. `likha [options] -p PROMPT [repository]` starts one run in the selected
   repository with no terminal UI. It works whether or not stdin and stdout
   are terminals (D1 for `-p -`).
2. The provider, model and key resolve exactly as for an interactive launch
   (flags, environment, stored setup). First-run setup never starts: with no
   provider configured, Likha prints the existing "no provider configured"
   message and exits 2. A ChatGPT sign-in that needs a browser login exits 2
   and points at `likha --provider chatgpt --login`.
3. An empty prompt, a prompt beginning with `/`, or an invalid flag
   combination (D5) is a usage error: nothing is sent and Likha exits 2. The
   `/` message says slash commands are interactive only.
4. The connection check runs before the request, as today. A failed check
   exits 1 with the provider's error, and no prompt is sent.

### What runs and what is refused

5. The run uses the same harness, tools, limits and command classification
   as an interactive run, with one difference: no review, question or
   consent is ever shown (decision 2).
6. These run without a prompt, as today: `read`, `glob`, `grep`,
   `read_output`, `plan_update`, explore children, read-only commands, and
   verification checks in a repository the user trusted earlier in the TUI,
   while their fingerprint still matches. Refused-tier commands are refused
   as today.
7. Everything else that would have asked is refused without running:
   - an `edit` or `edit_file` proposal (nothing is written);
   - a command in the ask, always-ask or untrusted-verification tier;
   - `ask_user`, web search and web fetch (D8);
   - MCP tools (D7).
8. Each refusal is returned to the model as a refused tool result that names
   the action, says no reviewer is present in headless mode, and, for an
   edit, asks the model to describe the change in its answer instead. It is
   never reported as success or as a user rejection (FR-09). The model may
   continue the run.
9. Every refusal is reported to the caller: one stderr line in text mode, a
   `tool_result` event with status `refused` in `--json` mode, and a count in
   the final summary.

### Output: text mode (default)

10. stdout receives only the final answer: the assistant text of the
    response that ended the run, written once when the run completes, with
    a trailing newline. A run that fails writes nothing to stdout.
11. stderr receives progress as plain lines without color or cursor
    movement: the repository and model at start, one line per tool call and
    its outcome (auto-approved, refused, failed), notices, and a final
    summary with the session ID, tool calls, refusals, tokens and spend when
    the price is known. Reasoning is not printed.
12. Errors go to stderr prefixed `likha:`, naming the provider and cause, and
    never contain an API key, OAuth token or authorization header.

### Output: `--json`

13. With `--json`, stdout carries only JSON Lines, one event per line,
    flushed as events happen; stderr stays empty after startup. Usage and
    configuration errors before the run starts still go to stderr with
    exit 2.
14. Event types and their fields:
    - `start`: `schema` (1), `session_id`, `repository`, `provider`,
      `model`, `effort`.
    - `text`: `delta` of assistant text.
    - `reasoning`: `delta` of display reasoning (D9).
    - `tool_start`: `call_id`, `name`, `arguments` (bounded).
    - `tool_result`: `call_id`, `name`, `status` (`ok`, `error`,
      `refused`, `cancelled`), `auto_approved` reason when one applied,
      `content` (bounded).
    - `notice`: `text`.
    - `usage`: one model request's prompt, completion, reasoning and cached
      tokens, and cost when the price is known.
    - `result`: `status` (`completed`, `failed`, `cancelled`,
      `time_limit`), `text` (the final answer, empty unless completed),
      `session_id`, totals for requests, tool calls, refusals, tokens and
      cost, and `error` when the run did not complete.
15. Exactly one `result` event ends every run that started, including failed,
    cancelled and timed-out runs, and it is the last line written. Unknown
    fields may be added in later schema versions; existing fields keep their
    meaning while `schema` stays 1.

### Ending the run

16. Exit codes follow D2. The final summary (text mode) or `result` event
    (`--json`) agrees with the exit code.
17. SIGINT or SIGTERM cancels the run as Esc does: no further action starts,
    in-flight tools end as cancelled, the session is saved, and Likha exits
    130. A second signal exits immediately.
18. When `--max-time` elapses (D4), the run is cancelled the same way, the
    result reports `time_limit`, and Likha exits 1.
19. The session is saved when the run ends, whatever the outcome (D3).
    Saving fails visibly and exits 1; the answer already written stays
    written. Nothing pending is replayed on resume (FR-10).

## Acceptance criteria

- `likha -p "summarize README" repo | cat` prints only the answer on stdout,
  progress on stderr, and exits 0, with no terminal attached.
- A prompt that makes the model propose an edit leaves every file unchanged;
  the model receives a refused result naming headless mode; stderr reports
  the refusal; the run can still complete with exit 0.
- `ls` runs unprompted; `rm build.log` and `npm install` are refused without
  running; a trusted repository's test command runs unprompted until its
  script changes, after which it is refused.
- `ask_user`, web tools and MCP tools are not offered; forged calls return
  refusals.
- `--json` output parses line by line, begins with `start`, ends with one
  `result`, and contains no other stdout text.
- An unconfigured state directory exits 2 without contacting any server; a
  revoked key exits 1 with the provider's error and no prompt sent.
- SIGINT during a model stream exits 130, saves the session, and reports
  `cancelled`.
- A run exceeding `--max-time 2s` (shortened in tests) reports `time_limit`
  and exits 1.
- The headless session appears in `likha --sessions` and opens with
  `likha --resume ID`; `-p … --resume ID` continues it.
- No output, in either mode, contains the API key, even when the provider
  echoes it in an error body.
