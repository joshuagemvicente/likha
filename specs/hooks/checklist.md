# Checklist: Hooks

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Configuration

- [ ] Hooks load only from `<stateDir>/hooks.json`. A repository's
      `.likha/hooks.json` or `.claude/settings.json` runs nothing.
- [ ] With no `hooks.json`, Likha behaves byte-for-byte as before: same
      requests, reviews and transcript.
- [ ] An invalid file (bad JSON, unknown event, missing command, bad
      matcher or timeout, writable by others) starts no hooks. The error
      shows at launch and in `/hooks`.
- [ ] Likha never creates `hooks.json`.

## Blocking

- [ ] A `pre_tool` hook that exits 2 blocks the call. The model gets
      `Blocked by hook "<name>": <reason>`, the tool does not run, and no
      review opens.
- [ ] A `{"decision":"block"}` on stdout blocks the same way.
- [ ] A hook's `allow`/`approve` output never skips a review, never answers
      a question, and shows a "hooks cannot approve" warning.
- [ ] Session grants and auto-approved command tiers never bypass a
      `pre_tool` hook.
- [ ] A failing or timed-out blocking hook blocks (fail closed), unless it
      sets `on_error: allow`, in which case a warning shows.
- [ ] A blocked `prompt_submit` keeps the draft and sends nothing. A blocked
      steering prompt is dropped with a notice, and the rest of the queue
      stays.

## Observing

- [ ] `session_start`, `post_tool`, `run_end` and `attention` hooks receive
      their documented payloads and never change Likha's behavior.
- [ ] A slow observe-only hook does not delay the UI or the next request.
- [ ] Every hook failure shows one warning naming the hook and the cause.

## Safety and scope

- [ ] No `LIKHA_API_KEY` or `LIKHA_*_API_KEY` reaches a hook's environment
      or payload.
- [ ] Hook output never reaches the model, except a block reason inside a
      refusal.
- [ ] Explore children and profile tasks fire `pre_tool`/`post_tool` with
      their identity, and never `prompt_submit` or `attention`.
- [ ] Headless runs apply the same blocks and report them as refusals.

## Surfaces

- [ ] `/hooks` shows the loaded hooks with their last outcome, the load
      error, or "No hooks configured". It works during a run.
- [ ] `/hooks` appears in `/help` and in the `/` autocomplete.
- [ ] Blocked tool items and hook warnings are legible in ASCII mode and
      without color.
- [ ] `docs/usage.md` documents the format, events, payloads, exit codes
      and the safety note.
