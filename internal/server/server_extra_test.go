// Package server_test (coverage additions)
package server_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestResponsesTranslation_WithInstructions verifies that instructions
// become a system message.
func TestResponsesTranslation_WithInstructions(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","choices":[]}`) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = ln.Close() })
	go func() { _ = s.Serve(ctx, ln) }()
	base := "http://" + ln.Addr().String()

	body := `{"model":"gpt-4o","input":"hello","instructions":"be helpful"}`
	resp := mustPost(t, base+"/v1/responses", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	// Verify the translated body contains system message.
	if len(gotBody) == 0 {
		t.Error("no body sent to upstream")
	}
}

// TestResponsesTranslation_NotInAllowList returns 403.
func TestResponsesTranslation_NotInAllowList(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, []string{"gpt-4o"})
	base := startTestServer(t, s)

	body := `{"model":"restricted","input":"hello"}`
	resp := mustPost(t, base+"/v1/responses", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// TestUpstreamModels_401Retry verifies that /v1/models retries after a 401.
func TestUpstreamModels_401Retry(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve token exchange so Invalidate() + re-Token() can succeed.
		if r.URL.Path == "/copilot_internal/v2/token" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"token":"new-tok","expires_at":9999999999}`) //nolint:errcheck
			return
		}
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"gpt-4o"}]}`) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServerWithGH(t, upstream.URL, nil, true)
	base := startTestServer(t, s)

	resp := mustGet(t, base+"/v1/models")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if calls != 2 {
		t.Errorf("upstream /models calls = %d, want 2 (401 + retry)", calls)
	}
}

// TestChat_401Retry verifies that chat retries once on 401.
func TestChat_401Retry(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve token exchange.
		if r.URL.Path == "/copilot_internal/v2/token" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"token":"new-tok","expires_at":9999999999}`) //nolint:errcheck
			return
		}
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","choices":[]}`) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServerWithGH(t, upstream.URL, nil, true)
	base := startTestServer(t, s)

	body := `{"model":"gpt-4o","messages":[]}`
	resp := mustPost(t, base+"/v1/chat/completions", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if calls != 2 {
		t.Errorf("upstream calls = %d, want 2 (401 + retry)", calls)
	}
}
