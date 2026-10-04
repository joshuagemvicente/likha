package tui

import (
	"time"

	"likha/internal/transcript"
)

// Reasoning blocks and turn footers (spec transcript-redesign § Reasoning,
// § Turn footer).
//
// A reasoning block is identified by its ordinal: the count of Reasoning
// entries before it. Entries are only ever removed for per-run furniture
// (Working, Queued, a failed answer stream), never for reasoning, so the
// ordinal stays put while entry indices shift. Measured durations, focus,
// and inline expansion all key on it.

// liveReasoningTail is how many trailing rows the live `Thinking…` block
// shows.
const liveReasoningTail = 3

// thoughtTime is a reasoning block's duration and whether it was measured.
type thoughtTime struct {
	d     time.Duration
	known bool
}

// now is the UI clock; tests pin it to measure durations exactly.
func (m *ui) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// reasoningOrdinal counts the Reasoning entries before index.
func (m *ui) reasoningOrdinal(index int) int {
	ord := 0
	for i := 0; i < index && i < len(m.entries); i++ {
		if m.entries[i].role == "Reasoning" {
			ord++
		}
	}
	return ord
}

// reasoningCount is the number of Reasoning entries in the transcript.
func (m *ui) reasoningCount() int {
	return m.reasoningOrdinal(len(m.entries))
}

// reasoningEntry is the entry index of the ord-th Reasoning entry, or -1.
func (m *ui) reasoningEntry(ord int) int {
	for i, e := range m.entries {
		if e.role != "Reasoning" {
			continue
		}
		if ord == 0 {
			return i
		}
		ord--
	}
	return -1
}

// reasoningLive reports whether the entry at index is the open reasoning
// stream of a working run: it renders as the live tail, not a marker.
func (m *ui) reasoningLive(index int) bool {
	return m.working && index >= 0 && index == m.reasoningStream
}

// openReasoning starts a reasoning block at the end of the transcript and
// its timer.
func (m *ui) openReasoning() {
	m.entries = append(m.entries, entry{role: "Reasoning"})
	m.reasoningStream = len(m.entries) - 1
	m.reasoningBuf.Reset()
	m.reasoningStarted = m.now()
}

// closeReasoning ends the open reasoning block, if any: its duration is
// recorded under its ordinal, and the stream and its buffer reset so the
// next reasoning delta opens a new block below whatever came in between.
// Every event that ends a block calls it — answer text, tool start and
// result, approval, steer delivery, run end, and a new turn.
func (m *ui) closeReasoning() {
	if i := m.reasoningStream; i >= 0 && i < len(m.entries) && m.entries[i].role == "Reasoning" && !m.reasoningStarted.IsZero() {
		if m.thoughtDurations == nil {
			m.thoughtDurations = make(map[int]time.Duration)
		}
		m.thoughtDurations[m.reasoningOrdinal(i)] = max(m.now().Sub(m.reasoningStarted), 0)
		m.layoutWidth = 0
	}
	m.reasoningStream = -1
	m.reasoningBuf.Reset()
	m.reasoningStarted = time.Time{}
}

// resetThoughts forgets the per-view reasoning state: measured durations,
// expansion, and the open block. Session switches call it; the next
// layout reads durations back from the resumed Turn entries.
func (m *ui) resetThoughts() {
	m.thoughtDurations = nil
	m.thoughtExpanded = nil
	m.toolExpanded = nil
	m.reasoningStarted = time.Time{}
	m.turnStarted = time.Time{}
	m.turnModel = ""
	m.turnThoughtBase = 0
	m.turnToolItems = 0
}

// thoughtTimes resolves every reasoning block's duration, by ordinal.
// Durations measured in this view win. Otherwise a Turn entry's
// thoughts_ms list (one value per block of that turn, in order) covers the
// Reasoning entries between the previous Turn entry and it: the k-th entry
// gets the k-th value. When the list is shorter than the entries — reasoning
// from an earlier run that ended without a footer precedes this turn's —
// the values cover the last entries, the ones the turn produced, and the
// earlier ones stay unknown ("Thought").
func (m *ui) thoughtTimes() []thoughtTime {
	var times []thoughtTime
	since := 0
	for _, e := range m.entries {
		switch e.role {
		case "Reasoning":
			times = append(times, thoughtTime{})
		case transcript.TurnRole:
			if info, ok := transcript.DecodeTurn(e.content); ok && len(info.Thoughts) > 0 {
				block := times[since:]
				offset := max(len(block)-len(info.Thoughts), 0)
				for k := offset; k < len(block); k++ {
					block[k] = thoughtTime{d: info.Thoughts[k-offset], known: true}
				}
			}
			since = len(times)
		}
	}
	for ord, d := range m.thoughtDurations {
		if ord >= 0 && ord < len(times) {
			times[ord] = thoughtTime{d: d, known: true}
		}
	}
	return times
}

// turnThoughts lists the measured durations of this turn's reasoning
// blocks in order, for the Turn entry.
func (m *ui) turnThoughts() []time.Duration {
	var thoughts []time.Duration
	for ord := m.turnThoughtBase; ord < m.reasoningCount(); ord++ {
		thoughts = append(thoughts, m.thoughtDurations[ord])
	}
	return thoughts
}

