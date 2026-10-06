package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"likha/internal/agent"
	"likha/internal/providers"
	likhaui "likha/internal/ui"
)

func statusWorkingTestUI(t *testing.T, width int) *ui {
	t.Helper()
	m := statusTestUI(t, "minimal", width, 24, providers.StoredStatusLineConfig{})
	m.working, m.runID, m.status = true, 1, "Waiting for model"
	m.events = make(chan agent.TurnEvent, 8)
	return m
}

func TestStatusWorkingSpinnerFramesLoopAtFixedWidth(t *testing.T) {
	m := statusWorkingTestUI(t, 80)
	want := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for frame := 0; frame < len(want)*3; frame++ {
		m.activityFrame = frame
		if got := m.statusSpinner(); got != want[frame%len(want)] || plainWidth(got) != 1 {
			t.Fatalf("frame %d: spinner=%q, want one-cell %q", frame, got, want[frame%len(want)])
		}
	}
	m.conn.ASCII = true
	for frame := 0; frame < 12; frame++ {
		m.activityFrame = frame
		if got := m.statusSpinner(); got != string("|/-\\"[frame%4]) || plainWidth(got) != 1 {
			t.Fatalf("frame %d: incorrect ASCII spinner %q", frame, got)
		}
	}
}

func TestStatusWorkingSpinnerAppearsBelowInput(t *testing.T) {
	forceANSI(t)
	m := statusWorkingTestUI(t, 80)
	m.input = []rune("a draft while the model runs")
	m.showActivity()
	view := m.View()
	rows := strings.Split(view, "\n")
	bottom := stripANSI(rows[len(rows)-1])
	if !strings.Contains(bottom, "⠋ Working…") || strings.Contains(bottom, "Waiting for model") {
		t.Fatalf("bottom status did not replace Waiting for model: %q", bottom)
	}
	if !strings.Contains(stripANSI(rows[len(rows)-2]), "a draft while the model runs") {
		t.Fatalf("indicator is not immediately beneath the editable input: %q", view)
	}
	// The transcript keeps its original ASCII spinner and text sweep.
	if row := m.lines[m.entryLines[m.activity]]; row != "| "+workingLabel {
		t.Fatalf("transcript indicator was changed: %q", row)
	}
}

func TestStatusWorkingSpinnerContinuesAfterTranscriptHandoff(t *testing.T) {
	m := statusWorkingTestUI(t, 80)
	m.showActivity()
	oldGeneration := m.activityGeneration
	m.activityTickCmd()
	_, cmd := m.Update(agent.TurnEvent{RunID: 1, Kind: "text", Text: "streaming answer"})
	if cmd == nil || m.hasActivity() || m.activityArmed != m.activityGeneration+1 {
		t.Fatal("model status clock did not re-arm after the transcript handoff")
	}
	before := m.statusState()
	frame := m.activityFrame
	m.Update(activityTickMsg{runID: 1, generation: oldGeneration})
	if m.activityFrame != frame {
		t.Fatal("stale transcript clock advanced the model status")
	}
	m.scroll, m.following = 3, false
	_, cmd = m.Update(activityTickMsg{runID: 1, generation: m.activityGeneration})
	if cmd == nil || m.statusState() == before {
		t.Fatal("bottom spinner stopped while the answer was still streaming")
	}
	if m.scroll != 3 || m.following {
		t.Fatal("bottom animation moved the transcript viewport")
	}
	if m.entries[len(m.entries)-1].content != "streaming answer" {
		t.Fatal("bottom animation changed transcript content")
	}
	// More deltas must not schedule a second clock for the same generation.
	if cmd := m.armActivityTick(); cmd != nil {
		t.Fatal("model status armed duplicate clocks")
	}
}

