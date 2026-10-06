package effortprobe

import (
	"fmt"
	"io"
	"strings"

	"likha/internal/model"
)

// Output is plain text without color or Nerd Font glyphs, so it reads the
// same in a terminal, a pipe or a pasted log (spec point 22).

// WritePlan prints everything spec point 6 asks for before the first model
// request: provider, model, endpoint, shapes, levels, request count, cost
// estimate and caps.
func WritePlan(w io.Writer, plan Plan, estimate CostEstimate) error {
	p := &printer{w: w}
	for _, note := range plan.Notes {
		p.line(note)
	}
	p.line("Effort probe plan")
	p.line("  Provider: %s", plan.Provider.Name)
	p.line("  Model:    %s", plan.Model)
	endpoint := plan.Endpoint
	if plan.EndpointOverride {
		endpoint += " (endpoint override)"
	}
	p.line("  Endpoint: %s", endpoint)
	for _, shape := range plan.Shapes {
		p.line("  Wire shape: %s (field %s)", shape.Label(), shapeField(shape))
	}
	p.line("  Levels (%s): %s", plan.LevelSource, levelList(plan.Levels))
	requests := plan.Requests()
	p.line("  Requests: %d (1 Default, %s, %s %q)", len(requests),
		plural(len(plan.Levels)*len(plan.Shapes), "level"), plural(len(plan.Shapes), "control"), ControlLevel)
	p.line("  Estimated cost: %s", estimate.Text())
	p.line("  Caps: at most %d requests; no new request after %s completion tokens (a request without reported usage counts %s); %d s per request; no retries",
		plan.Caps.MaxRequests, formatCount(plan.Caps.TokenBudget), formatCount(plan.Caps.AssumedOutput), int64(plan.Caps.RequestTimeout.Seconds()))
	p.line("  Likha's client re-sends a request once, as on a real turn, only after a rejection that generated nothing (a refused stream_options field, or a ChatGPT token refreshed after HTTP 401); the row records both attempts.")
	p.line("  A single request's output is not capped by the probe; the budget stops new requests only.")
	return p.err
}

