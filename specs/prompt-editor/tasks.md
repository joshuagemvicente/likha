# Tasks — Prompt line editor, native selection, image attachments

Ordered implementation tasks. Each ends with a verification step; no
checkmarks until the step is done. Automated entry point: `go test ./...`.

## M1 — Line editor (FR-17)

1. **Key-encoding probe.** Write a throwaway probe printing every `tea.KeyMsg`
   payload (type, string, runes) for ←/→/Home/End, Ctrl+A/B/E/F/K/U/Y/W,
   Ctrl+Backspace, Ctrl+Delete, Alt+Backspace, Alt+B/D/F on Ghostty. Record the
   encoding table in this file before implementing. Verify: every binding in
   FR-17 has a stable `msg.String()` on Ghostty; document Windows Terminal
   variants as pending (out of v1 targets).
2. **Editor state.** Add the editor struct (runes, cursor, kill ring, last
   kill length) with pure operations: insert, delete back/forward, kill
   word back/forward, kill to start/end, yank, move by word/char/line.
   Whitespace runs collapse on word kills. Verify: unit tests for every
   operation, including multi-space boundaries, unicode width runes, token
   atoms (empty list in M1), and cursor clamping.
3. **Prompt row rendering.** Replace the `Draft:` body line with a footer
   prompt row rendering the draft with a simulated cursor (reverse-video
   cell). Verify: `View()` tests for cursor position rendering and unchanged
   layout at minimum sizes.
4. **Wire bindings into Update.** Route keys through the editor; keep
   Enter-submit, `/` command dispatch, and inert-while-working/pending rules
   unchanged. Verify: Update key tests mirroring existing tui_test.go
   patterns, including typing while streaming and approval pending.
5. **Bracketed paste.** Insert pasted text at the cursor as one unit; convert
   newlines to spaces. Verify: paste-simulation tests plus one manual Ghostty
   paste of a multi-line snippet.

## M2 — Native selection (FR-14 amendment)

6. **Release mouse capture.** Remove `tea.WithMouseCellMotion` from
   `run.go`; remove the `tea.MouseMsg` wheel-paging branch in `tui.go`.
   Verify: `go test ./...` stays green; manual Ghostty walkthrough confirms
   drag-selection and Cmd+C/Cmd+V work, and PgUp/PgDn/Home/End/Ctrl+P/N page.
7. **Docs.** Update README (requirements/TUI section), `--help` text, and
   CHANGELOG: wheel paging removed deliberately, native selection enabled.
   Verify: docs state the tradeoff in the documented places.

## M3 — Image attachments (FR-18)

8. **Clipboard bridge.** Add per-platform clipboard helpers (macOS `osascript`
   image + `pbpaste` text; Windows PowerShell; Linux `xclip`/`wl-paste`) as
   async commands; detect image bytes by magic bytes (PNG/JPEG/WebP), text
   fallback, missing-tool error. Verify: unit tests with an injectable command
   runner for each platform path and each error path (tool missing, unknown
   bytes).
9. **Format and size guard.** Accept PNG/JPEG/WebP magic bytes only; reject
   GIF with "GIF is not supported; use PNG, JPEG, or WebP"; reject other
   containers and any image over 5 MB with a clear error before it enters the
   draft. Verify: unit tests per format, GIF rejection, oversize rejection.
10. **Editor integration.** Ctrl+V inserts a removable `[Image #N]` token as
    an atom (editor operations never split it); deleting the token drops the
    attachment. Attachment + `/`-command is rejected before dispatch.
    Verify: editor unit tests for token atoms; Update tests for paste flow.
11. **Wire format (both builders).** `requestMessage.Content` becomes an
    interface: string when text-only; text + `image_url` content parts
    otherwise. Apply the same part shape to `streamCodex` (Responses wire).
    Verify: httptest payload assertions for a text+image turn on both paths;
    text-only turns marshal byte-identical to today.
12. **Error mapping.** Map provider 400-rejects on image content to a clear
    "provider/model doesn't accept images — retry as text-only" error; the
    session stays usable. Verify: simulated rejection test.
13. **Persistence.** Write attachment blobs to the private state directory
    (`images/<sessionID>/<seq>.<ext>`, private modes), extend snapshots with
    attachment metadata, re-read on resume, drop missing files with a visible
    note; image bytes stay out of SQLite. Verify: save/round-trip/resume test
    including the missing-file path.
14. **Cost documentation.** README documents resend-every-turn token cost
    with the 5 MB cap math. Verify: doc review.
15. **Manual feature walkthrough.** Real provider turn with a pasted image:
    attach, inspect the token, submit, read the streamed answer, exit,
    relaunch, resume, confirm history intact. Record the outcome in
    checklist.md.
