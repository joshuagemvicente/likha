package effortprobe

import (
	"fmt"
	"net/http"
)

// Verdict is a row's judgement (spec points 13–18).
type Verdict string

// Row verdicts.
const (
	VerdictBaseline         Verdict = "baseline"          // Default succeeded
	VerdictFailed           Verdict = "failed"            // Default got HTTP 400/422
	VerdictAccepted         Verdict = "accepted"          // a level succeeded
	VerdictRejected         Verdict = "rejected"          // HTTP 400/422 with effort while Default succeeded
	VerdictIgnoredSuspected Verdict = "ignored-suspected" // heuristic: flat levels and an accepted control
	VerdictInconclusive     Verdict = "inconclusive"
	VerdictValidated        Verdict = "validated"   // the control was rejected: the field is read
	VerdictUnvalidated      Verdict = "unvalidated" // the control succeeded
)

// Finding is a shape's measured spread across published levels.
type Finding string

// Shape findings (spec point 15).
const (
	FindingVaried     Finding = "varied"
	FindingFlat       Finding = "flat"
	FindingUnmeasured Finding = "unmeasured"
)

// Signals a shape can be measured with (spec point 12).
const (
	SignalReasoning  = "reasoning tokens"
	SignalCompletion = "completion tokens"
)

// Thresholds of the flat and "same as" tests. They are Likha's choice, not a
// documented provider property (research.md), which is why
// ignored-suspected is called a heuristic.
const (
	flatRatio    = 0.10
	flatTokens   = 32
	sameAsRatio  = 0.05
	disclaimer   = "Verdicts come from one request per level; ignored-suspected is a heuristic, not proof. Re-run before relying on a borderline result."
	noteValidate = "field validated, no measurable difference"
	noteMapped   = "invalid level also accepted (provider maps or ignores unknown values)"
	noteDefault  = "succeeded where Default failed"
	noteUnpub    = "unpublished level; never counts toward eligibility"
)

// Summary is the per-shape outcome of a run (spec point 20).
type Summary struct {
	Shapes      []ShapeSummary `json:"shapes"`
	Recommended string         `json:"recommended_shape,omitempty"`
	Totals      Totals         `json:"totals"`
}

// ShapeSummary is one shape's finding and eligibility.
type ShapeSummary struct {
	Shape    string  `json:"shape"`
	Signal   string  `json:"signal"`
	Finding  Finding `json:"finding"`
	Control  Verdict `json:"control"`
	Eligible bool    `json:"eligible"`
	// Eligibility is the printed line, including the first reason when
	// the shape is not eligible and the endpoint-override remark.
	Eligibility string `json:"eligibility"`
	Totals      Totals `json:"totals"`
}

// Totals add up the rows that were sent.
type Totals struct {
	Requests         int     `json:"requests"`
	CompletionTokens int64   `json:"completion_tokens"`
	PartlyEstimated  bool    `json:"completion_tokens_partly_estimated"`
	CostUSD          float64 `json:"cost_usd"`
	CostRows         int     `json:"cost_known_requests"`
	CostEstimated    bool    `json:"cost_partly_estimated"`
}

// evaluate sets every row's verdict, reason and notes, and summarises each
// shape. It is a pure function of the plan and the finished rows.
func evaluate(plan Plan, rows []Row) Summary {
	if len(rows) == 0 {
		return Summary{}
	}
	defaultVerdict := judgeDefault(&rows[0])
	summary := Summary{Totals: totals(rows)}
	for _, shape := range plan.Shapes {
		summary.Shapes = append(summary.Shapes, evaluateShape(plan, shape.Name, rows, defaultVerdict))
	}
	summary.Recommended = recommend(plan, summary.Shapes)
	return summary
}

// judgeDefault gives the Default row baseline, failed or inconclusive.
func judgeDefault(row *Row) Verdict {
	switch {
	case row.Outcome == OutcomeOK:
		row.Verdict, row.Reason = VerdictBaseline, "Default succeeded"
	case invalidRequestStatus(*row):
		row.Verdict, row.Reason = VerdictFailed, fmt.Sprintf("HTTP %d without an effort field (the model's own default may refuse this request)", row.HTTPStatus)
	default:
		row.Verdict, row.Reason = VerdictInconclusive, failureReason(*row)
	}
	return row.Verdict
}

// shapeRows holds the indexes of one shape's rows.
type shapeRows struct {
	levels  []int // level rows, plan order
	control int   // -1 when absent
}

func rowsOfShape(rows []Row, shape string) shapeRows {
	s := shapeRows{control: -1}
	for i, row := range rows {
		if row.Shape != shape {
			continue
		}
		switch row.Kind {
		case KindLevel:
			s.levels = append(s.levels, i)
		case KindControl:
			s.control = i
		}
	}
	return s
}

