package ui

import (
	"strings"
	"testing"
)

func TestFindSwatchesHex(t *testing.T) {
	cases := []struct {
		line string
		want []Swatch
	}{
		{"use #4493f8 now", []Swatch{{Start: 4, End: 11, Hex: "#4493f8"}}},
		{"#ABC ok", []Swatch{{Start: 0, End: 4, Hex: "#aabbcc"}}},
		{"#abcd drops alpha", []Swatch{{Start: 0, End: 5, Hex: "#aabbcc"}}},
		{"#11223344 drops alpha", []Swatch{{Start: 0, End: 9, Hex: "#112233"}}},
		{"no swatch here", nil},
		{"#12 too short", nil},
		{"#12345 five digits", nil},
		{"issue #4493f8x stays plain", nil},
		{"##4493f8 stays plain", nil},
		{"see https://x/#frag stays plain", nil},
		{"#4493F8 and #ff0000 both", []Swatch{{Start: 0, End: 7, Hex: "#4493f8"}, {Start: 12, End: 19, Hex: "#ff0000"}}},
	}
	for _, tc := range cases {
		got := FindSwatches(tc.line)
		if len(got) != len(tc.want) {
			t.Fatalf("line %q: got %v, want %v", tc.line, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("line %q span %d: got %+v, want %+v", tc.line, i, got[i], tc.want[i])
			}
		}
		if len(got) > 0 {
			for _, sp := range got {
				if got := string([]rune(tc.line)[sp.Start:sp.End]); !strings.HasPrefix(strings.ToLower(got), "#") {
					t.Fatalf("line %q: span %+v misaligned", tc.line, sp)
				}
			}
		}
	}
}

func TestFindSwatchesFunc(t *testing.T) {
	cases := []struct {
		line string
		hex  []string
	}{
		{"rgb(68, 147, 248)", []string{"#4493f8"}},
		{"RGB(100%, 0%, 0%)", []string{"#ff0000"}},
		{"rgba(68,147,248,0.5) alpha ignored", []string{"#4493f8"}},
		{"rgb(68 147 248) spaces", []string{"#4493f8"}},
		{"hsl(210, 80%, 60%)", []string{"#4799eb"}},
		{"HSL(0 100% 50%)", []string{"#ff0000"}},
		{"hsl(120 100% 25% / 0.4)", []string{"#008000"}},
		{"rgb(300, -5, 999) clamps", []string{"#ff00ff"}},
		{"hsl(720, 100%, 50%) wraps", []string{"#ff0000"}},
		{"rgb(68, 147) too few", nil},
		{"rgb(68, 147, banana) bad token", nil},
		{"rgb(1,2,3,4,5) too many", nil},
		{"myrgb(1,2,3) glued name", nil},
		{"rgb(08,147,248)—trailing dash swatches the call", []string{"#0893f8"}},
		{"rgb(08, 147, 248) commas still swatch", []string{"#0893f8"}},
	}
	for _, tc := range cases {
		got := FindSwatches(tc.line)
		if len(got) != len(tc.hex) {
			t.Fatalf("line %q: got %v, want %v", tc.line, got, tc.hex)
		}
		for i, want := range tc.hex {
			if got[i].Hex != want {
				t.Fatalf("line %q span %d: got %q, want %q", tc.line, i, got[i].Hex, want)
			}
		}
	}
}

func TestSwatchStyleContrast(t *testing.T) {
	if got := contrastFG("#4493f8"); got != "#000000" {
		t.Fatalf("blue swatch fg = %q, want black text", got)
	}
	if got := contrastFG("#011627"); got != "#ffffff" {
		t.Fatalf("navy swatch fg = %q, want white text", got)
	}
}
