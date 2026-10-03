package model

// usageSeen records which token counts a usage object actually named. A
// missing or malformed count is unseen, never a reported zero.
type usageSeen struct {
	prompt, completion bool
}

// usageReport is one response's parsed usage. ok is false when the response
// carried no usage object; usage keeps the shared TokenUsage shape, and seen
// adds the completion flag TokenUsage does not carry.
type usageReport struct {
	usage TokenUsage
	seen  usageSeen
	ok    bool
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
