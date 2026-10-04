package tui

// editState carries the composer's cursor, selection, and kill ring for m.input
// (specs/tool-rendering-terminal-keys M2, FR-17). The buffer itself stays
// m.input []rune so existing draft plumbing keeps working; every mutation
// goes through these pure operations.
type editState struct {
	caret           int      // rune index of the insertion point, clamped to len(input)
	selectionAnchor int      // fixed rune index while the caret extends a selection
	selecting       bool     // also true for a zero-width selection during a drag
	ring            [][]rune // kill ring, most recent kill first, capped at killRingCap
	yankIdx         int      // ring index of the entry the last yank inserted
	yankLen         int      // runes the last yank inserted (for replace-on-repeat)
	lastWasYank     bool     // true between consecutive yanks (repeat cycles the ring)
}

const killRingCap = 8

// isWordSpace reports whether r acts as a word separator in the composer:
// horizontal whitespace and the newline a Return-modifier inserts.
func isWordSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

// clampCaret re-pins the caret after a wholesale buffer replacement.
func (s *editState) clampCaret(input []rune) {
	s.caret = min(len(input), max(0, s.caret))
}

// selectionRange returns a half-open rune range without changing editor state.
// Both ends are clamped in case a wholesale buffer replacement left them stale.
func (s *editState) selectionRange(input []rune) (start, end int, ok bool) {
	if !s.selecting {
		return 0, 0, false
	}
	anchor := min(len(input), max(0, s.selectionAnchor))
	caret := min(len(input), max(0, s.caret))
	start, end = min(anchor, caret), max(anchor, caret)
	return start, end, start < end
}

// clearSelection leaves the caret and kill/yank state untouched.
func (s *editState) clearSelection() {
	s.selectionAnchor = 0
	s.selecting = false
}

// setSelection keeps zero-width selections active so a drag can cross its
// anchor without losing the original insertion point.
func (s *editState) setSelection(input []rune, anchor, caret int) {
	s.selectionAnchor = min(len(input), max(0, anchor))
	s.caret = min(len(input), max(0, caret))
	s.selecting = true
	s.lastWasYank = false
}

// extendSelection fixes the old caret as the anchor on the first extension.
func (s *editState) extendSelection(input []rune, caret int) {
	anchor := s.selectionAnchor
	if !s.selecting {
		anchor = s.caret
	}
	s.setSelection(input, anchor, caret)
}

func (s *editState) selectedText(input []rune) string {
	start, end, ok := s.selectionRange(input)
	if !ok {
		return ""
	}
	return string(input[start:end])
}

// endCaret moves the caret to the end after a wholesale buffer replacement.
func (s *editState) endCaret(input []rune) {
	s.caret = len(input)
	s.clearSelection()
	s.lastWasYank = false
}

// deleteSelectedRunes removes a nonempty selection, optionally saving it on the
// kill ring. Plain deletes and replacement edits do not affect the ring.
func deleteSelectedRunes(input *[]rune, s *editState, kill bool) bool {
	start, end, ok := s.selectionRange(*input)
	if !ok {
		return false
	}
	if kill {
		s.pushKill((*input)[start:end])
	}
	*input = append((*input)[:start], (*input)[end:]...)
	s.caret = start
	s.clearSelection()
	s.lastWasYank = false
	return true
}

// insertRunes replaces selected text, or inserts rs at the caret, then moves
// the caret past the inserted runes. An empty insertion remains a no-op.
func insertRunes(input *[]rune, s *editState, rs []rune) {
	if len(rs) == 0 {
		return
	}
	s.clampCaret(*input)
	deleteSelectedRunes(input, s, false)
	s.clearSelection()
	*input = append(*input, make([]rune, len(rs))...)
	copy((*input)[s.caret+len(rs):], (*input)[s.caret:])
	copy((*input)[s.caret:], rs)
	s.caret += len(rs)
	s.lastWasYank = false
}

// deleteBack removes selected text or the rune before the caret.
// Reports whether it ran.
func deleteBack(input *[]rune, s *editState) bool {
	s.clampCaret(*input)
	if deleteSelectedRunes(input, s, false) {
		return true
	}
	s.clearSelection()
	if s.caret == 0 {
		return false
	}
	*input = append((*input)[:s.caret-1], (*input)[s.caret:]...)
	s.caret--
	s.lastWasYank = false
	return true
}

// deleteForward removes selected text or the rune at the caret.
// Reports whether it ran.
func deleteForward(input *[]rune, s *editState) bool {
	s.clampCaret(*input)
	if deleteSelectedRunes(input, s, false) {
		return true
	}
	s.clearSelection()
	if s.caret == len(*input) {
		return false
	}
	*input = append((*input)[:s.caret], (*input)[s.caret+1:]...)
	s.lastWasYank = false
	return true
}

// moveWordBack moves the caret to the start of the previous word, past the
// separating whitespace run.
func moveWordBack(input []rune, s *editState) {
	s.clampCaret(input)
	s.clearSelection()
	i := s.caret
	for i > 0 && isWordSpace(input[i-1]) {
		i--
	}
	for i > 0 && !isWordSpace(input[i-1]) {
		i--
	}
	s.caret = i
	s.lastWasYank = false
}

