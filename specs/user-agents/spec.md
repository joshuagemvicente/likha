# Feature: user-authored agent profiles and the built-in review role

**Status:** planned. **Phase:** 4. **Product requirements:** FR-28–29, with the
FR-30 scope note under the allowlist decision below. Implements the deferred
roadmap items "User-authored agents" and "Useful built-in profiles" from
[tooling-platform](../tooling-platform/spec.md). Scope and contract approved by
the user on 2026-10-04. Depends on the Phase 2
[explore runtime](../explore-agents/spec.md) and
[agent inspection](../agent-inspection/spec.md), and reuses the Phase 3
[skills](../markdown-skills/spec.md) discovery conventions. Companion
`tasks.md`, `checklist.md`, `context.md`, and `role.md` files are created when
implementation starts, per [spec conventions](../README.md).

## Scope and authority

Phase 4 adds declarative agent profiles under the private Likha state directory
and the built-in `review` role, and generalizes `task` dispatch from the single
hardcoded `explore` enum to the frozen run catalog. A profile is named
prompt/model/tool data. Defining one runs nothing and grants nothing; profiles
are data/prompt, never executable code, and cannot change the model provider
configuration, register tools, or approve effects.

This spec supersedes the Phase 1–3 note that `/agents` must not imply
user-authored profiles exist; it does not amend any approved limit in
[shared decisions](../tooling-platform/decisions.md). Depth, concurrency, spawn,
deadline, and request limits reference the shared explore rows instead of
restating new values. Write-capable children, project-local profiles, and LSP
tooling remain deferred (see the final section).

## Discovery format

Load only `<stateDir>/agents/<name>/AGENT.md` from the private global state
directory. There are no repository roots, remote catalogs, recursive discovery,
scripts, includes, or executables. Resolve paths safely; reject symlinks and
escapes exactly as [skills discovery](../../internal/skills/discovery.go)
requires: real directories under the state directory, a regular non-symlink
`AGENT.md`, and a canonical resolution that stays inside the agents directory.
A missing state or agents directory is a normal empty catalog with zero errors.

Frontmatter allows only `name`, `description`, `model`, and `tools`. `name` and
`description` are required; `model` and `tools` are optional. Unknown, missing,
or duplicate frontmatter fields are named rejections, as skills reject
`allowed-tools` and other unsupported fields. The frontmatter `name` must equal
the directory name and use the skills name rule: lowercase letters, digits, and
interior hyphens, 1–64 characters. `description` is a nonempty string of at
most 1 KiB. The Markdown body holds the profile instructions: nonempty valid
UTF-8 of at most 32 KiB, with the whole-file bound mirroring the skills rule
(body cap plus frontmatter). An oversized body does not load.

```markdown
---
name: api-mapper
description: Map public API surfaces and call sites before proposing changes.
tools: [read, grep, glob]
---
Find the exported symbols for the requested package. Report file/line
references. Do not propose edits; return findings only.
```

## Discovery, trust, and catalog caps

Reserved profile names are `main`, `explore`, `task`, `review`, and
`implement`. A directory using a reserved name, or any invalid, duplicate, or
malformed identity, becomes a named catalog error and is never advertised or
invocable. **Decision:** `explore` and `review` are live built-in identities
that discovery must not shadow; `implement` is reserved because it is the named
future write-capable built-in and a read-only profile claiming it would
mislead invocation; `main` and `task` are reserved runtime words where a
user-defined profile could only cause confusion.

Discovery advertises at most 32 valid identities, mirroring the skills cap.
Valid identities beyond the cap are held out of advertisement and surfaced by
name in `/agents` with a visible over-cap error; the user reduces the set
before they are advertised. No hidden subset is ever selected. Every rejected
identity produces a named, bounded, visible error (invalid name, reserved name,
malformed or duplicate frontmatter, unsupported field, oversized body, symlink,
escape, unreadable file); errors never prevent normal startup and never
silently drop or include an identity.

