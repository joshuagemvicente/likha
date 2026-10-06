# Checklist: Install command (`/install`)

Observable outcomes, mirroring [spec.md](spec.md) and
[v1-spec.md](../v1-spec.md) wording where they overlap. Status words per
[specs/README.md](../README.md).

- [x] `/install <request>` starts one agent turn in the current conversation;
      `//install` sends a literal prompt.
- [x] The transcript shows only the typed command; saved history holds the full
      install prompt.
- [x] Empty request, no provider, plan mode, an active run, or a pending review
      or question shows a visible refusal and no request reaches the provider.
- [x] The prompt carries the Likha-detected environment section (startup file
      names only); read-only-tier probes run without a prompt before the plan.
- [ ] Questions arrive as one questionnaire with the recommended option first
      and marked; obvious choices are stated as assumptions, not asked.
- [x] No command outside the read-only tier and no edit runs before the plan
      checklist appears; such calls refuse without a review.
- [ ] The plan names every command and every file it will change, including
      files outside the repository.
- [ ] Every change goes through its normal approval; declining a step is
      respected.
- [ ] The turn ends with a verification step where possible and a summary of
      installed, changed, skipped, and user-to-do items.
- [ ] Skipping the whole questionnaire ends the turn with no change.
- [ ] No question asks for a password, API key, or token.
- [ ] Resume replays no question, plan step, or approval.
- [x] `/help` and autocomplete list `/install`.
- [ ] Live probe of `/install nvm posix` (macOS, zsh).
