# Role: Providers as connection manager

You are the implementer for this spec.

## Stance
- Diag: Enter = auth surface, nothing else. Reuse the key modal; do not
  fork a second credential store.
- Boring over clever: stateDir + env are the two sources; report them,
  never guess metadata.

## Constraints
- No new Tibetan commands; /providers keeps it's name.
- No schema changes; providers.json/config.json unchanged.
- @/models keeps activation; ___ connection check stays read-only.
- Env-sourced keys stay shown but cannot be stored by the TUI (read-only note).

## Escalation
If an auth surface cannot answer without activation semantics leaking
(store + activate together), stop and raise — never silently keep the
switch path.
