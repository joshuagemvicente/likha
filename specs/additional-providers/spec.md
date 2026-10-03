# Feature: Additional predefined providers

**Status:** partially implemented — Groq, xAI, Together AI, Mistral AI, and Cerebras are wired as pending-probe rows; DeepSeek, Gemini, and Fireworks remain blocked on the listed compatibility work/probes.

## Context

Likha's predefined provider list (`internal/model/provider.go`, `Providers`) is the only thing between a user key and a session: the setup picker, the `/providers` dialog, the `/models` fan-out, `ResolveProvider`, and `--help` all iterate the table. Each new row is API-key-only, leaves `DefaultModel` empty pending a probe, and remains marked pending-probe until the admission gate passes.

| Canonical | Display | BaseURL | What it needs / status | Source |
| --- | --- | --- | --- | --- |
| `groq` | Groq | `https://api.groq.com/openai/v1` | API key only (`LIKHA_GROQ_API_KEY`); no client route change indicated. Pending full admission probe. | https://console.groq.com/docs/openai · https://console.groq.com/docs/models |
| `xai` | xAI | `https://api.x.ai/v1` | API key only (`LIKHA_XAI_API_KEY`); Chat Completions is documented as legacy. Pending full admission probe. | https://docs.x.ai/developers/rest-api-reference/inference |
| `together` | Together AI | `https://api.together.ai/v1` | API key only (`LIKHA_TOGETHER_API_KEY`); parser accepts documented envelope or bare-array list shapes. Pending live shape and full admission probes. | https://docs.together.ai/docs/inference/openai-compatibility · https://docs.together.ai/reference/models |
| `mistral` | Mistral AI | `https://api.mistral.ai/v1` | API key only (`LIKHA_MISTRAL_API_KEY`); schema/example disagree on list envelope, both are parsed. Pending live shape and full admission probes. | https://docs.mistral.ai/api/endpoint/chat · https://docs.mistral.ai/api/endpoint/models |
| `cerebras` | Cerebras | `https://api.cerebras.ai/v1` | API key only (`LIKHA_CEREBRAS_API_KEY`); test compaction's developer-role message with a supported model. Pending full admission probe. | https://inference-docs.cerebras.ai/resources/openai · https://inference-docs.cerebras.ai/api-reference/models/list-models |

Deferred candidates: DeepSeek needs the root-base validator plus developer-role/reasoning/tool-usage compatibility work and a root `GET /models` probe; Gemini needs thought-signature preservation for tool round-trips and a live tool probe; Fireworks needs verification of an OpenAI-shaped model list at the chat base or a separate model-list route. See [provider-candidates.md](provider-candidates.md) for the wider research matrix.

The environment-variable names use Likha's `LIKHA_<PROVIDER>_API_KEY` convention.
Every row still needs the admission probe: `GET /models`, streamed chat,
structured tool calls and tool-result roundtrip, mid-stream cancellation, and
revoked-key behavior. `DefaultModel` remains empty until a probe confirms a
model id.

Shape notes: Groq, xAI, Together, Mistral, and Cerebras use non-root HTTPS bases accepted by Likha's validator. The shared model-list parser accepts both `{ "data": [...] }` and a top-level array, covering documented Together and Mistral response examples. This parsing support does not replace a live response-shape or admission probe.

Admission follows the existing gate (v1-spec §3, predefined-providers resolved decision 4): streamed text, structured tool calls, mid-stream cancellation, revoked-key error, recorded in `specs/predefined-providers/context.md`. Rows without probe evidence ship marked pending-probe like today's unprobed rows — never described as accepted.

## Resolved decisions

1. **Defaults stay empty until a probe picks them** (dialagram precedent: `resolveModel` in `internal/app/run.go` requires `--model`/`LIKHA_MODEL` when `DefaultModel` is ""). No model id enters a row from memory.
2. **Rows added from route evidence:** Groq, xAI, Together AI, Mistral AI, and Cerebras are registered with empty defaults and pending-probe status. Together and Mistral use the shared model-list decoder's envelope-or-array support.
3. **DeepSeek, Gemini, and Fireworks remain deferred:** DeepSeek needs root-base validator and request-compatibility work plus a passing root-list probe; Gemini needs thought-signature-preserving tool round-trips and a passing tool probe; Fireworks needs a compatible model-list route established.
4. **No provider-specific UI or schema changes.** Registry rows automatically surface in setup, `/providers`, `/models`, and `--help`; the shared model-list decoder was extended for documented response shapes.

## User-visible behavior

1. `--provider groq|xai|together|mistral|cerebras` resolves to its base URL, reads its `LIKHA_<NAME>_API_KEY` in the existing key order, stores keys in `providers.json` like every other row, and requires `--model` until a probe sets a default.
2. Each added row appears in first-run setup, `/providers` (with configured/not-configured markers), and `/models` (one section per stored key); no row is described as accepted until its probe passes.
3. `deepseek`, `gemini`, and `fireworks` remain absent from the predefined list until their route/client gates are resolved.
4. The README provider table names each new row with its base URL, key variable, and probe status; unprobed rows read pending-probe, never accepted.

## Acceptance criteria

- [x] `--provider groq|xai|together|mistral|cerebras` resolve and use the standard API-key storage path.
- [x] Both `{ "data": [...] }` and top-level-array model lists decode into IDs; connection checks and `/models` share the decoder.
- [x] New rows appear in setup, `/providers`, `/models`, and `--help` without provider-specific UI code.
- [x] A row with no `DefaultModel` requires `--model`/`LIKHA_MODEL` with the existing "model is required" error.
- [ ] Each added row's full live probe is recorded before it is described as accepted; until then it is marked pending-probe.
- [ ] DeepSeek, Gemini, and Fireworks remain unregistered until their route/client gates are satisfied.
- [ ] No unprobed row is described as accepted in README, v1-spec, or release notes.
- [x] `go test ./...` from the project root passes with the feature's tests included; no mock-only test claims a provider works.
