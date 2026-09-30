// Package ui holds Lisa's color themes and the optional Nerd Font glyph set.
// Themes map onto seven style roles and never color conversation prose beyond
// error entries; Nerd Font icons are strictly opt-in so plain-text output
// remains complete and legible without patched fonts.
package ui

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Title    lipgloss.Style // headings, header identity
	Selected lipgloss.Style // cursor rows, focused identity
	Normal   lipgloss.Style // conversation prose (uncolored by default)
	Help     lipgloss.Style // status line hints
	Border   lipgloss.Style // rule lines
	Warning  lipgloss.Style // pending review markers
	Error    lipgloss.Style // error entries
	Muted    lipgloss.Style // thinking-model reasoning output
}

// defaultTheme keeps Lisa's original look: a single calm accent on a plain
// terminal palette.
func defaultTheme() Theme {
	return Theme{
		Title:    lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true),
		Selected: lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true),
		Normal:   lipgloss.NewStyle(),
		Help:     lipgloss.NewStyle().Bold(true),
		Border:   lipgloss.NewStyle(),
		Warning:  lipgloss.NewStyle().Bold(true),
		Error:    lipgloss.NewStyle(),
		Muted:    lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
}

// palette is the raw color set a theme family defines for a light or dark
// terminal. Empty entries inherit the terminal defaults.
type palette struct {
	accent  string // focus, headings, cursor
	text    string // optional main text tint ("" = terminal default)
	dim     string // hints
	warning string // pending review
	err     string // errors
}

// themeFamily maps a family name to its light and dark palettes; families
// with a single variant repeat it in both slots.
var families = map[string]struct{ dark, light palette }{
	"catppuccin": {
		dark:  palette{"#cba6f7", "#cdd6f4", "#6c7086", "#f9e2af", "#f38ba8"},
		light: palette{"#8839ef", "#4c4f69", "#8c8fa1", "#df8e1d", "#d20f39"},
	},
	"habamax": {
		dark:  palette{"#5f87d7", "#bcbcbc", "#7c7c6c", "#d7af5f", "#ff8787"},
		light: palette{"#005f87", "#3a3a3a", "#8a8a8a", "#af8700", "#d75f5f"},
	},
	"gruvbox": {
		dark:  palette{"#fabd2f", "#ebdbb2", "#928374", "#fe8019", "#fb4934"},
		light: palette{"#b57614", "#3c3836", "#7c6f64", "#d65d0e", "#9d0006"},
	},
	"tokyonight": {
		dark:  palette{"#7aa2f7", "#c0caf5", "#565f89", "#e0af68", "#f7768e"},
		light: palette{"#2e7de9", "#3760bf", "#8990b3", "#b15c00", "#c64343"},
	},
	"nord": {
		dark:  palette{"#88c0d0", "#d8dee9", "#616e88", "#ebcb8b", "#bf616a"},
		light: palette{"#5e81ac", "#2e3440", "#8f98b3", "#b58900", "#bf616a"},
	},
	"dracula": {
		dark:  palette{"#bd93f9", "#f8f8f2", "#6272a4", "#f1fa8c", "#ff5555"},
		light: palette{"#6c3fc5", "#282a36", "#6272a4", "#b58900", "#d6262e"},
	},
	"solarized": {
		dark:  palette{"#268bd2", "#eee8d5", "#586e75", "#b58900", "#dc322f"},
		light: palette{"#268bd2", "#073642", "#93a1a1", "#b58900", "#dc322f"},
	},
	"rose-pine": {
		dark:  palette{"#c4a7e7", "#e0def4", "#90819c", "#f6c177", "#eb6f92"},
		light: palette{"#907aa9", "#575279", "#9893a5", "#ea9d34", "#bf616a"},
	},
	"kanagawa": {
		dark:  palette{"#7e9cd8", "#dcd7ba", "#727169", "#ffa066", "#e46876"},
		light: palette{"#2d4f67", "#43436c", "#8a8980", "#c4791b", "#c34043"},
	},
	"everforest": {
		dark:  palette{"#a7c080", "#d3c6aa", "#9c9c8c", "#dbbc7f", "#e67e80"},
		light: palette{"#829e57", "#5c6370", "#a6b0a0", "#bf9f40", "#d2554f"},
	},
}

// ThemeNames lists the selectable theme names in a stable order.
func ThemeNames() []string {
	return []string{
		"default", "catppuccin", "habamax", "gruvbox", "tokyonight",
		"nord", "dracula", "solarized", "rose-pine", "kanagawa", "everforest",
	}
}

// HasDarkBackground reports whether the connected terminal uses a dark
// background. Detection failures fall back to dark.
func HasDarkBackground() bool {
	return lipgloss.HasDarkBackground()
}

// Named resolves a theme by name for the detected terminal variant. An
// unknown name yields false; the caller falls back to default.
func Named(name string, dark bool) (Theme, bool) {
	if name == "" || name == "default" {
		return defaultTheme(), true
	}
	family, ok := families[name]
	if !ok {
		return Theme{}, false
	}
	pal := family.dark
	if !dark {
		pal = family.light
	}
	return fromPalette(pal), true
}

// Resolve picks the theme for a name and terminal: named families use their
// variant; unknown names fall back to default.
func Resolve(name string, dark bool) Theme {
	theme, ok := Named(name, dark)
	if !ok {
		return defaultTheme()
	}
	return theme
}

func fromPalette(pal palette) Theme {
	return Theme{
		Title:    lipgloss.NewStyle().Foreground(lipgloss.Color(pal.accent)).Bold(true),
		Selected: lipgloss.NewStyle().Foreground(lipgloss.Color(pal.accent)).Bold(true),
		Normal: func() lipgloss.Style {
			if pal.text == "" {
				return lipgloss.NewStyle()
			}
			return lipgloss.NewStyle().Foreground(lipgloss.Color(pal.text))
		}(),
		Help:    lipgloss.NewStyle().Foreground(lipgloss.Color(pal.dim)).Bold(true),
		Border:  lipgloss.NewStyle().Foreground(lipgloss.Color(pal.accent)),
		Warning: lipgloss.NewStyle().Foreground(lipgloss.Color(pal.warning)).Bold(true),
		Error:   lipgloss.NewStyle().Foreground(lipgloss.Color(pal.err)),
		Muted:   lipgloss.NewStyle().Foreground(lipgloss.Color(pal.dim)),
	}
}
