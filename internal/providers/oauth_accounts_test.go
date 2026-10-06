package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"likha/internal/model"
)

func oauthFixture(t *testing.T, stateDir, clientID string) model.OAuthCredentials {
	t.Helper()
	host, err := EnsureOAuthHost(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return oauthFixtureForHost(host, clientID)
}

func oauthFixtureForHost(host, clientID string) model.OAuthCredentials {
	return model.OAuthCredentials{
		Issuer: model.ChatGPTIssuer, Subject: "fixture-subject", Email: "same-email@example.invalid",
		ClientID: clientID, HostID: host, AccountID: "legacy-account-metadata",
		Refresh: "fixture-refresh-" + clientID, Access: "fixture-access-" + clientID,
		IDToken: "fixture-id-token-" + clientID, TokenType: "Bearer", Expires: time.Now().Add(time.Hour).UnixMilli(),
		Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", model.ChatGPTPlanScope},
	}
}

func TestOAuthHostPersistsBeforeLoginAndSurvivesLogout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private-state")
	host, err := EnsureOAuthHost(dir)
	if err != nil || !strings.HasPrefix(host, "urn:uuid:") {
		t.Fatalf("host = %q, error = %v", host, err)
	}
	if _, err := os.Stat(KeyFilePath(dir)); !os.IsNotExist(err) {
		t.Fatalf("pre-authorization host unexpectedly created credentials: %v", err)
	}
	c := oauthFixtureForHost(host, "oaiapp_host")
	if err := StoreOAuth(dir, "chatgpt", c); err != nil {
		t.Fatal(err)
	}
	if err := LogoutOAuth(context.Background(), dir, "chatgpt", c.ClientID, func(context.Context, model.OAuthCredentials) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, oauthHostPath(dir), filepath.Join(dir, ".providers.lock")} {
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := EnsureOAuthHost(dir); err != nil || got != host {
		t.Fatalf("host after restart/sign-out = %q, %v", got, err)
	}
	for path, want := range map[string]os.FileMode{
		dir: 0700, oauthHostPath(dir): 0600, KeyFilePath(dir): 0600, filepath.Join(dir, ".providers.lock"): 0600,
	} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Fatalf("permissions of %s = %v, %v; want %o", path, info, err, want)
		}
	}
}

