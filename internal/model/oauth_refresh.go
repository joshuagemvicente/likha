package model

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RefreshCredentials renews one verified registration. Callers serialize and
// atomically persist the returned record. No legacy/dynamic client is usable.
// A newly returned ID token is validated without a login nonce; omitted
// identity, refresh, or scope fields retain their existing values.
func RefreshCredentials(ctx context.Context, httpClient *http.Client, old OAuthCredentials) (OAuthCredentials, error) {
	if err := ctx.Err(); err != nil {
		return OAuthCredentials{}, err
	}
	if !old.Registered() || old.Issuer != ChatGPTIssuer || !validIssuedClientID(old.ClientID) {
		return OAuthCredentials{}, errors.New("legacy or incomplete ChatGPT credentials require a fresh sign-in")
	}
	if !old.HasPlanScope() {
		return OAuthCredentials{}, ErrPlanScopeRequired
	}
	if old.Refresh == "" {
		return OAuthCredentials{}, &OAuthError{Operation: "refresh", Code: "invalid_refresh_token"}
	}
	client := oauthHTTPClient(ctx, httpClient)
	tokens, err := requestOAuthTokens(ctx, client, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {old.ClientID},
		"refresh_token": {old.Refresh}, "resource": {ChatGPTResource},
	}, "refresh")
	if err != nil {
		return OAuthCredentials{}, err
	}
	updated := old
	updated.Scopes = append([]string(nil), old.Scopes...)
	updated.Access, updated.Expires = tokens.AccessToken, tokens.Expires.UnixMilli()
	if tokens.RefreshToken != "" {
		updated.Refresh = tokens.RefreshToken
	}
	if tokens.TokenType != "" {
		updated.TokenType = tokens.TokenType
	}
	if updated.TokenType == "" {
		updated.TokenType = "Bearer"
	}
	if !strings.EqualFold(updated.TokenType, "Bearer") {
		return OAuthCredentials{}, &OAuthError{Operation: "refresh", Code: "invalid_response"}
	}
	if tokens.IDToken != "" {
		provider, err := discoverOAuthProvider(ctx, client)
		if err != nil {
			return OAuthCredentials{}, err
		}
		identity, err := verifyOAuthIdentity(ctx, provider, tokens.IDToken, old.ClientID, "", old.Subject)
		if err != nil {
			return OAuthCredentials{}, err
		}
		updated.IDToken = tokens.IDToken
		if identity.emailPresent {
			updated.Email = identity.email
		}
	}
	if tokens.scopeReturned {
		updated.Scopes = append([]string(nil), tokens.Scopes...)
	}
	if !updated.HasPlanScope() {
		return OAuthCredentials{}, ErrPlanScopeRequired
	}
	if err := ctx.Err(); err != nil {
		return OAuthCredentials{}, err
	}
	return updated, nil
}

// RevokeCredentials attempts to end the renewable session, not delete the
// registration. It retries only network/5xx failures, with bounded backoff.
// The caller clears local tokens even when remote revocation is unconfirmed.
func RevokeCredentials(ctx context.Context, httpClient *http.Client, credentials OAuthCredentials) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if credentials.Refresh == "" {
		return nil
	}
	if !credentials.Registered() || credentials.Issuer != ChatGPTIssuer || !validIssuedClientID(credentials.ClientID) {
		return errors.New("cannot revoke an incomplete ChatGPT registration")
	}
	client := oauthHTTPClient(ctx, httpClient)
	endpoint := ""
	for attempt := 0; attempt < 3; attempt++ {
		var err error
		if endpoint == "" {
			var metadata oauthMetadata
			metadata, err = discoverOAuthMetadata(ctx, client)
			if err == nil {
				if !issuerEndpoint(metadata.RevocationEndpoint) {
					return &OAuthError{Operation: "revocation discovery", Code: "invalid_response"}
				}
				endpoint = metadata.RevocationEndpoint
			}
		}
		if err == nil {
			_, err = oauthRequest(ctx, client, http.MethodPost, endpoint, url.Values{
				"token": {credentials.Refresh}, "token_type_hint": {"refresh_token"}, "client_id": {credentials.ClientID},
			}, "revocation")
		}
		if err == nil {
			return nil
		}
		var failure *OAuthError
		if attempt == 2 || !errors.As(err, &failure) || !failure.Temporary() {
			return err
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return &OAuthError{Operation: "revocation", Code: "network_error"}
}
