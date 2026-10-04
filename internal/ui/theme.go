// Package ui holds Likha's color themes, the transcript block glyphs, and the
// optional Nerd Font glyph set. Themes map onto canvas roles (Base, the user
// band, and the code/diff tints; the tool/model bands stay defined for
// dialogs and compatibility), the foreground roles, and the
// muted/error/success signal roles; prose adopts the single Normal fg while
// block identity comes from glyphs. Nerd Font icons are strictly opt-in so
// plain-text output remains complete and legible without patched fonts.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type Theme struct {
	Base     lipgloss.Style // app canvas background (theme's Normal bg; default = terminal default)
	BgUser   lipgloss.Style // user band background (bg only; default = terminal default)
	BgTool   lipgloss.Style // tool band background (bg only; default = terminal default)
	BgModel  lipgloss.Style // assistant band background (bg only; default = terminal default)
	Title    lipgloss.Style // headings, header identity
	Selected lipgloss.Style // cursor rows, focused identity
	Normal   lipgloss.Style // conversation prose (uncolored by default)
	Help     lipgloss.Style // status line hints
	Border   lipgloss.Style // rule lines
	Warning  lipgloss.Style // pending review markers
	Error    lipgloss.Style // error entries
	Muted    lipgloss.Style // thinking-model reasoning output

	Accent       lipgloss.Style // accent fg without bold (Border's color): inline code, keywords
	Success      lipgloss.Style // succeeded tool status dot
	BgCode       lipgloss.Style // code block / inline code background (bg only)
	BgDiffAdd    lipgloss.Style // added diff line background (bg only)
	BgDiffRemove lipgloss.Style // removed diff line background (bg only)
}

// defaultTheme keeps Likha's original look: a single calm accent on a plain
// terminal palette. Foregrounds stay on the terminal's 16-color palette; the
// canvas stays the terminal default, and only the derived tints (user band,
// code, diffs) carry a neutral hex background (adaptive.go).
func defaultTheme() Theme {
	return Theme{
		BgUser:       lipgloss.NewStyle().Background(defaultBgUser()),
		Title:        lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true),
		Selected:     lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true),
		Normal:       lipgloss.NewStyle(),
		Help:         lipgloss.NewStyle().Bold(true),
		Border:       lipgloss.NewStyle(),
		Warning:      lipgloss.NewStyle().Bold(true),
		Error:        lipgloss.NewStyle(),
		Muted:        lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Accent:       lipgloss.NewStyle().Foreground(lipgloss.Color("12")),
		Success:      lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		BgCode:       lipgloss.NewStyle().Background(defaultBgCode()),
		BgDiffAdd:    lipgloss.NewStyle().Background(defaultBgDiff(standardGreenDark, standardGreenLight)),
		BgDiffRemove: lipgloss.NewStyle().Background(defaultBgDiff(standardRedDark, standardRedLight)),
	}
}

// palette is the raw color set a theme family defines for a light or dark
// terminal. Empty entries inherit the terminal defaults.
type palette struct {
	base    string // Normal background ("" = terminal default)
	accent  string // focus, headings, cursor
	text    string // optional main text tint ("" = terminal default)
	dim     string // hints
	warning string // pending review
	err     string // errors
	green   string // optional family green for success ("" = standard green)
}

