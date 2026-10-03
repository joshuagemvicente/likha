# Context: Predefined accepted providers (BYOK)

Code and docs this feature touches.

## Code

| Path | Relevance |
| --- | --- |
| `internal/app/run.go` | Flag/env parsing (`--endpoint`, `--model`, `LIKHA_*`); provider config extends here. |
| `internal/model/client.go` | The one OpenAI-compatible surface; provider list must not fork it. |
| `internal/app/tui.go` | Connection status line, unverified warning, provider identity display. |
| `internal/app/agent.go` | Pre-run connection check hook before dispatching tools. |
| `internal/session/` | Untouched — schema and resume ignore the configured model (confirmed decision). |
| `tests/integration/cli_test.go` | Launch-level coverage for status lines and warnings. |

## Existing spec state

- v1-spec.md currently says local-only, no paid service, no provider-specific integrations
  (§2, §3, §5). This feature **amends** those lines; see spec.md "Functional changes".
- setup-plan.md Feature 5 owns "compatible local model setup" documentation — the
  published provider list belongs there too.
- feature-test-plan.md Feature 1 tracks real-model compatibility; hosted-provider probe
  results append there.

## Provider evidence (as of writing)

- OpenRouter: OpenAI-compatible `/api/v1`, Bearer key.
  https://openrouter.ai/docs/quickstart
- Amazon Bedrock: OpenAI-compatible ChatCompletions on `bedrock-runtime`/`bedrock-mantle`;
  API-key bearer or AWS SigV4.
  https://docs.aws.amazon.com/bedrock/latest/userguide/models-api-compatibility.html
  https://docs.aws.amazon.com/bedrock/latest/userguide/endpoints.html
- Dialagram (Nexum Router): OpenAI-compatible at `/router/v1`.
  https://dialagram.me
- Opencode Zen: OpenAI-compatible at `https://opencode.ai/zen/v1`; `GET /v1/models`
  returns an OpenAI-shaped list (claude-fable-5, claude-opus-5-5, gpt-5.3-codex, …).
- Opencode Go: OpenAI-compatible at `https://opencode.ai/zen/go/v1`; `GET /go/v1/models`
  returns an OpenAI-shaped list (minimax-m3, kimi-k3, glm-5.3-flash, …).
  Both `/models` routes answer publicly (no auth) with `{"object":"list","data":[{"id":…}]}`;
  chat completions and tool calls still need a live-key probe.
- Groq: first-party docs specify `https://api.groq.com/openai/v1`, Bearer API-key auth,
  `POST /chat/completions`, and `GET /models` with `data[].id`.
  https://console.groq.com/docs/openai
  https://console.groq.com/docs/models
- xAI: first-party docs specify `https://api.x.ai/v1`, Bearer API-key auth,
  `POST /chat/completions`, and `GET /models` with `data[].id`; Chat Completions is
  documented as legacy. https://docs.x.ai/developers/rest-api-reference/inference
- Together AI: first-party docs use `https://api.together.ai/v1` (not the older `.xyz`
  candidate URL) and document Bearer auth plus both routes. The compatibility page and
  model API response examples disagree on whether the list is `data[]` or a bare array;
  the shared parser accepts either, but the live shape remains pending-probe.
  https://docs.together.ai/docs/inference/openai-compatibility
  https://docs.together.ai/reference/models
- Mistral AI: first-party docs specify `https://api.mistral.ai/v1`, Bearer auth,
  `POST /chat/completions`, and `GET /models`; the schema says `data[]`, while the
  published example is a bare array. The shared parser accepts either; live shape and
  full behavior remain pending-probe. https://docs.mistral.ai/api/endpoint/models
- Cerebras: first-party docs specify `https://api.cerebras.ai/v1`, Bearer auth,
  `POST /chat/completions`, and `GET /models` with `data[].id`. The documented
  developer-role limitation means compaction behavior needs a suitable-model probe.
  https://inference-docs.cerebras.ai/resources/openai
  https://inference-docs.cerebras.ai/api-reference/models/list-models
- OpenCode Go requires a stable per-conversation session ID in `x-opencode-session`
  (observed live: chat completions return HTTP 400 `MissingSessionID` without it) and an
  identifying User-Agent rather than a generic HTTP-library name.
  https://opencode.ai/docs/go/#where-can-i-use-it
  Likha sends its own SQLite session ID in that header for both opencode rows and sets
  `User-Agent: likha/<version>` on every provider request.
- **Live probe result (2026-09-29, user-run):** `opencode-go` completed a real session
  with the user's API key through the TUI setup flow — connection check, model picker,
  streamed responses, and agent tool usage all worked after the session-header fix.
  `opencode-go` is **accepted**. `opencode-zen` uses the same surface and session header
  but its `/zen/v1` route has not had a live-key turn yet (still pending).
- Groq, xAI, Together AI, Mistral AI, and Cerebras are wired as pending-probe providers;
  no credentialed compatibility probes have been run for them. Documentation evidence
  establishes route intent only, not acceptance.
- Anthropic Claude (`claude`) is wired as a pending-probe provider against the OpenAI SDK
  compatibility layer (`https://api.anthropic.com/v1`, Bearer key, slot
  `LIKHA_CLAUDE_API_KEY`, blank default). Route checks on 2026-10-04 without a valid
  key: unauthenticated `GET /v1/models` → `x-api-key header is required`; invalid Bearer
  key → `invalid x-api-key`. No credentialed probe has run: the first gate (valid-key
  Bearer `GET /v1/models` decoding `data[].id`) and the full admission probe remain
  outstanding. https://platform.claude.com/docs/en/cli-sdks-libraries/libraries/openai-sdk
- Top agent TUIs defer failures to prompt time and validate on first use rather than
  preflight; Likha's startup + pre-run checks are deliberately stricter.
  https://opencode.ai/docs/troubleshooting/

## Open items carried forward

- AWS SigV4 auth for Bedrock (non-bearer) is a task-3 consideration if a user cannot use
  a Bedrock API key; defer unless the probe requires it.
- Dialagram's Claude-Code-style `/router/claude` endpoint is out of scope until a
  non-OpenAI adapter is specified.
