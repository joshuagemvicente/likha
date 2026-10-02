# Context: Providers as connection manager (auth only, no activation)

Code paths this feature touches. The behavior in spec.md is the contract.

## Code
- `internal/tui/providers.go` — `handleProvidersCommand` (meta-arg Parse — N-or-File currently applies directly through
  `applyProviderDirect`); `applyProviderDirect` (stored key check → switch start → modal); `startProviderSwitch` /
  `providerSwitchMsg` / `handleProviderSwitchMsg` / `activateProvider` (the verify-then-activate path being removed
  from this surface); `openKeyModal` / `updateKeyModal` / `handleKeyCheckMsg` (key modal: keep, but success stores
  the provider and returns instead of activating); `providersDialogItems` (rows + configured marks, unchanged);
  key-modal view helpers (drag-out view, masked input).
- `internal/tui/dialog.go` — `confirmDialog` `dialogProviders` branch (row press route: goes before auth, never
  switch); `dialogKind`/keys (none new); `commandHelp` text mention.
- `internal/tui/keyfile helpers` — `providers.StoredKey` / `ReadCredentials` (the new auth-state view names its
  source: state-dir store vs env `LISA_<PROVIDER>_API_KEY`).
- `internal/model` — `model.New` peer-URL check helper reuse: client construction only for the connection check
  (dry lines never become caps to the live UI); the live client is never passed until `/models` selects a provider.
- `internal/tui/providers_test.go` — switch-path tests being converted: activation becomes auth checks (row stays,
  nothing swaps on Enter; modal-success paths store + return). Key modal success now also tests that the previous
  provider remains active.
- `internal/tui/models_section_window_test.go` — /models cross-provider switching must keep working unchanged (regression net).

## Related specs
- specs/all-models — activation path (unchanged); Failure-Note: /models is the only surface that switches.
- specs/first-run-setup — key-modal sharing; the setup flow still ends with picking a provider.
- specs/providers-established-earlier — also considers a rename; keeping /providers here is authoritative unless renamed.
