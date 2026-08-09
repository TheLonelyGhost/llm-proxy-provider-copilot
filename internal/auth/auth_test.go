package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/paths"
)

// fakeStore implements auth.TokenStore in memory.
type fakeStore struct {
	ghTok *auth.GitHubToken
	cpTok *auth.CopilotToken
}

func (s *fakeStore) LoadGitHubToken() (*auth.GitHubToken, error) {
	if s.ghTok == nil {
		return nil, auth.ErrNotAuthenticated
	}
	return s.ghTok, nil
}
func (s *fakeStore) SaveGitHubToken(t *auth.GitHubToken) error { s.ghTok = t; return nil }
func (s *fakeStore) LoadCopilotToken() (*auth.CopilotToken, error) {
	return s.cpTok, nil
}
func (s *fakeStore) SaveCopilotToken(t *auth.CopilotToken) error { s.cpTok = t; return nil }
func (s *fakeStore) DeleteCopilotToken() error                   { s.cpTok = nil; return nil }
func (s *fakeStore) DeleteAll() error {
	s.ghTok = nil
	s.cpTok = nil
	return nil
}

func TestToken_UsesCached(t *testing.T) {
	store := &fakeStore{
		cpTok: &auth.CopilotToken{Token: "cached-tok", ExpiresAt: time.Now().Add(1 * time.Hour).Unix()},
	}
	a := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        "cid",
		GitHubAPIBase:   "http://unused",
		GitHubLoginBase: "http://unused",
		UserAgent:       "ua",
		EditorVersion:   "vscode/1.0",
		Store:           store,
	})

	tok, err := a.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token != "cached-tok" {
		t.Errorf("token = %q, want %q", tok.Token, "cached-tok")
	}
}

func TestToken_NoGitHubToken_ReturnsError(t *testing.T) {
	store := &fakeStore{} // no github token
	a := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        "cid",
		GitHubAPIBase:   "http://unused",
		GitHubLoginBase: "http://unused",
		UserAgent:       "ua",
		EditorVersion:   "vscode/1.0",
		Store:           store,
	})

	_, err := a.Token(context.Background())
	if err == nil {
		t.Fatal("expected error when no GitHub token")
	}
}

func TestToken_ExchangeFromUpstream(t *testing.T) {
	copilotTok := &auth.CopilotToken{Token: "fresh-tok", ExpiresAt: time.Now().Add(1 * time.Hour).Unix()}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/copilot_internal/v2/token" {
			body, _ := json.Marshal(map[string]any{
				"token":      copilotTok.Token,
				"expires_at": copilotTok.ExpiresAt,
			})
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	store := &fakeStore{
		ghTok: &auth.GitHubToken{AccessToken: "gh-token", ObtainedAt: time.Now()},
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
	if tok.Token != "fresh-tok" {
		t.Errorf("token = %q, want %q", tok.Token, "fresh-tok")
	}
}

func TestInvalidate_ClearsCached(t *testing.T) {
	store := &fakeStore{
		cpTok: &auth.CopilotToken{Token: "cached-tok", ExpiresAt: time.Now().Add(1 * time.Hour).Unix()},
	}
	a := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        "cid",
		GitHubAPIBase:   "http://unused",
		GitHubLoginBase: "http://unused",
		UserAgent:       "ua",
		EditorVersion:   "vscode/1.0",
		Store:           store,
	})

	a.Invalidate()
	if store.cpTok != nil {
		t.Error("Invalidate should clear disk token")
	}
}

func TestLogout_ClearsAll(t *testing.T) {
	store := &fakeStore{
		ghTok: &auth.GitHubToken{AccessToken: "gh-token"},
		cpTok: &auth.CopilotToken{Token: "cp-token"},
	}
	a := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        "cid",
		GitHubAPIBase:   "http://unused",
		GitHubLoginBase: "http://unused",
		UserAgent:       "ua",
		EditorVersion:   "vscode/1.0",
		Store:           store,
	})

	if err := a.Logout(); err != nil {
		t.Fatal(err)
	}
	if store.ghTok != nil || store.cpTok != nil {
		t.Error("Logout should clear all tokens")
	}
}

func TestFetchUserInfo(t *testing.T) {
	userInfo := map[string]any{
		"login":        "octocat",
		"copilot_plan": "business",
		"quota_snapshots": map[string]any{
			"premium_interactions": map[string]any{
				"entitlement": 300,
				"remaining":   93,
			},
		},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/copilot_internal/user" {
			body, _ := json.Marshal(userInfo)
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	store := &fakeStore{
		ghTok: &auth.GitHubToken{AccessToken: "gh-token"},
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

	info, err := a.FetchUserInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Login != "octocat" {
		t.Errorf("login = %q, want %q", info.Login, "octocat")
	}
	if info.CopilotPlan != "business" {
		t.Errorf("plan = %q, want %q", info.CopilotPlan, "business")
	}
}

func TestFetchUserInfo_NoGitHubToken(t *testing.T) {
	store := &fakeStore{}
	a := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        "cid",
		GitHubAPIBase:   "http://unused",
		GitHubLoginBase: "http://unused",
		UserAgent:       "ua",
		EditorVersion:   "vscode/1.0",
		Store:           store,
	})

	_, err := a.FetchUserInfo(context.Background())
	if err == nil {
		t.Fatal("expected error when no GitHub token")
	}
}