The catalog is discovered at idle/run boundaries and frozen for the run, as
skills freeze their run catalog. At dispatch the selected `AGENT.md` is
re-read and re-validated; if its name, description, or content fingerprint
changed since discovery, the spawn is refused with a named "changed on disk"
error that requests a catalog refresh, and no child is created.

## Allowlist policy and ceilings

The optional `tools` frontmatter is an allowlist array over the fixed
child-capable set. **Decision (child-capable set):** the coordination brief
proposed `glob, read, grep, task, ask_user`; this spec pins the set to `glob`,
`read`, `grep`, and `task`, excluding `ask_user`. FR-30 defines `ask_user` as
main-only and the shared decisions give child contexts no interaction channel,
so admitting it would silently amend an approved product requirement. A profile
naming `ask_user`, or any name outside the set, is rejected at discovery with a
named error rather than accepted and never granted. Widening the set — for
example granting children questions — requires a v1-spec amendment and a new
decision row, not an allowlist entry.

`tools` may only narrow. An absent `tools` field grants the full child-capable
ceiling; an empty array is a named definition error because a profile that can
invoke no tool cannot return findings. Unknown tool names are named errors, at
discovery and again at dispatch. The effective set is always parent effective
capabilities ∩ user/mode policy ∩ profile allowlist (authorization invariant 5):
a profile can never exceed its parent or the user, and an allowlist naming a
tool the parent lacks still cannot invoke it. Allowlists cannot add, approve,
or restore any capability, and a task prompt cannot widen them.

Depth, nesting, and budgets match the shared explore contract exactly and are
shared across all profiles: main is depth 0, children may nest to depth 2, a
depth-2 node of any profile has no `task` tool even if its allowlist names it,
four children execute across the whole tree, 16 children are accepted per main
run, each child has 32 model requests and a five-minute deadline from accepted
creation including queue and nested waits, and the earlier ancestor deadline
wins. Malformed or refused spawn requests that never create a child consume no
budget. Plan mode permits the same read-only profiles under the same limits.

## Built-in profiles

`explore` is unchanged: its existing read-only ceiling — repository
`glob`/`read`/`grep` plus `task` where depth permits, the embedded explore
prompt, and the same configured provider/model — stays authoritative. No
discovered file can redefine or extend it.

`review` is a new built-in profile with repository `glob`/`read`/`grep` plus
`task` nesting at depth-permitted nodes and a compiled review-oriented prompt.
It is read-only; in this phase it gains no LSP or semantic tools. **Decision:**
shipping review without code intelligence is deliberate degradation, not
silently falling back — review works with the same inspection tools as explore,
its `/agents` entry and prompt document that semantic tools are absent, and a
future code-intelligence spec may extend the ceiling explicitly.

`implement` is explicitly not available in this phase. Write-capable children
remain disabled until isolated-worktree review exists; no profile, allowlist,
or prompt can produce a write-capable child, and `/agents` does not list
`implement` as selectable.

## Model selection

The optional `model` frontmatter names one exact model id (a nonempty
single-line UTF-8 string without control characters). A profile with `model`
runs on that exact model only when the parent run's configured provider serves
it; otherwise the spawn is refused with a visible reason naming the profile,
the requested model, and the configured provider. There is no provider
fallback and no cheaper-model substitution. **Decision:** the `explore`
profile keeps the shared-decision rule "same configured provider/model" — the
override applies only to `review` and discovered profiles, preserving the
approved explore contract while adding selection where the deferred roadmap
explicitly planned an "optional compatible model". Hosted cost disclosure
stands unchanged: nested agents reach the configured provider and can multiply
cost; per-task usage attribution reflects the model actually used, and unknown
metrics stay unknown.

## Invocation contract

