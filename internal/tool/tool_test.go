package tool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/tool"
)

func makeCfgWithFields(baseURL string) *server.Config {
	return &server.Config{
		Name:            "copilot",
		GitHubLoginBase: baseURL,
		GitHubAPIBase:   baseURL,
		OAuthClientID:   "test-client",
		UserAgent:       "test/1.0",
		EditorVersion:   "vscode/1.0",
	}
}

func TestRunTool_UnknownTool(t *testing.T) {
	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, nil, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestRunTool_Login_Help(t *testing.T) {
	var out, errOut bytes.Buffer
	// --help exits via cobra; may return an error code but always produces output.
	_ = tool.RunTool(context.Background(), &out, &errOut, nil, "login", []string{"--help"})
	if out.Len() == 0 && errOut.Len() == 0 {
		t.Error("expected some output for login --help")
	}
}

func TestRunTool_Logout_Help(t *testing.T) {
	var out, errOut bytes.Buffer
	_ = tool.RunTool(context.Background(), &out, &errOut, nil, "logout", []string{"--help"})
	if out.Len() == 0 && errOut.Len() == 0 {
		t.Error("expected some output for logout --help")
	}
}

func TestRunTool_Login_WithConfig(t *testing.T) {
	// login requires a real GitHub endpoint for device code.
	// Use a fake that returns a proper device code, then cancel context before
	// polling completes.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/device/code" {
			resp := map[string]any{
				"device_code":      "dev-code",
				"user_code":        "USER-CODE",
				"verification_uri": "http://example.com/activate",
				"expires_in":       900,
				"interval":         1,
			}
			body, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck
			return
		}
		// Poll returns authorization_pending forever; we rely on ctx cancel.
		body, _ := json.Marshal(map[string]string{"error": "authorization_pending"})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer upstream.Close()

	cfg := &server.Config{
		Name:            "copilot",
		GitHubLoginBase: upstream.URL,
		GitHubAPIBase:   upstream.URL,
	}

	ctx, cancel := context.WithCancel(context.Background())

	var out, errOut bytes.Buffer

	// Run login in a goroutine so we can cancel it.
	done := make(chan error, 1)
	go func() {
		done <- tool.RunTool(ctx, &out, &errOut, cfg, "login", nil)
	}()

	// Wait briefly for the device code request, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	// Expect context cancellation or auth error — not nil.
	if err == nil {
		t.Error("expected error after context cancellation")
	}
}

func TestRunTool_Logout_WithStore(t *testing.T) {
	// Set up env-based token store in a temp dir.
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)

	// Pre-seed a GitHub token on disk.
	store := auth.NewFileTokenStoreAt(configDir, cacheDir)
	_ = store.SaveGitHubToken(&auth.GitHubToken{AccessToken: "gh-tok"})

	cfg := &server.Config{Name: "copilot"}
	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "logout", nil)
	if err != nil {
		t.Fatalf("logout failed: %v\n%s", err, errOut.String())
	}
	if out.Len() == 0 {
		t.Error("expected logout confirmation output")
	}

	// Verify token is gone.
	_, err = store.LoadGitHubToken()
	if err == nil {
		t.Error("expected error loading github token after logout")
	}
}
