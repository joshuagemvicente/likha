package redact

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestSecrets(t *testing.T) {
	const key = "sk-test/key+1"
	cases := []struct {
		name   string
		in     string
		leaked string
	}{
		{"raw", "bad key " + key, key},
		{"query escaped", "url?key=sk-test%2Fkey%2B1", "sk-test%2Fkey%2B1"},
		{"path escaped", "path/sk-test%2Fkey+1", "sk-test%2Fkey+1"},
		{"query escaped, lowercase hex", "url?key=sk-test%2fkey%2b1", "sk-test%2fkey%2b1"},
		{"path escaped, lowercase hex", "path/sk-test%2fkey+1", "sk-test%2fkey+1"},
		{"JSON with an escaped slash", `{"key":"sk-test\/key+1"}`, `sk-test\/key+1`},
		{"bearer", "Authorization: Bearer abc.def-123", "abc.def-123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Secrets(tc.in, key)
			if strings.Contains(got, tc.leaked) {
				t.Fatalf("Secrets(%q) = %q, still contains %q", tc.in, got, tc.leaked)
			}
			if !strings.Contains(got, "[redacted]") {
				t.Fatalf("Secrets(%q) = %q, want a redaction marker", tc.in, got)
			}
		})
	}
	if got := Secrets("plain text", ""); got != "plain text" {
		t.Fatalf("empty secret changed text: %q", got)
	}
}

// Encoders differ in how they spell a secret: percent escapes with
// lowercase hex digits, and JSON that escapes "/" as "\/", are replaced like
// the spellings Go's own encoders produce.
func TestSecretsOtherEncoderSpellings(t *testing.T) {
	const secret = "sk-ab+cd/ef=gh12"
	cases := []struct {
		name, text, spelled string
	}{
		{"query escaped, lowercase hex", "u?k=sk-ab%2bcd%2fef%3dgh12&x", "sk-ab%2bcd%2fef%3dgh12"},
		{"path escaped, lowercase hex", "/p/sk-ab+cd%2fef=gh12/x", "sk-ab+cd%2fef=gh12"},
		{"query escaped, uppercase hex", "u?k=sk-ab%2Bcd%2Fef%3Dgh12&x", "sk-ab%2Bcd%2Fef%3Dgh12"},
		{"JSON with an escaped slash", `{"k":"sk-ab+cd\/ef=gh12"}`, `sk-ab+cd\/ef=gh12`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Secrets(tc.text, secret)
			if strings.Contains(got, tc.spelled) || !strings.Contains(got, Marker) {
				t.Fatalf("Secrets(%q) = %q, want %q replaced", tc.text, got, tc.spelled)
			}
		})
	}
}

// A secret that is a prefix of another must not leave the longer secret's
// remainder behind, whatever order the caller lists them in.
func TestSecretsOverlappingSecretsLeaveNoRemainder(t *testing.T) {
	const short, long = "sk-live-abc", "sk-live-abc-refresh-XYZ"
	for _, order := range [][]string{{short, long}, {long, short}} {
		got := Secrets("token "+long+" key "+short, order...)
		if want := "token [redacted] key [redacted]"; got != want {
			t.Fatalf("Secrets(order %q) = %q, want %q", order, got, want)
		}
	}
}

// A secret shorter than minSecretLen is not a credential any provider
// issues, and replacing it everywhere would erase ordinary text: --api-key k
// would redact every "k" Likha prints. A short real key such as LiteLLM's
// default master key sk-1234 is still redacted.
func TestSecretsIgnoresShortSecrets(t *testing.T) {
	const text = "keep every k and xyz in this text"
	if got := Secrets(text, "k", "xyz"); got != text {
		t.Fatalf("Secrets with short secrets = %q, want the text unchanged", got)
	}
	if forms := Forms("k", "xyz"); len(forms) != 0 {
		t.Fatalf("Forms of short secrets = %+v, want none", forms)
	}
	cases := []struct {
		name, text, secret, want string
	}{
		{"minimum length", "key abcd end", "abcd", "key [redacted] end"},
		{"LiteLLM default master key", "Authorization failed for sk-1234.", "sk-1234", "Authorization failed for [redacted]."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Secrets(tc.text, tc.secret); got != tc.want {
				t.Fatalf("Secrets(%q, %q) = %q, want %q", tc.text, tc.secret, got, tc.want)
			}
		})
	}
}

