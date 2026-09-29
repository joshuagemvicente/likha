# Checklist: Slash commands in the prompt input

- [ ] Reserved commands are dispatched in the TUI and never sent to the model.
- [ ] Unknown slash commands show a visible error and preserve the draft.
- [ ] `/quit` is behaviorally identical to Ctrl+D, including pending-approval safety.
- [ ] `/help` lists the reserved commands in the conversation view.
- [ ] `/models` lists the provider's reported models and switches the live client for the next turn.
- [ ] `/sessions` lists the repository's saved sessions consistent with `--sessions`.
- [ ] Commands during an active run or pending approval are inert (except quit).
- [ ] `go test ./...` and the feature smoke pass before any of the above is described as verified.
