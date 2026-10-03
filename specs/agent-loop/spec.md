# Feature: Agent tool loop (harness steering + round-cap auto-continue)

**Status:** implemented (local). Existing request/history/approval checks pass;
32/64-round and live-provider walkthroughs remain unverified.
See [implementation evidence](../tooling-platform/implementation.md).

**Phase:** 1. Main-run behavior only. The approved implementation constraint
is existing checks plus recorded walkthrough evidence, with no new automated
tests or code-review pass. Child explore has separate terminal budgets under
[explore-agents](../explore-agents/spec.md); main continuation does not reset them.

## Purpose and scope

Two user-reported turn failures, validated against the code on 2026-10-01:

1. **Approval prompt for read-only searches.** `RunTurn` sends no system
   message at all (`internal/agent/agent.go` builds history as prior +
   user prompt; `internal/model/client.go` never injects one). Nothing
   steers the model to the built-in read-only tools, so models regularly
   inspect the repository through `run_command` (`grep -rn`, `rg`, `find`)
   and the user is shown the shell-command approval dialog for a search
   that FR-05 intends to be approval-free. The dialog is honest (any shell
   command may touch anything), but the friction is a harness defect: the
   model was never told the contract.
2. **Turn hard-stops at the tool-round checkpoint.** `RunTurn` loops
   `for range 32` and on exhaustion emits
   `fail(fmt.Errorf("model exceeded 32 consecutive tool rounds"))`
   (`agent.go:52-113`). The turn ends as an error entry; history survives,
   but the task is abandoned mid-flight and the user must manually
   re-prompt — and the new turn just runs into the same wall on long tasks.

This feature fixes both inside the existing loop. It is the first slice of
[agent-harness/](../agent-harness/spec.md) (the tool-contract section of the
harness prompt, landed narrow so the bug stops reproducing); the full
harness prompt and `AGENTS.md` loader remain that spec's scope.

## Root causes (validated)

- No system message exists anywhere in the request path, so tool choice is
  left to the model's defaults.
- The 32-round checkpoint treats "budget exhausted" the same as "model
  failed": it emits a terminal error event instead of a continuation.

## User-visible behavior

1. **Repository inspection has an approval-free dedicated-tool path.** When
   the user asks to find/search/read, the harness directs `grep`/`glob`/`read`.
   A model-requested shell still prompts; steering is not deterministic runtime
   enforcement of tool choice. Concretely: Likha sends
   a harness system message with every request whose tool contract states
   that the built-in read-only tools are the default for repository
   inspection and that `run_command` is reserved for actions only the
   shell can perform (building, running tests, git, anything outside the
   repository). The message is compiled into the binary, not stored in
   history, and is not user-editable in this slice.
2. **The round checkpoint continues instead of failing.** When a turn
   reaches the consecutive tool-round checkpoint, Likha appends a visible
   conversation notice (Likha role: "Tool-round checkpoint reached;
   continuing."), tells the model to continue in the same turn, and keeps
   the loop running. The notice recurs at each subsequent checkpoint so
   long tasks stay legible. The turn ends only when the model produces a
   final answer, the user cancels (existing Ctrl+C behavior, unchanged),
   or a real error occurs. No wall re-appears at a later fixed count.
3. **No approval semantics change.** `edit_file`, `run_command`, and
   first-use MCP tools keep FR-06/07/08/16 gating exactly as today. The
   harness message must not weaken the approval contract, must not ask
   the model to avoid approval, and must not claim commands are safe.

## Non-goals

- No auto-approval or allowlist of shell commands: command approval is not
  sandboxing (v1-spec §8), and parsing shell text for "read-only" is
  fragile. The fix is steering the model, not lowering the gate.
- No compaction auto-trigger at the checkpoint; `/compact` stays manual.
- No user-editable or project instructions file; that is agent-harness
  scope (AGENTS.md loader).
- No change to the checkpoint interval beyond making it non-terminal;
  `32` stays the notice cadence.

## Acceptance criteria

- AC-1: A captured model request for any turn contains exactly one system
  message, first, carrying the read-only-tool steering text; it is absent
  from the persisted session history.
- AC-2: With the steering in place, a scripted search-shaped task reaches
  the model with the system message and, when the model requests
  `run_command` for inspection anyway, the existing approval flow still
  gates it (no behavior change possible from prompt content alone).
- AC-3: An exercised main run past round 32 emits a visible checkpoint notice,
  keeps executing authorized tools, and can complete rather than producing
  the former round-cap error. Record evidence or leave this case unverified;
  this milestone does not require creation of a new fake-model test.
- AC-4: The same applies at round 64; cancellation stops subsequent actions
  with no executed-but-unreported tool. Child round exhaustion remains a
  limited task outcome rather than an unlimited child continuation.
- AC-5: The full suite (`go test ./...`) is green; the previous
  "exceeded 32 consecutive tool rounds" error no longer appears as a
  terminal turn outcome in tests.
