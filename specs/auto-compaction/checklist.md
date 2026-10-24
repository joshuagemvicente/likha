# Checklist: Automatic compaction

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Trigger

- [ ] A main-turn request estimated at 85% or more of a known context
      window is preceded by one automatic compaction.
- [ ] A request at 84% is sent unchanged.
- [ ] With an unknown window (`ctx ~…/?`) nothing compacts automatically.
- [ ] With `"auto_compact": false` in `config.json`, nothing compacts
      automatically and `/compact` still works.
- [ ] The check runs before the first request of a run and between tool
      rounds, never while a review or question is open.
- [ ] Explore children, profile tasks and session naming never compact
      automatically.

## What the user sees

- [ ] `Compacting…` shows in the status line while the summary is made, and
      the run stays active.
- [ ] The automatic divider names the percentage and window, followed by
      the summary; the earlier transcript stays scrollable.
- [ ] The context segment drops to the compacted estimate before the next
      request.
- [ ] The turn footer shows `· compacted`.
- [ ] If the request is still at 85% or more after compacting, one notice
      says so and no second compaction happens in that run.

## Continuing the task

- [ ] The run continues to completion without user input.
- [ ] The run's own prompt and delivered steering prompts reach the model
      verbatim after the summary.
- [ ] A steering prompt queued during compaction is delivered in the same
      run.
- [ ] Session grants, plan mode and `/install`'s plan gate behave the same
      after compaction.

## Failure and cancellation

- [ ] A failed summarize shows the error, leaves the conversation
      unchanged, and the original request is still sent.
- [ ] Esc during automatic compaction ends the run as cancelled, with the
      conversation uncompacted.

## Persistence and accounting

- [ ] A resumed session continues compacted, with its divider.
- [ ] The summarize request's usage is counted in session spend.
- [ ] No effort and no replay state is sent with the summarize request.

## Release

- [ ] `go test ./...` and `go vet ./...` pass.
- [ ] `docs/usage.md` documents automatic compaction and `auto_compact`.
- [ ] CHANGELOG entry states what was verified.
- [ ] Real-terminal walkthrough (tasks W1) recorded.
