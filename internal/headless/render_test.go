package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/tools"
)

// splitEvery cuts text into pieces of at most n bytes.
func splitEvery(text string, n int) []string {
	var pieces []string
	for len(text) > n {
		pieces = append(pieces, text[:n])
		text = text[n:]
	}
	return append(pieces, text)
}

func TestDeltaRedactorNeverReleasesASplitSecret(t *testing.T) {
	const escapable = "sk/esc+key=9"
	cfg := Config{Secrets: []string{testKey, escapable}}
	cases := []struct {
		name   string
		deltas []string
		leaked string
	}{
		{"split in two", []string{"the key is sk-headless-", "secret-123 ok"}, testKey},
		{"one byte at a time", splitEvery("prefix "+testKey+" suffix", 1), testKey},
		{"secret at the very end", []string{"key: sk-headless-sec", "ret-123"}, testKey},
		{"query escaped form", splitEvery("url?k=sk%2Fesc%2Bkey%3D9&x=1", 3), "sk%2Fesc%2Bkey%3D9"},
		{"bearer token split", []string{"Authorization: Bear", "er abc.def", "-123 done"}, "abc.def-123"},
		{"bearer token at the end", []string{"Authorization: Bearer abc", ".def-123"}, "abc.def-123"},
		{"multibyte text around it", splitEvery("héllo ☃ "+testKey+" ☃ wörld", 2), testKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDeltaRedactor(cfg)
			var released strings.Builder
			for _, delta := range tc.deltas {
				released.WriteString(d.push(delta))
			}
			released.WriteString(d.flush())
			got := released.String()
			if strings.Contains(got, tc.leaked) {
				t.Fatalf("released %q, which contains %q", got, tc.leaked)
			}
			if want := cfg.clean(strings.Join(tc.deltas, "")); got != want {
				t.Fatalf("released %q, want %q", got, want)
			}
		})
	}
}

// streamedReply writes text as one SSE delta per piece.
func streamedReply(pieces ...string) reply {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, piece := range pieces {
			delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": piece}}}})
			fmt.Fprintf(w, "data: %s\n\n", delta)
		}
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func TestJSONTextDeltasRedactASecretSplitAcrossDeltas(t *testing.T) {
	fake := newFakeProvider(t, streamedReply("Your key is sk-headless-", "secret-123, keep it safe."))
	run := newTestRun(t, fake, "what is my key")
	run.cfg.JSON = true
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d\nstderr:\n%s", code, stderr)
	}
	events := decodeEvents(t, stdout)
	var streamed strings.Builder
	for _, event := range eventsOfType(events, "text") {
		streamed.WriteString(event["delta"].(string))
	}
	if strings.Contains(stdout, "secret-123") || strings.Contains(streamed.String(), testKey) {
		t.Fatalf("a text delta leaked the key:\n%s", stdout)
	}
	if want := "Your key is [redacted], keep it safe."; streamed.String() != want {
		t.Fatalf("streamed text = %q, want %q", streamed.String(), want)
	}
	// Held text is released before the usage event that ends the response.
	var sawUsage bool
	for _, event := range events {
		switch event["type"] {
		case "usage":
			sawUsage = true
		case "text":
			if sawUsage {
				t.Fatalf("a text delta followed the response's usage event:\n%s", stdout)
			}
		}
	}
}

// A long credential, such as a ChatGPT OAuth token, must not hold back the
// text around it: everything up to the last whitespace goes out at once.
func TestDeltaRedactorReleasesTextBesideALongSecret(t *testing.T) {
	secret := "eyJ" + strings.Repeat("x", 2045)
	cfg := Config{Secrets: []string{secret}}
	d := newDeltaRedactor(cfg)
	if got, want := d.push("Here is the plan. "), "Here is the plan. "; got != want {
		t.Fatalf("first delta released %q before flush, want %q", got, want)
	}
	if got, want := d.push("Token: eyJxx"), "Token: "; got != want {
		t.Fatalf("delta with a partial secret released %q, want %q", got, want)
	}
	if got, want := d.push(secret[5:]+" and more."), "[redacted] and "; got != want {
		t.Fatalf("completed secret released %q, want %q", got, want)
	}
	if got, want := d.flush(), "more."; got != want {
		t.Fatalf("flush released %q, want %q", got, want)
	}
}