func evaluateShape(plan Plan, shape string, rows []Row, defaultVerdict Verdict) ShapeSummary {
	s := rowsOfShape(rows, shape)
	for _, i := range s.levels {
		if rows[i].Outcome != OutcomeOK {
			judgeFailedLevel(&rows[i], defaultVerdict)
		}
	}
	control := judgeControl(rows, s.control, defaultVerdict)
	signal := chooseSignal(rows, s.levels)
	finding := measure(rows, s.levels, signal)
	for _, i := range s.levels {
		if rows[i].Outcome == OutcomeOK {
			judgeSucceededLevel(&rows[i], finding, control, defaultVerdict)
		}
	}
	addSameAsNotes(rows, s.levels, signal)
	eligible, reason := eligibility(rows, s.levels, defaultVerdict, control, finding)
	return ShapeSummary{
		Shape: shape, Signal: signal, Finding: finding, Control: control, Eligible: eligible,
		Eligibility: eligibilityLine(plan, shape, eligible, reason),
		Totals:      totals(selectRows(rows, s)),
	}
}

func selectRows(rows []Row, s shapeRows) []Row {
	var out []Row
	for _, i := range s.levels {
		out = append(out, rows[i])
	}
	if s.control >= 0 {
		out = append(out, rows[s.control])
	}
	return out
}

// judgeFailedLevel handles a level row that got no answer. A 400/422 is a
// rejection only while Default succeeded (spec points 13–14).
func judgeFailedLevel(row *Row, defaultVerdict Verdict) {
	if row.Outcome == OutcomeSkipped {
		return // verdict and reason were set when it was skipped
	}
	if !invalidRequestStatus(*row) {
		row.Verdict, row.Reason = VerdictInconclusive, failureReason(*row)
		return
	}
	switch defaultVerdict {
	case VerdictBaseline:
		row.Verdict, row.Reason = VerdictRejected, fmt.Sprintf("HTTP %d with effort while Default succeeded", row.HTTPStatus)
	case VerdictFailed:
		row.Verdict, row.Reason = VerdictInconclusive, fmt.Sprintf("HTTP %d while Default also got HTTP 400/422", row.HTTPStatus)
	default:
		row.Verdict, row.Reason = VerdictInconclusive, fmt.Sprintf("HTTP %d while Default was inconclusive", row.HTTPStatus)
	}
}

// judgeControl gives the control row validated, unvalidated or
// inconclusive (spec point 18) and returns it as the shape's control result.
func judgeControl(rows []Row, i int, defaultVerdict Verdict) Verdict {
	if i < 0 {
		return VerdictInconclusive
	}
	row := &rows[i]
	switch {
	case row.Outcome == OutcomeOK:
		row.Verdict, row.Reason = VerdictUnvalidated, "the invalid level was accepted"
	case row.Outcome == OutcomeSkipped:
		// verdict and reason were set when it was skipped
	case invalidRequestStatus(*row) && defaultVerdict == VerdictBaseline:
		row.Verdict, row.Reason = VerdictValidated, fmt.Sprintf("HTTP %d: the invalid level was rejected", row.HTTPStatus)
	case invalidRequestStatus(*row):
		row.Verdict, row.Reason = VerdictInconclusive, fmt.Sprintf("HTTP %d while Default did not succeed", row.HTTPStatus)
	default:
		row.Verdict, row.Reason = VerdictInconclusive, failureReason(*row)
	}
	return row.Verdict
}

// chooseSignal uses reasoning tokens when every successful published level
// of the shape reported them, else completion tokens (spec point 12).
func chooseSignal(rows []Row, levels []int) string {
	succeeded := 0
	for _, i := range levels {
		row := rows[i]
		if !row.Published || row.Outcome != OutcomeOK {
			continue
		}
		succeeded++
		if row.ReasoningTokens == nil {
			return SignalCompletion
		}
	}
	if succeeded == 0 {
		return SignalCompletion
	}
	return SignalReasoning
}

func signalValue(row Row, signal string) (int64, bool) {
	count := row.CompletionTokens
	if signal == SignalReasoning {
		count = row.ReasoningTokens
	}
	if row.Outcome != OutcomeOK || count == nil {
		return 0, false
	}
	return *count, true
}

// measure applies the flat test to the successful published levels.
func measure(rows []Row, levels []int, signal string) Finding {
	var values []int64
	for _, i := range levels {
		if !rows[i].Published {
			continue
		}
		if v, ok := signalValue(rows[i], signal); ok {
			values = append(values, v)
		}
	}
	if len(values) < 2 {
		return FindingUnmeasured
	}
	lo, hi := values[0], values[0]
	for _, v := range values[1:] {
		lo, hi = min(lo, v), max(hi, v)
	}
	if float64(hi) <= float64(lo)*(1+flatRatio) || hi-lo <= flatTokens {
		return FindingFlat
	}
	return FindingVaried
}

