# Context: Install command (`/install`)

Code, tests, and specs this feature touches.

## Code

| Path | Relevance |
| --- | --- |
| `internal/agent/install.go` (new) | `InstallPrompt(request string) string`. |
| `internal/agent/install_prompt.md` (new) | Embedded detect/ask/plan/execute/verify instructions. |
| `internal/agent/tool_registry.go` | `RunOptions.InstallMode`; plan gate on `run_command` (non-`ReadOnly` tiers) and Write tools until `plan_update` succeeds. |
| `internal/agent/turn_execution.go` | Install-mode instructions beside the init- and plan-mode ones. |
| `internal/cmdpolicy/` | `Classify` and the `ReadOnly` tier (`simple.go`: `which`, `uname`, `ls`, …) used by the gate. |
| `internal/tools/ask_user.go` | Questionnaire schema (ask-user amendment). |
| `internal/tui/install_command.go` (new) | `startInstall`: refusals, one-turn `InstallMode`, short transcript line. |
| `internal/tui/init_command.go` | Pattern to mirror for refusals and the one-turn flag. |
| `internal/tui/dialog.go` | `handleCommand` dispatch, `commandHelp`. |
| `internal/tui/commandcomplete.go` | Autocomplete entry. |
| `internal/tui/tool_wiring.go` | `toolRunOptions` passes `InstallMode`. |

## Tests

| Path | Covers |
| --- | --- |
| `internal/agent/install_mode_test.go` (new) | Plan gate, prompt content. |
| `internal/tui/install_command_test.go` (new) | Refusals, transcript vs. history, one-turn mode, help/autocomplete. |

## Related specs and requirements

- [v1-spec.md](../v1-spec.md): FR-03, FR-04, FR-05, FR-06/FR-07, FR-08, FR-10,
  FR-21, FR-30, FR-31, FR-32, FR-35.
- [ask-user](../ask-user/spec.md): questionnaire amendment and asking rules.
- [plan-todo](../plan-todo/spec.md): `plan_update` checklist used as the plan.
- [command-permissions](../command-permissions/spec.md): tiers; read-only probes.
- [repo-init](../repo-init/spec.md): precedent for a command that starts one
  restricted turn.
