# Role — Transcript redesign

Acting stance and constraints for whoever executes this spec.

- Senior Go/TUI engineer. `rebuild()`/`mainView` line assembly, the
  `lines`/`lineStyles` alignment, paging, focus, and the overflow invariants
  are load-bearing; every new block must keep `m.lines` and
  `toolEntryStartLine`-style line arithmetic in sync (prefer deleting the
  duplicated arithmetic over mirroring it).
- The spec is the contract. User-visible behavior comes from `spec.md`; a
  conflict is resolved by changing the spec first. The superseded rules table
  in `spec.md` is the complete list of approved overrides — any other earlier
  rule still binds.
- Display only: never change what is sent to the model or what the agent
  runtime records, except the approved `tool_start` record and `Turn` entry
  (§ Persistence). Old sessions must keep loading.
- Dependencies: exactly `github.com/yuin/goldmark` and
  `github.com/alecthomas/chroma/v2` (pure Go) are approved, in slice 2 only.
  No glamour, no other new modules.
- Honest rendering: glyph + words carry meaning; color, bands, and blinking
  are reinforcement. Model text is untrusted — no escape sequences from
  content, ever.
- Theme discipline: new roles are derived with `mix()` + `legible()`; no
  hand-picked per-family tables unless the contrast gate fails, and every
  override is recorded in `tasks.md`.
- Tests: the ~22 assertions that pin role-label text are rewritten to assert
  glyph rendering (approved 2026-10-04). Every other existing check stays as
  strong as it is. Each slice adds tests for its own behavior.
- Delivery discipline: three slices, each committed after `go build`,
  `go vet`, `go test ./...`, `go test -race ./...`, gofmt, and
  `git diff --check` pass; the user tries each slice before the next starts.
