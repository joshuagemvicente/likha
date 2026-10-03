# Context: skill seams

- `internal/skills/skills.go`: inert placeholder, not an implemented skill loader.
- `internal/providers/config.go`, `internal/providers/keyfile.go`: private state/path conventions; skills do not contain provider credentials.
- `internal/agent/agent.go`: metadata/harness and main-only tool injection.
- `internal/tui/commandcomplete.go`, `internal/tui/dialog.go`: reserved `/skills` activation and explicit invocation.
- [Custom commands](../custom-commands/spec.md): prompt-template feature remains separate/deferred.
- [Harness](../agent-harness/spec.md), [registry](../tool-registry/spec.md): instruction ordering and dispatch enforcement.
