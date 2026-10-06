package effortprobe

import (
	"fmt"
	"strconv"

	"likha/internal/model"
)

// CostKind says what the cost estimate could establish.
type CostKind string

// Cost estimate kinds (spec point 6).
const (
	CostDollars      CostKind = "dollars"
	CostUnknownPrice CostKind = "unknown"
	CostSubscription CostKind = "subscription"
)

// CostEstimate is the spend preview printed before consent.
type CostEstimate struct {
	Kind          CostKind
	Dollars       float64
	OutputPrice   float64 // US dollars per million output tokens
	InputTokens   int64   // estimated input per request
	AssumedOutput int64   // output tokens assumed per request
	provider      string
	model         string
}

// Estimate prices the plan at the assumed output per request plus the
// estimated input, using the catalog price for the exact provider and model.
func Estimate(plan Plan) CostEstimate {
	e := CostEstimate{
		InputTokens:   estimatedInputTokens(plan.Model),
		AssumedOutput: plan.Caps.AssumedOutput,
		provider:      plan.Provider.Name,
		model:         plan.Model,
	}
	if plan.IsSubscription() {
		e.Kind = CostSubscription
		return e
	}
	input, output, ok := model.ModelPrice(plan.Provider.Name, plan.Model)
	if !ok {
		e.Kind = CostUnknownPrice
		return e
	}
	requests := float64(len(plan.Requests()))
	perRequest := float64(e.AssumedOutput)*output + float64(e.InputTokens)*input
	e.Kind, e.OutputPrice, e.Dollars = CostDollars, output, requests*perRequest/1_000_000
	return e
}

// Text renders the estimate in the words of spec point 6.
func (e CostEstimate) Text() string {
	switch e.Kind {
	case CostSubscription:
		return "uses your ChatGPT plan allowance; no per-token charge"
	case CostUnknownPrice:
		return fmt.Sprintf("cost unknown: no catalog price for %s on %s", e.model, e.provider)
	default:
		return fmt.Sprintf("about %s (assumes up to %s output tokens per request at $%.2f/M; input about %s tokens per request)",
			formatDollars(e.Dollars), formatCount(e.AssumedOutput), e.OutputPrice, formatCount(e.InputTokens))
	}
}

func formatDollars(v float64) string {
	if v > 0 && v < 0.005 {
		return "less than $0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

// formatCount writes n with thousands separators (40000 → "40,000").
func formatCount(n int64) string {
	digits := strconv.FormatInt(n, 10)
	sign := ""
	if n < 0 {
		sign, digits = "-", digits[1:]
	}
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return sign + digits
}
