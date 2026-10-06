package model

import (
	"encoding/json"
	"math"
)

// usageSeen records which token counts a usage object actually named. A
// missing or malformed count is unseen, never a reported zero.
type usageSeen struct {
	prompt, completion bool
}

// usageReport is one response's parsed usage. ok is false when the response
// carried no usage object; usage keeps the shared TokenUsage shape, and seen
// adds the completion flag TokenUsage does not carry.
type usageReport struct {
	usage  TokenUsage
	seen   usageSeen
	detail usageDetail
	ok     bool
}

// usageDetail is the billing breakdown a usage object may carry beyond the
// two totals (specs/model-metadata § Token accounting). Absent fields stay
// zero; costSeen marks a provider-reported charge.
type usageDetail struct {
	cacheRead  int64   // prompt tokens read from cache (subset of prompt)
	cacheWrite int64   // prompt tokens written to cache (subset of prompt)
	reasoning  int64   // reasoning tokens (subset of completion once normalized)
	total      int64   // total_tokens, when totalSeen
	totalSeen  bool    // total_tokens was a valid count
	cost       float64 // provider-charged US dollars
	costSeen   bool
}

// withAdditiveReasoning folds reasoning tokens into the completion count when
// the provider reports them on top of it rather than inside it. xAI does:
// its documented chat-completions example is prompt 32, completion 9,
// reasoning 94, total 135 (32+9+94), and its Responses example is input 32,
// output 9, reasoning 110, total 151. Reasoning is taken as additive when it
// exceeds the completion count, or when total_tokens equals prompt +
// completion + reasoning instead of prompt + completion; afterwards
// reasoning is again a subset of completion, so every reasoning token is
// billed and counted (specs/model-metadata § Token accounting).
func (r usageReport) withAdditiveReasoning() usageReport {
	reasoning := r.detail.reasoning
	if !r.seen.completion || reasoning <= 0 {
		return r
	}
	additive := reasoning > r.usage.Completion
	if r.seen.prompt && r.detail.totalSeen {
		sum := r.usage.Prompt + r.usage.Completion
		additive = additive || sum != r.detail.total && sum+reasoning == r.detail.total
	}
	if additive {
		r.usage.Completion += reasoning
	}
	return r
}

// request converts a parsed report into the exported per-request shape.
func (r usageReport) request() RequestUsage {
	return RequestUsage{
		Prompt: r.usage.Prompt, Completion: r.usage.Completion,
		PromptSeen: r.usage.PromptSeen, CompletionSeen: r.ok && r.seen.completion,
		CacheRead: r.detail.cacheRead, CacheWrite: r.detail.cacheWrite, Reasoning: r.detail.reasoning,
		Cost: r.detail.cost, CostSeen: r.detail.costSeen,
	}
}

// parseUsageDetail reads the breakdown fields documented by the providers
// Likha speaks to:
//   - OpenAI chat completions: prompt_tokens_details.cached_tokens and
//     .cache_write_tokens, completion_tokens_details.reasoning_tokens;
//   - OpenAI Responses (Codex): input_tokens_details.cached_tokens,
//     output_tokens_details.reasoning_tokens;
//   - DeepSeek: top-level prompt_cache_hit_tokens (same as cached_tokens);
//   - OpenRouter: the OpenAI fields plus a top-level cost in US dollars,
//     and for BYOK requests cost_details.upstream_inference_cost, the
//     upstream provider's own charge, which the user pays on top of
//     OpenRouter's fee in cost, so the request's cost is the sum;
//   - xAI: the OpenAI fields plus cost_in_usd_ticks, the exact charge in
//     ticks of 1e-10 US dollars (read only when cost is absent), and
//     reasoning tokens counted on top of completion (withAdditiveReasoning).
//
// Malformed or negative counts are ignored, and a malformed, negative, or
// null cost is never read as a reported zero.
func parseUsageDetail(usage map[string]json.RawMessage) usageDetail {
	count := func(raw json.RawMessage) (int64, bool) {
		var n int64
		if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &n) != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	nested := func(object, key string) (int64, bool) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(usage[object], &fields) != nil {
			return 0, false
		}
		return count(fields[key])
	}
	var d usageDetail
	if n, ok := nested("prompt_tokens_details", "cached_tokens"); ok {
		d.cacheRead = n
	} else if n, ok := nested("input_tokens_details", "cached_tokens"); ok {
		d.cacheRead = n
	} else if n, ok := count(usage["prompt_cache_hit_tokens"]); ok {
		d.cacheRead = n
	}
	if n, ok := nested("prompt_tokens_details", "cache_write_tokens"); ok {
		d.cacheWrite = n
	}
	if n, ok := nested("completion_tokens_details", "reasoning_tokens"); ok {
		d.reasoning = n
	} else if n, ok := nested("output_tokens_details", "reasoning_tokens"); ok {
		d.reasoning = n
	}
	if n, ok := count(usage["total_tokens"]); ok {
		d.total, d.totalSeen = n, true
	}
	dollars := func(raw json.RawMessage) (float64, bool) {
		var v float64
		if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &v) != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	}
	if cost, ok := dollars(usage["cost"]); ok {
		d.cost, d.costSeen = cost, true
		var details map[string]json.RawMessage
		if json.Unmarshal(usage["cost_details"], &details) == nil {
			if upstream, ok := dollars(details["upstream_inference_cost"]); ok && upstream > 0 {
				d.cost += upstream
			}
		}
	} else if ticks, ok := count(usage["cost_in_usd_ticks"]); ok {
		d.cost, d.costSeen = float64(ticks)/1e10, true
	}
	return d
}

// LastRequestUsage reports the token counts of the last request with a flag
// per count: promptSeen or completionSeen is false when the response did not
// name that count (a prompt-only report yields completionSeen=false and a
// zero completion that must not be read as zero tokens). ok is false when the
// last request reported neither count, including a request that failed, one
// still in flight, or no request at all. The report is reset when each
// request starts and is never cumulative across requests.
func (c *Client) LastRequestUsage() (prompt, completion int64, promptSeen, completionSeen, ok bool) {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	if !c.tokenSeen || !c.tokenPromptSeen && !c.tokenCompletionSeen {
		return 0, 0, false, false, false
	}
	if c.tokenPromptSeen {
		prompt = c.tokenPrompt
	}
	if c.tokenCompletionSeen {
		completion = c.tokenCompletion
	}
	return prompt, completion, c.tokenPromptSeen, c.tokenCompletionSeen, true
}

// LastRequest reports the last request's full usage breakdown, with the same
// ok rule as LastRequestUsage. Prefer StreamUsage when the client is shared.
func (c *Client) LastRequest() (RequestUsage, bool) {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	if !c.tokenSeen || !c.tokenPromptSeen && !c.tokenCompletionSeen {
		return RequestUsage{}, false
	}
	return c.tokenReport.request(), true
}
