# CLI tools and agents: selected workflow comparison

**Snapshot:** 2026-10-03. **Evidence:** official documentation and pinned public
source, not live CLI/model/backend probes. This comparison guides the planned
[three phases](../tooling-platform/README.md); it does not claim feature parity.

## Versions and boundaries

- Claude Code: live official docs; platform/model/permission gates apply.
- OpenCode: V2 docs. V1 names/configuration describe a different API.
- OMP: `can1357/oh-my-pi`, pinned `9348320cc4a30a7195d36a1f05a6c11bcb701a17`.
- Pi: `earendil-works/pi`, pinned `a276dabe57911253350bffb93cb7d7aff6a73261`;
  formerly `badlogic/pi-mono`. Pi and OMP are separate products.
- Codex CLI: v0.160.0, 2026-10-01, commit
  `a956835d020762cb2b570053af06f643a11c0ecc`. Live docs may cover backend-
  dependent behavior beyond one source configuration.
- Likha: current repository inventory on 2026-10-03; five built-ins and stdio
  MCP exist, while new tools/agents phase specs remain planned.

Exclude desktop/cloud/web-client/marketplace parity. Identify optional CLI
features with their gate, not as universally available default capabilities.

## Inspection and permissions

| CLI | Inspection route | Permission/control distinction |
| --- | --- | --- |
| Claude Code | Read; platform-specific embedded shell search; Glob/Grep depend on platform/settings | Fixed read-only Bash set can avoid prompts. Config default means Manual, but current docs describe interactive auto startup from v2.1.283 subject to eligibility/settings. OS sandbox is a separate control. |
| OpenCode V2 | Dedicated read/glob/grep | Shipped policy generally permits actions with targeted external-directory/.env asks. Permission rules do not contain host shell authority. |
| OMP | Dedicated read/glob/grep/find and optional semantic tools | Documented approval default yolo; configurable read/write/exec tiers. Fewer prompts do not establish a sandbox. |
| Pi | Default read/bash/edit/write; grep/find/ls selectable | No built-in per-call approval; external OS/container controls and trusted extensions add boundaries. |
| Codex CLI | Native exec_command/write_stdin shell inspection in the sampled tool plan | Auto combines workspace-write sandbox and on-request approval. Local networking defaults off; trust/model/backend settings affect behavior. A sandbox-permitted shell search need not prompt. |
| Likha planned | Complete dedicated glob/read/grep with actual repo identity | Confined approval-free reads; exact approval for every shell. Image 2 uses dedicated alternatives; Image 1 external inspection remains refused/gated. |

Inspection capability and shell permission are separate decisions. Copying a
tool name without the competitor's permission/sandbox model would not reproduce
its low-friction behavior.

## Changes, questions, and progress

| CLI | Targeted changes | Question/plan gates |
| --- | --- | --- |
| Claude Code | Exact-string Edit plus Write | AskUserQuestion is withheld from ordinary subagents. Enter/ExitPlanMode and plan acceptance affect permissions; task/checklist APIs are separate. |
| OpenCode V2 | Exact edit, full write, model-gated GPT patch | Interactive question is available to primary build/plan; general/explore deny it. Plan shell remains permission-controlled, not automatically OS read-only. |
| OMP | Default hashline editing, alternatives by model/settings | Ask requires a prompt-capable session. Todo differs from plan guards; plan/scratch artifacts have special permissions. |
| Pi | Unique non-overlapping exact replacements against original content | Questionnaire, plan mode, and subagents require extensions; they are not core features. |
| Codex CLI | apply_patch requires execution environment/model metadata support | request_user_input defaults to root-thread Plan-mode use; Default-mode enablement is under development/off by default; codex exec rejects it. update_plan is separately configured and its handler rejects Plan mode in this snapshot. |
| Likha planned | Exact-text create/modify batch with complete review/stale checks; retain edit_file | Main-only single question and persisted checklist; /plan independently blocks effects. Answers/step status grant no authority. |

Likha retains explicit write review. Multi-file approval does not imply
crash-atomic filesystem publication or a competitor's automatic-edit policy.

## Skills and trust

| CLI | Relevant behavior/gate |
| --- | --- |
| Claude Code | Progressive skill loading, multiple roots; allowed-tools can grant temporary turn permissions. Docs warn workspace trust does not gate that field. |
| OpenCode V2 | Metadata advertisement; loading checks agent skill permission. Native/compatibility roots and optional remote catalogs; other tool permissions still apply. |
| OMP | Provider/source-filtered discovery and skill:// reads; foreign project roots default on, foreign user roots opt-in in sampled docs. No universal per-skill prompt established. |
| Pi | Recursive discovery; project/ancestor loading is project-trust-gated. Loading trust does not constrain subsequent process authority. |
| Codex CLI | .agents/skills plus user/system/bundled roots; explicit/implicit invocation and policy. Project-config trust was not established as a standalone skill-load gate. Executable resources have separate execution treatment. |
| Likha planned | Private global strict SKILL.md; model/user activation, no executable resources, includes, permission/model fields, or automatic repo loading. |

## Delegation and nesting

| CLI | Documented/sampled limits | Lifecycle/authority |
| --- | --- | --- |
| Claude Code | Current docs: three nested layers, 20 concurrent subagents, with settings/ultracode exceptions | Background common interactively, foreground where applicable; fork context can inherit history. Teams are separate experimental behavior. |
| OpenCode V2 | Default depth one; numeric concurrency cap not established by cited docs | Awaited or background notification; child's configured rules need not be a subset of parent rules. |
| OMP | Sampled defaults: depth two, concurrency 32/session | Async normally enabled; blocking forces awaiting; missing job manager falls back synchronous. Parent spawns allowlist/depth apply. Headless yolo tiers use parent task authorization. |
| Pi | No built-in child runtime | Extension agents are optional; do not assign core limits. |
| Codex CLI | V1 defaults: six spawned threads/depth one. V2 default: four threads including primary; legacy max_depth ignored. Backend varies by model/settings | Spawn returns identity; wait/notification are separate. Sandbox/live permission overrides propagate; child approvals depend on interactive host support. |
| Likha planned | Main 0 → explore 1 → explore 2; four executing children excluding main; 16 accepted spawns/run and child time/round limits | Awaited results, fresh scoped brief, same model, restrictive read-only inheritance, waiting-parent permit release, partial findings, branch cancellation. |

