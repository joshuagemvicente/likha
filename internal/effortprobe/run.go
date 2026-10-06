package effortprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"likha/internal/model"
)

// Outcome is what happened to one request, before any verdict.
type Outcome string

// Request outcomes. Only OutcomeOK produced a model answer.
const (
	OutcomeOK           Outcome = "ok"
	OutcomeHTTPError    Outcome = "http_error"   // the provider answered with an error status
	OutcomeUnauthorized Outcome = "unauthorized" // HTTP 401/403; stops the run
	OutcomeTimeout      Outcome = "timeout"      // the per-request cap cancelled it
	OutcomeStreamError  Outcome = "stream_error" // a 2xx stream that dropped or was malformed
	OutcomeConnection   Outcome = "connection_error"
	OutcomeSkipped      Outcome = "skipped"     // never sent: a cap or an earlier stop
	OutcomeInterrupted  Outcome = "interrupted" // cancelled by Ctrl+C
)

// Row is one request's record (spec point 11). Counts the provider did not
// report are nil, never zero.
type Row struct {
	Index     int         `json:"index"`
	Kind      RequestKind `json:"kind"`
	Shape     string      `json:"shape,omitempty"`
	Level     string      `json:"level,omitempty"`
	Published bool        `json:"published"`
	// Sent is the effort fragment as seen in the outgoing body, "none" when
	// the body carried no effort field, or "" when nothing was sent.
	Sent             string   `json:"sent"`
	Outcome          Outcome  `json:"outcome"`
	HTTPStatus       int      `json:"http_status,omitempty"`
	Error            string   `json:"error,omitempty"`
	PromptTokens     *int64   `json:"prompt_tokens"`
	CompletionTokens *int64   `json:"completion_tokens"`
	ReasoningTokens  *int64   `json:"reasoning_tokens"`
	CostUSD          *float64 `json:"cost_usd"`
	CostSource       string   `json:"cost_source"`
	HeadersMS        *int64   `json:"time_to_headers_ms"`
	FirstTokenMS     *int64   `json:"time_to_first_token_ms"`
	TotalMS          *int64   `json:"total_ms"`
	ToolCalled       bool     `json:"tool_called"`
	ToolAnswer       *int64   `json:"tool_answer"`
	AnswerCorrect    bool     `json:"answer_correct"`
	// BudgetTokens is what the row counted toward the completion-token
	// budget; BudgetEstimated marks the assumed count of a row without usage.
	BudgetTokens    int64    `json:"budget_tokens"`
	BudgetEstimated bool     `json:"budget_tokens_estimated"`
	Verdict         Verdict  `json:"verdict"`
	Reason          string   `json:"reason"`
	Notes           []string `json:"notes,omitempty"`
}

// StopCause says why a run ended before its last planned request.
type StopCause string

// Stop causes. Budget is an expected end; the others fail the run.
const (
	StopBudget        StopCause = "budget"
	StopDefaultFailed StopCause = "default_failed"
	StopUnauthorized  StopCause = "unauthorized"
	StopInterrupted   StopCause = "interrupted"
)

// Stop records an early end.
type Stop struct {
	Cause  StopCause `json:"cause"`
	Reason string    `json:"reason"`
}

// Failed reports whether the stop makes the run fail (exit 1).
func (s *Stop) Failed() bool {
	return s != nil && (s.Cause == StopDefaultFailed || s.Cause == StopUnauthorized)
}

// Reporter receives each row as soon as its request ends, for progress.
type Reporter interface {
	RowDone(row Row, total int)
}

// Run sends the plan's requests one at a time through client and returns
// the report with verdicts. It never retries a row (decision 10) and never
// changes client: every request goes through its own observed fork with the
// effort forced on that fork only. An error means nothing could be run.
func Run(ctx context.Context, client *model.Client, plan Plan, out Reporter) (Report, error) {
	if client == nil {
		return Report{}, errors.New("effort probe: model client is required")
	}
	requests := plan.Requests()
	if len(requests) > plan.Caps.MaxRequests {
		return Report{}, fmt.Errorf("effort probe: %d requests exceed the cap of %d", len(requests), plan.Caps.MaxRequests)
	}
	r := runner{client: client, plan: plan, secrets: append(slices.Clone(plan.secrets), client.APIKey())}
	report := newReport(plan, time.Now())
	var spent int64
	for i, req := range requests {
		var row Row
		switch {
		case report.Stop != nil:
			row = skippedRow(i, req, "skipped: "+report.Stop.Reason)
		case spent >= plan.Caps.TokenBudget:
			report.Stop = &Stop{Cause: StopBudget, Reason: fmt.Sprintf("completion-token budget of %s reached", formatCount(plan.Caps.TokenBudget))}
			row = skippedRow(i, req, "skipped: "+report.Stop.Reason)
		default:
			row = r.send(ctx, i, req)
			spent += row.BudgetTokens
			report.Stop = stopAfter(req, row)
		}
		report.Rows = append(report.Rows, row)
		if out != nil {
			out.RowDone(row, len(requests))
		}
	}
	report.Interrupted = report.Stop != nil && report.Stop.Cause == StopInterrupted
	report.EndedAt = time.Now().UTC()
	report.Summary = evaluate(plan, report.Rows)
	return report, nil
}

