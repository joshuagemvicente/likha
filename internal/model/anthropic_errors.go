package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxAnthropicErrorText bounds the provider text Likha copies into an error,
// so a misbehaving stream cannot flood the transcript.
const maxAnthropicErrorText = 1024

// errClaudeEndsWithAssistant is returned before sending when the mapped
// conversation would end with an assistant turn: Claude's current models
// reject assistant prefill, and Likha never sends one (spec point 4).
var errClaudeEndsWithAssistant = errors.New("Claude request would end with an assistant turn; send a prompt to continue.")

// errAnthropicStreamEnded marks a stream that closed before message_stop,
// which is a dropped stream and never a finished answer (spec point 30).
var errAnthropicStreamEnded = errors.New("model stream ended before message_stop")

// spendCapNote leads the message of a spend-cap 429 so it does not read like
// a rate limit that clears on its own (spec point 28).
const spendCapNote = "spend limit reached (a quota, not a rate limit: requests keep failing until the organization's limit is raised or access resumes): "

// anthropicAPIError is a failure Anthropic reported, either as a non-2xx
// HTTP response or as an error event inside a 200 stream (Status 0). Callers
// match it with errors.As instead of reading the message.
type anthropicAPIError struct {
	Status     int    // HTTP status code; 0 for an in-stream error event
	StatusText string // HTTP status line, e.g. "529 status code 529"
	Type       string // Anthropic error.type, e.g. "overloaded_error"
	Message    string // Anthropic error.message, bounded
	Code       string // error.details.error_code, e.g. "enforced_spend_limit_reached"
	RequestID  string // request-id header, else the body's request_id
	Raw        string // bounded body when it is not Anthropic's JSON error
}

func (e *anthropicAPIError) Error() string {
	var b strings.Builder
	if e.unauthorized() {
		b.WriteString(ErrUnauthorized.Error() + ": ")
	}
	if e.Status == 0 {
		b.WriteString("model stream error: ")
	} else {
		b.WriteString("model HTTP " + e.StatusText + ": ")
	}
	if e.spendCap() {
		b.WriteString(spendCapNote)
	}
	switch {
	case e.Type != "" && e.Message != "":
		b.WriteString(e.Type + ": " + e.Message)
	case e.Type != "":
		b.WriteString(e.Type)
	case e.Message != "":
		b.WriteString(e.Message)
	default:
		b.WriteString(e.Raw)
	}
	if e.RequestID != "" {
		b.WriteString(" (request " + e.RequestID + ")")
	}
	return b.String()
}

// Unwrap lets errors.Is(err, ErrUnauthorized) recognise rejected credentials
// as it does on the other wires.
func (e *anthropicAPIError) Unwrap() error {
	if e.unauthorized() {
		return ErrUnauthorized
	}
	return nil
}

func (e *anthropicAPIError) unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// spendCap reports Anthropic's monthly spend cap, a 429 that keeps failing
// until access resumes, so it is a quota error rather than a rate limit.
func (e *anthropicAPIError) spendCap() bool {
	return e.Status == http.StatusTooManyRequests && e.Code == "enforced_spend_limit_reached"
}

// anthropicErrorBody is Anthropic's documented error envelope:
// {"type":"error","error":{"type","message","details"},"request_id"}.
type anthropicErrorBody struct {
	Type  string `json:"type"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Details *struct {
			ErrorCode string `json:"error_code"`
		} `json:"details"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

// readAnthropicHTTPError reads a non-2xx response (bounded to 64 KiB) into an
// anthropicAPIError. A body that is not Anthropic's JSON error, such as a
// Cloudflare 413 page, is kept raw.
func readAnthropicHTTPError(resp *http.Response) *anthropicAPIError {
	apiErr := &anthropicAPIError{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		RequestID:  strings.TrimSpace(resp.Header.Get("request-id")),
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
	if err != nil {
		apiErr.Raw = fmt.Sprintf("(reading error body: %v)", err)
		return apiErr
	}
	var envelope anthropicErrorBody
	if json.Unmarshal(body, &envelope) != nil || envelope.Error == nil {
		apiErr.Raw = boundText(strings.TrimSpace(string(body)))
		return apiErr
	}
	apiErr.Type = boundText(envelope.Error.Type)
	apiErr.Message = boundText(envelope.Error.Message)
	if envelope.Error.Details != nil {
		apiErr.Code = envelope.Error.Details.ErrorCode
	}
	if apiErr.RequestID == "" {
		apiErr.RequestID = envelope.RequestID
	}
	return apiErr
}

// boundText clips provider text to maxAnthropicErrorText bytes.
func boundText(text string) string {
	if len(text) <= maxAnthropicErrorText {
		return text
	}
	return text[:maxAnthropicErrorText] + "…"
}

// RefusalError is a Claude response that stopped with stop_reason
// "refusal". Category and Explanation come from stop_details and are empty
// when Anthropic names none. Like every stream error it yields no message,
// so the refused partial output never reaches history (spec point 19).
type RefusalError struct {
	Category    string
	Explanation string
}

func (e *RefusalError) Error() string {
	text := "Claude declined the request"
	if e.Category != "" {
		text += " (category: " + e.Category + ")"
	}
	text += "."
	if e.Explanation != "" {
		text += "\n" + e.Explanation
	}
	return text
}

// requestIDError appends Anthropic's request ID to a failure that happened
// after the response started, so the user can quote it to support (spec
// point 29). It keeps the cause matchable with errors.Is and errors.As.
type requestIDError struct {
	err       error
	requestID string
}

func (e *requestIDError) Error() string {
	return e.err.Error() + " (request " + e.requestID + ")"
}

func (e *requestIDError) Unwrap() error { return e.err }

// withRequestID attaches requestID to err unless either is empty or err
// already names it.
func withRequestID(err error, requestID string) error {
	if err == nil || requestID == "" {
		return err
	}
	var apiErr *anthropicAPIError
	if errors.As(err, &apiErr) && apiErr.RequestID != "" {
		return err
	}
	return &requestIDError{err: err, requestID: requestID}
}
