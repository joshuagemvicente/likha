# Feature: agent tree, child transcripts, and cancellation

**Status:** planned. **Phase:** 2. **Product requirements:** FR-28–29.
Depends on the [explore runtime](../explore-agents/spec.md).

## Discovery and active-run entry

`/agents` opens the built-in explore profile and session task tree while idle.
Show its exact tool ceiling, permitted nesting, inherited model, and budgets.
Include an empty state; do not expose profile creation or unavailable writer roles.

While running, typed reserved commands keep FR-21's inactive behavior. Users
can focus a visible task row with the transcript inspection controls and use
its Inspect action to open the tree without submitting a slash command or
losing the composer draft/held steering queue. Help and the footer describe
the focused controls. Return/Back closes inspection; Esc/Ctrl+C still cancels
an active run. Inspection is not a permission approval or task launch.

## Tree and detail views

Each node identifies stable task ID, parent, depth, brief label, status,
provider/model, rounds, deadline/elapsed time, and shared spawn-budget use.
States distinguish queued, running, waiting-for-child, completed, limited,
failed, cancelled, and interrupted. Show queue/wait time in the deadline view.
The tree remains usable on a narrow terminal through focused detail pages;
state does not depend on color, icons, animated indicators, or hover.

Task start/progress/terminal entries remain in the main transcript, with
source/parent attribution. A generic activity indicator does not replace
tool/result identity. Detail inspection exposes saved child messages/tool
arguments/results and paginated session-owned output. It does not merge them
into the parent model context or trigger extra provider calls.

Show measured child usage separately from main request context. Sum reported
per-request usage once per task; missing tokens/pricing stay unknown. A child
request cannot overwrite FR-23's main context display. Explain that nested
agents use the configured hosted provider and can multiply cost; do not promise
a cost cap from time/concurrency/round limits.

## Cancellation

A focused active node exposes `Cancel branch`. Confirm the target task label
and descendant scope through a dedicated action, not the file-approval dialog.
Disable it for terminal nodes. Pending confirmation does not pause deadlines.
The runtime, not the UI, enforces cancellation and delivers one terminal result.
Other branches may continue. Whole-run Esc/Ctrl+C cancels all descendants,
including while an inspector or branch confirmation is open.

After cancellation preserve partial records and move input focus to a usable
location. Ignore obsolete events by run/task identity without discarding required
terminal results. Retain current terminal restoration on exit.

## Persistence and acceptance guide

Save child transcripts/tree with the owning private session. Coordinate one
session writer so sibling progress cannot overwrite each other's snapshot.
Load older snapshots without inventing child records. On resume show unfinished
nodes as interrupted; never relaunch them or restore approval/trust state.
Missing optional artifact storage degrades to the saved bounded transcript.

- Inspect a live nested tree and child transcript without losing a typed draft.
- Observe queued vs running vs waiting nodes, correct depth/model/budgets, and
  out-of-order completion attributed to the right parent.
- Cancel one branch, then the main run from inside inspection; descendants stop
  and partial records remain visible.
- Resize, inspect long child output, exit/resume, and load a legacy session.
- Compare main context and child usage during overlapping requests; unknown
  metrics stay unknown and no duplicate cost accounting appears.
