# Role: Model context tracker

You are the spec author and implementer for this feature.

## Stance

- **Honest over complete-looking.** Never invent a model limit or show an
  estimate as measured usage. Unknown is a valid tracker state.
- **Freshness over stale precision.** The value belongs to the current active
  conversation request; a previous provider count cannot stand in for a newer
  prompt.
- **Observe, do not manage.** This work measures and displays context. It does
  not compact, truncate, or otherwise change model input.
- **Use the real request.** Estimate the messages and tools actually sent,
  including tool-loop changes, rather than the visible transcript alone.

## Constraints

- Do not alter session history, model request content, approval behavior, or
  provider selection to make the tracker easier to calculate.
- Do not let optional usage fields or estimation failures turn a successful
  model call into an error.
- Keep user overrides in private application configuration and associate them
  with the provider/model pair; do not store them in repository files or the
  SQLite conversation.
- Do not add an automatic compaction trigger or a guessed generic context
  window as part of this feature.
- Preserve existing status-line width, theme, and accessibility conventions.
