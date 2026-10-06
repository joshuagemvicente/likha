package effortprobe

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"likha/internal/model"
)

// Synthetic rows for the pure verdict function. A reasoning or completion
// count of -1 means the provider did not report it.

func count(n int64) *int64 {
	if n < 0 {
		return nil
	}
	return &n
}

func answered(kind RequestKind, shape, level string, reasoning, completion int64) Row {
	return Row{Kind: kind, Shape: shape, Level: level, Published: kind == KindLevel, Sent: "x", Outcome: OutcomeOK,
		HTTPStatus: http.StatusOK, ReasoningTokens: count(reasoning), CompletionTokens: count(completion)}
}

func failed(kind RequestKind, shape, level string, outcome Outcome, status int) Row {
	return Row{Kind: kind, Shape: shape, Level: level, Published: kind == KindLevel, Sent: "x", Outcome: outcome, HTTPStatus: status}
}

func defaultOK() Row { return answered(KindDefault, "", "", 500, 540) }

func level(name string, reasoning int64) Row {
	return answered(KindLevel, "reasoning_effort", name, reasoning, max(reasoning, 0)+40)
}

func control(outcome Outcome, status int) Row {
	if outcome == OutcomeOK {
		return answered(KindControl, "reasoning_effort", ControlLevel, 500, 540)
	}
	return failed(KindControl, "reasoning_effort", ControlLevel, outcome, status)
}

func lookupProvider(name string) model.Provider {
	p, _ := model.LookupProvider(name)
	return p
}

var validated = control(OutcomeHTTPError, http.StatusBadRequest)
var unvalidated = control(OutcomeOK, http.StatusOK)
var controlTimeout = control(OutcomeTimeout, http.StatusOK)

func topPlan(override bool) Plan {
	return Plan{Provider: lookupProvider("openai"), Shapes: []Shape{shapeTop}, Documented: &shapeTop, EndpointOverride: override}
}

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name        string
		rows        []Row
		want        []Verdict
		finding     Finding
		signal      string
		eligible    bool
		reason      string
		noteRow     int
		note        string
		overrideRun bool
	}{
		{
			name:    "varied with validated control",
			rows:    []Row{defaultOK(), level("low", 100), level("medium", 600), level("high", 2000), validated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictValidated},
			finding: FindingVaried, signal: SignalReasoning, eligible: true,
		},
		{
			name:    "varied with unvalidated control",
			rows:    []Row{defaultOK(), level("low", 100), level("high", 2000), unvalidated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictUnvalidated},
			finding: FindingVaried, signal: SignalReasoning, eligible: true, noteRow: 1, note: noteMapped,
		},
		{
			name:    "flat with validated control",
			rows:    []Row{defaultOK(), level("low", 500), level("high", 520), validated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictValidated},
			finding: FindingFlat, signal: SignalReasoning, eligible: true, noteRow: 2, note: noteValidate,
		},
		{
			name:    "flat within 32 tokens even above 10%",
			rows:    []Row{defaultOK(), level("low", 100), level("high", 130), validated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictValidated},
			finding: FindingFlat, signal: SignalReasoning, eligible: true,
		},
		{
			name:    "flat with unvalidated control",
			rows:    []Row{defaultOK(), level("low", 500), level("high", 540), unvalidated},
			want:    []Verdict{VerdictBaseline, VerdictIgnoredSuspected, VerdictIgnoredSuspected, VerdictUnvalidated},
			finding: FindingFlat, signal: SignalReasoning, reason: "low ignored-suspected",
		},
		{
			name:    "flat with inconclusive control",
			rows:    []Row{defaultOK(), level("low", 500), level("high", 540), controlTimeout},
			want:    []Verdict{VerdictBaseline, VerdictInconclusive, VerdictInconclusive, VerdictInconclusive},
			finding: FindingFlat, signal: SignalReasoning, reason: "low inconclusive (flat; control inconclusive)",
		},
		{
			name: "mixed reported and unreported reasoning falls back to completion",
			rows: []Row{defaultOK(), answered(KindLevel, "reasoning_effort", "low", 100, 140),
				answered(KindLevel, "reasoning_effort", "high", -1, 2040), validated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictValidated},
			finding: FindingVaried, signal: SignalCompletion, eligible: true,
		},
		{
			name: "no usage is unmeasured",
			rows: []Row{defaultOK(), answered(KindLevel, "reasoning_effort", "low", -1, -1),
				answered(KindLevel, "reasoning_effort", "high", -1, -1), unvalidated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictUnvalidated},
			finding: FindingUnmeasured, signal: SignalCompletion, reason: "levels unmeasured and the control was unvalidated",
		},
		{
			name: "one rejected level",
			rows: []Row{defaultOK(), level("low", 100), level("high", 2000),
				failed(KindLevel, "reasoning_effort", "xhigh", OutcomeHTTPError, http.StatusUnprocessableEntity), validated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictRejected, VerdictValidated},
			finding: FindingVaried, signal: SignalReasoning, reason: "xhigh rejected; a catalog override must remove it first",
		},
		{
			name: "one inconclusive level",
			rows: []Row{defaultOK(), level("low", 100), failed(KindLevel, "reasoning_effort", "medium", OutcomeHTTPError, http.StatusServiceUnavailable),
				level("high", 2000), validated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictInconclusive, VerdictAccepted, VerdictValidated},
			finding: FindingVaried, signal: SignalReasoning, reason: "medium inconclusive (HTTP 503)",
		},
		{
			name: "Default 400 with a later success",
			rows: []Row{failed(KindDefault, "", "", OutcomeHTTPError, http.StatusBadRequest), level("none", 0),
				failed(KindLevel, "reasoning_effort", "low", OutcomeHTTPError, http.StatusBadRequest),
				failed(KindControl, "reasoning_effort", ControlLevel, OutcomeHTTPError, http.StatusBadRequest)},
			want:    []Verdict{VerdictFailed, VerdictAccepted, VerdictInconclusive, VerdictInconclusive},
			finding: FindingUnmeasured, signal: SignalReasoning, reason: "Default was failed", noteRow: 1, note: noteDefault,
		},
		{
			name:    "same-as note on a silently mapped level",
			rows:    []Row{defaultOK(), level("low", 100), level("high", 2000), level("xhigh", 2050), validated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictValidated},
			finding: FindingVaried, signal: SignalReasoning, eligible: true, noteRow: 3, note: "same as high (possible downgrade)",
		},
		{
			name:    "endpoint override remark",
			rows:    []Row{defaultOK(), level("low", 100), level("high", 2000), validated},
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictValidated},
			finding: FindingVaried, signal: SignalReasoning, eligible: true,
			reason:      "(endpoint override: not evidence for openai)",
			overrideRun: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := slices.Clone(tc.rows)
			summary := evaluate(topPlan(tc.overrideRun), rows)
			if got := verdicts(rows); !slices.Equal(got, tc.want) {
				t.Fatalf("verdicts = %v, want %v", got, tc.want)
			}
			s := summary.Shapes[0]
			if s.Finding != tc.finding || s.Signal != tc.signal || s.Eligible != tc.eligible {
				t.Fatalf("shape = %+v", s)
			}
			if !strings.Contains(s.Eligibility, tc.reason) {
				t.Fatalf("eligibility = %q, want %q", s.Eligibility, tc.reason)
			}
			if tc.note != "" && !slices.Contains(rows[tc.noteRow].Notes, tc.note) {
				t.Fatalf("row %d notes = %v, want %q", tc.noteRow, rows[tc.noteRow].Notes, tc.note)
			}
			for _, row := range rows {
				if row.Kind == KindDefault {
					continue
				}
				for _, note := range row.Notes {
					if strings.HasPrefix(note, "same as ") && row.Level == "low" {
						t.Fatalf("the first level was compared with Default: %v", row.Notes)
					}
				}
			}
		})
	}
}