// themeFamily maps a family name to its light and dark palettes; families
// with a single variant repeat it in both slots.
var families = map[string]struct{ dark, light palette }{
	"catppuccin": {
		dark:  palette{base: "#1e1e2e", accent: "#cba6f7", text: "#cdd6f4", dim: "#6c7086", warning: "#f9e2af", err: "#f38ba8", green: "#a6e3a1"},
		light: palette{base: "#eff1f5", accent: "#8839ef", text: "#4c4f69", dim: "#8c8fa1", warning: "#df8e1d", err: "#d20f39", green: "#40a02b"},
	},
	"habamax": {
		dark:  palette{base: "#1c1c1c", accent: "#5f87d7", text: "#bcbcbc", dim: "#7c7c6c", warning: "#d7af5f", err: "#ff8787"},
		light: palette{base: "#ffffff", accent: "#005f87", text: "#3a3a3a", dim: "#8a8a8a", warning: "#af8700", err: "#d75f5f"},
	},
	"gruvbox": {
		dark:  palette{base: "#282828", accent: "#fabd2f", text: "#ebdbb2", dim: "#928374", warning: "#fe8019", err: "#fb4934", green: "#b8bb26"},
		light: palette{base: "#fbf1c7", accent: "#b57614", text: "#3c3836", dim: "#7c6f64", warning: "#d65d0e", err: "#9d0006", green: "#79740e"},
	},
	"tokyonight": {
		dark:  palette{base: "#1a1b26", accent: "#7aa2f7", text: "#c0caf5", dim: "#565f89", warning: "#e0af68", err: "#f7768e", green: "#9ece6a"},
		light: palette{base: "#e1e2e7", accent: "#2e7de9", text: "#3760bf", dim: "#8990b3", warning: "#b15c00", err: "#c64343", green: "#587539"},
	},
	"nord": {
		dark:  palette{base: "#2e3440", accent: "#88c0d0", text: "#d8dee9", dim: "#616e88", warning: "#ebcb8b", err: "#bf616a", green: "#a3be8c"},
		light: palette{base: "#eceff4", accent: "#5e81ac", text: "#2e3440", dim: "#8f98b3", warning: "#b58900", err: "#bf616a", green: "#a3be8c"},
	},
	"dracula": {
		dark:  palette{base: "#282a36", accent: "#bd93f9", text: "#f8f8f2", dim: "#6272a4", warning: "#f1fa8c", err: "#ff5555", green: "#50fa7b"},
		light: palette{base: "#f8f8f2", accent: "#6c3fc5", text: "#282a36", dim: "#6272a4", warning: "#b58900", err: "#d6262e", green: "#14710a"},
	},
	"solarized": {
		dark:  palette{base: "#002b36", accent: "#268bd2", text: "#eee8d5", dim: "#586e75", warning: "#b58900", err: "#dc322f", green: "#859900"},
		light: palette{base: "#fdf6e3", accent: "#268bd2", text: "#073642", dim: "#93a1a1", warning: "#b58900", err: "#dc322f", green: "#859900"},
	},
	"rose-pine": {
		dark:  palette{base: "#191724", accent: "#c4a7e7", text: "#e0def4", dim: "#90819c", warning: "#f6c177", err: "#eb6f92"},
		light: palette{base: "#faf4ed", accent: "#907aa9", text: "#575279", dim: "#9893a5", warning: "#ea9d34", err: "#bf616a"},
	},
	"kanagawa": {
		dark:  palette{base: "#1f1f28", accent: "#7e9cd8", text: "#dcd7ba", dim: "#727169", warning: "#ffa066", err: "#e46876", green: "#98bb6c"},
		light: palette{base: "#f2ecbc", accent: "#2d4f67", text: "#43436c", dim: "#8a8980", warning: "#c4791b", err: "#c34043", green: "#6f894e"},
	},
	"everforest": {
		dark:  palette{base: "#2d353b", accent: "#a7c080", text: "#d3c6aa", dim: "#9c9c8c", warning: "#dbbc7f", err: "#e67e80", green: "#a7c080"},
		light: palette{base: "#fdf6e3", accent: "#829e57", text: "#5c6370", dim: "#a6b0a0", warning: "#bf9f40", err: "#d2554f", green: "#8da101"},
	},
	"one-dark": {
		dark:  palette{base: "#282c34", accent: "#61afef", text: "#abb2bf", dim: "#5c6370", warning: "#e5c07b", err: "#e06c75", green: "#98c379"},
		light: palette{base: "#fafafa", accent: "#4078f2", text: "#383a42", dim: "#a0a1a7", warning: "#c18401", err: "#e45649", green: "#50a14f"},
	},
	"ayu": {
		dark:  palette{base: "#0f1419", accent: "#ffcc66", text: "#b3b1ad", dim: "#626a73", warning: "#ffb454", err: "#f07178", green: "#aad94c"},
		light: palette{base: "#fafafa", accent: "#ff9940", text: "#5c6773", dim: "#abb0bf", warning: "#f29718", err: "#f07178", green: "#86b300"},
	},
	"flexoki": {
		dark:  palette{base: "#100f0f", accent: "#4385be", text: "#FFFCF0", dim: "#9f9d96", warning: "#d0a215", err: "#d14d41", green: "#879a39"},
		light: palette{base: "#fffcf0", accent: "#205ea6", text: "#100f0f", dim: "#6f6e69", warning: "#ad8301", err: "#af3029", green: "#66800b"},
	},
	"oxocarbon": {
		dark:  palette{base: "#161616", accent: "#33b1ff", text: "#f2f4f8", dim: "#8a8a8a", warning: "#ee5396", err: "#ff7eb6", green: "#42be65"},
		light: palette{base: "#ffffff", accent: "#0f62fe", text: "#161616", dim: "#6f6f6f", warning: "#d02e1f", err: "#da1e28", green: "#24a148"},
	},
	"night-owl": {
		dark:  palette{base: "#011627", accent: "#82aaff", text: "#d6deeb", dim: "#637777", warning: "#ecc48d", err: "#ef5350"},
		light: palette{base: "#fbfbfb", accent: "#2aa298", text: "#403f53", dim: "#828ca8", warning: "#daaa01", err: "#d3423e"},
	},
	"github": {
		dark:  palette{base: "#0d1117", accent: "#4493f8", text: "#f0f6fc", dim: "#9198a1", warning: "#d29922", err: "#f85149", green: "#3fb950"},
		light: palette{base: "#ffffff", accent: "#0969da", text: "#1f2328", dim: "#59636e", warning: "#9a6700", err: "#d1242f", green: "#1a7f37"},
	},
	"monokai": {
		dark:  palette{base: "#2d2a2e", accent: "#ffd866", text: "#fff1f3", dim: "#727072", warning: "#f9cc6c", err: "#fd6883"},
		light: palette{base: "#f9f9f7", accent: "#c77dbb", text: "#29242a", dim: "#a59fa0", warning: "#c77dbb", err: "#e14775"},
	},
	"material": {
		dark:  palette{base: "#292d3e", accent: "#89ddff", text: "#a6accd", dim: "#697098", warning: "#ffcb6b", err: "#f07178", green: "#c3e88d"},
		light: palette{base: "#fafafa", accent: "#39adb5", text: "#253244", dim: "#90a4ae", warning: "#f6a434", err: "#e53935", green: "#91b859"},
	},
	"nightfox": {
		dark:  palette{base: "#192330", accent: "#719cd6", text: "#cdcecf", dim: "#71839b", warning: "#f4a261", err: "#e85b7a", green: "#81b29a"},
		light: palette{base: "#f6f2ee", accent: "#2848a9", text: "#3d2b5a", dim: "#7d7d8f", warning: "#955f61", err: "#c94f6d", green: "#396847"},
	},
	"iceberg": {
		dark:  palette{base: "#161821", accent: "#84a0c6", text: "#c6c8d1", dim: "#6b7089", warning: "#e2a478", err: "#e27878", green: "#b4be82"},
		light: palette{base: "#e8e9ec", accent: "#4d7cb9", text: "#33374c", dim: "#6684a3", warning: "#c27e3c", err: "#cc517a", green: "#668e3d"},
	},
	"horizon": {
		dark:  palette{base: "#1c1e26", accent: "#e95678", text: "#d5d8da", dim: "#6c6f93", warning: "#fab795", err: "#ec6a88"},
		light: palette{base: "#fdf0ed", accent: "#e95678", text: "#06060c", dim: "#8a8d9b", warning: "#f9a78e", err: "#e95678"},
	},
}