// However the model splits its reply into deltas, the released text equals
// the whole reply redacted.
func TestDeltaRedactorMatchesWholeTextRedaction(t *testing.T) {
	cfg := Config{Secrets: []string{"test", "sk-1234", testKey}}
	pieces := []string{"test", "te", "st", "sk-1234", "sk-", "1234", testKey, "sk-headless-", "%3D", `\n`, "x", "_", " ", ".", "Bearer ", "bear", "é", "☃"}
	random := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 2000; i++ {
		var text strings.Builder
		for range 1 + random.IntN(10) {
			text.WriteString(pieces[random.IntN(len(pieces))])
		}
		whole := text.String()
		d := newDeltaRedactor(cfg)
		var released strings.Builder
		for rest := whole; rest != ""; {
			n := 1 + random.IntN(min(len(rest), 7))
			released.WriteString(d.push(rest[:n]))
			rest = rest[n:]
		}
		released.WriteString(d.flush())
		if got, want := released.String(), cfg.clean(whole); got != want {
			t.Fatalf("streamed %q = %q, want %q", whole, got, want)
		}
	}
}

// A notice or the other stream can arrive in the middle of a word that may
// be a secret. The partial word stays held and joins the rest of its stream,
// so the secret is redacted whole after the event; "bearer" waiting for its
// value stays with it.
func TestJSONInterruptedDeltasNeverSplitASecret(t *testing.T) {
	cases := []struct {
		name   string
		events []agent.TurnEvent
		leaked string
		want   []eventLine
	}{
		{"short key around a notice", []agent.TurnEvent{
			{Kind: "text", Text: "key is te"}, {Kind: "notice", Text: "hook warning"}, {Kind: "text", Text: "st."},
		}, "st.", []eventLine{{"text", "key is "}, {"notice", "hook warning"}, {"text", "[redacted]."}}},
		{"long key around a notice", []agent.TurnEvent{
			{Kind: "text", Text: "token sk-headless-sec"}, {Kind: "notice", Text: "hook warning"}, {Kind: "text", Text: "ret-123 ok"},
		}, "ret-123", []eventLine{{"text", "token "}, {"notice", "hook warning"}, {"text", "[redacted] "}, {"text", "ok"}}},
		{"bearer value after a stream switch", []agent.TurnEvent{
			{Kind: "reasoning", Text: "use b"}, {Kind: "text", Text: "hi"}, {Kind: "reasoning", Text: "earer abc.def done"},
		}, "abc.def", []eventLine{{"reasoning", "use "}, {"reasoning", "bearer [redacted] "}, {"reasoning", "done"}, {"text", "hi"}}},
		{"word that only looked like a secret", []agent.TurnEvent{
			{Kind: "text", Text: "a b"}, {Kind: "notice", Text: "n"}, {Kind: "text", Text: "ook"},
		}, "", []eventLine{{"text", "a "}, {"notice", "n"}, {"text", "book"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout strings.Builder
			out := newRenderer(Config{JSON: true, Secrets: []string{"test", testKey}}, &stdout, io.Discard)
			for _, ev := range tc.events {
				out.event(ev)
			}
			out.(*jsonRenderer).endSegment()
			got := renderedEvents(t, stdout.String())
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("events = %v\nwant     %v", got, tc.want)
			}
			var streamed strings.Builder
			for _, line := range got {
				if line.kind != "notice" {
					streamed.WriteString(line.text)
				}
			}
			if tc.leaked != "" && strings.Contains(streamed.String(), tc.leaked) {
				t.Fatalf("streamed %q, which contains %q", streamed.String(), tc.leaked)
			}
		})
	}
}

// eventLine is one JSON event reduced to its type and text.
type eventLine struct{ kind, text string }

