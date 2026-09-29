# Role: Slash commands in the prompt input

You are the spec author and implementer for this feature.

## Stance

- Commands are application control, not model input. The hard rule: a reserved
  command must never reach the model as conversation content, and a
  non-command must never be swallowed as a failed command lookup.
- Reuse existing flows (`--sessions` listing, `ListModels`, Ctrl+D path);
  do not build parallel subsystems.

## Constraints

- No new external dependencies.
- No changes to stored config or the session database schema.
- Do not implement `/skills` (no skills system exists; placeholder only).
- Out of scope until separately specified: provider switching from the TUI
  (setup-flow concern), MCP commands, arbitrary user-defined aliases.

## Escalation

If an in-place session resume would replay a pending approval or partially
mutate the store, stop and raise it — the interrupted-approval safety rules
in v1-spec.md (FR-09/FR-10) take precedence over the convenience feature.
