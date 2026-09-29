# Tasks: Predefined accepted providers (BYOK)

**Status:** tasks 1–4 implemented (local); task 5 documentation written, hosted-provider probes outstanding — no checkmark below is final until `go test ./...` and the smoke scenarios pass on a clean run.

Ordered slices. Each task ends with user-visible behavior plus a test through the real
module interface. Automated entry point: `go test ./...` from the project root.

1. **Provider configuration schema.** **Done.** `internal/model/provider.go` holds the
   predefined list (openai, openrouter, bedrock, dialagram, opencode-zen, opencode-go);
   `internal/app/provider.go`
   resolves provider/endpoint/key; `internal/app/keyfile.go` stores keys as
   `<stateDir>/providers.json` mode 0600. `--api-key` (explicitly passed) persists;
   `LISA_API_KEY` feeds the flag default. `resolveProvider` tests cover resolution order,
   storage, and permissions.
2. **Connection check (startup and pre-run).** **Done.** `model.Client.Check` probes
   `GET /models` read-only; `EnsureConnected` memoizes success, retries failures;
   `run.go` runs it at startup (result shown in the TUI, not fatal); `agent.go` runs it
   before each agent run. Failure classes: `ErrUnreachable`, `ErrUnauthorized`,
   `ErrUnexpectedResponse`.
3. **Provider-aware model client.** **Done.** One OpenAI-compatible surface; provider
   settings are table rows; `New(endpoint, name, key)` accepts loopback HTTP `/v1` or
   HTTPS base URLs, sends `Authorization: Bearer` when a key exists, and keeps the
   no-redirect rule. Old loopback-only gate removed.
4. **Unlisted-endpoint warning.** **Done.** TUI header shows
   `Provider: <name> (accepted)` or `(unverified)`; startup failure shows `Not connected`
   with a classified error entry.
5. **Docs and release notes.** **Partially done.** README provider table, key handling,
   examples, and privacy wording updated; CHANGELOG entry added; v1-spec/setup-plan/
   feature-test-plan already amended. **Outstanding:** live probe evidence per hosted
   provider (needs real API keys) before the README rows may claim accepted status.

Follow-up (same feature, added 2026-09-29): **model name is optional.** Without
`--model`/`LISA_MODEL`, `resolveModel` (run.go) uses the provider's documented
`DefaultModel` (table row; OpenAI `gpt-4o-mini`, OpenRouter `openai/gpt-4o-mini`, Bedrock
haiku, Opencode Zen `gpt-5.3-codex`, Opencode Go `glm-5.3-flash`), or — for a custom
`--endpoint` — the first model the endpoint reports. `--help` prints the full option/provider/env
reference. Verified by `TestResolveModel*` tests, uncached integration tests, and a pty
TUI smoke that auto-picked `smoke-model` and streamed a turn.