func TestEvaluateRecommendsOneShape(t *testing.T) {
	objectLevel := func(name string, reasoning int64) Row {
		return answered(KindLevel, "reasoning.effort", name, reasoning, reasoning+40)
	}
	objectControl := func(outcome Outcome, status int) Row {
		row := control(outcome, status)
		row.Shape = "reasoning.effort"
		return row
	}
	rows := func(objectCtl Row) []Row {
		return []Row{defaultOK(),
			level("low", 100), level("high", 2000), unvalidated,
			objectLevel("low", 100), objectLevel("high", 2000), objectCtl}
	}
	cases := []struct {
		name       string
		documented *Shape
		objectCtl  Row
		want       string
	}{
		{"documented shape wins", &shapeObject, objectControl(OutcomeOK, http.StatusOK), "reasoning.effort"},
		{"validated control wins without a documented shape", nil, objectControl(OutcomeHTTPError, http.StatusBadRequest), "reasoning.effort"},
		{"reasoning_effort otherwise", nil, objectControl(OutcomeOK, http.StatusOK), "reasoning_effort"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := Plan{Provider: lookupProvider("opencode-go"), Shapes: []Shape{shapeTop, shapeObject}, Documented: tc.documented}
			summary := evaluate(plan, rows(tc.objectCtl))
			if !summary.Shapes[0].Eligible || !summary.Shapes[1].Eligible || summary.Recommended != tc.want {
				t.Fatalf("summary = %+v", summary)
			}
		})
	}
}

func TestEvaluateUnpublishedLevelsNeverCount(t *testing.T) {
	rejected := failed(KindLevel, "reasoning_effort", "turbo", OutcomeHTTPError, http.StatusBadRequest)
	rejected.Published = false
	rows := []Row{defaultOK(), level("low", 100), level("high", 2000), rejected, validated}
	summary := evaluate(topPlan(false), rows)
	if rows[3].Verdict != VerdictRejected || !summary.Shapes[0].Eligible {
		t.Fatalf("an unpublished rejected level blocked eligibility: %+v", summary.Shapes[0])
	}
}
