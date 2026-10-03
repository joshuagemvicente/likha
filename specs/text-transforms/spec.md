# Feature: Text transforms (user-defined input rewrites)

**Status:** draft

## Context

The precedent is Wispr Flow's **transforms**: user-defined rewrites applied to
text before it is sent — clean up dictated text, structure a rough idea — with
the transformed output landing where the user can review it. Likha already has
all the machinery this needs on the surface: an editable input buffer
(`m.input []rune` in `internal/app/tui.go`) that is fully user-editable until
Enter sends it to `runTurn`, and a slash-command dispatch that intercepts
prompts before they reach the model
([slash-commands/spec.md](../slash-commands/spec.md), FR-03).

The sibling feature
[custom-commands/spec.md](../custom-commands/spec.md) defines user-defined
markdown prompt commands created on shared storage machinery. Transforms are
its sibling, not its duplicate, and the difference is exactly one rule:

> **A custom command composes text and SENDS it as the user prompt. A text
> transform rewrites text and puts the result back INTO THE INPUT BUFFER,
> returning control to the user, who reviews it and sends it (Enter) or keeps
> editing — or cancels. Transforms never send anything by themselves.**

This review-before-send routing is the hard rule that distinguishes transforms
from custom commands, and it is the reason transforms are a separate feature
rather than an argument to custom commands.

## User-visible behavior

### `/transform` (no argument)

Lists the available transforms in the conversation view: the shipped default
`prompt-engineer`, plus each user-defined transform from the shared command
storage, with their descriptions. With no transforms defined beyond the
default, it says so clearly. `/transform` is added to the FR-03 reserved list;
it acts on the application, never on the conversation, and unknown or missing
names produce a visible error and are never sent to the model.

### `/transform <name>` — transform the current input buffer

Applies the named transform to the CURRENT input buffer text:

