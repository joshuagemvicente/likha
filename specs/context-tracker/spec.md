# Feature: Model context tracker

**Status:** in progress — implementation and automated tests complete; real-terminal walkthrough pending.

## Context

Likha already has a context segment in its status bar, but it can show `ctx —`
even when the user has a useful estimate available. Today the segment depends
on a provider-reported prompt-token count and a model ID present in Likha's
documented context-window catalog. It does not estimate the outbound request,
and the displayed value can therefore be unavailable or lag the current
conversation.

This feature makes that segment a trustworthy view of the active conversation's
input context. It refines the context portion of [TUI layout](../tui-layout/spec.md)
and FR-23 in [v1-spec.md](../v1-spec.md). It does not manage or preserve context.

## Resolved behavior

- **The tracked quantity is input context.** It is the input-token count for
  the active conversation's most recent model request, not generated output,
  cumulative session tokens, or spend. When the provider reports cached input
  separately, cached input is included only if it is not already part of the
  reported total.
- **Use measured usage when available.** After a request completes, the
  provider-reported input count replaces the estimate for that request. If the
  provider omits usage, Likha uses the estimate for that same request; it never
  carries an older measured count forward as though it described the current
  prompt.
- **Estimate the actual outbound input before each request.** The estimate
  covers the messages and tool definitions Likha sends, including the current
  prompt, steering prompts, and tool results already added to the conversation.
  Use a model-specific tokenizer when Likha supports one; otherwise use a local
  approximation. Every estimate is visibly marked with `~`.
- **Resolve the window without guessing.** A positive user override for the
  selected provider/model takes precedence, followed by provider/model metadata,
  then Likha's documented model catalog. Unknown, invalid, or absent limits stay
  unknown; Likha never substitutes a generic default.
- **Configure an override in private state.** Advanced users set a positive
  token count in the nested `context_windows` map in Likha's private
  `config.json`, keyed first by provider ID and then model ID. For example:
  `"context_windows": {"openai": {"gpt-4.1": 1000000}}`. No TUI editor is
  added. Invalid entries are not used as a limit.
- **Show partial facts honestly.** With a known limit, show used/limit and the
  percentage. Without a known limit, show the count and `?` instead of a
  percentage. For example, `ctx 68k/200k · 34%`, `ctx ~42k/200k · ~21%`, or
  `ctx ~42k/?`. If neither a measured count nor an estimate can be produced,
  show `ctx —`.
- **Keep the status bar legible at narrow widths.** Preserve the count,
  approximation marker, and unknown-limit marker; omit the percentage before
  hiding or truncating those facts. Keep the existing warning treatment at
  80% and above when the limit is known.
- **Refresh at the points that change active context.** Recalculate before
  every active-conversation model request and after a successful compaction or
  session resume. When compaction replaces history, the tracker returns to the
  compacted conversation's estimate rather than continuing to show usage for
  the larger summarize request. The unsent composer is not counted.
- **Missing telemetry is not a request failure.** Providers that do not return
  usage continue to work; the estimate remains visible. A usage-parsing or
  estimation limitation must not turn an otherwise successful model response
  into an error.
- **Recompute on resume.** The tracker is not durable session data. On resume,
  Likha estimates from the restored active conversation and replaces that value
  with measured usage after the next completed request, if available.

## Scope boundaries

- This is a display and accounting feature, not automatic compaction, a token
  budget, or a guarantee that a request will fit a provider's limits.
- It does not change which conversation messages Likha sends, model output
  limits, provider selection, or session persistence semantics.
- It does not estimate unsent composer text or accumulate billing/session
  token totals into the context count.
- The `~` marker communicates uncertainty: provider-side hidden instructions,
  tokenizer differences, and multimodal tokenization can make a local estimate
  differ from the provider's actual accounting.

## Acceptance criteria

- [ ] Before each active-conversation model request, the status segment updates
      from that request's input, including tool definitions and the current
      accumulated conversation; a prior request's measurement is not presented
      as current.
- [ ] When a completed response reports input usage, the tracker shows that
      measured count; when usage is absent, it retains the visibly approximate
      estimate for that request without failing the response.
- [ ] Usage-only stream events and usage attached to a final event with choices
      are both handled when supported by the provider protocol; absent or
      malformed optional usage does not fail an otherwise valid response.
- [ ] Model-specific tokenization is used where supported and a local fallback
      is marked approximate. The estimate includes the prompt messages and tool
      definitions Likha sends; it does not count generated output as input.
- [ ] A configured per-provider/model limit override wins over provider
      metadata; metadata wins over the documented catalog. The override is
      stored in private `config.json` under `context_windows`, keyed by
      provider ID and model ID. Unknown limits show `?`, never a fabricated
      percentage. Non-positive overrides and metadata are ignored or rejected
      visibly rather than used as a denominator.
- [ ] Measured and estimated counts render as used/limit with percentage when
      possible; estimates retain `~`; unknown limits retain `?`; `ctx —` is
      reserved for when no usable count is available.
- [ ] At narrow widths, the status segment stays within the layout and does not
      silently remove the estimate marker or imply an unknown limit is known.
- [ ] The existing 80% warning styling applies to measured and estimated
      percentages when a valid window is known.
- [ ] After compaction, the visible count reflects the replacement history;
      after resume, it is recomputed from restored history. Neither operation
      requires a schema migration or replays a previous request's count as
      current.
- [ ] Usage/estimation gaps do not prevent a successful model response, and
      `go test ./...` passes from the project root with tracker coverage.