// ThemeNames lists the selectable theme names in a stable order.
func ThemeNames() []string {
	return []string{
		"default", "catppuccin", "habamax", "gruvbox", "tokyonight",
		"nord", "dracula", "solarized", "rose-pine", "kanagawa", "everforest",
		"one-dark", "ayu", "flexoki", "oxocarbon", "night-owl",
		"github", "monokai", "material", "nightfox", "iceberg", "horizon",
	}
}

// HasDarkBackground reports whether the connected terminal uses a dark
// background. Detection failures fall back to dark.
func HasDarkBackground() bool {
	return lipgloss.HasDarkBackground()
}

// bgOf extracts the background color from a style, or "" when unset. The
// canvas roles (Base, BgUser, BgTool, BgModel) set one; every foreground
// role returns "". AdaptiveColor unsets report NoColor, which is neither a
// hex Color nor a resolvable dark/light pair, so both read as ""
// (terminal default preserved).
func bgOf(s lipgloss.Style) string {
	bg := s.GetBackground()
	if bg == nil {
		return ""
	}
	if c, ok := bg.(lipgloss.Color); ok {
		return string(c)
	}
	if ac, ok := bg.(lipgloss.AdaptiveColor); ok {
		if ac.Light == "" || ac.Dark == "" {
			return ""
		}
		// The background applies per render, not per resolve: the two mixes
		// track the same subtle ratio by construction, so report the tint
		// the terminal would show now. Callers (dialogView dimming, canvas
		// PaintRow fills) sample this at render time.
		if HasDarkBackground() {
			return ac.Dark
		}
		return ac.Light
	}
	return ""
}

