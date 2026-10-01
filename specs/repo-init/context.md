# Context: Repository init (`/init`)

Code paths, prior art, and specs this feature touches. No code is changed by
this spec yet (status: draft).

## Code paths (expected touchpoints)

| Path | Relevance |
| --- | --- |
| `internal/app/agent.go` | `runTurn` and the agent tool set (`read`, `read`, `grep`, edit tools). `/init` is a normal turn here; the survey prompt and the draft-as-edit-proposal flow land on this loop. Command recognition/interception may live nearby (`internal/app/tui.go`). |
| `internal/app/tui.go` | Command dispatch for reserved slash commands: add `/init` to the reserved list next to `/sessions`/`/models`/`/model`/`/quit`/`/help`; inertness during a run or pending approval follows the existing rule. |
| `internal/actions/edit.go` | Edit proposal/diff preview and stale-proposal detection — the path the drafted `AGENTS.md` proposal flows through unchanged. |
| `internal/repository/repository.go` | Read/list/search path confinement (FR-05) already validates repo-scoped paths; `AGENTS.md`-rooted read/write is just another repo path. |
| `internal/app/run.go` | Login-level config/provider checks: no new gating; `/init` requires the same configured provider as any turn. |

## Precedent

- Claude Code `/init`: survey the repository, generate `CLAUDE.md` at the root
  (build/test commands, code layout, conventions), leave the file for later
  sessions' system context. Lisa's generation step mirrors this with the
  neutral `AGENTS.md` filename; injection is an open decision.

## Related specs / requirements

- `specs/v1-spec.md`: FR-03 (reserved commands — list must be amended before
  implementation), FR-05 (read confinement), FR-06/FR-07 (edit approval,
  stale proposals), FR-09 (accurate failure reporting), FR-10 (session
  persistence; no approval replay), §5 (approval applies per proposal; no
  blanket permission).
- `specs/slash-commands/spec.md`: command interception, inertness during
  active runs, `//` escape, and v1-spec amendment pattern.

## Open decisions

- System-context auto-injection of `AGENTS.md` in later sessions (token cost,
  staleness, `/reload`) — separate feature if pursued.
- Interaction with an existing root-level `AGENTS.md` (refuse / offer rewrite
  / append section) — must be decided before implementation.