func renderedEvents(t *testing.T, stdout string) []eventLine {
	t.Helper()
	var lines []eventLine
	for _, event := range decodeEvents(t, stdout) {
		text, _ := event["delta"].(string)
		if text == "" {
			text, _ = event["text"].(string)
		}
		lines = append(lines, eventLine{event["type"].(string), text})
	}
	return lines
}

// Deltas and notices keep the order the model and hooks sent them in; only
// a stream's partial word waits, for the rest of its word or the response's
// end.
func TestJSONDeltasKeepStreamOrder(t *testing.T) {
	var stdout strings.Builder
	out := newRenderer(Config{JSON: true}, &stdout, io.Discard)
	out.event(agent.TurnEvent{Kind: "reasoning", Text: "I think, b"})
	out.event(agent.TurnEvent{Kind: "text", Text: "Answer: b"})
	out.event(agent.TurnEvent{Kind: "notice", Text: "a note"})
	out.event(agent.TurnEvent{Kind: "reasoning", Text: "ut more "})
	out.event(agent.TurnEvent{Kind: "text", Text: "ye."})
	out.event(agent.TurnEvent{Kind: "usage", Usage: &model.RequestUsage{Prompt: 1, Completion: 1}})
	want := []eventLine{
		{"reasoning", "I think, "},
		{"text", "Answer: "},
		{"notice", "a note"},
		{"reasoning", "but more "},
		{"text", "bye."},
		{"usage", ""},
	}
	got := renderedEvents(t, stdout.String())
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v\nwant     %v", got, want)
	}
}

