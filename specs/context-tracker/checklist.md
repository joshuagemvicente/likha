# Checklist: Model context tracker

Status mirrors [spec.md](spec.md) acceptance criteria. Automated outcomes are
checked; the real-terminal walkthrough remains pending.

- [x] The latest active-conversation request is represented by its own measured
      usage or a fresh estimate, never a stale prior measurement.
- [x] The tracker estimates before requests and refreshes after compaction and
      resume; generated output and unsent composer text are excluded.
- [x] Provider usage is parsed from supported stream event shapes without
      making optional telemetry a request requirement.
- [x] Estimates are marked `~`, use a tokenizer when supported and a local
      fallback otherwise, and cover the input Likha actually sends.
- [x] Window limits follow private `config.json` override → provider metadata →
      documented catalog; unknown limits show `?` and never a guessed
      percentage.
- [x] Wide and narrow layouts show the count/limit honestly, retain estimate
      and unknown markers, and keep the 80% warning behavior.
- [x] Compaction and resume show the active history's count without persisting
      or replaying stale tracker data.
- [ ] `go test ./...` passes and the real TUI has been checked at wide and
      narrow terminal widths.
