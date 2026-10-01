# Lisa v1: Incremental Feature and Test Plan

This plan applies to the requirements in [v1-spec.md](v1-spec.md). A feature is integrated only when its user-visible behavior works in the application, a behavioral test checks an observable failure mode, and its smoke scenario has been run. The shared automated entry point is `go test ./...` from the Lisa project root. Go unit tests live beside the code they test; `tests/integration/` holds tests that launch the executable. Do not add skipped, always-passing, or mock-only tests to claim a feature works.

## Feature 1 — Full-screen TUI, startup, identity, and model connection (FR-01–04, FR-12, FR-14)

**Implemented:** `lisa` selects a repository, displays its identity in an alternate-screen TUI, accepts prompts, streams text from a configured `/v1` endpoint, exposes previous/next pages instead of scrolling, and cancels active runs. The model name is required; the endpoint defaults to local Ollama. Text and tool calls follow the OpenAI-compatible SSE protocol. *(Historical record: the `local` provider row was later removed — see the provider-only pivot note in CHANGELOG.md. The OpenAI-compatible surface and all smokes remain valid against hosted providers.)*

- **Automated entry point:** `go test ./...` runs path/configuration tests, model-client streaming/fragmented-tool/error/cancellation tests using a local HTTP server, and TUI viewport, paging, resize, draft input, and stale-event tests.
- **Observed smoke:** Launched the actual TUI against a local OpenAI-compatible protocol server; checked logo and full-screen layout, streamed long output across four pages, navigated to older pages, cancelled a stream, and submitted another prompt afterward. *(The README's real-model claim was later verified: see "Real-model walkthrough" in Feature 2 below.)*
- **Release validation still required:** Repeat the TUI flow against a live-verified hosted provider and inspect resize/terminal restoration on each supported platform. *(Partially completed 2026-09-29 before the provider-only pivot: real Ollama `qwen3:4b` verified locally — connection, setup-skip via stored config, structured tool call, edit approval, session resume. Remaining: the published-binary walkthrough on each supported target.)*

## Feature 2 — Repository reading and agent tool loop (FR-05, FR-09)

**Implemented:** `internal/repository` confines list/read/literal-search operations to the selected repository, rejects traversal and symlink escapes, enforces size/result limits, and returns errors for unreadable or binary content. `internal/app/agent.go` exposes only these tools and reports both success and failure to the model and TUI.

- **Automated entry point:** `go test ./...` exercises real temporary repositories, path escape attempts, limits, the full tool-call conversation, and a rejected read sent back to the model. No dummy tool results are used.
- **Observed smoke:** A local protocol server requested `read`; the TUI displayed the request, actual repository entries, and a final assistant response after the tool result.
- **Real-model walkthrough (2026-09-29):** Completed the documented flow against real local Ollama 0.34.4 with `qwen3:4b` — structured `read` call returned real repository entries, an `edit_file` proposal rendered as a diff review, approval applied the displayed change (file contents verified), and the completed session was listed and resumed with full conversation context; a follow-up `read` on the resumed session confirmed persisted state. The documented real-model model combination is **verified locally**.
- **Release validation still required:** Repeat glob/read/grep and inaccessible-path cases with a live-verified hosted provider.

## Feature 3 — Reviewed edits and commands (FR-06–09)

- **Implemented:** `edit_file` shows a unified diff and blocks writes until the user reviews and approves every page. `run_command` shows the exact shell text, working directory, and sandbox warning. Rejection leaves files and command side effects untouched; stale edits fail.
- **Automated entry point:** `go test ./...` covers edits, conflicts, command outcomes, approval transitions, hidden control characters, and pagination. `go test -race ./...` passed locally.
- **Observed smoke:** In the installed local macOS ARM64 archive, rejected and approved the same file edit, then rejected and approved a command. Checked the file contents, command side effect, and exit status in the TUI. The protocol server was local; real-model compatibility remains unverified.

## Feature 4 — SQLite session continuity (FR-10–11)

- **Implemented:** Lisa stores repository-associated conversation snapshots in private SQLite state and selects sessions with `--sessions` and `--resume ID`. Cancellation and exit do not grant pending approvals. A stale concurrent save fails without replacing another process's session.
- **Automated entry point:** `go test ./...` exercises reopen, isolation, schema version checks, rollback, interrupted approvals, and concurrent-save conflicts. `go test -race ./...` passed locally.
- **Observed smoke:** Exited after completed turns and resumed them in the installed macOS ARM64 binary. Exited during an edit review, resumed without executing the edit, and confirmed another repository could not load that session. Used `sqlite3 .backup` and resumed a known session from a separate restored state directory.

## Feature 5 — Shareable release (FR-13)

- **Implemented locally:** `scripts/release.sh` builds versioned macOS/Linux archives for AMD64 and ARM64 with a checksum manifest. `scripts/smoke-release.sh` extracts a host archive and checks `--version` and `--help`.
- **Observed checks:** Four `v0.1.0-local` archives built and passed SHA-256 verification; the macOS ARM64 archive passed installation smoke and the local-protocol TUI walkthrough.
- **Release gate outstanding:** No published archive has been tested on every supported target with a live-verified provider. A local cross-build does not prove those target installations work.

## Gate for every integration

1. Implement the smallest complete user-visible slice; avoid empty packages or hypothetical interfaces.
2. Add an automated test exercising a consumer-visible success and a plausible failure/transition for that slice. The tests must pass through the same module interface callers use.
3. Run `go test ./...` and the feature's smoke scenario. Fix failures before starting the next slice.
4. Update the spec and user documentation if the implemented behavior or usage changes. State explicitly which later feature behaviors remain unavailable.
