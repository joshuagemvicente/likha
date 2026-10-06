// Package siwc_test exercises the complete local SIWC integration without an
// OpenAI account: a real loopback callback, signed OIDC tokens, protected files,
// refresh, authenticated model discovery, and Likha's unchanged agent/tool loop.
// It is an automated protocol fixture, not evidence of a live subscription login.
package siwc_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
)

type protocolFixture struct {
	t                  *testing.T
	key                *rsa.PrivateKey
	transport          http.RoundTripper
	mu                 sync.Mutex
	nonce              string
	challenge          string
	redirect           string
	clientID           string
	access             string
	refresh            string
	refreshes          int
	responses          int
	postLogoutRequests int
	revoked            bool
	truncated          bool
}

func newProtocolFixture(t *testing.T) *protocolFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &protocolFixture{t: t, key: key, transport: http.DefaultTransport,
		clientID: "oaiapp_likha_fixture", access: "fixture-access", refresh: "fixture-refresh"}
	// This package's tests intentionally run serially. The production model
	// client has a pinned origin; substituting its transport leaves that policy
	// intact while ensuring no request goes to OpenAI in this test.
	http.DefaultTransport = f
	t.Cleanup(func() { http.DefaultTransport = f.transport })
	return f
}

func (f *protocolFixture) reply(status int, contentType, body string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (f *protocolFixture) jsonReply(value any) (*http.Response, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return f.reply(http.StatusOK, "application/json", string(data))
}

func (f *protocolFixture) signedIDToken() string {
	f.t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "fixture-key", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": model.ChatGPTIssuer, "aud": f.clientID,
		"sub": "verified-fixture-subject", "email": "fixture@example.invalid", "nonce": f.nonce,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (f *protocolFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	// The system browser's callback really connects to the listener. Only
	// known public OpenAI origins are replaced with fixture responses.
	if r.URL.Scheme == "http" && r.URL.Hostname() == "127.0.0.1" {
		return f.transport.RoundTrip(r)
	}
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.String() {
	case model.ChatGPTIssuer + "/.well-known/openid-configuration":
		return f.jsonReply(map[string]any{"issuer": model.ChatGPTIssuer,
			"authorization_endpoint":                model.ChatGPTIssuer + "/api/accounts/authorize",
			"token_endpoint":                        model.ChatGPTIssuer + "/api/accounts/oauth/token",
			"revocation_endpoint":                   model.ChatGPTIssuer + "/api/accounts/oauth/revoke",
			"jwks_uri":                              model.ChatGPTIssuer + "/.well-known/jwks.json",
			"id_token_signing_alg_values_supported": []string{"RS256"}})
	case model.ChatGPTIssuer + "/.well-known/jwks.json":
		return f.jsonReply(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "fixture-key", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes())}}})
	case model.ChatGPTIssuer + "/api/accounts/oauth/token":
		if r.Method != http.MethodPost {
			return nil, fmt.Errorf("token grant was not POST")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		form, err := url.ParseQuery(string(body))
		if err != nil || form.Get("client_id") != f.clientID || form.Get("resource") != model.ChatGPTResource {
			return nil, fmt.Errorf("grant did not retain the issued registration and resource")
		}
		switch form.Get("grant_type") {
		case "authorization_code":
			digest := sha256.Sum256([]byte(form.Get("code_verifier")))
			if form.Get("code") != "fixture-code" || form.Get("redirect_uri") != f.redirect || base64.RawURLEncoding.EncodeToString(digest[:]) != f.challenge {
				return nil, fmt.Errorf("authorization-code grant lost its PKCE or exact callback URI")
			}
			return f.jsonReply(map[string]any{"access_token": f.access, "refresh_token": f.refresh,
				"id_token": f.signedIDToken(), "expires_in": 3600, "token_type": "Bearer",
				"scope": "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"})
		case "refresh_token":
			if form.Get("refresh_token") != f.refresh || form.Get("scope") != "" || f.revoked {
				return f.reply(http.StatusBadRequest, "application/json", `{"error":"invalid_grant"}`)
			}
			f.refreshes++
			f.access, f.refresh = fmt.Sprintf("fixture-access-%d", f.refreshes), fmt.Sprintf("fixture-refresh-%d", f.refreshes)
			return f.jsonReply(map[string]any{"access_token": f.access, "refresh_token": f.refresh, "expires_in": 3600, "token_type": "Bearer"})
		default:
			return nil, fmt.Errorf("unexpected OAuth grant")
		}
	case model.ChatGPTIssuer + "/api/accounts/oauth/revoke":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		form, err := url.ParseQuery(string(body))
		if err != nil || r.Method != http.MethodPost || form.Get("client_id") != f.clientID || form.Get("token") != f.refresh || form.Get("token_type_hint") != "refresh_token" {
			return nil, fmt.Errorf("revocation did not target this renewable session")
		}
		f.revoked = true
		return f.reply(http.StatusOK, "text/plain", "")
	case model.ChatGPTResource + "/models":
		if f.revoked {
			f.postLogoutRequests++
		}
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+f.access || f.revoked {
			return f.reply(http.StatusUnauthorized, "application/json", `{"error":{"message":"fixture unauthorized"}}`)
		}
		return f.jsonReply(map[string]any{"models": []any{
			map[string]string{"slug": "fixture-tool-model", "display_name": "Fixture tool model", "visibility": "list"},
			map[string]string{"slug": "hidden-model", "display_name": "Hidden", "visibility": "hide"}}})
	case model.ChatGPTResource + "/responses":
		if f.revoked {
			f.postLogoutRequests++
		}
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+f.access || f.revoked {
			return f.reply(http.StatusUnauthorized, "application/json", `{"error":{"message":"fixture unauthorized"}}`)
		}
		if r.Header.Get("ChatGPT-Account-Id") != "" || r.Header.Get("originator") != "" {
			return nil, fmt.Errorf("public SIWC request carried private Codex headers")
		}
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		if string(request["stream"]) != "true" || string(request["store"]) != "false" {
			return nil, fmt.Errorf("SIWC request must stream without server-side storage")
		}
		for _, unsupported := range []string{"previous_response_id", "temperature", "max_output_tokens", "metadata", "background"} {
			if _, ok := request[unsupported]; ok {
				return nil, fmt.Errorf("unsupported SIWC request field %s", unsupported)
			}
		}
		var input []map[string]json.RawMessage
		if err := json.Unmarshal(request["input"], &input); err != nil {
			return nil, fmt.Errorf("SIWC request input must be an array")
		}
		for _, item := range input {
			if string(item["role"]) == `"system"` {
				return nil, fmt.Errorf("system message sent as a SIWC input item")
			}
		}
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(request["tools"], &tools); err != nil || len(tools) == 0 {
			return nil, fmt.Errorf("agent tools missing")
		}
		if string(tools[0]["type"]) != `"namespace"` {
			return nil, fmt.Errorf("agent tools were not grouped in a namespace")
		}
		f.responses++
		if f.responses == 1 {
			item := map[string]any{"type": "function_call", "id": "fc_fixture", "call_id": "call_fixture", "name": "read", "namespace": "likha", "arguments": `{"path":"probe.txt"}`}
			stream := event("response.output_item.added", map[string]any{"output_index": 0, "item": item}) +
				event("response.output_item.done", map[string]any{"output_index": 0, "item": item})
			if !f.truncated {
				stream += event("response.completed", map[string]any{"response": map[string]any{"status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 50, "output_tokens": 10}}})
			}
			return f.reply(http.StatusOK, "text/event-stream", stream)
		}
		foundResult := false
		for _, item := range input {
			if string(item["type"]) == `"function_call_output"` && string(item["call_id"]) == `"call_fixture"` && strings.Contains(string(item["output"]), "fixture file content") {
				foundResult = true
			}
		}
		if !foundResult {
			return nil, fmt.Errorf("Likha's tool result was not replayed in full history")
		}
		return f.reply(http.StatusOK, "text/event-stream", event("response.output_text.delta", map[string]any{"delta": "Read verified."})+
			event("response.completed", map[string]any{"response": map[string]any{"status": "completed", "output": []any{}, "usage": map[string]int{"input_tokens": 100, "output_tokens": 20}}}))
	default:
		return nil, fmt.Errorf("unexpected network destination in SIWC fixture: %s", r.URL.Hostname())
	}
}

func event(name string, payload map[string]any) string {
	payload["type"] = name
	data, _ := json.Marshal(payload)
	return "event: " + name + "\ndata: " + string(data) + "\n\n"
}

func (f *protocolFixture) openBrowser(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	q := u.Query()
	if u.Scheme != "https" || u.Host != "auth.openai.com" || u.Path != "/api/accounts/authorize" || q.Get("client_id") != "dynamic_agent_client" || q.Get("agent_name_hint") != "Likha" || q.Get("ext_agent_host_id") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
		return fmt.Errorf("browser authorization was not a fresh Likha SIWC registration")
	}
	callback, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || callback.Scheme != "http" || callback.Hostname() != "127.0.0.1" || callback.Port() == "0" || callback.Port() == "" || callback.Path != "/auth/callback" {
		return fmt.Errorf("browser authorization did not use the bound loopback callback")
	}
	f.mu.Lock()
	f.nonce, f.challenge, f.redirect = q.Get("nonce"), q.Get("code_challenge"), q.Get("redirect_uri")
	f.mu.Unlock()
	query := callback.Query()
	query.Set("code", "fixture-code")
	query.Set("state", q.Get("state"))
	query.Set("client_id", f.clientID)
	callback.RawQuery = query.Encode()
	response, err := http.Get(callback.String())
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("loopback callback rejected fixture authorization: HTTP %d", response.StatusCode)
	}
	return nil
}

