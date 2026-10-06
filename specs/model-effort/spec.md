# Feature: Model effort (choose how hard a reasoning model thinks)

**Status:** in progress — M1–M4 and live OpenRouter levels implemented (local)
2026-10-06, automated suite green; provider live probes (M5, run by the user
with `likha --probe-effort`) outstanding, so no provider carries effort yet
and the effort step appears nowhere. Claude wording revised 2026-10-07 after
the merges with claude-native, chunked-content, reasoning-replay and
effort-probe. Requirement: [v1-spec FR-37](../v1-spec.md#model-effort-amendment-user-approved-2026-10-06).
Replaces the earlier `/think` draft (formerly `specs/thinking-control/`).

## Context

Reasoning models accept an effort level that trades latency and tokens for
answer quality. Likha sends no effort today, so every reasoning model runs at
its provider's default, and the user cannot change it.

There is no shared ladder of levels. Each model publishes its own list, and
the lists differ:

| Model | Published levels |
| --- | --- |
| `gpt-5` | minimal, low, medium, high |
| `gpt-5.5` | none, low, medium, high, xhigh |
| `claude-opus-5-5` | low, medium, high, xhigh, max |
| `deepseek-v4-pro` | low, high, max |
| `grok-4.7` | low, medium, high, xhigh |

Defaults differ too (Opus 5.5 medium, most others high, gpt-5.1 none), and
sending a level a model does not support fails on most providers. A fixed
`off/low/medium/high` ladder, as the earlier draft proposed, would offer
levels that some models reject and hide levels that others support. Likha
therefore offers each model exactly the levels it publishes.

The levels come from the models.dev catalog Likha already bundles: its
`reasoning_options` field lists a model's reasoning controls, which may be
named effort levels, a thinking-token budget or an on/off toggle. On
2026-10-06, 574 of the 651 reasoning-capable models in Likha's catalog had
some `reasoning_options`, but many offer only a budget or a toggle. The
catalog regenerated that day has 418 rows with effort levels (of 650
reasoning rows): 415 from models.dev and 3 Gemini 2.5 rows from documented
overrides. Models without effort levels get no effort step.
Sources and provider behavior are recorded in [research.md](research.md).

## Role

Let the user pick a model's effort at the moment they pick the model, from
the levels that model actually supports. Never send a level the model did
not publish. Never show a choice that the provider would silently ignore.
When effort is left alone, requests are exactly what Likha sends today.

## Scope

In scope: the `/models` selection flow, a `/effort` command, remembering the
level per model, the status bar, and sending the level on the main turn,
explore children and profile tasks.

Out of scope:

- Token-budget control. Models that publish only a thinking budget or an
  on/off switch get no effort choice, unless the catalog records a
  provider-documented level list for them (decision 8).
- Enabling Claude effort. The native Messages API client
  ([claude-native](../claude-native/spec.md), FR-38) already carries a level
  as `output_config.effort`, but the `claude` row stays unsupported until a
  user-run FR-40 probe on Anthropic's own endpoint passes, like every other
  provider. (Superseded 2026-10-06: this bullet said Claude's
  OpenAI-compatible endpoint ignores effort; Claude no longer uses that
  endpoint.)
- A command-line flag or environment variable for effort.
- Prompt keywords. `ultrathink` and similar words in a prompt are ordinary
  user text.
- Sending reasoning back to the provider. Display reasoning is never sent
  (FR-15 as amended by FR-39); provider-issued replay state belongs to
  [reasoning-replay](../reasoning-replay/spec.md).

## Decisions (user interview, 2026-10-06)

1. **The effort step appears every time a model is picked in `/models`**,
   when that model has published levels and its provider can carry effort.
2. **The cursor starts on the level last used with that provider and model**,
   or on **Default** when there is none.
3. **Default sends no effort.** The request is byte-identical to one sent
   before this feature, and the provider applies its own default.
4. **First-run setup does not ask.** New users start at Default.
5. **Providers that cannot carry effort skip the step.** These are providers
   without a passed live probe. Claude is one of them: its native Messages
   API carries effort as `output_config.effort`, but the row stays off until
   its `output_config` probe passes
   ([claude-native](../claude-native/spec.md) decision 8,
   [effort-probe](../effort-probe/spec.md)). The switch is not silent: Likha
   says effort stays at the provider default and why. (Revised 2026-10-07:
   Claude was first excluded because its OpenAI-compatible endpoint ignored
   effort; it no longer uses that endpoint.)
6. **The command is `/effort`.** It uses the same word as the provider APIs,
   Codex CLI and Claude Code. It replaces the draft's `/think`.
7. **The level is remembered per provider and model** in the private
   `config.json`.
8. **Level lists come from the provider's live model list when it publishes
   them** (OpenRouter does), **else from Likha's bundled catalog**, where a
   hand-checked override may add or correct a list. An upstream level named
   `default` (Groq Qwen) is dropped, because Default already means "provider
   decides".
9. **Background requests use Default.** Conversation compaction and session
   auto-naming never send effort. Explore children and profile tasks use the
   session's level.

## Functional changes

### Picking a model

1. In `/models`, Enter on a model that has effort levels on a provider that
   can carry them opens an effort step in the same dialog instead of
   switching. The step names the model and provider and lists:
   - **Default**, described as "provider decides". When the live model list
     names the provider's default level, the description includes it, e.g.
     "provider decides (medium)".
   - each published level, in the model's published order, by its published
     name. Common names carry a short hint: `none` no reasoning, fastest;
     `minimal` lightest reasoning; `low` light reasoning; `medium` balanced;
     `high` deeper reasoning; `xhigh` very deep, slower; `max` deepest,
     slowest, most tokens.
2. The cursor starts on the level last used with that provider and model, or
   on Default (decision 2). A remembered level that the model no longer
   publishes counts as none.
3. Enter in the step switches to the model and sets the level in one action.
   The conversation entry names both, e.g.
   `Model switched to gpt-5.5 on OpenAI and stored for later runs. Effort: high.`
   With Default, the entry ends `Effort: default.`
4. Esc in the step returns to the model list with the cursor on the same
   row, and changes nothing.
5. Enter on a model without effort levels switches immediately, as today.
6. Enter on a reasoning model whose provider cannot carry effort switches
   immediately. The conversation entry adds why effort stays at the provider
   default: `Effort stays at the provider default: not yet verified for
   <provider>.` Claude uses the same wording as every unprobed provider:
   `Effort stays at the provider default: not yet verified for Anthropic
   Claude.`
7. Switching models refuses during an active run or pending review, as today.
   The step is never opened in that state.

### `/effort`

8. `/effort` with no argument opens the effort step for the current model,
   with the level in use marked `(current)`. Enter applies the level without
   switching models.
9. `/effort <level>` sets the level directly when `<level>` is `default` or
   one of the current model's published levels. Level names are matched
   exactly and in lower case.
10. Other input to `/effort` makes no change and shows a visible error naming
    the accepted values, and the typed draft is kept. This covers an unknown
    level, extra words, or a level the model does not publish.
11. When the current model has no levels or its provider cannot carry
    effort, `/effort` prints why (point 6's wording, or
    `<model> publishes no effort levels`) and changes nothing.
12. `/effort` refuses during an active run or pending review, like every
    reserved command (`Commands are inactive while a run is active.`).
13. `/effort` is listed in `/help` and in the `/` autocomplete.

### What is sent

14. Every main-turn request uses the level that was current when the run
    started. A level changed later applies from the next run.
15. Explore children and profile tasks use the session's level. Compaction
    and session auto-naming send no effort (decision 9).
16. At Default no effort field is sent, on every provider (decision 3).
17. A provider that rejects a request carrying effort shows the usual
    visible error, followed by
    `Sent with effort <level>; /effort default sends none.`
    The request is not retried (FR-36 never retries rejected requests).

### Remembering

18. The chosen level is stored per provider and model in the private
    `config.json`. Choosing Default removes the stored entry.
19. On launch, the stored level for the active provider and model applies.
    Resuming a session keeps the current provider, model and level, as
    today. A stored level the model no longer publishes
    is ignored: Likha uses Default and adds one notice,
    `Stored effort xhigh is not offered for gpt-5.5; using default.`
    The stored entry is kept.
20. A model chosen outside `/models` never prompts for effort. This covers
    `--model` at launch, first-run setup and ChatGPT sign-in. The stored
    level for that provider and model applies, else Default.

### Status bar

21. When the level is not Default, the status bar's model segment shows it
    after the model name, e.g. `gpt-5.5 · high`. It is plain text and
    readable without color or Nerd Font glyphs. On a narrow terminal the
    level is dropped before the model name is shortened.

### Turn footer

22. A turn that ran above Default records its level in the turn footer after
    the model, e.g. `✻ gpt-5.5 · high · 12.3s · 3 tools`. The level is saved
    with the footer, so it reappears on resume. Turns at Default show no
    level and save exactly as before this feature.

## Acceptance criteria

- Enter on `gpt-5.5` in `/models` (on a provider with a passed probe) opens
  a step listing Default, none, low, medium, high, xhigh. Choosing `high`
  switches the model and the next request carries effort `high`, observed
  on the wire by a test server.
- Choosing Default produces request bodies byte-identical to a build without
  this feature.
- Esc in the step returns to the model list and leaves model, level and
  `config.json` unchanged.
- A model without levels, or on an unprobed provider, switches on the first
  Enter. A reasoning model on an unprobed provider gets the explanation in
  its switch entry; on Claude it reads `not yet verified for Anthropic
  Claude`, with no claim that the endpoint ignores effort.
- Re-opening `/models` and pressing Enter on the same model puts the cursor
  on the remembered level.
- `/effort high` sets the level, `/effort` opens the step, and
  `/effort turbo`, `/effort high extra` and `/effort max` (on a model
  without `max`) are visible errors that keep the draft.
- The level survives relaunch through `config.json`. A stored level the
  model no longer publishes gives Default and one notice.
- Compaction and session-naming requests carry no effort, even at `high`.
  An explore child's requests carry the session's level.
- A 400 on a request with effort shows the effort hint and is not retried.
- The status bar reads `model · high` at `high` and the model name alone at
  Default.
- First-run setup has no effort step.
- A turn at `high` ends with a footer naming `high` after the model, and the
  level survives resume; a Default turn's saved footer has no effort field.
