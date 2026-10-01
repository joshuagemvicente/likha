# Feature: Agent harness prompt (structured system prompt)

**Status:** draft — design for review; no implementation yet.

## Purpose and scope

Lisa's model interactions currently carry no identity or rules: the model sees
only tool definitions and conversation. A top-tier agent harness injects a
**system prompt** — the harness's operating instructions — on every turn. This
feature adds that layer: a structured, compiled-in prompt that makes Lisa
behave like a real coding agent instead of a chat window, plus a
**repository instructions file** so each project can steer the agent.

Scope for v1: software development (the tool set is code-shaped: read/search,
edit, run commands). Hardware and cybersecurity workflows ride the same
harness later through additional tools — the prompt structure below is
domain-generic on purpose.

## Architecture (textual)

```
model request per turn
├─ [1] system message        ← harness prompt (compiled, go:embed)
├─ [1] project instructions  ← AGENTS.md / LISA.md from the repository root
├─ prior conversation        ← assistant/tool messages from the session store
├─ current user prompt
└─ tool definitions          ← built-ins + MCP tools (already wired)
```

The prompt is layered, not one blob: harness identity and rules first, then
project context, then the conversation. Layers are separate messages so the
session store can distinguish harness-owned context from user-owned history.

## Core components

- **`internal/agent/prompt.md`** — the harness prompt itself, embedded with
  `go:embed`. A real file (not a magic string): versioned, reviewable, and
  editable without touching logic. Content sections, in order:
  1. **Identity** — "You are Lisa's agent: a terminal coding agent working
     in one repository."
  2. **Tool contract** — when to use `read`/`grep` vs guessing;
     that `edit_file` proposes full-file replacements shown as diffs; that
     `run_command` is approval-gated and not sandboxed; MCP tools likewise.
  3. **Workflow rules** — read before editing; smallest change that satisfies
     the request; verify with a command when possible; never claim an action
     ran that didn't.
  4. **Out-of-bounds** — never exfiltrate repository content; never write
     outside the repository; secrets in diffs are visible to the provider.
  5. **Output discipline** — concise prose between tool calls; no filler.
- **Project instructions loader** — `internal/agent/instructions.go`: reads
  `AGENTS.md` from the repository root (the ecosystem-standard name, shared
  with OpenCode/Claude Code/other agents; no Lisa-specific file), capped
  (e.g. 8 KiB, truncated with a visible marker), injected as a `developer`
  role message after the system message. Missing file = no-op.
- **Session wiring** — the harness prompt is prepended per request from the
  compiled source, not stored in history; persisted sessions keep storing
  only user-owned messages, so prompt upgrades apply cleanly on resume.

## Recommended integration for the current stage

Compiled-in harness prompt + project instructions file, both in one feature:

1. Add `internal/agent/` (prompt embed + instructions loader) — pure code,
   no UI change.
2. Prepend the system message (and project instructions when present) in
   `runTurn` before `prior`.
3. Amend FR-03 or add FR-17: "Lisa sends a structured harness prompt with
   every request; repository instructions files are included when present."
4. Tests: prompt present as first message, order fixed, project file
   precedence and size cap, no prompt duplicated on session replay.

Rejected alternatives: user-editable prompt files (security surface; the
compiled prompt is a product artifact, like the tools list), per-provider
prompt forks (one prompt, one behavior), and storing the prompt in SQLite
(configuration drift).

## Usage scenarios (software-centric)

- **Work repo:** `AGENTS.md` says "run `go test ./...` after every change" —
  the agent checks tests after every proposed edit, unprompted.
- **Personal project:** no instructions file — the harness prompt alone keeps
  the agent proposing diffs, using tools before editing, and staying terse.
- **Onboarding a model that ignores tools:** the prompt's tool-contract
  section is what makes weak models usable instead of chatty.

## Extensibility note

Later profiles can add domain sections (hardware flashing constraints,
cybersecurity engagement rules) layered the same way as project instructions —
selected per-session, never hardcoding the domain into the core prompt.
