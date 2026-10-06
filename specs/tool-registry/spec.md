# Feature: tool registry, policy, and catalog

**Status:** implemented (local); new acceptance walkthroughs unverified.
**Phase:** 1. **Product requirements:** FR-09, FR-24.
See [implementation evidence](../tooling-platform/implementation.md).

## Goal and boundaries

Users can identify an available tool, its source, and its approval requirements.
The runtime validates calls through one dispatch path. This feature moves the
current built-ins and stdio MCP tools without weakening their permissions.
Executable plugins, user tool registration, and saved shell allow-rules are
outside this phase. [Shared decisions](../tooling-platform/decisions.md) apply.

> **Amended 2026-10-04 by [command-permissions](../command-permissions/spec.md):**
> Likha now has compiled-in shell tiers (refuse, always ask, ask,
> verification, read-only), in-memory session grants for one exact command,
> and per-repository trust of verification checks stored in private
> `config.json`. User-authored saved shell allow-rules remain out of scope.

## Catalog behavior

`/tools` opens a keyboard-accessible catalog when no run is active. Each entry
shows model name, human title, source/server, description, effects, permission
scope, mode/agent eligibility, and availability reason. Optional unconfigured
tools remain visible as unavailable here but do not enter model definitions.
The view distinguishes configured MCP tools from Likha-authored built-ins.

The planned main-agent inventory is `glob`, `read`, `grep`, `edit_file`, `edit`,
`run_command`, `read_output`, then `task`, `ask_user`, `plan_update`, `skill`,
`web_search`, and `web_fetch` in their delivery phases. Publish only the tools
actually installed/enabled. Keep the existing five names and required inputs;
add optional fields without silently replaying old calls.

Every registration has a stable model name, description/schema, source identity,
effects and target scope, interactive/concurrency flags, cancellation policy,
output bounds, and backend requirements. One tool may have multiple effects;
an unknown MCP effect is conservative, not an inferred read permission.

## Call and result contract

1. Validate call identity, tool name, UTF-8/JSON arguments, required fields,
   field types, ranges, and the registered schema before authorization/dispatch.
   Built-ins reject unknown fields. Preserve valid MCP schemas; hide a tool
   with a visible reason when its schema cannot be validated safely.
2. Resolve the effective run/mode/agent capability ceiling. Omission from a
   model catalog is not enforcement: reject a crafted disallowed call too.
3. Authorize the actual target/effects. Route workspace proposals through diff
   approval, shell through exact-command approval, MCP through server TOFU,
   and web through its separate consent policy. Scope a grant to the displayed
   operation; instruction or agent text cannot expand it.
   *Amended 2026-10-04 by [command-permissions](../command-permissions/spec.md):*
   shell is classified before exact-command approval. Refused commands return
   a refused result; read-only commands, trusted verification checks, and
   exact commands the user allowed for the session run without a review and
   are labelled auto-approved. Session grants and repository trust are the
   only shell grants beyond the displayed operation; instruction or agent
   text still cannot create or expand either.
4. Dispatch with cancellation, then return the result exactly once against
   the original tool-call ID. Invalid/refused/failed calls have results too.

Results contain a status (`succeeded`, `refused`, `failed`, `cancelled`, or
`limited`), bounded content, source identity, completeness/truncation metadata,
warnings, and optional session-owned output reference/continuation. Distinguish
an action's exit status from completeness of captured output. A clipped payload
or incomplete scan cannot claim an exhaustive answer. Expose safe error codes
and recovery guidance, not credentials or stack traces.

The model receives a valid tool message even for refusal or cancellation.
The UI sees the same operation identity/status. Retain current reconciliation
of assistant calls that never executed when interruption occurs; a saved
transcript must not contain unmatched tool calls or inferred execution.

## Concurrency and interactions

Execute at most four independent main-agent read calls concurrently. Only a
declared parallel-safe contiguous read/task group may overlap. Mutations,
approval, and questions form ordering barriers. Return results in original
model-call order while allowing attributed progress events to arrive earlier.
Task requests use the run-wide child scheduler, not a fresh allowance per call.

Serialize user interactions through the existing UI seam. Tool handlers send
requests/results; they do not render dialogs or write TUI state. A cancelled
request ignores later answers and leaves subsequent prompts usable.

## Provenance and compatibility

Give MCP tools stable server-qualified names within provider name limits;
retain the original server/tool identity in the catalog and transcript. Detect
collisions after qualification. Reject ambiguous names instead of selecting
a server by registration order. Resolve a legacy raw name only if it identifies
one tool and does not collide with a built-in. Saved historical names remain
displayable; resume does not redispatch them.

Changing catalogs/configuration applies at safe run boundaries. Frozen run
definitions cannot acquire new tools/permissions from a file edited mid-turn.
Existing raw-string tool results remain readable alongside new envelopes.

## Acceptance guide

- Catalog an unavailable backend, a plan-mode-blocked tool, a built-in, and two
  identically named tools from different MCP servers; identities are distinct.
- Submit malformed arguments, an unknown name, and a child-disallowed call;
  each yields one attributed refusal/error without an effect.
- Run independent reads and a mutation barrier; results match original IDs
  and order, and no mutation precedes approval.
- Cancel pending approval and active reads; drain results without executing
  queued work or accepting a late approval.
- Resume an old five-tool transcript; no replay or invented grant occurs.

Record actual walkthrough evidence. Existing checks alone do not verify these
new cases; implementation creates no new tests under the approved constraint.
