# Tasks: Tool and agent platform (alpha → beta)

**Status:** proposed checklist. Each item should be checked only after the
linked spec's acceptance criteria and this checklist's release gates pass.

## 0. Scope and decision cleanup

- [ ] Review this roadmap and agree on Alpha/Beta scope.
- [ ] Reconcile `specs/mcp-support/spec.md`: resolved server-level session
      trust vs. contradictory per-call approval wording; keep implementation
      behavior unchanged unless separately decided.
- [ ] Resolve the plan-mode policy for MCP calls (recommended: block all MCP
      calls while plan mode is active).
- [ ] Confirm web-search backend/configuration and network permission UX.
- [ ] Decide initial skills and user-agent file locations/precedence; global
      agent files first, project-local profiles only with an explicit trust
      rule.
- [ ] Audit existing tool names/schemas and document compatibility requirements
      before adding aliases or replacing `edit_file`.

## 1. Foundation — alpha prerequisite

- [ ] Implement the compiled harness tool contract and round-cap continuation
      in [`agent-loop`](../agent-loop/spec.md).
- [ ] Implement project instructions and size/precedence rules in
      [`agent-harness`](../agent-harness/spec.md).
- [ ] Write a buildable registry spec: registration, model schema, input
      validation, source attribution, effect class, availability, output cap,
      cancellation, and parallel-safety metadata.
- [ ] Design one authorization path for built-in, MCP, web, and child-agent
      calls; retain existing edit/command approvals and MCP session TOFU.
- [ ] Move current five built-ins and MCP dispatch behind the registry with no
      visible behavior regression and no import cycle with the agent loop/UI.
- [ ] Add collision detection for built-in, MCP, and future user tool names.
- [ ] Add catalog inspection (`/tools` or equivalent): name, source, purpose,
      availability, and permission class.
- [ ] Test schemas and policy with fake handlers; test unknown tools, malformed
      input, denied calls, cancellation, output truncation, and error fidelity.

## 2. Alpha — core user workflow

- [ ] Specify and implement create + targeted edit/patch tools while keeping
      reviewed full diffs, stale checks, and explicit approval.
- [ ] Specify and implement `ask_user`: question/options, pending state,
      cancellation, answer delivery exactly once, and accessible TUI behavior.
- [ ] Specify and implement one visible plan/todo tool; distinguish it from
      subagent task spawning.
- [ ] Specify web search/fetch: supported backend, config, network consent,
      source URLs, content limits, timeout, and failure behavior.
- [ ] Implement web tools through the registry; no shell/curl fallback and no
      network tool exposed when its backend is unavailable.
- [ ] Implement [`custom-commands`](../custom-commands/spec.md) as user-invoked
      prompt templates; verify commands never execute code, register tools, or
      change permissions.
- [ ] Write a separate skill spec for model-invoked Markdown prompt packs:
      loading, discovery, scope, size limits, and how results enter context.
      A skill may guide tool use but cannot register tools or grant permissions.
- [ ] Implement the skill loader/tool only after that spec; keep skills
      distinct from slash commands and executable extensions.
- [ ] Add provenance labels to MCP, web, and built-in tool activity; preserve
      the transcript and approval UI contracts.
- [ ] Update user documentation with tool inventory, permissions, optional
      setup, and data-flow warnings.

## 3. Alpha — bounded read-only delegation

- [ ] Write a subagent/task spec covering child context, parent context,
      cancellation, time/budget limits, output limits, persistence, and UI.
- [ ] Add a `task` tool that can invoke only registered agent profiles; no
      arbitrary child prompt can expand its tool or permission set.
- [ ] Add built-in read-only `explore`; no nested children and no writes in
      alpha. State the allowed child tools in the model definition and enforce
      them at dispatch.
- [ ] Show child start, progress, completion, failure, and cancellation without
      making users poll or losing the parent transcript.
- [ ] Test the child cannot write, shell, call disallowed MCP, spawn a child,
      or continue work after cancellation.
- [ ] Complete the Alpha exit gate in `spec.md`, including a real-terminal
      walkthrough and at least one live provider structured-tool probe.
- [ ] Publish an alpha tool inventory and clearly label unverified provider
      combinations.

## 4. Beta — user-created agent profiles

- [ ] Specify profile format and validation: name, description, instructions,
      optional model, and a restrictive tool allowlist; reject unknown fields
      that could imply permissions.
- [ ] Implement `/agents` to list and inspect built-ins and user profiles;
      errors identify invalid/duplicate files without preventing app startup.
- [ ] Load global profiles first. Add repo-local profiles only after explicit
      trust and precedence behavior is defined; repository content cannot
      silently authorize tools.
- [ ] Add built-in `review` profile and document how users create profiles
      without writing code.
- [ ] Enforce effective permissions as parent ∩ user policy ∩ profile
      allowlist; profile settings may only reduce capabilities.
- [ ] Test malicious prompt text, unsupported model names, name collisions,
      missing tools, session resume, and no permission escalation.
- [ ] Decide whether any user-defined agent can write in beta. If yes, finish
      isolated worktree creation, review, merge/discard, cleanup, and recovery
      specs before exposing it; otherwise label all profiles read-only.

## 5. Beta — optional advanced capabilities

- [ ] Specify AST query/edit adapters, supported languages, diff review, and
      graceful fallback.
- [ ] Specify LSP lifecycle, server trust/configuration, timeout, supported
      operations, and graceful fallback when a server is missing.
- [ ] Implement AST/LSP as optional adapters; neither changes shell or edit
      approval semantics.
- [ ] Specify memory opt-in, project scope, content limits, inspect/forget,
      retention, provider disclosure, and secret handling.
- [ ] Implement memory disabled by default, with visible state and explicit
      read/write/forget operations; agent profiles cannot enable it silently.
- [ ] Complete the Beta exit gate; run macOS/Linux walkthroughs, supported
      provider probes, installation/config migration checks, and tool-disable
      recovery.

## Dependency map

```text
scope/security decisions
  └─ registry + central policy + harness prompt
       ├─ edit/patch, question, plan/todo, web, skills ── alpha core
       └─ task lifecycle + read-only explore ─────────── alpha delegation
            └─ profile format + /agents ──────────────── beta user agents
                 └─ worktree isolation ───────────────── beta write agents

registry ── AST/LSP adapters ── optional beta code intelligence
registry + privacy spec ─────── optional beta memory
```

Web, skills, and code-intelligence adapters may be developed independently
after the registry/policy seam is agreed. User-defined agents and delegated
writes block on the child permission and isolation specs.
