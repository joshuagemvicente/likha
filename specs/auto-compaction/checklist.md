# Checklist: Automatic compaction

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md). Checked items are covered by
automated tests against local test providers (named in brackets); none has
been exercised in a real terminal or against a live provider.

## Trigger

- [x] A main-turn request estimated at 85% or more of a known context
      window is preceded by one automatic compaction.
      [agent `TestAutoCompactionCompactsKeepsPromptAndContinues`, tui
      `TestAutoCompactionRunCompactsContinuesAndPersists`]
- [x] A request at 84% is sent unchanged. [`TestShouldAutoCompact`,
      `TestAutoCompactionDoesNotRunWhenOffOrBelowThreshold`]
- [x] With an unknown window (`ctx ~…/?`) nothing compacts automatically.
      [same, tui `TestAutoCompactionNeedsKnownWindow`]
- [x] With `"auto_compact": false` in `config.json`, nothing compacts
      automatically and `/compact` still works.
      [tui `TestAutoCompactionOptOutSendsUncompacted`, providers
      `TestAutoCompactConfigDefaultsOnAndRoundTrips`]
- [x] The check runs before the first request of a run and between tool
      rounds, never while a review or question is open.
      [`TestAutoCompactionCompactsKeepsPromptAndContinues` (first request),
      `TestAutoCompactionKeepsDeliveredSteeringVerbatim` (between rounds),
      `TestAutoCompactionWaitsForOpenReview` (review)]
- [x] Explore children, profile tasks and session naming never compact
      automatically. [`TestAutoCompactionNeverRunsInExploreChildren`
      covers explore children; profile tasks share the same child loop
      and session naming is a single tool-less request outside the loop,
      by inspection]

## What the user sees

- [x] `Compacting…` shows in the status line while the summary is made, and
      the run stays active. [tui `TestAutoCompactionRunCompactsContinuesAndPersists`]
- [x] The automatic divider names the percentage and window, followed by
      the summary; the earlier transcript stays scrollable.
      [same, `TestAutoCompactDivider`]
- [x] The context segment drops to the compacted estimate before the next
      request. [same; agent test checks the fresh estimate event]
- [x] The turn footer shows `· compacted`. [same,
      `TestFooterTextSegments`, `TestEncodeTurnCompactedOnlyWhenSet`]
- [x] If the request is still at 85% or more after compacting, one notice
      says so and no second compaction happens in that run.
      [`TestAutoCompactionStandsDownWhenStillOverThreshold`]

## Continuing the task

- [x] The run continues to completion without user input.
      [agent and tui end-to-end tests]
- [x] The run's own prompt and delivered steering prompts reach the model
      verbatim after the summary.
      [`TestAutoCompactionKeepsDeliveredSteeringVerbatim`, `TestCompactRunLeavesInputUntouched`]
- [x] A steering prompt queued during compaction is delivered in the same
      run. [agent `TestAutoCompactionDeliversSteeringQueuedDuringCompaction`,
      tui `TestAutoCompactionDeliversPromptQueuedDuringCompaction`]
- [ ] Session grants, plan mode and `/install`'s plan gate behave the same
      after compaction. The `/install` plan gate is covered
      (`TestAutoCompactionKeepsInstallPlanGateOpen`); plan mode lives in the
      harness and session grants in run options, neither in history, so
      compaction cannot reach them — verified by inspection only.

## Failure and cancellation

- [x] A failed summarize shows the error, leaves the conversation
      unchanged, and the original request is still sent.
      [`TestAutoCompactionFailureSendsOriginalRequest`]
- [x] Esc during automatic compaction ends the run as cancelled, with the
      conversation uncompacted. [agent
      `TestAutoCompactionCancelLeavesHistoryUncompacted`, tui
      `TestAutoCompactionEscCancelsRunUncompacted`]

## Persistence and accounting

- [x] A resumed session continues compacted, with its divider.
      [tui `TestAutoCompactionRunCompactsContinuesAndPersists`]
- [x] The summarize request's usage is counted in session spend. [same]
- [x] No effort and no replay state is sent with the summarize request.
      [`TestAutoCompactionSummarizeSendsNoEffort`; replay via the shared
      `CompactHistoryUsage` path, `TestBackgroundRequestsSendNoReplay`]

## Release

- [x] `go test ./...` and `go vet ./...` pass.
- [x] `docs/usage.md` documents automatic compaction and `auto_compact`.
- [ ] CHANGELOG entry states what was verified (merged by the coordinator).
- [ ] Real-terminal walkthrough (tasks W1) recorded.