// beginTurnFooter records what a user turn's footer reports: when it
// started, the model it runs on, where its reasoning blocks begin, and a
// fresh count of its tool items.
func (m *ui) beginTurnFooter() {
	m.turnStarted = m.now()
	m.turnModel = m.modelName
	m.turnThoughtBase = m.reasoningCount()
	m.turnToolItems = 0
}

// appendTurnFooter closes a finished user turn with its persisted Turn
// entry (spec § Turn footer). Every user turn that ends — done, cancelled,
// or failed — gets one; a compaction run never started a turn and gets
// none. tools counts the turn's top-level tool items, one per call even
// when a provider reuses a call ID across rounds.
func (m *ui) appendTurnFooter(tools int, cancelled bool) {
	if m.turnStarted.IsZero() {
		return
	}
	info := transcript.TurnInfo{
		Model:     m.turnModel,
		Duration:  max(m.now().Sub(m.turnStarted), 0),
		Tools:     tools,
		Cancelled: cancelled,
		Thoughts:  m.turnThoughts(),
	}
	m.entries = append(m.entries, entry{role: transcript.TurnRole, content: transcript.EncodeTurn(info)})
	m.turnStarted = time.Time{}
}

// reasoningRows lays out one Reasoning entry: the live `Thinking…` block
// with the last rows of the stream, the collapsed `Thought for Ns` marker,
// or the marker followed by the full reasoning when expanded. Every row
// fits width and escapes hidden runes.
func (m *ui) reasoningRows(md *markdownLayout, index int, t thoughtTime, expanded, focused bool, width int) []toolRow {
	g := m.blocks
	muted := m.theme.Muted
	italic := m.theme.Muted.Italic(true)
	const pad = "  "
	room := max(0, width-len(pad))
	if m.reasoningLive(index) {
		rows := []toolRow{{text: g.Thought + " " + toolClipCells("Thinking"+g.Ellipsis, room, g.Ellipsis), style: muted}}
		// Exactly the last rows (spec: "the last 3 lines"); earlier text
		// stays out of the live block until it collapses and expands.
		tail, _ := transcript.Tail(m.entries[index].content, room, liveReasoningTail)
		for _, line := range tail {
			rows = append(rows, toolRow{text: pad + line, style: italic})
		}
		return rows
	}
	glyph, style := g.Thought, muted
	if focused {
		glyph, style = g.Focus, withBase(m.theme.Selected, m.theme.Base)
	}
	rows := []toolRow{{text: glyph + " " + toolClipCells(transcript.ThoughtMarker(t.d, t.known), room, g.Ellipsis), style: style}}
	if !expanded {
		return rows
	}
	// Expanded reasoning renders as markdown (spec § Markdown) in Muted
	// italics, hanging under the pad.
	return append(rows, m.reasoningBodyRows(md, m.entries[index].content, width)...)
}

// footerRow lays out a Turn entry: `  ✻ model · duration · N tools` in
// Muted, segments dropping right to left at narrow widths. ok is false
// for content that does not decode, which renders nothing.
func (m *ui) footerRow(e entry, width int) (toolRow, bool) {
	info, ok := transcript.DecodeTurn(e.content)
	if !ok {
		return toolRow{}, false
	}
	g := m.blocks
	const indent = "  "
	text := indent + g.Thought + " " + transcript.FooterText(info, max(0, width-len(indent)-2), g.Ellipsis)
	return toolRow{text: text, style: m.theme.Muted}, true
}

// focusedThoughtEntry is the entry index of the focused reasoning marker.
// A marker whose block is live (or gone) is not focusable.
func (m *ui) focusedThoughtEntry() (int, bool) {
	state := &m.toolInspector
	if !state.focused || state.focusedThought == 0 || state.sessionID != m.snapshot.ID {
		return 0, false
	}
	index := m.reasoningEntry(state.focusedThought - 1)
	return index, index >= 0 && !m.reasoningLive(index)
}

// toggleThought expands or collapses the reasoning block at index inline.
// The state lives in this view only: rebuilds and resizes keep it, a
// session switch drops it, and it is never persisted.
func (m *ui) toggleThought(index int) {
	ord := m.reasoningOrdinal(index)
	if m.thoughtExpanded == nil {
		m.thoughtExpanded = make(map[int]bool)
	}
	if m.thoughtExpanded[ord] {
		delete(m.thoughtExpanded, ord)
	} else {
		m.thoughtExpanded[ord] = true
	}
	m.layoutWidth = 0
}

// transcriptFocusStops lists the entries Tab visits, in transcript order:
// each tool item and each collapsed reasoning marker.
func (m *ui) transcriptFocusStops() []int {
	plan := m.toolItemPlan()
	var indices []int
	for i, e := range m.entries {
		switch {
		case e.role == "Tool" && !plan.absorbed[i]:
			indices = append(indices, i)
		case e.role == "Reasoning" && !m.reasoningLive(i):
			indices = append(indices, i)
		}
	}
	return indices
}

// focusedStop is the focused tool item or reasoning marker.
func (m *ui) focusedStop() (int, bool) {
	if index, ok := m.focusedThoughtEntry(); ok {
		return index, true
	}
	return m.focusedToolEntry()
}
