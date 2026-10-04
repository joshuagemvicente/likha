# Feature: Prompt line editor, built-in selection/copy, and image attachments

**Status:** mixed: readline editing and built-in selection/copy implemented
(local); real-terminal walkthrough pending; image attachments planned.

Defines v1-spec FR-17 (line editing and selection/copy) and planned FR-18
(image attachments), and amends FR-14 (retain mouse scrolling and scrollbar
control alongside built-in selection).

## Original context (2026-09-29)

Likha's prompt input was append-only text: `m.input []rune` with a
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

Readline editing and live drafts during active runs have since landed. The
native-selection proposal below is historical; the 2026-10-04 amendment
retains mouse capture and chooses built-in selection/copy instead.

## Historical resolved decisions (user-confirmed, 2026-09-29)

- **Image source:** clipboard paste only (Ctrl+V). Image file paths typed in
  the prompt are NOT auto-detected or auto-attached.
- **Selection UX:** native terminal selection — Likha releases mouse capture so
  the terminal handles highlight and copy itself. No in-app selection, no
  in-app copy bindings; Ctrl+C keeps its cancel/quit meaning.
  **Superseded by the user-approved 2026-10-04 amendment below.**
- **Vision scope:** attachments are allowed for every provider; when a
  provider/model rejects images, Likha surfaces a clear error suggesting a
  text-only retry. No capability matrix to maintain.
- **Image history:** image data stays in the session history and is resent
  with every turn (OpenCode-style). Token cost grows with each image; README
  documents this.
- **Keybinding scope:** the full readline set (word ops, line ops, kill ring,
  motion). No Ctrl+R history search in this feature.

## Amendment: built-in selection/copy (user-approved, 2026-10-04)

This amendment replaces the native-selection, release-mouse, and no-in-app-copy
decisions above. Likha retains mouse capture for direct drag selection,
mouse-wheel scrolling, and scrollbar click/drag. Ctrl+C retains cancel/quit.
The first slice covers only the main composer and main conversation, including
visible tool-output previews. Setup/API-key forms and inspection overlays do
not support selection in this slice.

Automated checks (`go test ./...`, `go vet ./...`, and targeted race tests) pass,
including terminal key/mouse decoding and selection geometry regressions. The
real-terminal walkthrough is pending. Image attachments remain planned.

## User-visible behavior

### Line editing (FR-17)

- The draft renders with a visible cursor at the insertion point; arrow keys,
  Home/End, Ctrl+A/B/E/F move it.
- Kill bindings: Ctrl+U (to start), Ctrl+K (to end), Ctrl+W / Ctrl+Backspace /
  Alt+Backspace (word back), Ctrl+Delete / Alt+D (word forward). A run of
  whitespace counts as one word boundary — deleting a word plus its preceding
  spaces removes them in one keystroke, as a native terminal does.
- Ctrl+Y yanks the most recent kill; the kill ring keeps the last eight kills.
- Direct left-button drag highlights composer text using built-in selection.
  Shift+Left/Right/Up/Down extends selection where the terminal sends those
  chords; ordinary arrows collapse the input selection.
- Typing or pasting replaces selected input; Backspace/Delete removes it.
  With no selection, bracketed paste inserts at the cursor as one unit.
- Bracketed-paste text (multi-line or large) is inserted at the cursor as one
  unit; newlines pasted into the single-line draft become spaces.
- Editing stays live while a turn streams; Enter queues the draft as a
  steering prompt (FR-21). Editing stays inert while an approval is pending.

### Highlighting and copying (FR-14 / FR-17 amendment)

- Direct left-button drag, without a modifier, highlights text in the main
  composer or main conversation, including visible tool-output previews.
  Selecting alone does not write the clipboard; Alt+C copies the selection
  to the system clipboard. Ctrl+C keeps cancel/quit and does not copy.
- Esc clears selection first without clearing the draft or cancelling a run.
  A later Esc performs its usual draft-clear/cancel action.
- Mouse-wheel scrolling and clickable/draggable scrollbar control remain
  available; this amendment does not trade them for native selection.
- Selecting output pauses transcript rendering and bottom-follow on the
  selected snapshot. The agent continues ingesting streamed events; clearing
  selection restores the fresh transcript view.
- Resize, session changes, and screen changes clear stale selection. Selection
  is ephemeral UI state, outside persistence and model context.
- Input copies use the original input buffer, preserving its selected text
  rather than copying the rendered composer. Output copies use visible
  rendered text with newlines between wrapped rows, not raw Markdown or hidden
  tool output. Neither payload includes renderer-added ANSI styling, a synthetic
  caret, composer borders, scrollbar, or layout padding. Source characters in
  the original input buffer are preserved, including literal control characters.
- Clipboard writes use macOS `pbcopy`, Linux/Wayland `wl-copy`, or Linux/X11
  `xclip`/`xsel`. Missing helpers and failed writes report visible errors.
  No new application dependencies or SSH/OSC52 clipboard support are in scope.
- Setup/API-key forms and inspection overlays are excluded from this slice.

### Image attachments (FR-18, planned)

- Ctrl+V pastes the clipboard: if it holds an image, Likha inserts a
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

- Historical 2026-09-29 amendment: FR-14 released mouse capture and kept key
  paging; FR-17 delegated highlighting/copying to the terminal.
- Superseding 2026-10-04 amendment: FR-14 retains mouse capture, continuous wheel
  scrolling, and scrollbar control; FR-17 uses built-in input/output selection
  with Alt+C copy and selection-first Esc. See v1-spec.md §4 and §7.
- FR-18 remains planned; adding its requirement did not implement attachments.

## Verification

- Automated validation passed: selection/editing transitions, copied
  payloads, clipboard helper dispatch/failures, Esc precedence, output snapshot
  stability during streaming, and stale-selection clearing.
- Real-terminal walkthrough pending: composer and conversation drag highlighting,
  Shift+arrow support where the terminal sends chords, typing/paste replacement,
  Backspace/Delete, Alt+C success/error, unchanged Ctrl+C, Esc before draft
  clearing/cancellation, retained wheel/scrollbar behavior, streamed-output
  snapshots, resize/session/screen changes, and excluded forms/overlays.
- Automated checks do not replace live terminal/system-clipboard verification.

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