// Placeholder keys for local servers (--api-key test, ollama, none, dummy,
// EMPTY, local) are shorter than minSubstringSecretLen and are redacted only
// as whole tokens, so ordinary words that contain them survive.
func TestSecretsShortSecretsMatchWholeTokensOnly(t *testing.T) {
	cases := []struct {
		name, text, secret, want string
	}{
		{"word containing the key", "run the tests", "test", "run the tests"},
		{"key with punctuation", "key is test.", "test", "key is [redacted]."},
		{"key at both edges", "test", "test", "[redacted]"},
		{"key joined by underscore", "test_case and my-test", "test", "test_case and my-test"},
		{"key in a query", "url?key=none&x=1", "none", "url?key=[redacted]&x=1"},
		{"word containing ollama", "ollamas and ollama", "ollama", "ollamas and [redacted]"},
		{"uppercase placeholder", "EMPTYSET EMPTY", "EMPTY", "EMPTYSET [redacted]"},
		{"LiteLLM default master key", "failed for sk-1234.", "sk-1234", "failed for [redacted]."},
		{"longer key embedded", "sk-12345 sk-1234", "sk-1234", "sk-12345 [redacted]"},
		{"eight bytes match as substrings", "xxsk-123456xx", "sk-123456", "xx[redacted]xx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Secrets(tc.text, tc.secret); got != tc.want {
				t.Fatalf("Secrets(%q, %q) = %q, want %q", tc.text, tc.secret, got, tc.want)
			}
		})
	}
}

// A short secret next to an escape still stands alone: the escape's last
// letter (the D of %3D, the n of \n) is no part of the word. A form whose
// own edge is punctuation needs nothing from that side.
func TestSecretsShortSecretsBesideEscapes(t *testing.T) {
	cases := []struct {
		name, text, secret, want string
	}{
		{"after a percent escape", "?key%3Dsk-1234%26y", "sk-1234", "?key%3D[redacted]%26y"},
		{"after a JSON newline escape", `"a\nsk-1234"`, "sk-1234", `"a\n[redacted]"`},
		{"after a JSON tab escape", `"\tsk-1234"`, "sk-1234", `"\t[redacted]"`},
		{"after a JSON unicode escape", `"\u00e9sk-1234"`, "sk-1234", `"\u00e9[redacted]"`},
		{"punctuation edge after a letter", "key=x!abc12 end", "!abc12", "key=x[redacted] end"},
		{"word containing the key stays", "run the tests", "test", "run the tests"},
		{"plain letter before the key stays", "xsk-1234", "sk-1234", "xsk-1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Secrets(tc.text, tc.secret); got != tc.want {
				t.Fatalf("Secrets(%q, %q) = %q, want %q", tc.text, tc.secret, got, tc.want)
			}
		})
	}
}

// Occurrences of two secrets that overlap are replaced by one marker: no
// byte of either shows, whichever is found first.
func TestSecretsMergesOverlappingOccurrences(t *testing.T) {
	cases := []struct {
		name, text string
		secrets    []string
		want       string
	}{
		{"overlapping secrets", "abcdefghijklmnop", []string{"abcdefgh", "ghijklmnop"}, "[redacted]"},
		{"overlap in the other order", "x abcdefghijklmnop y", []string{"ghijklmnop", "abcdefgh"}, "x [redacted] y"},
		{"repeated secret overlapping itself", "aaaaaaaaaaaa", []string{"aaaaaaaa"}, "[redacted]"},
		{"touching secrets stay apart", "abcdefghijklmnop", []string{"abcdefgh", "ijklmnop"}, "[redacted][redacted]"},
		{"secret inside a bearer value", "Authorization: Bearer abcp@ss!word-12345 ok", []string{"p@ss!word-12345"}, "Authorization: Bearer [redacted] ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Secrets(tc.text, tc.secrets...); got != tc.want {
				t.Fatalf("Secrets(%q, %q) = %q, want %q", tc.text, tc.secrets, got, tc.want)
			}
		})
	}
}

