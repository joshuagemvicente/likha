package ui

import (
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Swatch is one inline color literal found in transcript text: rune offsets
// into the scanned line plus the color normalized to "#rrggbb".
type Swatch struct {
	Start, End int
	Hex        string
}

// FindSwatches scans plain transcript text for CSS color literals — "#rgb",
// "#rgba", "#rrggbb", "#rrggbbaa" (alpha dropped), "rgb()/rgba()" and
// "hsl()/hsla()" in comma or space form with an optional "/ alpha" (ignored)
// — and reports each span with its normalized hex. Matching is
// case-insensitive for digits, function names, and units; out-of-range
// channels clamp, hues wrap. Spans never overlap; scanning resumes after
// each match. A literal split across a wrap boundary may swatch a same-shape
// fragment on one line; text and widths are unaffected either way.
func FindSwatches(line string) []Swatch {
	r := []rune(line)
	var out []Swatch
	for i := 0; i < len(r); {
		switch {
		case r[i] == '#':
			if sp, ok := scanHexSwatch(r, i); ok {
				out = append(out, sp)
				i = sp.End
				continue
			}
			i++
		case r[i] == 'r' || r[i] == 'R' || r[i] == 'h' || r[i] == 'H':
			if sp, ok := scanFuncSwatch(r, i); ok {
				out = append(out, sp)
				i = sp.End
				continue
			}
			i++
		default:
			i++
		}
	}
	return out
}

func isHexDigit(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// isSwatchIdent marks runes that bind a "#" or function name into a longer
// token (identifiers, URL fragments, "##headings"): a literal touching one
// stays plain.
func isSwatchIdent(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '#'
}

func isSwatchTrailer(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func scanHexSwatch(r []rune, i int) (Swatch, bool) {
	if i > 0 && isSwatchIdent(r[i-1]) {
		return Swatch{}, false
	}
	for _, k := range []int{8, 6, 4, 3} {
		if i+1+k > len(r) {
			continue
		}
		ok := true
		for _, c := range r[i+1 : i+1+k] {
			if !isHexDigit(c) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if i+1+k < len(r) && (isHexDigit(r[i+1+k]) || isSwatchTrailer(r[i+1+k])) {
			continue // longer run: longest match or nothing
		}
		return Swatch{Start: i, End: i + 1 + k, Hex: normalizeHex(string(r[i+1 : i+1+k]))}, true
	}
	return Swatch{}, false
}

func normalizeHex(digits string) string {
	s := strings.ToLower(digits)
	switch len(s) {
	case 3:
		return "#" + string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	case 4:
		return "#" + string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	case 8:
		return "#" + s[:6]
	default:
		return "#" + s
	}
}

func matchFoldName(r []rune, i int, name string) bool {
	if i+len(name) > len(r) {
		return false
	}
	for k := 0; k < len(name); k++ {
		c := r[i+k]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != rune(name[k]) {
			return false
		}
	}
	return true
}

func scanFuncSwatch(r []rune, i int) (Swatch, bool) {
	if i > 0 && isSwatchIdent(r[i-1]) {
		return Swatch{}, false
	}
	var isHSL bool
	n := 0
	switch {
	case matchFoldName(r, i, "rgba"):
		n = 4
	case matchFoldName(r, i, "hsla"):
		n, isHSL = 4, true
	case matchFoldName(r, i, "rgb"):
		n = 3
	case matchFoldName(r, i, "hsl"):
		n, isHSL = 3, true
	default:
		return Swatch{}, false
	}
	j := i + n
	for j < len(r) && (r[j] == ' ' || r[j] == '\t') {
		j++
	}
	if j >= len(r) || r[j] != '(' {
		return Swatch{}, false
	}
	j++
	var vals []float64
	var units []string
	end := -1
	for {
		for j < len(r) && (r[j] == ' ' || r[j] == '\t' || r[j] == ',') {
			j++
		}
		if j >= len(r) {
			return Swatch{}, false
		}
		if r[j] == ')' {
			end = j + 1
			break
		}
		if r[j] == '/' {
			// Alpha: ignored for the swatch; skip to the close, bounded.
			k := j + 1
			for k < len(r) && r[k] != ')' && k-j <= 32 {
				k++
			}
			if k >= len(r) || r[k] != ')' {
				return Swatch{}, false
			}
			end = k + 1
			break
		}
		if len(vals) == 4 {
			return Swatch{}, false
		}
		v, u, nj, ok := parseSwatchNumber(r, j)
		if !ok {
			return Swatch{}, false
		}
		vals = append(vals, v)
		units = append(units, u)
		j = nj
	}
	if len(vals) < 3 || end-i > 64 {
		return Swatch{}, false
	}
	if end < len(r) && isSwatchIdent(r[end]) {
		return Swatch{}, false
	}
	var hex string
	if isHSL {
		hex = hslSwatchHex(vals, units)
	} else {
		hex = rgbSwatchHex(vals, units)
	}
	if hex == "" {
		return Swatch{}, false
	}
	return Swatch{Start: i, End: end, Hex: hex}, true
}

func parseSwatchNumber(r []rune, j int) (float64, string, int, bool) {
	k := j
	if k < len(r) && (r[k] == '+' || r[k] == '-') {
		k++
	}
	digits := 0
	dot := false
	for k < len(r) && (r[k] >= '0' && r[k] <= '9' || r[k] == '.') {
		if r[k] == '.' {
			if dot {
				break
			}
			dot = true
		} else {
			digits++
		}
		k++
	}
	if digits == 0 {
		return 0, "", j, false
	}
	v, err := strconv.ParseFloat(string(r[j:k]), 64)
	if err != nil {
		return 0, "", j, false
	}
	u := k
	for u < len(r) && (r[u] == '%' || r[u] >= 'a' && r[u] <= 'z' || r[u] >= 'A' && r[u] <= 'Z') {
		u++
	}
	return v, strings.ToLower(string(r[k:u])), u, true
}

func clampRound(v, lo, hi float64) int {
	return int(math.Round(min(max(v, lo), hi)))
}

func rgbSwatchHex(vals []float64, units []string) string {
	var ch [3]int
	for c := range 3 {
		v := vals[c]
		switch units[c] {
		case "%":
			v = v * 255 / 100
		case "":
		default:
			return ""
		}
		ch[c] = clampRound(v, 0, 255)
	}
	return "#" + hexByte(ch[0]) + hexByte(ch[1]) + hexByte(ch[2])
}

func hslSwatchHex(vals []float64, units []string) string {
	h := vals[0]
	switch units[0] {
	case "", "deg":
	case "rad":
		h = h * 180 / math.Pi
	case "grad":
		h *= 0.9
	case "turn":
		h *= 360
	default:
		return ""
	}
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	// Bare s/l numbers read as percentages (lenient); anything else fails.
	if units[1] != "" && units[1] != "%" || units[2] != "" && units[2] != "%" {
		return ""
	}
	s := min(max(vals[1], 0), 100)
	l := min(max(vals[2], 0), 100)
	r, g, b := hslToRGB(h, s, l)
	return "#" + hexByte(r) + hexByte(g) + hexByte(b)
}

func hslToRGB(h, s, l float64) (int, int, int) {
	s /= 100
	l /= 100
	c := (1 - math.Abs(2*l-1)) * s
	hp := h / 60
	x := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var r1, g1, b1 float64
	switch int(hp) {
	case 0:
		r1, g1, b1 = c, x, 0
	case 1:
		r1, g1, b1 = x, c, 0
	case 2:
		r1, g1, b1 = 0, c, x
	case 3:
		r1, g1, b1 = 0, x, c
	case 4:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}
	m := l - c/2
	return clampRound((r1+m)*255, 0, 255), clampRound((g1+m)*255, 0, 255), clampRound((b1+m)*255, 0, 255)
}

// SwatchStyle renders a color literal with the literal as its background and
// a contrasting foreground, so the token stays readable on any canvas. Under
// limited color profiles lipgloss degrades (or drops, under no-color) the
// swatch by itself — the token text is never altered.
func SwatchStyle(hex string) lipgloss.Style {
	return lipgloss.NewStyle().Background(lipgloss.Color(hex)).Foreground(lipgloss.Color(contrastFG(hex)))
}

func contrastFG(hex string) string {
	r, g, b, ok := hexChannels(hex)
	if !ok {
		return "#000000"
	}
	if luminance(r, g, b) > 0.179 {
		return "#000000"
	}
	return "#ffffff"
}