- The buffer must be non-empty (after trimming surrounding whitespace). If the
  buffer is empty or whitespace-only, Likha shows a visible error ("nothing in
  the draft to transform") and does not run the model.
- The captured buffer text is passed to the transform (the `$TEXT` placeholder
  in the transform's markdown template; the placeholder token follows whatever
  [custom-commands](../custom-commands/spec.md) resolves for its `$ARGUMENTS`
  precedent — one token, one rule, shared machinery).
- The transform runs as **one ordinary model call** (a single `runTurn`-style
  request, no subagents, no tool use, no agentic loop — see v1-spec §2: one
  active agent run at a time; a transform is not an agent run and must not
  become one).
- While the transform is in flight, the input area shows a **"Transforming…"
  status** (same role as the existing "thinking/working" activity line; the
  draft is not itself replaced until the result arrives).
- **Esc cancels:** the in-flight transform is cancelled (FR-04 cancellation
  semantics), the ORIGINAL buffer text is restored verbatim — every rune,
  cursor position included where the editor keeps one — and the "Transforming…"
  status clears. The original buffer text is never lost, on any path.
- **Model failure:** the original buffer is restored verbatim and the error is
  shown in the conversation view (FR-11: errors remain visible; the TUI stays
  usable). A partial/blocked stream result is a failure, never a half-applied
  buffer.
- On success, the **transformed output REPLACES the input buffer** in full.
  The user then reviews it and sends it with Enter as a normal prompt, or keeps
  editing it (all FR-17 bindings), or undoes it by further editing — no
  auto-send, no implicit Enter after the transform completes.

### `/transform <name> <text>`

Applies the transform to the given text instead of the buffer. `<text>` is
everything after the command and name (one blob, character-for-character, no
quoting rules — same single-blob argument stance as custom commands). The
behavior is otherwise identical: one model call, "Transforming…" status, Esc
cancel, and on success the transformed output replaces the input buffer (the
original `<text>` argument also remains visible as the typed command in the
conversation view's history, so the pre-transform text is recoverable by
reading, not by faith).

For `<text>` spanning multiple words no surrounding characters are required;
Likha takes the raw remainder. Empty remainder with `<name>` present behaves
exactly like `/transform <name>` (transforms the buffer).

### Routing (hard rule)

- A transform result is **never auto-sent**. Nothing reaches the model as a
  user prompt because a transform completed. The user's Enter does.
- Transforms do not short-circuit into a silent agent run, do not register or
  alter tools, and do not touch configuration. A transform file is untrusted
  input in exactly the sense [custom-commands/spec.md](../custom-commands/spec.md)
  defines; its only capability is: produce a string from its template plus the
  captured text, and put that string in the input buffer.
- `/transform` is inert during an active agent run or a pending approval,
  consistent with the other slash commands ([slash-commands/spec.md](../slash-commands/spec.md));
  nothing about a transform may interrupt or ride along with a pending review.
- The `//` escape is unaffected: `//transform x` reaches the model as the
  literal prompt "transform x".

### Shipped default transform: `prompt-engineer`

Likha ships exactly one built-in transform, named exactly `prompt-engineer`. It
is defined as markdown (the same template format as user-defined transforms),
so its full text below is the implementation source, copied verbatim:

```markdown
---
name: prompt-engineer
description: Rewrite a rough description into a rigorously structured prompt.
---

Rewrite the user's rough description between the TEXT markers below into a
rigorously structured prompt. Do not answer the request yourself, do not
attempt the task, do not invent requirements the user did not state or
contradict what they wrote. Your entire output is the rewritten prompt text
and nothing else — no preamble, no commentary, no code fence around the result.

TEXT:
$TEXT
END TEXT.

The rewritten prompt must contain exactly these sections, in this order, each
introduced by a markdown heading (`##`):

## Title
A short imperative title for the task derived from the description.

## Role & stance
The specific role the executor should adopt and the stance to bring to it
(e.g. "a careful reviewer who prioritizes correctness over cleverness").
Derived from the description; no generic filler.

## Task
The one-sentence task statement, phrased so its completion is observable.

## Context
Background the executor needs, drawn only from the description. Omit the
section if the description provides none.

## Inputs available
What material, files, data, or prior work the executor may draw on, as
stated in the description. Omit the section if none is stated.

## Output requirements
What the deliverable looks like: format, scope, and what counts as done.

## Constraints
Hard limits on how the work is done (time, size, scope, approach) as stated
in the description. Omit the section if none is stated.

## Do-nots
Actions to avoid, both stated by the user and directly implied by the role’s
stance. Omit the section if empty rather than inventing filler.

## Examples/References
Concrete examples or references from the description. Omit the section if
the description provides none.

## Execution checklist
Ordered list of items phrased as observable outcomes — each item begins a
sentence whose truth an outside observer can verify ("The diff contains no
changes outside X", "The command exits 0 on Y") — never phrased as steps
("first do", "then do").

Rules applied to every section:
- Use only information present in the description. Where the description is
  ambiguous, write the ambiguity as an explicit `[ASSUMPTION: …]` line inside
  the relevant section instead of silently resolving it.
- Never place bracketed content other than `[ASSUMPTION: …]`; invent no
  annotations of your own.
- Keep total length proportional to the description: no padding, no restated
  boilerplate.
```

The default transform's template ships rendered exactly as above; its file
lives beside the shipped implementation (the same loader that reads
user-defined transforms finds it, as a built-in that user files cannot shadow
— a user transform named `prompt-engineer` is refused with a visible error,
mirroring the reserved-name rule in slash-commands and custom-commands).

### Storage (shared with custom commands — dependency)

- User-defined transforms use the **same markdown storage machinery as
  custom commands** ([custom-commands/spec.md](../custom-commands/spec.md)):
  same loader, same frontmatter treatment, same private-state-directory and
  repo-scoped location thinking, same "markdown template, never code" rule.
- **Dependency note:** custom-commands is being specified in parallel. It owns
  the storage/frontmatter machinery; transforms depend on that machinery and
  differ from custom commands only in **output routing** — a custom command's
  result is SENT; a transform's result lands in the input buffer and requires
  the user's explicit send. This spec defines the routing; the storage loader,
  locations, precedence, load timing, and frontmatter schema remain custom-
  commands' open decisions until that spec resolves them.
- Until custom-commands' loader exists, this feature must not create a
  parallel loader of its own; implementation starts after both specs resolve
  their shared decisions.

## Open decisions (resolve before implementation)

1. **Transform parameters.** Can a user transform take parameters beyond the
   single captured text blob (e.g. `/transform mine terse <text>`)? Deferred:
   single-blob only for v1, per the same argument rules as custom commands.
2. **Nested/chained transforms** (`/transform a` whose output is fed to
   `/transform b`). Deferred — likely never. The review-before-send rule means
   each transform ends in the buffer where the user chooses the next step;
   chaining adds a magic path around that choice for no demonstrated need.
3. **Which model runs the transform.** The configured provider/model, or a
   fixed "cheap" model? This is a real trade-off: using the configured model
   keeps one provider story (and works when nothing cheap is configured) but
   burns the expensive model on a rewrite; a fixed cheap model cuts cost but
   adds a second model/provider surface to configure, verify, and explain.
   Undecided; both implementations share the single-run, cancellable, never-
   auto-send rules above, so the choice is isolated.

## Acceptance criteria

- [ ] `/transform` lists the shipped default and any user-defined transforms
      with descriptions; with none, it says so clearly.
- [ ] `/transform <name>` with an EMPTY input buffer shows a visible error
      ("nothing in the draft to transform") and runs no model call.
- [ ] `/transform <name>` with text in the buffer runs one ordinary model call
      and, on success, the transformed text REPLACES the buffer for the user
      to review.
- [ ] The transformed text is NEVER auto-sent: after a successful transform,
      the prompt is not submitted until the user presses Enter.
- [ ] While the transform is in flight, a "Transforming…" status is visible.
- [ ] Esc during the transform cancels it and restores the original buffer
      text verbatim, character-for-character, including the cursor position.
- [ ] A model error during the transform restores the original buffer text
      verbatim and shows the error in the conversation view. The original
      buffer text is recoverable after any failure — cancellation, error, or
      crash — without reaching for conversation history.
- [ ] A partial or interrupted transform result never leaves a half-written
      buffer: the buffer is either the original text or the complete result.
- [ ] `/transform <name> <text>` transforms the given text and replaces the
      buffer with the result; the typed command line remains visible in the
      conversation view as a record of the original text.
- [ ] Transforms are inert during an active agent run or pending approval.
- [ ] The `//` escape is unaffected: `//transform` reaches the model as a
      literal prompt, as today.
- [ ] `prompt-engineer` exists as a built-in transform, applies its template
      with the captured text at the `$TEXT` placeholder, and produces a
      structured prompt with the ten specified sections; it cannot be
      shadowed by a user file.
- [ ] A user transform file with invalid frontmatter or no body yields a
      clear error naming the file and is not listed or dispatchable, mirroring
      custom-commands' untrusted-input rule.
- [ ] A transform cannot execute shell code, alter tools or configuration, or
      send a prompt on its own; the only effect of applying one is buffer text.
- [ ] `go test ./...` passes before any of the above is described as verified.
