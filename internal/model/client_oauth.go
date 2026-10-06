package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// NewOAuth builds a Sign in with ChatGPT client for the public OpenAI API.
// Only a validated, issued registration with ChatGPT plan permission is usable.
// An empty model name is allowed for account-specific discovery, not inference.
// Endpoint and issuer arguments are retained for compatibility, not overrides.
func NewOAuth(baseURL, modelName, issuer, clientID string, creds OAuthCredentials) (*Client, error) {
	if strings.TrimSuffix(baseURL, "/") != ChatGPTResource {
		return nil, errors.New("Sign in with ChatGPT requires the public https://api.openai.com/v1 endpoint; endpoint overrides are not supported")
	}
	if strings.TrimSuffix(issuer, "/") != ChatGPTIssuer {
		return nil, errors.New("Sign in with ChatGPT requires the https://auth.openai.com issuer")
	}
	if err := validateClientOAuthCredentials(creds); err != nil {
		return nil, err
	}
	if clientID != creds.ClientID {
		return nil, errors.New("Sign in with ChatGPT requires the issued client ID saved with the selected account")
	}
	if creds.Access == "" && creds.Refresh == "" {
		return nil, errChatgptLoginExpired
	}
	c := &Client{
		url:   ChatGPTResource + "/responses",
		base:  ChatGPTResource,
		model: strings.TrimSpace(modelName),
		http:  noRedirectHTTPClient(),
	}
	lifetime, stop := context.WithCancel(context.Background())
	c.oauth = &oauthSession{client: c, creds: cloneClientOAuthCredentials(creds), lifetime: lifetime, stop: stop}
	return c, nil
}

func validateClientOAuthCredentials(creds OAuthCredentials) error {
	if !creds.Registered() || creds.Issuer != ChatGPTIssuer || creds.ClientID == "app_EMoamEEZ73f0CkXaXp7hrann" {
		return fmt.Errorf("%w: legacy or unregistered ChatGPT credentials; sign in again with Sign in with ChatGPT", ErrUnauthorized)
	}
	if !creds.HasPlanScope() {
		return fmt.Errorf("%w: ChatGPT plan usage is not authorized; enable ChatGPT plan usage for the selected account", ErrUnauthorized)
	}
	if !strings.EqualFold(creds.TokenType, "Bearer") {
		return fmt.Errorf("%w: unsupported ChatGPT token type; sign in again", ErrUnauthorized)
	}
	return nil
}

func cloneClientOAuthCredentials(creds OAuthCredentials) OAuthCredentials {
	creds.Scopes = append([]string(nil), creds.Scopes...)
	return creds
}

// SetOAuthRefresher installs the storage-aware token-access callback. It is
// invoked even for unexpired cached tokens so another process's logout, account
// switch, or rotation is observed before a request. The callback must reload the
// selected registration under its storage lock and persist rotations before
// returning. force asks it to replace a token rejected by HTTP 401.
// All forks share this callback and one in-process refresh flight.
func (c *Client) SetOAuthRefresher(refresh func(context.Context, OAuthCredentials, bool) (OAuthCredentials, error)) {
	if c == nil || c.oauth == nil {
		return
	}
	c.oauth.mu.Lock()
	c.oauth.refresher = refresh
	c.oauth.mu.Unlock()
}

// SetOAuthSaver retains the standalone client's persistence seam. Without a
// storage-aware refresher, successful rotations must be saved before inference.
// A failed save is returned and retried without rotating the same token again.
func (c *Client) SetOAuthSaver(save func(OAuthCredentials) error) {
	if c == nil || c.oauth == nil {
		return
	}
	c.oauth.mu.Lock()
	c.oauth.save = save
	c.oauth.mu.Unlock()
}

// InvalidateOAuth stops this local session and every fork. It cancels in-flight
// requests and refreshes and cannot be undone by a late refresh completion.
// The caller separately revokes and removes stored tokens during sign-out.
func (c *Client) InvalidateOAuth() {
	if c == nil || c.oauth == nil {
		return
	}
	c.oauth.mu.Lock()
	c.oauth.invalidateLocked()
	c.oauth.mu.Unlock()
}

var errChatgptLoginExpired = fmt.Errorf("%w: ChatGPT session is no longer usable; sign in again", ErrUnauthorized)

const (
	accessTokenGrace      = 30 * time.Second
	refreshRequestTimeout = 30 * time.Second
)