// shapeField names the exact body field a shape writes.
func shapeField(s Shape) string {
	if s.Wire == model.EffortTopLevel {
		return `"reasoning_effort": "<level>"`
	}
	return `"reasoning": {"effort": "<level>"}`
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func levelList(levels []Level) string {
	names := make([]string, 0, len(levels))
	for _, l := range levels {
		if l.Published {
			names = append(names, l.Name)
		} else {
			names = append(names, l.Name+" (unpublished)")
		}
	}
	return strings.Join(names, ", ")
}

// Progress prints one short line per finished request, so a long run shows
// it is alive; the verdicts come in the table at the end.
type Progress struct {
	W io.Writer
}

// RowDone implements Reporter.
func (p Progress) RowDone(row Row, total int) {
	status := string(row.Outcome)
	switch {
	case row.Outcome == OutcomeSkipped:
		status = row.Reason
	case row.HTTPStatus != 0:
		status = fmt.Sprintf("HTTP %d, %s", row.HTTPStatus, row.Outcome)
	}
	fmt.Fprintf(p.W, "[%d/%d] %s: %s\n", row.Index, total, rowLabel(row), status)
}

func rowLabel(row Row) string {
	switch row.Kind {
	case KindDefault:
		return "Default (no effort field)"
	case KindControl:
		return fmt.Sprintf("%s = %s (control)", row.Shape, row.Level)
	}
	label := fmt.Sprintf("%s = %s", row.Shape, row.Level)
	if !row.Published {
		label += " (unpublished)"
	}
	return label
}

// WriteTable prints every row with its verdict, then the per-shape summary
// and the heuristic disclaimer (spec points 11, 19, 20).
func WriteTable(w io.Writer, r Report) error {
	p := &printer{w: w}
	p.line("Effort probe results: %s / %s (%s%s)", r.Provider, r.Model, r.Endpoint, overrideMark(r.EndpointOverride))
	for _, row := range r.Rows {
		writeRow(p, r, row)
	}
	writeSummary(p, r)
	p.line("")
	p.line(disclaimer)
	return p.err
}

func overrideMark(override bool) string {
	if override {
		return ", endpoint override"
	}
	return ""
}

func writeRow(p *printer, r Report, row Row) {
	p.line("%2d. %s", row.Index, rowLabel(row))
	if row.Outcome == OutcomeSkipped {
		p.line("    verdict: %s (%s)", row.Verdict, row.Reason)
		return
	}
	sent := row.Sent
	if sent == "" {
		sent = "nothing (the request never left Likha)"
	}
	p.line("    sent: %s; %s; %s; %s", sent, statusText(row), tokenText(row), costText(r, row))
	p.line("    time: %s; tool: %s", timingText(row), toolText(row))
	if row.Error != "" {
		p.line("    error: %s", oneLine(row.Error))
	}
	verdict := fmt.Sprintf("    verdict: %s", row.Verdict)
	if row.Reason != "" {
		verdict += " (" + row.Reason + ")"
	}
	p.line("%s", verdict)
	for _, note := range row.Notes {
		p.line("    note: %s", note)
	}
}

func statusText(row Row) string {
	text := fmt.Sprintf("HTTP %d (%s)", row.HTTPStatus, row.Outcome)
	switch {
	case row.HTTPStatus == 0:
		text = "no HTTP status (" + string(row.Outcome) + ")"
	case row.Outcome == OutcomeOK:
		text = fmt.Sprintf("HTTP %d", row.HTTPStatus)
	}
	if row.Attempts > 1 {
		text += fmt.Sprintf(", re-sent once by Likha's client after HTTP %d", row.FirstAttemptStatus)
	}
	return text
}

func tokenText(row Row) string {
	return fmt.Sprintf("prompt %s, completion %s, reasoning %s",
		countText(row.PromptTokens), countText(row.CompletionTokens), countText(row.ReasoningTokens))
}

func countText(n *int64) string {
	if n == nil {
		return "not reported"
	}
	return formatCount(*n)
}

func costText(r Report, row Row) string {
	if row.CostUSD != nil {
		return fmt.Sprintf("cost $%.4f (%s)", *row.CostUSD, row.CostSource)
	}
	if model.IsSubscription(r.Provider) {
		return "cost: plan allowance"
	}
	return "cost unknown"
}

func timingText(row Row) string {
	return fmt.Sprintf("headers %s, first token %s, end %s", seconds(row.HeadersMS), seconds(row.FirstTokenMS), seconds(row.TotalMS))
}

func seconds(ms *int64) string {
	if ms == nil {
		return "-"
	}
	return fmt.Sprintf("%.2fs", float64(*ms)/1000)
}

func toolText(row Row) string {
	switch {
	case !row.ToolCalled:
		return "not called"
	case row.ToolAnswer == nil:
		return "called, no integer answer"
	case row.AnswerCorrect:
		return fmt.Sprintf("called, answer %d (correct)", *row.ToolAnswer)
	default:
		return fmt.Sprintf("called, answer %d (expected %d)", *row.ToolAnswer, expectedAnswer)
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func writeSummary(p *printer, r Report) {
	p.line("")
	p.line("Summary")
	p.line("  Run: %s", totalsText(r, r.Summary.Totals))
	for _, s := range r.Summary.Shapes {
		p.line("  %s: %s", s.Shape, totalsText(r, s.Totals))
		p.line("    levels %s (signal: %s); control %s", s.Finding, s.Signal, s.Control)
		p.line("    %s", s.Eligibility)
	}
	if r.Summary.Recommended != "" {
		p.line("  Recommended shape: %s", r.Summary.Recommended)
	}
	if r.Stop != nil {
		p.line("  Stopped early: %s", r.Stop.Reason)
	}
	if r.Interrupted {
		p.line("  Interrupted: the report holds the requests observed before Ctrl+C.")
	}
	p.line("  The probe changed no setting; enabling a provider is a separate, reviewed change.")
}

func totalsText(r Report, t Totals) string {
	text := fmt.Sprintf("%d requests sent, %s completion tokens", t.Requests, formatCount(t.CompletionTokens))
	if t.PartlyEstimated {
		text += " (partly estimated)"
	}
	switch {
	case model.IsSubscription(r.Provider):
		text += ", cost: plan allowance"
	case t.CostRows == 0:
		text += ", cost unknown"
	default:
		text += fmt.Sprintf(", cost $%.4f", t.CostUSD)
		var qualifiers []string
		if t.CostEstimated {
			qualifiers = append(qualifiers, "includes catalog estimates")
		}
		if t.CostRows < t.Requests {
			qualifiers = append(qualifiers, fmt.Sprintf("%d of %d requests priced", t.CostRows, t.Requests))
		}
		if len(qualifiers) > 0 {
			text += " (" + strings.Join(qualifiers, "; ") + ")"
		}
	}
	return text
}

// printer keeps the first write error so callers check once.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) line(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format+"\n", args...)
}