// BaseBG reports the theme's canvas background color, or "" when the theme
// leaves the terminal default in place (default family).
func (t Theme) BaseBG() string { return bgOf(t.Base) }

// BgUserBG reports the user band background, or "" when the theme leaves
// the terminal default in place. The default family carries a neutral band
// (defaultBgUser in adaptive.go).
func (t Theme) BgUserBG() string { return bgOf(t.BgUser) }

// BgToolBG reports the tool band background, or "" when the theme leaves
// the terminal default in place (default family).
func (t Theme) BgToolBG() string { return bgOf(t.BgTool) }

// BgModelBG reports the assistant band background, or "" when the theme
// leaves the terminal default in place (default family).
func (t Theme) BgModelBG() string { return bgOf(t.BgModel) }

// BgCodeBG reports the code block background ("" when unset).
func (t Theme) BgCodeBG() string { return bgOf(t.BgCode) }

// BgDiffAddBG reports the added diff line background ("" when unset).
func (t Theme) BgDiffAddBG() string { return bgOf(t.BgDiffAdd) }

// BgDiffRemoveBG reports the removed diff line background ("" when unset).
func (t Theme) BgDiffRemoveBG() string { return bgOf(t.BgDiffRemove) }

// PaintRow pads row to width display cells, then paints the theme canvas
// background behind every span: bg is inserted after each SGR reset so role
// foregrounds survive, and the SGR 48 span covers padding too. Empty bg is
// a plain pad (default family: terminal default preserved).
func PaintRow(row string, width int, bg string) string {
	padded := padCells(row, width)
	if bg == "" {
		return padded
	}
	r, g, b, ok := parseHex(bg)
	if !ok {
		return padded
	}
	open := "\x1b[48;2;" + Itoa(r) + ";" + Itoa(g) + ";" + Itoa(b) + "m"
	reset := "\x1b[0m"
	// Re-apply the canvas bg after every reset so role colors reopen on it.
	painted := strings.ReplaceAll(padded, reset, reset+open)
	return open + painted + reset
}

func padCells(row string, width int) string {
	if w := runewidth.StringWidth(stripCells(row)); w < width {
		return row + strings.Repeat(" ", width-w)
	}
	return row
}

func stripCells(s string) string {
	var b strings.Builder
	escaping := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			escaping = true
		case escaping:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '\\' {
				escaping = false
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseHex(s string) (r, g, b int, ok bool) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	hex := func(c byte) (int, bool) {
		switch {
		case c >= '0' && c <= '9':
			return int(c - '0'), true
		case c >= 'a' && c <= 'f':
			return int(c-'a') + 10, true
		case c >= 'A' && c <= 'F':
			return int(c-'A') + 10, true
		}
		return 0, false
	}
	v := make([]int, 6)
	for i := 0; i < 6; i++ {
		d, good := hex(s[1+i])
		if !good {
			return 0, 0, 0, false
		}
		v[i] = d
	}
	return v[0]*16 + v[1], v[2]*16 + v[3], v[4]*16 + v[5], true
}

func Itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [3]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}

// ParseHex parses "#rrggbb" into 0-255 channels.
func ParseHex(s string) (r, g, b int, ok bool) { return parseHex(s) }

// Named resolves a theme by name carrying both terminal variants: the dark
// argument is advisory/compat only (kept so callers and tests stay stable)
// while each role's AdaptiveColor selects Light/Dark per render from the
// renderer's cached HasDarkBackground.
func Named(name string, dark bool) (Theme, bool) {
	if name == "" || name == "default" {
		return defaultTheme(), true
	}
	family, ok := families[name]
	if !ok {
		return Theme{}, false
	}
	return fromNamedFamily(name, family), true
}

// Resolve picks the theme for a name and terminal: named families carry both
// variants; unknown names fall back to default.
func Resolve(name string, dark bool) Theme {
	theme, ok := Named(name, dark)
	if !ok {
		return defaultTheme()
	}
	return theme
}

// fromFamily builds one Theme whose roles carry both the family's dark and
// light variant via lipgloss.AdaptiveColor. Empty means "terminal default":
// an unset style keeps NoColor rather than an empty color string.
func fromFamily(fam struct{ dark, light palette }) Theme {
	return fromNamedFamily("", fam)
}

