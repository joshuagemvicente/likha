# Feature: Hooks (user commands that observe Likha and can block)

**Status:** planned — decisions recorded 2026-10-07 (user interview); no
implementation started. Requirement: [v1-spec FR-45](../v1-spec.md#attention-and-automation-amendment-user-approved-2026-10-07).

## Context

Every comparable harness lets the user run their own code at fixed points of
the agent loop. Claude Code has about thirty hook events whose command
handlers exchange JSON over stdin/stdout. Gemini CLI has ten
(`BeforeTool`, `AfterTool`, `Notification`, …). OpenCode has plugin
interception points (`tool.execute.before/after`, `permission.ask`). OMP has
`pi.on` handlers that can block or rewrite a tool call. Sources are listed in
[context.md](context.md).

Likha has none. A user who wants to stop the model from reading `.env`, log
every command to a file, or play a sound when a run ends has to patch Likha.
Approvals are the only control, and they apply per proposal, not as a
standing rule the user writes once.

Hooks are not the executable plugins that v1-spec §2 keeps out of scope. A
hook adds no tool, no prompt text and no approval path. It is a command the
user wrote, configured in the user's private state directory, and it can only
watch, or say no.

## Role

Let the user attach their own commands to a small set of Likha events, so
they can record what happens and enforce rules of their own. A hook can make
Likha do less, never more. No hook can approve an edit, a command or an MCP
call, answer a question, or let a refused command run. Every block is visible
to the user and to the model.

## Scope

In scope: a private global `hooks.json`, command hooks with a JSON event on
stdin, observe-only events, blocking `pre_tool` and `prompt_submit` events, a
`/hooks` status view, and the same behavior on the main run, explore
children, profile tasks and headless runs.

Out of scope:

- Repository or project hook files (`.likha/hooks.json`, `.claude/settings.json`
  and similar). Opening a repository never adds a hook (decision 1).
- Approving, auto-answering or skipping a review or a question (decision 3).
- Rewriting a tool call's arguments or a tool result's content.
- Adding text to the model's context (drafting default D7).
- Handler kinds other than a local command: HTTP, MCP-tool, prompt or agent
  handlers.
- A plugin API, extension packages or a marketplace.

## Decisions (user interview, 2026-10-07)

1. **Configuration lives only in `<stateDir>/hooks.json`.** It is private and
   global. Likha reads no hook configuration from the repository, the working
   directory or any other harness's settings.
2. **Hooks are commands that receive one JSON event on stdin.**
3. **Hooks observe and block, never approve.** A `pre_tool` hook can deny a
   tool call, and the model receives a refusal carrying the hook's reason.
   No hook output can skip a review, answer an approval or question, widen a
   session grant, or make a refused or always-ask command run without its
   usual prompt.

## Drafting defaults (confirm in review)

These were not part of the interview. They are proposed so the spec is
complete, and each can change in review without touching decisions 1–3.

- **D1. Events.** `session_start`, `prompt_submit`, `pre_tool`, `post_tool`,
  `run_end` and `attention` (the FR-41 moments: a review or question opened,
  a run finished). Only `pre_tool` and `prompt_submit` can block.
- **D2. Matchers.** `pre_tool` and `post_tool` entries take an optional
  `match` list of glob patterns over the tool's canonical name (for example
  `run_command`, `edit*`, `mcp__github__*`). With no `match`, the hook runs
  for every tool. Other events take no matcher.
- **D3. Timeout.** Each hook has a `timeout` in seconds: default 10, maximum
  60. Likha kills the hook's process group when it runs out.
- **D4. Failures.** For a blocking event, a hook that fails, times out, or
  writes unreadable output blocks the call (fail closed) and says why. A
  hook may set `"on_error": "allow"` to fail open instead. A failing
  observe-only hook never blocks. It shows one warning.
- **D5. Environment.** Hooks run with `sh -c` in the repository root,
  with the same environment as model commands: no `LIKHA_API_KEY` and no
  `LIKHA_<NAME>_API_KEY`. Likha adds `LIKHA_HOOK_EVENT` and
  `LIKHA_SESSION_ID`.
- **D6. Bounds.** A hook's stdout and stderr are each read up to 64 KiB,
  and the rest is discarded. A block reason is cut to 1 KiB. `post_tool`
  sends at most 16 KiB of result content and says when it cut the rest.
- **D7. No context injection.** Hook output never reaches the model, except
  a block reason inside its refusal.
- **D8. Ordering.** Hooks for one event run one after another in file order.
  The first block wins, and later `pre_tool` hooks for that call do not run.
  Separate tool calls that already run in parallel run their hooks in
  parallel.
- **D9. Loading.** `hooks.json` is read once at launch. Edits apply after a
  relaunch. A file that other users can write is refused.
- **D10. `/hooks`.** It lists the file path, each hook's event, name and
  matcher, and its last outcome. It also shows a load error, if any.
- **D11. Headless (FR-44).** Hooks run the same way. A `pre_tool` block
  becomes the same refusal, and `attention` does not fire because no one is
  watching.
- **D12. Children.** Explore children and profile tasks fire `pre_tool` and
  `post_tool` with their agent identity. They never fire `prompt_submit` or
  `attention`.

## Functional changes

