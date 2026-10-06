# Research: how coding agents recover from model request failures

Fetched 2026-10-05 from primary sources (official docs and source code).
Claims that could not be confirmed from a primary source are marked
**unverified**.

## OpenAI Codex CLI

Source: `codex-rs/model-provider-info/src/lib.rs`,
`codex-rs/codex-client/src/retry.rs`, `codex-rs/core/src/session/turn.rs`
(github.com/openai/codex, `main`).

- Each setting is per provider (`[model_providers.*]` in `config.toml`):
  - `request_max_retries` = 4: retries the initial HTTP request.
  - `stream_max_retries` = 5: reconnects a dropped stream.
  - `stream_idle_timeout_ms` = 300000 (5 min): treats a silent stream as lost.
  - The two retry counts are hard-capped at 100.
- Backoff is `base * 2^(attempt-1)` with ×0.9–1.1 jitter; the base value is
  **unverified**. When a Retry-After is present, it replaces the computed
  backoff.
- The policy flags are `retry_429`, `retry_5xx` and `retry_transport`
  (timeouts, connect and network errors). Build failures, policy violations
  and oversized responses are not retried.
- The turn loop re-sends the original input on each stream retry. It returns
  without retrying on context-window-exceeded, usage-limit-reached and
  aborted turns.
- UI, seen in user issues and not confirmed in source:
  `Reconnecting... 4/5 (21s • esc to interrupt)`, then
  `stream disconnected before completion: …`
  (github.com/openai/codex/issues/19121, /10378).

## Claude Code

Sources: code.claude.com/docs/en/errors and code.claude.com/docs/en/env-vars.

- `CLAUDE_CODE_MAX_RETRIES` defaults to 10, with a maximum of 15.
- `CLAUDE_CODE_RETRY_WATCHDOG=1` is meant for unattended runs. It retries 429
  and 529 indefinitely, with waits of up to 5 minutes.
- Retried: 529, 5xx, timeouts, dropped connections, stalls before the
  response starts, temporary 429 throttles, and expired credentials.
- Not retried: TLS failures, policy denials, content-filter blocks, and
  **failures mid-response**. In those cases the partial output stays, labelled
  "The response above may be incomplete", and the user sends `continue`.
- The UI shows `Retrying in Ns · attempt x/y`.
- Timeouts include a first-byte deadline, an event-level idle watchdog and a
  byte-level idle watchdog (clamped to 10s–30min). A 5-minute body idle
  timeout is available through `API_FORCE_IDLE_TIMEOUT`.
- The Anthropic SDK itself retries twice by default and honors `retry-after`
  (platform.claude.com/docs/en/api/errors).

## OpenCode

Source: `packages/opencode/src/session/retry.ts` (github.com/sst/opencode, `dev`).

- Initial delay 2000 ms, backoff factor 2, jitter 0.25, at most 5 retries.
- Without a provider header, a wait is capped at 30s.
- Retry-After handling checks `retry-after-ms` first, then `retry-after`
  (seconds or HTTP date), then falls back to backoff.
- Retryable: 429, 500, 502, 503, 504, 524, "rate limit", "overloaded",
  connection errors, timeouts, ECONNRESET, and response-stream errors.
  Context overflow is not retried.
- Free-tier and usage-limit errors show an actionable message instead of
  retrying.

## OMP (oh-my-pi)

Sources: github.com/can1357/oh-my-pi `docs/settings.md` and PR #13747.

- Default settings: `retry.maxRetries` 10, `retry.baseDelayMs` 500,
  `retry.maxDelayMs` 300000.
- If the provider asks for a wait longer than `maxDelayMs`, OMP fails fast
  unless `retry.waitForUsageReset` is set.
- A **mid-stream drop after streamed reasoning or tool calls** is retried on
  the same model **once**, then OMP moves to the fallback chain. A drop
  before any content falls back immediately.
- The failed partial output is dropped from context, so the retry re-plans
  from scratch.

## Wire formats for mid-stream errors

- **OpenRouter** (openrouter.ai/docs/api/reference/errors-and-debugging): the
  HTTP status stays 200. The final SSE chunk carries
  `error: {code, message, metadata}` and `finish_reason: "error"`, and then
  the stream ends.
- **Anthropic native**: `event: error` with
  `{"type":"error","error":{"type":"overloaded_error"}}`, the streaming
  equivalent of HTTP 529.
- **Anthropic's OpenAI-compatible endpoint**: errors are OpenAI-shaped and
  `retry-after` is supported. HTTP codes are 429 `rate_limit_error`, 500
  `api_error`, 504 `timeout_error` and 529 `overloaded_error`. The exact shape
  of a mid-stream error on this endpoint is **unverified**.
- **OpenAI Responses**: `response.failed` carries `error.code`, for example
  `server_error` or `rate_limit_exceeded`, and there is also a separate
  top-level `error` event. OpenAI's error-codes guide marks 429, 500 and 503
  as retryable: wait at least Retry-After, otherwise use exponential backoff
  with jitter and a limited number of retries. That `server_error` and
  `rate_limit_exceeded` inside `response.failed` are retryable is
  **unverified** from a primary source. Quota and billing 429s are not worth
  retrying.

## Billing and continuation

- No primary source states how a dropped stream is billed, or whether the
  input is billed again on a retry. Treat both as **unverified** and do not
  claim a retry is free.
- Anthropic prompt caching makes an entry available only after the first
  response begins, so caching helps a retry only if the first attempt had
  started responding.
- Anthropic documents continuation after an interruption: put the partial
  text in an assistant message (≤ Claude 4.5), or add a user message saying
  the previous response was interrupted (≥ 4.6). Tool-use and thinking blocks
  cannot be partially recovered. Likha rejects continuation for this feature
  because it is not portable across OpenAI-compatible providers
  ([spec.md](spec.md) out of scope).

## What this means for Likha

- Retrying the whole request with capped exponential backoff and about five
  tries is the common pattern (Codex, OpenCode). Likha follows it.
- Practice on mid-stream drops is split: Claude Code doesn't retry them, OMP
  retries once, and Codex retries up to 5. Likha takes a middle ground of 2
  retries (spec decision 4).
- A 5-minute stall timeout matches both Codex and Claude Code.
- Usage-limit and quota 429s must fail fast; every reference agent does this.