// Text mode reports an automatic compaction as a note naming its trigger,
// worded as the TUI's divider.
func TestTextRendererReportsAutoCompaction(t *testing.T) {
	cases := []struct {
		name           string
		tokens, window int64
		want           string
	}{
		{"known window", 172_000, 200_000, "likha: note: Conversation compacted automatically at ~86% of the 200k context window.\n"},
		{"fractional window", 1_290, 1_500, "likha: note: Conversation compacted automatically at ~86% of the 1.5k context window.\n"},
		{"no window", 10, 0, "likha: note: Conversation compacted automatically.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr strings.Builder
			out := &textRenderer{stdout: io.Discard, stderr: &stderr}
			out.event(agent.TurnEvent{Kind: "auto_compacted", Text: "summary", ContextTokens: tc.tokens, ContextWindow: tc.window})
			if stderr.String() != tc.want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

// streamCapture decodes the JSON events a renderer wrote since the last
// call, keeping each stream's text so far.
type streamCapture struct {
	stdout *strings.Builder
	read   int
	text   map[string]*strings.Builder
	deltas []eventLine
}

func newStreamCapture(stdout *strings.Builder) *streamCapture {
	return &streamCapture{stdout: stdout, text: map[string]*strings.Builder{"text": {}, "reasoning": {}}}
}

func (c *streamCapture) update(t *testing.T) {
	t.Helper()
	fresh := c.stdout.String()[c.read:]
	c.read = c.stdout.Len()
	for _, line := range strings.Split(strings.TrimSuffix(fresh, "\n"), "\n") {
		if line == "" {
			continue
		}
		var event struct{ Type, Delta string }
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("stdout line is not JSON: %q (%v)", line, err)
		}
		if builder, ok := c.text[event.Type]; ok {
			builder.WriteString(event.Delta)
			c.deltas = append(c.deltas, eventLine{event.Type, event.Delta})
		}
	}
}

// secretRunes are the characters random test secrets are made of: no
// whitespace, and no brackets, which the marker itself holds.
var secretRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.~+/=@!#$%^&*()<>?:;,'\"|{}é☃ß中")

// randomSecret is a secret of 4 to 40 bytes.
func randomSecret(random *rand.Rand) string {
	target := 4 + random.IntN(37)
	var secret strings.Builder
	for {
		r := string(secretRunes[random.IntN(len(secretRunes))])
		if secret.Len()+len(r) > 40 {
			break
		}
		secret.WriteString(r)
		if secret.Len() >= target {
			break
		}
	}
	if secret.Len() < 4 {
		secret.WriteString("abcd"[:4-secret.Len()])
	}
	return secret.String()
}

// randomResponse is one stream's text for one model response: words,
// spaces, bearer prefixes and the secrets, whole, in pieces and escaped.
func randomResponse(random *rand.Rand, secrets []string) string {
	pieces := []string{"the", "key", "x", "ab", "test", " ", " ", "  ", "\n", "\t", ".", ",", "é", "☃", "_", "-", "%3D", `\n`,
		"Bearer ", "bearer  ", "BEARER\t", "Authorization: Bearer ", "b", "bear", "er", "sk-", "1234"}
	var text strings.Builder
	for range random.IntN(14) {
		if random.IntN(3) == 0 {
			secret := secrets[random.IntN(len(secrets))]
			switch random.IntN(5) {
			case 0:
				secret = secret[:random.IntN(len(secret)+1)]
			case 1:
				secret = secret[random.IntN(len(secret)+1):]
			case 2:
				secret = url.QueryEscape(secret)
			case 3:
				quoted, _ := json.Marshal(secret)
				secret = strings.Trim(string(quoted), `"`)
			}
			text.WriteString(secret)
			continue
		}
		text.WriteString(pieces[random.IntN(len(pieces))])
	}
	return text.String()
}

// randomDeltas splits text into deltas of 1 to 8 bytes, cutting through
// characters too.
func randomDeltas(random *rand.Rand, text string) []string {
	var deltas []string
	for text != "" {
		n := 1 + random.IntN(min(len(text), 8))
		deltas = append(deltas, text[:n])
		text = text[n:]
	}
	return deltas
}

// segmentEnds are the events that end a model response.
var segmentEnds = []agent.TurnEvent{
	{Kind: "usage", Usage: &model.RequestUsage{Prompt: 1, Completion: 1}},
	{Kind: "tool_start", ToolCall: &model.ToolCall{ID: "c1", Name: "read_file", Arguments: "{}"}},
	{Kind: "tool_result"},
	{Kind: "auto_compacted"},
	{Kind: "done"},
	{Kind: "error", Text: "stream failed"},
}

// However deltas are split, interleaved with the other stream and with
// notices, each stream's released text equals its whole response redacted
// at every response's end, and no long secret (one Secrets replaces as a
// substring) ever shows in a delta.
func TestJSONStreamRedactionMatchesWholeResponse(t *testing.T) {
	const cases = 50_000
	random := rand.New(rand.NewPCG(7, 8))
	for i := 0; i < cases; i++ {
		secrets := []string{randomSecret(random)}
		for range random.IntN(3) {
			secrets = append(secrets, randomSecret(random))
		}
		cfg := Config{JSON: true, Secrets: secrets}
		var stdout strings.Builder
		out := newRenderer(cfg, &stdout, io.Discard)
		capture := newStreamCapture(&stdout)
		want := map[string]*strings.Builder{"text": {}, "reasoning": {}}
		var trace []string
		for range 1 + random.IntN(3) {
			pending := map[string][]string{}
			for kind := range want {
				response := randomResponse(random, secrets)
				want[kind].WriteString(cfg.clean(response))
				pending[kind] = randomDeltas(random, response)
			}
			for len(pending["text"])+len(pending["reasoning"]) > 0 {
				if random.IntN(6) == 0 {
					out.event(agent.TurnEvent{Kind: "notice", Text: "note"})
					trace = append(trace, "notice")
					continue
				}
				kind := "text"
				if len(pending["text"]) == 0 || len(pending["reasoning"]) > 0 && random.IntN(2) == 0 {
					kind = "reasoning"
				}
				out.event(agent.TurnEvent{Kind: kind, Text: pending[kind][0]})
				trace = append(trace, kind+":"+strconv.Quote(pending[kind][0]))
				pending[kind] = pending[kind][1:]
			}
			end := segmentEnds[random.IntN(len(segmentEnds))]
			out.event(end)
			trace = append(trace, end.Kind)
			capture.update(t)
			for kind, builder := range want {
				if got := capture.text[kind].String(); got != builder.String() {
					t.Fatalf("case %d, secrets %q: %s released %q, want %q\nevents: %s", i, secrets, kind, got, builder.String(), strings.Join(trace, " "))
				}
			}
		}
		for _, delta := range capture.deltas {
			for _, secret := range secrets {
				if len(secret) >= 8 && strings.Contains(delta.text, secret) {
					t.Fatalf("case %d: %s delta %q shows secret %q\nevents: %s", i, delta.kind, delta.text, secret, strings.Join(trace, " "))
				}
			}
		}
	}
}

// The reviewer's repros, each a secret split around an event.
func TestJSONStreamRedactionRepros(t *testing.T) {
	cases := []struct {
		name    string
		secrets []string
		events  []agent.TurnEvent
		want    string
		leaked  string
	}{
		{"symbol after bearer around a notice", []string{"p@ss!word-12345"}, []agent.TurnEvent{
			{Kind: "text", Text: "Authorization: Bearer "}, {Kind: "notice", Text: "hook warning"}, {Kind: "text", Text: "p@ss!word-12345 ok"},
		}, "Authorization: Bearer [redacted] ok", "@ss!word"},
		{"symbol after bearer around a stream switch", []string{"p@ss!word-12345"}, []agent.TurnEvent{
			{Kind: "text", Text: "Authorization: Bearer "}, {Kind: "reasoning", Text: "hm "}, {Kind: "text", Text: "p@ss!word-12345 ok"},
		}, "Authorization: Bearer [redacted] ok", "@ss!word"},
		{"short key starting the next response", []string{"sk-1234"}, []agent.TurnEvent{
			{Kind: "text", Text: "Let me look up the key"},
			{Kind: "tool_start", ToolCall: &model.ToolCall{ID: "c1", Name: "read_file", Arguments: "{}"}},
			{Kind: "tool_result"},
			{Kind: "text", Text: "sk-1234 is the configured key."},
		}, "Let me look up the key[redacted] is the configured key.", "sk-1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout strings.Builder
			out := newRenderer(Config{JSON: true, Secrets: tc.secrets}, &stdout, io.Discard)
			for _, ev := range tc.events {
				out.event(ev)
			}
			out.event(agent.TurnEvent{Kind: "done"})
			capture := newStreamCapture(&stdout)
			capture.update(t)
			if got := capture.text["text"].String(); got != tc.want {
				t.Fatalf("text = %q, want %q", got, tc.want)
			}
			for _, delta := range capture.deltas {
				if strings.Contains(delta.text, tc.leaked) {
					t.Fatalf("delta %q shows %q", delta.text, tc.leaked)
				}
			}
		})
	}
}

// pushAll streams deltas through a redactor and returns what it released,
// failing when it ever holds more than maxHeld bytes or takes a second.
func pushAll(t *testing.T, d *deltaRedactor, deltas []string, maxHeld int) string {
	t.Helper()
	var released strings.Builder
	start := time.Now()
	for _, delta := range deltas {
		released.WriteString(d.push(delta))
		if len(d.pending) > maxHeld {
			t.Fatalf("redactor holds %d bytes after %d released, want at most %d", len(d.pending), released.Len(), maxHeld)
		}
	}
	released.WriteString(d.flush())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("streaming %d deltas took %s", len(deltas), elapsed)
	}
	return released.String()
}

