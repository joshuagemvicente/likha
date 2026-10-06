# Agent-harness improvement review: first-party evidence

**Snapshot:** 2026-10-04. **Status:** research, not a spec or approved decisions.
This note proposes mechanisms to assess, not feature-parity or measured-superiority claims.
No hosted-provider requests, interactive competitor probes, or runtime implementations were performed.
The parent session inspected local source and ran `go test ./...`; the latest run passed.

## Scope and baseline

- Read [CLI workflows](../tooling-landscape/cli-workflows.md) and [feature comparison](../tooling-landscape/feature-comparison.md); their older local inventories are not current.
- Source-checked current baseline: harness prompt, tools registry, targeted edits, retained output, bounded read-only children/profiles,
  skills, `ask_user`, plan/todo, and optional web tools are implemented. These are substrates, not missing-feature recommendations.
- OpenCode Go and Dialagram are **user-reported trials**, not compatibility certifications.
- Latest parent-session verification: `go test ./...` passed on the current working tree.
  Three TUI failures observed earlier in the conversation were absent on this rerun.
- The source-grounded local assessment and proposed order appear below. Existing unrelated working-tree changes were not edited.

## Version and evidence boundaries

- Began at [the V2 docs index](https://opencode.ai/v2/docs/); only named V2 pages inform findings, not V1 schemas/behavior.
  Provider/compaction settings concern server/project configuration, not terminal `cli.json`.
- Live, unversioned docs are not installed-binary evidence; the previous Codex v0.160.0 pin does not pin today's docs.
- Gemini loop code is pinned to `acae7124bdd849e554eaa5e090199a0cf08cd782`
  (2026-07-17, latest commit returned for that file); it is not a release-wide inventory. [S9]
  Live docs announce an Antigravity CLI transition for unpaid/Google One users; these findings do not cover that CLI/access. [S10]
- **Observed** means documented/sampled source; **Candidate** means a proposal needing local audit, design, and verification.

## 1. Automatic context management and bounded overflow recovery

**Observed:** OpenCode V2 automatically summarizes older history, keeps approximately 15,000 recent tokens,
and reserves a buffer defaulting to 10% of the limit. It compacts/retries a context-too-long request **once**;
another rejection becomes an error. Earlier messages remain stored; retained-tail tool outputs are shortened. [S1]
Native compaction is opt-in for supported OpenAI Responses deployments/models; encrypted checkpoints are
provider/model/endpoint-specific. Oversized native compaction retries a smaller request, not local summarization.
Fixed instructions/tool schemas can consume the budget, leaving compaction unable to help. [S1]

**Candidate:** add a model-aware preflight budget and visible automatic-compaction boundary.
Reuse retained outputs for prompt-only elision of superseded reads/results, without rewriting
the inspectable transcript. Preserve tool-call/result pairs, pending work, and artifact references.
Allow one overflow recovery, then explain failure. Keep approvals/plan restrictions in runtime state, never recreated by summaries.

## 2. Provider adapters, capabilities, and retry policy

**Observed:** V2 separates native/compatible runtime packages, model capabilities/compatibility, limits, and variants.
Package-specific settings require runtime support. vLLM discovery cannot infer tool support, so tools start disabled.
HTTP header/chunk waits default to five minutes, whole-request timeout has no default; timeout retries stop after three. [S2]
Codex separates HTTP retries (default four), SSE interruption retries (default five), stream-idle timeout,
WebSocket support, and a Responses-only `wire_api`. [S3]

**Candidate:** audit Likha's wire adapters separately from provider labels. Maintain explicit
tool/media/reasoning/schema/cache/usage capabilities with provenance and unknown states.
Use fixture-based conformance cases for split tool arguments, finish reasons, partial streams,
and usage accounting. Classify auth/schema/overflow/rate-limit/transient failures; bound retries
with cancellable backoff/deadline; never replay an executed tool after a generation retry. No universal endpoint parity is established.

## 3. Loop detection and whole-run budgets

**Observed:** sampled Gemini source hashes tool name plus JSON arguments and checks cycles of length one through five
repeated five times. It detects repetitive streamed content, exempts code blocks, and starts model checks after 30 turns.
The diagnostic prompt distinguishes unchanged outcomes from batch work/productive edit-test iteration;
the cheap hash detector itself does not compare outcomes. [S9]
Gemini exposes loop-disable and `maxSessionTurns` settings, with the latter defaulting to unlimited. [S10]
Codex's rollout token-budget feature is documented as **under development and off by default**,
not an established default dollar or wall-clock limit. [S3]

**Candidate:** combine cheap normalized call/result fingerprints with progress evidence and
user-configurable root-run elapsed-time, request, and token budgets. Include children, retries, compaction,
and title/helper requests in accounting rather than relying only on existing child caps.
An opted-in exhausted budget could pause with partial findings/reason and offer continuation.
Do not reinstate Likha's former arbitrary 32-round failure: FR-20 deliberately keeps checkpoints non-terminal.
Dollar caps need supported pricing; unknown is not zero. Budget behavior needs an explicit spec decision.
Optional model-based loop diagnosis adds calls/cost and should not be the only termination guard.

## 4. Shell lifecycle is distinct from shell authority

**Observed:** Gemini documents a 300-second shell **inactivity** timeout and background completion behavior. [S10]
Codex exposes PTY-backed execution and a background-terminal **poll window**. Neither setting alone
proves a total command lifetime limit or process-tree cancellation guarantee. [S3]

**Candidate:** define explicit command states, bounded output/artifact retention, exit status,
wall-clock deadline, and distinct cancellation/timeout results. Cancellation should propagate
through the run and children, terminate the process group with a bounded grace period, and reap
processes; background jobs need ownership/shutdown policy. Retained output exists: improve lifecycle guarantees, not storage again.
Process-tree escape handling and cross-platform behavior remain verification work, not findings.

## 5. Sandboxing adds containment; approval does not

**Observed:** Codex separates OS-enforced boundaries from approvals; local workspace-write networking defaults off.
Its command proxy excludes model/authentication traffic, hosted search, apps, and MCP connections. [S4]
Claude Code's sandbox defaults off and wraps shell commands/subprocesses, not file/web tools, hooks, or MCP servers.
It supports regular approvals inside the sandbox, strict no-unsandboxed-retry, and fail-if-unavailable startup.
Default reads and inherited environment variables can expose credentials without added restrictions. [S6]

**Candidate:** preserve Likha's exact approvals while evaluating an optional fail-closed shell
sandbox with explicit write roots, credential/environment filtering, and independent network
policy. Show unsupported-platform state; no silent unsandboxed fallback or claims that file confinement contains shell execution.
MCP and other subprocesses need a separate threat model; blanket approval is not a substitute.

## 6. Edit validation and bounded repair feedback

**Observed:** Aider auto-lints edited files by default, supports custom/per-language commands, and reads nonzero
exit status plus stdout/stderr as diagnostics. Auto-testing is opt-in with `--test-cmd`/`--auto-test`; builds can use
the same mechanisms. A formatter's nonzero exit can be mistaken for a lint failure. [S12]

**Candidate:** add configurable post-approval parse/lint/build/test feedback, selected by touched
files and project conventions. Report the exact command, status, diagnostic preview, and full
artifact; separate validation failure from edit-publication failure. Count repair attempts against
run budgets. Treat validators as executable, potentially side-effecting project code requiring
the normal command gate. Formatting must join the reviewed proposal or get separate approval, not invisibly mutate approved edits.

## 7. Reversible checkpoints that do not discard user work

**Observed:** Claude Code checkpoints turns and can restore code, conversation, or both. Only tracked file-tool edits
are recoverable: Bash changes/most subagent edits are not restored. External edits normally aren't captured except
when touching the same files; symlinked/hard-linked paths are skipped. Docs do **not** promise arbitrary overlapping
manual/concurrent edits survive rewind. Checkpoints do not replace Git. [S7]

**Candidate:** journal before/after bytes and hashes for each approved Likha write, including
create/rename/delete semantics where supported. Undo only when current contents match the
recorded post-image; otherwise refuse or offer a separately reviewed inverse/three-way change.
Preserve dirty starting state, index, unrelated/untracked files, and later user modifications.
Expose exactly what is undoable, not shell effects automatically; never implement rewind with broad checkout/reset operations.

## 8. Headless structured execution as an evaluation seam

**Observed:** `codex exec` separates stderr progress from final stdout, offers JSONL lifecycle/tool/usage events,
JSON-Schema final output, ephemeral runs, and resume; its documented default is a read-only sandbox. [S5]
Gemini's CLI configuration reference documents `--output-format json` and `stream-json`. [S10]

**Candidate:** a small one-shot Likha adapter over the same core, with versioned events, stable
failure/approval-required outcomes, and optional final-schema validation; no TUI scraping.
Headless execution must not implicitly approve writes/shell/MCP or silently answer questions.
Use recorded provider streams and temporary fixture repositories for repeatable evaluation of
overflow, malformed edits, approval denial, cancellation, budgets, and user-preserving undo.
Score verified tasks, invalid calls, retries, tokens/cost, and latency separately; no live benchmarks were authorized or run here.

## 9. Repository maps before expansive code-intelligence infrastructure

**Observed:** Aider supplies paths and important symbol definitions/signatures, dependency-graph-ranked within
a relevance-sensitive token budget. `--map-tokens` defaults to 1,000 but may expand, especially without files in chat.
This is an overview/retrieval aid, not evidence of universal semantic navigation or better outcomes. [S11]

**Candidate:** evaluate a bounded, ignore-aware path/symbol map with content-hash invalidation,
language coverage disclosure, and a hard prompt budget. Return file/line provenance and use
existing reads to verify current code. Measure whether it reduces irrelevant inspection on local
fixtures before embeddings/executable LSP services with their extra complexity/authority. A directory tree is not a symbol map.

## 10. Per-model effort/harness policy and cache-aware prompt assembly

**Observed:** Codex exposes model-advertised reasoning effort, a plan-specific override, and
replacement instruction files. V2 has model variants and runtime/capability overrides. [S3] [S2]
Gemini aliases distinguish family-specific thinking budgets/levels and utility roles such as summarization/loop detection. [S10]
Claude Code orders stable system/tools, project context, then conversation for exact-prefix
caching. Model switches invalidate cache; effort changes do so on most models, with documented
model/provider exceptions. It reports cache-read/write usage and describes compaction's cache
tradeoffs. Gateway handling can affect whether cache markers actually work. [S8]

**Candidate:** version optional model-family harness/effort settings, without weakening common
permission rules. Reject or visibly mark unsupported effort rather than silently pretending it
applied. Keep stable prompt/tool ordering and append dynamic state; track cache-read/write and
unknown usage separately. Do not ignore changed safety instructions merely to keep a prefix warm.
Auxiliary routing/cache warming need explicit cost/privacy choices; uniform caching or cheaper helpers isn't established for Likha.

## Local assessment and proposed order

These priorities are an engineering assessment from the current code, not a competitor
benchmark, approved roadmap, or authorization to change existing product boundaries.

### Existing strengths to preserve

- The [compiled prompt and root loader](../../internal/agent/harness.go) are implemented;
  root-only `AGENTS.md`, the 32-KiB reject-on-overflow cap, and frozen run instructions are deliberate policy.
- The [registry](../../internal/tools/registry.go) validates arguments and enforces capabilities
  before authorization. [Targeted edits and shell calls](../../internal/agent/tool_registry.go)
  still require exact proposal/command approval; plan mode blocks mutations and MCP.
- [Retained output](../../internal/tooloutput/store.go) already provides bounded private artifacts;
  this is not a missing feature. Improve request-context selection and output usefulness instead.
- [Read-only children](../../internal/explore/types.go) already have depth, concurrency, spawn,
  time, and request caps. [Profiles](../../internal/profiles/policy.go) cannot widen that ceiling.

### Priorities

| Priority | Improvement | Current local evidence | First slice / success condition |
| --- | --- | --- | --- |
| P0 | Completion-aware provider handling | [Chat stream chunks](../../internal/model/client.go#L952) do not retain `finish_reason`; [Responses decoding](../../internal/model/codex.go#L428) handles `response.completed` and `response.incomplete` together as successful terminal events. | Distinguish completed, token-limited, filtered, and failed generations. Never call an incomplete response a finished task or execute incomplete tool proposals. Verify both wire surfaces with conformance cases. |
| P0 | Request resilience and capability-aware behavior | [Chat requests](../../internal/model/client.go#L418) retry only rejected optional usage telemetry; [OAuth requests](../../internal/model/client.go#L538) retry token refresh once. Other transport/HTTP failures end the run. The HTTP client has no configured response-header/stream-idle deadlines. | Typed errors, cancellable bounded backoff for appropriate rate-limit/transient failures, and separate header/idle-stream timeouts. Never replay executed tools, switch providers silently, or count unknown capabilities as supported. |
| P1 | Automatic context management | [Main requests](../../internal/agent/turn_execution.go#L124) resend accumulated history; [compaction](../../internal/agent/compaction.go) is one manual summarize call. Individual results are bounded, but their aggregate context still grows. | Preflight with known model limits and a generation reserve; prune obsolete request-only outputs, compact at safe boundaries, and recover from overflow at most once. Preserve goals, refusals, tool/result pairing, and inspectable execution evidence. Unknown model limits need an explicit policy, not a guessed capacity. [S1] |
| P1 | Explicit coding verification and bounded repair | The [prompt](../../internal/agent/prompt.md) requires honest reporting but does not establish a specific inspect → change → focused checks → repair workflow. The [turn loop](../../internal/agent/turn_execution.go#L155) ends when the model returns no tools. | Add proportionate workflow guidance and recorded check outcomes. Every validator command still gets normal approval. Distinguish edit applied, checks passed, checks failed, and verification not run; do not require tests for pure explanation tasks. [S12] |
| P1 | Progress guards and optional root-run budgets | The [main loop](../../internal/agent/turn_execution.go#L124) continues indefinitely with notices every 32 rounds; the [TUI](../../internal/tui/tui.go#L404) supplies a cancellation context, not a run deadline. Child caps do not cap the main run or total spend. | Detect repeated unchanged failures using call/result/progress evidence, not identical tool names alone. Offer user-controlled request/token/active-time budgets across main and helpers; show unknown costs accurately. Keep the current non-terminal checkpoint contract unless the user opts into a new budget policy. [S9] [S10] |
| P1 | Repeatable harness evaluations | [CLI integration tests](../../tests/integration/cli_test.go) cover startup errors; passing protocol/unit checks does not establish live-model task completion quality. | Use the same disposable coding tasks on OpenCode Go and Dialagram, recording exact model IDs, harness version, verified outcome, invalid calls, requests, tokens, and latency. Add a structured/headless driver only under a separate scope decision. [S5] |
| P2 | Execution containment | [Commands](../../internal/actions/command.go#L47) run `sh -c` with process-group cancellation and a bounded post-exit wait, but ambient filesystem/network authority remains. Output arrives after command completion. | Evaluate optional fail-closed shell sandboxing, explicit environment/secret policy, command deadlines, and live output. Keep approval distinct from containment; MCP/subprocesses need their own stated boundary. [S4] [S6] |
| P2 | Safe undo after completed edits | The [edit journal](../../internal/session/edit_journal.go) is crash-recovery evidence, and the [recovery UI](../../internal/tui/tool_wiring.go#L115) explicitly performs no replay or rollback. That is not a successful-edit rewind feature. | Reuse journal evidence for separately reviewed inverse proposals; refuse overlapping later user edits. Do not rewind with broad Git resets or promise to reverse arbitrary shell effects. [S7] |
| Later | Repository symbol maps and model-specific effort/cache policy | Current inspection is textual; [provider records](../../internal/model/provider.go) describe connection defaults, not a complete model capability matrix. [Thinking control](../thinking-control/spec.md) remains a draft. | Measure whether a small ignore-aware symbol map improves discovery before adopting LSP. Add effort or model-family prompt variants only with provider/model evidence, retaining common safety rules and unknown usage states. [S2] [S3] [S8] [S11] |

### Two additional correctness reviews

1. **Compaction trust:** [CompactHistory](../../internal/agent/compaction.go#L47) stores a generated
   summary as a developer message; [Responses encoding](../../internal/model/codex.go#L55)
   folds it into top-level instructions. Review instruction-laundering risk and keep summaries
   attributed as continuation data, not new policy or historical permission. Runtime approvals
   remain enforced; this inspection did not demonstrate an approval bypass or a prompt-injection exploit.
   Changing summary authority would amend the existing compaction spec.
2. **Connection-check claims:** [EnsureConnected](../../internal/model/client.go#L924) returns
   immediately after a successful cached check. The README's claim that revoked keys are checked
   before each prompt is stronger than this path establishes. Either define a fresh/TTL policy or
   document that later revocation is caught when a generation request fails.

### Recommended next decision

Start with completion/error handling and provider conformance, then automatic context management
and the verification workflow. Add progress guards alongside those, without silently restoring a
fixed-round cutoff. Defer write-capable agents, executable plugins, broad permission grants, and
LSP until measured needs justify their authority and complexity.

Automatic compaction, root budgets, sandboxing, headless execution, and rewind need new or amended
feature specs: current choices are manual compaction, non-terminal main checkpoints, explicit
per-proposal edit/shell approvals, read-only children, and an interactive TUI. No requirements,
runtime code, permission defaults, or tests were changed by this research. A future evaluation
suite is a new work item, not an amendment to the earlier no-new-tests implementation milestone.

## Sources

Twelve selected topic sources, all fetched live on 2026-10-04. Reference links apply to each
adjacent competitor finding; candidate paragraphs intentionally describe unapproved proposals.

[S1]: https://opencode.ai/v2/docs/compaction
[S2]: https://opencode.ai/v2/docs/providers
[S3]: https://developers.openai.com/codex/config-file/config-reference.md
[S4]: https://developers.openai.com/codex/agent-approvals-security.md
[S5]: https://developers.openai.com/codex/non-interactive-mode.md
[S6]: https://code.claude.com/docs/en/sandboxing
[S7]: https://code.claude.com/docs/en/checkpointing
[S8]: https://code.claude.com/docs/en/prompt-caching
[S9]: https://github.com/google-gemini/gemini-cli/blob/acae7124bdd849e554eaa5e090199a0cf08cd782/packages/core/src/services/loopDetectionService.ts
[S10]: https://geminicli.com/docs/reference/configuration/
[S11]: https://aider.chat/docs/repomap.html
[S12]: https://aider.chat/docs/usage/lint-test.html
