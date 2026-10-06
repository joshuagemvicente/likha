# User agents user guide

**Status:** Phase 4 describes planned, disabled behavior. This is not a
published-release claim. See the [spec](spec.md) for exact bounds and
[implementation evidence](../tooling-platform/phase-4-implementation.md) for
build status.

## Phase 4: your own agent profiles

Profiles live under the same private Likha state directory as skills and web
configuration (`~/.config/likha/` on macOS and
Linux): one directory per profile at `<stateDir>/agents/<name>/`, containing
one `AGENT.md`. A profile is named prompt/model/tool data. Defining one runs
nothing and grants nothing. Only the global state directory is scanned — no
repository file can define or shadow an agent identity.

`AGENT.md` carries frontmatter with required `name` and `description`, optional
`model` and `tools`, and a Markdown body of profile instructions:

```markdown
---
name: api-mapper
description: Map public API surfaces and call sites before proposing changes.
tools: [read, grep, glob]
---
Find the exported symbols for the requested package. Report file/line
references. Do not propose edits; return findings only.
```

`name` must equal the directory name and follow the skills name rule;
`description` is a short nonempty string; the body is plain instructions, not
code. `model` names one exact model id — used only when the configured provider
serves it, with no fallback or substitution. `tools` is an allowlist over the
child-capable set (`glob`, `read`, `grep`, `task`) that can only narrow;
omitting it grants the full child ceiling. Size caps, the name rule, and every
validation detail are in the [spec](spec.md) — this guide does not restate the
numbers.

## Built-in profiles

`explore` is unchanged: repository `glob`/`read`/`grep` plus `task` where depth
permits, the embedded explore prompt, and the same configured provider/model as
before. No file you write can redefine or extend it.

`review` is new: the same read-only ceiling and nesting as explore with a
review-oriented prompt. It has no LSP or semantic tools in this phase — that
absence is deliberate and stated in its `/agents` entry and prompt, not a
silent fallback.

`implement` does not exist in this phase. No profile, allowlist, or prompt can
produce a write-capable child, and `/agents` does not list `implement` as
selectable.

## /agents and dispatch

Open `/agents` while idle to see `explore`, `review`, and every discovered
profile with its description, declared tool ceiling, permitted nesting, model
(or inherited parent model), and budgets. Identities that error or exceed the
catalog cap are listed by name with the reason, so you can fix the files.
There is no in-TUI creation or editing: edit `AGENT.md` on disk and the catalog
re-discovers at the next run boundary.

The `task` tool advertises `explore`, `review`, and the discovered names,
sorted, frozen for the run. The harness describes that frozen catalog instead
of a hardcoded role. Forged, unknown, reserved, over-cap, or discovered-after-
freeze names refuse with a named error, create no child, and consume no spawn
budget.

## What refuses and why

- **Reserved names** (`main`, `explore`, `task`, `review`, `implement`) become
  named catalog errors and are never advertised or invocable.
- **Malformed, duplicate, or unknown frontmatter fields**, invalid names, and
  oversized bodies are named rejections, mirroring how skills reject
  unsupported fields.
- **Unknown tool names** — including `ask_user`, which is main-only — refuse at
  discovery, not silently at spawn.
- **Allowlists cannot exceed permissions.** The effective set is always the
  parent/user permission intersection with the allowlist: a profile listing a
  tool its parent lacks still cannot invoke it, and no prompt can widen it.
- **Changed on disk** after discovery: dispatch re-validates the selected
  `AGENT.md` and refuses with a named changed-on-disk error that requests a
  catalog refresh. No child is created from a file that no longer matches the
  frozen catalog.

Errors never prevent normal startup and never silently drop or include an
identity.

## Untrusted findings and cost

Profile instructions reach the child as user-authored data under the same
framing as the task brief: they grant no capabilities and cannot override
runtime policy. Child findings remain untrusted task data — verify them like
any other model output. Parent conversation, credentials, pending approvals,
and output artifacts are not inherited.

Nested agents reach the configured provider and can multiply cost, especially
with a `model` override on many concurrent children. Per-task usage attribution
reflects the model actually used. Time/depth/spawn limits constrain work but do
not guarantee a dollar budget.

## Not in this phase

Write-capable children (`implement`) and project-local profiles are
unavailable; definitions live only in the global state directory. LSP/semantic
tools for `review` await a future code-intelligence spec. See the spec's
deferred-decisions section for what each would require.
