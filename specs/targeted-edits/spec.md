# Feature: reviewed exact-text edits and creation

**Status:** implemented (local); new stale/partial/crash walkthroughs unverified.
**Phase:** 1. **Product requirements:** FR-06–07, FR-26.
See [implementation evidence](../tooling-platform/implementation.md).

## Scope and input

Add `edit` alongside compatible `edit_file`. Its bounded `operations` list uses
two explicit variants:

```json
{"operations":[
  {"kind":"replace","path":"internal/example.go","old_text":"old","new_text":"new"},
  {"kind":"create","path":"new/package/readme.md","content":"# Package\n"}
]}
```

Support UTF-8 text create/modify only. Delete, rename, fuzzy matching, unified
patch syntax, binary edits, permission changes, and arbitrary directory actions
remain out of scope. Root confinement and existing per-file safety limits apply.
Use at most 64 operations and four MiB of total proposed content per call;
reject excess before preparing a review. These are documented handler bounds,
not permission grants or hidden partial application.

## Matching and proposal

Resolve replacements against the original snapshot of each file. `old_text`
must be nonempty and match exactly once; reject missing, ambiguous, overlapping,
or mutually inconsistent matches. Multiple non-overlapping replacements may
target one file. Create requires a previously absent path and cannot combine
with replacement of the same file. Treat unchanged output as no-op/refused,
not a successful write. Preserve newline and encoding behavior visibly.

Validate the entire call before proposing writes. The review shows every path,
required new parent directory, and full resulting diff. No accepted operation
changes disk until the user has seen the whole review and approves it. Decline
changes nothing, including missing parent directories. At an unusable terminal
size, refuse approval until the complete review is reachable.

## Approval and stale handling

Approval covers exactly this snapshot/proposal. Revalidate every target and
ancestor before publishing any file; one stale/missing/created/replaced target
blocks the whole proposal. Return a conflict; preparing a new proposal requires
a new review. Confine parent creation and reject symlink/unsafe ancestor changes.

Preflight/stage all replacements before applying the group. If application
fails after some publication, attempt safe recovery only for unchanged files
written by this proposal; do not overwrite subsequent user changes. Report
precise applied/restored/unresolved paths and any leftover directories.
Retain recoverable proposal state privately for interruption reporting.

Multi-file filesystem publication is not crash-atomic. Approval and preflight
are whole-change operations, but a crash/race can leave a partial apply. On
resume show interrupted/partial state and recovery guidance; do not replay the
write, infer success, or silently roll back other work. Verify recovery rules
before claiming group application complete. Parent-directory creation follows
the same honest partial-failure reporting.

## Cancellation and compatibility

Before publication, cancellation discards staged work and leaves targets
unchanged. Once publication starts, finish/report the bounded apply/recovery
boundary before delivering the terminal result; start no subsequent proposal.
Reject late approvals after cancellation. Keep current `edit_file(path,content)`
calls, full replacement reviews, and saved history readable through the registry.

Explore children receive neither edit tool, even if they forge its name.
`/plan` refuses edits before opening approval. Skill/agent text cannot authorize
application. There is no automatic formatter or shell fallback in this phase.

## Acceptance guide

- Review a create plus two replacements in different files; inspect all pages,
  decline, then prepare/approve a fresh proposal. Only its displayed diff applies.
- Exercise duplicate/missing/overlapping old text and an existing create target;
  invalid proposals leave no effect and return an actionable error.
- Alter any file/ancestor during review; the entire stale proposal is refused.
- Create a missing safe parent; decline creates nothing; unsafe parents fail.
- Cancel before apply and interrupt during a multi-file boundary; distinguish
  unchanged, complete, and partial/recovery states without replay on resume.
