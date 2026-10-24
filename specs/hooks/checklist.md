# Checklist: Hooks

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md). A checked item is covered by an
automated test that runs real hook scripts (named after the item); nothing
here has been seen in a real terminal yet (tasks W1).

## Configuration

- [x] Hooks load only from `<stateDir>/hooks.json`. A repository's
      `.likha/hooks.json` or `.claude/settings.json` runs nothing.
      (`TestRepositoryHookFilesAreIgnored`)
- [x] With no `hooks.json`, Likha behaves byte-for-byte as before: same
      requests, reviews and transcript. (`TestNoHooksLeavesRequestsUnchanged`,
      `TestNilGateLeavesDispatchUnchanged`, and the unchanged existing suite)
- [x] An invalid file (bad JSON, unknown event, missing command, bad
      matcher or timeout, writable by others) starts no hooks. The error
      shows at launch and in `/hooks`. (`TestLoadRejectsWholeFile`,
      `TestHooksInvalidFileStartsNoneAndIsReported`)
- [x] Likha never creates `hooks.json`.
      (`TestLoadMissingFileIsEmptyAndNotCreated`)

## Blocking

- [x] A `pre_tool` hook that exits 2 blocks the call. The model gets
      `Blocked by hook "<name>": <reason>`, the tool does not run, and no
      review opens. (`TestPreToolHookBlocksCommandBeforeReview`,
      `TestGateBlockSkipsAuthorizeAndRun`)
- [x] A `{"decision":"block"}` on stdout blocks the same way.
      (`TestFireStdoutBlockDecision`)
- [x] A hook's `allow`/`approve` output never skips a review and shows a
      "hooks cannot approve" warning. (`TestPreToolHookCannotApprove`,
      `TestFireApproveDecisionIsIgnoredWithWarning`) Questions use the same
      path (the gate runs before `ask_user` opens anything); no separate
      `ask_user` test.
- [x] Session grants and auto-approved command tiers never bypass a
      `pre_tool` hook. (`TestPreToolHookBlocksSessionGrantedCommand`)
- [x] A failing or timed-out blocking hook blocks (fail closed), unless it
      sets `on_error: allow`, in which case a warning shows.
      (`TestFireTimeoutFailsClosedOrOpen`,
      `TestFireUnreadableOutputAndNonzeroExitFailClosed`)
- [x] A blocked `prompt_submit` keeps the draft and sends nothing. A blocked
      steering prompt is dropped with a notice, and the rest of the queue
      stays. (`TestPromptSubmitBlockKeepsDraftAndSendsNothing`,
      `TestSteeringPromptBlockedAtDelivery`,
      `TestSteerBlockedDropsOnlyThatQueuedPrompt`,
      `TestAllSteeringBlockedEndsRunWithoutRequest`)

## Observing

- [x] `session_start`, `post_tool`, `run_end` and `attention` hooks receive
      their documented payloads and never change Likha's behavior.
      (`TestObserveHooksRunOffTheUIAndReportFailures`,
      `TestPostToolHookEventCarriesResult`, `TestFirePayloadAndEnvironment`)
- [x] A slow observe-only hook does not delay the UI or the next request.
      (`TestObserveHooksRunOffTheUIAndReportFailures`: firing returns at once
      and the hook runs in a Bubble Tea command)
- [x] Every hook failure shows one warning naming the hook and the cause.
      (`TestFireObserveFailureNeverBlocks`,
      `TestObserveHooksRunOffTheUIAndReportFailures`)

## Safety and scope

- [x] No `LIKHA_API_KEY` or `LIKHA_*_API_KEY` reaches a hook's environment
      or payload. (`TestFirePayloadAndEnvironment`)
- [x] Hook output never reaches the model, except a block reason inside a
      refusal. (By construction: only `BlockError` text enters a tool result;
      `TestPreToolHookBlocksCommandBeforeReview` checks the refusal text.)
- [x] Explore children and profile tasks fire `pre_tool`/`post_tool` with
      their identity, and never `prompt_submit` or `attention`.
      (`TestExploreChildReadBlockedAndNamed/explore` and `/review`)
- [ ] Headless runs apply the same blocks and report them as refusals.
      Waits for [headless-print](../headless-print/spec.md) (tasks T12).

## Surfaces

- [x] `/hooks` shows the loaded hooks with their last outcome, the load
      error, or "No hooks configured". It works during a run.
      (`TestHooksViewStates`, `TestHooksInvalidFileStartsNoneAndIsReported`)
- [x] `/hooks` appears in `/help` and in the `/` autocomplete.
      (`TestHooksViewStates`)
- [ ] Blocked tool items and hook warnings are legible in ASCII mode and
      without color. They are plain text in the existing refusal and notice
      rows; not separately tested or seen in a terminal.
- [x] `docs/usage.md` documents the format, events, payloads, exit codes
      and the safety note.