// stopAfter says whether a finished row ends the run: Ctrl+C, a 401/403,
// or a Default failure other than HTTP 400/422 (decision 4).
func stopAfter(req Request, row Row) *Stop {
	switch {
	case row.Outcome == OutcomeInterrupted:
		return &Stop{Cause: StopInterrupted, Reason: "interrupted"}
	case row.Outcome == OutcomeUnauthorized:
		return &Stop{Cause: StopUnauthorized, Reason: fmt.Sprintf("credentials rejected (HTTP %d)", row.HTTPStatus)}
	case req.Kind == KindDefault && row.Outcome != OutcomeOK && !invalidRequestStatus(row):
		return &Stop{Cause: StopDefaultFailed, Reason: "Default failed (" + failureReason(row) + "); nothing about effort can be learned"}
	}
	return nil
}

func skippedRow(index int, req Request, reason string) Row {
	row := baseRow(index, req)
	row.Outcome, row.Verdict, row.Reason = OutcomeSkipped, VerdictInconclusive, reason
	return row
}

func baseRow(index int, req Request) Row {
	row := Row{Index: index + 1, Kind: req.Kind, Level: req.Level, Published: req.Published, CostSource: costUnknown}
	if req.Kind != KindDefault {
		row.Shape = req.Shape.Name
	}
	return row
}

type runner struct {
	client  *model.Client
	plan    Plan
	secrets []string
}

// send runs one request and records what was observed; verdicts come later.
func (r runner) send(ctx context.Context, index int, req Request) Row {
	row := baseRow(index, req)
	rec := &recorder{}
	fork, err := r.client.ForkWithTransport(rec.wrap)
	if err != nil {
		row.Outcome, row.Error = OutcomeConnection, redact(err.Error(), r.secrets...)
		if errors.Is(err, model.ErrUnauthorized) {
			row.Outcome = OutcomeUnauthorized
		}
		return row
	}
	if req.Kind == KindDefault {
		fork.SetEffort(model.EffortUnsupported, "")
	} else {
		fork.SetEffort(req.Shape.Wire, req.Level)
	}
	reqCtx, cancel := context.WithTimeout(ctx, r.plan.Caps.RequestTimeout)
	defer cancel()
	first := &firstToken{}
	rec.start(time.Now())
	reply, usage, usageOK, err := fork.StreamUsage(reqCtx, probeMessages(), probeTools(), first.mark, first.mark)
	total := rec.since()
	obs := rec.snapshot()
	row.Sent, row.HTTPStatus = obs.fragment, obs.status
	row.TotalMS = millis(total)
	if obs.attempts > 0 && obs.status != 0 {
		row.HeadersMS = millis(obs.headers)
	}
	if at, ok := first.since(obs.began); ok {
		row.FirstTokenMS = millis(at)
	}
	if err != nil {
		row.Outcome = classify(ctx, reqCtx, err, obs)
		row.Error = redact(err.Error(), r.secrets...)
	} else {
		row.Outcome = OutcomeOK
		r.recordAnswer(&row, reply, usage, usageOK)
	}
	r.chargeBudget(&row)
	return row
}

// classify names a failed request's outcome from typed errors and the
// observed status, never from error text.
func classify(parent, reqCtx context.Context, err error, obs observation) Outcome {
	var rejected *model.EffortRejectedError
	switch {
	case parent.Err() != nil:
		return OutcomeInterrupted
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded):
		return OutcomeTimeout
	case errors.Is(err, model.ErrUnauthorized):
		return OutcomeUnauthorized
	case errors.As(err, &rejected):
		return OutcomeHTTPError
	case obs.status != 0 && (obs.status < 200 || obs.status >= 300):
		return OutcomeHTTPError
	case obs.status != 0:
		return OutcomeStreamError
	default:
		return OutcomeConnection
	}
}