func TestStatusWorkingSpinnerStopsForOtherStates(t *testing.T) {
	for _, kind := range []string{"done", "error", "approval", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			m := statusWorkingTestUI(t, 80)
			if m.armActivityTick() == nil {
				t.Fatal("model status without a transcript placeholder did not arm")
			}
			if kind == "cancel" {
				m.cancelling, m.status = true, "Cancelling"
			} else {
				ev := agent.TurnEvent{RunID: 1, Kind: kind, Text: "provider error"}
				if kind == "approval" {
					ev.Approval = &agent.ApprovalRequest{Kind: "edit", Title: "Review", Body: "diff", Reply: make(chan bool, 1)}
				}
				m.Update(ev)
			}
			if state := m.statusState(); strings.Contains(state, "Working…") {
				t.Fatalf("%s left the model-running status: %q", kind, state)
			}
			if _, cmd := m.Update(activityTickMsg{runID: 1, generation: m.activityGeneration}); cmd != nil {
				t.Fatalf("%s re-armed the model status clock", kind)
			}
		})
	}
}

func TestStatusWorkingSpinnerKeepsLabelStillAcrossThemes(t *testing.T) {
	forceANSI(t)
	m := statusWorkingTestUI(t, 80)
	for _, name := range likhaui.ThemeNames() {
		for _, dark := range []bool{false, true} {
			m.theme = likhaui.Resolve(name, dark)
			var label string
			for frame := 0; frame < len(statusSpinnerFrames); frame++ {
				m.activityFrame = frame
				plain := m.statusState() + " · Page 1/1 · ^C"
				rendered := m.renderStatusHint(plain, m.theme.Help)
				if stripANSI(rendered) != plain {
					t.Fatalf("theme %s dark %t changed visible hint text", name, dark)
				}
				start := strings.Index(rendered, workingLabel)
				if start < 0 {
					t.Fatalf("theme %s split the readable label", name)
				}
				suffix := rendered[start:]
				if frame == 0 {
					label = suffix
				} else if suffix != label {
					t.Fatalf("theme %s frame %d animated the readable label", name, frame)
				}
			}
		}
	}
}

func TestStatusWorkingSpinnerWidthsAndFallbacks(t *testing.T) {
	previous := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(previous)
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.Ascii} {
		lipgloss.SetColorProfile(profile)
		for _, ascii := range []bool{false, true} {
			for _, nerd := range []bool{false, true} {
				for width := 40; width <= 200; width++ {
					m := statusWorkingTestUI(t, width)
					m.conn.ASCII, m.conn.Nerd = ascii, nerd
					m.glyphs = likhaui.NerdGlyphs()
					m.queue = []string{"first", "second"}
					frames := map[string]bool{}
					for frame := 0; frame < len(statusSpinnerFrames); frame++ {
						m.activityFrame = frame
						rows := m.statusLineRows(100, 200)
						last := stripANSI(rows[len(rows)-1])
						if profile == termenv.Ascii && strings.Contains(rows[len(rows)-1], "\x1b[") {
							t.Fatal("no-color status contains ANSI styling")
						}
						hasCancel := strings.Contains(last, "^C") || strings.Contains(last, "Ctrl+C cancel")
						if !strings.Contains(last, "Working…") || strings.Contains(last, "Waiting for model") || !hasCancel || !strings.Contains(last, "Page 100/200") {
							t.Fatalf("profile %v ascii %t nerd %t width %d lost activity/controls: %q", profile, ascii, nerd, width, last)
						}
						queued := "2 queued"
						if width < 70 {
							queued = "2Q"
						}
						if !strings.Contains(last, queued) {
							t.Fatalf("width %d lost queued count: %q", width, last)
						}
						if ascii && strings.ContainsAny(last, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") || strings.Contains(last, likhaui.NerdGlyphs().Waiting) {
							t.Fatalf("width %d ignored the spinner glyph rules: %q", width, last)
						}
						for _, row := range rows {
							if got := plainWidth(row); got > width {
								t.Fatalf("width %d status overflow (%d): %q", width, got, row)
							}
						}
						frames[last] = true
					}
					if len(frames) < 3 {
						t.Fatalf("width %d spinner did not animate", width)
					}
				}
			}
		}
	}
}
