// server_coverage_test.go covers the remaining low-coverage paths.
package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
)

// TestAuth_ReturnsNonNil verifies that Auth() is accessible.
func TestAuth_ReturnsNonNil(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	if s.Auth() == nil {
		t.Error("Auth() should return non-nil Authenticator")
	}
}

// TestNewAuthenticatorFromConfig_WithStore verifies the factory uses an explicit store.
func TestNewAuthenticatorFromConfig_WithStore(t *testing.T) {
	store := &fakeTokenStore{
		cpTok: &auth.CopilotToken{Token: "tok", ExpiresAt: 9999999999},
	}
	a, err := server.NewAuthenticatorFromConfig(server.Config{
		OAuthClientID:   "cid",
		GitHubAPIBase:   "https://api.github.com",
		GitHubLoginBase: "https://github.com",
	}, store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil Authenticator")
	}
}

// TestNewAuthenticatorFromConfig_NilStore verifies the factory creates a FileTokenStore when store is nil.
func TestNewAuthenticatorFromConfig_NilStore(t *testing.T) {
	t.Setenv("LLM_PROXY_CONFIG_DIR", t.TempDir())
	t.Setenv("LLM_PROXY_CACHE_DIR", t.TempDir())

	a, err := server.NewAuthenticatorFromConfig(server.Config{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil Authenticator")
	}
}

// TestPort_Helper verifies the Port helper.
func TestPort_Helper(t *testing.T) {
	if got := server.Port(9001); got != "9001" {
		t.Errorf("Port(9001) = %q, want %q", got, "9001")
	}
}

// TestModels_UpstreamError returns 502 when upstream is down.
func TestModels_UpstreamError(t *testing.T) {
	// Upstream that refuses connections.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "internal server error") //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil) // no allow-list → hits upstream
	base := startTestServer(t, s)

	resp := mustGet(t, base+"/v1/models")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

// TestResponsesTranslation_ArrayInput tests array input in /v1/responses.
func TestResponsesTranslation_ArrayInput(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","choices":[]}`) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	// input as an array (array of messages / input items)
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"hello"}]}`
	resp := mustPost(t, base+"/v1/responses", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if len(gotBody) == 0 {
		t.Error("no body sent upstream")
	}
}

// TestResponsesTranslation_WithMaxOutputTokens tests max_output_tokens mapping.
func TestResponsesTranslation_WithMaxOutputTokens(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","choices":[]}`) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	body := `{"model":"gpt-4o","input":"hello","max_output_tokens":1024,"stream":false}`
	resp := mustPost(t, base+"/v1/responses", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if len(gotBody) == 0 {
		t.Error("no body sent upstream")
	}
}

// TestNewServer_WithRequestTimeout verifies that a valid timeout is accepted.
func TestNewServer_WithRequestTimeout(t *testing.T) {
	_, err := server.New(server.Config{
		Name:           "copilot",
		RequestTimeout: "30s",
	}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestEmbeddings_NotInAllowList returns 403 for embeddings with restricted model.
func TestEmbeddings_NotInAllowList(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, []string{"text-embedding-3-small"})
	base := startTestServer(t, s)

	body := `{"model":"other-model","input":"hello"}`
	resp := mustPost(t, base+"/v1/embeddings", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}
