# Shared decisions and limits

Approved 2026-10-03. Feature specs reference this table instead of choosing
different values. Changing an approved limit or capability ceiling requires
the user's agreement and updates to its acceptance guide.

| Item | Contract |
| --- | --- |
| Comparison | Selected CLI workflows from Claude Code, OpenCode V2, OMP, Pi, Codex CLI; primary sources, no parity claim |
| Work authorization | Documentation now; implementation only after separate approval |
| Implementation workers | Eight parallel subagents on independent ownership areas; coordinator owns shared integration |
| Verification | No new automated tests or code-review pass; run existing checks, retain failures, label unexercised behavior unverified |
| Shell | Exact command/cwd approval each time; cwd is not a sandbox; detached jobs may survive cancellation (amended 2026-10-04 by [command-permissions](../command-permissions/spec.md), user-approved — prompted commands keep this rule; read-only commands, trusted verification checks, and session-allowed exact commands run without a prompt and time out after 10 minutes; destructive commands are refused or always ask) |
| Main rounds | Visible continuation every 32 tool rounds; user cancellation remains available |
| Explore capability | Repository glob/read/grep; task only for permitted explore children; same configured provider/model |
| Explore depth | Main 0 → explore 1 → explore 2; dispatch refuses deeper spawning |
| Explore concurrency | Four executing children across the run tree; waiting/queued parents use no execution slot |
| Explore total spawns | 16 accepted child creations per main run, including nested/completed/cancelled children |
| Explore deadline | Five minutes per child from accepted creation, including queued/waiting time; ancestor cancellation/deadline can end it sooner |
| Explore rounds | 32 model requests per child; return bounded partial findings with a limited status at exhaustion |
| Task lifecycle | Await result; preserve original call/result order; failure/limit returns partial findings, not an automatic main-run failure |
| Cancellation | Whole-run Esc/Ctrl+C cancels descendants; dedicated tree action cancels a selected branch |
| Root instructions | Selected repository root AGENTS.md only, 32 KiB; reject oversized/unsafe input with visible warning |
| Skills | Private global `<stateDir>/skills/<name>/SKILL.md`; at most 32 advertised skills; 32 KiB body cap; reject oversized body |
| Skill authority | Instructions only; main agent/user invocation; no scripts, resources, includes, remote catalogs, or permission fields |
| Model-visible tool output | 64 KiB inline maximum per result, including metadata; byte-safe UTF-8 boundaries |
| Retained output | Five MiB per call, 50 MiB per session, private session-linked artifacts; no automatic context injection |
| Output lifetime | Persist with session; explicit clear and session deletion remove artifacts; missing output never triggers rerun |
| Checklist | At most 32 steps; persist with session; restored state grants no permissions/restart |
| Web search | One configured search backend: `brave`, `tavily`, `exa`, or `duckduckgo` (revised 2026-10-04 by user approval — OpenCode-style pluggable providers); enabled search with no backend named uses keyless `duckduckgo` (revised 2026-10-04 by user approval — keys only when the user opts into a keyed backend); no vendor/shell fallback, including from a keyed backend missing its key; at most 10 results |
| Web fetch | Explicitly enabled unauthenticated public HTTPS, local text/HTML/Markdown conversion; two MiB received-body cap |
| Web timeout | 30 seconds for network/processing after consent; pending consent remains cancellable |
| Redirects | At most five for fetch; validate address/origin on every hop; new origin needs its own grant; search credentials never follow redirects |
| Web grants | Active conversation, in-memory only; clear on session switch/resume, exit, or backend configuration changes; switching backends requires fresh consent |
| Web disclosure | Search query reaches the configured backend (Brave keeps its 90-day retention disclosure; Tavily/Exa disclose flow with retention per their policies; DuckDuckGo discloses its unofficial-endpoint fragility); fetch reaches destination sites; selected result content reaches the configured model provider |
| Existing confinement | Canonical root identity, traversal/symlink defenses, current repo/file safety caps remain unless a feature specifies a deliberate change |

## Permission distinctions

Repository edits, shell execution, and new network destinations use their
defined approval paths. Private transcript/checklist/artifact persistence is
harness-owned session state, not an authorization to mutate the repository.
Explore's transcript writes do not grant its model a write tool.

MCP trust follows FR-16: the running manager remembers approved servers until
app relaunch. Phase 1 does not broaden that grant or persist it to SQLite.
Web grants have the narrower conversation lifetime in the table above.

Instruction text, skill text, web content, child findings, and saved history
cannot override these dispatch rules. Enforce tool ceilings even for manually
crafted calls to tools omitted from the model catalog.
