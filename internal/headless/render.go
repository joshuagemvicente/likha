package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/redact"
	"likha/internal/tools"
)

// SchemaVersion is the --json event schema; fields keep their meaning while
// it stays the same.
const SchemaVersion = 1

// maxEventField bounds arguments and content in events and progress lines.
const maxEventField = 4 << 10

// startInfo opens every run's output.
type startInfo struct {
	SessionID, Repository, Provider, Model, Effort, Variant string
}

// renderer writes one run's output. Errors are kept and reported by finish,
// so a closed stdout fails the run instead of being ignored; writeErr
// reports the first one as soon as it happens.
type renderer interface {
	start(info startInfo)
	event(ev agent.TurnEvent)
	finish(result runResult) error
	writeErr() error
}

func newRenderer(cfg Config, stdout, stderr io.Writer) renderer {
	if cfg.JSON {
		return &jsonRenderer{cfg: cfg, out: json.NewEncoder(stdout),
			text: newDeltaRedactor(cfg), reasoning: newDeltaRedactor(cfg)}
	}
	return &textRenderer{cfg: cfg, stdout: stdout, stderr: stderr}
}

// autoApprovedReason is why a tool call ran without a prompt, empty when
// someone approved it. It comes from the agent's structured field, never
// from the content: a file or a command's output may hold a line that looks
// like the agent's label.
func autoApprovedReason(cfg Config, result *tools.Result) string {
	return cfg.clean(result.AutoApproved)
}

// textRenderer prints the final answer on stdout and plain progress lines on
// stderr, without color or cursor movement. Only stdout carries the answer,
// so only a failed stdout write is an output failure (err); an unusable
// stderr (2>&-) just ends the progress lines (progressErr).
type textRenderer struct {
	cfg            Config
	stdout, stderr io.Writer
	err            error
	progressErr    error
}

// line writes one progress line. Nothing more is written after either
// stream failed: a failed answer is reported by Run, not followed by a
// summary that reads as success.
func (t *textRenderer) line(format string, args ...any) {
	if t.err != nil || t.progressErr != nil {
		return
	}
	_, t.progressErr = fmt.Fprintf(t.stderr, format+"\n", args...)
}

func (t *textRenderer) writeErr() error { return t.err }

func (t *textRenderer) start(info startInfo) {
	t.line("likha: headless run in %s with %s / %s (variant %s, session %s)", info.Repository, info.Provider, info.Model, info.Variant, info.SessionID)
}

func (t *textRenderer) event(ev agent.TurnEvent) {
	switch ev.Kind {
	case "notice":
		t.line("likha: note: %s", firstLine(t.cfg.clean(ev.Text)))
	case "auto_compacted":
		t.line("likha: note: %s", compactionTrigger(ev.ContextTokens, ev.ContextWindow))
	case "tool_result":
		if ev.ToolCall == nil || ev.ToolResult == nil {
			return
		}
		// The model names the call, so its name can carry anything.
		name := t.cfg.clean(ev.ToolCall.Name)
		switch outcome := toolOutcome(ev.ToolResult); outcome {
		case "ok":
			if reason := autoApprovedReason(t.cfg, ev.ToolResult); reason != "" {
				t.line("likha: tool %s: auto-approved (%s)", name, reason)
				return
			}
			t.line("likha: tool %s: ok", name)
		default:
			// Redact before cutting: a cut through a secret would leave a
			// piece too short to be recognized.
			t.line("likha: tool %s: %s: %s", name, outcome, bound(errorLine(t.cfg.clean(ev.ToolResult.Content)), 400))
		}
	}
	// A run error is reported once, by finish.
}

func (t *textRenderer) finish(result runResult) error {
	if result.Status == statusCompleted || result.saveFailed {
		if result.Text != "" && t.err == nil {
			_, t.err = fmt.Fprintln(t.stdout, result.Text)
		}
	}
	if result.Err != "" {
		t.line("likha: %s", result.Err)
	}
	t.line("likha: %s", summary(result))
	return t.err
}

// summary is the final progress line.
func summary(result runResult) string {
	parts := []string{result.Status, "session " + result.SessionID,
		plural(result.Totals.Requests, "request"), plural(result.Totals.ToolCalls, "tool call"),
		plural(result.Totals.Refusals, "refusal")}
	if result.Totals.usageSeen {
		parts = append(parts, fmt.Sprintf("%d prompt + %d completion tokens", result.Totals.Prompt, result.Totals.Completion))
		if result.Totals.CostKnown {
			parts = append(parts, fmt.Sprintf("$%.4f", result.Totals.Cost))
		}
	}
	return strings.Join(parts, " · ")
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// errorLine is the line of a failed or refused result that names why.
func errorLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "Error: ") {
			return strings.TrimPrefix(line, "Error: ")
		}
	}
	return firstLine(content)
}

