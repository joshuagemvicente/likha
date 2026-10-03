# Tasks: Agent tool loop (harness steering + round-cap auto-continue)

Ordered; each task carries its verification. No checkmarks until work
starts.

1. **Harness system message.** Add the compiled tool-contract system
   message (embed or constant in `internal/agent`), prepend it in
   `RunTurn` before `prior`, and keep it out of what the session store
   persists (verify via a snapshot round-trip in the test).
   *Verify:* unit test asserting the request history's first message is
   `system` with the steering text, exactly once, and that the persisted
   snapshot's first message is not it (AC-1).
2. **Checkpoint continuation in `RunTurn`.** Replace the post-loop
   `fail` with: emit a `notice` turn event, append a `developer`
   continue-instruction message, and re-enter the loop. Track rounds so
   the notice text can name the count. Keep all existing `ctx.Err()`
   checks and `appendUnexecuted` reconciliation untouched.
   *Verify:* fake-model test that requests one tool call per round;
   assert execution continues past round 32, a `notice` arrives, and the
   turn ends `done` (AC-3); a second pass asserts the round-64 notice
   (AC-4).
3. **TUI notice rendering.** Handle the `notice` kind in the event
   switch: append a `Lisa`-role conversation entry and leave `working`
   and any pending state untouched.
   *Verify:* TUI test feeding a `notice` event mid-run: entry appears,
   composer stays inert, no cancellation/pending side effects (AC-2/4
   UI half).
4. **Steering text review.** Read the tool-contract wording against
   FR-06/07/08/16: it must direct tool choice without implying that any
   command is pre-approved or that approval can be skipped.
   *Verify:* the wording is asserted in the AC-1 test (approval sentences
   present verbatim), preventing silent weakening later.
5. **Full suite + spec status.** Run `go test ./...`; flip this spec's
   status to `implemented (local)` and update
   [specs/README.md](../README.md) and the agent-harness cross-reference.
   *Verify:* suite green; index row matches status words rules.
