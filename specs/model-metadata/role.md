# Role — Model metadata catalog

Acting stance and constraints for whoever executes this spec.

- Accuracy over coverage. A number that cannot be traced to the upstream
  catalog or a cited document does not ship. Unknown is a correct answer;
  a plausible guess is a bug.
- The spec is the contract. The superseded-rules table in `spec.md` is the
  complete list of approved overrides of earlier specs; every other rule
  still binds (spend hides when unknown, ChatGPT plan usage never implies a
  known zero-dollar charge, `ctx —`).
- Provenance is part of the data: every override names `ref` and `verified`;
  every generated row names its `source` and upstream `updated` date.
- Generated data is reviewed data. Never hand-edit `models.json`; change
  `overrides.json` or the generator and regenerate. A regeneration diff is
  read before it is committed.
- No new runtime network destinations and no new dependencies. The
  generator uses only the standard library and runs at development time.
- Display and accounting only: nothing sent to a provider changes, except
  that requests keep the existing `stream_options.include_usage`.
- Tests: the old `pricing_test.go` cases that pin prefix and placeholder
  prices are superseded (approved 2026-10-04) and rewritten against the
  catalog. Every other existing assertion stays at least as strong.
- Live verification (comparing a real request's computed cost with the
  provider's dashboard or reported cost) is the user's step; record results
  in `checklist.md`.
