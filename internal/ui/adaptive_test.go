package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestMixEndpoints(t *testing.T) {
	if got := mix("#cba6f7", "#1e1e2e", 0); got != "#1e1e2e" {
		t.Fatalf("ratio 0 must yield bg, got %q", got)
	}
	if got := mix("#cba6f7", "#1e1e2e", 1); got != "#cba6f7" {
		t.Fatalf("ratio 1 must yield fg, got %q", got)
	}
	if got := mix("#ffffff", "#000000", 0.5); got != "#808080" {
		t.Fatalf("white/black midpoint must be #808080, got %q", got)
	}
	if got := mix("#cba6f7", "#1e1e2e", 0.1); got != "#2f2c42" {
		t.Fatalf("10%% accent-into-base tint wrong, got %q", got)
	}
}

func TestMixShortHexAndCase(t *testing.T) {
	if got := mix("#fff", "#000000", 1); got != "#ffffff" {
		t.Fatalf("short #rgb must expand, got %q", got)
	}
	if got := mix("#CBA6F7", "#1E1E2E", 1); got != "#cba6f7" {
		t.Fatalf("output must be lowercase hex, got %q", got)
	}
}

func TestMixClampsAndRejects(t *testing.T) {
	if got := mix("#ff0000", "#000000", -0.5); got != "#000000" {
		t.Fatalf("negative ratio must clamp to bg, got %q", got)
	}
	if got := mix("#ff0000", "#000000", 1.5); got != "#ff0000" {
		t.Fatalf("ratio > 1 must clamp to fg, got %q", got)
	}
	// Invalid input returns bg (never a panic, never a fabricated hue).
	if got := mix("nope", "#1e1e2e", 0.5); got != "#1e1e2e" {
		t.Fatalf("bad fg must return bg, got %q", got)
	}
	if got := mix("#cba6f7", "nope", 0.5); got != "nope" {
		t.Fatalf("bad bg must return bg unchanged, got %q", got)
	}
	if got := mix("#cba6f7", "#1e1e2", 0.5); got != "#1e1e2" {
		t.Fatalf("short bg must return bg unchanged, got %q", got)
	}
}

func TestLegibleAccentsOnBase(t *testing.T) {
	for name, fam := range families {
		for _, variant := range []struct {
			label string
			pal   palette
		}{
			{"dark", fam.dark},
			{"light", fam.light},
		} {
			if !legible(variant.pal.accent, variant.pal.base) {
				t.Errorf("%s/%s: accent %q on base %q rejected",
					name, variant.label, variant.pal.accent, variant.pal.base)
			}
		}
	}
}

func TestLegibleRejects(t *testing.T) {
	near := [][2]string{
		{"#ffffff", "#fefefe"},
		{"#000000", "#111111"},
		{"#1c1c1c", "#1c1c1c"},
		{"#268bd2", "#268bd2"},
	}
	for _, pair := range near {
		if legible(pair[0], pair[1]) {
			t.Errorf("near-identical pair %q on %q must be illegible", pair[0], pair[1])
		}
		if legible("", pair[1]) || legible(pair[0], "") || legible("nope", pair[1]) {
			t.Errorf("invalid input must be illegible (pair %q/%q)", pair[0], pair[1])
		}
	}
}

// TestContrastMatrix runs the gate over every family x light/dark x
// role-fg-on-its-bg, plus Normal text on each derived band tint. Any failure
// fails the build: the fix is a named bandOverrides entry, never a skip.
func TestContrastMatrix(t *testing.T) {
	slots := []struct {
		role string
		fg   func(p palette) string
	}{
		{"accent", func(p palette) string { return p.accent }},
		{"text", func(p palette) string { return p.text }},
		{"dim", func(p palette) string { return p.dim }},
		{"warning", func(p palette) string { return p.warning }},
		{"err", func(p palette) string { return p.err }},
	}
	for name, fam := range families {
		for _, variant := range []struct {
			label string
			pal   palette
		}{
			{"dark", fam.dark},
			{"light", fam.light},
		} {
			for _, s := range slots {
				fg := s.fg(variant.pal)
				if !legible(fg, variant.pal.base) {
					t.Errorf("%s/%s: %s %q on base %q illegible (needs override)",
						name, variant.label, s.role, fg, variant.pal.base)
				}
			}
		}
	}

	bands := []struct {
		key   string
		ratio float64
	}{
		{"user", bandUserRatio},
		{"tool", bandToolRatio},
		{"model", bandModelRatio},
	}
	for name, fam := range families {
		for _, variant := range []struct {
			label string
			light bool
			pal   palette
		}{
			{"dark", false, fam.dark},
			{"light", true, fam.light},
		} {
			for _, b := range bands {
				tint := bandBG(
					bandKey{family: name, band: b.key, light: variant.light},
					mix(variant.pal.accent, variant.pal.base, b.ratio),
				)
				if !legible(variant.pal.text, tint) {
					t.Errorf("%s/%s: text %q on %s band %q (ratio %.2f) illegible (needs override)",
						name, variant.label, variant.pal.text, b.key, tint, b.ratio)
				}
			}
		}
	}

	for key := range bandOverrides {
		if _, ok := families[key.family]; !ok {
			t.Errorf("override names unknown family %q", key.family)
		}
		switch key.band {
		case "user", "tool", "model":
		default:
			t.Errorf("override names unknown band %q", key.band)
		}
	}
}

