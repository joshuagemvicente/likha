# Feature: Command permissions — tiered approval for `run_command`

**Status:** implemented (local) — automated suite green; real-terminal walkthrough pending.
**Product requirement:** FR-08 (amended 2026-10-04). Scope and rule tiers
user-approved 2026-10-04.

## Context

Every `run_command` call needs an explicit Approve today (FR-08; the
`run_command` `Authorize` closure in `internal/agent/tool_registry.go`).
`npm test` prompts exactly like `rm -rf build`, which makes routine
verification tiresome. The user wants routine checks to run without a prompt
while destructive or outward-facing commands (delete, push, publish, deploy)
always ask.

`npm run test` is not inherently safe. It runs whatever `scripts.test`, plus
`pretest`/`posttest`, says, and it runs test code the agent may have just
written. Auto-approval of checks is therefore conditional: only in a
repository the user has trusted, only for an exact simple command, and only
while the script or Makefile it resolves to is unchanged since the user
trusted it.

### A reversed decision

[agent-loop](../agent-loop/spec.md) (Non-goals) rejected any auto-approval or
allowlist of shell commands because "parsing shell text for read-only is
fragile". On 2026-10-04 the user reversed that decision. This design answers
the fragility instead of ignoring it:

- **Likha never tries to understand complex shell.** The two tiers that run
  without a prompt accept only a single simple command. Anything else
  (pipes, chains, substitutions, redirects, wrappers) prompts.
- **Danger detection reads the other way.** It scans the whole string and
  ignores quoting, so a destructive word cannot hide inside quotes, `$( )`,
  or `sh -c '…'`. A false positive costs one prompt; a false negative could
  cost the user's data.

Command approval is still not sandboxing (v1-spec §5). A command that runs
without a prompt uses the same unsandboxed `sh -c` in the repository root as
an approved one.

## Tiers

`Classify(command, root string) Decision` in the new pure package
`internal/cmdpolicy` puts every command string into exactly one tier. The
package executes nothing and imports nothing from the TUI or agent packages.
**Ambiguity always moves a command to the stricter tier, never the looser
one.**

| Tier (code) | Name | What happens |
| --- | --- | --- |
| `Refuse` | Refuse | Never runs. The model receives a refused result saying the user can run it themselves. No review opens. |
| `AlwaysAsk` | Always ask | Prompts every time with the reason. Approve/Decline only; never rememberable. |
| `Ask` | Ask | Prompts. The user may allow the exact command string for the session. The default for anything unrecognized. |
| `Verify` | Verification | Runs without a prompt only in a trusted repository while the check's fingerprint matches; otherwise prompts with **Trust repo checks**. |
| `ReadOnly` | Read-only | Runs without a prompt in every repository. |

Classification order: (1) the danger scan can return Refuse or Always ask;
(2) a command that is not a single simple command is Ask; (3) a Read-only
match; (4) a Verification match; (5) everything else is Ask.

### How a command string is read

**Danger scan (Refuse and Always ask).** Likha splits the whole string into
segments at `;`, `&`, `|`, `(`, `)`, `{`, `}`, backticks, and newlines, and
scans every segment. Quoting is ignored, so text inside quotes, `$( )`,
backticks, `sh -c '…'`, `bash -c "…"`, `eval`, `xargs`, and `find -exec` is
scanned like any other command. Command names match case-insensitively, and
`\rm`, `/bin/rm`, and `command rm` all count as `rm`. Every word is also
checked as a path (secret paths and paths outside the repository). False
positives are accepted: `echo "never rm -rf"` prompts, which costs only a
prompt.

