# Tasks: Slash commands in the prompt input

**Status:** planned — not started. Ordered slices; the entry point is
`go test ./...` from the project root.

1. **Command dispatcher.** Intercept leading-`/` input in the TUI before turn
   dispatch; a reserved-words table maps commands to handlers; unknown
   commands produce a visible error and keep the draft. Verify: unit tests
   cover dispatch, unknown-command error, and no model round-trip.
2. **`/quit` and `/help`.** `/quit` reuses the Ctrl+D path exactly (including
   abandon-channel handling). `/help` renders the command list as a
   conversation entry. Verify: behavior parity test for `/quit` vs Ctrl+D.
3. **`/models`.** Fetch the provider's model list (reuse `model.ListModels`),
   render numbered choices in the conversation view, switch the live client on
   selection. Verify: switch takes effect on the next turn; stored config
   untouched.
4. **`/sessions`.** Render `--sessions`-equivalent listing; in-place resume
   replaces the active history/store snapshot (resolve open question 1 first).
5. **Docs.** README command reference, CHANGELOG, FR-03 amendment.
