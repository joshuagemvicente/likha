package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const oauthResponseLimit = 1 << 20

var errOAuthResponseTooLarge = errors.New("OAuth response exceeded the size limit")

// OAuthError exposes only a safe machine-readable code and HTTP status. Token
// endpoint bodies, descriptions, transport errors, and URLs are never echoed.
type OAuthError struct {
	Operation  string
	Code       string
	StatusCode int
}

func (e *OAuthError) Error() string {
	message := "ChatGPT " + e.Operation + " failed"
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" (HTTP %d)", e.StatusCode)
	}
	message += ": " + safeOAuthCode(e.Code)
	if e.RequiresLogin() {
		message += "; sign in again with the saved account"
	}
	return message
}

// Temporary marks failures eligible for bounded backoff, not lost credentials.
func (e *OAuthError) Temporary() bool {
	return e.Code == "network_error" || e.StatusCode >= 500 && e.StatusCode <= 599
}

// RequiresLogin identifies OpenAI's documented unusable-refresh-token errors.
// invalid_client is a configuration failure, not a reason to delete tokens.
func (e *OAuthError) RequiresLogin() bool {
	switch e.Code {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
		return true
	default:
		return false
	}
}

func safeOAuthCode(code string) string {
	switch code {
	case "access_denied", "invalid_grant", "invalid_client", "invalid_refresh_token", "token_expired",
		"refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused", "invalid_request",
		"invalid_scope", "unauthorized_client", "unsupported_grant_type", "unsupported_response_type",
		"server_error", "temporarily_unavailable", "network_error", "invalid_response", "invalid_id_token",
		"identity_mismatch", "client_mismatch", "incomplete_registration":
		return code
	default:
		return "oauth_error"
	}
}

func safeOAuthRequestError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return &OAuthError{Operation: operation, Code: "network_error"}
}

func issuerEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Scheme == "https" && u.Host == "auth.openai.com" && u.User == nil &&
		u.Opaque == "" && u.RawQuery == "" && u.Fragment == "" && strings.HasPrefix(u.Path, "/")
}

// oauthHTTPClient clones the supplied client, preserves its transport seam and
// any shorter timeout, but never follows redirects or sends caller cookies.
// Its transport pins requests and bounds OIDC-library response reads as well.
func oauthHTTPClient(ctx context.Context, supplied *http.Client) *http.Client {
	client := &http.Client{}
	if supplied != nil {
		*client = *supplied
	}
	if client.Timeout <= 0 || client.Timeout > oauthHTTPTimeout {
		client.Timeout = oauthHTTPTimeout
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = &oauthTransport{base: transport, operationContext: ctx}
	return client
}

type oauthTransport struct {
	base             http.RoundTripper
	operationContext context.Context
}

func (t *oauthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !issuerEndpoint(request.URL.String()) {
		return nil, errors.New("OAuth endpoint is outside the documented issuer origin")
	}
	// go-oidc's key-set fetch deliberately ignores its original cancellation.
	// Retain HTTP-client timeouts AND tie those background fetches to this
	// operation, so cancelling login/refresh also closes an in-flight JWKS read.
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(t.operationContext, cancel)
	response, err := t.base.RoundTrip(request.Clone(ctx))
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	if response.Body == nil {
		stop()
		cancel()
		return nil, errors.New("OAuth endpoint returned no response body")
	}
	response.Body = &oauthResponseBody{ReadCloser: response.Body, remaining: oauthResponseLimit, cleanup: func() { stop(); cancel() }}
	return response, nil
}

type oauthResponseBody struct {
	io.ReadCloser
	remaining int
	cleanup   func()
}

func (b *oauthResponseBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, errOAuthResponseTooLarge
	}
	if len(p) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= n
	if b.remaining < 0 {
		return n, errOAuthResponseTooLarge
	}
	return n, err
}

func (b *oauthResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cleanup()
	return err
}

func oauthRequest(ctx context.Context, client *http.Client, method, endpoint string, form url.Values, operation string) ([]byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, &OAuthError{Operation: operation, Code: "invalid_request"}
	}
	request.Header.Set("Accept", "application/json")
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, safeOAuthRequestError(ctx, operation, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !errors.Is(err, errOAuthResponseTooLarge) {
			return nil, &OAuthError{Operation: operation, Code: "network_error", StatusCode: response.StatusCode}
		}
		return nil, &OAuthError{Operation: operation, Code: "invalid_response", StatusCode: response.StatusCode}
	}
	if response.StatusCode != http.StatusOK {
		var reply struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(payload, &reply)
		return nil, &OAuthError{Operation: operation, Code: safeOAuthCode(reply.Error), StatusCode: response.StatusCode}
	}
	return payload, nil
}

type tokenResponse struct {
	IDToken      string          `json:"id_token"`
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	TokenType    string          `json:"token_type"`
	ExpiresIn    *int64          `json:"expires_in"`
	Scope        json.RawMessage `json:"scope"`
}

func requestOAuthTokens(ctx context.Context, client *http.Client, form url.Values, operation string) (TokenSet, error) {
	payload, err := oauthRequest(ctx, client, http.MethodPost, oauthTokenURL, form, operation)
	if err != nil {
		return TokenSet{}, err
	}
	var reply tokenResponse
	if err := json.Unmarshal(payload, &reply); err != nil || reply.AccessToken == "" ||
		reply.TokenType != "" && !strings.EqualFold(reply.TokenType, "Bearer") {
		return TokenSet{}, &OAuthError{Operation: operation, Code: "invalid_response"}
	}
	lifetime := oauthTokenLifetime
	if reply.ExpiresIn != nil {
		if *reply.ExpiresIn <= 0 || *reply.ExpiresIn > int64((1<<63-1)/time.Second) {
			return TokenSet{}, &OAuthError{Operation: operation, Code: "invalid_response"}
		}
		lifetime = time.Duration(*reply.ExpiresIn) * time.Second
	}
	tokens := TokenSet{
		IDToken: reply.IDToken, AccessToken: reply.AccessToken, RefreshToken: reply.RefreshToken,
		TokenType: reply.TokenType, Expires: time.Now().Add(lifetime), scopeReturned: len(reply.Scope) != 0,
	}
	if tokens.scopeReturned {
		var scope string
		if string(reply.Scope) == "null" || json.Unmarshal(reply.Scope, &scope) != nil {
			return TokenSet{}, &OAuthError{Operation: operation, Code: "invalid_response"}
		}
		tokens.Scopes = strings.Fields(scope)
	}
	if form.Get("grant_type") == "authorization_code" && tokens.TokenType == "" {
		return TokenSet{}, &OAuthError{Operation: operation, Code: "invalid_response"}
	}
	return tokens, nil
}
