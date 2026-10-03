# Context: web implementation and sources

- `internal/providers/config.go`, `internal/providers/keyfile.go`: current private state conventions; search credentials remain separate.
- `internal/model/client.go`: request context conventions; do not reuse model auth for browsing.
- `internal/agent/agent.go`, `internal/tui/tui.go`: registration, interaction, cancel, and result seams.
- [Registry](../tool-registry/spec.md), [output](../tool-output/spec.md), [limits](../tooling-platform/decisions.md): authorization, retention, and bounds.
- [Brave Web Search API](https://api-dashboard.search.brave.com/api-reference/web/search/get/index.html.md) and [authentication](https://api-dashboard.search.brave.com/documentation/guides/authentication): initial adapter, checked 2026-10-03.
- [Brave API privacy notice](https://api-dashboard.search.brave.com/documentation/resources/privacy-notice): query retention; recheck before shipping.
- [CLI comparison](../tooling-landscape/cli-workflows.md): distinguishes hosted search, network fetch, and shell policy.
