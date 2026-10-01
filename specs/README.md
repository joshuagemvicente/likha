# Lisa Spec Conventions

This directory is the source of truth for Lisa's behavior and delivery. The contract is
**spec per feature**: each feature has its own folder with tasks, context, role, checklist,
and status, so implementation cannot drift from the plan. The overarching product contract
stays in [v1-spec.md](v1-spec.md); feature folders refine it and never contradict it.

## Layout

```
specs/
  README.md              ← this file: conventions and feature index
  v1-spec.md             ← product contract (single source for FRs, acceptance, release gate)
  setup-plan.md          ← setup and delivery notes
  <feature>/             ← one folder per feature
    spec.md              ← WHAT and WHY: context, role, scope, functional changes, acceptance criteria
    tasks.md             ← HOW: ordered implementation tasks, each with verification
    checklist.md         ← progress gates, mirrors v1-spec.md wording where it overlaps
    context.md           ← links to the code, tests, and docs this feature touches
    role.md              ← the acting stance and constraints for whoever executes this spec
```

## Rules

1. A feature folder is created before its implementation starts; `tasks.md` begins empty of
   checkmarks.
2. `spec.md` states user-visible behavior only. Implementation notes belong in `tasks.md`.
3. Requirement wording in `spec.md` must match v1-spec.md's tone: verifiable sentences, no
   marketing, no unbounded "later" work. If a feature needs a new functional requirement,
   amend v1-spec.md first, then reference it here.
4. `checklist.md` items are phrased as observable outcomes, not code changes.
5. Status words are only: **planned**, **in progress**, **implemented (local)**,
   **verified (release)**. The release gate in v1-spec.md defines the last one.
6. The automated entry point for every feature is `go test ./...` from the project root;
   integration tests live in `tests/integration/`.
7. No skipped, always-passing, or mock-only tests may be used to claim a feature works.

## Feature index

| Feature | Folder | Status |
| --- | --- | --- |
| Predefined accepted providers (BYOK) | [predefined-providers/](predefined-providers/spec.md) | implemented (local) — hosted-provider probes outstanding |
| First-run provider setup in the TUI | [first-run-setup/](first-run-setup/spec.md) | implemented (local) |
| Slash commands in the prompt input | [slash-commands/](slash-commands/spec.md) | implemented (local) — including the `/` command autocomplete popup (typing `/` opens the reserved-command list; see [conversation-compaction/](conversation-compaction/context.md)) |
| Themes and optional Nerd Font icons | [themes/](themes/spec.md) | implemented (local) |
| MCP server support (tools via stdio) | [mcp-support/](mcp-support/spec.md) | implemented (local) |
| Agent harness prompt (system prompt + project instructions) | [agent-harness/](agent-harness/spec.md) | draft — awaiting review |
| @ file/folder references (mention paths in prompts) | [file-references/](file-references/spec.md) | implemented (local) |
| curl installer for released binaries | [curl-install/](curl-install/spec.md) | implemented (local) — no release published |
| ChatGPT Plus/Pro login (OAuth 2) provider | [chatgpt-plus/](chatgpt-plus/spec.md) | implemented (local) — live probe outstanding |
| Prompt line editor, native selection, image attachments | [prompt-editor/](prompt-editor/spec.md) | planned |
| Conversation compaction (/compact) | [conversation-compaction/](conversation-compaction/spec.md) | implemented (local) |
| Thinking control (/think) | [thinking-control/](thinking-control/spec.md) | draft |
| Repo context file (/init → AGENTS.md) | [repo-init/](repo-init/spec.md) | draft |
| Plan mode (/plan read-only) | [plan-mode/](plan-mode/spec.md) | draft |
| Custom commands / skills | [custom-commands/](custom-commands/spec.md) | draft |
| Text transforms (/transform, Wispr-Flow style) | [text-transforms/](text-transforms/spec.md) | draft |
| TUI layout (status bar enrichment, header declutter, scrollbar fix, role backgrounds) | [tui-layout/](tui-layout/spec.md) | implemented (local) — phases 1–3 done (phase 1: header declutter + mini logo, status-bar enrichment incl. git/env/spend/ctx, auto session names, folder/branch default-on; phase 2: content-width breakpoints + per-path no-overflow invariant tests; phase 3: role backgrounds via adaptive-themes); real-terminal walkthroughs pending |
| Muted tool rendering + terminal-native composer keys | [tool-rendering-terminal-keys/](tool-rendering-terminal-keys/spec.md) | in progress — M1 (muted tools) and M2 (composer keys) implemented (local); Ctrl+Delete chord unbindable on bubbletea v1.3.10, carried by Alt+D |
| Repository structure refactor (phased package split) | [structure-refactor/](structure-refactor/spec.md) | implemented (local) — Phase 1 + Phase 1.5 landed 2026-10-01; Phase 2 parked |
| Agent tool loop (read-only search steering + round-cap auto-continue; first slice of agent-harness) | [agent-loop/](agent-loop/spec.md) | planned |
| Adaptive themes (live preview + full-surface color) | [adaptive-themes/](adaptive-themes/spec.md) | implemented (local) — M1 preview, M2 adaptive helpers, M3 bands landed 2026-10-01; live Ghostty/terminal walkthrough outstanding |
| All-provider models in `/models` | [all-models/](all-models/spec.md) | implemented (local) — automated suite green; real-TUI walkthrough outstanding |
| Fast `/models` open (cache + progressive render) | [models-perf/](models-perf/spec.md) | implemented (local) — automated suite green; real-TUI walkthrough outstanding |
