# Feature: Automatic compaction (compact before the context window fills)

**Status:** implemented (local) 2026-10-07 — decisions recorded 2026-10-07
(user interview); drafting defaults D1–D10 implemented as written.
Verified by automated checks only (`go test ./...`, `go vet ./...`,
`gofmt`, `-race` on agent, tui, transcript and providers) against local
test providers; no real-terminal walkthrough (tasks W1) and no live
provider. Requirement: [v1-spec FR-42](../v1-spec.md#attention-and-automation-amendment-user-approved-2026-10-07).
Refines [conversation-compaction](../conversation-compaction/spec.md)
(replaces its resolved decision 1, "manual-only in v1") and builds on
[context-tracker](../context-tracker/spec.md) (FR-23).

## Context

Every main-turn request resends the whole conversation. Long sessions grow
until a request no longer fits the model's context window, and the provider
rejects it. Rejected requests are never retried (FR-36), so the run ends with
an error and the user has to notice, type `/compact`, and ask the model to
continue.

`/compact` is the only way to shrink a conversation today. Conversation
compaction kept it manual because Likha had no request-side size estimate.
The context tracker now estimates every outbound request before it is sent
and resolves the context window from an override, provider metadata or the
bundled catalog, never guessing. That estimate is the trigger this feature
needs.

Claude Code, OpenCode V2 and OMP all compact automatically near the limit
([tooling-landscape](../tooling-landscape/feature-comparison.md) §2.4,
Tier 1 item 3).

## Role

Keep long runs going without the user watching the context meter. Compact
once, visibly, before a request would come close to the window, then carry
on with the same task. Never hide that it happened, never compact on a guess,
and never lose the user's own words for the task in progress.

## Scope

In scope: every main-turn model request, including the first request of a
run and requests between tool rounds; `/init`, `/install` and plan-mode turns
are main turns too.

Out of scope:

- Explore children and profile tasks. They start from fresh, bounded
  contexts (32 requests, five minutes) and keep failing as today if they
  overflow.
- Compacting after a provider has already rejected a request as too large.
  The trigger is the pre-request estimate only. A rejected request still
  ends the run under FR-36, and its error still points at `/compact`.
- Other compaction methods (pruning old tool output, keep-recent windows,
  provider-native checkpoints). The summarizer is the existing `/compact`
  call.
- A user-set threshold. 85% is fixed.
- Changing `/compact` itself. Manual compaction keeps its refusals, focus
  argument and idle-only behavior.

## Decisions (user interview, 2026-10-07)

1. **Trigger: 85% of the context window.** Before each main-turn request,
   when the estimated size of that request reaches 85% of the model's
   context window, Likha compacts first.
2. **Summarizer: the existing `/compact` call.** Same single summarize
   request, same provider and model, no effort and no replay state, same
   visible divider in the conversation.
3. **The run continues.** After compacting, Likha sends the request against
   the compacted conversation and the run goes on with no input from the
   user.

## Drafting defaults (confirm in review)

These were chosen while drafting, not in the interview. Each is a single
sentence so review can flip it without rewriting the spec.

- D1. **Unknown window means off.** When the context window is unknown
  (`ctx ~42k/?`), automatic compaction never runs and nothing is announced.
- D2. **On by default, with an opt-out.** `"auto_compact": false` in the
  private `config.json` turns it off. There is no TUI toggle or command.
- D3. **The current run's user words are kept verbatim.** The prompt that
  started the run and steering prompts already delivered in it follow the
  summary unchanged; everything else, including this run's tool rounds, is
  summarized.
- D4. **The summary emphasizes the task in progress.** The automatic call
  adds a fixed focus: what is done in the current task and what remains.
- D5. **One attempt per request, then stand down.** If the compacted request
  is still at or above 85%, or there was nothing to summarize, Likha shows
  a notice and does not compact again for the rest of the run.
- D6. **A failed summarize does not end the run.** Likha shows the error,
  keeps the conversation unchanged, sends the original request anyway, and
  stops trying for the rest of the run.
- D7. **Esc during automatic compaction cancels the run,** exactly like Esc
  during a model request (FR-04). The conversation is left uncompacted.
- D8. **Steering prompts keep their FR-21 timing.** Prompts queued during
  compaction stay queued and are delivered at the next safe point of the
  same run; a finished automatic compaction does not hold the queue the way
  a finished `/compact` does.
- D9. **Run state survives compaction.** Session grants, plan mode,
  `/install`'s plan gate, the plan checklist, retained output artifacts and
  the tool-round count are unaffected.
- D10. **The turn footer records it**, e.g. `· compacted`.

## Functional changes

### When it runs

1. Before each main-turn model request, Likha estimates the request as the
   context tracker already does. If the context window is known (D1),
   automatic compaction is on (D2), and the estimate is at least 85% of the
   window, Likha compacts before sending.
2. The check happens only at the point where a request is about to be sent:
   after the current tool calls have settled and after queued steering
   prompts have been delivered. It never runs while a review or question is
   open, and never in the middle of a streamed response.
3. Automatic compaction never runs for explore children, profile tasks,
   session naming, or the summarize request itself.

### What the user sees

4. While compacting, the status line reads `Compacting…` and the working
   indicator stays up. The run is still active: `/` commands stay refused
   and Esc cancels the run (D7).
5. When compaction finishes, the conversation shows the same divider as
   `/compact`, marked automatic and naming the trigger, followed by the
   summary. Example:
   `Conversation compacted automatically at ~86% of the 200k context window. Summary of earlier turns:`
   The earlier transcript stays on screen and scrollable; only what is sent
   to the model changes.
6. The context segment of the status line is recalculated for the compacted
   conversation before the next request is sent.
7. The run then continues: the model receives the summary, the current run's
   own prompts verbatim (D3), and a note that work already done in this run
   is in the summary and should not be repeated.
8. A run that compacted shows it in its footer (D10).
9. If compaction cannot bring the request under 85%, or there is nothing to
   summarize, a notice says so once and the run continues without further
   automatic compaction (D5). Example:
   `Still at ~91% of the context window after compacting; continuing without compacting again in this run. /compact with a focus may help.`
10. If the summarize request fails, the error is shown, nothing is replaced,
    and the run continues with the original request (D6). If that request
    is then rejected for size, the run ends with the usual error, which
    names `/compact`.

### Persistence and accounting

11. The compacted conversation and the divider are saved to the session as
    `/compact` saves them, so a resumed session continues compacted.
12. The summarize request's usage is added to session spend, as `/compact`'s
    is.
13. Replay state from summarized turns is dropped, as `/compact` drops it
    (FR-39). The summary is never sent with effort (FR-37 decision 9).

### Turning it off

14. With `"auto_compact": false` in `config.json`, no automatic compaction
    runs; the status line keeps its 80% warning and `/compact` works as
    before. `docs/usage.md` documents the key.

## Acceptance criteria

- A conversation whose next request is estimated at 85% or more of a known
  window compacts once, shows the automatic divider, and the run continues
  to completion without user input.
- At 84% nothing happens; the request is sent unchanged.
- An unknown window never triggers compaction.
- With `"auto_compact": false`, a request at 95% is sent without compacting.
- The run's own prompt and delivered steering prompts reach the model
  verbatim after the summary.
- A compaction that leaves the request at 85% or more shows the stand-down
  notice once, and no second compaction happens in that run.
- A failed summarize shows the error, leaves the conversation byte-identical,
  and the original request is sent.
- Esc during automatic compaction ends the run as cancelled with the
  conversation uncompacted.
- A steering prompt queued during compaction is delivered in the same run.
- An explore child or profile task never compacts automatically.
- Resuming after an automatic compaction restores the compacted conversation
  and its divider.
- The summarize request's usage appears in session spend.
