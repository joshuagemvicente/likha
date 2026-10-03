# Spec: Thinking control (`/think`)

**Status:** draft

## Context

Reasoning models expose a thinking-budget lever: more thinking can trade
latency and tokens for answer quality on hard tasks. Likha already renders
thinking-model reasoning muted in the conversation ([v1-spec.md](../v1-spec.md)
FR-15) but has no way for the user to influence *how much* the model thinks —
the chat-completions request body in `internal/model/client.go` carries no
reasoning-effort field, and the Codex Responses request in
`internal/model/codex.go` likewise omits it.

The precedent is Claude Code's thinking control: plain `think`, `think hard`,
`think harder`, and `ultrathink` keywords in a prompt allocate escalating
thinking budgets, and `/think` exposes the same idea as an explicit command
instead of a prompt keyword (documented in Anthropic's "Claude Code: Best
practices for agentic coding", ultrathink = highest allocation). Likha adopts
the *concept* — a per-session quality lever for reasoning models — not the
prompt-keyword mechanism: prompts must never gain magic words (v1-spec §3:
compatibility is behavior to verify; keyword sniffing would be invisible,
undocumented behavior).

Scope guard: `/think` must land in the reserved-command machinery of
[v1-spec.md](../v1-spec.md) FR-03 — it acts on the application, never on the
conversation — and must obey FR-15's visual discipline (muted, no flashy
chrome, Nerd Font glyphs strictly opt-in).

## User-visible behavior

1. **Command.** `/think <level>` with `level` ∈ `off`, `low`, `medium`,
   `high` sets the thinking effort used for all *subsequent* turns of the
   current session. `/think` with no argument shows the current level and
   the accepted values, like `/help` output: it changes nothing.

2. **Default level.** OPEN DECISION: the default is `off` (no field is ever
   sent — exact current behavior, zero provider risk) or `medium` (the lever
   exists and does something out of the box, but every provider probe must
   cover the default too). The decision must weigh: v1's "compatibility is
   behavior to verify" stance, and the risk that a default-on field breaks
   providers that reject unknown parameters.

3. **Turn scoping.** The level applies to the next assistant turn and stays
   until changed or the session ends. It does not retroactively change the
   current turn. Changing it mid-run edits nothing in flight. OPEN DECISION:
   the value persists with the session (resumed sessions restore their
   level, shown when resumed) or persists in `config.json` next to
   `provider`/`model`/`theme` (all sessions share one default). Either is
   acceptable; per-session restore is more consistent with FR-10's
   "resume the conversation as it was", but must be written down as a
   schema addition to the session snapshot.

4. **Status-line indicator.** While a turn runs at an effort level above
   `off`, the footer status row shows a plain-text badge naming the level
   (e.g. `thinking: high`). Constraints (FR-15, explicit):
   - the badge renders in the theme's accent role, kept **muted** — it is a
     state label, not a decoration;
   - the color encodes the level (one accent color per level, from the
     existing theme palette — no new colors may be invented for it), and
     **the plain text stays legible without color** (the level word is
     always in the badge);
   - there is **no animation, flash, or pulsing** of any kind; the badge
     appears for the duration of the streaming turn and disappears with the
     status row's normal turnover;
   - no Nerd Font glyph in the badge unless `--nerd-fonts`/`LIKHA_NERD=1` is
     on, and then only as an opt-in prefix marker, never as the sole
     carrier of the level.

5. **Non-reasoning models.** The command is accepted on every provider and
   model (it is a setting, not a capability claim). Whether the level is
   transmitted is governed by the capability matrix below; where it cannot
   be transmitted, the visible outcome is governed by the same matrix.

6. **Unknown input.** `/think` with a value outside the accepted set, or a
   second word, produces a visible error naming the accepted values, is
   never sent to the model, and preserves the draft (same contract as
   unknown commands in FR-03). The current level is unchanged.

