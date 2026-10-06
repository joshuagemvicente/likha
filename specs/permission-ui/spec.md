# Feature: Approve/Decline buttons for permission reviews

**Status:** implemented (local) — automated suite green; real-TUI
walkthrough outstanding.

## Context

Likha's approval gate is keyboard-invisible. The review screen renders the
proposal body (`internal/tui/view.go:78-90`), and the decision is two
unlabelled letter keys: `y`/`Y` approves only after every review page has
been seen (`tui.go:719-735`), `n`/`N` rejects (`tui.go:736-747`). The only
affordance is the status-line fragment `Y/N PgUp/PgDn ^C`
(`status_line.go:80,88`) plus a composer text override
(`composer.go:52-58`). Nothing on screen looks like a decision; a user who
does not already know the chords has to read the status bar, and the letter
keys say "yes/no" to nothing in particular.

Every reference agent renders the decision as *labelled, focused options*
navigated with the arrow keys and confirmed with Enter; none of them relies
on memorised `y`/`n` (Claude Code removed those defaults in v2.1.280).

## Research findings (recorded 2026-10-02)

**Claude Code** (`code.claude.com/docs/en/permissions`, `…/interactive-mode`,
`…/keybindings.md`, fetched 2026-10-02):

- A permission prompt is a titled block: what Claude is about to do, the
  exact command, the line "This command requires approval", then "Do you
  want to proceed?" with a focused option list. The Bash/Manual example
  shows four options: `Yes`; `Yes, and don't ask again for: npm test *`;
  `Yes, and switch to auto mode`; `No`. The footer lists `Esc to cancel`
  and `Tab to amend` (open a comment field on Yes/No to send feedback).
- `Confirmation` context bindings: `Enter` confirm, `Esc` decline,
  `↑/↓` previous/next option, `Tab` next field, `Space` toggle.
- The docs record that `y`/`n` were default confirmation bindings before
  v2.1.280 and are no longer defaults (user-rebindable only).

