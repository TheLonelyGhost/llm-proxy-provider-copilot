package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
)

// fakeTokenStore implements auth.TokenStore in memory for tests.
type fakeTokenStore struct {
	ghTok *auth.GitHubToken
	cpTok *auth.CopilotToken
}

func (s *fakeTokenStore) LoadGitHubToken() (*auth.GitHubToken, error) {
	if s.ghTok == nil {
		return nil, auth.ErrNotAuthenticated
	}
	return s.ghTok, nil
}
func (s *fakeTokenStore) SaveGitHubToken(t *auth.GitHubToken) error { s.ghTok = t; return nil }
func (s *fakeTokenStore) LoadCopilotToken() (*auth.CopilotToken, error) {
	return s.cpTok, nil
}
func (s *fakeTokenStore) SaveCopilotToken(t *auth.CopilotToken) error { s.cpTok = t; return nil }
func (s *fakeTokenStore) DeleteCopilotToken() error                   { s.cpTok = nil; return nil }
func (s *fakeTokenStore) DeleteAll() error {
	s.ghTok = nil
	s.cpTok = nil
	return nil
}

// newTestServer constructs a server with a fake upstream Copilot API and a
// pre-seeded in-memory token, bypassing disk I/O.
func newTestServer(t *testing.T, apiBase string, models []string) *server.Server {
	t.Helper()
	return newTestServerWithGH(t, apiBase, models, false)
}

// newTestServerWithGH optionally includes a GitHub token so the authenticator
// can re-exchange after a 401 (needed for retry tests).
func newTestServerWithGH(t *testing.T, apiBase string, models []string, withGH bool) *server.Server {
	t.Helper()

	store := &fakeTokenStore{
		cpTok: &auth.CopilotToken{Token: "fake-copilot-token", ExpiresAt: 9999999999},
	}
	if withGH {
		store.ghTok = &auth.GitHubToken{AccessToken: "fake-gh-token"}
	}
	authenticator := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        server.DefaultOAuthClientID,
		GitHubAPIBase:   apiBase, // reuse upstream for token exchange in retry tests
		GitHubLoginBase: server.DefaultGitHubLoginBase,
		UserAgent:       server.DefaultUserAgent,
		EditorVersion:   server.DefaultEditorVersion,
		Store:           store,
	})

	return server.NewWithAuth(server.Config{
		Name:    "copilot",
		APIBase: apiBase,
		Models:  models,
	}, authenticator, nil)
}

func startTestServer(t *testing.T, s *server.Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = ln.Close() })
	go func() { _ = s.Serve(ctx, ln) }()
	return "http://" + ln.Addr().String()
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // test URLs are constructed in-process
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	return resp
}

func mustPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body)) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	return resp
}

// TestHealthz verifies the /healthz endpoint returns HTTP 200.
func TestHealthz(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	resp := mustGet(t, base+"/healthz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
}

// TestReload_POST verifies the reload endpoint accepts POST.
func TestReload_POST(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	resp, err := http.Post(base+"/management/reload", "application/json", nil) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("http.Post returned nil response")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("reload status = %d, want 200", resp.StatusCode)
	}
}

// TestReload_GET verifies the reload endpoint rejects GET with 405.
func TestReload_GET(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	resp := mustGet(t, base+"/management/reload")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("reload GET status = %d, want 405", resp.StatusCode)
	}
}

// TestModels_AllowList verifies that configured models are returned from /v1/models.
func TestModels_AllowList(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, []string{"gpt-4o", "claude-3.5-sonnet"})
	base := startTestServer(t, s)

	resp := mustGet(t, base+"/v1/models")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("models status = %d", resp.StatusCode)
	}
	var out struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "list" {
		t.Errorf("object = %q, want %q", out.Object, "list")
	}
	if len(out.Data) != 2 {
		t.Errorf("model count = %d, want 2", len(out.Data))
	}
}

// TestModels_Upstream queries the upstream /models endpoint when no allow-list is set.
func TestModels_Upstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"data":[{"id":"gpt-4o"},{"id":"claude-3.5-sonnet"}]}`) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil) // no allow-list
	base := startTestServer(t, s)

	resp := mustGet(t, base+"/v1/models")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("models status = %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 2 {
		t.Errorf("model count = %d, want 2", len(out.Data))
	}
}

// TestModels_Upstream_DisabledFiltered verifies that policy=disabled models are filtered out.
func TestModels_Upstream_DisabledFiltered(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"data":[{"id":"gpt-4o"},{"id":"restricted","policy":{"state":"disabled"}}]}`) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	resp := mustGet(t, base+"/v1/models")
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 || out.Data[0].ID != "gpt-4o" {
		t.Errorf("data = %+v, expected only gpt-4o", out.Data)
	}
}

