# Feature: Model request recovery (retry dropped and failed model requests)

**Status:** planned — decisions recorded 2026-10-05 (user interview); no
implementation started. Requirement: [v1-spec FR-36](../v1-spec.md#model-request-recovery-amendment-user-approved-2026-10-05).

## Context

A model request that fails for a transient reason ends the whole run today.
Observed 2026-10-05 on `opencode-go` / `glm-5.3-flash`: after 3m 55s of
streamed thinking, the provider closed the connection mid-sentence. No
`finish_reason` or error event arrived, and Likha showed
`model stream ended before [DONE] or exceeded response limit`. The run
stopped after 12m 30s and 7 tools. The interrupted reasoning was 10.8 KB, far
below Likha's 16 MiB stream cap, so the message blamed a limit that was never
hit. The user had to notice, read the error, and type "continue".

Every provider shares the same agent and the same two wire paths: OpenAI-
compatible chat completions for API-key providers, and the Responses wire for
ChatGPT sign-in. So recovery is one behavior across all providers, not a
per-provider patch.

Retrying is safe from side effects: Likha runs no tool from a partial
response. A tool call executes only after the provider has delivered a
complete, validated response. Resending the same request therefore cannot
repeat an action. The costs are time and provider usage.

Prior art is recorded in [research.md](research.md). Codex CLI and OpenCode
retry the whole request with capped exponential backoff, about five tries.
Claude Code retries only before the response starts. OMP retries a mid-stream
drop once.

## Role

Make transient provider failures recover on their own without hiding them.
Never retry a failure that would fail the same way again, and never spend
unbounded time or usage. When recovery gives up, say why in words a user can
act on, and offer a one-command way to resume.

## Scope

In scope: every streamed model request on every provider, namely the main
turn, conversation compaction, explore children, and profile tasks. Session
auto-naming stays a single best-effort attempt.

Out of scope:

- Continuing from the partial output, e.g. Anthropic's "your previous
  response was interrupted" recovery. It is not portable across OpenAI-
  compatible providers, and tool-call and thinking blocks cannot be partially
  recovered. A retry always starts the response over.
- Switching to another model or provider on failure (OMP's fallback chains).
- User-tunable retry settings. Limits are fixed (decision 7).
- Truncation by a provider's output-token limit (`finish_reason: "length"`).
  That is a complete response, not a transport failure. It is noted in
  [context.md](context.md) as a separate gap.

## Decisions (user interview, 2026-10-05)

1. **Mid-stream drops retry automatically.** A drop after output has already
   streamed is retried without asking, the same as a failure before output.
2. **Partial output stays visible, marked interrupted.** Reasoning or text
   from the dropped attempt remains in the transcript, labelled as an
   interrupted attempt. The new attempt streams below it. Partial output is
   never sent to the model.
3. **Budget: at most 5 attempts per request.** Waits between attempts are
   about 1s, 2s, 4s and 8s, with ±25% jitter, and each wait is capped at 30s.
4. **Mid-stream drops get a smaller budget.** At most 2 retries (3 attempts)
   follow a drop after output, because each one re-runs the whole
   generation, and a gateway that cuts long requests at a fixed point cuts
   the retry at the same point.
5. **A provider's stated wait is honored up to 60s.** If the provider says to
   wait longer, Likha fails right away and shows when to try again.
6. **Stall timeout: 5 minutes.** A request that receives no data for 300s
   counts as dropped and follows the same retry rules.
7. **Fixed defaults.** No new configuration.
8. **`/retry` resends the failed request** after recovery gives up or a
   failure is not retryable.
9. **Callers:** compaction, explore children, and profile tasks recover too.
   A child's retries count against its existing request and time budgets.
   Session auto-naming makes one silent attempt.

## Functional changes

### What is retried

1. A request is retried when it fails for a reason that may not repeat:
   - the connection could not be made, or was reset or closed before the
     response finished;
   - the request stalled (decision 6);
   - the provider answered rate-limited (HTTP 429), timed out (408, 504),
     overloaded or unavailable (500, 502, 503, 529, and gateway 520–524);
   - the provider reported a server, overload, or rate-limit error inside the
     stream.
2. A request is never retried when retrying cannot help:
   - authentication or permission failures (401, 403). The existing one-time
     ChatGPT token refresh still applies;
   - a rejected request (400, 404, 413, 422), including context-window
     overflow;
   - a usage, quota, credit, or billing limit, even when sent as HTTP 429.
     For the ChatGPT provider, the documented plan-limit code
     (`subscription_sharing_usage_limit_exceeded`) pauses plan requests and
     points at [ChatGPT Usage](https://chatgpt.com/settings/usage) without
     inferring a reset time, retrying, switching accounts, or falling back to
     API-key billing (specs/chatgpt-plus);
   - a malformed or protocol-violating response, such as invalid JSON or
     tool-call fragments;
   - a response larger than Likha's 16 MiB stream limit;
   - TLS certificate failures;
   - cancellation by the user.

### During recovery

3. Before each retry, the conversation shows one recovery row naming the
   cause, the attempt about to start, the attempt limit, and the wait. The
   wait counts down. The row is legible without color or Nerd Font glyphs.
   Example:
   `↻ opencode-go closed the stream mid-response — retrying 2/3 in 2s · esc to stop`.
4. If the failed attempt had streamed reasoning or text, that block closes
   and is labelled interrupted (decision 2). Content from different attempts
   is never merged into one block.
5. The status line shows that Likha is retrying, not "Waiting for model".
6. Esc during a wait or a retried attempt cancels the run as it does today.
   The run ends as cancelled, not as an error, and no further attempt starts.
7. A retry resends exactly the failed request. Steering prompts queued during
   recovery wait for the next safe point (FR-21). They do not join the
   retried request.
8. A success after one or more retries continues the run normally. The turn
   footer counts the retries, e.g. `· 1 retry`.

### When recovery gives up

9. The final error names the provider, the cause in plain words, and the
   number of attempts made, and it suggests `/retry`. The cause wording
   distinguishes these cases:
   - closed the connection before responding;
   - closed the stream mid-response;
   - sent no data for 5 minutes;
   - rate limited, with the time to retry when the provider gave one;
   - overloaded or unavailable (with the HTTP status);
   - reported an error (with its message, bounded);
   - response exceeded Likha's 16 MiB limit;
   - could not be reached (with the network reason).

   Likha no longer says "ended before [DONE] or exceeded response limit"
   without knowing which one happened.
10. Partial output of the last failed attempt stays visible, marked
    interrupted. Tool results and edits from earlier rounds in the same run
    remain in the session, as today.

### `/retry`

11. `/retry` is a reserved command. When the session's most recent prompt
    turn ended in an error (not a cancellation), it starts a new run that
    resends that turn's last request. The conversation, including earlier
    tool results, is sent as it stood, with no new user message. It works
    after resuming a session. A failed `/compact` is retried by running
    `/compact` again.
12. `/retry` never reruns a tool, re-applies an edit, or reuses an approval.
    It sends only a model request, and the model decides what to do next.
13. Otherwise `/retry` prints a visible notice ("Nothing to retry") and
    starts no run: when the last run succeeded or was cancelled, when no run
    has happened, or while a run is active (FR-21's refusal applies).

### Callers other than the main turn

14. Compaction and explore children recover under the same rules. Each child
    attempt counts as one of the child's 32 model requests. Backoff waits
    count toward the child's five-minute limit. A child that runs out of
    budget during recovery returns its usual limited or failed result with
    the cause. Recovery rows for a child appear in that child's inspection
    view (`/agents`), not in the main conversation.
15. Session auto-naming makes one attempt and fails silently, as today.

### Usage honesty

16. Usage and spend count only what providers report. A failed attempt
    usually reports no usage, and the provider may still bill it. The docs say
    so, and Likha never claims a retry was free.

## Acceptance criteria

- A stream closed mid-response is retried; the interrupted block and the
  recovery row are visible; the run then completes normally.
- A 503 followed by success completes the run with one retry and no error
  entry.
- Retry-After of 10s is honored; Retry-After of 120s fails at once with the
  provider's reset time.
- A third mid-stream drop ends the run with an error naming the drop and "3
  attempts"; five pre-output failures end it with "5 attempts".
- A request with no data for 300s is retried (verified with a shortened
  timeout in tests and the real value documented).
- 401, 400, quota-429, malformed stream, and 16 MiB overflow fail on the first
  attempt with distinct messages.
- Esc during a backoff wait ends the run as cancelled within one second.
- `/retry` after a failed run resends the request without rerunning tools;
  `/retry` after success or cancellation prints "Nothing to retry".
- An explore child's retries are visible in `/agents` and count against its
  32-request budget.
- Session auto-naming makes exactly one request.
- Behavior is identical on the chat-completions and ChatGPT (Responses) wire
  paths.
