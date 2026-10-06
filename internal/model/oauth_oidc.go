package model

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
)

type oauthMetadata struct {
	oidc.ProviderConfig
	RevocationEndpoint string `json:"revocation_endpoint"`
}

func discoverOAuthMetadata(ctx context.Context, client *http.Client) (oauthMetadata, error) {
	payload, err := oauthRequest(ctx, client, http.MethodGet, ChatGPTIssuer+"/.well-known/openid-configuration", nil, "OIDC discovery")
	if err != nil {
		return oauthMetadata{}, err
	}
	var metadata oauthMetadata
	if json.Unmarshal(payload, &metadata) != nil || metadata.IssuerURL != ChatGPTIssuer {
		return oauthMetadata{}, &OAuthError{Operation: "OIDC discovery", Code: "invalid_response"}
	}
	return metadata, nil
}

func discoverOAuthProvider(ctx context.Context, client *http.Client) (*oidc.Provider, error) {
	metadata, err := discoverOAuthMetadata(ctx, client)
	if err != nil {
		return nil, err
	}
	if metadata.AuthURL != oauthAuthorizeURL || metadata.TokenURL != oauthTokenURL || !issuerEndpoint(metadata.JWKSURL) {
		return nil, &OAuthError{Operation: "OIDC discovery", Code: "invalid_response"}
	}
	// Discovery is decoded locally only to bound bodies and redact errors. All
	// signature and standard JWT claim verification is delegated to go-oidc.
	return metadata.ProviderConfig.NewProvider(oidc.ClientContext(ctx, client)), nil
}

type oauthIdentity struct {
	issuer       string
	subject      string
	email        string
	emailPresent bool
}

func verifyOAuthIdentity(ctx context.Context, provider *oidc.Provider, rawToken, clientID, nonce, expectedSubject string) (oauthIdentity, error) {
	verifier := provider.Verifier(&oidc.Config{
		ClientID: clientID, SupportedSigningAlgs: []string{oidc.RS256},
	})
	token, err := verifier.Verify(ctx, rawToken)
	if err != nil {
		if ctx.Err() != nil {
			return oauthIdentity{}, ctx.Err()
		}
		// Library errors can include untrusted claims, JWKS bodies, or URLs.
		return oauthIdentity{}, &OAuthError{Operation: "identity verification", Code: "invalid_id_token"}
	}
	if token.Subject == "" || nonce != "" && (token.Nonce == "" || subtle.ConstantTimeCompare([]byte(token.Nonce), []byte(nonce)) != 1) {
		return oauthIdentity{}, &OAuthError{Operation: "identity verification", Code: "invalid_id_token"}
	}
	if expectedSubject != "" && token.Subject != expectedSubject {
		return oauthIdentity{}, &OAuthError{Operation: "identity verification", Code: "identity_mismatch"}
	}
	var claims struct {
		Email           *string `json:"email"`
		AuthorizedParty string  `json:"azp"`
	}
	if token.Claims(&claims) != nil || claims.AuthorizedParty != "" && claims.AuthorizedParty != clientID ||
		len(token.Audience) > 1 && claims.AuthorizedParty != clientID {
		return oauthIdentity{}, &OAuthError{Operation: "identity verification", Code: "invalid_id_token"}
	}
	identity := oauthIdentity{issuer: token.Issuer, subject: token.Subject, emailPresent: claims.Email != nil}
	if claims.Email != nil {
		identity.email = *claims.Email
	}
	return identity, nil
}
