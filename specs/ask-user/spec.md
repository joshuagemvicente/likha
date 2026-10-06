# Feature: model questions with preserved steering state

**Status:** implemented (local) for the single-question contract (headless-tested;
interactive walkthrough pending). Questionnaire amendment (2026-10-05): implemented (local) — automated suite
green; walkthrough and live probe pending.
**Phase:** 3. **Product requirements:** FR-30 (amended 2026-10-05).

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

## Amendment 2026-10-05: questionnaires and asking rules

Requested with [install-command](../install-command/spec.md); applies to every
turn. The single-question form above stays valid and unchanged.

### Questionnaire form

One `ask_user` call may instead carry `questions`: one to four entries. Each
entry has a `question` (same 4 KiB bound) and optional options. An option is a
label (1–120 characters) with an optional description (up to 200 characters)
and an optional `recommended` flag. At most one option per question is
recommended; malformed calls, including a second recommended option, refuse
before any interaction. A call uses either the single-question form or
`questions`, never both.

The dialog shows one question at a time with an `n of N` progress marker. The
recommended option shows a **Recommended** tag and starts focused; without one,
the first option starts focused. Descriptions show muted under their option.
Free text stays available on every question. ↑/↓ choose, Enter answers and
moves to the next unanswered question, ←/→ move between questions so earlier
answers can be changed, Ctrl+S skips the current question, and Esc keeps its
cancel-run meaning (FR-04). Answering or skipping the last remaining question
submits the whole questionnaire.

The call returns exactly one result against the original call ID, listing per
question the chosen option label or free text, or that it was skipped. If every
question was skipped, the result is a refusal, never an empty success. All other
single-question rules (draft and queue preservation, serialization, no
permission grants, provider notice, exactly-once delivery, resume, child
refusal) apply to the questionnaire as a whole.

### When the agent asks

The main agent's instructions add these rules for every turn:

- Ask when a request has more than one reasonable reading and a wrong guess
  would cause real rework or change something hard to undo, and neither the
  conversation nor the repository settles it.
- Do not ask when the code, project conventions, or a sensible default answer
  it. Proceed and state the assumption in the reply.
- Check what can be checked with read tools before asking.
- Group related choices into one questionnaire of at most four questions; put
  the recommended option first with a one-line reason.
- Never use a question to get permission; approvals stay separate (FR-08).
  Never ask for passwords, API keys, or tokens.

These rules guide the model; they are not a classifier. Automated tests check
that the instructions are present. Whether the model follows them is verified
only by live probe.

### Amendment acceptance

- Ask a four-question questionnaire with recommended options and
  descriptions; change an earlier answer with ←; verify one result carrying
  every answer against the original call ID.
- Skip one question, then all questions; verify the per-question skip and the
  all-skipped refusal.
- Send two recommended options, five questions, and both forms in one call;
  each refuses before the dialog opens.
- Narrow terminal: progress, tag, descriptions, and controls stay usable.
- Live probe: an ambiguous request triggers a questionnaire; a clear request
  proceeds with a stated assumption.