func TestCopilotToken_Expired(t *testing.T) {
	past := &auth.CopilotToken{Token: "tok", ExpiresAt: time.Now().Add(-1 * time.Hour).Unix()}
	if !past.Expired(time.Now(), 5*time.Minute) {
		t.Error("past token should be expired")
	}

	future := &auth.CopilotToken{Token: "tok", ExpiresAt: time.Now().Add(1 * time.Hour).Unix()}
	if future.Expired(time.Now(), 5*time.Minute) {
		t.Error("future token should not be expired")
	}
}

func TestFileTokenStore_RoundTrip(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)

	store, err := auth.NewFileTokenStore()
	if err != nil {
		t.Fatal(err)
	}

	// GitHub token
	ghTok := &auth.GitHubToken{AccessToken: "gh-access", TokenType: "bearer"}
	if err := store.SaveGitHubToken(ghTok); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadGitHubToken()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccessToken != "gh-access" {
		t.Errorf("github token = %q, want %q", loaded.AccessToken, "gh-access")
	}

	// Check mode 0600
	ghPath := filepath.Join(configDir, "github_token.json")
	info, err := os.Stat(ghPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("github token mode = %o, want 0600", info.Mode().Perm())
	}

	// Copilot token
	cpTok := &auth.CopilotToken{Token: "cp-access", ExpiresAt: 9999999}
	if err := store.SaveCopilotToken(cpTok); err != nil {
		t.Fatal(err)
	}
	loaded2, err := store.LoadCopilotToken()
	if err != nil {
		t.Fatal(err)
	}
	if loaded2 == nil {
		t.Fatal("LoadCopilotToken returned nil after save")
	}
	if loaded2.Token != "cp-access" {
		t.Errorf("copilot token = %q, want %q", loaded2.Token, "cp-access")
	}

	// Delete copilot token
	if err := store.DeleteCopilotToken(); err != nil {
		t.Fatal(err)
	}
	loaded3, err := store.LoadCopilotToken()
	if err != nil {
		t.Fatal(err)
	}
	if loaded3 != nil {
		t.Error("expected nil copilot token after delete")
	}

	// DeleteAll
	if err := store.DeleteAll(); err != nil {
		t.Fatal(err)
	}
	_, err = store.LoadGitHubToken()
	if err == nil {
		t.Error("expected error loading github token after DeleteAll")
	}
}

func TestFileTokenStore_MissingGitHubToken(t *testing.T) {
	t.Setenv("LLM_PROXY_CONFIG_DIR", t.TempDir())
	t.Setenv("LLM_PROXY_CACHE_DIR", t.TempDir())

	store, err := auth.NewFileTokenStore()
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.LoadGitHubToken()
	if err == nil {
		t.Fatal("expected ErrNotAuthenticated")
	}
}

func TestNewFileTokenStoreAt(t *testing.T) {
	cfg := t.TempDir()
	cache := t.TempDir()

	store := auth.NewFileTokenStoreAt(cfg, cache)
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	// Missing token returns ErrNotAuthenticated.
	_, err := store.LoadGitHubToken()
	if err == nil {
		t.Error("expected error for missing github token")
	}
}

func TestAuthenticator_Accessors(t *testing.T) {
	a := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        "my-client-id",
		GitHubAPIBase:   "https://api.github.com",
		GitHubLoginBase: "https://github.com",
		UserAgent:       "ua",
		EditorVersion:   "vscode/1.0",
		Store:           &fakeStore{},
	})

	if a.ClientID() != "my-client-id" {
		t.Errorf("ClientID = %q", a.ClientID())
	}
	if a.GitHubLoginBase() != "https://github.com" {
		t.Errorf("GitHubLoginBase = %q", a.GitHubLoginBase())
	}
	if a.HTTPClient() == nil {
		t.Error("HTTPClient should not be nil")
	}
}

func TestNewFileTokenStore_UsesEnv(t *testing.T) {
	cfg := t.TempDir()
	cache := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", cfg)
	t.Setenv("LLM_PROXY_CACHE_DIR", cache)

	store, err := auth.NewFileTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestPaths_Integration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LLM_PROXY_CACHE_DIR", dir)

	got, err := paths.CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("CacheDir = %q, want %q", got, dir)
	}
}