func (r runner) recordAnswer(row *Row, reply model.Message, usage model.RequestUsage, usageOK bool) {
	row.ToolCalled, row.ToolAnswer = toolAnswer(reply)
	row.AnswerCorrect = row.ToolAnswer != nil && *row.ToolAnswer == expectedAnswer
	if !usageOK {
		return
	}
	if usage.PromptSeen {
		row.PromptTokens = int64Ptr(usage.Prompt)
	}
	if usage.CompletionSeen {
		row.CompletionTokens = int64Ptr(usage.Completion)
	}
	if usage.ReasoningSeen {
		row.ReasoningTokens = int64Ptr(usage.Reasoning)
	}
	cost, source := model.RequestCost(r.plan.Provider.Name, r.plan.Model, usage)
	switch source {
	case model.CostReported:
		row.CostUSD, row.CostSource = &cost, costReported
	case model.CostEstimated:
		row.CostUSD, row.CostSource = &cost, costEstimated
	}
}

// chargeBudget counts the row toward the completion-token budget. A row
// without a reported completion count is charged the assumed output when
// the model may have generated: an answer, a timeout, a dropped stream or a
// lost connection. An error status from the provider generated nothing.
func (r runner) chargeBudget(row *Row) {
	if row.CompletionTokens != nil {
		row.BudgetTokens = *row.CompletionTokens
		return
	}
	switch row.Outcome {
	case OutcomeHTTPError, OutcomeUnauthorized, OutcomeSkipped:
		return
	}
	if row.Sent == "" {
		return
	}
	row.BudgetTokens, row.BudgetEstimated = r.plan.Caps.AssumedOutput, true
}

func int64Ptr(v int64) *int64 { return &v }

func millis(d time.Duration) *int64 { return int64Ptr(d.Milliseconds()) }

// firstToken records when the first streamed text or reasoning arrived.
type firstToken struct {
	mu sync.Mutex
	at time.Time
}

func (f *firstToken) mark(string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.at.IsZero() {
		f.at = time.Now()
	}
}

func (f *firstToken) since(start time.Time) (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.at.IsZero() || start.IsZero() {
		return 0, false
	}
	return f.at.Sub(start), true
}

// recorder observes one row's HTTP attempts through ForkWithTransport. It
// keeps the last attempt's status, its header latency and the effort
// fragment of the outgoing body. It never reads request headers, which
// carry the credential, and reads the body only through GetBody so the
// forwarded request keeps its own.
type recorder struct {
	mu  sync.Mutex
	obs observation
}

type observation struct {
	began    time.Time
	attempts int
	status   int
	headers  time.Duration
	fragment string
}

func (r *recorder) wrap(next http.RoundTripper) http.RoundTripper {
	return &recordingTransport{next: next, rec: r}
}

func (r *recorder) start(at time.Time) {
	r.mu.Lock()
	r.obs.began = at
	r.mu.Unlock()
}

func (r *recorder) since() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Since(r.obs.began)
}

func (r *recorder) snapshot() observation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.obs
}

type recordingTransport struct {
	next http.RoundTripper
	rec  *recorder
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost {
		return t.next.RoundTrip(req)
	}
	fragment := effortFragment(req)
	resp, err := t.next.RoundTrip(req)
	t.rec.mu.Lock()
	defer t.rec.mu.Unlock()
	t.rec.obs.attempts++
	t.rec.obs.fragment = fragment
	t.rec.obs.status = 0
	if err == nil {
		t.rec.obs.status = resp.StatusCode
		t.rec.obs.headers = time.Since(t.rec.obs.began)
	}
	return resp, err
}

// effortFields are the body fields that can carry effort, in report order.
var effortFields = []string{"reasoning_effort", "reasoning", "output_config"}

// effortFragment returns the effort part of the outgoing JSON body exactly
// as sent, for example "reasoning_effort":"high", or "none". Nothing else
// of the body is kept (spec point 25).
func effortFragment(req *http.Request) string {
	if req.GetBody == nil {
		return "unreadable body"
	}
	body, err := req.GetBody()
	if err != nil {
		return "unreadable body"
	}
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil {
		return "unreadable body"
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return "unreadable body"
	}
	var parts []string
	for _, name := range effortFields {
		value, ok := fields[name]
		if !ok {
			continue
		}
		var compact bytes.Buffer
		if json.Compact(&compact, value) != nil {
			return "unreadable body"
		}
		parts = append(parts, fmt.Sprintf("%q:%s", name, compact.String()))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}
