# Checklist — Text transforms (user-defined input rewrites)

Observable outcomes. Mirrors the acceptance criteria in
[spec.md](spec.md); wording follows v1-spec.md FR-03 (commands never sent to
the model), FR-04 (cancellation), FR-11 (visible errors), FR-17 (editable
draft).

## Listing and dispatching

- [ ] `/transform` lists the shipped default `prompt-engineer` and every
  user-defined transform, with descriptions; with none beyond the default it
  still names the default and does not look broken.
- [ ] `/transform <name>` for a name that is not defined shows a visible
  error, is never sent to the model, and leaves the draft untouched.
- [ ] `/transform`, application and conversation state still behave as
  before: unknown slash commands keep their error, reserved commands keep
  their behavior, and `//transform …` reaches the model as a literal prompt.

## Transforming the buffer (`/transform <name>`)

- [ ] With an empty (or whitespace-only) input buffer, `/transform <name>`
  shows "nothing in the draft to transform" and makes no model call.
- [ ] With text in the buffer, applying a transform runs exactly one model
  call; no tools execute and no agent loop starts.
- [ ] While the transform is in flight, "Transforming…" is visible in the
  input/activity area and the original draft text is still on screen.
- [ ] On success, the transformed text replaces the input buffer in full and
  nothing is sent: the user reviews it, edits it (FR-17 bindings all work),
  or sends it with Enter as an ordinary prompt.
- [ ] The buffer is never in a half-written state: it shows either the exact
  original text or the complete result, never a mix.
- [ ] Esc during "Transforming…" cancels the model call and restores the
  original buffer verbatim, character-for-character, cursor position intact.
- [ ] A model stream that errors or fails before completing restores the
  original buffer verbatim and shows the error in the conversation view; the
  TUI stays usable and the original buffer text is recoverable after any
  failure without scrolling conversation history.

## Transforming given text (`/transform <name> <text>`)

- [ ] `/transform <name> <text>` transforms the given text (the remainder
  after the name, verbatim, no quoting rules) and the result lands in the
  input buffer, pending the user's own Enter.
- [ ] The typed `/transform …` line remains in the conversation view as a
  record of the pre-transform text.
- [ ] `/transform <name>` with no remainder behaves identically to the
  buffer variant above.

## Shipped default

- [ ] `prompt-engineer` is listed by `/transform` with its description.
- [ ] Applied to a rough description, `prompt-engineer` produces a prompt
  with exactly the ten specified sections in order: Title; Role & stance;
  Task; Context; Inputs available; Output requirements; Constraints;
  Do-nots; Examples/References; Execution checklist — with checklist items
  phrased as observable outcomes, not steps.
- [ ] A user-defined transform named `prompt-engineer` is refused with a
  visible error at load and never dispatches; the built-in cannot be shadowed.
- [ ] The shipped template matches the spec's verbatim markdown exactly
  (diffing the shipped file against the spec catches drift).

## Shared storage and safety (custom-commands dependency)

- [ ] User-defined transforms load from the same markdown machinery as
  custom commands — no parallel loader, no second location scheme.
- [ ] A transform file with invalid frontmatter or no body produces a clear
  error naming the file, is never dispatchable, and never silently skipped.
- [ ] `/transform` is inert during an active agent run or pending approval,
  like the other slash commands.
- [ ] Applying a transform cannot execute code, alter tools, change stored
  configuration, or send anything to the model by itself — the only
  observable effect of a transform is input-buffer text awaiting the user's
  explicit send.
- [ ] After every transform outcome (success, cancel, error), a later `Enter`
  (or deliberate editing) is the only thing that ever submits a prompt; the
  transformed text is never auto-sent.

## Gate

- [ ] `go test ./...` passes and the feature smoke (apply, cancel, error
  paths in the real TUI) is recorded per `specs/feature-test-plan.md` before
  any of the above is described as verified.
