# Context: Thinking control (`/think`)

Code paths this feature touches. Precedent links at the end; no design
decisions hidden in prose — all open decisions live in
[spec.md](spec.md).

## Code

- `internal/model/client.go` — `Stream()` builds the chat-completions
  request body inline (the anonymous body struct starts around line 343);
  there is **no** reasoning-effort field today. Adding one is a one-field
  change plus a way to carry the level in. Note the existing field-tightness
  convention: unknown fields risk HTTP 400 on strict relays, which is why
  the capability matrix exists. `Message.Reasoning` (line ~33) is captured
  and never sent back — unrelated to this feature but the reason the file
  is reasoning-aware already.
- `internal/model/codex.go` — `BuildCodexRequest` maps the conversation onto
  a Responses API body (`codexRequest`, line ~40). Its doc comment is a hard
  contract: sampling parameters are *never emitted because the backend
  rejects them with HTTP 400*, and "no other fields beyond those specified
  here". A `reasoning: {effort}` field therefore both violates that comment
  and is plausibly fine — the comment and the probe must be resolved
  together in tasks.
- `internal/model/provider.go` — `Providers` table; provider names used in
  the capability matrix, plus `SessionHeader`/`Auth` fields showing how a
  provider row can carry metadata if support needs to be declared per row.
- `internal/app/tui.go` — command dispatch: `commandHelp` (~line 547) and
  `handleCommand` (~line 556); the `default:` branch is the
  unknown-command-visible-error pattern `/think` argument validation should
  mirror. Status row: `mainView()` footer (`status := fmt.Sprintf(...)`,
  ~line 1301, `Usage()` hook right after) is where the badge mounts.
  Reasoning rendering precedent: `case "reasoning"` (~line 880) and the
  muted `Muted` role styling in `rebuild()` (~line 1236).
- `internal/app/config.go` — `storedProviderConfig` + `loadStoredConfig` /
  `saveStoredConfig`: the exact shape to extend if the persistence decision
  lands in `config.json` (field pattern: optional, `omitempty`, 0600 file,
  corrupt file is an error not a silent reset).
- `internal/app/run.go` — flag/env precedent (`--theme`/`LISA_THEME`) if
  the spec's persistence decision ever grows an env override (not required
  by the spec; listed as the house pattern only).
- `internal/app/tui_test.go` — `TestReasoningStreamsMutedAndClosesOnContent`
  and the `/help`/unknown-command tests (lines ~186-256) are the test
  harness shape to copy: `httptest` server + `tea.KeyMsg` drives + entry
  assertions.
- `internal/model/client_test.go` — `TestStream...` asserts the request
  struct round-trips; a `/think` request-body test belongs beside it.
- `internal/model/codex_test.go` — request-body tests for the Codex wire
  (e.g. line ~66: reasoning "must never be sent"); the `reasoning: {effort}`
  addition gets a sibling test here.
- `internal/app/run_test.go` — config.json permission/JSON-corruption tests
  (lines ~244-256) if persistence lands in `config.json`.
- Session store `internal/session/session.go` — the snapshot type, if
  per-session persistence wins the open decision.

## Specs and docs

- [specs/v1-spec.md](../v1-spec.md) — FR-03 (reserved commands act on the
  application, unknown commands error visibly, `//` escape), FR-10 (resume
  semantics constrain per-session persistence), FR-11 (errors stay visible,
  TUI survives), FR-15 (muted reasoning; never sent back; Nerd Fonts
  opt-in; plain text legible without color), §3 (compatibility is behavior
  to verify — the basis of the probe gate). This feature refines, never
  contradicts, that contract. No FR amendment needed: this is configuration
  of existing model requests.
- [specs/README.md](../README.md) — feature index row (added at
  implementation time, not by this draft).

## Precedent

- Anthropic, "Claude Code: Best practices for agentic coding"
  (https://www.anthropic.com/engineering/claude-code-best-practices) — the
  `think` < `think hard` < `think harder` < `ultrathink` keyword ladder and
  its thinking-budget allocations; the origin of the "Ultra Think analog"
  framing for this feature.
- OpenAI Platform docs on `reasoning_effort` in chat completions
  (https://platform.openai.com/docs/api-reference/chat/create) and
  `reasoning.effort` in the Responses API
  (https://platform.openai.com/docs/api-reference/responses) — the two wire
  shapes named in the capability matrix.
- Lisa's own status convention: the footer status row's plain-text state
  strings (`Connected`, `Waiting for model`, `Reading repository`) — the
  badge must read as a sibling of those, not as a new class of UI.
