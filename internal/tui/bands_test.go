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

// M3 band roles (specs/adaptive-themes M3): rebuild wires each role to its
// fg-on-band style, default stays plain, and limited profiles degrade to
// legible plain text with labels and content intact.

func bandsTestUI(t *testing.T) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// colorName renders a TerminalColor by structural type so tests compare
// roles without pinning hex values.
func colorName(c lipgloss.TerminalColor) string {
	if c == nil {
		return "nil"
	}
	if _, ok := c.(lipgloss.NoColor); ok {
		return "NoColor"
	}
	if ac, ok := c.(lipgloss.AdaptiveColor); ok {
		return "Adaptive(" + ac.Light + "|" + ac.Dark + ")"
	}
	if col, ok := c.(lipgloss.Color); ok {
		return "Color(" + string(col) + ")"
	}
	return "other"
}

// roleLine finds the first layout line with the given prefix.
func roleLine(m *ui, prefix string) (int, string) {
	for i, line := range m.lines {
		if strings.HasPrefix(line, prefix) {
			return i, line
		}
	}
	return -1, ""
}

func rebuildRoles(t *testing.T, name string) (*ui, lisaui.Theme) {
	t.Helper()
	theme := lisaui.Resolve(name, true)
	m := bandsTestUI(t)
	m.theme = theme
	m.themeName = name
	m.entries = append(m.entries,
		entry{role: "You", content: "hello"},
		entry{role: "Assistant", content: "hi there"},
		entry{role: "Tool", content: "listing files"},
		entry{role: "Reasoning", content: "thinking"},
		entry{role: "Error", content: "boom"},
		entry{role: "Queued", content: "queued prompt"},
		entry{role: "Lisa", content: "plain prose"},
	)
	m.layoutWidth = 0
	_ = m.View()
	return m, theme
}

func TestBandRolesAcrossThemes(t *testing.T) {
	for _, name := range lisaui.ThemeNames() {
		if name == "default" {
			continue
		}
		m, theme := rebuildRoles(t, name)
		cases := []struct {
			prefix string
			fg     lipgloss.TerminalColor
			bg     lipgloss.TerminalColor
		}{
			{"You:", theme.Normal.GetForeground(), theme.BgUser.GetBackground()},
			{"Assistant:", theme.Normal.GetForeground(), theme.BgModel.GetBackground()},
			{"Tool:", theme.Muted.GetForeground(), theme.BgTool.GetBackground()},
			{"Error:", theme.Error.GetForeground(), theme.Base.GetBackground()},
			{"Queued:", theme.Muted.GetForeground(), theme.BgUser.GetBackground()},
		}
		for _, tc := range cases {
			idx, line := roleLine(m, tc.prefix)
			if idx < 0 {
				t.Fatalf("theme %s: %s line missing: %q", name, tc.prefix, m.lines)
			}
			got := m.lineStyles[idx]
			if fg := colorName(got.GetForeground()); fg != colorName(tc.fg) {
				t.Fatalf("theme %s: %s fg %v, want %v (line %q)", name, tc.prefix, fg, colorName(tc.fg), line)
			}
			if bg := colorName(got.GetBackground()); bg != colorName(tc.bg) {
				t.Fatalf("theme %s: %s bg %v, want %v (line %q)", name, tc.prefix, bg, colorName(tc.bg), line)
			}
		}
		// Reasoning stays flat: same fg as Muted, no background.
		idx, line := roleLine(m, "Reasoning:")
		if idx < 0 {
			t.Fatalf("theme %s: Reasoning line missing", name)
		}
		if got := m.lineStyles[idx]; got.Value() != theme.Muted.Value() {
			t.Fatalf("theme %s: Reasoning style %v, want Muted %v", name, got, theme.Muted)
		}
		if _, ok := m.lineStyles[idx].GetBackground().(lipgloss.NoColor); !ok {
			t.Fatalf("theme %s: Reasoning must carry no band, got bg %v (line %q)", name, m.lineStyles[idx].GetBackground(), line)
		}
		// Lisa/system keeps the canvas: no bespoke band.
		lisaIdx, _ := roleLine(m, "Lisa:")
		if lisaIdx < 0 {
			t.Fatalf("theme %s: Lisa line missing", name)
		}
		if bg := colorName(m.lineStyles[lisaIdx].GetBackground()); bg != colorName(theme.Base.GetBackground()) {
			t.Fatalf("theme %s: Lisa bg %v, want canvas %v", name, bg, colorName(theme.Base.GetBackground()))
		}
		// Bands are real for non-default families.
		if got := theme.BgUserBG(); got == "" {
			t.Fatalf("theme %s: BgUser band empty", name)
		}
		if got := theme.BgToolBG(); got == "" {
			t.Fatalf("theme %s: BgTool band empty", name)
		}
		if got := theme.BgModelBG(); got == "" {
			t.Fatalf("theme %s: BgModel band empty", name)
		}
	}
}

