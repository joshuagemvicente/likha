# Checklist: Custom commands (user-defined skills)

- [ ] `/skills` lists the available user-defined commands with descriptions and a clear empty state.
- [ ] A user command invoked with arguments expands its template and the expansion is sent as an ordinary user prompt.
- [ ] Actions proposed after a custom command still require the unchanged edit/command approval.
- [ ] A user command file named after a reserved command is refused with a visible error and never dispatched.
- [ ] Unknown slash commands keep the existing error behavior (visible, not sent to the model, draft restored).
- [ ] A command file with invalid frontmatter or no body yields a clear error naming the file and is never dispatched.
- [ ] User commands are inert during an active run or pending approval, like the reserved commands.
- [ ] The `//` escape is unaffected.
- [ ] A loaded command file cannot execute shell code, alter tools, or change stored configuration; the only observable effect of one is prompt text.
- [ ] `go test ./...` and the feature smoke pass before any of the above is described as verified.
