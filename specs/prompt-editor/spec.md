# Feature: Prompt line editor, native selection, and image attachments

**Status:** planned

Implements v1-spec FR-17 (line editing) and FR-18 (image attachments), and
amends FR-14 (mouse no longer captured; keys page).

## Context

Lisa's prompt input is currently append-only text: `m.input []rune` with a
backspace that trims the tail, no cursor, and no paste semantics beyond
key-by-key runes. Users of agent TUIs (OpenCode, OMP, Claude Code) expect
readline-style editing, terminal-native text selection for copy/paste, and
image pasting. Two constraints shape the design:

- The TUI captures the mouse (`tea.WithMouseCellMotion`) for wheel paging, which
  blocks the terminal's own text selection. Releasing capture restores native
  highlight/copy at the cost of mouse-wheel paging (keys keep paging).
- Model requests carry text-only content today; images require multimodal
  content parts, and the ChatGPT/Codex Responses surface (`streamCodex`) is a
  separate payload builder that needs the same treatment.

## Resolved decisions (user-confirmed, 2026-09-29)

- **Image source:** clipboard paste only (Ctrl+V). Image file paths typed in
  the prompt are NOT auto-detected or auto-attached.
- **Selection UX:** native terminal selection — Lisa releases mouse capture so
  the terminal handles highlight and copy itself. No in-app selection, no
  in-app copy bindings; Ctrl+C keeps its cancel/quit meaning.
- **Vision scope:** attachments are allowed for every provider; when a
  provider/model rejects images, Lisa surfaces a clear error suggesting a
  text-only retry. No capability matrix to maintain.
- **Image history:** image data stays in the session history and is resent
  with every turn (OpenCode-style). Token cost grows with each image; README
  documents this.
- **Keybinding scope:** the full readline set (word ops, line ops, kill ring,
  motion). No Ctrl+R history search in this feature.

## User-visible behavior

### Line editing (FR-17)

- The draft renders with a visible cursor at the insertion point; arrow keys,
  Home/End, Ctrl+A/B/E/F move it.
- Kill bindings: Ctrl+U (to start), Ctrl+K (to end), Ctrl+W / Ctrl+Backspace /
  Alt+Backspace (word back), Ctrl+Delete / Alt+D (word forward). A run of
  whitespace counts as one word boundary — deleting a word plus its preceding
  spaces removes them in one keystroke, as a native terminal does.
- Ctrl+Y yanks the most recent kill; the kill ring keeps the last eight kills.
- Highlighting text for copy/paste is the terminal's job: Lisa releases mouse
  capture, so dragging selects, and the terminal's copy/paste commands work
  (Cmd+C/Cmd+V on macOS, Ctrl+Shift+C/V on Linux).
- Bracketed-paste text (multi-line or large) is inserted at the cursor as one
  unit; newlines pasted into the single-line draft become spaces.
- Editing stays inert while a turn streams or an approval is pending.

### Image attachments (FR-18)

- Ctrl+V pastes the clipboard: if it holds an image, Lisa inserts a
  `[Image #N]` token into the draft and shows a visible confirmation; the
  token is a single editing unit — backspace or any word-kill removes the
  token and its image together.
- The format guard accepts only PNG, JPEG, and WebP by magic bytes (never by
  file extension or clipboard metadata). GIF is rejected with
  "GIF is not supported; use PNG, JPEG, or WebP" even when the provider would
  accept it.
- Each image is capped at 5 MB; larger data is rejected with a clear error
  before it enters the draft.
- Enter submits the prompt with text and image content parts; the model
  receives `[Image #N]` positions as `{type:"text"}` plus
  `{type:"image_url"}` parts (data URLs). A prompt beginning with `/` cannot
  carry images: attachment plus slash-command is rejected before dispatch.
- A provider or model that rejects images yields a clear error suggesting a
  text-only retry; the session stays usable.
- Image data remains in session history and is resent with each turn.
  Attachments survive relaunch: blobs live in the private state directory
  beside the session database; a missing stored image on resume is dropped
  with a visible note. Image bytes never enter the session database itself.

## Functional changes (applied in v1-spec.md)

- FR-14 amended: the mouse wheel no longer pages; keys do.
- FR-17, FR-18 added (see v1-spec.md §4).

## Open items

1. Probe Ctrl+Delete / Alt+D / Alt+Backspace key encoding on Ghostty (primary)
   and Windows Terminal (documented target backlog) before fixing handlers.
2. Confirm the terminal cursor rendering approach in the alternate screen
   (simulated reverse-video cell vs. real cursor) during M1.
3. Animated WebP/animated PNG ride inside accepted containers unless an
   explicit chunk guard is requested; default is to accept them.
4. Keyboard shortcut for removing an attachment without deleting its token
   (default: deleting the token removes the image; a dedicated detach key is
   not planned).
