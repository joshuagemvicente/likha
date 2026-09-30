# Feature: Custom commands (user-defined skills)

**Status:** draft.

## Context

Users of agent TUIs expect to reuse their own prompt recipes as typed
commands. The precedent is Claude Code's custom slash commands
(`.claude/commands/*.md`: a markdown file whose filename becomes the command
and whose body is a prompt template with `$ARGUMENTS` substituted) and OMP
skills: both are **prompt templates, not code**. Lisa already reserves the
name `/skills` in the TUI but it is inert — `internal/skills/skills.go` is an
explicit placeholder ("`/skills` in the TUI is intentionally reserved but
inert"), and [slash-commands/spec.md](../slash-commands/spec.md) excludes
`/skills` from scope "until a skills feature exists".

This feature defines that skills feature: **user-defined slash commands**,
each a markdown file the user authors. It activates the reserved-but-inert
`internal/skills` package.

**Explicitly NOT a plugin runtime.** v1-spec.md §6 states v1 has no plugin
architecture, and this feature does not introduce one. A custom command is
markdown read from disk and expanded into conversation text. There is no
execution, ever: no shell invocation, no scripts, no tool registration or
allowlist changes, no code loading, no hooks. The only effect of invoking a
custom command is that Lisa composes a prompt string and treats it exactly as
if the user had typed it. If this feature ever needs to run anything, it is a
different spec contradicting v1-spec.md.

## User-visible behavior

- A user command is a markdown file whose name (filename without `.md`)
  becomes a slash command and whose body is a prompt template. Proposed
  locations:
  - repository `.lisa/commands/*.md` — repo-scoped, shareable via version
    control;
  - `<stateDir>/commands/*.md` — global, beside `config.json` /
    `providers.json` / `mcp.json` in the private state directory.
  Whether both locations exist, and which wins when the same command name is
  defined in both, is an open decision (below).
- `/skills` stops being inert and lists the available user commands with
  their descriptions. With none defined, it says so clearly. It does not
  change the reserved built-in commands.
- A user command is invoked like a built-in: `/<name> [arguments]`. The
  template body is expanded with an arguments placeholder substituted by what
  the user typed after the command (placeholder token and argument rules are
  an open decision; `$ARGUMENTS` is the precedent). The expansion is sent as
  the user prompt — **ordinary conversation text**. The approval flow is
  unaffected: any edit or shell command the model then proposes goes through
  the normal FR-06/FR-08 review; a custom command never pre-approves,
  pre-authorizes, or skips any review.
- A command name that collides with a reserved command is **refused** with a
  visible error (at load and when it would be listed); it is never silently
  shadowed by a user file or vice versa.
- An unknown `/word` that is neither reserved nor a defined user command
  keeps the existing unknown-command behavior from
  [slash-commands/spec.md](../slash-commands/spec.md): visible error in the
  conversation view, never sent to the model, draft text restored.
- A command file with invalid frontmatter, or with no body, produces a clear
  error naming the file, and is never dispatched.

## Security stance (hard rule)

Command files are **untrusted input**, in the same class as repository
content (v1-spec §5: repository content cannot override user approval
decisions). A command file can never:

- execute shell commands or any code;
- register, alter, disable, or allowlist agent tools;
- change provider, model, theme, or any stored configuration;
- weaken or bypass the edit/command approval flow.

The entire capability of a custom command is: produce a prompt string from
its own file contents plus the arguments the user typed. Tests for this
feature must demonstrate that the only observable effect of a loaded command
file is prompt text in the conversation.

## Open decisions (resolve before implementation)

1. **Frontmatter schema.** Candidate fields: `name` (or always
   filename-derived), `description`, and an `allowed-tools`-style field. An
   `allowed-tools` equivalent implies runtime policy, which this spec
   explicitly rejects; if kept at all it could only be advisory text shown in
   `/skills`, never enforced behavior. Recommendation for v1: `description`
   only, name from the filename.
2. **Locations and precedence.** Repo only, state dir only, or both; if both
   exist, does the repo file win, the state file win, or is the collision an
   error? (Tension: `.lisa/commands` inside an untrusted repository vs.
   treating command files as untrusted input regardless of location.)
3. **Load timing.** Hot reload on file change vs. load once at startup (with
   `/skills` re-reading?).
4. **Arguments.** Placeholder token name; a single blob vs. positional
   arguments; quoting rules for multi-word arguments; behavior when the
   template has no placeholder but arguments were given.
5. **`/skills` scope.** Does it list anything MCP-provided? Recommended: no —
   MCP servers and tools have `/mcp`; `/skills` lists user-defined prompt
   commands only.
6. **Interaction with existing rules.** The `//` escape and
   inert-during-active-run rules from [slash-commands/spec.md](../slash-commands/spec.md)
   apply unchanged; confirm.

## Functional changes (to apply in v1-spec.md when started)

- **FR-03 extension:** the reserved-command list gains `/skills`; the
  sentence gains that user-defined commands from markdown files are also
  dispatched, that reserved names cannot be overridden, and that a custom
  command's expansion is sent as the user prompt.
- A new functional requirement, in v1-spec.md's tone: "The user can define
  custom slash commands as markdown prompt templates in the repository or the
  private state directory; `/skills` lists them; an invoked command's
  template, with the typed arguments substituted, is sent as the user prompt
  and never executes anything or changes tools or configuration."
- README command reference documents `.lisa/commands/*.md` (and the state
  directory, per the location decision) and the untrusted-input rule.

## Acceptance criteria (draft)

- [ ] `/skills` lists defined user commands with descriptions and shows a
      clear empty-state message when none exist.
- [ ] `/<name> [arguments]` expands the template with the arguments
      substituted and sends the expansion as an ordinary user prompt.
- [ ] The expansion receives no special privileges: proposed edits and
      commands after a custom command go through the unchanged FR-06/FR-08
      approval flow.
- [ ] Reserved command behavior is unchanged; a user file named after a
      reserved command is refused with a visible error and never dispatched.
- [ ] Unknown `/word` keeps the existing error: visible, not sent to the
      model, draft restored.
- [ ] A file with invalid frontmatter or no body produces a clear error
      naming the file and is never dispatched.
- [ ] Custom commands are inert during an active run or pending approval,
      consistent with the other commands.
- [ ] The `//` escape still sends a literal slash to the model.
- [ ] A loaded command file cannot cause shell execution, tool changes, or
      configuration changes (test demonstrates the only effect is prompt
      text).
- [ ] `go test ./...` passes before any of the above is described as
      verified.
