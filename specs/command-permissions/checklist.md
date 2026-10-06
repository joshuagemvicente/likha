# Checklist: command permissions

Observable outcomes; unchecked until seen in the real TUI. Mirrors
[spec.md](spec.md) acceptance criteria. Status words per
[specs/README.md](../README.md) rules.

- [ ] Destructive or remote-code commands (`rm -rf /`, `rm -rf .`,
      `mkfs`, `dd of=/dev/…`, a fork bomb, `curl … | sh`) are refused
      without a review, and the refusal tells the model the user can run
      them; the refusal is visible in the transcript.
- [ ] Deletion, git history/remote, publish, deploy, privilege, process,
      secret-path, and outside-repository commands prompt every time with
      `Always asks: this command <reason>.` and only Approve/Decline.
- [ ] Chains, pipes, substitutions, redirects, and wrapped commands never
      run without a prompt, even when every part looks harmless.
- [ ] Ordinary commands (`npm install`, `mv`, `git commit`) prompt with
      Approve / Allow for session / Decline; after Allow for session the
      exact string runs unprompted until session switch, session delete, or
      relaunch.
- [ ] In an untrusted repository the first check (`npm test`, `go test`,
      `make test`) prompts with **Trust repo checks**; after trusting, checks
      run unprompted.
- [ ] Editing a trusted script, its `pre`/`post` hook, or the Makefile makes
      that check prompt again; a script whose text deletes or pushes always
      prompts.
- [ ] Deleting the repository's `command_trust` entry from `config.json`
      restores the trust prompt; nothing in the repository can grant trust.
- [ ] Read-only commands (`git status`, `git diff`, `ls`, `<tool> --version`)
      run unprompted in any repository.
- [ ] Every command that ran without a prompt still shows as a tool item
      with `· auto-approved` on its `⎿` line and the reason in its result.
- [ ] An auto-approved command stops after 10 minutes and is not reported
      as successful; prompted commands have no timeout.
- [ ] No command can read `LIKHA_API_KEY` or any `LIKHA_*_API_KEY`.
- [ ] Plan mode still refuses every command.
- [ ] Below 56 columns the bar shows `[Approve] [Session] [Decline]` or
      `[Approve] [Trust] [Decline]` without overflow; focus and the
      read-to-end gate work across all visible buttons; `Esc`/`Ctrl+C`
      cancel the run.
- [ ] `go test ./...`, `go vet ./...`, and
      `go test -race ./internal/agent ./internal/cmdpolicy ./internal/tui`
      pass from the project root.
- [ ] Status of this feature is only ever: planned, in progress,
      implemented (local), or verified (release), per the release gate in
      [v1-spec.md](../v1-spec.md).
