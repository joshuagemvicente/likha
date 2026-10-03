# Checklist: Claude (Anthropic) predefined provider

Status mirrors [spec.md](spec.md); live-probe items stay unchecked until
recorded in `specs/predefined-providers/context.md`.

- [ ] The `claude` row is appended to `model.Providers` with
      `LIKHA_CLAUDE_API_KEY` and an empty default.
- [ ] The row resolves through the standard API-key storage path
      (`TestResolveProviderSelectsEndpointsAndKeys` covers it in its loop).
- [ ] The row flows into setup, `/providers`, `/models`, and `--help` through
      the shared registry — no provider-specific UI code.
- [ ] Bearer-authenticated `GET /v1/models` probe resolves (or a minimal,
      specified client remedy is tested and documented first).
- [ ] Full live admission probe recorded: model list, streamed text,
      structured tool call/result, cancellation, bad key.
- [ ] `DefaultModel` pinned from a passing probe only.
- [ ] README, v1-spec, CHANGELOG, and provider-candidates matrix updated with
      pending-probe status; nothing is described as accepted before that.
- [ ] `go test ./...` passes from the project root.
