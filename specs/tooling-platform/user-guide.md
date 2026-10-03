# Tools and agents user guide

**Status:** Phase 1 is implemented locally, with new walkthroughs unverified.
Phases 2–3 describe planned, disabled behavior. This is not a published-release
claim. See [implementation evidence](implementation.md).

## Phase 1: core tools

Start Likha in the intended repository. `/tools` shows actual tool availability,
source, and approval requirements. The harness tells the model the real root;
repository read/search should use `read`, `glob`, and `grep`.

Example request: “List Go files under cmd and internal, then read go.mod.”
Expected workflow: scoped glob calls plus read, with no shell prompt. A model
can still choose shell; Likha then asks approval rather than guessing safety.
The `/workspace` command in Image 1 reaches outside the selected repo and
remains gated. The find/cat command in Image 2 has a dedicated-tool alternative.

Inspection respects ignores/hidden filters by default. Ask to include those
files explicitly when necessary. Large results identify incomplete/skipped
content and provide a next page or narrower-scope suggestion. A direct read
of a named repo file differs from discovery filtering.

Ask for a change and inspect its complete multi-file diff. Approve only after
reaching the end; Decline changes nothing. A changed target makes an old review
stale. Shell remains separately approved with exact command/cwd and unsandboxed
warning. Group file application is not crash-atomic; inspect reported partial
paths after a disk error/interruption rather than assuming rollback.

Focus a tool row and expand it with Enter/Ctrl+O. The footer lists navigation
and Back controls; Esc/Ctrl+C cancels an active run even during inspection.
Full proposal review is separate from expanding past output. Retained output
stays private with the session, subject to byte caps, and can contain secrets.
Clear retained output explicitly when needed; missing output never reruns its
generating command. Only requested pages enter the configured provider's context.

Use `/tools clear-output` to reclaim the session's retained-output allowance.
Artifacts retain at most five MiB per call and 50 MiB per session, with no silent
eviction. Tab/Shift+Tab focus transcript tool rows; Ctrl+O opens local inspection.
Within an artifact inspector, left/right arrows read local pages. Ordinary file
ranges use next offsets; limited glob/grep queries currently require narrowing,
not cursor pagination. Lines too large for a page are clipped with a warning.

Use `/tools recovery` to redisplay incomplete exact-edit records. On startup or
session resume, incomplete private journals are reported without rerunning or
rolling back writes. The private `sessions.sqlite` `edit_journal` table retains
original/proposed text, identities, and write intents for manual reconciliation.
It may contain secrets and is not automatically sent to the model. Saved state
is evidence, not proof of current disk state or permission to overwrite it.
Exact multi-file `edit` is disabled without a private session journal; compatible
`edit_file` keeps its existing single-file approval contract.

MCP remains stdio-configured in private state. Catalog/transcript names identify
the original server/tool. First approval trusts that server until app relaunch,
not just one tool; `/plan` later blocks all MCP calls even if the server is trusted.

## Phase 2: nested exploration

Example request: “Explore the agent and session modules independently; let the
explore agents delegate narrower repository investigations if useful.”

The main model calls `task`; explore may spawn another explore once. All children
use the active provider/model with a fresh scoped brief and repository-read-only
tools. They cannot edit, shell, call MCP/web, ask questions, or acquire broader
permissions from their prompt. Time/round/depth/spawn limits constrain work,
but do not guarantee a dollar budget.

Open `/agents` while idle or Inspect a task row while running. The tree shows
queued/running/waiting nodes, parent/depth, usage, and remaining budgets. Four
children can execute; waiting parents release slots so nested work can run.
Inspect a node's transcript without adding its full history to the main model.

Cancel branch ends that node and descendants; other branches can continue.
Esc/Ctrl+C cancels the main run and all children. Partial/limited findings remain
visible. Resume shows unfinished nodes as interrupted and does not restart them.

## Phase 3: questions and planning

`ask_user` pauses for one complete question, choices/free text, or Skip. Your
answer reaches the configured provider. The interaction preserves the normal
draft/steering queue; an answer grants no edit/shell/network approval.