func TestOAuthHostConcurrentInitialization(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := EnsureOAuthHost(dir)
			if err != nil {
				t.Error(err)
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	var want string
	for id := range ids {
		if want == "" {
			want = id
		}
		if id == "" || id != want {
			t.Fatalf("concurrent initialization returned different identities")
		}
	}
}

func TestOAuthHostMalformedOrMissingNeverOverwritesIdentity(t *testing.T) {
	for _, bad := range []string{"{broken", "null", `{}`, `{"ext_agent_host_id":" "}`} {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(oauthHostPath(dir), []byte(bad), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := EnsureOAuthHost(dir); err == nil {
				t.Fatal("malformed host identity was accepted")
			}
			if data, err := os.ReadFile(oauthHostPath(dir)); err != nil || string(data) != bad {
				t.Fatal("malformed host identity was silently overwritten")
			}
		})
	}
	dir := t.TempDir()
	c := oauthFixture(t, dir, "oaiapp_missing-host")
	if err := StoreOAuth(dir, "chatgpt", c); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(oauthHostPath(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureOAuthHost(dir); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("missing previously used host identity = %v", err)
	}
}

func TestOAuthAccountsRetainSeparateSameEmailRegistrations(t *testing.T) {
	dir := t.TempDir()
	first := oauthFixture(t, dir, "oaiapp_b-workspace")
	second := oauthFixture(t, dir, "oaiapp_a-workspace")
	if err := StoreKey(dir, "openai", "fixture-api-key"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.OAuthCredentials{first, second} {
		if err := StoreOAuth(dir, "chatgpt", c); err != nil {
			t.Fatal(err)
		}
	}
	accounts, active, err := OAuthAccounts(dir, "chatgpt")
	if err != nil || active != second.ClientID || !reflect.DeepEqual(accounts, []model.OAuthCredentials{second, first}) {
		t.Fatalf("separate sorted account registrations = %+v, %q, %v", accounts, active, err)
	}
	if got, err := StoredKey(dir, "openai"); err != nil || got != "fixture-api-key" {
		t.Fatal("storing registrations dropped another provider")
	}
	if err := SelectOAuthAccount(dir, "chatgpt", first.ClientID); err != nil {
		t.Fatal(err)
	}
	if got, signedIn, err := StoredOAuth(dir, "chatgpt"); err != nil || !signedIn || !reflect.DeepEqual(got, first) {
		t.Fatalf("selected registration did not round-trip: %+v, %v, %v", got, signedIn, err)
	}
	for _, mutate := range []func(*model.OAuthCredentials){
		func(c *model.OAuthCredentials) { c.Subject = "different-subject" },
		func(c *model.OAuthCredentials) { c.HostID = "different-host" },
		func(c *model.OAuthCredentials) { c.Issuer = "https://fixture.invalid" },
	} {
		bad := first
		mutate(&bad)
		if err := StoreOAuth(dir, "chatgpt", bad); err == nil {
			t.Fatal("issued client identity/host mismatch was accepted")
		}
	}
	if got, signedIn, err := StoredOAuth(dir, "chatgpt"); err != nil || !signedIn || !reflect.DeepEqual(got, first) {
		t.Fatal("rejected sign-in changed the active registration")
	}
}

func TestLegacyOAuthRequiresFreshBrowserSignInWithoutDiscardingData(t *testing.T) {
	t.Setenv("LIKHA_ENDPOINT", "")
	t.Setenv("LIKHA_API_KEY", "")
	t.Setenv("LIKHA_CHATGPT_API_KEY", "")
	dir := t.TempDir()
	legacy := `{"openai":"fixture-key","chatgpt":{"type":"oauth","refresh":"old-fixture-refresh","access":"old-fixture-access","expires":123,"account_id":"old-fixture-account"}}`
	if err := os.WriteFile(KeyFilePath(dir), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if _, signedIn, err := StoredOAuth(dir, "chatgpt"); err != nil || signedIn {
		t.Fatalf("legacy OAuth considered usable: %v, %v", signedIn, err)
	}
	if accounts, active, err := OAuthAccounts(dir, "chatgpt"); err != nil || len(accounts) != 0 || active != "" {
		t.Fatalf("legacy tokens became a registration: %+v, %q, %v", accounts, active, err)
	}
	if _, err := ResolveProvider("chatgpt", "", "", false, dir); !errors.Is(err, ErrOAuthLoginRequired) || !strings.Contains(err.Error(), "fresh browser sign-in") || strings.Contains(err.Error(), "--device-login") {
		t.Fatalf("legacy resolution remedy = %v", err)
	}
	if err := StoreOAuth(dir, "chatgpt", model.OAuthCredentials{Access: "unvalidated-fixture", Refresh: "fixture"}); !errors.Is(err, ErrOAuthLoginRequired) {
		t.Fatalf("unregistered sign-in error = %v", err)
	}
	if got, err := os.ReadFile(KeyFilePath(dir)); err != nil || string(got) != legacy {
		t.Fatal("legacy tokens were discarded before successful sign-in")
	}
	c := oauthFixture(t, dir, "oaiapp_migrated")
	if err := StoreOAuth(dir, "chatgpt", c); err != nil {
		t.Fatal(err)
	}
	if got, err := StoredKey(dir, "openai"); err != nil || got != "fixture-key" {
		t.Fatal("legacy API key migration was lost")
	}
	entries, err := ReadCredentials(dir)
	if err != nil || entries["chatgpt"].Refresh != "" || entries["chatgpt"].ActiveClientID != c.ClientID {
		t.Fatalf("successful migration did not replace obsolete tokens: %+v, %v", entries, err)
	}
}

func TestLogoutOAuthClearsTokensRetainsIdentityAndWarnsOnRemoteFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "confirmed"},
		{name: "network failure", err: errors.New("fixture network unavailable")},
		{name: "cancellation", err: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			other := oauthFixture(t, dir, "oaiapp_other")
			selected := oauthFixture(t, dir, "oaiapp_selected")
			for _, c := range []model.OAuthCredentials{other, selected} {
				if err := StoreOAuth(dir, "chatgpt", c); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			err := LogoutOAuth(context.Background(), dir, "chatgpt", selected.ClientID, func(_ context.Context, c model.OAuthCredentials) error {
				calls++
				if !reflect.DeepEqual(c, selected) {
					t.Fatal("revocation received tokens for another registration")
				}
				return tc.err
			})
			if calls != 1 || (err != nil) != (tc.err != nil) {
				t.Fatalf("logout calls = %d, error = %v", calls, err)
			}
			if tc.err != nil && (!errors.Is(err, tc.err) || !strings.Contains(err.Error(), "signed out locally") || !strings.Contains(err.Error(), "ChatGPT Settings")) {
				t.Fatalf("unconfirmed remote sign-out warning = %v", err)
			}
			accounts, active, err := OAuthAccounts(dir, "chatgpt")
			selected.Access, selected.Refresh, selected.IDToken, selected.Expires = "", "", "", 0
			if err != nil || active != "" || !reflect.DeepEqual(accounts, []model.OAuthCredentials{other, selected}) {
				t.Fatalf("local clearing/registration retention = %+v, %q, %v", accounts, active, err)
			}
			if _, signedIn, err := StoredOAuth(dir, "chatgpt"); err != nil || signedIn {
				t.Fatal("another retained account was silently made active")
			}
			if err := SelectOAuthAccount(dir, "chatgpt", selected.ClientID); !errors.Is(err, ErrOAuthLoginRequired) {
				t.Fatalf("signed-out registration selected without reconnect: %v", err)
			}
			if err := LogoutOAuth(context.Background(), dir, "chatgpt", selected.ClientID, nil); err != nil {
				t.Fatalf("repeated local sign-out is not idempotent: %v", err)
			}
			if err := SelectOAuthAccount(dir, "chatgpt", other.ClientID); err != nil {
				t.Fatal(err)
			}
			if err := LogoutOAuth(context.Background(), dir, "chatgpt", selected.ClientID, nil); err != nil {
				t.Fatal(err)
			}
			if _, active, err := OAuthAccounts(dir, "chatgpt"); err != nil || active != other.ClientID {
				t.Fatal("signing out an inactive account changed the active account")
			}
		})
	}
}

