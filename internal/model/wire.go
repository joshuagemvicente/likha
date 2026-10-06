package model

import "strings"

// ProviderWire is the request protocol a provider row speaks
// (specs/claude-native decision 1). The zero value is OpenAI-compatible chat
// completions, so every row without a stated wire keeps today's requests.
type ProviderWire uint8

const (
	// WireChat posts OpenAI-compatible chat completions to {base}/chat/completions.
	WireChat ProviderWire = iota
	// WireAnthropic posts Anthropic Messages API requests to {base}/messages
	// with x-api-key authentication.
	WireAnthropic
)

// route is the generating request path the wire posts to, relative to the
// endpoint base.
func (w ProviderWire) route() string {
	if w == WireAnthropic {
		return "/messages"
	}
	return "/chat/completions"
}

// wireFor returns the wire of the provider row whose base URL is exactly
// base, ignoring one trailing slash. There is no prefix matching: a custom
// endpoint, even on a provider's host, keeps chat completions so a wire never
// changes behind the user's back.
func wireFor(base string) ProviderWire {
	base = strings.TrimSuffix(base, "/")
	for _, p := range Providers {
		if p.BaseURL == base {
			return p.Wire
		}
	}
	return WireChat
}