// A short key glued to a longer secret stands alone once printed: the bytes
// beside it are replaced too, so it must not be left showing.
func TestSecretsShortKeyBesideAnotherSecret(t *testing.T) {
	cases := []struct {
		name, text string
		secrets    []string
		want       string
	}{
		{"key before a long secret", "sk-1234abcdefghij", []string{"sk-1234", "abcdefghij"}, "[redacted][redacted]"},
		{"key after a long secret", "abcdefghijsk-1234 end", []string{"sk-1234", "abcdefghij"}, "[redacted][redacted] end"},
		{"key inside a word stays", "xsk-1234y", []string{"sk-1234", "abcdefghij"}, "xsk-1234y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Secrets(tc.text, tc.secrets...); got != tc.want {
				t.Fatalf("Secrets(%q, %q) = %q, want %q", tc.text, tc.secrets, got, tc.want)
			}
		})
	}
}

// Redacting the two sides of a SplitBefore cut apart never redacts less
// than redacting the whole text, however the text is made.
func TestSplitBeforeNeverMissesARedaction(t *testing.T) {
	secrets := []string{"test", "sk-1234", "OT3;l:=", "sk-live-abcdef", "a/b+c"}
	pieces := []string{"test", "te", "st", "sk-1234", "OT3;l:=", "sk-live-abcdef", "a%2Fb%2Bc", "%3D", "%", `\n`, `\u00e9`, `\`,
		"x", "_", "0", ".", "Bearer ", "bearer", "bear", "er", "abc", "é"}
	forms := Forms(secrets...)
	random := rand.New(rand.NewPCG(1, 2))
	// SplitBefore needs a tail longer than any form and "bearer".
	tail := len("sk-live-abcdef") + len("bearer") + 1
	for i := 0; i < 5000; i++ {
		var text strings.Builder
		for text.Len() <= tail+8 {
			text.WriteString(pieces[random.IntN(len(pieces))])
		}
		whole := text.String()
		replaced := redactedBytes(whole, forms)
		for limit := 0; limit <= len(whole)-tail; limit++ {
			cut := SplitBefore(whole, limit, forms)
			first, second := redactedBytes(whole[:cut], forms), redactedBytes(whole[cut:], forms)
			for at, in := range replaced {
				if in && !(at < cut && first[at] || at >= cut && second[at-cut]) {
					t.Fatalf("cut %q at %d: byte %d is redacted in the whole text (%q) but not in its parts (%q + %q)",
						whole, cut, at, Secrets(whole, secrets...), Secrets(whole[:cut], secrets...), Secrets(whole[cut:], secrets...))
				}
			}
		}
	}
}

// SplitBefore is given the start of a stream: whatever text follows, the
// parts its cut leaves redact at least what the whole stream does. A
// bearer's spaces can run past the tail, so its value arrives later.
func TestSplitBeforeOfAStreamNeverMissesARedaction(t *testing.T) {
	secrets := []string{"test", "sk-1234", "OT3;l:=", "sk-live-abcdef", "a/b+c"}
	spaces := strings.Repeat(" ", 40)
	pieces := []string{"test", "te", "st", "sk-1234", "OT3;l:=", "sk-live-abcdef", "a%2Fb%2Bc", "%3D", `\n`,
		"x", "_", "0", ".", "Bearer ", "bearer", "bearer" + spaces, "Bearer" + spaces[:25], spaces, "tok", "é"}
	forms := Forms(secrets...)
	random := rand.New(rand.NewPCG(3, 4))
	// The tail SplitBefore needs: the longest whole-token form, the
	// longest form and "bearer".
	tail := len("sk-1234") + len("sk-live-abcdef") + len("bearer") + 1
	for i := 0; i < 3000; i++ {
		var text strings.Builder
		for text.Len() <= 2*tail+64 {
			text.WriteString(pieces[random.IntN(len(pieces))])
		}
		whole := text.String()
		replaced := redactedBytes(whole, forms)
		for end := tail + 1; end <= len(whole); end += 1 + random.IntN(4) {
			limit := end - tail - random.IntN(4)
			if limit < 0 {
				continue
			}
			cut := SplitBefore(whole[:end], limit, forms)
			first, second := redactedBytes(whole[:cut], forms), redactedBytes(whole[cut:], forms)
			for at, in := range replaced {
				if in && !(at < cut && first[at] || at >= cut && second[at-cut]) {
					t.Fatalf("stream %q cut at %d (limit %d, %d bytes seen): byte %d is redacted in the whole stream (%q) but not in its parts (%q + %q)",
						whole, cut, limit, end, at, Secrets(whole, secrets...), Secrets(whole[:cut], secrets...), Secrets(whole[cut:], secrets...))
				}
			}
		}
	}
}

// redactedBytes marks the bytes of text that Secrets replaces.
func redactedBytes(text string, forms []Form) []bool {
	marked := make([]bool, len(text))
	for _, group := range groups(find(text, forms)) {
		for at := group.start; at < group.end; at++ {
			marked[at] = true
		}
	}
	return marked
}

// SplitBefore never cuts inside a redaction, an unfinished bearer value, or
// right after an escape that decides a short secret's word edge.
func TestSplitBefore(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		limit int
		want  int
	}{
		{"free cut", "attest_xyz attest_xyz attest_xyz", 12, 12},
		{"inside a long secret", "xx sk-live-abcdef yyyyyyyyyyyyyyyyyyyyyy", 6, 3},
		{"inside a bearer value", "xx Bearer abc.def yyyyyyyyyyyyyyyyyyyyy", 12, 3},
		{"inside bearer spaces at the end", "xx bearer                    ", 12, 3},
		// Once the bearer's value arrives, sk-1234 right before it stands
		// alone, however many spaces come first.
		{"inside a short key before an open bearer", ".sk-1234bearer                              ", 3, 1},
		{"after an escape before a short key", "?key%3DOT3;l:=&yyyyyyyyyyyyyyyyyyyyyy", 6, 4},
		{"inside a character", "ééééééééééééééééééééééééé", 5, 4},
	}
	forms := Forms("sk-live-abcdef", "OT3;l:=", "sk-1234")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SplitBefore(tc.text, tc.limit, forms); got != tc.want {
				t.Fatalf("SplitBefore(%q, %d) = %d, want %d", tc.text, tc.limit, got, tc.want)
			}
		})
	}
}

// A bearer value is replaced after "bearer" and its spaces as written:
// rewriting the word as "Bearer " would spell out a secret that ends in "B"
// right before it.
func TestSecretsKeepsTheBearerWordAsWritten(t *testing.T) {
	cases := []struct {
		name, text, secret, want string
	}{
		{"capitalized", "Authorization: Bearer abc.def", "zzzzzzzz", "Authorization: Bearer [redacted]"},
		{"lowercase and two spaces", "auth bearer  abc.def ok", "zzzzzzzz", "auth bearer  [redacted] ok"},
		{"secret ending in B before it", "/0JEf5WI#bearer abc", "/0JEf5WI#B", "/0JEf5WI#bearer [redacted]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Secrets(tc.text, tc.secret)
			if got != tc.want {
				t.Fatalf("Secrets(%q, %q) = %q, want %q", tc.text, tc.secret, got, tc.want)
			}
			if strings.Contains(got, tc.secret) {
				t.Fatalf("Secrets(%q) = %q, which spells out %q", tc.text, got, tc.secret)
			}
		})
	}
}