func TestLogoutOAuthWarnsWithoutRevocationCallback(t *testing.T) {
	dir := t.TempDir()
	c := oauthFixture(t, dir, "oaiapp_no-revoke")
	if err := StoreOAuth(dir, "chatgpt", c); err != nil {
		t.Fatal(err)
	}
	if err := LogoutOAuth(context.Background(), dir, "chatgpt", c.ClientID, nil); err == nil || !strings.Contains(err.Error(), "remote ChatGPT revocation was not confirmed") {
		t.Fatalf("missing remote revocation warning = %v", err)
	}
	if accounts, _, err := OAuthAccounts(dir, "chatgpt"); err != nil || len(accounts) != 1 || accounts[0].Refresh != "" || accounts[0].Access != "" || accounts[0].IDToken != "" {
		t.Fatal("local tokens remained after unconfirmed remote revocation")
	}
}

func TestLogoutOAuthCanceledRevokeStillPersistsLocalClearing(t *testing.T) {
	dir := t.TempDir()
	c := oauthFixture(t, dir, "oaiapp_canceled-revoke")
	if err := StoreOAuth(dir, "chatgpt", c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := LogoutOAuth(ctx, dir, "chatgpt", c.ClientID, func(ctx context.Context, _ model.OAuthCredentials) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "signed out locally") {
		t.Fatalf("canceled revocation warning = %v", err)
	}
	if _, signedIn, err := StoredOAuth(dir, "chatgpt"); err != nil || signedIn {
		t.Fatalf("context cancellation prevented local sign-out: %v, %v", signedIn, err)
	}
}

func TestRefreshOAuthSerializesConcurrentGoroutinesAndKeepsOtherCredentials(t *testing.T) {
	dir := t.TempDir()
	other := oauthFixture(t, dir, "oaiapp_a-retained")
	expected := oauthFixture(t, dir, "oaiapp_b-active")
	expected.Expires = 1
	for _, c := range []model.OAuthCredentials{other, expected} {
		if err := StoreOAuth(dir, "chatgpt", c); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	updated := expected
	updated.Access, updated.Refresh, updated.IDToken = "rotated-fixture-access", "rotated-fixture-refresh", "rotated-fixture-id"
	updated.Expires = time.Now().Add(time.Hour).UnixMilli()
	updated.Scopes = append([]string(nil), expected.Scopes...)
	updated.Scopes = append(updated.Scopes, "fixture-new-metadata")
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := RefreshOAuth(context.Background(), dir, "chatgpt", expected, i%2 == 0, func(_ context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
				calls.Add(1)
				if !reflect.DeepEqual(old, expected) {
					t.Error("refresh did not reload the expected token set")
				}
				return updated, nil
			})
			if err != nil || !reflect.DeepEqual(got, updated) {
				t.Errorf("serialized refresh = %+v, %v", got, err)
			}
		}()
	}
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := StoreKey(dir, fmt.Sprintf("fixture-provider-%d", i), "fixture-key"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("rotated %d times instead of once", calls.Load())
	}
	accounts, active, err := OAuthAccounts(dir, "chatgpt")
	if err != nil || active != expected.ClientID || !reflect.DeepEqual(accounts, []model.OAuthCredentials{other, updated}) {
		t.Fatalf("atomic token replacement lost retained data: %+v, %q, %v", accounts, active, err)
	}
	entries, err := ReadCredentials(dir)
	if err != nil || len(entries) != 7 {
		t.Fatalf("simultaneous API-key writes were lost: %d entries, %v", len(entries), err)
	}
}

