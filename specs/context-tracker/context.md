# Context: Model context tracker

Code and existing behavior this feature integrates with. The feature contract
is in [spec.md](spec.md); implementation order is in [tasks.md](tasks.md).

## Code

| Path | Relevance |
| --- | --- |
| `internal/agent/agent.go` | Builds active turn history, adds steering prompts and tool results, and calls the model repeatedly; request estimates must follow this actual history. |
| `internal/agent/compaction.go` | Sends the pre-compaction conversation for summarization and returns replacement history; tracker must move to the replacement context after success. |
| `internal/model/client.go` | Builds streaming requests, parses usage events, and stores the latest response usage; usage handling and freshness belong at this seam. |
| `internal/model/windows.go` | Holds `TokenUsage` and the documented context-window catalog; maintain catalog evidence and model-limit resolution here or at its model-level boundary. |
| `internal/model/provider.go` | Defines provider identities and endpoint configuration; metadata support must respect provider capabilities and pair limits with the selected provider/model. |
| `internal/providers/config.go` | Stores private provider/model configuration in `config.json`; `context_windows` adds a positive override nested by provider ID and model ID. |
| `internal/tui/status_line.go` | Renders `contextSegment`; current behavior is measured-usage-only and renders `ctx —` without usage or a known window. |
| `internal/tui/tui.go` | Receives completed run events and currently copies provider usage into the TUI state. |
| `internal/tui/status_line_test.go`, `internal/tui/status_view_test.go` | Existing segment and layout coverage; update expectations for estimated and unknown-limit forms. |
| `specs/tui-layout/spec.md` | Records the currently implemented status-bar contract; this feature supersedes only its context-segment behavior when implemented. |
| `specs/conversation-compaction/spec.md` | Defines manual compaction behavior; tracker recalculation does not add automatic compaction. |

## Protocol references

- OpenAI-compatible chat completion streaming usage can appear in a terminal
  usage-only event or with a final choice; handle only documented/provider
  supported shapes and tolerate missing usage.
- Provider model-list metadata is optional and may be incomplete or incorrect;
  catalog entries remain documentation-backed and user overrides are explicit.

## Related patterns

- [OpenCode custom providers](https://opencode.ai/docs/providers/#custom-provider)
  define `limit.context` on a model configuration.
- [OMP custom models](https://omp.sh/docs/custom-models) supports `contextWindow`
  on model definitions and `modelOverrides` when discovered metadata is wrong.
- [Claude Code status lines](https://code.claude.com/docs/en/statusline) expose
  used percentage and window size; its [model configuration](https://code.claude.com/docs/en/model-config)
  documents `CLAUDE_CODE_MAX_CONTEXT_TOKENS` for custom model IDs and gateways.
