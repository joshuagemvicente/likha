# Context: First-run provider setup in the TUI

## Code

| Path | Relevance |
| --- | --- |
| `internal/app/run.go` | Setup trigger, config precedence, non-interactive guard. |
| `internal/app/config.go` | New: `config.json` load/save. |
| `internal/app/tui.go` | Setup stages inside the Bubble Tea model. |
| `internal/app/keyfile.go` | Existing `providers.json` key store reused for the entered key. |
| `internal/model/client.go` | `ListModels` (read-only probe) reused for the check; client constructed at setup completion. |
| `internal/model/provider.go` | Provider table shown by the picker. |

## Behavior notes

- Setup completion constructs the `model.Client` inside the TUI (Run cannot,
  because provider/endpoint/key are unknown until then); `EnsureConnected` is
  therefore bypassed for the first launch — the setup check already verified
  the connection.
- `ui.client` is nil during setup; the existing `turnEvent` guard
  (`!m.working || v.runID != m.runID`) makes late events harmless, but no turn
  can start before setup completes (Enter in setup stage is selection, not
  prompt submission).
- Related specs: [predefined-providers/](../predefined-providers/spec.md)
  defines the provider table this picker renders; FR-02 (v1-spec.md) is the
  amended requirement.

## Open items

- Editing stored config re-triggers setup by deleting `config.json` (documented).
- A "custom endpoint" setup path is out of scope; CLI flags cover it.