func TestStoreKeyWaitsForLockedOAuthRefreshWithoutLosingEitherUpdate(t *testing.T) {
	dir := t.TempDir()
	c := oauthFixture(t, dir, "oaiapp_locked-refresh")
	c.Expires = 1
	if err := StoreOAuth(dir, "chatgpt", c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	refreshDone := make(chan error, 1)
	go func() {
		_, err := RefreshOAuth(ctx, dir, "chatgpt", c, true, func(ctx context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
			close(refreshStarted)
			select {
			case <-releaseRefresh:
			case <-ctx.Done():
				return model.OAuthCredentials{}, ctx.Err()
			}
			old.Access, old.Refresh = "locked-fixture-access", "locked-fixture-refresh"
			old.Expires = time.Now().Add(time.Hour).UnixMilli()
			return old, nil
		})
		refreshDone <- err
	}()
	<-refreshStarted
	keyStarted := make(chan struct{})
	keyDone := make(chan error, 1)
	go func() {
		close(keyStarted)
		keyDone <- StoreKey(dir, "openai", "simultaneous-fixture-key")
	}()
	<-keyStarted
	var keyErr error
	finishedBeforeRelease := false
	select {
	case keyErr = <-keyDone:
		finishedBeforeRelease = true
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRefresh)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	if !finishedBeforeRelease {
		keyErr = <-keyDone
	}
	if finishedBeforeRelease || keyErr != nil {
		t.Fatalf("unrelated key write did not wait for the credential-file lock: early=%v, error=%v", finishedBeforeRelease, keyErr)
	}
	if key, err := StoredKey(dir, "openai"); err != nil || key != "simultaneous-fixture-key" {
		t.Fatalf("locked refresh overwrote the unrelated API key: %q, %v", key, err)
	}
	if current, signedIn, err := StoredOAuth(dir, "chatgpt"); err != nil || !signedIn || current.Access != "locked-fixture-access" || current.Refresh != "locked-fixture-refresh" {
		t.Fatalf("unrelated key write overwrote refreshed OAuth tokens: %+v, %v, %v", current, signedIn, err)
	}
}

func TestRefreshOAuthExpiryGraceAndForcedRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expiry time.Duration
		force  bool
		calls  int
	}{
		{name: "cached token", expiry: time.Hour, calls: 0},
		{name: "near expiry", expiry: 5 * time.Second, calls: 1},
		{name: "expired", expiry: -time.Minute, calls: 1},
		{name: "forced 401", expiry: time.Hour, force: true, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			c := oauthFixture(t, dir, "oaiapp_expiry")
			c.Expires = time.Now().Add(tc.expiry).UnixMilli()
			if err := StoreOAuth(dir, "chatgpt", c); err != nil {
				t.Fatal(err)
			}
			calls := 0
			_, err := RefreshOAuth(context.Background(), dir, "chatgpt", c, tc.force, func(_ context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
				calls++
				old.Access, old.Refresh = "new-fixture-access", "new-fixture-refresh"
				old.Expires = time.Now().Add(time.Hour).UnixMilli()
				return old, nil
			})
			if err != nil || calls != tc.calls {
				t.Fatalf("refresh calls = %d, error = %v; want %d", calls, err, tc.calls)
			}
		})
	}
}