// Streaming costs the same per byte however long the text grows. With
// whitespace in it, only the partial word after the last space waits; a run
// with no whitespace waits up to the cap, never longer.
func TestDeltaRedactorStaysFastAndBounded(t *testing.T) {
	jwt := "eyJ" + strings.Repeat("abcDEF123_-", 186) // 2049 bytes
	cases := []struct {
		name    string
		secret  string
		text    string
		size    int
		maxHeld int
		exact   bool
	}{
		{"words beside a short key", "test", strings.Repeat("attest_xyz ", 10_000) + "test.", 10, 32, true},
		{"words beside a JWT", jwt, strings.Repeat("some words ", 5_000) + jwt + strings.Repeat(" more words", 5_000), 10, len(jwt) + 32, true},
		{"token bytes beside a short key", "test", strings.Repeat("attest_xyz", 10_000), 10, minDeltaCap + 10, false},
		{"token bytes beside a JWT", jwt, strings.Repeat("attest_xyz", 6_500) + jwt + strings.Repeat("attest_xyz", 3_500), 10, minDeltaCap + 10, false},
		{"repeated digits beside a short key", "0000", strings.Repeat("0", 3_000), 1, 3_001, true},
		{"many repeated digits beside a short key", "0000", strings.Repeat("0", 100_000), 1, minDeltaCap + 1, false},
		{"a secret repeated through a long word", "aaaaaaaa", strings.Repeat("a", 100_000) + " tail", 7, minDeltaCap + 7, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Secrets: []string{tc.secret}}
			got := pushAll(t, newDeltaRedactor(cfg), splitEvery(tc.text, tc.size), tc.maxHeld)
			if tc.exact && got != cfg.clean(tc.text) {
				t.Fatalf("streamed redaction differs from the whole text's (len %d vs %d)", len(got), len(cfg.clean(tc.text)))
			}
			if len(tc.secret) >= 8 {
				for at := 0; at+32 <= len(tc.secret); at += 32 {
					if strings.Contains(got, tc.secret[at:at+32]) {
						t.Fatalf("released text shows bytes %d-%d of the secret", at, at+32)
					}
				}
			}
		})
	}
}

