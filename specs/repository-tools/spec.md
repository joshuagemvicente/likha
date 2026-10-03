# Feature: repository inspection tool polish

**Status:** implemented (local); new acceptance walkthroughs unverified.
**Phase:** 1. **Product requirements:** FR-05, FR-19, FR-25.
See [implementation evidence](../tooling-platform/implementation.md).

## Scope

Polish `glob`, `read`, and `grep` so normal inspection fits dedicated tools.
Keep canonical-root, traversal, symlink, binary/UTF-8, file-size, and walk
defenses. These tools remain approval-free and repository-confined. They do
not interpret shell, fetch URLs, or read arbitrary session artifacts.

## Inputs and outputs

| Tool | Required fields | New optional fields | Behavior |
| --- | --- | --- | --- |
| `read` | `path` | `offset`, `limit` | File: numbered 1-based lines for a requested range. Directory: sorted relative children, directories suffixed `/`, bounded page. |
| `glob` | `pattern` | `path`, `include_hidden`, `include_ignored`, `limit`, `cursor` | Sorted repository-relative regular-file matches under a scoped directory. |
| `grep` | `pattern` | `path`, `include`, `literal`, `case_sensitive`, `include_hidden`, `include_ignored`, `limit`, `cursor` | Sorted `path:line: text` matches with completeness/filter metadata. |

Omitting optional fields retains valid legacy requests. `read(path)` for a file
can retain the old plain-text body; the harness directs large inspections to
numbered ranges. `offset` starts at 1 and defaults to 1; `limit` is positive.
A directory read defaults to its first page. Report total lines/children when
known, selected range, effective limits, and whether more content exists.
An offset past the end returns an empty page with an explicit end marker.

Default glob limit is 100 paths, grep limit 100 matches, and directory-read
limit 200 children. Callers may lower these; hard existing walk/match caps
remain. Ranged file reads still respect the existing one-MiB file bound.
Default numbered range length is 200 lines when range arguments are present;
the 64-KiB result cap can shorten it with an explicit next offset.

`path` accepts a file where meaningful for grep, or a directory for discovery.
Paths are relative to the pinned root. Glob syntax and Go regexp syntax remain
documented; literal mode bypasses regexp interpretation. Case behavior defaults
to the existing case-sensitive contract and respects explicit case selection.
An include filter narrows paths before file reads, not after a full scan.

## Filtering and completeness

Discovery honors root/nested `.gitignore` and skips hidden entries by default.
Explicit hidden/ignored flags expand discovery within the repository; `.git`
internals stay excluded. Direct reads of an explicitly named file do not use
discovery filters. Show applied filters so a no-match result is explainable.
Symlink entries stay excluded/refused under the existing confinement contract.

For an oversized, unreadable, or unsupported file encountered during grep,
continue with available matches and bounded skipped-file warnings. Mark the
scan incomplete. Invalid queries, traversal, unsafe/replaced root identity,
and invalid requested scopes fail instead of returning an apparently complete
result. A detected root safety failure invalidates the operation's payload.

At result/walk/byte limits return useful bounded content with `limited` status,
the cap/reason, and recovery guidance. If a continuation is available, bind it
to tool/query/root/filter identity and an observed directory snapshot. A stale
cursor yields a clear restart result; it cannot authorize a changed query or
external path. Where a safety walk cap prevents continuation, suggest narrowing
`path`/`include` rather than pretending the rest was searched.

Check cancellation during walks and file processing, not just before/after
dispatch. Parallel reads share bounded resources and keep individual identity.

## Screenshot regression scenarios

**Image 2:** listing `cmd/**/*.go`, `internal/**/*.go`, and reading `go.mod`
should use glob/read without shell approval. Include actual canonical repo and
tool instructions in the request. A live model that nevertheless requests
shell still receives the existing approval; prompts cannot guarantee tool
choice. Report observed provider behavior rather than promising perfect routing.

**Image 1:** the model's guessed `/workspace` path lies outside the chosen root.
Repository tools refuse it; the harness explains the real root. Deliberate
external inspection requires the normal explicit shell approval. No automatic
command classifier or read-only shell allowlist ships here.

## Acceptance guide

- Inspect a directory, narrow Go paths, read a file range, and literal/case
  search; results expose lines, filters, bounds, and correct continuation.
- Compare ignored/hidden results with explicit inclusion and direct reads.
- Include one oversize/unreadable file; useful matches remain, completeness
  is false, and no exhaustive no-match claim appears.
- Reach a result/walk cap, change files between pages, and cancel a walk;
  each returns honest metadata rather than hanging or reusing a stale cursor.
- Attempt absolute/traversal/symlink/replaced-root access; no escaped data
  appears. Record both screenshot workflows with a supported provider.