// TestRolesCarryBothVariants asserts every role style carries an
// AdaptiveColor with both fields set, matching the family's light/dark
// palette slots; Light and Dark differ wherever the family defines two
// variants.
func TestRolesCarryBothVariants(t *testing.T) {
	fgRoles := map[string]func(Theme) lipgloss.Style{
		"Title":    func(th Theme) lipgloss.Style { return th.Title },
		"Selected": func(th Theme) lipgloss.Style { return th.Selected },
		"Normal":   func(th Theme) lipgloss.Style { return th.Normal },
		"Help":     func(th Theme) lipgloss.Style { return th.Help },
		"Border":   func(th Theme) lipgloss.Style { return th.Border },
		"Warning":  func(th Theme) lipgloss.Style { return th.Warning },
		"Error":    func(th Theme) lipgloss.Style { return th.Error },
		"Muted":    func(th Theme) lipgloss.Style { return th.Muted },
	}
	slots := map[string]func(p palette) string{
		"Title":    func(p palette) string { return p.accent },
		"Selected": func(p palette) string { return p.accent },
		"Normal":   func(p palette) string { return p.text },
		"Help":     func(p palette) string { return p.dim },
		"Border":   func(p palette) string { return p.accent },
		"Warning":  func(p palette) string { return p.warning },
		"Error":    func(p palette) string { return p.err },
		"Muted":    func(p palette) string { return p.dim },
	}
	for _, name := range ThemeNames() {
		if name == "default" {
			continue
		}
		fam := families[name]
		theme := Resolve(name, true)
		for role, get := range fgRoles {
			ac, ok := get(theme).GetForeground().(lipgloss.AdaptiveColor)
			if !ok {
				t.Fatalf("%s/%s: foreground is %T, want AdaptiveColor", name, role, get(theme).GetForeground())
			}
			if ac.Light == "" || ac.Dark == "" {
				t.Fatalf("%s/%s: AdaptiveColor missing variant: %+v", name, role, ac)
			}
			wantDark, wantLight := slots[role](fam.dark), slots[role](fam.light)
			if !strings.EqualFold(ac.Dark, wantDark) || !strings.EqualFold(ac.Light, wantLight) {
				t.Fatalf("%s/%s: got %+v, want dark %q light %q", name, role, ac, wantDark, wantLight)
			}
			if !strings.EqualFold(wantDark, wantLight) && strings.EqualFold(ac.Light, ac.Dark) {
				t.Fatalf("%s/%s: Light == Dark but family defines two variants", name, role)
			}
		}
		bg, ok := theme.Base.GetBackground().(lipgloss.AdaptiveColor)
		if !ok {
			t.Fatalf("%s/Base: background is %T, want AdaptiveColor", name, theme.Base.GetBackground())
		}
		if bg.Light == "" || bg.Dark == "" {
			t.Fatalf("%s/Base: AdaptiveColor missing variant: %+v", name, bg)
		}
		if !strings.EqualFold(bg.Dark, fam.dark.base) || !strings.EqualFold(bg.Light, fam.light.base) {
			t.Fatalf("%s/Base: got %+v, want dark %q light %q", name, bg, fam.dark.base, fam.light.base)
		}
	}
}

// TestResolveVariantArgAdvisory proves the dark argument no longer selects a
// baked variant: both resolves carry the same dual-variant styles while
// signatures and call sites stay unchanged.
func TestResolveVariantArgAdvisory(t *testing.T) {
	for _, name := range ThemeNames() {
		if name == "default" {
			continue
		}
		dark, light := Resolve(name, true), Resolve(name, false)
		if !reflect.DeepEqual(dark.Title.GetForeground(), light.Title.GetForeground()) {
			t.Fatalf("%s: Title differs by resolve variant: %v vs %v",
				name, dark.Title.GetForeground(), light.Title.GetForeground())
		}
		if !reflect.DeepEqual(dark.Base.GetBackground(), light.Base.GetBackground()) {
			t.Fatalf("%s: Base bg differs by resolve variant", name)
		}
	}
}

// TestDefaultStaysTerminalDefault pins the fallback: today's plain look is
// untouched — Base carries no background, Normal no foreground.
func TestDefaultStaysTerminalDefault(t *testing.T) {
	theme := Resolve("default", true)
	if bg := theme.BaseBG(); bg != "" {
		t.Fatalf("default BaseBG must be empty, got %q", bg)
	}
	if _, isNoColor := theme.Normal.GetForeground().(lipgloss.NoColor); !isNoColor {
		t.Fatalf("default Normal must stay unset, got %v", theme.Normal.GetForeground())
	}
}

func TestFromPaletteCarriesBoth(t *testing.T) {
	theme := fromPalette(palette{base: "#1e1e2e", accent: "#cba6f7", text: "#cdd6f4"})
	ac, ok := theme.Title.GetForeground().(lipgloss.AdaptiveColor)
	if !ok {
		t.Fatalf("fromPalette Title is %T, want AdaptiveColor", theme.Title.GetForeground())
	}
	if ac.Light != "#cba6f7" || ac.Dark != "#cba6f7" {
		t.Fatalf("fromPalette must mirror the single variant in both fields: %+v", ac)
	}
}

func TestBandHelpers(t *testing.T) {
	ac := bandColor("#2f2c42", "#d8d3e8")
	if ac.Light != "#2f2c42" || ac.Dark != "#d8d3e8" {
		t.Fatalf("bandColor must carry light/dark tints: %+v", ac)
	}
	key := bandKey{family: "test-family", band: "user"}
	if got := bandBG(key, "#abcdef"); got != "#abcdef" {
		t.Fatalf("bandBG without override must return the derived tint, got %q", got)
	}
	bandOverrides[key] = "#123456"
	defer delete(bandOverrides, key)
	if got := bandBG(key, "#abcdef"); got != "#123456" {
		t.Fatalf("bandBG override must win, got %q", got)
	}
}