// judgeSucceededLevel applies spec point 16 to a level that answered.
func judgeSucceededLevel(row *Row, finding Finding, control, defaultVerdict Verdict) {
	switch {
	case finding == FindingFlat && control == VerdictUnvalidated:
		row.Verdict, row.Reason = VerdictIgnoredSuspected, "heuristic: flat across levels and the invalid control was accepted"
	case finding == FindingFlat && control != VerdictValidated:
		row.Verdict, row.Reason = VerdictInconclusive, "flat; control inconclusive"
	default:
		row.Verdict, row.Reason = VerdictAccepted, fmt.Sprintf("HTTP %d; levels %s", row.HTTPStatus, finding)
		if finding != FindingVaried && control == VerdictValidated {
			row.Notes = append(row.Notes, noteValidate)
		}
		if control == VerdictUnvalidated {
			row.Notes = append(row.Notes, noteMapped)
		}
		if defaultVerdict == VerdictFailed {
			row.Notes = append(row.Notes, noteDefault)
		}
	}
	if !row.Published {
		row.Notes = append(row.Notes, noteUnpub)
	}
}

// addSameAsNotes marks a published level whose signal is within 5% of the
// level before it, a sign of silent downgrading (spec point 17). Default is
// never compared: a model's default level is expected to match it.
func addSameAsNotes(rows []Row, levels []int, signal string) {
	prev := -1
	for _, i := range levels {
		if !rows[i].Published {
			continue
		}
		v, ok := signalValue(rows[i], signal)
		if !ok {
			prev = -1
			continue
		}
		if prev >= 0 {
			p, _ := signalValue(rows[prev], signal)
			if nearlyEqual(p, v) {
				rows[i].Notes = append(rows[i].Notes, fmt.Sprintf("same as %s (possible downgrade)", rows[prev].Level))
			}
		}
		prev = i
	}
}

func nearlyEqual(a, b int64) bool {
	if a == b {
		return true
	}
	diff := float64(max(a, b) - min(a, b))
	return diff <= sameAsRatio*float64(max(a, b))
}

// eligibility applies spec point 20 and returns the first blocking reason.
// Notes never block, and unpublished levels never count.
func eligibility(rows []Row, levels []int, defaultVerdict, control Verdict, finding Finding) (bool, string) {
	if defaultVerdict != VerdictBaseline {
		return false, fmt.Sprintf("Default was %s", defaultVerdict)
	}
	for _, i := range levels {
		row := rows[i]
		if !row.Published || row.Verdict == VerdictAccepted {
			continue
		}
		if row.Verdict == VerdictRejected {
			return false, row.Level + " rejected; a catalog override must remove it first"
		}
		return false, fmt.Sprintf("%s %s (%s)", row.Level, row.Verdict, row.Reason)
	}
	if control != VerdictValidated && finding != FindingVaried {
		return false, fmt.Sprintf("levels %s and the control was %s, so nothing shows the field is read", finding, control)
	}
	return true, ""
}

func eligibilityLine(plan Plan, shape string, eligible bool, reason string) string {
	line := fmt.Sprintf("Eligible to enable %s for %s: ", shape, plan.Provider.Name)
	if eligible {
		line += "yes"
	} else {
		line += "no — " + reason
	}
	if plan.EndpointOverride {
		line += fmt.Sprintf(" (endpoint override: not evidence for %s)", plan.Provider.Name)
	}
	return line
}

// recommend picks one shape when more than one is eligible: the documented
// shape, else the one whose control was validated, else reasoning_effort.
func recommend(plan Plan, shapes []ShapeSummary) string {
	var eligible []ShapeSummary
	for _, s := range shapes {
		if s.Eligible {
			eligible = append(eligible, s)
		}
	}
	switch len(eligible) {
	case 0:
		return ""
	case 1:
		return eligible[0].Shape
	}
	if plan.Documented != nil {
		for _, s := range eligible {
			if s.Shape == plan.Documented.Name {
				return s.Shape
			}
		}
	}
	var validated []string
	for _, s := range eligible {
		if s.Control == VerdictValidated {
			validated = append(validated, s.Shape)
		}
	}
	if len(validated) == 1 {
		return validated[0]
	}
	return shapeTop.Name
}

// invalidRequestStatus reports an HTTP 400 or 422 answer, the statuses a
// provider uses for a field or value it refuses.
func invalidRequestStatus(row Row) bool {
	return row.Outcome == OutcomeHTTPError &&
		(row.HTTPStatus == http.StatusBadRequest || row.HTTPStatus == http.StatusUnprocessableEntity)
}

// failureReason is the one-line reason of an inconclusive row.
func failureReason(row Row) string {
	switch row.Outcome {
	case OutcomeTimeout:
		return "timed out"
	case OutcomeStreamError:
		return "dropped or malformed stream"
	case OutcomeConnection:
		return "connection error"
	case OutcomeInterrupted:
		return "interrupted"
	case OutcomeSkipped:
		return row.Reason
	case OutcomeUnauthorized, OutcomeHTTPError:
		return fmt.Sprintf("HTTP %d", row.HTTPStatus)
	}
	return string(row.Outcome)
}

func totals(rows []Row) Totals {
	var t Totals
	for _, row := range rows {
		if row.Sent == "" {
			continue
		}
		t.Requests++
		t.CompletionTokens += row.BudgetTokens
		t.PartlyEstimated = t.PartlyEstimated || row.BudgetEstimated
		if row.CostUSD != nil {
			t.CostUSD += *row.CostUSD
			t.CostRows++
			t.CostEstimated = t.CostEstimated || row.CostSource == costEstimated
		}
	}
	return t
}