// jsonRenderer writes one JSON object per line on stdout.
type jsonRenderer struct {
	cfg Config
	out *json.Encoder
	err error
	// text and reasoning redact streamed deltas, which may split a secret.
	text, reasoning *deltaRedactor
}

// deltaRedactor redacts one stream (text or reasoning) of one model
// response as it arrives, so a secret split across deltas is never printed.
//
// It releases text only up to the last safe cut: a whitespace byte that does
// not follow "bearer". No secret form contains whitespace (see incremental),
// and the only redaction that spans whitespace is a bearer authorization
// value, whose spaces after "bearer" are never a cut. Every released chunk
// therefore starts and ends at a byte no redaction includes, where a short
// secret's word edge reads the same as in the whole response, so redacting
// each chunk on its own (clean) gives exactly the whole response's
// redaction. What is held is only the partial word after the last cut; a
// notice or the other stream's delta never forces it out, and it joins the
// stream's next delta. The response's end (see jsonRenderer.endSegment)
// releases the rest: a secret cannot continue into the next response.
type deltaRedactor struct {
	// secrets lists what the stream redacts. It can grow during a run (a
	// sign-in rotating its tokens), so push and flush read it again;
	// secretCount is how many it listed when forms were computed.
	secrets     func() []string
	secretCount int
	forms       []redact.Form
	// incremental is false when a secret form contains whitespace (a custom
	// key with a space in it): a cut at whitespace could then split it, so
	// everything is held until the end of the response, or the cap.
	incremental bool
	// capBytes bounds pending when no safe cut arrives (a long run of text
	// without whitespace); keep is how much a cap release leaves held, enough
	// to see the whole of any secret form that starts before its cut.
	capBytes, keep int
	pending        []byte
	// cut is the end of pending's last safe cut, 0 when it has none.
	cut  int
	scan cutScanner
	// suppress drops text up to the next safe cut after a cap release ended
	// inside a redaction, whose marker was already written.
	suppress bool
}

// minDeltaCap is the least text a stream holds while it waits for a safe
// cut; bearerAllowance is what a cap release keeps beyond the longest secret
// form, which covers a partial "bearer" and a short secret's next byte.
const (
	minDeltaCap     = 64 << 10
	bearerAllowance = 4 << 10
)

func newDeltaRedactor(cfg Config) *deltaRedactor {
	d := &deltaRedactor{secrets: cfg.secrets}
	d.setSecrets(cfg.secrets())
	return d
}

// setSecrets computes the forms and bounds for secrets.
func (d *deltaRedactor) setSecrets(secrets []string) {
	d.secretCount = len(secrets)
	d.forms = redact.Forms(secrets...)
	incremental := true
	longest := 0
	for _, form := range d.forms {
		longest = max(longest, len(form.Text))
		if strings.ContainsFunc(form.Text, func(r rune) bool { return r < utf8.RuneSelf && redact.IsSpace(byte(r)) }) {
			incremental = false
		}
	}
	if !incremental {
		// The cuts found so far assumed no form spans whitespace.
		d.cut = 0
	}
	d.incremental = incremental
	d.keep = longest + bearerAllowance
	// A cap release keeps at most a quarter of what it held, so every byte
	// is searched for secrets a bounded number of times.
	d.capBytes = max(minDeltaCap, 4*d.keep)
}

// refreshSecrets recomputes the forms when the secret list grew since they
// were computed. The list only grows (Config.secrets), so its length tells.
// Nothing held was redacted yet, so held text is redacted with the new
// forms too.
func (d *deltaRedactor) refreshSecrets() {
	if secrets := d.secrets(); len(secrets) != d.secretCount {
		d.setSecrets(secrets)
	}
}

// push adds a delta and returns the redacted text that is safe to release.
// Only the delta's own bytes are searched for a cut.
func (d *deltaRedactor) push(delta string) string {
	d.refreshSecrets()
	from := len(d.pending)
	d.pending = append(d.pending, delta...)
	if d.incremental {
		for i := from; i < len(d.pending); i++ {
			if !d.scan.cutAfter(d.pending[i]) {
				continue
			}
			if d.suppress {
				// The suppressed redaction ends before this whitespace.
				d.pending = append(d.pending[:0], d.pending[i:]...)
				i, d.suppress = 0, false
			}
			d.cut = i + 1
		}
	}
	if d.suppress {
		d.pending = d.pending[:0]
		return ""
	}
	released := d.release(d.cut)
	if len(d.pending) > d.capBytes {
		released += d.capRelease()
	}
	return released
}