func (f *protocolFixture) signIn(t *testing.T, stateDir string) model.OAuthCredentials {
	t.Helper()
	host, err := providers.EnsureOAuthHost(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tokens, err := model.BrowserLogin(ctx, model.BrowserLoginOptions{HostID: host, OpenBrowser: f.openBrowser})
	if err != nil {
		t.Fatalf("signed fixture browser login: %v", err)
	}
	creds := tokens.Credentials()
	if creds.ClientID != f.clientID || creds.Subject != "verified-fixture-subject" || creds.HostID != host || !creds.HasPlanScope() {
		t.Fatal("browser login lost its verified identity, registration, host or grant")
	}
	if err := providers.StoreOAuth(stateDir, "chatgpt", creds); err != nil {
		t.Fatal(err)
	}
	return creds
}

func storedClient(t *testing.T, stateDir string, creds model.OAuthCredentials) *model.Client {
	t.Helper()
	client, err := model.NewOAuth(model.ChatGPTResource, "fixture-tool-model", model.ChatGPTIssuer, creds.ClientID, creds)
	if err != nil {
		t.Fatal(err)
	}
	client.SetOAuthRefresher(func(ctx context.Context, expected model.OAuthCredentials, force bool) (model.OAuthCredentials, error) {
		return providers.RefreshOAuth(ctx, stateDir, "chatgpt", expected, force, func(ctx context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
			return model.RefreshCredentials(ctx, nil, old)
		})
	})
	return client
}

func TestBrowserSignInPersistsAndRunsLikhasToolLoop(t *testing.T) {
	f := newProtocolFixture(t)
	stateDir := t.TempDir()
	if err := providers.StoreKey(stateDir, "openai", "unrelated-fixture-key"); err != nil {
		t.Fatal(err)
	}
	creds := f.signIn(t, stateDir)
	// Simulate restart with an expired token. A real signed-in refresh is
	// exercised, and its rotating replacement must be persisted before use.
	creds.Expires = time.Now().Add(-time.Minute).UnixMilli()
	if err := providers.StoreOAuth(stateDir, "chatgpt", creds); err != nil {
		t.Fatal(err)
	}
	client := storedClient(t, stateDir, creds)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	details, err := client.Models(ctx)
	if err != nil || len(details) != 1 || details[0].ID != "fixture-tool-model" {
		t.Fatalf("account-specific model discovery: %v, %v", details, err)
	}
	// Independently reconstructed clients may start with the same stale
	// snapshot. They must reload the protected record, not rotate it again.
	second := storedClient(t, stateDir, creds)
	var group sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := second.Models(ctx)
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent restored client discovery: %v", err)
		}
	}
	stored, signedIn, err := providers.StoredOAuth(stateDir, "chatgpt")
	if err != nil || !signedIn || stored.Access != "fixture-access-1" || stored.Refresh != "fixture-refresh-1" || f.refreshes != 1 {
		t.Fatal("refreshed token pair was not persisted atomically exactly once")
	}
	if stored.Subject != creds.Subject || stored.ClientID != creds.ClientID || stored.HostID != creds.HostID || stored.IDToken != creds.IDToken || !stored.HasPlanScope() {
		t.Fatal("refresh without new identity or scope discarded the validated registration")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "probe.txt"), []byte("fixture file content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var events []agent.TurnEvent
	agent.RunTurn(ctx, client, repo, root, nil, "Read probe.txt", nil, nil, func(event agent.TurnEvent) { events = append(events, event) })
	tools, usage, done := 0, 0, false
	for _, event := range events {
		switch event.Kind {
		case "error":
			t.Fatalf("SIWC agent turn failed: %s", event.Text)
		case "tool_result":
			tools++
		case "usage":
			usage++
		case "done":
			done = true
		}
	}
	if !done || tools != 1 || usage != 2 || f.responses != 2 {
		t.Fatalf("unchanged agent loop did not complete both rounds: done=%t tools=%d usage=%d requests=%d", done, tools, usage, f.responses)
	}
	if err := providers.LogoutOAuth(ctx, stateDir, "chatgpt", creds.ClientID, func(ctx context.Context, c model.OAuthCredentials) error { return model.RevokeCredentials(ctx, nil, c) }); err != nil {
		t.Fatal(err)
	}
	if err := client.Check(ctx); err == nil {
		t.Fatal("client continued after account logout")
	}
	if f.postLogoutRequests != 0 {
		t.Fatal("client sent a cleared credential after local logout")
	}
	accounts, _, err := providers.OAuthAccounts(stateDir, "chatgpt")
	if err != nil || len(accounts) != 1 || accounts[0].ClientID != creds.ClientID || accounts[0].Access != "" || accounts[0].Refresh != "" || accounts[0].IDToken != "" {
		t.Fatal("logout did not retain registration while clearing all tokens")
	}
	host, err := providers.EnsureOAuthHost(stateDir)
	if err != nil || host != creds.HostID {
		t.Fatal("logout changed the stable host identity")
	}
	key, err := providers.StoredKey(stateDir, "openai")
	if err != nil || key != "unrelated-fixture-key" {
		t.Fatal("SIWC lifecycle modified an unrelated API key")
	}
}

func TestTruncatedResponsesNeverExecuteTools(t *testing.T) {
	f := newProtocolFixture(t)
	f.truncated = true
	stateDir := t.TempDir()
	client := storedClient(t, stateDir, f.signIn(t, stateDir))
	root := t.TempDir()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	agent.RunTurn(context.Background(), client, repo, root, nil, "Read probe.txt", nil, nil, func(event agent.TurnEvent) {
		if event.Kind == "tool_start" || event.Kind == "tool_result" || event.Kind == "done" {
			t.Fatalf("truncated response reached execution or success: %s", event.Kind)
		}
		if event.Kind == "error" {
			failed = true
		}
	})
	if !failed || f.responses != 1 {
		t.Fatal("truncated response was not rejected at the transport boundary")
	}
}