func TestRefreshOAuthForcedRetryDoesNotRotateReplacedNearExpiryTokenTwice(t *testing.T) {
	dir := t.TempDir()
	expected := oauthFixture(t, dir, "oaiapp_replaced-near-expiry")
	current := expected
	current.Access, current.Refresh = "already-replaced-fixture-access", "already-replaced-fixture-refresh"
	current.Expires = time.Now().Add(5 * time.Second).UnixMilli()
	if err := StoreOAuth(dir, "chatgpt", current); err != nil {
		t.Fatal(err)
	}
	got, err := RefreshOAuth(context.Background(), dir, "chatgpt", expected, true, func(context.Context, model.OAuthCredentials) (model.OAuthCredentials, error) {
		t.Error("forced stale retry rotated an already-replaced token twice")
		return current, nil
	})
	if err == nil || !strings.Contains(err.Error(), "near expiry") || !reflect.DeepEqual(got, model.OAuthCredentials{}) {
		t.Fatalf("near-expiry replacement was reused or rotated: %+v, %v", got, err)
	}
}

func TestRefreshOAuthFailureDoesNotReplaceDiskData(t *testing.T) {
	for _, tc := range []string{"transient", "identity mismatch", "missing plan scope", "missing tokens"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			c := oauthFixture(t, dir, "oaiapp_failed-refresh")
			c.Expires = 1
			if err := StoreOAuth(dir, "chatgpt", c); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(KeyFilePath(dir))
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			got, err := RefreshOAuth(context.Background(), dir, "chatgpt", c, true, func(_ context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
				calls++
				switch tc {
				case "transient":
					return model.OAuthCredentials{}, errors.New("fixture unavailable")
				case "identity mismatch":
					old.Subject = "different-subject"
				case "missing plan scope":
					old.Scopes = []string{"openid"}
				case "missing tokens":
					old.Access = ""
				}
				return old, nil
			})
			if err == nil || calls != 1 || !reflect.DeepEqual(got, model.OAuthCredentials{}) {
				t.Fatalf("unsafe refresh result = %+v, %v (%d calls)", got, err, calls)
			}
			if after, err := os.ReadFile(KeyFilePath(dir)); err != nil || !bytes.Equal(before, after) {
				t.Fatal("refresh failure changed persisted tokens")
			}
		})
	}
}