// flush releases everything still held, redacted, at the end of the
// response, and forgets the response.
func (d *deltaRedactor) flush() string {
	d.refreshSecrets()
	released := ""
	if !d.suppress {
		released = d.clean(string(d.pending))
	}
	d.pending, d.cut, d.scan, d.suppress = d.pending[:0], 0, cutScanner{}, false
	return released
}

// clean is Config.clean with the stream's forms listed once: a stream
// redacts every chunk it releases.
func (d *deltaRedactor) clean(text string) string {
	return redact.Apply(strings.ToValidUTF8(text, "�"), d.forms)
}

// release returns pending[:cut] redacted and keeps the rest.
func (d *deltaRedactor) release(cut int) string {
	if cut <= 0 {
		return ""
	}
	released := d.clean(string(d.pending[:cut]))
	d.pending = append(d.pending[:0], d.pending[cut:]...)
	d.cut = 0
	return released
}

// capRelease releases most of pending when it outgrew capBytes without a
// safe cut. It cuts keep bytes before the end, or earlier where
// redact.SplitBefore must, so no secret is split; the text at the cut may be
// redacted more than the whole text would be (an over-redaction, never a
// leak). When one redaction covers most of pending (a secret repeated
// through a long word, a huge bearer value), it writes the marker and drops
// text up to the next safe cut.
func (d *deltaRedactor) capRelease() string {
	text := string(d.pending)
	cut := redact.SplitBefore(text, len(text)-d.keep, d.forms)
	if cut >= len(text)/2 {
		return d.release(cut)
	}
	released := d.clean(text[:cut]) + redact.Marker
	d.pending, d.cut, d.suppress = d.pending[:0], 0, true
	return released
}

// cutScanner finds safe cuts in a stream byte by byte: whitespace that does
// not follow "bearer" (in any case, as redact's bearer pattern matches it).
type cutScanner struct {
	// word holds the last bytes of the current word, lowercased; words are
	// how many of them are set.
	word  [len("bearer")]byte
	words int
	// space means the last byte was whitespace; afterBearer that its run of
	// whitespace follows "bearer".
	space, afterBearer bool
}

// cutAfter takes the stream's next byte and reports whether the stream can
// be cut right after it.
func (s *cutScanner) cutAfter(b byte) bool {
	if !redact.IsSpace(b) {
		if s.space {
			s.space, s.words = false, 0
		}
		copy(s.word[:], s.word[1:])
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		s.word[len(s.word)-1] = b
		s.words = min(s.words+1, len(s.word))
		return false
	}
	if !s.space {
		s.space = true
		s.afterBearer = s.words == len(s.word) && string(s.word[:]) == "bearer"
	}
	return !s.afterBearer
}

func (j *jsonRenderer) writeDelta(kind, delta string) {
	if delta != "" {
		j.write(deltaEvent{Type: kind, Delta: delta})
	}
}

// endSegment releases both streams' held text at the end of a model
// response, before the event that ends it, and starts the next response
// afresh. The agent emits tool_start, tool_result, usage and
// auto_compacted only between responses, never while one streams.
func (j *jsonRenderer) endSegment() {
	j.writeDelta("reasoning", j.reasoning.flush())
	j.writeDelta("text", j.text.flush())
}

func (j *jsonRenderer) write(value any) {
	if j.err != nil {
		return
	}
	j.err = j.out.Encode(value)
}

type startEvent struct {
	Type       string `json:"type"`
	Schema     int    `json:"schema"`
	SessionID  string `json:"session_id"`
	Repository string `json:"repository"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
	Variant    string `json:"variant"`
}

type deltaEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

type toolStartEvent struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolResultEvent struct {
	Type         string `json:"type"`
	CallID       string `json:"call_id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	AutoApproved string `json:"auto_approved,omitempty"`
	Content      string `json:"content"`
}