### Configuration

1. Likha reads hooks from `<stateDir>/hooks.json` only (decision 1). A
   missing file means no hooks, with no notice. Likha does not create the
   file.
2. The file maps event names to lists of hook entries. Each entry has a
   `command`, and optionally a `name`, `match`, `timeout` and `on_error`
   (D2–D4). Example:

   ```json
   {
     "hooks": {
       "pre_tool": [
         {"name": "no-dotenv", "match": ["read", "grep"], "command": "~/bin/deny-dotenv"}
       ],
       "run_end": [{"command": "afplay /System/Library/Sounds/Glass.aiff"}]
     }
   }
   ```

3. A file Likha cannot use (invalid JSON, an unknown event, an entry without
   a command, a bad pattern or timeout, writable by others) starts no hooks
   at all. It never starts some of them. Likha shows one error at launch
   naming the file and the problem, and `/hooks` repeats it. The session
   otherwise works normally.

### Running a hook

4. A hook receives exactly one JSON object on stdin, then end-of-file. Every
   event carries `version`, `event`, `session_id`, `repository` and `agent`
   (`main`, or a child's identity). Events add their own fields:
   - `session_start`: whether the session was resumed;
   - `prompt_submit`: the prompt text, and its origin (typed, steering,
     `/init`, `/install`);
   - `pre_tool`: the tool's canonical name, its source (built-in or the MCP
     server name), its effects (read, write, exec, network) and the validated
     arguments;
   - `post_tool`: the same, plus the result status, the exit code for
     commands, and bounded result content (D6);
   - `run_end`: the run's outcome (completed, failed, cancelled) and the
     error text on failure;
   - `attention`: what needs the user (a review, a question, a finished run).
5. A hook signals a block by exiting with status 2. Its stderr becomes the
   reason. A hook may also print `{"decision": "block", "reason": "…"}` on
   stdout. Exit status 0 with no block decision means "no objection".
6. Output that tries to approve (`"decision": "allow"`, `"approve"`, or any
   other value) is ignored. It counts as "no objection" and shows a warning
   that hooks cannot approve (decision 3).

### Blocking a tool call

7. `pre_tool` hooks run after a call's arguments pass validation and before
   any authorization: before command classification, session grants,
   auto-approval, an edit or MCP review, or a question opens. A blocked call
   therefore never opens a review and never uses a grant.
8. A blocked call returns a refusal to the model: `Blocked by hook
   "<name>": <reason>`. The tool did not run. The transcript shows the tool
   item as blocked, with the hook's name and reason, in the same place an
   ordinary refusal appears.
9. A call no hook blocks proceeds exactly as it would without hooks,
   including its review. A hook cannot make a call skip its review
   (decision 3). Approve-always grants and auto-approved command tiers still
   apply after the hooks have run. They never apply instead of them.

### Blocking a prompt

10. `prompt_submit` hooks run before a user prompt is sent to the model. A
    block sends nothing, keeps the prompt in the composer, and shows
    `Prompt blocked by hook "<name>": <reason>`. A blocked steering prompt is
    dropped from the queue with the same notice, and the other queued
    prompts stay.

### Observing

11. `session_start`, `post_tool`, `run_end` and `attention` hooks never
    change what Likha does. They run without blocking the UI. A slow
    observe-only hook never delays the next model request.
12. A hook failure is never silent. It shows one warning naming the hook and
    the cause (exit status, timeout, unreadable output). For blocking events,
    the call or prompt is blocked or allowed according to D4, and the
    warning says which.

### Seeing what is configured

13. `/hooks` shows the loaded hooks, or the load error, or "No hooks
    configured" with the file path (D10). `/hooks` is listed in `/help` and
    in the `/` autocomplete, and it works while a run is active.

### Safety

14. Hooks run without a review, because the user wrote them in their own
    private configuration, like MCP server commands in `mcp.json`. The docs
    say that a hook runs with the user's permissions, outside any review,
    and receives tool arguments and prompt text.
15. Provider credentials never reach a hook, neither in its environment
    (D5) nor in its event payload.

## Acceptance criteria

- With no `hooks.json`, every request, review and transcript is unchanged.
- A `pre_tool` hook matching `read` that exits 2 for paths ending in `.env`
  blocks that read. The model gets the refusal with the reason, and no
  review opens. Other reads proceed.
- A `pre_tool` hook on `run_command` that prints `{"decision":"allow"}` does
  not skip the command's review, and a warning says hooks cannot approve.
- A session-granted command is still blocked when a `pre_tool` hook says so.
- A `pre_tool` hook that times out blocks the call with a timeout reason.
  The same hook with `"on_error": "allow"` lets the call through with a
  warning.
- A `prompt_submit` block keeps the draft and sends no request.
- A `run_end` hook receives the outcome of completed, failed and cancelled
  runs, and a slow one does not delay the next turn.
- An invalid `hooks.json` starts no hooks, and the error appears at launch
  and in `/hooks`.
- `LIKHA_API_KEY` set in Likha's environment is absent from the hook's
  environment.
- An explore child's `read` is blocked by a matching `pre_tool` hook, and
  the event names the child agent.
- A repository containing `.likha/hooks.json` or `.claude/settings.json`
  hooks runs none of them.