// A secret form with whitespace in it can span any cut, so such a stream is
// held whole until its response ends.
func TestDeltaRedactorHoldsTextWhenASecretHasASpace(t *testing.T) {
	cfg := Config{Secrets: []string{"open sesame 42"}}
	d := newDeltaRedactor(cfg)
	var released strings.Builder
	for _, delta := range []string{"the words: open ", "sesame 42 and ", "more"} {
		released.WriteString(d.push(delta))
	}
	if released.Len() != 0 {
		t.Fatalf("released %q before the response ended", released.String())
	}
	if got, want := d.flush(), "the words: [redacted] and more"; got != want {
		t.Fatalf("flush released %q, want %q", got, want)
	}
}

// With a small cap, long runs without whitespace go out through cap
// releases. A cap cut may redact more than the whole text would, but it
// never splits a secret: neither a long secret nor a short one standing
// alone between dots ever shows, and nothing grows past the cap.
func TestDeltaRedactorCapNeverShowsASecret(t *testing.T) {
	const cases = 20_000
	random := rand.New(rand.NewPCG(9, 10))
	words := []string{"attest", "x", "_", "-", "0", "%3D", `\n`, "bearer", "Bearer ", "bear", "er", "é", "☃"}
	for i := 0; i < cases; i++ {
		secrets := []string{randomSecret(random), randomSecret(random)}
		cfg := Config{Secrets: secrets}
		d := newDeltaRedactor(cfg)
		longest := 0
		for _, form := range d.forms {
			longest = max(longest, len(form.Text))
		}
		d.keep = longest + 8
		d.capBytes = 4 * d.keep
		var text strings.Builder
		for text.Len() < 6*d.capBytes {
			if random.IntN(3) == 0 {
				// A short secret stands alone after a percent escape too.
				edge := []string{".", "%3D"}[random.IntN(2)]
				text.WriteString(edge + secrets[random.IntN(len(secrets))] + ".")
				continue
			}
			if random.IntN(12) == 0 {
				// A bearer whose spaces outrun a cap release's keep, with a
				// short secret right before it that stands alone once the
				// value arrives.
				spaces := strings.Repeat(" ", 1+random.IntN(2*d.keep))
				text.WriteString("." + secrets[random.IntN(len(secrets))] + "bearer" + spaces + "tok")
				continue
			}
			text.WriteString(words[random.IntN(len(words))])
		}
		var got strings.Builder
		for _, delta := range randomDeltas(random, text.String()) {
			got.WriteString(d.push(delta))
			if len(d.pending) > d.capBytes {
				t.Fatalf("case %d: redactor holds %d bytes, cap %d", i, len(d.pending), d.capBytes)
			}
		}
		got.WriteString(d.flush())
		released := got.String()
		for _, secret := range secrets {
			if len(secret) >= 8 && strings.Contains(released, secret) || strings.Contains(released, "."+secret+".") || strings.Contains(released, "%3D"+secret+".") ||
				strings.Contains(released, "."+secret+"bearer") {
				t.Fatalf("case %d: released %q shows secret %q\ntext %q", i, released, secret, text.String())
			}
		}
	}
}

