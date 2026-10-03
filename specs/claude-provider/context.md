# Context: Claude (Anthropic) predefined provider

Code paths this feature touches. No design decisions hidden in prose — all
decisions live in [spec.md](spec.md).

## Code

| Path | Relevance |
| --- | --- |
| `internal/model/provider.go` | `Providers` table; the new row appends after `cerebras` with shape `Name`/`DisplayName`/`BaseURL`/`KeyEnv`/`DefaultModel`/`Hosted`, zero `Auth`, no `SessionHeader`. |
| `internal/model/client.go` | Single chat-completions surface: `Stream` builds the body (`model`, `messages`, `stream`, `stream_options.include_usage`, `tools`) and sends `Authorization: Bearer` — all documented as fully supported by the compat layer. `Check`/`ListModels` hit `GET {base}/models` and share `decodeModelIDs` (OpenAI `data[]` envelope or bare array) — the one route with an unproven Bearer-auth question. `parseEndpoint` accepts the non-root HTTPS path unchanged. |
| `internal/providers/provider.go` | `ResolveProvider` reads `LIKHA_CLAUDE_API_KEY` in the existing key order and falls back to stored `providers.json` — no changes. |
| `internal/app/run.go` | `resolveModel` requires `--model`/`LIKHA_MODEL` while `DefaultModel` is empty (dialagram precedent); `--help` iterates the table. |
| `internal/tui/setup.go`, `internal/tui/providers.go`, `internal/tui/models_all.go` | Setup picker, `/providers` dialog, `/models` fan-out iterate `model.Providers`; the row surfaces with zero UI changes. |
| `internal/providers/provider_test.go` | `TestResolveProviderSelectsEndpointsAndKeys` loops every non-OAuth row; the new row is covered without a new test. |
| `internal/model/windows.go`, `pricing.go`, `naming.go` | Existing `claude-` exact-ID entries and `claude-` prefix fallbacks (200K context, Anthropic-docs pricing, "Claude" display names) already render Claude models — metadata needs no changes. |

## Docs

- `README.md` provider table (pending-probe status) and the key-resolution
  paragraph.
- `specs/v1-spec.md` §3 predefined-providers enumeration.
- `specs/predefined-providers/context.md` — admission-probe evidence lands
  here.
- `specs/additional-providers/provider-candidates.md` — the candidates
  matrix, updated with the Anthropic/compat-layer findings.
- `CHANGELOG.md` Unreleased entry.

## Related specs

- [v1-spec.md](../v1-spec.md) — §3 ("adding a provider is configuration";
  probe gate); a native `/v1/messages` wire would amend it, not this feature.
- [predefined-providers/](../predefined-providers/spec.md) — admission gate
  (decision 4), key storage, connection-check timing.
- [additional-providers/](../additional-providers/spec.md) — the wave this
  row joins; same table-row discipline and pending-probe doctrine.
- [chatgpt-plus/](../chatgpt-plus/spec.md) — contrast: OAuth provider vs. this
  row's plain BYOK key (the Claude-subscription OAuth path, if ever wanted,
  would follow that shape — out of scope here).

## Evidence (primary sources)

- OpenAI SDK compatibility layer: https://platform.claude.com/docs/en/cli-sdks-libraries/libraries/openai-sdk
  (fetched 2026-10-04) — base `https://api.anthropic.com/v1/`; chat
  completions, streaming, `stream_options`, `tools[n].function`, assistant
  `tool_calls`, and `tool` role fully supported; system/developer hoisting as
  a benign behavior note; `strict` ignored (Likha never sends it); prompt
  caching unsupported (irrelevant: Likha sends no caching headers).
- Live route checks (2026-10-04, no valid key — hence the mandatory probe):
  unauthenticated `GET /v1/models` → `x-api-key header is required`; invalid
  Bearer key → `invalid x-api-key`. Bearer credentials are consumed on the
  route, but a valid-key `data[].id` response is not established by
  documentation alone.

## Open items

- Bearer-authenticated `GET /v1/models` shape and behavior (first gate,
  tasks.md step 2).
- Full admission probe before "accepted" (tasks.md step 3).
- `DefaultModel` value from probe results — never from memory.
- If ever needed later, separately specified: native `/v1/messages` wire,
  Claude subscription OAuth, `anthropic-workspace-id` header support,
  prompt caching via `anthropic-beta`.