// moveWordForward moves the caret past the separating whitespace run and the
// following word.
func moveWordForward(input []rune, s *editState) {
	s.clampCaret(input)
	s.clearSelection()
	i := s.caret
	for i < len(input) && isWordSpace(input[i]) {
		i++
	}
	for i < len(input) && !isWordSpace(input[i]) {
		i++
	}
	s.caret = i
	s.lastWasYank = false
}

// killWordBack kills the previous word together with the whitespace run in
// front of it — one keystroke, one deletion (FR-17's whitespace-run rule).
// When the caret sits in whitespace, the run itself is the kill.
func killWordBack(input *[]rune, s *editState) bool {
	s.clampCaret(*input)
	if deleteSelectedRunes(input, s, true) {
		return true
	}
	s.clearSelection()
	i := s.caret
	for i > 0 && !isWordSpace((*input)[i-1]) {
		i--
	}
	for i > 0 && isWordSpace((*input)[i-1]) {
		i--
	}
	if i == s.caret {
		return false
	}
	s.pushKill((*input)[i:s.caret])
	*input = append((*input)[:i], (*input)[s.caret:]...)
	s.caret = i
	s.lastWasYank = false
	return true
}

// killWordForward kills the next word together with the whitespace run after
// it — the mirror of killWordBack. When the caret sits in whitespace, the
// run and the following word are the kill.
func killWordForward(input *[]rune, s *editState) bool {
	s.clampCaret(*input)
	if deleteSelectedRunes(input, s, true) {
		return true
	}
	s.clearSelection()
	i := s.caret
	for i < len(*input) && isWordSpace((*input)[i]) {
		i++
	}
	for i < len(*input) && !isWordSpace((*input)[i]) {
		i++
	}
	if i == s.caret {
		return false
	}
	s.pushKill((*input)[s.caret:i])
	*input = append((*input)[:s.caret], (*input)[i:]...)
	s.lastWasYank = false
	return true
}

// killToStart kills everything before the caret (the draft is one editable
// region, so this is the whole head).
func killToStart(input *[]rune, s *editState) bool {
	s.clampCaret(*input)
	if deleteSelectedRunes(input, s, true) {
		return true
	}
	s.clearSelection()
	if s.caret == 0 {
		return false
	}
	s.pushKill((*input)[:s.caret])
	*input = append([]rune(nil), (*input)[s.caret:]...)
	s.caret = 0
	s.lastWasYank = false
	return true
}

// killToEnd kills everything from the caret to the end of the draft.
func killToEnd(input *[]rune, s *editState) bool {
	s.clampCaret(*input)
	if deleteSelectedRunes(input, s, true) {
		return true
	}
	s.clearSelection()
	if s.caret == len(*input) {
		return false
	}
	s.pushKill((*input)[s.caret:])
	*input = (*input)[:s.caret]
	s.lastWasYank = false
	return true
}

// yank replaces selected text or reinserts the most recent kill at the caret.
// A consecutive yank (no edit between) replaces the last yank with the
// next-older ring entry, cycling back to the newest after the oldest.
func yank(input *[]rune, s *editState) bool {
	if len(s.ring) == 0 {
		return false
	}
	s.clampCaret(*input)
	idx := 0
	if s.selecting {
		// A new selection takes precedence over a previous yank's span.
		deleteSelectedRunes(input, s, false)
		s.clearSelection()
	} else if s.lastWasYank && s.yankLen > 0 && s.yankLen <= s.caret {
		// Replace the previous yank in place.
		start := s.caret - s.yankLen
		*input = append((*input)[:start], (*input)[s.caret:]...)
		s.caret = start
		idx = (s.yankIdx + 1) % len(s.ring)
	}
	kill := s.ring[idx]
	insertRunes(input, s, kill) // clears lastWasYank
	s.yankIdx = idx
	s.yankLen = len(kill)
	s.lastWasYank = true
	return true
}

// transpose swaps the two runes before the caret, keeping the caret between
// them. Fewer than two runes before the caret is a no-op.
func transpose(input *[]rune, s *editState) bool {
	s.clampCaret(*input)
	s.clearSelection()
	if s.caret < 2 {
		return false
	}
	(*input)[s.caret-2], (*input)[s.caret-1] = (*input)[s.caret-1], (*input)[s.caret-2]
	s.lastWasYank = false
	return true
}

// pushKill stores a kill on the ring, newest first.
func (s *editState) pushKill(kill []rune) {
	if len(kill) == 0 {
		return
	}
	s.ring = append([][]rune{append([]rune(nil), kill...)}, s.ring...)
	if len(s.ring) > killRingCap {
		s.ring = s.ring[:killRingCap]
	}
	s.lastWasYank = false
}

// insertNewline inserts a typed newline at the caret (the Return-modifier
// action). Submit flattens typed newlines to spaces, matching pastes.
func insertNewline(input *[]rune, s *editState) {
	insertRunes(input, s, []rune{'\n'})
}
