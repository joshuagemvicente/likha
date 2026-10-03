# Checklist: Agent tool loop (harness steering + round-cap auto-continue)

Observable outcomes, mirrors v1-spec FR-19/FR-20 wording.

- [ ] Every agent request's model-visible history starts with exactly one
      system message carrying the read-only steering text, and that
      message never appears in a persisted or resumed session (FR-19,
      AC-1).
- [ ] The steering text directs the model to the built-in read-only tools
      and states that `run_command` requires explicit user approval and
      is not sandboxed — the approval contract is restated, never
      weakened (AC-2, task 4 review).
- [ ] A turn crossing the tool-round checkpoint shows a visible notice
      entry and keeps executing tools; the turn can still end `done`
      after round 32, and the previous terminal error does not occur
      (FR-20, AC-3).
- [ ] Each later checkpoint emits another notice naming the round count;
      the turn remains cancellable at every round with today's
      cancellation guarantees (AC-4).
- [ ] `edit_file`, `run_command`, and first-use MCP tools still round-trip
      through their normal approval dialogs unchanged (FR-06/07/08/16
      unchanged).
- [ ] Failure of any step — an error mid-turn, a rejected approval — is
      reported per FR-09 exactly as today; the notice path introduces no
      new silent-failure route.
