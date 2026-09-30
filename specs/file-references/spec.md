# Feature: @ file and folder references (mention paths in prompts)

**Status:** implemented (local).

## What it does

Typing `@` in the prompt opens a file completion popup over the repository;
an `@path` token in a submitted prompt inlines that file's content into the
user message the model receives — the OpenCode/OMP/Claude Code reference
model. Unresolvable tokens stay literal, so typos are visible, never silent.

## Behavior

- **Popup:** `@` while the draft is editable opens the popup; the index is
  the confined `Repository.Tree` walk, run once in the background and reused
  afterward. It lists files and folders (directories carry a trailing `/`,
  so completing a folder yields `@src/` for a folder reference). Hidden
  directories like `.git`, **`node_modules`** (at any depth), symlinked
  entries, and **every `.gitignore` rule** (root file plus one per
  subdirectory, deeper rules winning) are excluded. Runes after the last `@`
  filter case-insensitively; ↑/↓ move, Enter or Tab completes
  `@<chosen> ` into the draft, Esc closes. With no matches, Enter submits
  normally — the popup never traps input.
- **File expansion:** each `@path` resolving to a file appends a fenced
  `[Referenced file @path]` block to the user message; up to 16 files and
  64 KiB of content per turn.
- **Folder expansion:** a reference resolving to a directory (bare name or
  `@src/`) inlines its tree listing — one path per line, directories marked
  `/`, capped at 200 entries with a visible truncation marker. Structure is
  inlined; content stays a read-tool away.
- Unresolvable tokens stay literal, so typos are visible, never silent. The
  displayed draft keeps the literal `@path`; the expansion is per-turn and
  not persisted, so resumed sessions replay the original prompt and current
  references apply per turn.
- The reference content reaches the model without tool rounds, so obvious
  context costs nothing extra; the model can still use `read_file` for
  anything too large to inline.

## Tests

`internal/repository/tree_test.go`: node_modules never listed, root
`.gitignore` rules (`build/`, `vendor/`) and nested per-directory rules
(`src/generated.ts`) respected, root rules apply to subfolder walks,
confined `IsDir` (file vs dir vs parent-traversal rejection).
`internal/app/mentions_test.go`: file expansion (resolved,
punctuation-stripped, unknown-literal, plain-untouched), folder expansion
(both `@sub` and `@sub/`), index contents (folders listed, hidden skipped),
popup open/filter/complete/view paths, and a `runTurn` fake-server test
proving referenced file content reaches the user message.
