// Additional auth tests covering device-code flow and error paths.
package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
)

func newAuthWithUpstream(t *testing.T, upstream *httptest.Server, store auth.TokenStore) *auth.Authenticator {
	t.Helper()
	return auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        "test-client",
		GitHubAPIBase:   upstream.URL,
		GitHubLoginBase: upstream.URL, // reuse same server for login endpoints
		UserAgent:       "test/1.0",
		EditorVersion:   "vscode/1.0",
		Store:           store,
		HTTPClient:      upstream.Client(),
		IntervalUnit:    time.Millisecond, // fast polling
	})
}

func TestRequestDeviceCode_Success(t *testing.T) {
	var upstreamURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/device/code" {
			resp := map[string]any{
				"device_code":      "dev-code",
				"user_code":        "USER-CODE",
				"verification_uri": upstreamURL + "/activate",
				"expires_in":       900,
				"interval":         5,
			}
			body, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	upstreamURL = upstream.URL

	a := newAuthWithUpstream(t, upstream, &fakeStore{})
	code, err := a.RequestDeviceCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code.DeviceCode != "dev-code" {
		t.Errorf("device code = %q", code.DeviceCode)
	}
	if code.UserCode != "USER-CODE" {
		t.Errorf("user code = %q", code.UserCode)
	}
}

func TestRequestDeviceCode_HTTPError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error")) //nolint:errcheck
	}))
	defer upstream.Close()

	a := newAuthWithUpstream(t, upstream, &fakeStore{})
	_, err := a.RequestDeviceCode(context.Background())
	if err == nil {
		t.Fatal("expected error from HTTP 500")
	}
}

func TestPollForToken_Success(t *testing.T) {
	store := &fakeStore{}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/login/oauth/access_token" {
			if calls < 2 {
				// Return authorization_pending first.
				body, _ := json.Marshal(map[string]string{"error": "authorization_pending"})
				w.Header().Set("Content-Type", "application/json")
				w.Write(body) //nolint:errcheck
				return
			}
			// On second call, return the token.
			body, _ := json.Marshal(map[string]string{
				"access_token": "gh-access-token",
				"token_type":   "bearer",
				"scope":        "read:user",
			})
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	a := newAuthWithUpstream(t, upstream, store)
	code := &auth.DeviceCodeResponse{
		DeviceCode: "dev",
		UserCode:   "CODE",
		ExpiresIn:  900,
		Interval:   1,
	}
	tok, err := a.PollForToken(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if tok == nil {
		t.Fatal("PollForToken returned nil token")
	}
	if tok.AccessToken != "gh-access-token" {
		t.Errorf("token = %q", tok.AccessToken)
	}
	// Token should be persisted.
	loaded, err := store.LoadGitHubToken()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccessToken != "gh-access-token" {
		t.Errorf("saved token = %q", loaded.AccessToken)
	}
}

func TestPollForToken_SlowDown(t *testing.T) {
	store := &fakeStore{}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body []byte
		if calls == 1 {
			body, _ = json.Marshal(map[string]any{"error": "slow_down", "interval": 1})
		} else {
			body, _ = json.Marshal(map[string]string{
				"access_token": "final-tok",
				"token_type":   "bearer",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer upstream.Close()

	a := newAuthWithUpstream(t, upstream, store)
	code := &auth.DeviceCodeResponse{DeviceCode: "d", UserCode: "U", ExpiresIn: 900, Interval: 1}
	tok, err := a.PollForToken(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if tok == nil {
		t.Fatal("PollForToken returned nil token")
	}
	if tok.AccessToken != "final-tok" {
		t.Errorf("token = %q", tok.AccessToken)
	}
}

func TestPollForToken_AccessDenied(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(map[string]string{"error": "access_denied", "error_description": "user denied"})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer upstream.Close()

	a := newAuthWithUpstream(t, upstream, &fakeStore{})
	code := &auth.DeviceCodeResponse{DeviceCode: "d", UserCode: "U", ExpiresIn: 900, Interval: 1}
	_, err := a.PollForToken(context.Background(), code)
	if err == nil {
		t.Fatal("expected error for access_denied")
	}
}

func TestPollForToken_ExpiredCode(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(map[string]string{"error": "expired_token"})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer upstream.Close()

	a := newAuthWithUpstream(t, upstream, &fakeStore{})
	code := &auth.DeviceCodeResponse{DeviceCode: "d", UserCode: "U", ExpiresIn: 900, Interval: 1}
	_, err := a.PollForToken(context.Background(), code)
	if err == nil {
		t.Fatal("expected error for expired_token")
	}
}

func TestPollForToken_ContextCancelled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(map[string]string{"error": "authorization_pending"})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer upstream.Close()

	a := newAuthWithUpstream(t, upstream, &fakeStore{})
	code := &auth.DeviceCodeResponse{DeviceCode: "d", UserCode: "U", ExpiresIn: 9999, Interval: 1}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := a.PollForToken(ctx, code)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestToken_RefreshesExpiredDisk(t *testing.T) {
	// Put an expired token on disk; Token() should re-exchange via GitHub.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/copilot_internal/v2/token" {
			body, _ := json.Marshal(map[string]any{
				"token":      "fresh",
				"expires_at": time.Now().Add(1 * time.Hour).Unix(),
			})
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	store := &fakeStore{
		ghTok: &auth.GitHubToken{AccessToken: "gh-tok"},
		cpTok: &auth.CopilotToken{Token: "old", ExpiresAt: 1}, // expired
	}
	a := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        "cid",
		GitHubAPIBase:   upstream.URL,
		GitHubLoginBase: "http://unused",
		UserAgent:       "ua",
		EditorVersion:   "vscode/1.0",
		Store:           store,
		HTTPClient:      upstream.Client(),
	})

	tok, err := a.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token != "fresh" {
		t.Errorf("token = %q, want %q", tok.Token, "fresh")
	}
}

func TestAuthenticatorAccessors(t *testing.T) {
	a := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        "cid",
		GitHubAPIBase:   "https://api.github.com",
		GitHubLoginBase: "https://github.com",
		UserAgent:       "ua",
		EditorVersion:   "v",
		Store:           &fakeStore{},
	})

	if a.LoginURL() == "" {
		t.Error("LoginURL should not be empty")
	}
	if a.GitHubLoginBase() != "https://github.com" {
		t.Errorf("GitHubLoginBase = %q", a.GitHubLoginBase())
	}
	if a.ClientID() != "cid" {
		t.Errorf("ClientID = %q", a.ClientID())
	}
}
