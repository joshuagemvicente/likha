# Feature: tool output, expansion, and retained artifacts

**Status:** implemented (local); new cap/inspection/resume walkthroughs unverified.
**Phase:** 1. **Product requirements:** FR-09–11, FR-27.
See [implementation evidence](../tooling-platform/implementation.md).

## Transcript behavior

Render a compact muted tool row with name, source, target summary, status, and
bounded result preview. A focused row can expand/collapse with Enter or Ctrl+O;
Tab navigates inspectable rows; Back closes focused inspection, while
Esc/Ctrl+C still cancels an active run. Document focused controls in the footer.
Full-screen scroll, native terminal selection, unknown/error states, and narrow
terminal reachability remain intact. Inspection does not approve an action.

Keep pending approval content separate from collapsed result rows. All paths
and the complete proposed diff remain reachable before Approve enables;
expanding past output never counts as reviewing an unrelated pending proposal.

## Model and capture limits

Use the [shared bounds](../tooling-platform/decisions.md). Capture a useful
UTF-8 preview, preserve final command exit status, and signal truncated/incomplete
content. A command with exit 0 may have limited captured output; display both.
Continue draining command pipes after the capture cap so the process cannot
block on an unconsumed stream. This does not add process sandboxing or guarantee
that detached descendants stop.

Beyond the inline limit, retain output in private session-linked artifacts up
to per-call/session caps. On cap exhaustion stop retaining overflow, preserve
the preview, and report discarded bytes when known. Do not silently evict older
artifacts. Show an explicit clear action to reclaim the session allowance.
Do not store credentials from configuration; command/content output can still
contain secrets and needs a visible private-storage/provider disclosure.

## Retrieval and persistence

Expose `read_output` to the main agent with required `artifact_id` and optional
1-based `offset`/positive `limit`. Return numbered textual pages within the
inline cap, a next offset, completeness, and owner/source identity. An ID is a
lookup capability scoped to the active session; it is not a file path or URL.
Refuse traversal, arbitrary local files, foreign-session IDs, and expired IDs.
Explore does not receive this extra tool; its findings remain bounded.

The UI can inspect session-owned main/child artifacts without injecting them
into model context. The model receives only requested pages, not the entire
artifact or automatic old output on resume. Keep metadata references in saved
results and store artifacts privately with the owning session. Explicit output
clearing and session deletion remove artifacts with recoverable error reporting.

On failed/missing retention or clearing, keep transcript previews usable and
show the storage error. On resume, a missing artifact is unavailable; no command,
read, or network call runs to recreate it. Persist interruption/completeness
metadata so unfinished capture cannot appear complete.

## Acceptance guide

- Expand and collapse main/MCP outputs at wide/narrow sizes without losing
  viewport position, provenance, or approval controls.
- Produce output beyond inline/capture/session caps; preview, artifact pages,
  exit status, and discarded-content warning agree.
- Retrieve an artifact page, then attempt an external path or foreign ID; only
  the current session-owned page reaches the model.
- Resume, clear, and remove output; old previews survive missing storage and
  no generating command is replayed.
- Cancel capture and inspect the interrupted result; preserve identity and
  partial state without flooding the main conversation.
