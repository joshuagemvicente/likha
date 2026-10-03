# Feature: custom slash command templates (deferred)

**Status:** planned, deferred beyond the approved tools/agents phases.

This draft describes prompt templates only. The separate
[Markdown skill spec](../markdown-skills/spec.md) owns `/skills`, `/skill`, and
the global SKILL.md loader. This feature would use `/commands` for template
discovery; its remaining format/location decisions require separate approval.

## Context

Users of agent TUIs expect to reuse their own prompt recipes as typed
commands. The precedent is Claude Code's custom slash commands
(`.claude/commands/*.md`: a markdown file whose filename becomes the command
and whose body is a prompt template with `$ARGUMENTS` substituted). Likha's
templates produce prompt text only; this does not imply foreign skill formats
share the same executable-resource or permission semantics. Actual Markdown
skills have their own discovery/loading contract in the linked Phase 3 spec.

**Explicitly NOT a plugin runtime.** v1-spec.md §6 states v1 has no plugin
architecture, and this feature does not introduce one. A custom command is
markdown read from disk and expanded into conversation text. There is no
execution, ever: no shell invocation, no scripts, no tool registration or
allowlist changes, no code loading, no hooks. The only effect of invoking a
custom command is that Likha composes a prompt string and treats it exactly as
if the user had typed it. If this feature ever needs to run anything, it is a
different spec contradicting v1-spec.md.

## User-visible behavior

- A user command is a markdown file whose name (filename without `.md`)
  becomes a slash command and whose body is a prompt template. Proposed
  locations:
  - repository `.likha/commands/*.md` — repo-scoped, shareable via version
    control;
  - `<stateDir>/commands/*.md` — global, beside `config.json` /
    `providers.json` / `mcp.json` in the private state directory.
  Whether both locations exist, and which wins when the same command name is
  defined in both, is an open decision (below).
- `/commands` would list the available user templates with
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
   `/commands`, never enforced behavior. Recommendation: `description`
   only, name from the filename.
2. **Locations and precedence.** Repo only, state dir only, or both; if both
   exist, does the repo file win, the state file win, or is the collision an
   error? (Tension: `.likha/commands` inside an untrusted repository vs.
   treating command files as untrusted input regardless of location.)
3. **Load timing.** Hot reload on file change vs. load once at startup (with
   `/commands` re-reading?).
4. **Arguments.** Placeholder token name; a single blob vs. positional
   arguments; quoting rules for multi-word arguments; behavior when the
   template has no placeholder but arguments were given.
5. **`/commands` scope.** List templates only; MCP has `/mcp` and real skills
   have `/skills`. Do not merge these distinct catalogs.
6. **Interaction with existing rules.** The `//` escape and
   inert-during-active-run rules from [slash-commands/spec.md](../slash-commands/spec.md)
   apply unchanged; confirm.

## Functional changes (to apply in v1-spec.md when started)

- **Deferred FR-03 extension:** the reserved-command list would gain `/commands`; the
  sentence gains that user-defined commands from markdown files are also
  dispatched, that reserved names cannot be overridden, and that a custom
  command's expansion is sent as the user prompt.
- A new functional requirement, in v1-spec.md's tone: "The user can define
  custom slash commands as markdown prompt templates in the repository or the
  private state directory; `/commands` would list them; an invoked command's
  template, with the typed arguments substituted, is sent as the user prompt
  and never executes anything or changes tools or configuration."
- README command reference documents `.likha/commands/*.md` (and the state
  directory, per the location decision) and the untrusted-input rule.

## Acceptance criteria (draft)

- [ ] `/commands` lists defined user templates with descriptions and shows a
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