type noticeEvent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usageEvent struct {
	Type             string   `json:"type"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	ReasoningTokens  int64    `json:"reasoning_tokens"`
	CachedTokens     int64    `json:"cached_tokens"`
	Cost             *float64 `json:"cost,omitempty"`
}

type resultEvent struct {
	Model            string   `json:"model"`
	Variant          string   `json:"variant"`
	Type             string   `json:"type"`
	Status           string   `json:"status"`
	Text             string   `json:"text"`
	SessionID        string   `json:"session_id"`
	Requests         int      `json:"requests"`
	ToolCalls        int      `json:"tool_calls"`
	Refusals         int      `json:"refusals"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	ReasoningTokens  int64    `json:"reasoning_tokens"`
	Cost             *float64 `json:"cost,omitempty"`
	Error            string   `json:"error,omitempty"`
}

func (j *jsonRenderer) writeErr() error { return j.err }

func (j *jsonRenderer) start(info startInfo) {
	j.write(startEvent{Type: "start", Schema: SchemaVersion, SessionID: info.SessionID, Repository: info.Repository,
		Provider: info.Provider, Model: info.Model, Effort: info.Effort, Variant: info.Variant})
}

func (j *jsonRenderer) event(ev agent.TurnEvent) {
	switch ev.Kind {
	case "text":
		// Each stream holds only its own partial word, which the other
		// stream's deltas leave held (see deltaRedactor).
		j.writeDelta("text", j.text.push(ev.Text))
	case "reasoning":
		j.writeDelta("reasoning", j.reasoning.push(ev.Text))
	case "tool_start":
		j.endSegment()
		if ev.ToolCall != nil {
			// The model names the call and its ID, so both can carry anything.
			j.write(toolStartEvent{Type: "tool_start", CallID: j.cfg.clean(ev.ToolCall.ID), Name: j.cfg.clean(ev.ToolCall.Name),
				Arguments: bound(j.cfg.clean(ev.ToolCall.Arguments), maxEventField)})
		}
	case "tool_result":
		j.endSegment()
		if ev.ToolCall != nil && ev.ToolResult != nil {
			j.write(toolResultEvent{Type: "tool_result", CallID: j.cfg.clean(ev.ToolCall.ID), Name: j.cfg.clean(ev.ToolCall.Name),
				Status: toolOutcome(ev.ToolResult), AutoApproved: autoApprovedReason(j.cfg, ev.ToolResult),
				Content: bound(j.cfg.clean(ev.ToolResult.Content), maxEventField)})
		}
	case "notice":
		// A notice (a background hook's warning, say) can arrive in the
		// middle of either stream, which then continues: everything up to
		// each stream's last safe cut is already out, and a partial word
		// stays held to join the rest of its stream.
		j.write(noticeEvent{Type: "notice", Text: j.cfg.clean(ev.Text)})
	case "usage":
		j.endSegment()
		if ev.Usage != nil {
			j.writeUsage(*ev.Usage)
		}
	case "auto_compacted":
		// Schema 1 has no compaction event: the summarize request is one
		// more usage event and the compaction a notice naming its trigger.
		j.endSegment()
		if ev.Usage != nil {
			j.writeUsage(*ev.Usage)
		}
		j.write(noticeEvent{Type: "notice", Text: compactionTrigger(ev.ContextTokens, ev.ContextWindow)})
	case "stream_interrupted", "error", "done":
		// The response ended without another event to precede.
		j.endSegment()
	}
}

// writeUsage writes one model request's usage event.
func (j *jsonRenderer) writeUsage(usage model.RequestUsage) {
	event := usageEvent{Type: "usage", PromptTokens: usage.Prompt, CompletionTokens: usage.Completion,
		ReasoningTokens: usage.Reasoning, CachedTokens: usage.CacheRead}
	if cost, ok := requestCost(j.cfg, usage); ok {
		event.Cost = &cost
	}
	j.write(event)
}

func (j *jsonRenderer) finish(result runResult) error {
	j.endSegment()
	event := resultEvent{Type: "result", Status: result.Status, SessionID: result.SessionID,
		Model: j.cfg.Client.Model(), Variant: effortName(j.cfg.Variant),
		Requests: result.Totals.Requests, ToolCalls: result.Totals.ToolCalls, Refusals: result.Totals.Refusals,
		PromptTokens: result.Totals.Prompt, CompletionTokens: result.Totals.Completion, ReasoningTokens: result.Totals.Reasoning,
		Error: result.Err}
	if result.Status == statusCompleted || result.saveFailed {
		event.Text = result.Text
	}
	if result.Totals.usageSeen && result.Totals.CostKnown {
		cost := result.Totals.Cost
		event.Cost = &cost
	}
	j.write(event)
	return j.err
}
