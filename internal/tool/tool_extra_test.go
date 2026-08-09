// tool_extra_test.go exercises additional login/logout branches.
package tool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/tool"
)

// TestRunTool_Login_WithFlags tests login using explicit --github-login-base flag.
func TestRunTool_Login_WithFlags(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/device/code" {
			resp := map[string]any{
				"device_code":      "dev-code",
				"user_code":        "USER-CODE",
				"verification_uri": "", // empty so verifyURL falls back
				"expires_in":       900,
				"interval":         1,
			}
			body, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck
			return
		}
		body, _ := json.Marshal(map[string]string{"error": "authorization_pending"})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())

	var out, errOut bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- tool.RunTool(ctx, &out, &errOut, nil, "login", []string{
			"--github-login-base", upstream.URL,
			"--github-api-base", upstream.URL,
			"--client-id", "test-client",
		})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	if err == nil {
		t.Error("expected error after context cancellation")
	}
}

// TestRunTool_Login_WithConfig_Fields tests login when cfg has settings populated.
func TestRunTool_Login_WithConfig_Fields(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/device/code" {
			resp := map[string]any{
				"device_code": "dev",
				"user_code":   "CODE",
				"expires_in":  900,
				"interval":    1,
			}
			body, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck
			return
		}
		body, _ := json.Marshal(map[string]string{"error": "authorization_pending"})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer upstream.Close()

	// cfg with all fields populated (exercises the "if cfg != nil" branches)
	cfg := makeCfgWithFields(upstream.URL)

	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- tool.RunTool(ctx, &out, &errOut, cfg, "login", nil)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	err := <-done
	if err == nil {
		t.Error("expected error after context cancellation")
	}
}

// TestRunTool_Logout_WithNilCfg tests logout with nil cfg.
func TestRunTool_Logout_WithNilCfg(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)

	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, nil, "logout", nil)
	if err != nil {
		t.Fatalf("logout with nil cfg failed: %v", err)
	}
}

// TestRunTool_Logout_WithPopulatedCfg exercises the cfg != nil branches in logout.
func TestRunTool_Logout_WithPopulatedCfg(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)

	cfg := makeCfgWithFields("http://unused")
	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "logout", nil)
	if err != nil {
		t.Fatalf("logout with cfg failed: %v", err)
	}
	if out.Len() == 0 {
		t.Error("expected logout confirmation output")
	}
}
