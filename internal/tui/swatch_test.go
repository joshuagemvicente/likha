package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"lisa/internal/providers"
	"lisa/internal/session"
)

// Swatches paint inline: a hex literal in an assistant message renders its
// own background (truecolor 48;2, ANSI-degraded per profile) while the
// stripped text, widths, and surrounding band stay exactly as without it.
func swatchTestUI(t *testing.T, role, content string) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "tokyonight"}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.entries = append(m.entries, entry{role: role, content: content})
	m.layoutWidth = 0
	forceANSI(t)
	return m
}

func TestSwatchPaintsHexBackground(t *testing.T) {
	m := swatchTestUI(t, "Assistant", "the accent is #4493f8 here")
	view := m.View()
	if !strings.Contains(view, "104m#4493f8") {
		t.Fatalf("hex literal painted no swatch background: %q", view)
	}
	if got := stripANSI(view); !strings.Contains(got, "the accent is #4493f8 here") {
		t.Fatalf("swatch altered visible text: %q", got)
	}
	for i, row := range strings.Split(view, "\n") {
		if w := plainWidth(row); w != 80 {
			t.Fatalf("row %d width %d != 80 after swatch: %q", i, w, stripANSI(row))
		}
	}
}

func TestSwatchPaintsHSLAndRGB(t *testing.T) {
	m := swatchTestUI(t, "Assistant", "a hsl(210, 80%, 60%) and rgb(255, 0, 0) pair")
	view := m.View()
	// hsl(210,80%,60%) degrades to ANSI 111 under the test profile; the
	// call keeps its text and gains the swatch background either way.
	if !strings.Contains(stripANSI(view), "hsl(210, 80%, 60%)") {
		t.Fatalf("hsl literal text lost: %q", stripANSI(view))
	}
	if !strings.Contains(view, "mhsl(210, 80%, 60%)") {
		t.Fatalf("hsl literal painted no swatch: %q", view)
	}
	if !strings.Contains(view, "101mrgb(255, 0, 0)") {
		t.Fatalf("rgb literal painted no swatch: %q", view)
	}
}

func TestSwatchKeepsBandAndRestoresOnTheme(t *testing.T) {
	m := swatchTestUI(t, "Assistant", "blue #4493f8 chip")
	band := m.View()
	if !strings.Contains(band, "104m#4493f8") {
		t.Fatalf("no swatch before theme switch: %q", band)
	}
	for _, name := range []string{"github", "default"} {
		m.themeName = name
		m.previewTheme(name)
		m.layoutWidth = 0
		view := m.View()
		if name == "default" {
			// Canvas drops out, but the literal's own color stays.
			if !strings.Contains(view, "104m#4493f8") {
				t.Fatalf("default theme lost the swatch: %q", view)
			}
			continue
		}
		if !strings.Contains(view, "104m#4493f8") {
			t.Fatalf("theme %s lost the swatch: %q", name, view)
		}
		if got := stripANSI(view); !strings.Contains(got, "blue #4493f8 chip") {
			t.Fatalf("theme %s altered text: %q", name, got)
		}
	}
}

func TestSwatchSkipsLogoAndWrapsClean(t *testing.T) {
	m := swatchTestUI(t, "Assistant", "edge "+strings.Repeat("#abc ", 30)+"end")
	view := m.View()
	for i, row := range strings.Split(view, "\n") {
		if w := plainWidth(row); w != 80 {
			t.Fatalf("row %d width %d != 80 with wrapped swatches: %q", i, w, stripANSI(row))
		}
	}
	if got := stripANSI(view); !strings.Contains(got, "#abc") {
		t.Fatalf("wrapped swatch text lost: %q", got)
	}
}