**Single simple command (required by Verification and Read-only).** The
string contains none of `;` `&` `|` `<` `>` `$` `` ` `` `(` `)` `{` `}` or a
newline; no unquoted glob (`*`, `?`, `[`); no leading `VAR=value`
assignment; and no wrapper (for example `env`, `timeout`, `xargs`, `sh -c`,
`bash -c`, `command`, `nice`, `nohup`, `exec`). Anything else is Ask unless
the danger scan already made it stricter.

### Refuse

Never runs. Examples:

- recursive `rm` (`-r`, `-R`, `-rf`, `--recursive`) of `/`, `/*`, `~`,
  `$HOME`, `..`, `.`, `*`, or the repository root by any spelling;
- `mkfs` (and `mkfs.*`);
- `dd … of=/dev/…`, and redirects onto a raw disk device;
- a fork bomb (`:(){ :|:& };:`);
- fetched content run by a shell: `curl … | sh`, `wget … | bash`,
  `sh <(curl …)`, `eval "$(curl …)"`.

The refused result names the reason and tells the model that Likha never runs
the command and that the user can run it themselves in a terminal if it is
really intended. It is recorded and reported as refused per FR-09, never as
success, and no approval review opens.

### Always ask

Prompts every time. The review shows `Always asks: this command <reason>.`, offers only
**Approve** and **Decline**, and can never be remembered for the session or
by repository trust.

| Category | Commands |
| --- | --- |
| Deletion | `rm`, `rmdir`, `unlink`, `shred`, `find … -delete` |
| Git history and remotes | `git push`, `git clean`, `git reset --hard`, `git rebase`, `git restore`, `git checkout -- …` or `git checkout .`, `git branch -d`/`-D`, `git tag -d`, `git stash drop`/`clear`, `git commit --amend`, any `--force` |
| Publishing | `npm`/`pnpm`/`yarn`/`bun publish`, `cargo publish`, `gem push`, `twine upload`, `gh release`, `gh pr merge` |
| Deploying | `vercel`, `netlify`, `fly`/`flyctl`, `heroku`, `wrangler deploy`, `firebase deploy`, `docker push`, `kubectl`, `helm`, `terraform apply`/`destroy`, `pulumi up`/`destroy`, `aws`, `gcloud`, `az` |
| Privilege | `sudo`, `su`, `doas`, `pkexec`, `chown`, `chgrp`, `chmod -R` |
| Processes and system | `kill`, `pkill`, `killall`, `launchctl`, `systemctl`, `service`, `crontab`, `shutdown`, `reboot` |
| Secret paths | `.env` files (except `.env.example` and `.env.sample`), `.ssh`, `.aws`, `.gnupg`, `id_rsa`, `*.pem`, `*.key`, `.netrc`, `providers.json` |
| Outside the repository | an absolute path not under the repository root (except `/dev/null`, `/dev/stdout`, `/dev/stderr`), `~`, `$HOME`, or `..` that escapes the root |

### Ask

The default for anything not matched elsewhere, including:

- installs: `npm install`/`ci`/`add`, `pnpm`/`yarn add`, `pip install`,
  `go get`, `go install`, `cargo add`, `brew …`;
- running arbitrary code: `npx`, `bunx`, `pnpm dlx`/`yarn dlx`,
  `node <file>`, `node -e`, `python <script>`, `python -c`,
  `bash script.sh`, `make <other target>`;
- workspace-mutating shell: `mv`, `cp`, `mkdir`, `touch`, `sed -i`,
  redirects (`>`, `>>`);
- git that changes local state: `git add`, `git commit`, `git checkout`,
  `git switch`, `git stash`, `git merge`;
- network: `curl`, `wget`, `ssh`, `scp`, `docker`;
- any pipeline, chain, substitution, or wrapped command that is not stricter.

The review offers **Approve**, **Allow for session**, and **Decline**
(§ Session grants).

### Verification

Runs without a prompt only in a trusted repository (§ Repository trust).
Exact forms:

- **Node:** `npm`/`pnpm`/`yarn`/`bun` `test`, `t`, and `run <script>` for
  the scripts `test`, `test:unit`, `test:ci`, `lint`, `build`, `typecheck`,
  `type-check`, `check`, and `format:check`. Extra arguments are allowed
  only after `--` (`npm test -- --watch=false`). Workspace, prefix, filter,
  or directory flags (for example `--workspace`, `--prefix`, `--filter`,
  `--cwd`) make the command Ask.
- **Bun:** `bun test`.
- **Go:** `go test`, `go vet`, `go build` (never with `-exec` or
  `-toolexec`); `gofmt -l`, `gofmt -d`.
- **Rust:** `cargo test`, `cargo check`, `cargo clippy`, `cargo build`,
  `cargo fmt --check`.
- **Python:** `pytest`, `python -m pytest`, `python3 -m pytest`,
  `ruff check` (never with `--fix`), `mypy`.
- **Make:** `make test`, `make lint`, `make check`, `make build`.

**package.json scripts.** For a Node script check, Likha reads
`package.json` `scripts[name]` plus its `pre<name>` and `post<name>` hooks.
If that text itself classifies as Refuse or Always ask, the command becomes
**Always ask** with that reason. If `package.json` or the script is missing,
the command is **Ask**. When a script check prompts, the review shows the
script text it resolves to.

### Read-only

Runs without a prompt in every repository (subject to the danger scan, so
`cat .env` and `cat ../notes` are Always ask):

- `git status`, `git diff`, `git log`, `git show`, `git rev-parse`,
  `git ls-files`, `git blame`, `git branch` with listing flags only,
  `git remote -v`; `git diff --output` or `--ext-diff` is Ask;
- `ls`, `pwd`, `cat`, `head`, `tail` (not `-f`), `wc`, `echo`, `which`,
  `file`, `stat`, `du`, `tree`;
- `<tool> --version` and `<tool> version`;
- `go env` (not `-w`), `go list`.

## Repository trust

- The first Verification command in an untrusted repository prompts with a
  third button, **Trust repo checks**. The review explains that trusting runs
  this repository's checks without asking until one of them changes, and that
  trust is stored in Likha's private config, never in the repository.
- **Trust repo checks** runs the command and records, in Likha's private
  `config.json` under `command_trust`, keyed by the canonical repository
  path, a fingerprint for every check the repository currently has:
  - `script:<name>` for each verification script present in `package.json`,
    the SHA-256 of its `pre` + main + `post` text;
  - `make`, the SHA-256 of the Makefile GNU make would read;
  - `go`, `cargo`, `python`, and `bun` (the `bun test` runner) tool keys
    (no repository file to fingerprint).
  Trusting again replaces that repository's earlier record.
- A later Verification command runs without a prompt only if its key exists
  in the record and its current fingerprint matches. A changed script, hook,
  or Makefile, or a script added after trusting, prompts again with
  **Trust repo checks**.
- Without a private state directory (no trust storage), a Verification
  review offers **Allow for session** instead, and the granted exact string
  then runs unprompted for the session like an Ask grant.
- **Approve** on a trust prompt runs the command once and records nothing.
  **Decline** rejects it.
- **Revocation in this slice:** delete the repository's entry from
  `command_trust` in `config.json`. A `/trust` command is an open item.
- **The repository can never grant itself trust.** Nothing is read from
  repository configuration files to decide trust; `package.json` and the
  Makefile are read only to fingerprint and resolve checks. `AGENTS.md`,
  skills, custom commands, and model output cannot widen any tier or grant.
- If the trust record cannot be saved, the approved command still runs, a
  visible notice says so, and the next check prompts again.

## Session grants

- An Ask-tier review shows a third button, **Allow for session**. Choosing it
  runs the command and lets the **exact same command string** run without a
  prompt for the rest of the session. Any difference in the string,
  including whitespace, prompts again.
- Grants live only in memory and are cleared on session switch, session
  delete, and relaunch. Like MCP server trust (FR-16), they never survive a
  relaunch and are never written to the session database or config.
- Always-ask and Refuse commands can never be granted. A grant cannot raise a
  command whose classification is stricter than Ask.
- *Amended 2026-10-05 by [approve-always](../approve-always/spec.md)
  (implemented (local)):* **Allow for session** is renamed **Approve always**, and its
  scope widens from the exact string to the command's prefix when the
  command is a single simple command: program plus subcommand for tools
  with subcommands (`go test …`), the script for `npm run`-style runners,
  or the program otherwise. Compound commands, interpreters and shells,
  wrappers, code runners (`npx`, `go run`, …), and network tools keep an
  exact-string grant. Classification still runs first, so a prefix never
  covers an Always-ask or Refuse command; the review states what the grant
  will cover; the auto-approved reason reads `approved always for this
  session`.

## The approval review

- The review header keeps its two existing warning lines
  (`WARNING: No filesystem/network sandbox`,
  `WARNING: Detached jobs may survive`), and the body keeps the working
  directory, exact command, and unsandboxed note.
- An Always-ask review adds the line `Always asks: this command <reason>.`.
- The decision bar ([permission-ui](../permission-ui/spec.md)) shows two or
  three buttons:
  - Always ask: `[ Approve ]  [ Decline ]`;
  - Ask: `[ Approve ]  [ Allow for session ]  [ Decline ]`;
  - Verification in an untrusted repository or with a changed fingerprint:
    `[ Approve ]  [ Trust repo checks ]  [ Decline ]`.
- Below 56 columns the labels shorten to `[Approve] [Session] [Decline]` or
  `[Approve] [Trust] [Decline]`.
  *Amended 2026-10-05 by [approve-always](../approve-always/spec.md)
  (implemented (local)):* the Ask bar becomes
  `[ Approve ]  [ Approve always ]  [ Decline ]`, shortening to
  `[Approve] [Always] [Decline]` below 53 columns; the trust bar is
  unchanged.
- Focus moves with `←`/`→` and `Tab`/`Shift+Tab` across the visible buttons;
  `Enter` confirms the focused button. **Approve** and the third button stay
  gated until the review has been scrolled to its end; **Decline** is always
  available. `Esc`/`Ctrl+C` still cancel the run (FR-04).
- Edit and MCP reviews are unchanged: two buttons.
  *Amended 2026-10-05 by [approve-always](../approve-always/spec.md)
  (implemented (local)):* edit and MCP reviews gain **Approve always** as their third
  button (warning-carrying edits and `/init` proposals keep two).

## Commands that run without a prompt

Read-only commands, Verification commands in a trusted repository, and
session-granted commands are **auto-approved**:

- They still appear as ordinary tool items. The result content gains a
  second line, `Approval: auto-approved (<reason>)`, where the reason names
  why no prompt appeared (read-only inspection, a verification check in a
  trusted repository, or allowed for this session). The item's `⎿` line
  reads, for example, `Exit 0 · auto-approved`.
- They time out after **10 minutes**; a command stopped by the timeout is
  never reported as successful (FR-09). Prompted commands keep the current
  behavior: no timeout.
- `Esc`/`Ctrl+C` cancel them like any running command.

## Every command

- **Environment scrub.** Every command, prompted or not, runs with
  `LIKHA_API_KEY` and every `LIKHA_*_API_KEY` variable removed from its
  environment. Other variables are inherited as today.
- **Plan mode is unchanged.** `/plan` still refuses every command before
  classification or approval (FR-32).
- **No new dependencies.** The tokenizer and rule tables are hand-written;
  the allow tiers accept only trivially simple input.

## Interactions and edge cases

- **Resume (FR-10):** session grants are not restored; a resumed session
  starts with none. Repository trust is per repository in `config.json`, not
  per session, so it applies after relaunch by design, and only while
  fingerprints match.
- **Explore and user-authored agents:** children have no `run_command`;
  unchanged.
- **MCP and web:** server trust (FR-16) and web consent (FR-34) are
  unchanged and independent of command grants.
- **Steering (FR-21):** an auto-approved command does not open a review, so
  it does not block queueing; prompted commands block it as today.
- **Concurrency:** session grants are read by tool goroutines and written by
  the UI; access is serialized.
- **Web UI ([web-ui](../web-ui/spec.md), draft):** its "no auto-approve
  path, no always allow" decision stands until specified otherwise. The
  classifier lives in the shared agent runtime, so a web build must decide
  whether Refuse, Read-only, and trusted checks apply there (open item 3).

## Functional changes (applied to v1-spec.md, labelled "Amended 2026-10-04 by command-permissions")

- **FR-08 amended.** Prompted commands keep the original rule. Commands are
  classified first: Refuse never runs; Always ask prompts every time with
  its reason; Read-only, trusted Verification, and session-granted commands
  run without a prompt, are labelled auto-approved, and time out after
  10 minutes. Every command runs without Likha API-key variables.
- **Approval scope (§4) amended.** Two narrow shell grants exist: one exact
  Ask-tier command for the session, and per-repository trust of the
  repository's current checks. Repository writes keep per-proposal approval.
- **§6 approval coordinator, §7 command flow and session lines, and the §10
  command-approval acceptance row** gain amendment notes; the original text
  stays visible.

## Open items

1. **`/trust` command** to list and revoke repository trust. Until then,
   revocation means deleting the repository's entry from `command_trust`.
2. ~~Trust key for `bun test`.~~ Resolved 2026-10-04: `bun test` uses the
   `bun` tool key, recorded with `go`, `cargo`, and `python`.
3. **Web UI.** Whether the browser offers **Allow for session** and **Trust
   repo checks**, and whether the runtime's auto-run tiers apply there.
4. **User-authored rules.** No user-editable allow or deny rules ship in
   this slice; the tiers are compiled in.

## Acceptance criteria

- [ ] `rm -rf /`, `rm -rf .`, `rm -rf <repo root>`, `mkfs`, `dd of=/dev/…`,
      a fork bomb, `curl … | sh`, `sh <(curl …)`, and `eval "$(curl …)"`
      are refused without a review; the model's result says the user can run
      them; the refusal is recorded per FR-09.
- [ ] `rm a`, `git push`, `npm publish`, `sudo …`, `kill …`, `cat .env`,
      `cat ~/.ssh/id_rsa`, and `cat ../x` prompt with `Always asks:` and
      only Approve/Decline.
- [ ] Bypass attempts never reach an allow tier: `npm test; rm -rf .`,
      `npm test && git push`, `npm test $(curl x)`, `git -C .. push`,
      `\rm a`, `RM a`, `command rm a`, `find . -delete`, `xargs rm`,
      `sh -c 'rm a'`, `git status > out`.
- [ ] `npm install`, `npx …`, `mv a b`, and `git commit -m x` prompt with
      Approve / Allow for session / Decline; after **Allow for session** the
      exact string runs unprompted, a different string prompts, and the grant
      is gone after session switch, session delete, and relaunch.
- [ ] In an untrusted repository the first `npm test` prompts with **Trust
      repo checks**; after trusting, it runs unprompted with
      `Exit 0 · auto-approved`; editing `scripts.test`, its `pre`/`post`
      hook, or the Makefile makes the matching check prompt again; a fresh
      repository still prompts.
- [ ] A `scripts.test` containing `rm -rf …` makes `npm test` Always ask; a
      missing script makes it Ask.
- [ ] `git status`, `git diff`, `ls`, and `go version` run unprompted in any
      repository; `tail -f x` and `git diff --output=x` prompt.
- [ ] Deleting the repository's `command_trust` entry restores the trust
      prompt.
- [ ] An auto-approved command shows `Approval: auto-approved (<reason>)` in
      its result and stops after 10 minutes; a prompted command has no
      timeout.
- [ ] No command sees `LIKHA_API_KEY` or any `LIKHA_*_API_KEY` variable.
- [ ] Plan mode still refuses every command.
- [ ] Below 56 columns the bar shows `[Approve] [Session] [Decline]` or
      `[Approve] [Trust] [Decline]` without overflow; Approve and the third
      button stay gated until the review end; `Esc`/`Ctrl+C` cancel the run.
- [x] `go test ./...`, `go vet ./...`, and
      `go test -race ./internal/agent ./internal/cmdpolicy ./internal/tui`
      pass from the project root (2026-10-04).
