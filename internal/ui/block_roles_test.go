package ui

// Transcript redesign roles: Accent mirrors Border's color without bold,
// Success comes from the family green (or the standard green through the
// contrast gate), and the default family gains neutral user/code tints while
// its canvas and prose stay on the terminal defaults.

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestAccentMatchesBorderWithoutBold(t *testing.T) {
	for _, name := range ThemeNames() {
		theme := Resolve(name, true)
		if theme.Accent.GetBold() {
			t.Fatalf("%s/Accent: must not be bold", name)
		}
		if name == "default" {
			continue
		}
		if theme.Accent.GetForeground() != theme.Border.GetForeground() {
			t.Fatalf("%s/Accent: fg %v, want Border's %v", name,
				theme.Accent.GetForeground(), theme.Border.GetForeground())
		}
	}
}

func TestSuccessUsesFamilyGreenVerbatim(t *testing.T) {
	for name, fam := range families {
		for _, variant := range []struct {
			label string
			light bool
			pal   palette
		}{
			{"dark", false, fam.dark},
			{"light", true, fam.light},
		} {
			want := variant.pal.green
			if want == "" {
				want = standardGreenDark
				if variant.light {
					want = standardGreenLight
				}
			}
			if got := successFG(variant.pal, variant.light); got != want {
				t.Errorf("%s/%s: success %q, want %q unchanged (gate adjusted a shipped palette)",
					name, variant.label, got, want)
			}
		}
	}
}

func TestSuccessStepsTowardTextWhenIllegible(t *testing.T) {
	// A base equal to the green fails the gate, so the candidate must move
	// toward the text color until it passes.
	p := palette{base: "#5faf5f", text: "#ffffff"}
	got := successFG(p, false)
	if got == standardGreenDark {
		t.Fatalf("illegible green must be adjusted, got %q", got)
	}
	if !legible(got, p.base) {
		t.Fatalf("adjusted green %q must pass the gate on %q", got, p.base)
	}
	// Without a base there is nothing to gate against.
	if got := successFG(palette{}, true); got != standardGreenLight {
		t.Fatalf("base-less palette must keep the standard green, got %q", got)
	}
}

func TestDefaultNeutralTints(t *testing.T) {
	theme := Resolve("default", true)
	for role, style := range map[string]lipgloss.Style{
		"BgUser": theme.BgUser, "BgCode": theme.BgCode,
		"BgDiffAdd": theme.BgDiffAdd, "BgDiffRemove": theme.BgDiffRemove,
	} {
		if _, ok := style.GetBackground().(lipgloss.AdaptiveColor); !ok {
			t.Fatalf("default/%s: background is %T, want AdaptiveColor", role, style.GetBackground())
		}
	}
	for role, style := range map[string]lipgloss.Style{"BgUser": theme.BgUser, "BgCode": theme.BgCode} {
		ac := style.GetBackground().(lipgloss.AdaptiveColor)
		dr, dg, db, _ := hexChannels(ac.Dark)
		lr, lg, lb, _ := hexChannels(ac.Light)
		if dr != dg || dg != db || lr != lg || lg != lb {
			t.Fatalf("default/%s: tint must be neutral gray, got %+v", role, ac)
		}
		cr, _, _, _ := hexChannels(defaultDarkCanvas)
		if dr <= cr {
			t.Fatalf("default/%s: dark tint %q must lean toward white from %q", role, ac.Dark, defaultDarkCanvas)
		}
		if lr >= 0xff {
			t.Fatalf("default/%s: light tint %q must lean toward black", role, ac.Light)
		}
	}
	user := theme.BgUser.GetBackground().(lipgloss.AdaptiveColor)
	if user.Dark != "#2e2e2e" || user.Light != "#f0f0f0" {
		t.Fatalf("default/BgUser: got %+v, want dark #2e2e2e (8%% white) light #f0f0f0 (6%% black)", user)
	}
	// Typical terminal foregrounds (ANSI 7 / black) and the Muted ANSI 8
	// gray stay legible on every default tint.
	for _, tint := range []lipgloss.Style{theme.BgUser, theme.BgCode, theme.BgDiffAdd, theme.BgDiffRemove} {
		ac := tint.GetBackground().(lipgloss.AdaptiveColor)
		for _, pair := range [][2]string{{"#c0c0c0", ac.Dark}, {"#808080", ac.Dark}, {"#000000", ac.Light}, {"#808080", ac.Light}} {
			if !legible(pair[0], pair[1]) {
				t.Fatalf("default: %q on tint %q illegible", pair[0], pair[1])
			}
		}
	}
	if theme.Accent.GetForeground() != lipgloss.Color("12") || theme.Success.GetForeground() != lipgloss.Color("2") {
		t.Fatalf("default Accent/Success must stay on the 16-color palette: %v / %v",
			theme.Accent.GetForeground(), theme.Success.GetForeground())
	}
	if bg := theme.BgUserBG(); bg != "#2e2e2e" && bg != "#f0f0f0" {
		t.Fatalf("default BgUserBG must report the neutral band, got %q", bg)
	}
}

func TestBgAccessorsReportTints(t *testing.T) {
	theme := fromPalette(palette{base: "#1e1e2e", accent: "#cba6f7", text: "#cdd6f4", err: "#f38ba8", green: "#a6e3a1"})
	if got, want := theme.BgCodeBG(), mix("#cba6f7", "#1e1e2e", bandCodeRatio); got != want {
		t.Fatalf("BgCodeBG = %q, want %q", got, want)
	}
	if got, want := theme.BgDiffAddBG(), mix("#a6e3a1", "#1e1e2e", bandDiffRatio); got != want {
		t.Fatalf("BgDiffAddBG = %q, want %q", got, want)
	}
	if got, want := theme.BgDiffRemoveBG(), mix("#f38ba8", "#1e1e2e", bandDiffRatio); got != want {
		t.Fatalf("BgDiffRemoveBG = %q, want %q", got, want)
	}
}