// A bearer's spaces may run longer than a cap release keeps, so its value
// arrives after the cut. A short key right before "bearer" stands alone once
// it does, so the cut must not split the key, wherever the cap falls.
func TestDeltaRedactorCapKeepsAShortKeyBeforeAnOpenBearer(t *testing.T) {
	cfg := Config{Secrets: []string{"sk-1234"}}
	texts := []string{strings.Repeat("x", 61431) + ".sk-1234bearer" + strings.Repeat(" ", 4092) + "tok end"}
	probe := newDeltaRedactor(cfg)
	// Place the key around the first cap release's limit.
	limit := probe.capBytes + 1 - probe.keep
	for start := limit - 8; start <= limit+1; start++ {
		prefix := strings.Repeat("x", start-1) + ".sk-1234bearer"
		texts = append(texts, prefix+strings.Repeat(" ", probe.capBytes+1-len(prefix))+"tok end")
	}
	for _, text := range texts {
		got := pushAll(t, newDeltaRedactor(cfg), splitEvery(text, 1), probe.capBytes)
		if strings.Contains(got, "sk-1234") || strings.Contains(got, "sk-") || strings.Contains(got, "tok") {
			at := strings.Index(got, ".")
			t.Fatalf("streamed %q, want the key and the bearer value redacted as in %q", got[at:at+30], cfg.clean(text)[at:at+30])
		}
	}
}

// A bounded field is redacted before it is cut: cutting first would leave
// the start of a secret too short to be recognized as one.
func TestBoundedFieldsNeverShowPartOfASecret(t *testing.T) {
	const secret = "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123" // 38 bytes
	cfg := Config{JSON: true, Secrets: []string{secret}}
	call := &model.ToolCall{ID: "c1", Name: "run_command", Arguments: strings.Repeat("a", 4086) + secret}
	result := &tools.Result{Status: tools.Failed, Content: "Error: " + strings.Repeat("e", 390) + secret}
	jsonResult := &tools.Result{Status: tools.Succeeded, Content: strings.Repeat("c", 4086) + secret}

	var stdout strings.Builder
	out := newRenderer(cfg, &stdout, io.Discard)
	out.event(agent.TurnEvent{Kind: "tool_start", ToolCall: call})
	out.event(agent.TurnEvent{Kind: "tool_result", ToolCall: call, ToolResult: jsonResult})
	var stderr strings.Builder
	text := newRenderer(Config{Secrets: cfg.Secrets}, io.Discard, &stderr)
	text.event(agent.TurnEvent{Kind: "tool_result", ToolCall: call, ToolResult: result})

	for name, output := range map[string]string{"json": stdout.String(), "text": stderr.String()} {
		if strings.Contains(output, secret[:8]) {
			t.Fatalf("%s output shows the start of the secret:\n%s", name, output[max(0, len(output)-120):])
		}
	}
	for _, event := range decodeEvents(t, stdout.String()) {
		for _, field := range []string{"arguments", "content"} {
			if value, ok := event[field].(string); ok && len(value) > maxEventField {
				t.Fatalf("%s %s holds %d bytes, more than %d", event["type"], field, len(value), maxEventField)
			}
		}
	}
}
