# Tasks: Additional predefined providers

**Status:** partially implemented — five provider rows and dual-shape model-list
decoding are wired; live admission probes remain outstanding.

1. **Provider rows (implemented):** register Groq, xAI, Together AI, Mistral AI,
   and Cerebras after `chatgpt`, all API-key-only with empty defaults and
   pending-probe status. The existing provider-resolution and UI surfaces
   iterate the table.
2. **Model-list decoding (implemented):** `Check` and `ListModels` accept both
   the OpenAI `{ "data": [{"id": ...}] }` envelope and a bare array of
   model objects. Unit tests cover both.
3. **Documentation (implemented):** document bases, key env vars, pending
   probe state, and route caveats in README, v1-spec, provider evidence,
   changelog, and this feature spec.
4. **Live probes (pending):** for each registered provider, run model listing,
   streamed text, structured tool call and tool-result roundtrip, mid-stream
   cancellation, and wrong/revoked-key checks. Pin a default model only after
   a successful probe; record per-provider evidence in
   `specs/predefined-providers/context.md`.
5. **Deferred candidates:** DeepSeek needs root-base and request-compatibility
   work plus a root `/models` probe; Gemini needs thought-signature-preserving
   tool calls and a live probe; Fireworks needs a same-base OpenAI-shaped model
   list or a specified alternate list route. Do not add them as predefined
   providers until those gates are resolved.

Verify the implementation with `go test ./...` from the project root. Live
compatibility is not established by mock tests or documentation alone.
