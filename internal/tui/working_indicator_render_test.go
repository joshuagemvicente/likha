package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"lisa/internal/providers"
	"lisa/internal/session"
	lisaui "lisa/internal/ui"
)

func workingRenderUI(t *testing.T, width int) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	m.working = true
	m.showActivity()
	return m
}

func activityRows(view string) []string {
	var rows []string
	for _, row := range strings.Split(view, "\n") {
		if strings.Contains(stripANSI(row), workingLabel) {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestWorkingIndicatorFramesAndThemeRender(t *testing.T) {
	forceANSI(t)
	m := workingRenderUI(t, 80)
	var plainSuffix string
	var rendered map[string]bool = map[string]bool{}
	var spinners map[string]bool = map[string]bool{}
	for frame := 0; frame < 8; frame++ {
		m.activityFrame = frame
		m.layoutWidth = 0
		rows := activityRows(m.View())
		if len(rows) != 1 {
			t.Fatalf("frame %d: activity rows=%d", frame, len(rows))
		}
		plain := strings.TrimSpace(stripANSI(rows[0]))
		if len([]rune(plain)) < 3 || !strings.Contains(plain, workingLabel) {
			t.Fatalf("frame %d: malformed activity row %q", frame, plain)
		}
		runes := []rune(plain)
		if !strings.ContainsRune("|/-\\", runes[0]) || runes[1] != ' ' {
			t.Fatalf("frame %d: spinner missing: %q", frame, plain)
		}
		spinners[string(runes[0])] = true
		suffix := string(runes[1:])
		if frame == 0 {
			plainSuffix = suffix
		} else if suffix != plainSuffix {
			t.Fatalf("frame %d: plain label changed: %q != %q", frame, suffix, plainSuffix)
		}
		if !strings.Contains(rows[0], "\x1b[") {
			t.Fatalf("frame %d: expected theme color under forced ANSI", frame)
		}
		rendered[rows[0]] = true
	}
	if len(spinners) != len(workingSpinnerFrames) {
		t.Fatalf("spinner frames visited %v", spinners)
	}
	if len(rendered) < 2 {
		t.Fatal("styled activity row did not animate")
	}
	if strings.Contains(stripANSI(m.View()), "Working:") {
		t.Fatal("activity row has a role prefix")
	}

	for _, name := range lisaui.ThemeNames() {
		theme := lisaui.Resolve(name, true)
		m := workingRenderUI(t, 80)
		m.theme = theme
		m.layoutWidth = 0
		if !strings.Contains(stripANSI(m.View()), workingLabel) {
			t.Fatalf("theme %s lost activity row", name)
		}
	}
}

func TestWorkingIndicatorLimitedProfilesAndWidths(t *testing.T) {
	previous := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(previous)
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.Ascii} {
		lipgloss.SetColorProfile(profile)
		for _, width := range []int{40, 80, 141, 200} {
			m := workingRenderUI(t, width)
			m.activityFrame = 5
			m.layoutWidth = 0
			view := m.View()
			rows := activityRows(view)
			if len(rows) != 1 || !strings.Contains(stripANSI(rows[0]), workingLabel) {
				t.Fatalf("profile %v width %d: activity row missing: %q", profile, width, stripANSI(view))
			}
			if plainWidth(rows[0]) > width {
				t.Fatalf("profile %v width %d: activity overflows: %q", profile, width, stripANSI(rows[0]))
			}
			for i, row := range strings.Split(view, "\n") {
				if got := plainWidth(row); got > width {
					t.Fatalf("profile %v width %d line %d overflows (%d): %q", profile, width, i, got, stripANSI(row))
				}
			}
		}
	}
}
