package transcript

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

func TestEncodeTurnStableShape(t *testing.T) {
	got := EncodeTurn(TurnInfo{Model: "nexum-router", Duration: 12300 * time.Millisecond, Tools: 3, Cancelled: true})
	want := `{"v":1,"model":"nexum-router","ms":12300,"tools":3,"cancelled":true}`
	if got != want {
		t.Fatalf("EncodeTurn = %s, want %s", got, want)
	}
}

func TestEncodeTurnClampsNegativesAndRoundsMillis(t *testing.T) {
	got := EncodeTurn(TurnInfo{Model: "m", Duration: -time.Second, Tools: -2})
	if want := `{"v":1,"model":"m","ms":0,"tools":0,"cancelled":false}`; got != want {
		t.Fatalf("EncodeTurn = %s, want %s", got, want)
	}
	got = EncodeTurn(TurnInfo{Model: "m", Duration: 1500*time.Microsecond + 1})
	if !strings.Contains(got, `"ms":2`) {
		t.Fatalf("EncodeTurn did not round 1.5ms up: %s", got)
	}
}

func TestTurnRoundTrip(t *testing.T) {
	cases := []TurnInfo{
		{Model: "nexum-router", Duration: 12300 * time.Millisecond, Tools: 3},
		{Model: "gpt-5.1", Duration: 64 * time.Second, Tools: 1, Cancelled: true},
		{Model: "", Duration: 0, Tools: 0},
		{Model: `quote"back\slash<tag>&`, Duration: 2 * time.Hour, Tools: 999},
		{Model: "模型-ñ", Duration: 7 * time.Millisecond},
	}
	for _, want := range cases {
		got, ok := DecodeTurn(EncodeTurn(want))
		if !ok {
			t.Fatalf("DecodeTurn(EncodeTurn(%+v)) not ok", want)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip = %+v, want %+v", got, want)
		}
	}
}

func TestTurnThoughtsRoundTrip(t *testing.T) {
	want := TurnInfo{Model: "m", Duration: 9 * time.Second, Tools: 2, Thoughts: []time.Duration{4200 * time.Millisecond, 0, 90 * time.Second}}
	enc := EncodeTurn(want)
	if !strings.Contains(enc, `"thoughts_ms":[4200,0,90000]`) {
		t.Fatalf("EncodeTurn = %s, want thoughts_ms in order", enc)
	}
	got, ok := DecodeTurn(enc)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v (ok %t), want %+v", got, ok, want)
	}
	// Negative and sub-millisecond values store clamped and rounded.
	enc = EncodeTurn(TurnInfo{Thoughts: []time.Duration{-time.Second, 1500 * time.Microsecond}})
	if !strings.Contains(enc, `"thoughts_ms":[0,2]`) {
		t.Fatalf("EncodeTurn = %s, want clamped/rounded thoughts", enc)
	}
	// A turn without reasoning keeps the field out entirely.
	if enc := EncodeTurn(TurnInfo{Model: "m"}); strings.Contains(enc, "thoughts") {
		t.Fatalf("EncodeTurn = %s, want no thoughts_ms", enc)
	}
	// An invalid list drops the durations but keeps the footer.
	for _, s := range []string{
		`{"v":1,"model":"m","ms":5,"tools":0,"cancelled":false,"thoughts_ms":[100,-1]}`,
		`{"v":1,"model":"m","ms":5,"tools":0,"cancelled":false,"thoughts_ms":[9223372036854775807]}`,
	} {
		got, ok := DecodeTurn(s)
		if !ok || got.Thoughts != nil || got.Model != "m" {
			t.Fatalf("DecodeTurn(%s) = %+v ok %t, want footer without thoughts", s, got, ok)
		}
	}
	// A wrongly typed list fails the decode like any other malformed field.
	if _, ok := DecodeTurn(`{"v":1,"thoughts_ms":"soon"}`); ok {
		t.Fatal("DecodeTurn accepted a string thoughts_ms")
	}
}

