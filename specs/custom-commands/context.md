# Context: custom command templates (deferred)

This feature is outside the approved phases. Real SKILL.md discovery and
`/skills` belong to [Markdown skills](../markdown-skills/spec.md); templates
would use `/commands`. Remaining format/location choices are unresolved.

## Code

| Path | Relevance |
| --- | --- |
| `internal/skills/skills.go` | Current inert package; the separate Markdown skill feature owns its activation. |
| `internal/tui/tui.go`, `internal/tui/dialog.go`, `internal/tui/commandcomplete.go` | Command dispatch, `//`, help, and unknown-command behavior. |
| `internal/app/run.go` | State directory resolution: `LIKHA_STATE_DIR` or `os.UserConfigDir()/likha` (~lines 149–157); `config.json` and `mcp.json` are loaded here before the TUI starts — the precedent for loading command files at startup. |
| `internal/providers/config.go`, `internal/providers/keyfile.go`, `internal/mcp/config.go` | Current private state/config conventions. |
| `internal/mcp/` | MCP is a separate configured tool source and `/mcp` surface; templates do not register its tools. |

## Related specs

- [slash-commands/spec.md](../slash-commands/spec.md): the existing command
  system this extends — `//` escape, unknown-command behavior, inert during
  active runs; `/skills` is listed there as a reserved placeholder.
- [v1-spec.md](../v1-spec.md): FR-03 (reserved command list, unknown-command
  error, `//` escape), §5 privacy/state rules (repository content and
  untrusted input never override approval; private state directory), §6
  "v1 does not require a plugin architecture".
- Feature folders with status/index implications: add this feature to the
  `specs/README.md` index when the folder ships (folder creation, not code,
  may proceed independently).

## Precedent links

- Claude Code custom slash commands:
  https://code.claude.com/docs/en/commands — `.claude/commands/*.md` in the
  project (checked into version control) or `~/.claude/commands/` globally;
  the filename becomes the command and `$ARGUMENTS` marks where the typed
  arguments are inserted. Newer docs also describe skills under
  `.claude/skills/<name>/SKILL.md`; foreign skill formats can support executable
  resources/permission effects. Do not assume their semantics match passive templates.
- OMP skills: the `skill://<name>` convention — markdown instruction files
  selected by name, executed by reading, never executed as code.

## Open items

- See the "Open decisions" section of [spec.md](spec.md): frontmatter schema,
  location/precedence, load timing, argument rules, `/commands` scope, and the
  reserved-list wording for the FR-03 amendment. None is resolved yet.
