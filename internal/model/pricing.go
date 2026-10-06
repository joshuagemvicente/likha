// Request pricing (specs/model-metadata): the cost of one model request in
// US dollars, from the provider's reported cost when the response carries
// one, or the bundled catalog's price
// for the exact (provider, model) pair applied to the request's token
// breakdown. Anything else is unknown: callers hide spend rather than
// estimate, and no price is ever borrowed from another provider, a similar
// model, or a family prefix.
package model

import (
	"math"
	"strings"

	"likha/internal/model/catalog"
)

// RequestUsage is one model request's token breakdown as the provider
// reported it. CacheRead and CacheWrite are subsets of Prompt; Reasoning is
// a subset of Completion (usage parsing folds a provider's additive
// reasoning count, such as xAI's, into Completion first). Cost is the
// provider-charged amount in US dollars when CostSeen.
type RequestUsage struct {
	Prompt         int64
	Completion     int64
	PromptSeen     bool
	CompletionSeen bool
	CacheRead      int64
	CacheWrite     int64
	Reasoning      int64
	Cost           float64
	CostSeen       bool
}

// CostSource says how a request's cost was obtained.
type CostSource uint8

const (
	// CostUnknown: no reported cost, no applicable catalog price, or
	// incomplete token counts. Callers must not show a number.
	CostUnknown CostSource = iota
	// CostReported: the provider stated the charge in the response (exact).
	CostReported
	// CostSubscription is reserved for compatibility with the earlier pricing
	// API. A plan provider alone no longer establishes an exact zero charge.
	CostSubscription
	// CostEstimated: catalog price × reported tokens.
	CostEstimated
)

// Known reports whether the source yields a cost at all.
func (s CostSource) Known() bool { return s != CostUnknown }

// Exact reports whether the cost is the provider's own figure rather than
// Likha's estimate.
func (s CostSource) Exact() bool { return s == CostReported || s == CostSubscription }

// RequestCost prices one request for provider (Likha canonical name) and
// modelID. See the package comment for the order of sources.
func RequestCost(provider, modelID string, u RequestUsage) (float64, CostSource) {
	if u.CostSeen && u.Cost >= 0 && !math.IsNaN(u.Cost) && !math.IsInf(u.Cost, 0) {
		return u.Cost, CostReported
	}
	if IsSubscription(provider) {
		// SIWC consumes a shared plan allowance; the user may separately opt
		// into credits in ChatGPT Settings. Neither those settings nor a
		// confirmed charge can be inferred from this provider identity.
		return 0, CostUnknown
	}
	if !u.PromptSeen || !u.CompletionSeen {
		return 0, CostUnknown
	}
	entry, ok := catalog.Lookup(provider, modelID)
	if !ok || entry.Cost == nil {
		return 0, CostUnknown
	}
	return estimate(*entry.Cost, u), CostEstimated
}

// estimate applies a price card to a token breakdown. Subsets are clamped
// so a malformed report can never price more tokens than the totals, and a
// missing optional rate prices its tokens at the base input or output rate.
func estimate(card catalog.Cost, u RequestUsage) float64 {
	prompt, completion := max(u.Prompt, 0), max(u.Completion, 0)
	cacheRead := clamp(u.CacheRead, 0, prompt)
	cacheWrite := clamp(u.CacheWrite, 0, prompt-cacheRead)
	fresh := prompt - cacheRead - cacheWrite
	reasoning := clamp(u.Reasoning, 0, completion)
	r := card.RatesFor(prompt)
	rate := func(optional *float64, base float64) float64 {
		if optional != nil {
			return *optional
		}
		return base
	}
	dollars := float64(fresh)*r.Input +
		float64(cacheRead)*rate(r.CacheRead, r.Input) +
		float64(cacheWrite)*rate(r.CacheWrite, r.Input) +
		float64(completion-reasoning)*r.Output +
		float64(reasoning)*rate(r.Reasoning, r.Output)
	return dollars / 1_000_000
}

func clamp(v, lo, hi int64) int64 {
	return max(lo, min(v, hi))
}

// ModelPrice returns the catalog's base input and output prices per million
// tokens for display. ok is false when the pair has no price; subscription
// providers report ok=false too (they have no per-token price).
func ModelPrice(provider, modelID string) (input, output float64, ok bool) {
	entry, found := catalog.Lookup(provider, modelID)
	if !found || entry.Cost == nil || catalog.Billing(provider) == catalog.BillingSubscription {
		return 0, 0, false
	}
	return entry.Cost.Input, entry.Cost.Output, true
}

// IsSubscription reports whether provider bills a plan instead of tokens.
func IsSubscription(provider string) bool {
	return catalog.Billing(strings.TrimSpace(provider)) == catalog.BillingSubscription
}