func TestDecodeTurnRejectsGarbage(t *testing.T) {
	for _, s := range []string{
		"",
		"   ",
		"not json",
		"null",
		"[]",
		`"string"`,
		"42",
		`{"model":"m","ms":10,"tools":1}`, // no version
		`{"v":0,"model":"m"}`,
		`{"v":-1}`,
		`{"v":1,"ms":-5}`,
		`{"v":1,"tools":-1}`,
		`{"v":"1"}`,
		`{"v":1,"ms":"fast"}`,
		`{"v":1,"model":7}`,
		`{"v":1,"ms":9223372036854775807}`, // overflows time.Duration
		`{"v":1} trailing`,
		`{"v":1`,
	} {
		if got, ok := DecodeTurn(s); ok {
			t.Errorf("DecodeTurn(%q) = %+v, true; want false", s, got)
		}
	}
}

func TestDecodeTurnToleratesUnknownFieldsAndLaterVersions(t *testing.T) {
	got, ok := DecodeTurn(` {"v":2,"model":"m","ms":4200,"tools":2,"cancelled":true,"tokens":{"in":5},"extra":[1,2]} `)
	if !ok {
		t.Fatal("DecodeTurn rejected unknown fields")
	}
	want := TurnInfo{Model: "m", Duration: 4200 * time.Millisecond, Tools: 2, Cancelled: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeTurn = %+v, want %+v", got, want)
	}
	// Missing optional fields read as zero values.
	got, ok = DecodeTurn(`{"v":1}`)
	if !ok || !reflect.DeepEqual(got, TurnInfo{}) {
		t.Fatalf("DecodeTurn minimal = %+v, %v", got, ok)
	}
}

