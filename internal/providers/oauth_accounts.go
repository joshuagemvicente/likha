package providers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"likha/internal/model"
)

// ErrOAuthLoginRequired lets interactive startup offer browser sign-in rather
// than treating a missing, legacy, or signed-out registration as usable.
var ErrOAuthLoginRequired = errors.New("ChatGPT browser sign-in required")

func oauthLoginRequired(reason string) error {
	return fmt.Errorf("%w: %s; sign in through /providers or run likha --provider chatgpt in an interactive terminal", ErrOAuthLoginRequired, reason)
}

func oauthHostPath(stateDir string) string {
	return filepath.Join(stateDir, "oauth-host.json")
}

// EnsureOAuthHost must be called before opening authorization. The opaque
// host identity is independent of accounts and is never removed by sign-out.
func EnsureOAuthHost(stateDir string) (string, error) {
	unlock, err := lockCredentials(context.Background(), stateDir)
	if err != nil {
		return "", err
	}
	defer unlock()
	if host, err := readOAuthHost(stateDir); err == nil {
		return host, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	creds, err := ReadCredentials(stateDir)
	if err != nil {
		return "", err
	}
	for _, entry := range creds {
		if len(entry.Accounts) != 0 {
			return "", fmt.Errorf("OAuth host identity is missing while saved registrations exist; restore the original protected host file before reconnecting")
		}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("creating OAuth host identity: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	host := fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	data, err := json.Marshal(struct {
		HostID string `json:"ext_agent_host_id"`
	}{HostID: host})
	if err != nil {
		return "", err
	}
	if err := writePrivateFile(oauthHostPath(stateDir), data); err != nil {
		return "", fmt.Errorf("persisting OAuth host identity before sign-in: %w", err)
	}
	return host, nil
}

func readOAuthHost(stateDir string) (string, error) {
	data, err := readPrivateFile(oauthHostPath(stateDir))
	if err != nil {
		return "", fmt.Errorf("reading OAuth host identity: %w", err)
	}
	var saved struct {
		HostID string `json:"ext_agent_host_id"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		return "", fmt.Errorf("stored OAuth host identity is not valid JSON: %w", err)
	}
	if strings.TrimSpace(saved.HostID) == "" || saved.HostID != strings.TrimSpace(saved.HostID) {
		return "", fmt.Errorf("stored OAuth host identity is missing or invalid; restore the original protected host file before reconnecting")
	}
	return saved.HostID, nil
}

func checkOAuthHost(stateDir, expected string) error {
	host, err := readOAuthHost(stateDir)
	if err != nil {
		return err
	}
	if host != expected {
		return fmt.Errorf("saved ChatGPT registration belongs to a different host; restore its original host identity before reconnecting")
	}
	return nil
}

func validateOAuthEntry(entry StoredCredential) error {
	if (len(entry.Accounts) > 0 || entry.ActiveClientID != "") && entry.Type != "oauth" {
		return fmt.Errorf("account registrations require OAuth credential type")
	}
	for id, c := range entry.Accounts {
		if id == "" || id != c.ClientID || !c.Registered() || c.Issuer != model.ChatGPTIssuer {
			return fmt.Errorf("saved OAuth registration has an invalid client/identity mapping")
		}
	}
	if entry.ActiveClientID != "" {
		if _, exists := entry.Accounts[entry.ActiveClientID]; !exists {
			return fmt.Errorf("active OAuth registration is missing")
		}
	}
	return nil
}

func oauthSignedIn(c model.OAuthCredentials) bool {
	return c.Registered() && c.Issuer == model.ChatGPTIssuer && c.HasPlanScope() && c.Access != "" && c.Refresh != "" &&
		c.Expires > 0 && strings.EqualFold(c.TokenType, "Bearer")
}

func validateOAuthTokens(c model.OAuthCredentials) error {
	if !oauthSignedIn(c) || c.IDToken == "" {
		return oauthLoginRequired("a validated issued registration, ChatGPT plan grant, and complete token set are required (legacy Codex credentials require a fresh sign-in)")
	}
	return nil
}

func sameOAuthRegistration(a, b model.OAuthCredentials) bool {
	return a.Issuer == b.Issuer && a.Subject == b.Subject && a.ClientID == b.ClientID && a.HostID == b.HostID
}

// OAuthAccounts returns every registration, including signed-out ones, sorted
// by issued client ID. Email is display metadata, never an account key.
func OAuthAccounts(stateDir, provider string) ([]model.OAuthCredentials, string, error) {
	creds, err := ReadCredentials(stateDir)
	if err != nil {
		return nil, "", err
	}
	entry := creds[provider]
	accounts := make([]model.OAuthCredentials, 0, len(entry.Accounts))
	for _, c := range entry.Accounts {
		if err := checkOAuthHost(stateDir, c.HostID); err != nil {
			return nil, "", err
		}
		accounts = append(accounts, c)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ClientID < accounts[j].ClientID })
	return accounts, entry.ActiveClientID, nil
}

// StoreOAuth stores a completed, validated browser sign-in. A failed write
// cannot change the active registration or destroy another account's tokens.
func StoreOAuth(stateDir, provider string, c model.OAuthCredentials) error {
	if err := validateOAuthTokens(c); err != nil {
		return err
	}
	unlock, err := lockCredentials(context.Background(), stateDir)
	if err != nil {
		return err
	}
	defer unlock()
	if err := checkOAuthHost(stateDir, c.HostID); err != nil {
		return err
	}
	creds, err := ReadCredentials(stateDir)
	if err != nil {
		return err
	}
	entry := creds[provider]
	if old, exists := entry.Accounts[c.ClientID]; exists && !sameOAuthRegistration(old, c) {
		return fmt.Errorf("ChatGPT issued client ID is already associated with another identity or host; the saved registration was not replaced")
	}
	if entry.Accounts == nil {
		entry.Accounts = make(map[string]model.OAuthCredentials)
	}
	entry.Accounts[c.ClientID] = c
	// Only a successful new sign-in replaces obsolete flat Codex data.
	creds[provider] = StoredCredential{Type: "oauth", ActiveClientID: c.ClientID, Accounts: entry.Accounts}
	return writeCredentials(stateDir, creds)
}

// SelectOAuthAccount is intentionally strict: a retained signed-out account
// must complete browser reauthorization before it can become active.
func SelectOAuthAccount(stateDir, provider, clientID string) error {
	unlock, err := lockCredentials(context.Background(), stateDir)
	if err != nil {
		return err
	}
	defer unlock()
	creds, err := ReadCredentials(stateDir)
	if err != nil {
		return err
	}
	entry := creds[provider]
	c, exists := entry.Accounts[clientID]
	if !exists || !oauthSignedIn(c) {
		return oauthLoginRequired("the selected saved account is signed out or has no ChatGPT plan grant; reconnect it first")
	}
	if err := checkOAuthHost(stateDir, c.HostID); err != nil {
		return err
	}
	entry.ActiveClientID = clientID
	creds[provider] = entry
	return writeCredentials(stateDir, creds)
}

// LogoutOAuth serializes revocation with refresh and always clears local
// tokens after an attempted revoke, even when the callback fails or cancels.
// An empty clientID selects the current active registration.
func LogoutOAuth(ctx context.Context, stateDir, provider, clientID string, revoke func(context.Context, model.OAuthCredentials) error) error {
	unlock, err := lockCredentials(ctx, stateDir)
	if err != nil {
		return err
	}
	defer unlock()
	creds, err := ReadCredentials(stateDir)
	if err != nil {
		return err
	}
	entry := creds[provider]
	if clientID == "" {
		clientID = entry.ActiveClientID
	}
	c, exists := entry.Accounts[clientID]
	if !exists {
		return oauthLoginRequired("the saved account registration was not found")
	}
	var remoteErr error
	if c.Refresh != "" {
		if revoke == nil {
			remoteErr = fmt.Errorf("no revocation callback is available")
		} else {
			remoteErr = revoke(ctx, c)
		}
	} else if c.Access != "" || c.IDToken != "" {
		remoteErr = fmt.Errorf("no refresh token is available for remote revocation")
	}
	c.Access, c.Refresh, c.IDToken = "", "", ""
	c.Expires = 0
	entry.Accounts[clientID] = c
	if entry.ActiveClientID == clientID {
		entry.ActiveClientID = ""
	}
	creds[provider] = entry
	writeErr := writeCredentials(stateDir, creds)
	if remoteErr != nil {
		warning := fmt.Errorf("remote ChatGPT revocation was not confirmed; disconnect the app in ChatGPT Settings: %w", remoteErr)
		if writeErr != nil {
			return errors.Join(fmt.Errorf("local ChatGPT sign-out could not be persisted: %w", writeErr), warning)
		}
		return fmt.Errorf("signed out locally, but %w", warning)
	}
	if writeErr != nil {
		return fmt.Errorf("local ChatGPT sign-out could not be persisted: %w", writeErr)
	}
	return nil
}

// RefreshOAuth reloads under a process-shared lock before deciding to rotate.
// A forced retry with an already-replaced token reuses the persisted rotation.
// It never returns newly rotated credentials unless they were persisted.
func RefreshOAuth(ctx context.Context, stateDir, provider string, expected model.OAuthCredentials, force bool, refresh func(context.Context, model.OAuthCredentials) (model.OAuthCredentials, error)) (model.OAuthCredentials, error) {
	unlock, err := lockCredentials(ctx, stateDir)
	if err != nil {
		return model.OAuthCredentials{}, err
	}
	defer unlock()
	creds, err := ReadCredentials(stateDir)
	if err != nil {
		return model.OAuthCredentials{}, err
	}
	entry := creds[provider]
	c, exists := entry.Accounts[entry.ActiveClientID]
	if !exists || !oauthSignedIn(c) {
		return model.OAuthCredentials{}, oauthLoginRequired("the active ChatGPT session was signed out or has no usable registration")
	}
	if !sameOAuthRegistration(c, expected) {
		return model.OAuthCredentials{}, oauthLoginRequired("the active ChatGPT account changed; select it again before sending requests")
	}
	if err := checkOAuthHost(stateDir, c.HostID); err != nil {
		return model.OAuthCredentials{}, err
	}
	replaced := c.Access != expected.Access || c.Refresh != expected.Refresh || c.Expires != expected.Expires
	fresh := c.Expires > time.Now().Add(30*time.Second).UnixMilli()
	if force && replaced && !fresh {
		return model.OAuthCredentials{}, fmt.Errorf("another process replaced the ChatGPT token but it is still near expiry; retry or reconnect through /providers")
	}
	if fresh && (!force || replaced) {
		return c, nil
	}
	if refresh == nil {
		return model.OAuthCredentials{}, fmt.Errorf("ChatGPT token refresh is unavailable; reconnect through /providers")
	}
	updated, err := refresh(ctx, c)
	if err != nil {
		return model.OAuthCredentials{}, err // Preserve tokens on transient failure.
	}
	if !sameOAuthRegistration(c, updated) {
		return model.OAuthCredentials{}, fmt.Errorf("ChatGPT refresh returned a different client/identity/host mapping; stored credentials were not replaced")
	}
	if err := validateOAuthTokens(updated); err != nil {
		return model.OAuthCredentials{}, err
	}
	entry.Accounts[c.ClientID] = updated
	creds[provider] = entry
	if err := writeCredentials(stateDir, creds); err != nil {
		return model.OAuthCredentials{}, fmt.Errorf("persisting rotated ChatGPT credentials failed; no request was sent with the unpersisted token: %w", err)
	}
	return updated, nil
}
