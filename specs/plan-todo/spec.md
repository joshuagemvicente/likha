# Feature: persisted plan/todo checklist

**Status:** planned. **Phase:** 3. **Product requirements:** FR-31.

## State and tool

Expose one main-agent `plan_update` tool accepting `steps`, a complete ordered
list of at most 32 `{id,title,status}` records. IDs are unique nonempty stable
strings; titles are plain nonempty text up to 240 characters. Status is
`pending`, `in_progress`, `completed`, or `blocked`. A valid update replaces
the list as one session-state operation; invalid updates leave the old plan
unchanged. An empty list clears the plan visibly. There are no separate task
CRUD tools, hidden scheduling, or permission fields.

Display a compact progress summary with an inspectable complete list, and add
each meaningful update to the transcript. Users can open `/todo` while idle or
use the active checklist Inspect action without disturbing steering input.
Expose an empty state and completed/blocked/interrupted text independent of color.

This checklist describes the main agent's declared work. It does not execute
steps, verify changes, launch agents, switch `/plan` mode, or approve effects.
Explore children cannot update it; their task statuses live in the agent tree.
The model can declare a step completed, but UI wording must not present that as
independent verification of a command/file outcome. Preserve accurate tool
results so users can inspect the supporting action evidence.

## Persistence and interruptions

Store the list, last update, and interrupted-run marker with the private session.
Resume restores the list but starts no work and grants no permissions. Preserve
completed statuses; mark formerly active work as interrupted/unconfirmed until
the main agent/user revisits it in a fresh turn. Do not convert cancellation
into completion. Old sessions load with an empty plan. Save failure produces a
visible state/result error rather than claiming durable persistence.

`/plan` is the separate [read-only mode](../plan-mode/spec.md). A persisted
checklist never enables/disables that live mode. Plan updates remain allowed
in read-only mode because they change harness-owned session state, not repo files.

## Acceptance guide

- Create, reorder/update, complete/block, and clear a bounded list; list and
  transcript agree. Invalid/duplicate/excess records preserve old state.
- Inspect while a run is active without submitting a reserved slash command
  or losing a draft/queue.
- Cancel and resume an in-progress plan; show interruption with no restart or
  fabricated completion. Load a legacy session with a clear empty state.
- Toggle /plan and attempt a file edit; checklist state grants no write access.
