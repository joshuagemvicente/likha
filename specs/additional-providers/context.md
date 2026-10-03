# Context: Additional predefined providers

Code paths this feature touches. No design decisions hidden in prose — all
decisions live in [spec.md](spec.md).

## Code

| Path | Relevance |
| --- | --- |
| `internal/model/provider.go` | `Providers` table; row shape (`Name`, `DisplayName`, `BaseURL`, `KeyEnv`, `DefaultModel`, `Hosted`, `SessionHeader`, `Auth`). Groq, xAI, Together, Mistral, and Cerebras rows are appended after `chatgpt`, with empty defaults and pending-probe status. |
| `internal/model/client.go` | `parseEndpoint` (empty-path HTTPS bases rejected — the DeepSeek gate), `fetchModelList` (`base + "/models"`), and `decodeModelIDs` (OpenAI `data[]` envelope or bare array). No provider-specific route fork for the added rows. |
| `internal/providers/provider.go` | `ResolveProvider`: `KeyEnv` env read, stored-key fallback, and the error naming `KeyEnv` when no key exists. |
| `internal/app/run.go` | `--help` provider list (iterates the table) and `resolveModel` (empty `DefaultModel` requires `--model`/`LIKHA_MODEL` — the dialagram precedent Wave 1 follows). |
| `internal/tui/setup.go`, `internal/tui/providers.go`, `internal/tui/models_all.go` | Setup picker, `/providers` dialog, `/models` fan-out — all iterate `model.Providers`, so new rows surface with zero UI changes. Each stored key widens the `/models` fan-out by one fetch (expected, not a bug). |
| `internal/providers/provider_test.go` | `TestResolveProviderSelectsEndpointsAndKeys` loops every non-OAuth row — new Wave 1 rows are covered without a new test. |

## Docs

- `README.md` provider table (columns: Provider, `--provider` name, API key, base URL, default model) plus the "not yet probed" note.
- `specs/v1-spec.md` §3 predefined-providers list (enumeration update, not an FR change).
- `specs/predefined-providers/context.md` provider-evidence section (probe results land here).
- `CHANGELOG.md` Unreleased entry.

## Related specs

- [v1-spec.md](../v1-spec.md) — §3 ("adding a provider is configuration"; probe gate), FR-02 (unchanged: one hosted provider from the list, BYOK).
- [predefined-providers/](../predefined-providers/spec.md) — admission probe (decision 4), key storage, connection-check timing.
- [first-run-setup/](../first-run-setup/spec.md), [slash-commands/](../slash-commands/spec.md), [all-models/](../all-models/spec.md) — surfaces that consume the table.
- [chatgpt-plus/](../chatgpt-plus/spec.md) — contrast: new rows are API-key only, zero `Auth` value.

## Evidence (primary sources)

- Groq `https://api.groq.com/openai/v1` — https://console.groq.com/docs/openai and https://console.groq.com/docs/models
- xAI `https://api.x.ai/v1` — https://docs.x.ai/developers/rest-api-reference/inference
- Together `https://api.together.ai/v1` — https://docs.together.ai/docs/inference/openai-compatibility and https://docs.together.ai/reference/models; the response examples disagree on envelope versus bare array, so the live shape is pending.
- Mistral `https://api.mistral.ai/v1` — https://docs.mistral.ai/api/endpoint/chat and https://docs.mistral.ai/api/endpoint/models; schema/example disagree on envelope versus bare array, so the live shape is pending.
- Cerebras `https://api.cerebras.ai/v1` — https://inference-docs.cerebras.ai/resources/openai and https://inference-docs.cerebras.ai/api-reference/models/list-models
- DeepSeek `https://api.deepseek.com` (no `/v1`) — https://api-docs.deepseek.com/; root-base validator and request-compatibility gates remain.
- Gemini `https://generativelanguage.googleapis.com/v1beta/openai` (beta) — https://ai.google.dev/gemini-api/docs/openai; tool-call thought-signature preservation remains.

Fireworks remains out of the provider table until its same-base `/models` route is established from a primary source or live probe; the current documented account-scoped endpoint has a different response shape.

## Open items

- Full live admission probes for the five registered pending-probe providers.
- Together and Mistral live model-list response shape; shared parser supports both documented forms.
- DeepSeek root-`/models` probe, endpoint validator tests, developer-role and reasoning/tool request compatibility.
- Gemini thought-signature-preserving structured-tool-call probe against the beta compatibility layer.
- Fireworks same-base OpenAI-shaped `GET /models` route evidence.
- Per-row `DefaultModel` values, picked from probe results — never from memory.