The `task` tool's `agent` parameter becomes an enum computed at the run
boundary from the frozen catalog: `explore`, `review`, and the discovered
profile names, sorted. Forged or unknown names — including names discovered
after the run froze, reserved names, and over-cap identities — refuse with a
named error, create no child, and consume no spawn budget. The description
(≤120 characters) and prompt (1–32 KiB UTF-8) limits are unchanged from the
explore contract.

Each child starts a fresh context with the compiled harness, root instructions
snapshot, scoped brief, and the selected profile's instructions attributed as
user-authored profile data under the same untrusted-data framing as the task
brief: it grants no capabilities and cannot override runtime policy, and child
findings remain untrusted task data. The harness prompt and task tool
description describe the frozen catalog at the run boundary instead of
hardcoding the explore role. Parent conversation, credentials, pending
approvals, and output artifacts are still not inherited.

## Inspection

`/agents` while idle lists the built-in `explore` and `review` profiles and the
discovered catalog: name, description, declared tool ceiling, permitted
nesting, model (or inherited parent model), and budgets. For discovered
identities it also surfaces over-cap and named-error entries so the user can
fix the catalog. It offers no in-TUI profile creation or editing; profiles are
edited on disk and re-discovered at the next boundary. Task rows show the
profile name alongside task ID, depth, status, provider/model, and budgets.
Transcripts, usage attribution, branch cancellation, and resume behavior remain
those of [agent-inspection](../agent-inspection/spec.md).

## Persistence and interrupted resume

Task records keep the agent/profile name (`explore.Record.Agent`). Interrupted
resume marks queued/running/waiting tasks interrupted regardless of profile,
shows available partial results, never replays a task, and never reloads a
changed disk file to reconstruct history; a changed-on-disk profile affects
only new dispatch. A new explicit task call uses a new ID and fresh budget.
Historical records remain inspectable even when the defining file is later
deleted or renamed.

## Deferred decisions

- **Project-local profiles stay deferred.** Definitions live only in the
  private global state directory. Repository-local profiles require a
  trust/precedence policy specifying which directory wins, whether a repository
  may override or shadow global entries, and how provenance is displayed; this
  phase records that no repository can define or shadow an agent identity.
- **`ask_user` for children** requires an FR-30 amendment and a decisions-table
  row before any profile may name it.
- **`implement`** requires the isolated-worktree spec, including approval
  behavior inside the isolated workspace, before write-capable children ship.
- **LSP/semantic tools for `review`** follow the future code-intelligence spec;
  until then review's ceiling is fixed as above.

## Acceptance guide

- Discover an empty catalog (no agents directory) and a valid catalog;
  `/agents` lists built-ins plus discovered profiles with ceilings and models,
  and catalog errors never prevent normal startup.
- Invoke a discovered profile from the model with a scoped brief; confirm a
  fresh context, profile instructions attributed as data, and the declared or
  inherited model in the task row.
- Exceed the 32-identity cap; confirm the over-cap error names the affected
  identities and none of them is silently advertised or invocable.
- Name an unknown or disallowed tool in `tools` (including `ask_user`) and
  confirm a named discovery error; confirm an allowlist can only narrow — a
  profile listing a tool the parent lacks still cannot invoke it.
- Change an `AGENT.md` on disk after discovery and invoke it; confirm the
  changed-on-disk refusal requests a catalog refresh and creates no child.
- Compare `review` and `explore` ceilings in `/agents` and at dispatch; both
  stay read-only, neither gains edit, shell, MCP, web, or question tools, and
  review documents the absence of semantic tools.
- Forge `task` calls with unknown, reserved, over-cap, and post-freeze agent
  names at main and nested depths; confirm named refusals with no child
  creation and no spawn-budget consumption.
- Exercise depth and budget limits across mixed profiles: depth cap, four
  executing children, 16 accepted per run, 32 requests, five minutes including
  waits; a profile model served by another provider refuses with a visible
  reason.
- Cancel a branch and the whole run across mixed profiles; resume shows
  interrupted records with profile names, no replay, and no restored
  permissions.
