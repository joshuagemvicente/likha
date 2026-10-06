package model

// ReplayRejectedError marks a request rejected as a bad request (HTTP 400)
// while it carried provider replay state, or while state another issuer
// wrote was left out (specs/reasoning-replay T12). Missing counts assistant
// turns sent without state the provider may require; Sent counts turns that
// carried replay state. Err is the provider error. When the request also
// carried effort, EffortRejectedError wraps this error, so errors.As finds
// both and both hints show. Every later request repeats the same history and
// fails the same way, so the interface points at /compact. The chat
// completions path builds it in replayEncoder.rejected, the native Claude
// client in rejectedAnthropicRequest.
type ReplayRejectedError struct {
	Missing int
	Sent    int
	Err     error
}

func (e *ReplayRejectedError) Error() string { return e.Err.Error() }

func (e *ReplayRejectedError) Unwrap() error { return e.Err }
