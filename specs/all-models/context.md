# Context: All-provider models in `/models`

Code paths this feature touches. No design decisions hidden in prose — the
behavior above is the whole contract.

## Code

- `internal/tui/dialog.go` — `openDialog` `dialogModels` branch (today: one
  `ListModels` against the live client, 5 s bound); `dialogState`
  `loading`/`loadErr`; `updateDialog` key handling (query, Enter guard,
  Esc-discard) extended with non-selectable header rows (cursor space,
  `dialogMatches`, `windowList`, and the stale-list guards in
  `confirmDialog` index selectable rows only); `confirmDialog`
  `dialogModels` branch (today: `applyModel` on the single-provider list;
  headers resolve to their section's provider, never to a model);
  `dialogView` replaced per-row provider column with section headers (the
  one-`m.conn.Provider` column and its narrow-drop go away); `dialogLabel`
  ` (current)` marker; `dialogMatches`; `commandHelp`.
- `internal/tui/tui.go` — `modelsListMsg`, `handleModelsResult` (cursor on
  live model, empty-list `loadErr`, stale-list guards), `applyModel`
  (live `SetModel` + `config.json` model store), `Update`
  `modelsListMsg` branch; `Update` `oauthLoginMsg` branch feeds setup from
  `model.ChatGPTModels` (precedent for the curated list).
- `internal/tui/providers.go` — `providersDialogItems` (configured ✔ vs
  not-configured vs active `[connected]`; corrupt-read-as-unconfigured
  stance to mirror); `startProviderSwitch` /
  `handleProviderSwitchMsg` / `activateProvider` (verify-then-activate
  shape, buffer reset, session-only — the cross-provider apply reuses this
  shape plus a config store); `applyProviderDirect` (OAuth never enters a
  key modal — the reason `/models` stays keyless too).
- `internal/providers/catalog.go` — `SwitchModelID` (provider-switch model
  resolution; not reused row-for-row here since the user picks the model
  explicitly, but the no-carryover rule stands).
- `internal/providers/keyfile.go` — `StoredKey` / `StoredOAuth` (the fetch
  set source); `internal/providers/config.go` — `LoadStoredConfig` /
  `SaveStoredConfig` (pair persistence).
- `internal/model/client.go` — `ListModels` / `fetchModelList` (error
  classes the per-provider attribution maps onto), `New` / `NewOAuth` /
  `SetModel` / `APIKey`.
- `internal/model/provider.go` — `Providers` table order (grouping after
  the active provider), `ChatGPTModels` (curated list: the Codex backend
  has no OpenAI-shaped list route, which is also why `/models` on a
  `chatgpt` session errors today).
- Tests to extend: `internal/tui/models_sessions_dialog_test.go`,
  `dialog_test_helpers_test.go`, `providers_test.go`, `tui_test.go`
  (`httptest` `/v1/models` servers + `tea.KeyMsg` drives). Hard seam to
  respect: `model.Providers` URLs are fixed, so the fan-out needs an
  injectable list function or the tests cannot point rows at fake servers.

## Related specs

- [v1-spec.md](../v1-spec.md) — FR-03 (reserved commands act on the
  application; refinement needs no amendment), FR-11 (visible errors).
- [slash-commands/](../slash-commands/spec.md) — dispatch, dialog
  integration, unknown-command conventions.
- [predefined-providers/](../predefined-providers/spec.md) — accepted
  list, key storage, connection-check timing.
- [first-run-setup/](../first-run-setup/spec.md) — `setupCheckMsg` probe
  and model-stage picker (single-provider precedent).
- [chatgpt-plus/](../chatgpt-plus/spec.md) — curated list, OAuth client
  construction with saver.
- [README.md](../README.md) — feature index row (set to implemented
  (local) at implementation time, not by this draft).

## Precedent

- House only: the `/providers` dialog (configured-state rows, cursor on
  active, query over every row, verify-before-visible). No external
  precedent cited — no external behavior is being matched.
