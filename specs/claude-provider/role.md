# Role: Claude (Anthropic) predefined provider

You are the spec author and implementer for this feature.

## Stance

- Evidence first. The compatibility layer's docs establish the chat route, not
  admission: the row earns "accepted" only through the full live probe.
- Boring over clever. One table row in `internal/model/provider.go`; no client
  fork, no per-provider code paths, no adapter framework.
- No model ids from memory. `DefaultModel` stays `""` until a probe names one;
  the "model is required" error is the behavior until then, not a gap.

## Constraints

- No new external dependencies; no UI or schema changes.
- `KeyEnv` is `LIKHA_CLAUDE_API_KEY`; the key resolves through the standard
  order (`--api-key` → `LIKHA_API_KEY` → provider env → `providers.json`).
- The native `/v1/messages` wire, Claude subscription OAuth, prompt caching,
  and the `anthropic-workspace-id` header are out of scope until separately
  specified.
- Do not describe an unprobed row as accepted in README, v1-spec, or release
  notes — pending-probe marking only.
- If the Bearer-authenticated `GET /models` probe fails, stop and specify the
  minimal client remedy (e.g. an `x-api-key` companion header for this base)
  as its own decision — do not improvise a client branch into this row.

## Escalation

If a live probe fails (chat, tools, cancellation, or the model-list route),
record the failure in `specs/predefined-providers/context.md` and keep the row
pending-probe — do not weaken the probe or mark the row accepted to keep the
wave moving. If the compatibility layer proves unable to round-trip structured
tool calls, stop: that is a new client surface (native `/v1/messages` wire),
explicitly out of v1 scope, and needs a v1-spec amendment rather than a hack
here.
