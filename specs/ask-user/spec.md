# Feature: model questions with preserved steering state

**Status:** planned. **Phase:** 3. **Product requirements:** FR-30.

## Tool and interaction

Main-only `ask_user` accepts one nonempty `question` and optional `options`
of distinct human-readable strings. Bound the question to 4 KiB, choices to
eight entries of 120 characters, and free-text answer to 8 KiB. Reject malformed
calls before opening interaction. Show the complete question and source identity
with wrapping/paging, including on narrow terminals.

Offer optional choices plus an always-available free-text answer. Selecting a
choice or submitting text returns one answer tool result against the original
call ID. Give users an explicit `Skip` action that returns a refused/unanswered
result, not an empty successful answer. Do not treat an answer as permission
for an edit, command, MCP trust, or network grant.

Pause main model/tool progression until answer, skip, failure, or cancellation.
Serialize this interaction with approval dialogs. Preserve the existing composer
draft and queued steering prompts separately; answer input cannot accidentally
become a steering message, and queued text cannot silently answer the question.
Restore draft/focus afterward. Inform users that answer text reaches their
configured provider; do not ask for API keys through this tool.

Esc/Ctrl+C cancels the active run, clears the pending question, and preserves
held steering state according to FR-21. Ignore later submissions against an
expired/cancelled question. Answer delivery is exactly once across UI updates.
Explore children cannot invoke ask_user; a forged call returns a refusal.

## Persistence and acceptance

Persist the request and completed answer/skip/cancel result as conversation
events, not live reply channels or pending input. On app interruption/resume,
record the unanswered request as interrupted; do not reopen it, replay answers,
or start a new model request merely to ask it again. The model may ask a fresh
question after a new user-authorized turn.

- Answer with a choice and free text; verify the exact tool-call association.
- Keep a draft/queue while answering, then skip; steering state stays separate.
- Cancel or exit while pending, submit a stale answer, and resume; no duplicate
  or inferred response appears.
- Attempt ask_user from explore and while another interaction is pending;
  children refuse and the main interaction coordinator serializes safely.
- Resize and page a long question; complete wording and controls remain usable.