7. **Provider capability matrix.** OPEN DECISION, gated on probe evidence:
   no row may claim support without a recorded live probe, per v1-spec §3
   ("compatibility is a behavior to verify"). The matrix rows are the seven
   predefined providers; the column is the wire mechanism:

   | Provider | Wire surface under consideration | Evidence needed |
   | --- | --- | --- |
   | openai | chat-completions `reasoning_effort` (o-series/gpt-5 reasoning models); rejected by non-reasoning models | probe: send on `gpt-5`-class and on `gpt-4o-mini`; record accepted vs HTTP 400 |
   | openrouter | chat-completions `reasoning_effort` (OpenRouter normalizes some models); unknown for others | probe: one supporting model and one model that does not |
   | bedrock | OpenAI-compatible route (`/openai/v1`) — whether it passes `reasoning_effort` through to Converse | probe against the openai-compat endpoint, Claude reasoning model |
   | dialagram | chat-completions relay; behavior unknown | probe |
   | opencode-zen | chat-completions (`gpt-5.3-codex` default suggests reasoning models) | probe |
   | opencode-go | chat-completions (`glm-5.3-flash`) | probe |
   | chatgpt | Responses `reasoning: {effort}` — natively supported by the API surface; but the Codex backend already rejects sampling parameters with HTTP 400 (see `BuildCodexRequest` doc), so `reasoning` must be probed, not assumed | probe with a real login |

   Stance to pick (OPEN DECISION, one for the whole matrix): a row that has
   not been probed **either (a) never sends the field and the badge is
   suppressed with one muted footer note saying so, or (b) sends it and a
   provider rejection (HTTP 400 naming the field) surfaces as a visible
   turn error suggesting `/think off`**. Both are FR-11-conformant; (a) is
   safer against silent breakage, (b) is more honest. Do not mix stances
   across rows without documenting why. Unlisted endpoints (BYOK custom
   URL) always follow the unprobed stance: Likha cannot know what an
   arbitrary endpoint accepts.

8. **Off.** `off` sends no reasoning field at all — byte-identical request
   bodies to today's. This is the escape hatch for any provider the matrix
   stances cannot satisfy.

## Non-goals

- No prompt-keyword detection (`ultrathink` inside a prompt is user text,
  full stop).
- No per-turn UI to pick a level at send time (the command is the whole
  interface; dialogs are for `/models`-style selection and this is one
  value).
- No "thinking budget in tokens" passthrough; levels map onto each wire's
  own effort vocabulary only.
- No new providers, no changes to the approval flow, no FR-15 rendering
  changes (reasoning stays muted and is never sent back to the provider).

## Acceptance criteria

- [ ] `/think high` followed by a prompt changes the next request to the
      provider by exactly one field (the matrix's wire field for that
      provider) with the chosen value; a capture proxy or test server
      observes the field.
- [ ] `/think off` makes subsequent request bodies byte-identical to a
      session that never used `/think`.
- [ ] `/think` with no argument prints the current level and accepted
      values and starts no turn.
- [ ] `/think nonsense`, `/think low extra`, and `/think --high` each print
      a visible error naming the accepted values, leave the level
      unchanged, never reach the model, and preserve the typed draft.
- [ ] A turn started at `high` shows the level badge on the status row for
      the duration of the streaming turn, and the badge text reads
      correctly with color disabled (e.g. theme with accent off, or
      NO_COLOR-style check).
- [ ] The badge never animates or flashes and introduces no new theme
      colors.
- [ ] On a provider where the matrix says the setting cannot be
      transmitted, the chosen stance's visible behavior (suppression note
      or turn error) is exactly what the spec's matrix row records — no
      silent no-op without the documented note/error.
- [ ] The level survives what the persistence decision in point 3 chose:
      either a resumed session restores it visibly, or a relaunch reads it
      from `config.json`; whichever was chosen, a test exercises it.
- [ ] `go test ./...` passes with the feature's tests included; the request
      body assertions cover every level mapping in the chosen matrix.
