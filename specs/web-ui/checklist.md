# Checklist: Likha web UI (`likha --serve`)

Observable outcomes; unchecked until seen. Status mirrors
[spec.md](spec.md) acceptance criteria and the milestone gates in
[tasks.md](tasks.md). The TUI remains the primary surface: every M1 item
must hold before any web code is written.

## M1 — shared runtime, no user-visible change

- [ ] The TUI's streaming turn, queued steering prompt, approved edit,
      declined command, `/compact`, and resume behave exactly as on the
      base commit.
- [ ] `internal/runtime` owns the turn lifecycle; `internal/tui` no longer
      holds `history`, `queue`, `steer`, `events`, `abandon`, `cancel`,
      `runID`, or `working`.
- [ ] `internal/runtime` does not import `bubbletea`, `lipgloss`, `tui`,
      or `server`; `go test ./...` and `go test -race ./...` pass.

## M2 — `likha --serve` MVP

- [ ] `likha --serve` prints a loopback URL with a pairing token; opening it
      pairs the browser; the token is single-use; the cookie dies with the
      server.
- [ ] A non-loopback `--host` without a token is refused; with a token it
      starts with a visible warning.
- [ ] The session list shows the repository's sessions with names and
      update times; New session and resume work without a page reload.
- [ ] A prompt streams assistant text and tool activity into the transcript
      at the same granularity the TUI shows; the transcript follows at the
      bottom and anchors when scrolled up.
- [ ] An `edit_file` card shows the path and diff with `+N −M`; Approve is
      refused until the diff end has been seen; Decline executes nothing;
      an approved edit applies the exact displayed change.
- [ ] A `run_command` card shows the exact command, working directory, and
      warning; approving streams the real output and exit status.
- [ ] Cancel stops the run; a pending approval resolves as cancelled
      without executing.
- [ ] Closing the tab does not cancel a run; after a server restart the
      session resumes with history intact.
- [ ] `go test ./...` and `go test -race ./...` pass; the embedded assets
      all resolve (no 404s).

## M3 — daily-driver parity

- [ ] While a run streams, a second prompt shows a `Queued` row and is
      delivered inside the same run at the TUI's drain points; leftover
      queue runs as the next turn.
- [ ] `@` completes repository files and folders (gitignore-aware) and
      inlines them server-side; `/compact`, `/models`, `/help` work and
      are never sent to the model.
- [ ] The model dialog lists every configured provider's models, filters,
      and switches mid-session with the choice persisted.
- [ ] The `/providers` modal stores a key only after a passing check and
      never displays the stored key.
- [ ] Context usage, tokens, spend, session name, reasoning collapse,
      compaction marker, and errors render exactly when the TUI would show
      them — `ctx —` when the window is unknown, no fabricated numbers.
- [ ] Two tabs stay consistent; a second decision on a resolved approval is
      refused and the card shows the recorded outcome.
- [ ] At 360px width, reading, queueing, cancelling, approving, and
      declining all work; the active theme family is reflected where
      representable.

## M4 — attach mode and polish

- [ ] `likha --web` shows the live session in both the terminal and the
      browser; streaming appears in both; steering and approvals from
      either surface land in the other.
- [ ] `export.html` opens offline and reproduces the transcript.
- [ ] README documents the web UI and the security posture (loopback,
      pairing, unsandboxed approvals).

## Security (verified once, re-checked at each milestone)

- [ ] No token or cookie value appears in logs or event-stream URLs.
- [ ] Foreign-`Origin` mutations are rejected; no CORS headers are
      emitted.
- [ ] The web UI has no auto-approve path, no "always allow", and no
      cross-session approval memory.
