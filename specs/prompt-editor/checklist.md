# Checklist — Prompt line editor, native selection, image attachments

Observable outcomes. Mirrors v1-spec FR-14 (amended), FR-17, FR-18.

## Line editing (FR-17)

- [ ] The draft shows a cursor at the insertion point; arrows, Home/End, and
  Ctrl+A/B/E/F move it.
- [ ] Ctrl+U kills to the start; Ctrl+K kills to the end; both are yankable.
- [ ] Ctrl+W, Ctrl+Backspace, and Alt+Backspace delete the previous word with
  its whitespace run in one keystroke.
- [ ] Ctrl+Delete and Alt+D delete the next word with its whitespace run in one
  keystroke.
- [ ] Alt+B/F move by word.
- [ ] Ctrl+Y restores the most recent kill; repeated kills are retrievable
  through the ring.
- [ ] Highlight-drag selects in the terminal's own UI and the terminal's
  copy/paste commands work while Lisa runs.
- [ ] Pasted text (bracketed paste) inserts at the cursor with newlines as
  spaces.
- [ ] Editing stays inert while a turn streams and while an approval is
  pending.

## Mouse capture (FR-14 amendment)

- [ ] Lisa does not capture the mouse; the terminal's native selection works.
- [ ] PgUp/PgDn, Ctrl+P/N, Home, End page the session; mouse wheel does not.
- [ ] README and `--help` state the wheel tradeoff.

## Image attachments (FR-18)

- [ ] Ctrl+V with an image on the clipboard inserts `[Image #N]` and a visible
  confirmation.
- [ ] The token deletes as one unit and drops its image.
- [ ] GIF and unknown formats are rejected with clear messages.
- [ ] Images over 5 MB are rejected before entering the draft.
- [ ] An image turn reaches the model as text + image content parts on both
  the chat-completions and Codex/Responses builders; text-only turns are
  unchanged on the wire.
- [ ] A provider rejecting images produces a clear text-only-retry error and
  the session remains usable.
- [ ] Attachments survive relaunch via private state directory storage; a
  missing image on resume is dropped with a visible note; image bytes never
  enter SQLite.
- [ ] A real provider turn with a pasted image streams an answer that reflects
  the image (live-verified with the user's key, like the provider probes).
