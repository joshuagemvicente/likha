// Package redact removes credentials from text Likha prints or saves.
package redact

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// bearerPattern finds an authorization value a provider may echo.
var bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`)

// minSecretLen is the shortest secret Secrets replaces: a one- to
// three-letter value (--api-key k) is no credential, and replacing it would
// erase those letters from every message Likha prints.
const minSecretLen = 4

// minSubstringSecretLen is the shortest secret Secrets replaces wherever it
// occurs. Shorter ones are often placeholder keys for local servers
// ("test", "none", "dummy", "ollama"), which replacing as substrings would
// cut out of ordinary words ("run the tests"); they are replaced only as
// whole tokens. Real short keys still are: LiteLLM's default master key
// sk-1234 stands alone wherever it is echoed.
const minSubstringSecretLen = 8

// Marker replaces each secret.
const Marker = "[redacted]"

// Form is one text form of a secret that Secrets replaces. A WholeToken form
// (from a secret shorter than minSubstringSecretLen) matches only where it
// stands alone: on each side where the form's own edge byte is a token byte
// (see tokenByte), the text must end there, hold a byte that is not one, or
// hold a byte another redaction replaces. Before the form, a percent escape
// (%3D) or JSON escape (\n) also counts as the edge of a word.
type Form struct {
	Text       string
	WholeToken bool
}

// Secrets replaces every secret in text, in its raw, URL-escaped and
// JSON-escaped forms, plus any bearer authorization value, with Marker
// ("Bearer abc" becomes "Bearer [redacted]").
// Secrets shorter than minSecretLen are ignored; those shorter than
// minSubstringSecretLen are replaced only as whole tokens. Occurrences that
// overlap are replaced together by one Marker, so no part of either shows.
func Secrets(text string, secrets ...string) string {
	return Apply(text, Forms(secrets...))
}

// Apply is Secrets with the secrets' forms (see Forms) already listed, for a
// caller that redacts many texts with the same secrets.
func Apply(text string, forms []Form) string {
	occurrences := find(text, forms)
	if len(occurrences) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	for _, group := range groups(occurrences) {
		out.WriteString(text[last:group.start])
		out.WriteString(group.label(text))
		last = group.end
	}
	out.WriteString(text[last:])
	return out.String()
}

// openBearer matches "bearer" and its spaces at the end of a text, still
// waiting for the value bearerPattern would replace.
var openBearer = regexp.MustCompile(`(?i)bearer\s*$`)

// SplitBefore returns the largest index, at most limit, at which text, the
// start of a stream that may continue, can be cut so that Secrets of the two
// parts replaces everything Secrets of the whole would: the cut falls inside
// no redaction, not inside "bearer" and its spaces still waiting for a
// value, not inside a character, and not where an escape before the cut (the
// %3D of key%3Dsk-1234) decides whether a short secret just after it stands
// alone. The parts may be redacted more than the whole, since at the cut a
// short secret counts as standing alone, but never less. text[limit:] must be
// longer than the longest whole-token form plus the longest form and
// "bearer": a short secret just before limit may stand alone only because a
// redaction starts right after it, so that redaction must be seen whole too.
// forms are the secrets' forms (see Forms).
func SplitBefore(text string, limit int, forms []Form) int {
	cut := limit
	searched := text
	if loc := openBearer.FindStringIndex(text); loc != nil {
		cut = min(cut, loc[0])
		// The bearer still waiting for its value may be followed by any
		// number of spaces, so the value can arrive after limit and beyond
		// what the tail shows. Once it does, a short secret right before
		// "bearer" stands alone; search as if it had arrived, so the cut
		// keeps such a secret whole (an over-redaction when no value comes).
		searched = text + " x"
	}
	replaced := groups(find(searched, forms))
	for cut > 0 {
		at := sort.Search(len(replaced), func(k int) bool { return replaced[k].end > cut })
		switch {
		case at < len(replaced) && replaced[at].start < cut:
			cut = replaced[at].start
		case !utf8.RuneStart(text[cut]) || escapeDecides(text, cut, forms):
			cut--
		default:
			return cut
		}
	}
	return 0
}

// escapeDecides reports whether a whole-token form starts just after cut,
// close enough that the bytes wordStartsAfter reads before it reach back
// across the cut, while those bytes hold a percent or JSON escape.
func escapeDecides(text string, cut int, forms []Form) bool {
	if !strings.ContainsAny(text[max(0, cut-escapeLen):cut], `%\`) {
		return false
	}
	for _, form := range forms {
		if !form.WholeToken {
			continue
		}
		window := text[cut+1 : min(len(text), cut+escapeLen+len(form.Text))]
		if strings.Contains(window, form.Text) {
			return true
		}
	}
	return false
}

// Forms lists every form of secrets that Secrets replaces, longest first.
// Secrets shorter than minSecretLen are ignored.
func Forms(secrets ...string) []Form {
	var forms []Form
	seen := make(map[Form]bool)
	for _, secret := range secrets {
		if len(secret) < minSecretLen {
			continue
		}
		whole := len(secret) < minSubstringSecretLen
		for _, text := range secretForms(secret) {
			form := Form{Text: text, WholeToken: whole}
			if !seen[form] {
				seen[form] = true
				forms = append(forms, form)
			}
		}
	}
	sort.SliceStable(forms, func(i, j int) bool { return len(forms[i].Text) > len(forms[j].Text) })
	return forms
}

// secretForms lists the spellings of secret that Secrets replaces: raw,
// URL query- and path-escaped with upper- or lowercase hex digits, and
// JSON-escaped with "/" written plain or as "\/". Partly escaped spellings
// (only the "=" escaped, say) and escapes applied twice are not recognized.
func secretForms(secret string) []string {
	query, path := url.QueryEscape(secret), url.PathEscape(secret)
	forms := []string{secret, query, path, lowerHexEscapes(query), lowerHexEscapes(path)}
	if quoted, err := json.Marshal(secret); err == nil {
		escaped := strings.Trim(string(quoted), `"`)
		forms = append(forms, escaped, strings.ReplaceAll(escaped, "/", `\/`))
	}
	return forms
}

