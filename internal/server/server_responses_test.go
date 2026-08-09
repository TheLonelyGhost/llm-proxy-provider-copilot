// server_fuzz_test.go provides a seed-corpus fuzz test for responsesToChat translation.
package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestResponsesTranslation_ArrayInputWithInstructions tests array input + instructions combo.
func TestResponsesTranslation_ArrayInputWithInstructions(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","choices":[]}`) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	body := `{"model":"gpt-4o","input":[{"role":"user","content":"hello"}],"instructions":"be helpful","stream":true}`
	resp := mustPost(t, base+"/v1/responses", body)
	defer resp.Body.Close()
	// Body should be forwarded; stream=true is supported.
	if resp.StatusCode >= 500 {
		t.Errorf("status = %d, want < 500", resp.StatusCode)
	}
}

// TestResponsesTranslation_InvalidBody verifies bad JSON returns 400.
func TestResponsesTranslation_InvalidBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	resp := mustPost(t, base+"/v1/responses", "not json")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestResponses_UpstreamError returns the upstream status code.
func TestResponses_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":"rate limited"}`) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	body := `{"model":"gpt-4o","input":"hello"}`
	resp := mustPost(t, base+"/v1/responses", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
}

// TestChat_UpstreamError passes through upstream error status.
func TestChat_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":"unavailable"}`) //nolint:errcheck
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL, nil)
	base := startTestServer(t, s)

	body := `{"model":"gpt-4o","messages":[]}`
	resp := mustPost(t, base+"/v1/chat/completions", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}
