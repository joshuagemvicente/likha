# Mid-turn follow-up UX: Claude Code and OpenCode

**Date:** 2026-10-03 · **Status:** research note feeding [spec.md](spec.md) (steering-prompts).
**Method:** first-party evidence only: official interactive/keybinding docs and OpenCode source pinned to the repository's current `dev` HEAD (`108b988a08227df45417f27905a4d6b27ad49b6d`, fetched 2026-10-03). Docs claims and source-confirmed details are distinguished below. Claude Code is closed source; its queue internals are undocumented.

## Findings

### Claude Code

- **Enter queues; it does not interrupt.** A message typed and submitted while Claude works becomes a queued entry in the conversation. The docs say queued entries are shown in gray, and sent/queued entries stay gray until Claude starts responding to them [1].
- **Delivery timing depends on the work.** For plain messages queued during tool calls, Claude receives them as soon as those calls finish, within the same turn. If the turn ends with queued messages, they are sent automatically in submission order, without another keypress [1]. This is the documented behavior; the CLI source and exact provider-message representation are unpublished.
- **Several submissions stay distinct in the UI and ordered.** Docs refer to multiple “queued entries” and say they are sent in typed order. They do not specify whether the provider request represents adjacent entries as separate user messages or merges them [1].
- **Controls:** `Ctrl+Enter` (or `Ctrl+X Ctrl+S`) sends the queue plus any current draft now. Depending on current work, Claude interrupts response-only/non-backgroundable work or backgrounds backgroundable work and continues in the same turn. `Esc` interrupts and sends what was already queued, but not the draft. `Up` from the first input line recalls queued items into the editor [1].
- **No delivery-mode selector is documented.** `Shift+Tab` cycles permission modes, not follow-up delivery modes [1].

### OpenCode

- **Official docs are quiet about queue semantics.** The keybind reference documents `Enter` as `input_submit`, `Escape` as `session_interrupt`, and `Ctrl+Enter` as one of the newline bindings. It does not describe what submitting during a run does, how pending prompts are delivered, or a user-facing queue/steer mode [2].
- **Current TUI source confirms a live composer and a visible pending state.** The session view disables the prompt for pending permissions/questions, not merely because a session is running; Enter submits through `session.prompt`. A user message after the in-progress assistant is rendered with a `QUEUED` badge [3]. In the TUI's legacy prompt loop, each submission is written as its own user message, and the loop reloads history before each provider request; a message present by the next loop iteration is therefore available after the current step/tool work settles. Source does not specify a guarantee for the narrow race where input arrives after the loop's final exit check. It retains separate user-message records in order; the final provider serialization is delegated to `convertToModelMessages`, so source alone does not prove whether adjacent user messages are coalesced on the wire [3].
- **The same revision also contains a newer, explicit V2 delivery model—but the TUI submit path above does not pass its `delivery` field.** The V2 core API has `steer` and `queue`; omitted delivery defaults to `steer`. Steers are promoted in admission order at a provider-turn boundary, and all eligible steers can be promoted together. A queue item is promoted one at a time when the current session drain would otherwise become idle; remaining queue items run in later drains [4]. They remain distinct admitted prompt/message IDs, rather than being concatenated into one prompt [4]. This is source-confirmed core/API behavior, not a documented TUI mode or an official docs promise.
- **Interruption/send-now:** the TUI binds Escape to interruption and shows “again to interrupt”; its handler aborts on the second press. The official keybind docs make `Ctrl+Enter` a newline, not send-now. I found no send-now action in the inspected TUI submit path [2][3].
- **Mode selector:** no delivery selector is documented. V2 exposes a programmatic/API field, but the current TUI's `session.prompt` call does not set it [2][3][4].

## Recommendation for Likha *(judgment, not source fact)*

Keep **one default mode**: Enter queues for the next safe provider-request boundary. The comparison supports a clear default queue with explicit pending state; it does not establish that users benefit from choosing a delivery mode on every submission. A separate send-now action can be added later without turning delivery into a persistent mode toggle.

Make timing and end-state explicit in the composer/status copy, not just the count: e.g. **“Enter queues · sent after current tool work, before the next model request”** while running, and **“N queued · run ended; Enter sends next turn · Esc clears”** for a held batch. Keep queued rows visibly distinct until the agent actually accepts them into history; change the label to “Sent” (or equivalent) at that point. This addresses the discoverability gap without changing Likha's safe-point contract. If M2 send-now ships, explain that it interrupts the current response but waits for active tool work to settle; do not imply it kills a tool.

## Sources

[1] Claude Code, “Interactive mode,” sections “Queue messages while Claude works,” “When Claude Code sends what you queued,” and “Take back what you queued.” https://code.claude.com/docs/en/interactive-mode · accessed 2026-10-03.

[2] OpenCode, “Keybinds.” https://opencode.ai/docs/keybinds · accessed 2026-10-03.

[3] OpenCode source at `108b988a08227df45417f27905a4d6b27ad49b6d` (current `dev` HEAD fetched 2026-10-03): [`packages/tui/src/component/prompt/index.tsx`](https://github.com/anomalyco/opencode/blob/108b988a08227df45417f27905a4d6b27ad49b6d/packages/tui/src/component/prompt/index.tsx) (submit and interrupt); [`packages/tui/src/routes/session/index.tsx`](https://github.com/anomalyco/opencode/blob/108b988a08227df45417f27905a4d6b27ad49b6d/packages/tui/src/routes/session/index.tsx) (disabled/pending logic and `QUEUED` badge); [`packages/opencode/src/session/prompt.ts`](https://github.com/anomalyco/opencode/blob/108b988a08227df45417f27905a4d6b27ad49b6d/packages/opencode/src/session/prompt.ts) (legacy prompt, loop, history-to-request); [`packages/opencode/src/session/message-v2.ts`](https://github.com/anomalyco/opencode/blob/108b988a08227df45417f27905a4d6b27ad49b6d/packages/opencode/src/session/message-v2.ts) (message conversion). Accessed 2026-10-03.

[4] OpenCode source at the same pinned commit: [`packages/core/src/session.ts`](https://github.com/anomalyco/opencode/blob/108b988a08227df45417f27905a4d6b27ad49b6d/packages/core/src/session.ts) (V2 API/default); [`packages/core/src/session/input.ts`](https://github.com/anomalyco/opencode/blob/108b988a08227df45417f27905a4d6b27ad49b6d/packages/core/src/session/input.ts) (admission and promotion); [`packages/core/src/session/runner/llm.ts`](https://github.com/anomalyco/opencode/blob/108b988a08227df45417f27905a4d6b27ad49b6d/packages/core/src/session/runner/llm.ts) (delivery at turn/drain boundaries); [`packages/core/src/session/run-coordinator.ts`](https://github.com/anomalyco/opencode/blob/108b988a08227df45417f27905a4d6b27ad49b6d/packages/core/src/session/run-coordinator.ts) (wake/follow-up drain). Accessed 2026-10-03.

## Limits

Claude Code’s internal queue data model and wire-level merging are not published. OpenCode official docs do not document mid-turn delivery. Its pinned repository contains both the TUI's legacy prompt loop and a distinct V2 core delivery API; do not treat V2's `queue`/`steer` distinction as a user-facing TUI option without further verification.