// lowerHexEscapes rewrites every percent escape in escaped (%2B) with
// lowercase hex digits (%2b), as some encoders write them.
func lowerHexEscapes(escaped string) string {
	out := []byte(escaped)
	for i := 0; i+2 < len(out); i++ {
		if out[i] == '%' {
			out[i+1], out[i+2] = lowerHex(out[i+1]), lowerHex(out[i+2])
			i += 2
		}
	}
	return string(out)
}

func lowerHex(b byte) byte {
	if 'A' <= b && b <= 'F' {
		return b + 'a' - 'A'
	}
	return b
}

// occurrence is one match Secrets replaces: text[start:end]. For a bearer
// authorization value, value is where the value begins after "bearer" and
// its spaces; it is -1 for a secret form.
type occurrence struct{ start, end, value int }

// find lists every occurrence of forms and of bearer authorization values in
// text, sorted by start; at the same start a secret comes before a bearer
// value. Occurrences may overlap.
func find(text string, forms []Form) []occurrence {
	var found []occurrence
	for _, loc := range bearerPattern.FindAllStringIndex(text, -1) {
		value := loc[0] + len("bearer")
		for value < loc[1] && IsSpace(text[value]) {
			value++
		}
		found = append(found, occurrence{loc[0], loc[1], value})
	}
	for _, form := range forms {
		if !form.WholeToken {
			found = appendMatches(found, text, form.Text, nil)
		}
	}
	sortOccurrences(found)
	// A whole-token form next to another redaction stands alone: the bytes
	// beside it are replaced, so they join no word with it once printed.
	replaced := groups(found)
	for _, form := range forms {
		if form.WholeToken {
			found = appendMatches(found, text, form.Text, func(start, end int) bool {
				return standsAlone(text, start, end, replaced)
			})
		}
	}
	sortOccurrences(found)
	return found
}

