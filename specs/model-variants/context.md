# Context: Model variants

Use [spec.md](spec.md) for behavior, [tasks.md](tasks.md) for the execution
sequence and [research.md](research.md) when checking upstream behavior.

## Existing seams

| Area | Files | Relevant seam |
| --- | --- | --- |
| Catalog input | `internal/model/catalog/catalog.go`, `models.json` | User's current reasoning-options work exposes effort/budget/toggle and output bounds; do not overwrite it |
| Existing effort | `internal/model/effort.go`, `effort_test.go` | Provider wire controls and opt-in probe compatibility; UI is retired, provider terminology remains |
| Protocols | `internal/model/client.go`, `wire.go`, `codex.go`, `responses_stream.go` | Chat/native dispatch and currently OAuth-only Responses path |
| Native Claude | `anthropic_client.go`, `anthropic_request.go`, `anthropic_models.go` | Adaptive baseline, output limits, live capabilities and provider replay |
| Fork ownership | `internal/model/fork.go` | Fork copies request configuration, shares safe OAuth refresh state, resets telemetry; setters are currently between-run only |
| Public discovery | `internal/model/client_models.go`, `internal/app/model_metadata.go` | Account-visible ChatGPT models; supplement capabilities without expanding visibility |
| Preferences | `internal/providers/config.go`, `credential_storage*.go` | Current plain config writes; credential lock/atomic helpers demonstrate existing private-storage conventions |
| Startup/headless | `internal/app/run.go`, `headless.go` | Flags, explicit override precedence, metadata discovery and stored selection |
| UI entry points | `internal/tui/tui.go`, `effort.go`, `models_all.go`, `dialog.go`, `commandcomplete.go` | Sole dispatcher, old forced effort step, same/foreign-provider activation |
| Setup/reconnect | `internal/tui/setup.go`, `providers.go`, `oauth_login.go` | Apply stored selection without asking during setup/sign-in |
| Queueing | `internal/tui/tui.go`, `steering_*test.go`, `steer_now_test.go` | Existing string queues, steer channel and held-queue controls |
| Attribution | `internal/tui/thought_items.go`, `status_line.go`, `internal/transcript` | Turn model/effort snapshot, status identity, persisted legacy footer |
| Sessions | `internal/session/session.go`, `internal/tui/session_delete.go` | JSON snapshot compatibility, conflict checks, resume and fresh-session reset |
| Background work | `internal/agent/sessionname.go`, `compaction.go`, `internal/tui/tui.go` | Existing WithoutEffort/WithoutReplay can return the mutable parent at Default |
| Probe | `internal/effortprobe`, `internal/app/effort_probe.go` | Explicit paid diagnostic; no automatic provider enablement |
| Guides | `docs/usage.md`, `docs/providers.md` | Current `/effort`, Ctrl+T transpose, and pending-probe wording |

## Baseline and ownership

At the start of this work, `go test ./...`, `go test -race ./...` and
`go vet ./...` passed. No live-provider or terminal verification was performed.

Pre-existing user changes: `CHANGELOG.md`, `specs/README.md`,
`specs/model-metadata/spec.md`, catalog sources/tests/generated data, and
`specs/reasoning-options/`; `.claude/` contains existing worktrees. Keep those
changes and directories. New changelog/index additions must not replace the
user's entries. Nothing here authorizes operating in those worktrees.

Import rules remain those of `ARCHITECTURE.md` and root `AGENTS.md`: agent has
no UI/providers import; variants and provider lowering belong in model;
providers can import model; persistence and UI consume the model interface.