func TestRefreshOAuthPersistenceErrorNeverReturnsRotatedToken(t *testing.T) {
	dir := t.TempDir()
	c := oauthFixture(t, dir, "oaiapp_persistence")
	c.Expires = 1
	if err := StoreOAuth(dir, "chatgpt", c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(KeyFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	got, err := RefreshOAuth(context.Background(), dir, "chatgpt", c, true, func(_ context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
		// Turn the destination into a directory to fail atomic persistence
		// deterministically, even when the tests are run as root.
		if err := os.Rename(KeyFilePath(dir), filepath.Join(dir, "fixture-original.json")); err != nil {
			return model.OAuthCredentials{}, err
		}
		if err := os.Mkdir(KeyFilePath(dir), 0700); err != nil {
			return model.OAuthCredentials{}, err
		}
		old.Access, old.Refresh = "unpersisted-fixture-access", "unpersisted-fixture-refresh"
		old.Expires = time.Now().Add(time.Hour).UnixMilli()
		return old, nil
	})
	if err == nil || !strings.Contains(err.Error(), "persisting rotated") || !reflect.DeepEqual(got, model.OAuthCredentials{}) {
		t.Fatalf("unpersisted token escaped storage: %+v, %v", got, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "fixture-original.json")); err != nil || !bytes.Equal(data, before) {
		t.Fatal("failed persistence destroyed the original credential contents")
	}
}

func TestRefreshOAuthCannotRestoreSignedOutOrSwitchedAccount(t *testing.T) {
	dir := t.TempDir()
	c := oauthFixture(t, dir, "oaiapp_active")
	c.Expires = 1
	if err := StoreOAuth(dir, "chatgpt", c); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	refreshDone := make(chan error, 1)
	go func() {
		_, err := RefreshOAuth(context.Background(), dir, "chatgpt", c, true, func(_ context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
			close(started)
			<-release
			old.Access, old.Refresh = "new-fixture-access", "new-fixture-refresh"
			old.Expires = time.Now().Add(time.Hour).UnixMilli()
			return old, nil
		})
		refreshDone <- err
	}()
	<-started
	logoutDone := make(chan error, 1)
	go func() {
		logoutDone <- LogoutOAuth(context.Background(), dir, "chatgpt", c.ClientID, func(_ context.Context, current model.OAuthCredentials) error {
			if current.Refresh != "new-fixture-refresh" {
				return errors.New("revocation failed to reload the latest rotating token")
			}
			return nil
		})
	}()
	close(release)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	if err := <-logoutDone; err != nil {
		t.Fatal(err)
	}
	callback := func(context.Context, model.OAuthCredentials) (model.OAuthCredentials, error) {
		t.Error("stale session attempted a refresh after sign-out/account change")
		return c, nil
	}
	if _, err := RefreshOAuth(context.Background(), dir, "chatgpt", c, true, callback); !errors.Is(err, ErrOAuthLoginRequired) {
		t.Fatalf("signed-out stale session error = %v", err)
	}
	other := oauthFixture(t, dir, "oaiapp_other-active")
	if err := StoreOAuth(dir, "chatgpt", other); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshOAuth(context.Background(), dir, "chatgpt", c, true, callback); !errors.Is(err, ErrOAuthLoginRequired) {
		t.Fatalf("switched-account stale session error = %v", err)
	}
	if accounts, active, err := OAuthAccounts(dir, "chatgpt"); err != nil || active != other.ClientID || len(accounts) != 2 || accounts[0].Refresh != "" {
		t.Fatal("stale refresh restored the signed-out registration")
	}
}

func TestCredentialLockWaitRespectsContextCancellation(t *testing.T) {
	for _, crossProcessLock := range []bool{false, true} {
		t.Run(fmt.Sprintf("flock=%v", crossProcessLock), func(t *testing.T) {
			dir := t.TempDir()
			c := oauthFixture(t, dir, "oaiapp_cancel")
			if err := StoreOAuth(dir, "chatgpt", c); err != nil {
				t.Fatal(err)
			}
			if crossProcessLock {
				f, err := openPrivateFile(filepath.Join(dir, ".providers.lock"), true)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if locked, err := tryCredentialFileLock(f); err != nil || !locked {
					t.Fatalf("fixture flock = %v, %v", locked, err)
				}
			} else {
				unlock, err := lockCredentials(context.Background(), dir)
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if _, err := RefreshOAuth(ctx, dir, "chatgpt", c, true, nil); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lock wait ignored cancellation: %v", err)
			}
		})
	}
}

func TestRefreshOAuthSubprocessLock(t *testing.T) {
	const marker = "_LIKHA_TEST_OAUTH_LOCK_DIR"
	if dir := os.Getenv(marker); dir != "" {
		host, err := EnsureOAuthHost(dir)
		if err != nil {
			t.Fatal(err)
		}
		c := oauthFixtureForHost(host, "oaiapp_process")
		c.Expires = 1
		got, err := RefreshOAuth(context.Background(), dir, "chatgpt", c, true, func(_ context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
			f, err := os.OpenFile(filepath.Join(dir, "fixture-refresh-count"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return model.OAuthCredentials{}, err
			}
			if _, err := f.WriteString("refresh\n"); err != nil {
				f.Close()
				return model.OAuthCredentials{}, err
			}
			if err := f.Close(); err != nil {
				return model.OAuthCredentials{}, err
			}
			time.Sleep(80 * time.Millisecond)
			old.Access, old.Refresh = "process-fixture-access", "process-fixture-refresh"
			old.Expires = time.Now().Add(time.Hour).UnixMilli()
			return old, nil
		})
		if err != nil || got.Access != "process-fixture-access" {
			t.Fatalf("subprocess refresh = %+v, %v", got, err)
		}
		return
	}
	dir := t.TempDir()
	c := oauthFixture(t, dir, "oaiapp_process")
	c.Expires = 1
	if err := StoreOAuth(dir, "chatgpt", c); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 4)
	outputs := make([]bytes.Buffer, len(commands))
	for i := range commands {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRefreshOAuthSubprocessLock$", "-test.count=1")
		cmd.Env = append(os.Environ(), marker+"="+dir)
		cmd.Stdout, cmd.Stderr = &outputs[i], &outputs[i]
		commands[i] = cmd
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Errorf("refresh subprocess %d: %v\n%s", i, err, outputs[i].String())
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "fixture-refresh-count"))
	if err != nil || string(data) != "refresh\n" {
		t.Fatalf("processes rotated a refresh token more than once: %q, %v", data, err)
	}
}

func TestReadCredentialsMalformedStorageIsNotOverwritten(t *testing.T) {
	for _, data := range []string{
		`{broken`, `null`, `[]`, `{"openai":null}`, `{"openai":17}`,
		`{"chatgpt":{"type":"oauth","accounts":{"issued":null}}}`,
		`{"chatgpt":{"type":"oauth","active_client_id":"missing"}}`,
		`{"chatgpt":{"type":"oauth","accounts":[]}}`,
	} {
		t.Run(data, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(KeyFilePath(dir), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadCredentials(dir); err == nil {
				t.Fatal("malformed credential storage was accepted")
			}
			if err := StoreKey(dir, "openrouter", "fixture-key"); err == nil {
				t.Fatal("malformed storage was silently replaced")
			}
			if got, err := os.ReadFile(KeyFilePath(dir)); err != nil || string(got) != data {
				t.Fatal("malformed credential storage was overwritten")
			}
		})
	}
}

func TestStoredKeyIgnoresOAuthAndUnknownCredentialTypes(t *testing.T) {
	dir := t.TempDir()
	data := `{"legacy":"fixture-legacy","plain":{"key":"fixture-plain"},"api":{"type":"api","key":"fixture-api"},"oauth":{"type":"oauth","key":"not-an-api-key","refresh":"fixture-refresh"},"unknown":{"type":"unknown","key":"not-an-api-key"}}`
	if err := os.WriteFile(KeyFilePath(dir), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	for provider, want := range map[string]string{"legacy": "fixture-legacy", "plain": "fixture-plain", "api": "fixture-api", "oauth": "", "unknown": ""} {
		if got, err := StoredKey(dir, provider); err != nil || got != want {
			t.Fatalf("StoredKey(%s) = %q, %v; want %q", provider, got, err, want)
		}
	}
	if info, err := os.Stat(KeyFilePath(dir)); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("loose credential permissions not repaired: %v, %v", info, err)
	}
	if err := StoreKey(dir, "oauth", ""); err == nil {
		t.Fatal("API-key deletion discarded retained OAuth data")
	}
}

func TestCredentialWritesAreAtomicToConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	if err := StoreKey(dir, "openai", "fixture-original"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-done:
				readerDone <- nil
				return
			default:
			}
			data, err := os.ReadFile(KeyFilePath(dir))
			var entries map[string]StoredCredential
			if err == nil {
				err = json.Unmarshal(data, &entries)
			}
			if err != nil || entries["openai"].Key == "" {
				readerDone <- fmt.Errorf("reader observed incomplete atomic storage: %v", err)
				return
			}
		}
	}()
	for i := range 30 {
		if err := StoreKey(dir, "openai", fmt.Sprintf("fixture-key-%d", i)); err != nil {
			close(done)
			<-readerDone
			t.Fatal(err)
		}
	}
	close(done)
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".providers.json-") {
			t.Fatal("atomic write left a credential-bearing temporary file behind")
		}
	}
}

func TestCredentialStorageRejectsSymlinksAndNonregularFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "fixture-target")
	if err := os.WriteFile(target, []byte(`{"openai":"fixture-key"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, KeyFilePath(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredentials(dir); err == nil {
		t.Fatal("credential symlink was followed")
	}
	if err := StoreKey(dir, "openai", "replacement"); err == nil {
		t.Fatal("credential symlink was replaced")
	}
	if err := os.Remove(KeyFilePath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(KeyFilePath(dir), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredentials(dir); err == nil {
		t.Fatal("credential directory was accepted as a file")
	}
}
