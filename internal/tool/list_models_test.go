// list_models_test.go exercises the --tool list-models subcommand.
package tool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/tool"
)

// TestRunTool_ListModels_Help verifies the subcommand is registered and
// produces help output.
func TestRunTool_ListModels_Help(t *testing.T) {
	var out, errOut bytes.Buffer
	_ = tool.RunTool(context.Background(), &out, &errOut, nil, "list-models", []string{"--help"})
	if out.Len() == 0 && errOut.Len() == 0 {
		t.Error("expected some output for list-models --help")
	}
}

// TestRunTool_ListModels_WithUpstream drives a fake upstream and verifies
// that list-models prints the catalogue (unfiltered by any allow-list) one
// model per line.
func TestRunTool_ListModels_WithUpstream(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)

	// Fake Copilot upstream returns two models plus one disabled model.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			resp := map[string]any{
				"token":      "fake-copilot-tok",
				"expires_at": time.Now().Add(time.Hour).Unix(),
			}
			b, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		case "/models":
			resp := map[string]any{
				"data": []map[string]any{
					{"id": "gpt-4o"},
					{"id": "claude-3.5-sonnet"},
					{"id": "disabled-model", "policy": map[string]string{"state": "disabled"}},
				},
			}
			b, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	// Seed a GitHub token so the authenticator can exchange it.
	store := auth.NewFileTokenStoreAt(configDir, cacheDir)
	if err := store.SaveGitHubToken(&auth.GitHubToken{AccessToken: "fake-gh-token"}); err != nil {
		t.Fatalf("seed github token: %v", err)
	}

	cfg := &server.Config{
		Name:          "copilot",
		APIBase:       upstream.URL,
		GitHubAPIBase: upstream.URL,
	}

	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "list-models", nil)
	if err != nil {
		t.Fatalf("list-models failed: %v\n%s", err, errOut.String())
	}

	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	wantSet := map[string]bool{"gpt-4o": true, "claude-3.5-sonnet": true}

	for _, id := range got {
		if !wantSet[id] {
			t.Errorf("unexpected model in output: %q", id)
		}
		delete(wantSet, id)
	}
	for id := range wantSet {
		t.Errorf("expected model %q not in output", id)
	}
	// Disabled model must not appear.
	if strings.Contains(out.String(), "disabled-model") {
		t.Error("disabled-model should not appear in list-models output")
	}
}

// TestRunTool_ListModels_IgnoresAllowList verifies that list-models is NOT
// filtered by the backend allow-list (per contract).
func TestRunTool_ListModels_IgnoresAllowList(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			resp := map[string]any{
				"token":      "fake-copilot-tok",
				"expires_at": time.Now().Add(time.Hour).Unix(),
			}
			b, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		case "/models":
			resp := map[string]any{
				"data": []map[string]any{
					{"id": "gpt-4o"},
					{"id": "o3-mini"},
				},
			}
			b, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := auth.NewFileTokenStoreAt(configDir, cacheDir)
	if err := store.SaveGitHubToken(&auth.GitHubToken{AccessToken: "fake-gh-token"}); err != nil {
		t.Fatalf("seed github token: %v", err)
	}

	// Only gpt-4o is in the allow-list; list-models must still return both.
	cfg := &server.Config{
		Name:          "copilot",
		APIBase:       upstream.URL,
		GitHubAPIBase: upstream.URL,
		Models:        []string{"gpt-4o"},
	}

	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "list-models", nil)
	if err != nil {
		t.Fatalf("list-models failed: %v\n%s", err, errOut.String())
	}

	if !strings.Contains(out.String(), "o3-mini") {
		t.Error("list-models should include o3-mini even though it is not in the allow-list")
	}
	if !strings.Contains(out.String(), "gpt-4o") {
		t.Error("list-models should include gpt-4o")
	}
}
