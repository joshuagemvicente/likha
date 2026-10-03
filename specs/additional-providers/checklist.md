# Checklist: Additional predefined providers

Status mirrors [spec.md](spec.md); live-probe items stay unchecked until
recorded in `specs/predefined-providers/context.md`.

- [x] Groq, xAI, Together AI, Mistral AI, and Cerebras resolve from the
      provider table with their API-key env slots and empty defaults.
- [x] Provider rows flow into setup, `/providers`, `/models`, and `--help`
      through the shared registry.
- [x] The shared model-list decoder accepts both `{ "data": [...] }` and a
      top-level array; `Check` and `ListModels` use the same decoder.
- [x] README and v1-spec identify the new providers as pending-probe, not
      accepted; no model defaults were guessed.
- [ ] Run every new row through the full live admission probe: model list,
      streamed text, structured tool call/result, cancellation, and bad key.
- [ ] Record each live result before marking that provider accepted.
- [x] DeepSeek, Gemini, and Fireworks remain deferred behind their route/client
      compatibility gates.
- [x] `go test ./...` passes from the project root.