func TestTurnDurationBands(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-3 * time.Second, "0.0s"},
		{0, "0.0s"},
		{40 * time.Millisecond, "0.0s"},
		{50 * time.Millisecond, "0.1s"},
		{8200 * time.Millisecond, "8.2s"},
		{9940 * time.Millisecond, "9.9s"},
		{9960 * time.Millisecond, "10.0s"},
		{12300 * time.Millisecond, "12.3s"}, // the spec's footer example
		{42 * time.Second, "42.0s"},
		{42640 * time.Millisecond, "42.6s"},
		{59940 * time.Millisecond, "59.9s"},
		{59960 * time.Millisecond, "1m 0s"}, // rounds into the minute band
		{64 * time.Second, "1m 4s"},
		{59*time.Minute + 59*time.Second, "59m 59s"},
		{59*time.Minute + 59600*time.Millisecond, "1h 0m"}, // rounds into the hour band
		{time.Hour, "1h 0m"},
		{time.Hour + 2*time.Minute + 20*time.Second, "1h 2m"},
		{time.Hour + 2*time.Minute + 40*time.Second, "1h 3m"},
		{26*time.Hour + 5*time.Minute, "26h 5m"},
	}
	for _, c := range cases {
		if got := TurnDuration(c.d); got != c.want {
			t.Errorf("TurnDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestFooterTextSegments(t *testing.T) {
	cases := []struct {
		name string
		info TurnInfo
		want string
	}{
		{"plural tools", TurnInfo{Model: "nexum-router", Duration: 12300 * time.Millisecond, Tools: 3}, "nexum-router · 12.3s · 3 tools"},
		{"short turn decimal", TurnInfo{Model: "nexum-router", Duration: 8200 * time.Millisecond, Tools: 3}, "nexum-router · 8.2s · 3 tools"},
		{"singular tool", TurnInfo{Model: "m", Duration: 42 * time.Second, Tools: 1}, "m · 42.0s · 1 tool"},
		{"zero tools omitted", TurnInfo{Model: "m", Duration: 64 * time.Second}, "m · 1m 4s"},
		{"negative tools omitted", TurnInfo{Model: "m", Duration: time.Second, Tools: -1}, "m · 1.0s"},
		{"cancelled", TurnInfo{Model: "m", Duration: 2 * time.Second, Tools: 2, Cancelled: true}, "m · 2.0s · 2 tools · cancelled"},
		{"cancelled no tools", TurnInfo{Model: "m", Duration: 2 * time.Second, Cancelled: true}, "m · 2.0s · cancelled"},
		{"empty model", TurnInfo{Duration: 2 * time.Second, Tools: 1}, "2.0s · 1 tool"},
		{"blank model", TurnInfo{Model: "  ", Duration: 2 * time.Second}, "2.0s"},
		{"hidden runes escaped", TurnInfo{Model: "a\x1b[31mb\u202ec\u200bd\u2028e", Duration: time.Second}, `a\u001B[31mb\u202Ec\u200Bd\u2028e · 1.0s`},
		{"whitespace collapsed", TurnInfo{Model: " a\n\tb  c ", Duration: time.Second}, "a b c · 1.0s"},
	}
	for _, c := range cases {
		if got := FooterText(c.info, 80, "…"); got != c.want {
			t.Errorf("%s: FooterText = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFooterTextDropsSegmentsRightToLeft(t *testing.T) {
	info := TurnInfo{Model: "nexum-router", Duration: 12300 * time.Millisecond, Tools: 3, Cancelled: true}
	// Full: "nexum-router · 12.3s · 3 tools · cancelled" = 42 cells.
	// Without cancelled: 30; without tools: 20; model alone: 12.
	expect := func(width int) string {
		switch {
		case width >= 42:
			return "nexum-router · 12.3s · 3 tools · cancelled"
		case width >= 30:
			return "nexum-router · 12.3s · 3 tools"
		case width >= 20:
			return "nexum-router · 12.3s"
		case width >= 12:
			return "nexum-router"
		}
		return ""
	}
	for width := 10; width <= 60; width++ {
		got := FooterText(info, width, "…")
		if w := runewidth.StringWidth(got); w > width {
			t.Fatalf("width %d: %q is %d cells", width, got, w)
		}
		if want := expect(width); want != "" && got != want {
			t.Errorf("width %d: FooterText = %q, want %q", width, got, want)
		}
	}
	if got := FooterText(info, 11, "…"); got != "nexum-rout…" {
		t.Errorf("width 11 truncation = %q", got)
	}
	if got := FooterText(info, 10, "…"); got != "nexum-rou…" {
		t.Errorf("width 10 truncation = %q", got)
	}
	if got := FooterText(info, 10, "..."); got != "nexum-r..." {
		t.Errorf("width 10 ASCII truncation = %q", got)
	}
}

func TestFooterTextTinyWidthsAndWideRunes(t *testing.T) {
	info := TurnInfo{Model: "模型模型模型", Duration: time.Second, Tools: 2}
	for width := -1; width <= 20; width++ {
		for _, ell := range []string{"…", "..."} {
			got := FooterText(info, width, ell)
			if w := runewidth.StringWidth(got); w > width && !(width <= 0 && got == "") {
				t.Fatalf("width %d ell %q: %q is %d cells", width, ell, got, w)
			}
			if width <= 0 && got != "" {
				t.Fatalf("width %d: want empty, got %q", width, got)
			}
		}
	}
	// 12-cell model at 6 cells: two wide runes (4 cells) + "…"; a third would straddle.
	if got := FooterText(info, 6, "…"); got != "模型…" {
		t.Errorf("wide truncation = %q", got)
	}
	// Ellipsis wider than the width: hard cut without it.
	if got := FooterText(TurnInfo{Model: "abcdef"}, 2, "..."); got != "ab" {
		t.Errorf("hard cut = %q", got)
	}
	// Without a model the duration is the last segment standing.
	if got := FooterText(TurnInfo{Duration: 64 * time.Second, Tools: 5}, 6, "…"); got != "1m 4s" {
		t.Errorf("model-less narrow = %q", got)
	}
}
