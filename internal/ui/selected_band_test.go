package ui

// The Selected role carries a full-row background band (specs note: UI-only
// change so every selection dialog's cursor row renders as a highlight):
// derived per family like the conversation bands, both variants present,
// accent-on-band legibility enforced, and the default family keeps its
// plain terminal look.

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

var selectedBandFamilies = []string{
	"catppuccin", "habamax", "gruvbox", "tokyonight", "nord", "dracula",
	"solarized", "rose-pine", "kanagawa", "everforest", "one-dark", "ayu",
	"flexoki", "oxocarbon", "night-owl", "github", "monokai", "material",
	"nightfox", "iceberg", "horizon",
}

func TestSelectedBandCarriesBothVariants(t *testing.T) {
	for _, name := range selectedBandFamilies {
		theme := Resolve(name, true)
		bg, ok := theme.Selected.GetBackground().(lipgloss.AdaptiveColor)
		if !ok {
			t.Fatalf("%s/Selected: background is %T, want AdaptiveColor", name, theme.Selected.GetBackground())
		}
		if bg.Light == "" || bg.Dark == "" {
			t.Fatalf("%s/Selected: AdaptiveColor missing a variant: %+v", name, bg)
		}
		dark := Resolve(name, true)
		light := Resolve(name, false)
		if dark.Selected.GetBackground() != light.Selected.GetBackground() {
			t.Fatalf("%s/Selected: background differs by resolve variant", name)
		}
	}
}

func TestSelectedBandLegible(t *testing.T) {
	for name, fam := range families {
		for _, variant := range []struct {
			label string
			light bool
			pal   palette
		}{
			{"dark", false, fam.dark},
			{"light", true, fam.light},
		} {
			if variant.pal.accent == "" && variant.pal.base == "" {
				continue
			}
			tint := bandBG(
				bandKey{family: name, band: "selection", light: variant.light},
				mix(variant.pal.accent, variant.pal.base, bandSelectionRatio),
			)
			if tint == "" {
				continue
			}
			if !legible(variant.pal.text, tint) {
				t.Errorf("%s/%s: text %q on selection band %q illegible (needs override)",
					name, variant.label, variant.pal.text, tint)
			}
		}
	}
}

func TestSelectedDefaultStaysPlain(t *testing.T) {
	theme := Resolve("default", true)
	bg, isAdaptive := theme.Selected.GetBackground().(lipgloss.AdaptiveColor)
	if isAdaptive {
		t.Fatalf("default family must keep the terminal default, got band %+v", bg)
	}
}
