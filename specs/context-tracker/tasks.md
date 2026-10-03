# Tasks: Model context tracker

**Status:** in progress — implementation and automated verification are complete;
the real-terminal walkthrough remains. Acceptance entry point is `go test ./...`
from the project root.

1. **Define the tracker state and source precedence.** Represent the active
   request's measured or estimated input count separately from its model-window
   limit. Add the nested provider/model `context_windows` override to private
   `config.json`; apply it before provider metadata and the documented catalog.
   Validate limits. Verify precedence, unknown models, invalid values, and
   provider/model scoping with unit tests.
2. **Complete usage capture.** Request usage on supported streaming APIs and
   parse usage both in usage-only events and final events carrying choices.
   Ensure absent usage clears the current response's measured state so a stale
   count cannot replace its estimate. Verify supported wire shapes and
   provider-compatible fallbacks with stream tests.
3. **Estimate outbound input.** Add a testable estimator over the actual
   messages and tools sent for the active conversation. Use a supported
   model-specific tokenizer where available and a labeled local fallback
   otherwise. Account for supported attachment types without presenting a
   partial text-only count as an exact total.
4. **Connect request lifecycle updates.** Publish the pre-request estimate,
   then replace it with measured usage on completion when available. Cover
   tool rounds, steering prompts, failed/cancelled requests, `/compact`, and
   resume; only active-conversation requests update this tracker.
5. **Resolve model limits.** Read positive per-provider/model overrides and
   provider-exposed model metadata before falling back to the documented
   catalog. Keep unknown limits unknown and preserve existing documented
   catalog evidence.
6. **Render the context segment.** Show used/limit, percentage when known,
   `~` for estimates, and `?` for unknown limits. Preserve these markers under
   narrow layout pressure and retain the current 80% warning treatment.
7. **Verify end-to-end behavior.** Add regression tests for latest-request
   freshness, compaction/resume recalculation, both stream usage shapes,
   unknown limits, provider failures/unsupported usage, and narrow rendering.
   Run `go test ./...`; manually inspect the status line at wide and narrow
   terminal widths before marking the feature implemented.