// oauthSession is shared rather than copied by Fork: a refresh token can rotate
// only once. Callbacks belong to the session, not to an individual request client.
type oauthSession struct {
	client *Client // original transport; task-fork POST gates never gate refreshes

	mu          sync.Mutex
	creds       OAuthCredentials
	version     uint64 // distinguishes rotations even if an access token is reused
	refresher   func(context.Context, OAuthCredentials, bool) (OAuthCredentials, error)
	save        func(OAuthCredentials) error
	pendingSave bool
	flight      *oauthAccessFlight
	invalidated bool
	invalidErr  error
	lifetime    context.Context
	stop        context.CancelFunc
}

type oauthAccessFlight struct {
	done    chan struct{}
	token   string
	version uint64
	err     error
}

type oauthAccessToken struct {
	value   string
	version uint64
}

func (s *oauthSession) invalidateLocked() {
	if s.invalidated {
		return
	}
	s.invalidated = true
	if s.invalidErr == nil {
		s.invalidErr = errChatgptLoginExpired
	}
	s.creds.Access, s.creds.Refresh, s.creds.IDToken = "", "", ""
	s.pendingSave = false
	s.stop()
}

func usableClientOAuthToken(creds OAuthCredentials) bool {
	return creds.Access != "" && time.Now().UnixMilli() < creds.Expires-accessTokenGrace.Milliseconds()
}

func sameClientOAuthRegistration(a, b OAuthCredentials) bool {
	return a.Issuer == b.Issuer && a.Subject == b.Subject && a.ClientID == b.ClientID && a.HostID == b.HostID
}

func (s *oauthSession) access(ctx context.Context, force bool) (string, error) {
	token, err := s.accessAfterRejection(ctx, force, oauthAccessToken{})
	return token.value, err
}

// rejected identifies the exact rejected token. A sibling or another process
// may already have replaced it; that does not require a second rotation.
func (s *oauthSession) accessAfterRejection(ctx context.Context, force bool, rejected oauthAccessToken) (oauthAccessToken, error) {
	for {
		s.mu.Lock()
		if s.invalidated {
			err := s.invalidErr
			s.mu.Unlock()
			return oauthAccessToken{}, err
		}
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return oauthAccessToken{}, err
		}
		if rejected.value != "" && (rejected.value != s.creds.Access || rejected.version != s.version) {
			force = false
		}
		if flight := s.flight; flight != nil {
			s.mu.Unlock()
			token, err := s.waitAccess(ctx, flight)
			if err != nil {
				return oauthAccessToken{}, err
			}
			// Successful waiters perform their own storage reload. Sharing a
			// refresh result must not bypass the every-access logout check.
			if rejected.value == "" || token != rejected {
				force = false
			}
			continue
		}
		if s.refresher == nil && !force && !s.pendingSave && usableClientOAuthToken(s.creds) {
			token := oauthAccessToken{value: s.creds.Access, version: s.version}
			s.mu.Unlock()
			return token, nil
		}
		flight := &oauthAccessFlight{done: make(chan struct{})}
		s.flight = flight
		old, pending := cloneClientOAuthCredentials(s.creds), s.pendingSave
		refresh, save := s.refresher, s.save
		s.mu.Unlock()

		// The first caller's cancellation must not cancel a sibling's shared
		// refresh. Each waiter honors its own context; logout cancels the flight.
		go s.runAccess(context.WithoutCancel(ctx), flight, old, pending, force, refresh, save)
		return s.waitAccess(ctx, flight)
	}
}

