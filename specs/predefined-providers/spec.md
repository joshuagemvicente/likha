# Feature: Predefined accepted providers (BYOK)

**Status:** implemented (local) — `opencode-go` live-verified; other hosted providers pending probes. *(2026-09-29 update: the local-provider fallback described below was removed — see CHANGELOG.md's provider-only pivot. This spec records the feature as first designed.)*

## Context

Likha's v1 contract was local-first: one OpenAI-compatible local endpoint, no paid service,
no provider-specific integrations. The product direction has shifted: Likha ships with a
**predefined list of accepted providers** so a user can bring their own key (BYOK) and use
hosted APIs the same way top agent harnesses do — user enters an API key, Likha verifies the
connection, and the session starts. (The offline fallback was since removed; Likha hosts no
models and bundles no local inference server.)

This is a scope change to v1-spec.md, not just an added section: §2 (in-scope), §3
(model integration row), and §5 (privacy) currently exclude hosted/paid providers. The
wording updates below keep one protocol surface for v1 and defer provider-specific
adapters.

## Role & stance

Act as the spec author and implementer. Be evidence-first: a provider is only "accepted"
when its behavior is demonstrated, not because its marketing says OpenAI-compatible.
Keep language consistent with v1-spec.md — verifiable sentences, no unbounded claims.

## Resolved decisions (user-confirmed)

1. **Providers are predefined by Likha, not auto-discovered.** Likha is the one defining the
   accepted list; users add only an API key. Initial list: **Dialagram (Nexum Router),
   OpenRouter, Amazon Bedrock**.
2. **Protocol surface:** OpenAI-compatible first, adapters later. All three providers ship
   an OpenAI-compatible surface:
   - OpenRouter: native OpenAI-compatible `/api/v1`, Bearer key. (Source:
     https://openrouter.ai/docs/quickstart)
   - Amazon Bedrock: OpenAI-compatible ChatCompletions on `bedrock-runtime` /
     `bedrock-mantle`; accepts API-key bearer auth or AWS SigV4. (Sources:
     https://docs.aws.amazon.com/bedrock/latest/userguide/models-api-compatibility.html,
     https://docs.aws.amazon.com/bedrock/latest/userguide/endpoints.html)
   - Dialagram (Nexum Router): OpenAI-compatible at `/router/v1`, Claude-Code-style
     adapter at `/router/claude`. (Source: https://dialagram.me)
   Because all three are OpenAI-compatible, v1 needs **no new protocol adapter code** —
   only configuration, auth, and connection-check wiring.
3. **Unlisted behavior:** an endpoint not on the list is **allowed with a visible warning**
   at startup; the run proceeds but the TUI and docs mark the configuration unverified.
4. **Admission probe:** full tool-call probe — streamed text, structured tool calls (not
   prose suggestions), mid-stream cancellation, tool-result fidelity. This is the gate for
   adding any new provider row to the list.
5. **Key storage:** API keys live in **private config inside the Likha state directory**
   (mode 0700, outside the repository, never in the SQLite session DB). This follows
   v1-spec.md's existing rule for private local state.
6. **Connection check timing:** a cheap, read-only call at **startup** (e.g. models list)
   and again before each **agent run**, so a revoked key surfaces without wasting a prompt.
7. **Session continuity:** unchanged. SQLite schema and resume flow ignore the configured
   model; sessions stay valid across providers.

## Functional changes (wording to apply in v1-spec.md)

- **§2 In scope:** replace "A local OpenAI-compatible model endpoint, with a documented
  working configuration" with "A predefined list of accepted model providers, local and
  hosted, configured BYOK-style: the user supplies an API key and Likha verifies the
  connection. Unlisted OpenAI-compatible endpoints are allowed with a visible
  unverified warning."
- **§2 Out of scope:** narrow "broad provider-specific integrations" to "provider-specific
  protocol adapters for non-OpenAI-compatible surfaces" so the exclusion stays coherent.
- **§3 Model integration row:** "OpenAI-compatible chat API, with a predefined accepted
  provider list (OpenRouter, Amazon Bedrock, Dialagram; local Ollama/llama.cpp remains the
  documented offline fallback). Adding a provider is configuration; adding a non-OpenAI
  protocol is a new adapter."
- **§5 Privacy:** replace "Likha requires no proprietary or paid service" with "Local
  offline use requires no proprietary or paid service; hosted providers in the predefined
  list require a user-supplied API key and receive conversation content. Likha explains
  which provider receives what." Key storage wording already matches the existing private
  local state bullet.
- **FR-02:** extend to "The user can configure one model provider from the predefined list
  (API key for hosted providers, local endpoint for offline providers). Missing
  configuration, connectivity failures, revoked keys, and unsupported capabilities produce
  clear errors. A startup connection check and a per-run check surface a dead key before
  prompts are sent."
- **§7 Model and tool contract:** add "Accepted providers are listed in the release
  documentation with the probe result used to admit them."
- **§10 Model acceptance row:** validation runs each listed provider end to end (stream,
  structured tool call, cancellation, revoked-key error) and exercises an unlisted
  endpoint to confirm the warning.

## Acceptance criteria

- [ ] Each listed provider streams text and invokes a structured tool call through the
      existing `internal/model` client.
- [ ] Mid-stream cancellation works against each listed provider.
- [ ] Startup and pre-run connection checks surface a dead/revoked key with a clear
      message, and the TUI stays usable.
- [ ] Unlisted endpoint produces the visible unverified warning; listed providers do not.
- [ ] API keys persist in the private state directory (0700), never in the repository or
      session DB, and are excluded from backups of `sessions.sqlite` by design.
- [ ] Release documentation names each accepted provider, its probe result, and the exact
      base URL/key format the user must supply.