`/todo` inspects the model's bounded progress list; updates persist with the
session. “Completed” is declared plan state, so inspect tool evidence before
assuming verification. Resume marks interrupted active work without restarting it.

`/plan` toggles a live read-only mode while idle. The status bar shows the mode.
Repo reads, bounded explore, questions, checklist, and passive skills can remain
available. Edits, shell, and MCP refuse before approval. Optional web keeps its
own consent rules. Resume starts normal approval-gated mode, regardless of a
saved checklist. Slash commands stay inactive during a run; inspect via task/
checklist controls or cancel first when changing mode.

## Phase 3: skills

Place a strict `SKILL.md` under `<stateDir>/skills/<name>/`, with only name and
description frontmatter plus instructions. See the [format example](../markdown-skills/spec.md).
`/skills` lists/inspects them; `/skill <name> [request]` invokes one as a normal
main-agent turn. The model can also load a discovered skill on demand.

Only global prompt-only skills ship. Project/foreign skills, scripts, extra
permission/model fields, includes, and remote catalogs are unsupported. Skill
instructions reach the provider and do not override permissions. Oversized or
invalid files produce named errors; reduce an over-cap catalog to advertise it.

## Phase 3: optional web setup

Use the private Likha state directory: `~/.config/likha/` on Linux or
`~/Library/Application Support/likha/` on macOS. Protect the directory and secret
files using the same private-state rules as provider credentials.

In `tools.json`, enable only the web tools you want:

```json
{"web":{"search":{"enabled":true,"backend":"brave"},"fetch":{"enabled":true}}}
```

Provide `BRAVE_SEARCH_API_KEY` or private 0600 `tool-keys.json`:

```json
{"brave":"<your-search-api-key>"}
```

An environment key wins over the saved key; neither enables search by itself.
Fetch needs enablement but no search key. `/tools` explains missing/disabled
setup. Avoid putting credentials in repo files, prompts, or skill content.

First search asks to authorize Brave for this conversation and displays the
query. Brave's API notice states default query records may last up to 90 days.
Review project details in queries before granting. Later searches use that
same backend without another prompt; failures do not switch vendors.

First fetch asks per public HTTPS origin; new redirect origins ask too. Only
public HTTPS on port 443 and supported text content are allowed. No cookies,
credentials, private hosts, browser scripts, hidden extraction model, or shell
fallback. The site sees the request; returned content sent to the configured
model follows that provider's policy. Content can include malicious instructions;
it supplies data, not authority to execute or disclose secrets.

Grants reset on switching/resuming sessions or app exit/config changes. Inspect
old results without repeating the request. Cancel slow calls normally; network
deadlines start after consent and exclude later consent waits.

## Phase 4: your own agent profiles

The task tool now advertises more than the built-in explore agent. Author a
profile as `agents/<name>/AGENT.md` in the private state directory — frontmatter
carries `name`, `description`, and optionally `model` and `tools`; the body is
the profile's instructions. See `specs/user-agents/user-guide.md` for the
copy-paste example and the full ceiling table.

- The built-in `review` profile reads and searches the repository and reports
  findings; `explore` is unchanged; `implement` stays unavailable until
  isolated worktrees exist.
- A profile's `tools` list can only narrow the read-only child ceiling
  (glob, read, grep, task), and dispatch re-intersects it with your live
  permissions, so a profile can never grant more than you could.
- `/agents` lists every profile with its exact ceiling, optional model, and
  over-cap or malformed identities named visibly; task rows and transcripts
  show which profile ran.
- Changed-on-disk profiles refuse and request a catalog refresh instead of
  loading a replacement. Project-local profiles remain deferred until a
  trust/precedence policy exists.

## Recovery and evidence

Refusal: inspect the named permission/mode; make a fresh authorized request if
needed. Truncation: inspect retained pages or narrow the query. Stale edit:
prepare a fresh proposal. Interrupted task: inspect partial results, then decide
whether to request new work. Missing backend/storage: fix setup without relying
on silent fallback/replay.

Competitor matrices describe source-documented capabilities, not live-tested
parity. Existing checks alone do not verify these new workflows. Keep walkthrough
outcomes and unsupported provider combinations visible before release claims.
