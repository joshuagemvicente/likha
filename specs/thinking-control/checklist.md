# Checklist: Thinking control (`/think`)

Observable outcomes, mirrors [spec.md](spec.md). Status words per
[specs/README.md](../README.md) rules.

- [ ] `/think off|low|medium|high` changes the effort used by every
      subsequent turn of the session, and does not affect a turn already
      in flight.
- [ ] A `/think`-set level reaches the provider as exactly the one wire
      field the capability matrix row records for that provider — observed
      on the wire, not inferred from code.
- [ ] `off` produces request bodies identical to Lisa before this feature.
- [ ] Bare `/think` reports the current level and accepted values and
      starts no model turn.
- [ ] Invalid input (`unknown`, extra words, punctuation variants) prints a
      visible error naming the accepted values, leaves the level
      unchanged, never reaches the model, and preserves the typed draft.
- [ ] While a turn streams at an effort level above `off`, the footer
      status row shows the level as plain text legible without color, in a
      muted theme-accent badge with no animation, flash, or new theme
      colors, and no Nerd Font glyph unless fonts are opted in.
- [ ] Every provider row of the capability matrix that claims support has a
      recorded live probe behind it; no row claims support without
      evidence (v1-spec §3).
- [ ] On an unprobed or unsupported provider, the matrix's single chosen
      stance produces the documented visible outcome (suppression note or
      turn error naming `/think off`) — never a silent no-op.
- [ ] Reasoning rendering is untouched by this feature: reasoning stays
      muted and is never sent back to the provider (FR-15).
- [ ] The level persists exactly as the spec's persistence decision
      decided — per session (restored visibly on resume) or in
      `config.json` (read on relaunch) — and a test exercises that path.
- [ ] Failure modes stay FR-11-conformant: a provider rejecting the effort
      field surfaces a visible error and leaves the TUI usable with the
      session intact.
- [ ] Status of this feature is only ever: planned, in progress,
      implemented (local), or verified (release) — this draft's
      `Status: draft` line resolves to one of those at implementation
      start.
