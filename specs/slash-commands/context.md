# Context: Slash commands in the prompt input

## Code

| Path | Relevance |
| --- | --- |
| `internal/app/tui.go` | Prompt input handling (`case "enter"`), the interception point. |
| `internal/app/agent.go` | Turn dispatch — commands must short-circuit before `runTurn`. |
| `internal/app/config.go`, `keyfile.go` | Stored config `/models` must not touch. |
| `internal/model/client.go` | `ListModels` reused for `/models`; client swap on selection. |
| `internal/session/session.go` | `--sessions` listing reused for `/sessions`. |

## Related specs

- [first-run-setup/](../first-run-setup/spec.md) — provider/model storage that
  `/models` must leave alone (open question 2).
- [predefined-providers/](../predefined-providers/spec.md) — provider surface
  the commands operate through.

## Open questions carried from spec.md

1. `/sessions` in-place resume vs print-and-relaunch.
2. `/models` live-only vs persisted.
3. Literal-slash escaping (`//` escape or accepted limitation).
4. Interaction rules during active runs / pending approvals.
