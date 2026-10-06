# /init: write the repository-root AGENTS.md

Survey this repository and propose a root `AGENTS.md` that tells a coding agent
what it needs to work here. The harness loads that file into every future turn,
so every line costs context: keep it short, factual, and specific to this repo.

## Limits for this turn

- Use read-only tools only: read, glob, grep, and task subagents for broad
  exploration. Do not run commands; shell commands, MCP tools, and other
  writes are refused this turn.
- Your only write is one `edit_file` call with path `AGENTS.md` (the
  repository root) and the complete new file content. Do not create nested
  `AGENTS.md` files or edit anything else.
- The user reviews the full diff before anything is written.

## Survey

1. If a root `AGENTS.md` exists, read it first. You will improve it, not
   replace it.
2. Read the existing agent and contributor guidance that is present:
   `CLAUDE.md`, `.cursorrules`, `.cursor/rules/`, `.github/copilot-instructions.md`,
   and `README.md`. Treat them as leads, not as truth: carry over only facts
   you can verify in the repository, and never copy them wholesale.
3. Find how the project is built, run, tested, linted, and formatted: build
   manifests and lockfiles (for example `go.mod`, `package.json`,
   `pyproject.toml`, `Cargo.toml`), `Makefile` or task runner files, and CI
   workflows. Prefer commands exactly as these files define them (scripts,
   targets, CI steps); otherwise use only the standard command of a toolchain
   whose manifest you read.
4. Map the top-level layout and the few directories that matter most.
5. Look for conventions a newcomer would get wrong: generated files, required
   tooling versions, test layout, error-handling or naming patterns that the
   code consistently follows, and anything the existing docs warn about.

## What to write

- Build, run, test, and lint commands, including how to run a single test.
- Repository layout: the important directories and what lives in each.
- Conventions and gotchas that are verifiable in the code or config.

Rules:

- Every fact must be backed by a file you read this turn. If you cannot verify
  something, leave it out. Never invent commands, paths, or tools.
- No marketing, project history, or generic advice that applies to any repo.
- Prefer terse bullets and code spans over prose.
- Target about 150 lines (roughly 8 KiB). Never exceed 32 KiB: the harness
  ignores a root `AGENTS.md` larger than that.

If `AGENTS.md` already exists, keep the user-authored content and structure,
fix facts that are now stale, and add what is missing. Propose the result as
one full-file `edit_file` call. If it does not exist, create it.

## Finish

End the turn by calling `edit_file` with path `AGENTS.md` and the full content.
Then summarize in a few lines what you added or changed and why.
