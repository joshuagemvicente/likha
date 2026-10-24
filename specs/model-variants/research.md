# Research: OpenCode V2 model variants

Primary sources inspected 2026-10-09. Canonical repository:
<https://github.com/anomalyco/opencode>, active `v2` branch; pinned commit
`ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4` (2026-10-09 01:27:23 UTC).
The repository-default `dev` is not the V2 reference.

## Documented contract

- [V2 keybindings](https://opencode.ai/v2/docs/cli/keybinds): `variant.cycle`
  defaults to Ctrl+T; `variant.list` is unbound.
- [V2 models](https://opencode.ai/v2/docs/models): model-specific variants,
  `provider/model#variant`, settings/headers/body overlays, provider → model →
  variant precedence, unknown explicit variant resolution errors. An in-flight
  request keeps starting settings. This does not guarantee a whole-run snapshot.

## Source-confirmed behavior

- [Cycle helper](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/tui/src/model-preference.ts#L41-L75):
  unset → named list order → unset; reserved default excluded, no alphabetical
  sorting; exact provider/model preference key.
- [Preference storage](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/tui/src/model-preference.ts#L103-L181):
  serialized, flock-protected atomic writes; explicit Default persists as
  `default`, unlike a missing key.
- [Model picker](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/tui/src/component/dialog-model.tsx#L126-L138):
  model applied first, valid named variant closes; otherwise selector replaces
  model dialog when choices exist. Esc does not undo the already selected model.
- [Selector](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/tui/src/component/dialog-variant.tsx#L10-L38):
  searchable Default-first ordered list; selecting Default clears overlay.
- [Commands](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/tui/src/app.tsx#L913-L935):
  `/variants` has thinking/effort aliases. Likha deliberately removes effort.
- [Selection scopes](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/tui/src/context/local.tsx#L236-L338):
  session draft/durable selection precede global exact-pair preferences;
  late acknowledgments match captured selection before clearing it.
- [Variant mutation](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/tui/src/context/local.tsx#L472-L497):
  no busy guard; composer and preference update, current HTTP attempt unchanged;
  preference save errors are swallowed upstream. Likha reports them.
- [Keymap](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/tui/src/context/keymap.tsx#L204-L247):
  variant bindings are base-mode bindings; autocomplete/dialog modes suppress
  them, rather than treating Ctrl+T as a global chord.
- [Generation](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/core/src/variant.ts#L6-L50):
  effort preferred over budget; optional off toggle prepended; ID dedup;
  high/max budgets computed below output limit. Likha additionally rejects an
  impossible minimum/maximum rather than offering a below-minimum preset.
- [Protocol mappings](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/core/src/variant.ts#L305-L418):
  Claude thinking/effort, Gemini budgets/levels and OpenRouter unified controls.
  These are adapter-specific; Likha's Gemini endpoint is compatibility, not
  upstream's native Gemini adapter. Port intent, not the wrong wire fields.
- [Merge](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/core/src/provider.ts#L181-L213):
  recursive object merge, scalar/array replacement, case-insensitive headers.
- [Resolver](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/core/src/model-resolver.ts#L136-L160):
  request-local overlay, unknown explicit variant fails, catalog unchanged.

## Queue limitation (source-derived, not live-reproduced)

[Inbox schema](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/schema/src/session-inbox.ts#L14-L44)
stores prompt payload/delivery, not a model selection. The
[runner](https://github.com/anomalyco/opencode/blob/ea666727a14c961f3a4e6ce9d0f5862b3a2bcbf4/packages/core/src/session/runner/llm.ts#L165-L250)
reloads session/model for later steps/retries. A later committed choice can
therefore affect earlier queued work. Likha freezes a whole run and captures
held items instead of duplicating this mutable-session mechanism.

## Provider sources and integration constraints

- [OpenAI reasoning](https://platform.openai.com/docs/guides/reasoning):
  supported effort values/defaults are model-specific; GPT-6 Astra and GPT-6.1
  Sol require Responses for function calling. Responses reasoning items should
  be returned with tool outputs; display summaries are not a substitute.
- [Public SIWC limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations):
  retain official public authentication and account-visible discovery, not an
  upstream private Codex backend integration.
- [Claude effort](https://platform.claude.com/docs/en/build-with-claude/effort):
  `output_config.effort`; not every model supports the same levels or off state.
- [Claude manual thinking](https://platform.claude.com/docs/en/build-with-claude/extended-thinking):
  manual budgets for supported older models; newer adaptive-only models reject
  manual thinking. Budgets must fit output capacity.
- [Gemini compatibility](https://ai.google.dev/gemini-api/docs/openai):
  reasoning effort or Google thinking level/budget, not both; thinking-off is
  model-specific. Native upstream Gemini setting names require translation.
- [OpenRouter model metadata](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties.md):
  supported_efforts is descending; null means no allowlist; mandatory=true
  rejects off/none; supports_max_tokens describes budget support.

No upstream tests were run locally, no model credentials were used and no
live inference was performed during this research. Sources establish a design
reference, not live-provider certification of Likha.
