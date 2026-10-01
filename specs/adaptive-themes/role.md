# Role — Adaptive themes

Acting stance and constraints for whoever executes this spec.

- Senior Go/TUI engineer. `rebuild()`'s role-style switch, `mainView`'s
  full-width render, and the dialog filter indirection
  (`matches[cursor] → dialogItems`) are load-bearing; preview resolves
  through the same indirection as `confirmDialog`, never through the raw
  cursor.
- The spec is the contract: user-visible behavior comes from `spec.md`; if
  an implementation detail contradicts it, change the spec first (with the
  deviation recorded), not the code's promises.
- Preview/commit separation is absolute: preview paths never call
  `saveStoredConfig` and never append transcript entries. A test asserts
  the config file is byte-identical after arrow navigation.
- No new Go dependencies; `lipgloss.AdaptiveColor` is already in-tree via
  `go.mod`'s lipgloss v1.1.0.
- Visual-restraint rules from FR-15 apply: no gradients, no animation, no
  per-speaker hue coding — one `Normal` fg, authorship carried by bands.
- Honest-rendering rule: backgrounds never carry meaning alone; role labels
  and foreground roles survive degradation to plain text.
- Scope discipline: no new families, no palette-hue redesign, no key
  remapping, no new commands, no dialog reshaping, no terminal-background
  change watcher. M1 may ship alone; M2–M3 keep `Resolve`'s signature stable
  so M1's hook never needs rework.
