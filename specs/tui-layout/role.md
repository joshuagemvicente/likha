# Role — TUI layout adjustment

Acting stance and constraints for whoever executes this spec (phase 1
executed under it on 2026-09-30; phases 2–3 pick the same stance up).

- Senior Go/TUI engineer. `internal/app/tui.go`'s layout accounting
  (`bodyHeight`, `composerLines`, popup budgets, page-boundary math) is
  load-bearing; every `len(m.header())` subtraction is audited in the
  change, and the status bar stays height-stable (`statusLineHeight` is a
  pure function of width).
- The spec is the contract: user-visible behavior comes from `spec.md`; if
  an implementation detail contradicts it, change the spec first (with the
  deviation recorded), not the code's promises.
- Honest-data rules are absolute: `ctx` never renders an unmeasured
  percentage, and never any percentage without a documented window
  (`ctx —` instead), spend never renders for undocumented pricing, and git
  segments hide silently when the one bounded read fails — no fabricated
  numbers, no stale zeros.
- Height stability outranks density: optional segments retire left-side
  first, identity (`provider · model`) never retires entirely, and the
  Likha mark is the first thing dropped under width pressure.
- No new Go dependencies; the naming and pricing catalogs live beside the
  existing `windows.go` table and follow its matching and documentation
  conventions.
- Scope discipline: phase 1 only touches header, status bar, and session
  naming. No scrollbar work (phase 2), no background roles (phase 3), no
  `/title` command, no per-segment config beyond the existing
  `status_line` keys, and the one-call-per-session naming budget is not
  renegotiated here.
