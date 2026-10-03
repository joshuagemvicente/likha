# Context: Custom commands (user-defined skills)

## Code

| Path | Relevance |
| --- | --- |
| `internal/skills/skills.go` | The explicit placeholder this feature replaces: only a doc comment; `/skills` is "intentionally reserved but inert". |
| `internal/app/tui.go` | `handleCommand` (~line 552) is the slash-command dispatch: reserved cases, `//` escape, unknown-command default branch that restores the draft. `commandHelp` (~line 547) is the `/help` text unknown-command errors point to. The Enter path calls `handleCommand` when the prompt starts with `/` (~line 1119). |
| `internal/app/run.go` | State directory resolution: `LIKHA_STATE_DIR` or `os.UserConfigDir()/likha` (~lines 149–157); `config.json` and `mcp.json` are loaded here before the TUI starts — the precedent for loading command files at startup. |
| `internal/app/config.go`, `internal/app/keyfile.go`, `internal/mcp/config.go` | Private state file precedents: missing file is not an error, corrupt file is a loud error, mode 0600 (`config.json`, `providers.json`, `mcp.json`). |
| `internal/app/mcp.go`, `internal/mcp/` | The MCP loading pattern (`newMcpManager` at TUI construction) and `/mcp` status command — the sibling feature whose boundary `/skills` must not blur. |

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
  `.claude/skills/<name>/SKILL.md`, but both remain markdown prompt
  templates, not code.
- OMP skills: the `skill://<name>` convention — markdown instruction files
  selected by name, executed by reading, never executed as code.

## Open items

- See the "Open decisions" section of [spec.md](spec.md): frontmatter schema,
  location/precedence, load timing, argument rules, `/skills` scope, and the
  reserved-list wording for the FR-03 amendment. None is resolved yet.
