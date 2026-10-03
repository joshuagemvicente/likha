# Phase 1 implementation evidence — 2026-10-03

**Status:** implemented locally; new behavior is not verified for release.
Phases 2–3 remain disabled. No new tests or code-review pass were performed.

## Authorization and ownership

The user authorized application implementation from the beginning of the phase
index and requested parallel subagents. Eight workers owned non-overlapping
areas: registry/schema/policy; repository inspection; harness; exact edits;
private artifacts; inspection UI; MCP identities; capture/session metadata.
The coordinator integrated the turn loop, commands, event/state wiring, and
private edit journals. This worker count is unrelated to the future four-child
runtime execution limit.

Existing README/CHANGELOG, app/model/provider/setup/model-menu changes and tests
were present before this implementation and preserved. Pre-existing untracked
`internal/tui/sel_probe_test.go.bak`, `lisa`, and model-metadata files were left
untouched. Changes are local; no release or deployment was performed.

## Integrated interfaces

| Area | Files and behavior |
| --- | --- |
| Registry | `internal/tools/`: deterministic catalog/definitions, validated schema dispatch, capability/mode narrowing, typed status/provenance, common authorization, bounded model content. Unsafe/unsupported MCP schemas remain unavailable with a reason. |
| Composition | `internal/agent/tool_registry.go`: migrated built-ins, `edit`, `read_output`, qualified MCP adapter, unavailable placeholders for later phases. Exact edits require private recovery journaling. |
| Harness | `internal/agent/harness.go`, `prompt.md`: embedded system instructions and real canonical root; optional confined root `AGENTS.md`, rejected rather than truncated above 32 KiB. Request-only snapshots stay outside conversation history. |
| Main loop | `internal/agent/turn_execution.go`: at most four executing contiguous safe reads, original result ordering, mutation/interaction barriers, tool-ID validation, steering/cancellation reconciliation, nonterminal 32-round notices. |
| Repository | `internal/repository/inspection*.go`: directory and numbered-range reads; scoped, ignore-aware, literal/case searches; bounded partial warnings and root revalidation. Legacy repository methods remain unchanged. |
| Exact edits | `internal/actions/targeted*.go`: original-snapshot unique/non-overlapping replacements and create; one complete review; whole-proposal preflight; safe parents/staging; bounded publication/recovery reporting. Darwin/Linux exclusive rename helpers. |
| Edit persistence | `internal/session/edit_journal.go`: private FULL-synchronous SQLite boundary records, separate from conversation revisions. `SetJournal` records intent before repository effects. Resume inspection never writes or replays. |
| Output | `internal/actions/command_capture.go`, `internal/tooloutput/`: drained bounded capture, execution status separate from completeness, session-owned private artifacts, 5/50-MiB caps, numbered retrieval, explicit clear. |
| MCP | `internal/mcp/tool_catalog.go`: stable provider-safe server-qualified names, original identity, safe legacy aliases, exact trusted-server calls, existing session TOFU and stdio transport. |
| TUI/session | `internal/tui/tools_view.go`, `tool_wiring.go` and shared hooks; `session.ToolRecord`: compact attributed results, catalog/expansion/pagination, retained preview metadata, private recovery notices, draft/queue preservation. |

## Checks

- Before implementation: `go build ./...` and `go test ./...` passed.
- Worker checks: existing action/repository/MCP/session checks, relevant race
  checks, package builds/vet, and Linux action-package compilation passed as
  reported by their owners. These do not exercise all newly added behavior.
- Initial integration found hidden rejection diagnostics, cancelled-call
  reconciliation, menu capacity, and rejection-summary wrapping regressions.
  They were fixed in implementation rather than deleting/skipping assertions.
- The old context-tracker assertion assumed no system prompt and the five-tool
  list. The user explicitly authorized its migration: it now checks the frozen
  harness/catalog and retains exact conversation, steering, result, usage, and
  estimate assertions. No additional test was created.
- Final `go build ./...`: passed.
- Final `go test ./...`: passed across the existing suite.
- Final `go test -race ./...`: passed across the existing suite.
- Scoped `git diff --check` for the integration files and specs: passed.
- No baseline full race run was collected before parallel edits; the final
  full race result is recorded without claiming a baseline comparison.

## Known limitations and unverified scenarios

- Glob/grep expose no usable cursor. Nonempty cursors are refused; bounded
  discovery advises narrowing `path`/`include`. File/directory/artifact reads
  support numbered continuations. Oversized individual lines are explicitly
  clipped; the tail is not recoverable through line-offset paging.
- Multi-file publication is not crash-atomic. Replacements may briefly be
  absent. Journals and filesystem changes are not one transaction; an intent
  can describe either side of an interrupted operation. Recovery on resume is
  manual inspection, never automatic replay or rollback. Crash/fault-journal
  behavior is unexercised.
- Exact edits require Darwin/Linux exclusive-rename support. Cross-platform
  build evidence is not a Linux interactive walkthrough.
- Artifact output and private edit journals may contain secrets; neither is
  redacted. Requested output pages may reach the configured provider. Journal
  contents do not automatically enter model history.
- MCP discovery retains its existing process-start behavior before first-call
  trust. Cancellation stops local waiting, not guaranteed server effects.
- No session-delete command exists in the current baseline; explicit output
  clearing is implemented. A future deletion surface must remove its artifacts.
- Live provider Image 1/Image 2 routing, exact-edit stale/partial/crash cases,
  output caps/resume/pagination, narrow-terminal inspection, root-instruction
  snapshots, qualified MCP conflicts/schema refusals, and 32/64-round
  continuation walkthroughs remain unverified.
- No hosted-provider or published-binary probe was performed. Passing existing
  checks is not competitor parity or release verification.

The next dependency checkpoint is the Phase 1 manual acceptance guide. Do not
interpret these local implementation records as verification of nested explore
or any Phase 3 workflow.