**OpenCode** (`opencode.ai/v2/docs/permissions`, issue anomalyco/opencode
#7954, fetched 2026-10-02):

- When a rule resolves to `ask`, the client replies with one of three
  labelled choices: **Allow once** (`once`), **Allow always** (`always` —
  saves the tool's proposed patterns), **Reject** (`reject`, which also
  rejects every other pending request in the session).
- TUI interaction (reported in issue #7954): `←/→` (or `h/l`) select among
  the three; `Enter` confirms; the footer shows `⇆ select · Enter confirm`.
  The issue documents that older single-letter shortcuts (`enter`/`a`/`d`)
  were removed in the permission rework — the direction is explicit
  options, not letter keys.

**OMP** (omp docs `approval-mode.md`, `keybindings.md`, fetched 2026-10-02):

- The approval prompt body is specified: `Allow tool: <name>`, `Origin:
  MCP server tool` when applicable, `Reason: <reason>`, and per-tool
  details (command, path, code, browser action, subagent assignment).
- The public keybinding table contains no approval chords — there are no
  `y`/`n` bindings. The decision surface is an approve/deny selector
  (docs-derived; exact labels are not in the public keybinding table).
- Where a client owns the gate (ACP), decisions route through
  `session/request_permission`; a rejected, cancelled, or unsupported
  prompt rejects the tool call — never a silent allow.

**The consistent shape:** a labelled option set, exactly one focused
option, arrow/Tab navigation, Enter to confirm, Esc to back out — no
letter-key approval anywhere.

## User-visible behavior

### The decision bar

The review screen keeps its current title, warnings, and scrollable body
(`view.go:78-90`). While a review is pending, the composer area — otherwise
dead, since editing is inert during a review — is replaced by a pinned
decision bar, always visible below the proposal:

```
Edit · internal/tui/tui.go
--- a/internal/tui/tui.go
+++ b/internal/tui/tui.go
@@ … diff body (scrollable) …

> [ Approve ]    [ Decline ]
  ←/→ choose · Enter confirm · Esc cancel run
```

- Two buttons, labelled exactly **Approve** and **Decline**.
- Exactly one is focused; focus is always visible — the focused button
  carries the dialog-cursor convention (`> ` marker plus the `Selected`
  role, which paints a full-row band in every theme family that has one;
  `dialog.go:549,608` is the precedent) and unfocused buttons render muted.
- The bar does not render the draft, the caret, or composer borders; it
  occupies the composer's reserved rows, so no layout math changes.

### Keyboard

- `←`/`→` move focus; `Tab`/`Shift+Tab` cycle it (the Claude Code
  `confirm:nextField` analogue). `↑`/`↓` are not bound.
- `Enter` confirms the focused button.
- `Esc` / `Ctrl+C` keep their current meaning: cancel the run (FR-04),
  clearing the review without a decision.
- No letter keys decide anything: `y`, `n`, and every other rune are inert
  while a review is pending, exactly as editing is today.

### Approve readiness (the existing gate stays)

- Approve is available only after every review page has been seen — the
  user has scrolled to the end of the proposal
  (`scroll.go:238-259` `markSeenFromScroll`, the same condition the `y`
  handler enforces today via `reviewSeen`).
- Until then, Approve renders muted and activating it changes nothing:
  the status shows `Scroll to the end to approve` (the reworded gate
  status, one shared constant) and no reply is sent to the agent.
- Decline is always available.

### Outcomes (unchanged semantics)

- **Approve** sends the approval for that specific proposal and sets
  `Executing approved <kind>` (the `edit_file`/`run_command`/MCP paths
  keep their exact current behavior and safety checks).
- **Decline** rejects without executing, records the rejection visibly,
  and lets the model continue — the current `n` semantics.
- After either decision the bar disappears with the review and the composer
  returns; a page-gate refusal leaves the review open.

### Narrow terminals and themes

- At 40 columns the bar renders `> [ Approve ]  [ Decline ]` with the
  short hint `←/→ Enter Esc`; the no-overflow invariant tests extend to it.
- Focus must be visible on every theme family, including `default` (whose
  `Selected` role has no band) via the `> ` marker.
- Buttons are keyboard-focused, not mouse-clickable: mouse capture stays
  off (FR-14) so native text selection keeps working. All three reference
  tools are keyboard-driven dialogs too.

## Interactions and edge cases

- **Steering queue (specs/steering-prompts):** the review still blocks
  queueing and editing; queued messages are delivered after the tool
  settles (approved or rejected), unchanged.
- **Resize during review:** focus and gate state survive; the bar re-fits.
- **Persistence:** nothing new is stored; `reviewFocus` is transient like
  `pending` and reset on every approval event.
- **Nerd fonts:** the review marker behavior (`reviewMark`) is unchanged.
- **No approval-semantics change:** what may be approved, the per-proposal
  approval scope, stale-edit checks, and command warnings are untouched.
  *Amended 2026-10-04 by [command-permissions](../command-permissions/spec.md):*
  that feature changes command approval semantics, not this one. Command
  reviews may show a third button between Approve and Decline: **Allow for
  session** (ask-tier commands) or **Trust repo checks** (verification checks
  in an untrusted repository or with a changed fingerprint). Always-ask
  reviews keep two buttons and add a `Why this asks:` line; edit and MCP
  reviews keep two buttons. Below 50 columns the labels shorten to
  `[Approve] [Session] [Decline]` or `[Approve] [Trust] [Decline]`. Focus
  moves across all visible buttons with `←/→` and `Tab`/`Shift+Tab`; the
  third button shares Approve's read-to-end gate; the two command warnings
  stay.
  *Amended 2026-10-05 by [approve-always](../approve-always/spec.md)
  (implemented (local)):* the third button on edit, MCP, and Ask-tier command reviews is
  labelled **Approve always** (`[Always]` when the wide row does not fit
  with three columns spare: below 53 columns); **Trust repo checks** keeps
  its label and its 56-column breakpoint. Always-ask commands,
  warning-carrying edits, and `/init` proposals keep two buttons. Approve
  always shares Approve's read-to-end gate.

## Functional changes (to apply in v1-spec.md at implementation start)

- **FR-22 added.** "An approval review presents the decision as labelled
  actions — **Approve** and **Decline** — with exactly one focused action,
  moved with the arrow keys or Tab and confirmed with Enter; the focused
  action is visibly highlighted on every theme. Approve remains unavailable
  until the review has been read to its end; Decline rejects the proposal
  and lets the model continue. Escape keeps its cancel-run meaning
  (FR-04). No letter-key approval shortcuts exist."
- FR-06/FR-07/FR-08/FR-09 and the approval scope are unchanged; FR-14's
  mouse-capture rule is unchanged (keyboard-focused buttons, no clicks).
  *Amended 2026-10-04 by [command-permissions](../command-permissions/spec.md):*
  FR-08 and the approval scope were later amended by that feature, which
  adds the third command-review button described above; FR-22's two-button
  description remains accurate for edit, MCP, and always-ask reviews.

## Open decisions (recommended defaults; confirm or override)

1. **Default focus:** recommended **Approve** (first option, matching
   Claude Code and OpenCode). Alternative: **Decline** for an
   accident-resistant default.
2. **Esc:** recommended **cancel the run** (FR-04, current behavior);
   alternative: decline the action and let the model continue (Claude
   Code's `Esc` = No).
3. **Placement:** recommended **pinned decision bar replacing the composer**
   (always visible, zero layout-math change); alternative: append the
   buttons to the end of the proposal body (reachable only by scrolling,
   which self-enforces the gate but hides the affordance).

## Acceptance criteria

- [ ] While a review is pending, `[ Approve ]` and `[ Decline ]` render
      with exactly one focused button, visible on every theme family;
      `←/→`/`Tab` move focus and `Enter` confirms the focused action.
- [ ] No letter key approves, declines, or rejects; no `Y/N` or `Yes/No`
      wording remains in the UI, README, or help surfaces.
- [ ] Approve before reading to the end is refused with a visible
      `Scroll to the end to approve` status and sends no reply; after
      reaching the end, Approve executes the current approval path
      unchanged.
- [ ] Decline rejects without executing and records the rejection; the
      model continues.
- [ ] `Esc`/`Ctrl+C` still cancel the run from a pending review.
- [ ] The bar renders without overflow at 40×12 and every composer style.
- [ ] `go test ./...` and `go test -race ./...` pass from the project root.
