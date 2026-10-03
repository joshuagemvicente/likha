# Context — Text transforms (user-defined input rewrites)

Code, tests, and docs this feature touches.

## Code

| Path | Relevance |
| --- | --- |
| `internal/app/tui.go` | The input buffer this feature writes into: `m.input []rune`, editable until Enter (`case "enter"`) sends the prompt to the agent. The Enter path calls `handleCommand` (~line 552) when the prompt starts with `/` (~line 1119) — the dispatch where `/transform` intercepts. The unknown-command default branch restores the draft, the precedent for transform errors leaving the buffer intact. Cursor/kill state from the prompt-editor feature (FR-17) must survive cancel/failure restore: restoring the buffer means restoring what the editor holds, not just the rune slice. |
| `internal/app/agent.go` | `runTurn` — the single ordinary model call a transform is. The transform call must reuse this path (cancellable, streamed, error-reporting) with tool dispatch disabled, not a second request builder. |
| `internal/app/config.go`, `internal/app/keyfile.go`, `internal/app/run.go` | Private state directory resolution (`LIKHA_STATE_DIR` or `os.UserConfigDir()/likha`) and the startup-file-loading precedent (`config.json`, `mcp.json`) that the shared markdown loader sits beside. |
| `internal/skills/skills.go` | The placeholder package (`/skills` "intentionally reserved but inert") that the shared custom-commands storage machinery activates — transforms load through it, they do not open their own storage path. |
| `internal/model/client.go` | `Stream` / `streamCodex` — the model client the single transform call rides on; no new wire format. |
| `internal/session/session.go` | Whether the "Transforming…" state and the applied-transform event belong in session history (open decision: the typed `/transform …` command line is a conversation event; the in-flight status is not). |

## Shared machinery (dependency)

- **[custom-commands/spec.md](../custom-commands/spec.md)** owns the markdown
  storage/frontmatter machinery transforms reuse (loader, locations,
  precedence, load timing, `$ARGUMENTS`-style placeholder token). Transforms
  differ from custom commands only in output routing — buffer replace +
  review-before-send instead of send-as-prompt. Its unresolved decisions
  (frontmatter schema, locations, load timing) gate this feature's
  implementation, not its spec.
- [slash-commands/spec.md](../slash-commands/spec.md): the dispatch rules
  `/transform` extends — FR-03 reserved list, `//` escape, inert during
  active runs/pending approvals, unknown-command error that restores the
  draft.
- [prompt-editor/spec.md](../prompt-editor/spec.md): FR-17 editor state the
  buffer replace/restore interacts with (cursor, kill ring, bracketed paste).
- [v1-spec.md](../v1-spec.md): FR-03, FR-04 (cancellation), FR-11 (visible
  errors), FR-17 (editing), §2 (one active agent run — a transform is not an
  agent run), §5 (untrusted input never overrides approval).
- `specs/README.md` feature index gains this folder when it ships (index
  update is owned by the main agent, not this spec).

## Precedent links

- Wispr Flow transforms — user-defined rewrites applied to text before it is
  sent, with the result landing where the user reviews it. This is the
  product precedent for the feature and for the hard review-before-send
  routing rule.
- Claude Code custom slash commands: https://code.claude.com/docs/en/commands
  — `.claude/commands/*.md` markdown prompt templates (the storage precedent
  custom-commands adopts and transforms share).
- OMP skills: the `skill://<name>` convention — markdown instruction files
  selected by name, read, never executed as code.

## Open items

- See the "Open decisions" section of [spec.md](spec.md): transform
  parameters (deferred: single blob only), nested/chained transforms
  (deferred — likely never), and which model runs the transform call
  (configured provider/model vs. a fixed cheap model — undecided). The
  shared storage/frontmatter decisions live in
  [custom-commands/spec.md](../custom-commands/spec.md) and gate
  implementation of both features.