// TestChat_ForwardsToUpstream verifies that chat requests reach the upstream.
func TestChat_ForwardsToUpstream(t *testing.T) {
	chatResp := `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, chatResp) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, []string{"gpt-4o"})
	base := startTestServer(t, s)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	resp := mustPost(t, base+"/v1/chat/completions", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("chat status = %d", resp.StatusCode)
	}
}

// TestChat_NotInAllowList returns 403 when the model is not in the allow-list.
func TestChat_NotInAllowList(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, []string{"gpt-4o"})
	base := startTestServer(t, s)

	body := `{"model":"claude-3.5-sonnet","messages":[]}`
	resp := mustPost(t, base+"/v1/chat/completions", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// TestCompletions_Returns501 verifies that /v1/completions is not implemented.
func TestCompletions_Returns501(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, []string{"gpt-4o"})
	base := startTestServer(t, s)

	body := `{"model":"gpt-4o","prompt":"hi"}`
	resp := mustPost(t, base+"/v1/completions", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", resp.StatusCode)
	}
}

// TestEmbeddings_ForwardsToUpstream verifies embeddings reach the upstream.
func TestEmbeddings_ForwardsToUpstream(t *testing.T) {
	embedResp := `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embeddings" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, embedResp) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, []string{"text-embedding-3-small"})
	base := startTestServer(t, s)

	body := `{"model":"text-embedding-3-small","input":"hello"}`
	resp := mustPost(t, base+"/v1/embeddings", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("embeddings status = %d", resp.StatusCode)
	}
}

// TestResponsesTranslation verifies that /v1/responses translates and forwards.
func TestResponsesTranslation(t *testing.T) {
	chatResp := `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, chatResp) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, []string{"gpt-4o"})
	base := startTestServer(t, s)

	body := `{"model":"gpt-4o","input":"hello"}`
	resp := mustPost(t, base+"/v1/responses", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("responses status = %d", resp.StatusCode)
	}
}

// TestHeaders_SentToUpstream verifies that required Copilot headers are forwarded.
func TestHeaders_SentToUpstream(t *testing.T) {
	var gotHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","choices":[]}`) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	body := `{"model":"gpt-4o","messages":[]}`
	resp := mustPost(t, base+"/v1/chat/completions", body)
	defer resp.Body.Close()

	for _, hdr := range []string{"Editor-Version", "Editor-Plugin-Version", "Copilot-Integration-Id", "User-Agent"} {
		if gotHeaders.Get(hdr) == "" {
			t.Errorf("missing header %q in upstream request", hdr)
		}
	}
	if gotHeaders.Get("Authorization") != "Bearer fake-copilot-token" {
		t.Errorf("Authorization = %q", gotHeaders.Get("Authorization"))
	}
}

// TestNewServer_InvalidTimeout verifies that invalid request_timeout is rejected.
func TestNewServer_InvalidTimeout(t *testing.T) {
	_, err := server.New(server.Config{
		Name:           "copilot",
		RequestTimeout: "not-a-duration",
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid request_timeout")
	}
}

// TestNewServer_ZeroTimeout verifies that a non-positive request_timeout is rejected.
func TestNewServer_ZeroTimeout(t *testing.T) {
	_, err := server.New(server.Config{
		Name:           "copilot",
		RequestTimeout: "-1s",
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error for non-positive request_timeout")
	}
}

// TestNewServer_MissingName verifies that an empty Name is rejected.
func TestNewServer_MissingName(t *testing.T) {
	_, err := server.New(server.Config{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty Name")
	}
}

// TestAllowList_EmptyAllowsAll verifies that an empty allow-list permits any model.
func TestAllowList_EmptyAllowsAll(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"x","choices":[]}`) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil) // empty = all allowed
	base := startTestServer(t, s)

	body := `{"model":"any-model","messages":[]}`
	resp := mustPost(t, base+"/v1/chat/completions", body)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("empty allow-list should not return 403")
	}
}
