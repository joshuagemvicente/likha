package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"likha/internal/providers"
	"likha/internal/session"
	likhaui "likha/internal/ui"
)

// Band roles (specs/adaptive-themes M3, superseded in part by
// transcript-redesign): rebuild wires each block to its glyph and fg-on-canvas
// style, only the user prompt carries a band (default included), and limited
// profiles degrade to legible plain text with glyphs and content intact.

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

func rebuildRoles(t *testing.T, name string) (*ui, likhaui.Theme) {
	t.Helper()
	theme := likhaui.Resolve(name, true)
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
		entry{role: "Likha", content: "plain prose"},
	)
	m.layoutWidth = 0
	_ = m.View()
	return m, theme
}

func TestBandRolesAcrossThemes(t *testing.T) {
	for _, name := range likhaui.ThemeNames() {
		if name == "default" {
			continue
		}
		m, theme := rebuildRoles(t, name)
		// Glyph-language blocks (transcript-redesign § Backgrounds): only the
		// user prompt (and its queued form) sits on a band; assistant, tool,
		// error, and notice blocks sit on the base canvas.
		cases := []struct {
			prefix string
			fg     lipgloss.TerminalColor
			bg     lipgloss.TerminalColor
		}{
			{"> hello", theme.Normal.GetForeground(), theme.BgUser.GetBackground()},
			{"⏺ hi there", theme.Normal.GetForeground(), theme.Base.GetBackground()},
			{"⏺ listing files", theme.Muted.GetForeground(), theme.Base.GetBackground()},
			{"✗ boom", theme.Error.GetForeground(), theme.Base.GetBackground()},
			{"> queued · queued prompt", theme.Muted.GetForeground(), theme.BgUser.GetBackground()},
			{"ℹ plain prose", theme.Muted.GetForeground(), theme.Base.GetBackground()},
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
			// The user band spans the prompt block plus one padding row
			// above and below; canvas blocks get no padding rows.
			if tc.bg == theme.BgUser.GetBackground() {
				for _, pad := range []int{idx - 1, idx + 1} {
					if m.lines[pad] != "" || colorName(m.lineStyles[pad].GetBackground()) != colorName(tc.bg) {
						t.Fatalf("theme %s: %s band padding row %d = %q bg %v, want blank on BgUser", name, tc.prefix, pad, m.lines[pad], colorName(m.lineStyles[pad].GetBackground()))
					}
				}
			} else if bg := colorName(m.lineStyles[idx-1].GetBackground()); bg == colorName(theme.BgUser.GetBackground()) {
				t.Fatalf("theme %s: %s is preceded by a band row", name, tc.prefix)
			}
		}
		// Reasoning stays flat: same fg as Muted, no background.
		idx, line := roleLine(m, "✻ Thought")
		if idx < 0 {
			t.Fatalf("theme %s: Reasoning line missing", name)
		}
		if got := m.lineStyles[idx]; colorName(got.GetForeground()) != colorName(theme.Muted.GetForeground()) {
			t.Fatalf("theme %s: Reasoning style %v, want Muted %v", name, got, theme.Muted)
		}
		if _, ok := m.lineStyles[idx].GetBackground().(lipgloss.NoColor); !ok {
			t.Fatalf("theme %s: Reasoning must carry no band, got bg %v (line %q)", name, m.lineStyles[idx].GetBackground(), line)
		}
		// Bands stay defined for non-default families (dialogs and
		// compatibility), even though only BgUser paints the transcript.
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
	for _, name := range likhaui.ThemeNames() {
		if name == "default" {
			continue
		}
		theme := likhaui.Resolve(name, true)
		m := bandsTestUI(t)
		m.theme = theme
		m.themeName = name
		m.entries = append(m.entries, entry{role: "Logo", content: "LIKHA"})
		m.layoutWidth = 0
		_ = m.View()
		idx, line := roleLine(m, "LIKHA")
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

// TestDefaultBandOnlyOnUserPrompt: the default family keeps the plain
// terminal look everywhere except the user prompt, which gets a faint
// neutral band (transcript-redesign § Backgrounds); tool and model bands stay
// terminal default and no other transcript row paints a background.
func TestDefaultBandOnlyOnUserPrompt(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m, theme := rebuildRoles(t, "default")
	if theme.BgUserBG() == "" {
		t.Fatal("default must carry a neutral user band")
	}
	if theme.BgToolBG() != "" || theme.BgModelBG() != "" {
		t.Fatalf("default tool/model bands must stay terminal default: %q %q", theme.BgToolBG(), theme.BgModelBG())
	}
	plain := lipgloss.NewStyle()
	for _, prefix := range []string{"⏺ hi there", "⏺ listing files", "ℹ plain prose"} {
		idx, line := roleLine(m, prefix)
		if idx < 0 {
			t.Fatalf("default: %s line missing", prefix)
		}
		got := m.lineStyles[idx]
		if prefix == "⏺ hi there" {
			// Normal is unset for default, so assistant prose stays plain.
			if got.GetForeground() != plain.GetForeground() {
				t.Fatalf("default: %s style %v, want plain (line %q)", prefix, got, line)
			}
		}
		if _, ok := got.GetBackground().(lipgloss.NoColor); !ok {
			t.Fatalf("default: %s must paint no background, got %v (line %q)", prefix, got.GetBackground(), line)
		}
	}
	user, line := roleLine(m, "> hello")
	if user < 0 {
		t.Fatalf("default: user line missing: %q", m.lines)
	}
	if bg := colorName(m.lineStyles[user].GetBackground()); bg != colorName(theme.BgUser.GetBackground()) {
		t.Fatalf("default: user prompt bg %v, want BgUser (line %q)", bg, line)
	}
	// Only user-band rows (the prompt plus its padding, and the queued
	// prompt) paint a background in the rendered transcript.
	rows := strings.Split(m.View(), "\n")
	checked, bandRows := 0, 0
	for i := range m.bodyHeight() {
		at := m.scroll + i
		if at >= len(m.lines) {
			break
		}
		banded := colorName(m.lineStyles[at].GetBackground()) == colorName(theme.BgUser.GetBackground())
		painted := strings.Contains(rows[i], "48;2") || strings.Contains(rows[i], "48;5")
		if banded != painted {
			t.Fatalf("default: row %d %q painted=%t, want %t: %q", i, m.lines[at], painted, banded, rows[i])
		}
		checked++
		if banded {
			bandRows++
		}
	}
	if checked < 10 || bandRows < 3 {
		t.Fatalf("default: checked %d transcript rows, %d on the band", checked, bandRows)
	}
}

func TestDegradationAcrossProfiles(t *testing.T) {
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.Ascii} {
		previous := lipgloss.ColorProfile()
		lipgloss.SetColorProfile(profile)
		func() {
			defer lipgloss.SetColorProfile(previous)
			for _, name := range likhaui.ThemeNames() {
				m, _ := rebuildRoles(t, name)
				view := m.View()
				// Glyphs and words survive every profile: block identity
				// never rides on color alone.
				for _, want := range []string{"> hello", "⏺ hi there", "⏺ listing files", "✻ Thought", "✗ boom", "> queued · queued prompt", "ℹ plain prose"} {
					if !strings.Contains(stripANSI(view), want) {
						t.Fatalf("profile %v theme %s: content %q lost in %q", profile, name, want, stripANSI(view)[:min(200, len(stripANSI(view)))])
					}
				}
				for _, row := range strings.Split(stripANSI(view), "\n") {
					for _, label := range []string{"You:", "Assistant:", "Tool:", "Reasoning:", "Likha:", "Error:", "Agent:", "Queued:"} {
						if strings.HasPrefix(row, label) {
							t.Fatalf("profile %v theme %s: row keeps role label %q: %q", profile, name, label, row)
						}
					}
				}
			}
		}()
	}
}

func TestDegradedBandsKeepWidths(t *testing.T) {
	forceANSI(t)
	for _, width := range []int{40, 80} {
		for _, name := range likhaui.ThemeNames() {
			theme := likhaui.Resolve(name, true)
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
