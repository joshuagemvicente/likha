# Feature: persisted plan/todo checklist

**Status:** planned. **Phase:** 3. **Product requirements:** FR-31.

## State and tool

Expose one main-agent `plan_update` tool accepting `steps`, a complete ordered
list of at most 32 `{id,title,status}` records. IDs are unique nonempty stable
strings; titles are plain nonempty text up to 240 characters (of emoji tag
sequences, only the England, Scotland and Wales flags). Status is
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
completed statuses; when the run that left work in_progress or blocked was cut
short (cancelled, failed, or ended with the app), mark that work as
interrupted/unconfirmed until the main agent/user revisits it in a fresh turn.
A run that completed may leave a step in progress, such as one waiting on the
user's answer; it resumes as it was, with no interruption reported. A
cancelled or failed run records the interruption with the plan as it ends, so
a later run that completes without updating the plan does not clear it; a
later plan update replaces the plan and the interruption with it. A run
cancelled while its `prompt_submit` hook still runs started no work and
interrupts nothing. A run whose session cannot be saved as it ends resumes
interrupted, as one that ended with the app does. Do not
convert cancellation into completion. The record that a run is still working
is kept per session: two Likha instances working in the same session can
mask each other's interruption. Sessions left mid-run by builds before this
record existed resume without an interruption being reported. Old sessions load with an empty plan. Save failure produces a
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
  fabricated completion. Cancel a run mid-step, complete another run without a
  plan update, quit and resume; still show the interruption. Complete a run that leaves a step in progress, quit
  and resume; show no interruption. Load a legacy session with a clear empty state.
- Toggle /plan and attempt a file edit; checklist state grants no write access.