func TestLogoOnCanvasAcrossThemes(t *testing.T) {
	for _, name := range lisaui.ThemeNames() {
		if name == "default" {
			continue
		}
		theme := lisaui.Resolve(name, true)
		m := bandsTestUI(t)
		m.theme = theme
		m.themeName = name
		m.entries = append(m.entries, entry{role: "Logo", content: "LISA"})
		m.layoutWidth = 0
		_ = m.View()
		idx, line := roleLine(m, "LISA")
		if idx < 0 {
			t.Fatalf("theme %s: Logo line missing: %q", name, m.lines)
		}
		got := m.lineStyles[idx]
		if fg := colorName(got.GetForeground()); fg != colorName(theme.Title.GetForeground()) {
			t.Fatalf("theme %s: Logo fg %v, want Title %v (line %q)", name, fg, colorName(theme.Title.GetForeground()), line)
		}
		if bg := colorName(got.GetBackground()); bg != colorName(theme.Base.GetBackground()) {
			t.Fatalf("theme %s: Logo bg %v, want canvas %v (line %q)", name, bg, colorName(theme.Base.GetBackground()), line)
		}
	}
}

func TestDefaultBandsAreNoOps(t *testing.T) {
	m, theme := rebuildRoles(t, "default")
	if theme.BgUserBG() != "" || theme.BgToolBG() != "" || theme.BgModelBG() != "" {
		t.Fatalf("default bands must stay terminal default: %q %q %q",
			theme.BgUserBG(), theme.BgToolBG(), theme.BgModelBG())
	}
	plain := lipgloss.NewStyle()
	for _, prefix := range []string{"You:", "Assistant:", "Tool:", "Lisa:"} {
		idx, line := roleLine(m, prefix)
		if idx < 0 {
			t.Fatalf("default: %s line missing", prefix)
		}
		got := m.lineStyles[idx]
		if prefix == "Assistant:" || prefix == "You:" {
			// Normal is unset for default, so prose stays plain.
			if got.Value() != plain.Value() {
				t.Fatalf("default: %s style %v, want plain (line %q)", prefix, got, line)
			}
		}
		if _, ok := got.GetBackground().(lipgloss.NoColor); !ok {
			t.Fatalf("default: %s must paint no background, got %v (line %q)", prefix, got.GetBackground(), line)
		}
	}
	view := m.View()
	if strings.Contains(view, "48;2") || strings.Contains(view, "48;5") {
		t.Fatalf("default view must not paint a background: %q", view[:min(120, len(view))])
	}
}

func TestDegradationAcrossProfiles(t *testing.T) {
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.Ascii} {
		previous := lipgloss.ColorProfile()
		lipgloss.SetColorProfile(profile)
		func() {
			defer lipgloss.SetColorProfile(previous)
			for _, name := range lisaui.ThemeNames() {
				m, _ := rebuildRoles(t, name)
				view := m.View()
				for _, want := range []string{"You: hello", "Assistant: hi there", "Tool: listing files", "Reasoning: thinking", "Error: boom", "Lisa: plain prose"} {
					if !strings.Contains(stripANSI(view), want) {
						t.Fatalf("profile %v theme %s: content %q lost in %q", profile, name, want, stripANSI(view)[:min(200, len(stripANSI(view)))])
					}
				}
			}
		}()
	}
}

func TestDegradedBandsKeepWidths(t *testing.T) {
	forceANSI(t)
	for _, width := range []int{40, 80} {
		for _, name := range lisaui.ThemeNames() {
			theme := lisaui.Resolve(name, true)
			m := bandsTestUI(t)
			m.theme = theme
			m.themeName = name
			m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m.entries = append(m.entries,
				entry{role: "You", content: strings.Repeat("word ", 40)},
				entry{role: "Assistant", content: strings.Repeat("answer ", 40)},
				entry{role: "Tool", content: strings.Repeat("output ", 40)},
				entry{role: "Reasoning", content: strings.Repeat("thought ", 40)},
			)
			m.layoutWidth = 0
			for i, row := range strings.Split(m.View(), "\n") {
				if w := plainWidth(row); w != width {
					t.Fatalf("profile ANSI theme %s width %d: row %d width %d != %d: %q", name, width, i, w, width, stripANSI(row))
				}
			}
		}
	}
}