Compare counting rules before comparing numbers. Codex V2's sampled four-thread
budget includes primary; Likha's cap excludes main. Awaited nesting requires
parents to release execution slots while their descendants queue.

## Web and data flow

| CLI | Relevant distinction |
| --- | --- |
| Claude Code | Search uses Anthropic backend with provider restrictions. Fetch can make an extra extraction-model call rather than returning only a raw page. |
| OpenCode V2 | Exa, Firecrawl, Parallel, Tavily, TinyFish integrations. Random/fallback configuration may contact another vendor after rate limiting; fetch differs from shell networking. |
| OMP | URL fetch can be part of read with reader/rendering routes; search has fallback chains. A read tier need not mean offline or one vendor. |
| Pi | No native sampled search/fetch; shell/extensions/MCP supply optional network access. |
| Codex CLI | Cached hosted search defaults on; live mode configurable. Hosted open_page/find_in_page depend on model/API. Standalone web.run is gated, under development/off by default in the sampled configuration. Shell network policy is separate. |
| Likha planned | One configured Brave service, direct public HTTPS/local conversion, first-use backend/origin consent; no vendor/extraction-model/shell fallback. |

Brave returns URL/title/snippet data without requesting generated answers; its
API notice states default query retention up to 90 days. Tavily also supports
the technical contract and claims API zero retention, but broader query-use/
sharing privacy scope remains unresolved. Brave is the chosen first adapter,
not a claim of better relevance, price, latency, or consumer-search privacy.

## Adoption and evidence

Phase 1 adopts complete inspection, deterministic exact edits, provenance,
and bounded inspectable output. Phase 2 adopts nested scoped role execution
with stricter permissions/awaiting than several competitors. Phase 3 adopts
question/checklist/mode separation, progressive passive instructions, and
explicit one-service web access. User profiles, writers, teams, semantic tooling,
and executable plugins stay deferred. No comparison binary/provider was live-tested.

## Primary sources

Checked 2026-10-03. Recheck changing docs before shipping.

- Claude: [tools](https://code.claude.com/docs/en/tools-reference), [permission modes](https://code.claude.com/docs/en/permission-modes), [subagents](https://code.claude.com/docs/en/sub-agents), [skill permissions](https://code.claude.com/docs/en/skills#pre-approve-tools-for-a-skill).
- OpenCode V2: [tools](https://opencode.ai/v2/docs/tools), [agents](https://opencode.ai/v2/docs/agents), [permissions](https://opencode.ai/v2/docs/permissions), [skills](https://opencode.ai/v2/docs/skills), [search](https://opencode.ai/v2/docs/websearch).
- OMP pinned: [edit](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/docs/tools/edit.md), [approvals](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/docs/approval-mode.md), [task discovery](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/docs/task-agent-discovery.md), [task settings](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/packages/coding-agent/src/task/settings.ts), [skills](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/docs/skills.md), [search](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/docs/tools/web_search.md).
- Pi pinned: [README](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/README.md), [edit](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/src/core/tools/edit.ts), [skills](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/docs/skills.md), [security](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/docs/security.md).
- Codex: [security](https://developers.openai.com/codex/agent-approvals-security), [subagents](https://developers.openai.com/codex/agent-configuration/subagents), [skills](https://learn.chatgpt.com/docs/build-skills), [web](https://developers.openai.com/codex/web-search).
- Codex pinned: [tool plan](https://github.com/openai/codex/blob/a956835d020762cb2b570053af06f643a11c0ecc/codex-rs/core/src/tools/spec_plan.rs), [features](https://github.com/openai/codex/blob/a956835d020762cb2b570053af06f643a11c0ecc/codex-rs/features/src/lib.rs), [config](https://github.com/openai/codex/blob/a956835d020762cb2b570053af06f643a11c0ecc/codex-rs/core/src/config/mod.rs), [question handler](https://github.com/openai/codex/blob/a956835d020762cb2b570053af06f643a11c0ecc/codex-rs/core/src/tools/handlers/request_user_input.rs), [plan handler](https://github.com/openai/codex/blob/a956835d020762cb2b570053af06f643a11c0ecc/codex-rs/core/src/tools/handlers/plan.rs), [agent specs](https://github.com/openai/codex/blob/a956835d020762cb2b570053af06f643a11c0ecc/codex-rs/core/src/tools/handlers/multi_agents_spec.rs).
- Search: [Brave API](https://api-dashboard.search.brave.com/api-reference/web/search/get/index.html.md), [Brave privacy](https://api-dashboard.search.brave.com/documentation/resources/privacy-notice), [Tavily search](https://docs.tavily.com/documentation/api-reference/endpoint/search), [FAQ](https://docs.tavily.com/faq/faq), [privacy](https://tavily.com/privacy).

Local baseline: `internal/agent/agent.go`, `internal/repository/repository.go`,
`internal/actions/edit.go`, `internal/actions/command.go`,
`internal/mcp/manager.go`, `internal/session/session.go`. Inventory found no
current child runtime, question/checklist/web/skill loader, root instructions,
or compiled harness; planned docs do not change that implementation status.