// fromNamedFamily builds one Theme as fromFamily does, plus the M3 band
// backgrounds: each mixes the family accent toward the base canvas at the
// M2-tuned ratio (bandUserRatio and friends in adaptive.go), resolved
// through bandBG so a recorded per-family override wins. The code tint mixes
// the accent the same way; the diff tints mix the success green and the
// error color instead. The name threads
// through for the override lookup only; fromFamily's unnamed form keeps
// working for single-variant test palettes (no override ever matches "").
func fromNamedFamily(name string, fam struct{ dark, light palette }) Theme {
	style := func(darkVal, lightVal string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(adaptive(darkVal, lightVal))
	}
	bold := func(darkVal, lightVal string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(adaptive(darkVal, lightVal)).Bold(true)
	}
	// band derives one Bg role background for both variants. Empty on
	// both sides (a half-defined test palette) keeps the terminal
	// default, mirroring adaptive's empty handling.
	tint := func(bandName string, ratio float64, darkFG, lightFG string) lipgloss.Style {
		light := bandBG(bandKey{family: name, band: bandName, light: true},
			mix(lightFG, fam.light.base, ratio))
		dark := bandBG(bandKey{family: name, band: bandName, light: false},
			mix(darkFG, fam.dark.base, ratio))
		if light == "" && dark == "" {
			return lipgloss.NewStyle()
		}
		if light == "" {
			light = dark
		}
		if dark == "" {
			dark = light
		}
		return lipgloss.NewStyle().Background(bandColor(light, dark))
	}
	band := func(bandName string, ratio float64) lipgloss.Style {
		return tint(bandName, ratio, fam.dark.accent, fam.light.accent)
	}
	greenDark, greenLight := successFG(fam.dark, false), successFG(fam.light, true)
	return Theme{
		Base:     lipgloss.NewStyle().Background(adaptive(fam.dark.base, fam.light.base)),
		BgUser:   band("user", bandUserRatio),
		BgTool:   band("tool", bandToolRatio),
		BgModel:  band("model", bandModelRatio),
		Title:    bold(fam.dark.accent, fam.light.accent),
		Selected: selected(fam.dark, fam.light, name),
		Normal:   lipgloss.NewStyle().Foreground(adaptive(fam.dark.text, fam.light.text)),
		Help:     bold(fam.dark.dim, fam.light.dim),
		Border:   style(fam.dark.accent, fam.light.accent),
		Warning:  bold(fam.dark.warning, fam.light.warning),
		Error:    style(fam.dark.err, fam.light.err),
		Muted:    style(fam.dark.dim, fam.light.dim),

		Accent:       style(fam.dark.accent, fam.light.accent),
		Success:      style(greenDark, greenLight),
		BgCode:       band("code", bandCodeRatio),
		BgDiffAdd:    tint("diff-add", bandDiffRatio, greenDark, greenLight),
		BgDiffRemove: tint("diff-remove", bandDiffRatio, fam.dark.err, fam.light.err),
	}
}

// selected builds the Selected role: the accent foreground (cursor rows,
// focused identity) plus a full-row background highlight at
// bandSelectionRatio — the UI-only change that turns every selection
// dialog's cursor row into a tinted band instead of a bare "> " marker.
// An accent-less test palette skips the background (terminal default),
// mirroring band's empty handling; the contrast matrix test still guards
// accent-on-tint legibility.
func selected(dark, light palette, name string) lipgloss.Style {
	lightMix := ""
	if light.accent != "" || light.base != "" {
		lightMix = bandBG(bandKey{family: name, band: "selection", light: true},
			mix(light.accent, light.base, bandSelectionRatio))
	}
	darkMix := ""
	if dark.accent != "" || dark.base != "" {
		darkMix = bandBG(bandKey{family: name, band: "selection", light: false},
			mix(dark.accent, dark.base, bandSelectionRatio))
	}
	st := lipgloss.NewStyle().Foreground(adaptive(dark.accent, light.accent)).Bold(true)
	if lightMix != "" || darkMix != "" {
		st = st.Background(bandColor(lightMix, darkMix))
	}
	return st
}

// fromPalette builds one Theme from a single variant, kept for tests: both
// AdaptiveColor fields carry the same value, so the selected variant always
// matches the source palette regardless of the renderer's detection.
func fromPalette(pal palette) Theme {
	return fromFamily(struct{ dark, light palette }{dark: pal, light: pal})
}
