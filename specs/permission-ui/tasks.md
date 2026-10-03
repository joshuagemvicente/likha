# Tasks: Approve/Decline buttons for permission reviews

**Status:** implemented (local). Ordered; each task carries its
verification. No checkmarks until work starts. Entry point:
`go test ./...` from the project root; `go test -race ./...` before calling
the feature done.

1. **Review focus state and gate helper.** Add `reviewFocus` to `ui`
   (`0` = Approve, `1` = Decline; initialized to Approve whenever a
   `TurnEvent` of kind `approval` sets `m.pending`, `tui.go:455-465`, and
   cleared alongside `pending` and `reviewSeen`). Add a `reviewReady()`
   helper that returns whether every `reviewSeen` page is true, and a
   shared `const reviewGateStatus = "Scroll to the end to approve"` in the
   `tui` package so `tui.go`, `composer.go`, and `status_line.go` cannot
   drift on the string.
   *Verify:* unit test — a fresh approval focuses Approve; `reviewReady()`
   follows `reviewSeen`; decision paths and done/error clear the focus.

2. **Decision bar rendering.** In `composerLines()`, replace the pending
   text override (`composer.go:52-58`) with the action block for every
   composer style: row 1 `> [ Approve ]    [ Decline ]` (focused button =
   `> ` marker plus `theme.Selected`; unfocused = muted, two-space indent),
   row 2 the hint `←/→ choose · Enter confirm · Esc cancel run`. When
   `!reviewReady()`, Approve renders muted regardless of focus and the hint
   reads `Scroll to the end to approve · ←/→ choose · Esc cancel run`.
   Width-safe at 40 columns (short hint `←/→ Enter Esc`); no caret and no
   draft rendering while pending.
   *Verify:* rendering tests at 80 and 40 columns for all four composer
   styles — both buttons present, exactly one focus marker, Approve muted
   pre-gate and normal post-gate; overflow invariant tests extended to the
   bar; focus difference asserted on the `default` theme family (no band)
   and a banded family.

3. **Key handling.** Replace the `y`/`Y` and `n`/`N` cases
   (`tui.go:719-747`) with: `left`/`right` (and `Tab`/`Shift+Tab`) move
   focus; `enter` confirms the focused button — Approve checks
   `reviewReady()` first and, when false, sets `reviewGateStatus` and sends
   no reply; Approve sends `Reply <- true` + `Executing approved <kind>`;
   Decline sends `Reply <- false` + `Rejected`. `Esc`/`Ctrl+C` keep the
   cancel-run path (`tui.go:753-768`). All other runes stay inert while
   `pending != nil`.
   *Verify:* tests for focus movement wrapping, Enter on each button,
   gated Approve (no reply sent, review still open, status text), Decline
   rejection recorded, Esc cancel unchanged, and `y`/`n` being inert.

4. **Status hints and wording.** Replace the `Y/N PgUp/PgDn ^C` fragments
   (`status_line.go:79-80,88`) with `←/→ · Enter · Esc` (narrow variants
   `←/→ Enter Esc`), and use `reviewGateStatus` wherever the old gate
   string appears (`tui.go:725`, `composer.go:53`, `status_line.go:79`).
   *Verify:* status tests for both layouts at review start, after a gated
   Approve, and on a ready review; no remaining `Y/N` string in the TUI
   package.

5. **Rewrite the tests that drive `y`/`n`.** Update every approval call
   site to the focus + Enter interaction: `tui_test.go:689,694,707,806,
   812,817,1718`; `status_line_test.go:153,165,159`; `composer_test.go:
   105,120`. Keep each test's original intent (approve-after-scroll,
   reject, page-gate refusal, cancellation) — only the input changes.
   *Verify:* `go test ./...` green with the rewritten call sites and no
   `"y"`/`"n"` approval messages left in `internal/tui` tests.

6. **Docs.** Apply FR-22 to `specs/v1-spec.md`; update README (review
   walkthroughs at lines ~97, ~110, ~114, ~174-176, ~200, ~283 that say
   "press Y/N" or "review keys"), `CHANGELOG.md`, and the `specs/README.md`
   index row. Update stale `Y/N` wording in
   `specs/slash-commands/providers.md:204` and
   `specs/update-notification/banner.md:207,247`. Cross-link this feature
   in `specs/steering-prompts/context.md` (the review gate blocks queueing).
   *Verify:* no `Y/N`/`y`/`n` approval wording remains in README or specs;
   index row matches status.

## Notes

- No approval semantics change: reply values, the per-proposal scope, the
  stale-edit check, and the command warnings are untouched.
- No new dependencies; no stored state; `reviewFocus` is transient.
- The decision bar reuses the composer's reserved rows, so `bodyHeight()`
  and every overflow budget stay as they are.