// appendMatches appends every occurrence of form in text, overlapping ones
// included, that keep accepts (all of them when keep is nil).
func appendMatches(found []occurrence, text, form string, keep func(start, end int) bool) []occurrence {
	for from := 0; from < len(text); {
		index := strings.Index(text[from:], form)
		if index < 0 {
			break
		}
		start := from + index
		end := start + len(form)
		if keep == nil || keep(start, end) {
			found = append(found, occurrence{start, end, -1})
		}
		from = start + 1
	}
	return found
}

func sortOccurrences(found []occurrence) {
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].start != found[j].start {
			return found[i].start < found[j].start
		}
		return found[i].value < 0 && found[j].value >= 0
	})
}

// group is a run of overlapping occurrences that one Marker replaces.
type group struct {
	start, end int
	// value is where the bearer value starts when the group starts with
	// "bearer" and no secret in it starts before that value; it is -1
	// otherwise.
	value int
}

// label is what replaces the group: the marker, after "bearer" and its
// spaces exactly as the text wrote them. Rewriting them (to "Bearer ", say)
// could spell out a secret that ends in "B" just before them.
func (g group) label(text string) string {
	if g.value >= 0 {
		return text[g.start:g.value] + Marker
	}
	return Marker
}

// groups merges sorted occurrences that overlap. Occurrences that only touch
// stay apart, one Marker each.
func groups(found []occurrence) []group {
	var merged []group
	for _, occ := range found {
		if n := len(merged); n > 0 && occ.start < merged[n-1].end {
			last := &merged[n-1]
			last.end = max(last.end, occ.end)
			if occ.value < 0 && occ.start < last.value {
				last.value = -1
			}
			continue
		}
		merged = append(merged, group{start: occ.start, end: occ.end, value: occ.value})
	}
	return merged
}

// replacedAt reports whether byte i of the text lies inside one of groups.
func replacedAt(groups []group, i int) bool {
	at := sort.Search(len(groups), func(k int) bool { return groups[k].end > i })
	return at < len(groups) && groups[at].start <= i
}

// IsSpace reports whether b is whitespace as the bearer pattern's \s reads
// it. No redaction spans such a byte except the spaces between "bearer" and
// its value, unless a secret holds one.
func IsSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

// tokenByte reports whether b can continue a word or key: a whole-token
// match must not touch one on a side where it ends in one itself.
func tokenByte(b byte) bool {
	return b == '_' || b == '-' || ('0' <= b && b <= '9') || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

// standsAlone reports whether the whole-token occurrence text[start:end] is
// not part of a longer word. A side where the occurrence's own edge byte is
// no token byte ("!abc12" after a letter) needs nothing from its neighbor,
// and a neighbor that another redaction replaces is no part of the word.
func standsAlone(text string, start, end int, replaced []group) bool {
	if tokenByte(text[start]) && !wordStartsAfter(text[max(0, start-escapeLen):start]) && !replacedAt(replaced, start-1) {
		return false
	}
	return !tokenByte(text[end-1]) || end == len(text) || !tokenByte(text[end]) || replacedAt(replaced, end)
}

// escapeLen is the longest escape wordStartsAfter reads: \u00e9.
const escapeLen = len(`\u00e9`)

// wordStartsAfter reports whether a word can start right after prefix: at
// the start of the text, after a byte that is no token byte, or after a
// percent escape (key%3Dsk-1234) or JSON escape ("a\nsk-1234", "é")
// whose last character is a token byte only by accident. An escaped
// backslash followed by n reads as an escape too; that errs toward
// redacting.
func wordStartsAfter(prefix string) bool {
	n := len(prefix)
	switch {
	case n == 0 || !tokenByte(prefix[n-1]):
		return true
	case n >= 3 && prefix[n-3] == '%' && hexDigits(prefix[n-2:]):
		return true
	case n >= 2 && prefix[n-2] == '\\' && strings.IndexByte("bfnrt", prefix[n-1]) >= 0:
		return true
	case n >= 6 && prefix[n-6:n-4] == `\u` && hexDigits(prefix[n-4:]):
		return true
	}
	return false
}

func hexDigits(text string) bool {
	for i := 0; i < len(text); i++ {
		b := text[i]
		if !('0' <= b && b <= '9' || 'a' <= b && b <= 'f' || 'A' <= b && b <= 'F') {
			return false
		}
	}
	return true
}
