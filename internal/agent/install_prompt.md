# /install: interview, plan, then install

Install what the user asks for under "Install request" below, on this machine.
Likha has already detected the basics; they are under "Environment" below.
Work in this order and do not skip ahead: detect, ask the gaps, post the plan,
execute, verify, summarize. Every change is an approved command or an approved
edit; this turn grants no additional permissions.

## 1. Detect first, without asking

Trust the "Environment" section for the OS and architecture, the user's
shell, which shell startup files exist, and which package managers are on
PATH; do not probe for those again. Startup files are listed by name only.

Learn the rest before asking anything, mainly whether the requested tool or
its prerequisites are already installed. Use the repository read tools and
single simple read-only commands such as `which <tool>`, `uname -r`, or
`<tool> --version` where that runs without a prompt. Until the plan is posted,
any other command is refused, so keep probes in that tier. If a fact still
matters and cannot be detected, use a sensible default and state it as an
assumption, ask it in the questionnaire, or make checking it the first step of
the plan.

## 2. Ask only the gaps

Ask only the choices that detection and the request leave open and that would
change what gets installed or modified: install method, version, location,
which files to change, optional add-ons.

- Ask them all in one `ask_user` call using the `questions` form (one to four
  questions). In each question put the recommended option first, mark it
  `recommended`, and give it a one-line reason in its `description`.
- If nothing is ambiguous, ask nothing. State your assumptions in the plan.
- Ask a follow-up questionnaire only when an answer opens a new choice.
- If the user skips a question, use the recommended option and say so in the
  plan. If the user skips the whole questionnaire, stop without making any
  change and tell them to run `/install` again with the details in the request.
- Never ask for passwords, API keys, or tokens. List any step that needs one
  for the user to do themselves.

## 3. Post the plan before any change

Call `plan_update` with the install plan before running anything that changes
the machine: every command you will run, every file you will change (including
files outside the repository, such as `~/.zshrc`), and the assumptions you made.
Until a plan is posted in this turn, commands outside the read-only tier and
every edit are refused without review.

## 4. Execute with approvals

Work through the plan one step at a time and keep the checklist current with
`plan_update`. Every command and edit goes through its normal approval. If the
user declines a step, adapt the plan or stop; never retry the same change
another way. Edit tools are confined to the repository, so files outside it
(for example a shell startup file) change only through approved commands.

## 5. Verify

Check the result where a check exists, for example `<tool> --version`. Tools
defined as shell functions, such as nvm, load only in a new shell: verify them
in a fresh login shell, for example `zsh -lc 'nvm --version'`.

## 6. Summarize

End with a short summary: what was installed, what changed (commands run and
files modified), what was skipped or declined, and anything the user must do
themselves, such as opening a new terminal.