func (s *oauthSession) waitAccess(ctx context.Context, flight *oauthAccessFlight) (oauthAccessToken, error) {
	select {
	case <-ctx.Done():
	case <-s.lifetime.Done():
	case <-flight.done:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invalidated {
		return oauthAccessToken{}, s.invalidErr
	}
	if err := ctx.Err(); err != nil {
		return oauthAccessToken{}, err
	}
	return oauthAccessToken{value: flight.token, version: flight.version}, flight.err
}

func (s *oauthSession) runAccess(ctx context.Context, flight *oauthAccessFlight, old OAuthCredentials, pending, force bool,
	refresh func(context.Context, OAuthCredentials, bool) (OAuthCredentials, error), save func(OAuthCredentials) error) {
	requestCtx, stopRequest := s.requestContext(ctx)
	defer stopRequest()
	refreshCtx, cancel := context.WithTimeout(requestCtx, refreshRequestTimeout)
	defer cancel()

	fresh, err := old, error(nil)
	if refresh != nil {
		fresh, err = refresh(refreshCtx, cloneClientOAuthCredentials(old), force)
	} else {
		if pending {
			if save == nil {
				err = errors.New("cannot persist refreshed ChatGPT credentials; configure the credential saver before retrying")
			} else if err = save(cloneClientOAuthCredentials(old)); err != nil {
				err = fmt.Errorf("persist ChatGPT credentials: %w", err)
			} else {
				pending = false
			}
		}
		if err == nil && (force || !usableClientOAuthToken(old)) {
			if old.Refresh == "" {
				err = errChatgptLoginExpired
			} else {
				fresh, err = RefreshCredentials(refreshCtx, s.client.http, cloneClientOAuthCredentials(old))
			}
			if err == nil {
				err = validateClientOAuthReplacement(old, fresh)
			}
			if err == nil && save != nil {
				if err = refreshCtx.Err(); err == nil {
					err = save(cloneClientOAuthCredentials(fresh))
					if err != nil {
						pending = true
						err = fmt.Errorf("persist ChatGPT credentials: %w", err)
					}
				}
			}
		}
	}
	if err == nil {
		err = validateClientOAuthReplacement(old, fresh)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invalidated {
		err = s.invalidErr
	} else if err == nil {
		s.replaceCredentialsLocked(fresh)
		s.pendingSave = false
		flight.token = fresh.Access
		flight.version = s.version
	} else if pending && refresh == nil {
		// The old refresh token may already be invalid. Keep the replacement
		// for a save retry, but do not use it until persistence succeeds.
		s.replaceCredentialsLocked(fresh)
		s.pendingSave = true
	} else if terminalClientOAuthRefresh(err) {
		err = fmt.Errorf("%w: %w", errChatgptLoginExpired, err)
		s.invalidErr = err
		s.invalidateLocked()
	}
	flight.err = err
	s.flight = nil
	close(flight.done)
}

func (s *oauthSession) replaceCredentialsLocked(fresh OAuthCredentials) {
	if s.creds.Access != fresh.Access || s.creds.Refresh != fresh.Refresh || s.creds.Expires != fresh.Expires {
		s.version++
	}
	s.creds = cloneClientOAuthCredentials(fresh)
}

func validateClientOAuthReplacement(old, fresh OAuthCredentials) error {
	if err := validateClientOAuthCredentials(fresh); err != nil {
		return err
	}
	if !sameClientOAuthRegistration(old, fresh) {
		return fmt.Errorf("%w: the selected ChatGPT account registration changed; select the account again", ErrUnauthorized)
	}
	if fresh.Access == "" || fresh.Expires <= time.Now().UnixMilli() {
		return errChatgptLoginExpired
	}
	return nil
}

func terminalClientOAuthRefresh(err error) bool {
	var failure *OAuthError
	return errors.Is(err, errChatgptLoginExpired) || errors.As(err, &failure) && failure.RequiresLogin()
}

// requestContext makes local sign-out effective for both pending and active
// HTTP requests, including requests issued by another fork.
func (s *oauthSession) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.lifetime, cancel)
	if s.lifetime.Err() != nil {
		cancel()
	}
	return requestCtx, func() { stop(); cancel() }
}

// doOAuthRequest retries only a pre-stream HTTP 401, exactly once. It never
// changes providers or retries a partially consumed stream, 403, or 429.
func (c *Client) doOAuthRequest(ctx context.Context, method, route string, payload []byte, accept string) (*http.Response, error) {
	token, err := c.oauth.accessAfterRejection(ctx, false, oauthAccessToken{})
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, ChatGPTResource+route, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create ChatGPT request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token.value)
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", UserAgent)
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
		}
		if resp.StatusCode != http.StatusUnauthorized || attempt != 0 {
			return resp, nil
		}
		rejectedErr := oauthHTTPError(resp)
		resp.Body.Close()
		token, err = c.oauth.accessAfterRejection(ctx, true, token)
		if err != nil {
			return nil, fmt.Errorf("%w; refreshing the rejected ChatGPT token: %w", rejectedErr, err)
		}
	}
	panic("unreachable OAuth retry")
}

// oauthHTTPError preserves nonstandard admission bodies, structured API error
// codes/params, status, and request ID without treating 403/429 as refresh hints.
func oauthHTTPError(resp *http.Response) error {
	detail, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes+1))
	if len(detail) > maxErrorBytes {
		detail = detail[:maxErrorBytes]
	}
	message := fmt.Sprintf("ChatGPT HTTP %s", resp.Status)
	if requestID := resp.Header.Get("x-request-id"); requestID != "" {
		message += fmt.Sprintf(" (request ID %s)", requestID)
	}
	if err != nil {
		message += fmt.Sprintf(" (reading error: %v)", err)
	}
	if len(detail) != 0 {
		message += ": " + strings.TrimSpace(string(detail))
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	}
	return errors.New(message)
}
