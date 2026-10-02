// Adaptive color helpers: derived band tints and a contrast gate.
//
// Every Theme role carries both terminal variants in one
// lipgloss.AdaptiveColor; lipgloss selects Light/Dark per render from the
// renderer's cached HasDarkBackground, so family switching stays a Resolve
// call while light/dark adaptation moved from resolve time to render time.
// lipgloss auto-degrades hex through ANSI256 to ANSI, so no per-profile
// values are needed.
package ui

import (
	"math"

	"github.com/charmbracelet/lipgloss"
)

// Band tint ratios: share of the family accent mixed toward the base canvas
// (mix fg=accent, bg=canvas) for each M3 background band. Tuned 2026-10-01
// against the contrast gate below, starting from the spec's 8-12% window:
//
//	user  10% — Normal-on-band minimum 4.08 (tokyonight light)
//	tool  10% — same pairing; activity and result share the band
//	model  8% — assistant prose fills the most rows, so the quietest tint
//	selection 12% — dialog cursor rows: a full-row highlight must stay
//	visible next to conversation bands while tracking the same
//	text-on-band contract the matrix test enforces.
//
// Bands are subtle by design: text-on-band contrast tracks text-on-base
// (family minimum 4.52, tokyonight light), and every derived tint passes the
// gate with wide margin, so bandOverrides below stays empty.
const (
	bandUserRatio  = 0.10
	bandToolRatio  = 0.10
	bandModelRatio = 0.08

	// bandSelectionRatio backs the Selected role's background: the dialog
	// cursor row renders as a full-row highlight band, slightly stronger
	// than the conversation bands so selection stays obvious next to
	// them.
	bandSelectionRatio = 0.12
)

// bandKey names one band background: family, band ("user", "tool", "model")
// and variant (light true = light terminal).
type bandKey struct {
	family string
	band   string
	light  bool
}

// bandOverrides replaces a derived band tint where it fails the contrast
// gate; the value is the hand-picked hex. Empty today — every family ×
// variant × band tint passes legible (see the matrix test), so no override
// is recorded.
//
// A failing pair must add its override here with a comment naming the pair
// and its measured ratio; the matrix test fails the build otherwise, never
// a silent skip.
var bandOverrides = map[bandKey]string{}

// bandBG resolves a band background: a recorded override wins, else the
// derived tint stands.
func bandBG(key bandKey, derived string) string {
	if alt, ok := bandOverrides[key]; ok {
		return alt
	}
	return derived
}

// bandColor carries one band background for both terminal variants: M3
// builds each Bg role as bandColor(mix(lightAccent, lightBase, ratio),
// mix(darkAccent, darkBase, ratio)) so the band re-tints per render.
func bandColor(lightMix, darkMix string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: lightMix, Dark: darkMix}
}

// adaptive builds one AdaptiveColor from a dark/light palette pair. Empty
// means "terminal default": both empty leaves the style unset (NoColor),
// one empty reuses the other for both variants so a half-defined pair never
// renders an empty color string.
func adaptive(darkVal, lightVal string) lipgloss.TerminalColor {
	if darkVal == "" {
		darkVal = lightVal
	}
	if lightVal == "" {
		lightVal = darkVal
	}
	if darkVal == "" {
		return lipgloss.NoColor{}
	}
	return lipgloss.AdaptiveColor{Light: lightVal, Dark: darkVal}
}

// mix interpolates fg toward bg in hex space: ratio 0 yields bg, 1 yields
// fg (white-on-black midpoint is "#808080"). Both "#rrggbb" and short
// "#rgb", either case, are accepted. Invalid input returns bg unchanged —
// never a panic, never a fabricated hue. Out-of-range ratios clamp to the
// endpoints; NaN returns bg.
func mix(fg, bg string, ratio float64) string {
	fr, fgg, fb, okFG := hexChannels(fg)
	br, bgg, bb, okBG := hexChannels(bg)
	if !okFG || !okBG || math.IsNaN(ratio) {
		return bg
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	rest := 1 - ratio
	return "#" + hexByte(int(math.Round(float64(fr)*ratio+float64(br)*rest))) +
		hexByte(int(math.Round(float64(fgg)*ratio+float64(bgg)*rest))) +
		hexByte(int(math.Round(float64(fb)*ratio+float64(bb)*rest)))
}

// legibleThreshold is the minimum WCAG-style contrast ratio for a foreground
// on its background. It is deliberately far below WCAG AA (4.5:1): several
// accent/dim/warning roles sit in the 1.7-3.5 range on their own canvas by
// design (measured matrix minimum 1.72, horizon light warning; lightest
// accent 2.04, ayu light), and the 8-10% band tints barely move text
// contrast, so a 4.5 floor would reject the shipped palettes. The gate
// guards against degenerate near-identical pairings (ratio ~1.0-1.1), not AA
// certification: body text (Normal on base) holds >= 4.5 by palette
// construction (minimum 4.52, tokyonight light) and >= 4.0 on any derived
// band (minimum 4.08, tokyonight light at 10%).
const legibleThreshold = 1.5

// legible reports whether fg on bg meets the contrast floor. Invalid input
// is illegible (false), never an error.
func legible(fg, bg string) bool {
	fr, fgg, fb, okFG := hexChannels(fg)
	br, bgg, bb, okBG := hexChannels(bg)
	if !okFG || !okBG {
		return false
	}
	l1 := luminance(fr, fgg, fb)
	l2 := luminance(br, bgg, bb)
	hi, lo := l1, l2
	if lo > hi {
		hi, lo = lo, hi
	}
	return (hi+0.05)/(lo+0.05) >= legibleThreshold
}

// luminance is the WCAG relative luminance of an sRGB triple.
func luminance(r, g, b uint32) float64 {
	lin := func(c uint32) float64 {
		v := float64(c) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// hexChannels parses "#rrggbb" or short "#rgb" (either case) into channels.
func hexChannels(s string) (r, g, b uint32, ok bool) {
	var d [6]byte
	switch {
	case len(s) == 7 && s[0] == '#':
		copy(d[:], s[1:])
	case len(s) == 4 && s[0] == '#':
		d[0], d[1] = s[1], s[1]
		d[2], d[3] = s[2], s[2]
		d[4], d[5] = s[3], s[3]
	default:
		return 0, 0, 0, false
	}
	v := func(c byte) (uint32, bool) {
		switch {
		case c >= '0' && c <= '9':
			return uint32(c - '0'), true
		case c >= 'a' && c <= 'f':
			return uint32(c-'a') + 10, true
		case c >= 'A' && c <= 'F':
			return uint32(c-'A') + 10, true
		}
		return 0, false
	}
	var n [6]uint32
	for i := range 6 {
		x, good := v(d[i])
		if !good {
			return 0, 0, 0, false
		}
		n[i] = x
	}
	return n[0]*16 + n[1], n[2]*16 + n[3], n[4]*16 + n[5], true
}

const hexDigits = "0123456789abcdef"

// hexByte renders one channel clamped to two lowercase hex digits. mix only
// feeds it 0-255 convex combinations, so the clamp never fires; it exists so
// a future caller cannot fabricate an out-of-range channel.
func hexByte(v int) string {
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return string([]byte{hexDigits[v>>4], hexDigits[v&0xf]})
}
