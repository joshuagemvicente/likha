# Feature: Claude (Anthropic) predefined provider

**Status:** spec'd, not implemented.

## Context

Likha's predefined provider list (`internal/model/provider.go`, `Providers`) is
the only thing between a user key and a session: the setup picker, the
`/providers` dialog, the `/models` fan-out, `ResolveProvider`, and `--help` all
iterate the table. Anthropic's official OpenAI SDK compatibility layer
([OpenAI SDK compatibility](https://platform.claude.com/docs/en/cli-sdks-libraries/libraries/openai-sdk))
serves an OpenAI-shaped chat-completions wire at `https://api.anthropic.com/v1`,
so a Claude endpoint fits Likha's single-protocol architecture as a table row —
the same shape as the `groq`/`xai`/`together`/`mistral`/`cerebras` rows. The
native `/v1/messages` wire and Claude subscription OAuth are explicitly out of
scope: both would be a new client surface, which v1 does not include.

## Resolved decisions

1. **One table row, no client changes.** Append after `cerebras`:
   `{Name: "claude", DisplayName: "Anthropic Claude", BaseURL: "https://api.anthropic.com/v1", KeyEnv: "LIKHA_CLAUDE_API_KEY", DefaultModel: "", Hosted: true}`.
   No `SessionHeader`, zero `Auth` value (static Bearer key).
2. **`DefaultModel` stays empty until a live probe names a model.** The
   documented examples (e.g. `claude-opus-5-5`) are evidence of the route, not
   of which model the probe passed with — the repo doctrine forbids model ids
   from memory. Until then `resolveModel` requires `--model`/`LIKHA_MODEL`
   (the dialagram precedent, v1-spec §3).
3. **No provider-specific UI or client fork.** Setup, `/providers`, `/models`,
   and `--help` surface the row automatically; `internal/model/codex.go`-style
   protocol work is not needed for the compat layer.
4. **pending-probe status.** The row ships marked pending-probe and is never
   described as accepted before the full admission gate passes
   (`specs/predefined-providers/`).
5. **Out of scope, separately specified:** native `/v1/messages` (Anthropic
   Messages wire), Claude subscription OAuth (Claude Code-style login), prompt
   caching (`anthropic-beta` headers; the compat layer documents caching as
   unsupported), workspace header `anthropic-workspace-id` (only needed for
   multi-workspace personal/service keys; revisit only if a probe fails on
   key type).

## Compatibility evidence (route level)

From the [compatibility documentation](https://platform.claude.com/docs/en/cli-sdks-libraries/libraries/openai-sdk)
(fetched 2026-10-04), against Likha's actual request surface
(`internal/model/client.go` `Stream`):

| Likha sends | Anthropic compat layer |
| --- | --- |
| `POST {base}/chat/completions` | Fully supported |
| Bearer `Authorization` | Fully supported on compat routes |
| `stream: true` + `stream_options.include_usage` | Fully supported |
| `tools[n]` `function` name/description/parameters (`type: "function"`) | Fully supported |
| assistant `tool_calls` + `tool` role with `tool_call_id` | Fully supported |
| `system`/`developer` roles mid-conversation | Supported but hoisted to a single initial system message (benign behavior note, same class as the Cerebras compaction caveat) |
| User-Agent: `likha/<version>` | No restriction (header table does not constrain it) |
| `temperature`, `n`, penalties... | Not sent by Likha; `n` would have to be 1, `temperature` is capped — irrelevant to Likha's body |
| `strict` tool schema flag | Ignored by Anthropic; Likha does not send it |

Model-list route (`GET {base}/models` — used by Likha's `Check()` on
connection and `/models`): **open question, must be probed.** The docs' header
table marks `authorization` fully supported, but a live check on 2026-10-04
showed unauthenticated `GET /v1/models` → `x-api-key header is required`, and a
request carrying an invalid Bearer key → `invalid x-api-key` — Bearer
credentials are consumed on the route, but whether a *valid* Bearer key yields
the OpenAI `data[].id` envelope is not established by documentation. If a valid
key is rejected with the same error, the row cannot ship unchanged: the remedy
is a specified, minimal client branch (send `x-api-key` alongside Bearer for
this base) decided in a follow-up — not a silent fork.

Likha's existing Claude metadata needs no changes: `windows.go`,
`pricing.go`, and `naming.go` already carry documented entries and `claude-`
prefix fallbacks for context window (200K), pricing, and display names.
*(Superseded for windows and pricing by
[model-metadata](../model-metadata/spec.md), user-approved 2026-10-04: Claude
rows come from the bundled catalog; current models are 1M context.)*

## User-visible behavior

1. `--provider claude` resolves to `https://api.anthropic.com/v1`, reads
   `LIKHA_CLAUDE_API_KEY` in the existing key order (`--api-key` →
   `LIKHA_API_KEY` → provider env → stored `providers.json`), and requires
   `--model`/`LIKHA_MODEL` until a probe sets a default.
2. The row appears in first-run setup (API-key paste stage), `/providers`
   (configured/not-configured marker), `/models` (when a stored key exists),
   and generated `--help`.
3. Until its probe passes, README and v1-spec describe the row as
   pending-probe, never accepted.

## Acceptance criteria

- [ ] `--provider claude` resolves through the standard API-key storage path;
      the existing `TestResolveProviderSelectsEndpointsAndKeys` loop covers it
      without a new test.
- [ ] The row appears in setup, `/providers`, `/models`, and `--help` without
      provider-specific UI code.
- [ ] A Bearer-authenticated `GET https://api.anthropic.com/v1/models` with a
      valid key decodes model IDs through the shared decoder, **or** a
      specified minimal client change is tested and documented first.
- [ ] Full live admission probe recorded in
      `specs/predefined-providers/context.md` before the row is described as
      accepted: model list, streamed text, structured tool call and
      tool-result roundtrip, mid-stream cancellation, revoked-key error.
- [ ] `DefaultModel` is set only from a passing probe result, never from docs.
- [ ] README, v1-spec, and CHANGELOG carry the row with pending-probe status
      until then.
- [ ] `go test ./...` passes from the project root; no mock-only test claims
      the provider works.
