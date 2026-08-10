package tool_test

// budget_schema_test.go validates that every JSON payload the budget tool call
// can produce conforms to assets/budget.schema.json at the repo root.
//
// The test skips cleanly when the schema file is absent so that the suite
// still runs in environments where the assets directory is not present.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/tool"
)

// schemaPath resolves assets/budget.schema.json relative to the repo root.
// The source file lives at internal/tool/, so the root is two levels up.
func schemaPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller unavailable; skipping schema validation")
	}
	// file is the absolute path of this source file; walk up to the repo root.
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(repoRoot, "assets", "budget.schema.json")
}

// loadSchema compiles the budget JSON schema.  Returns nil and skips the test
// if the schema file is not present.
func loadSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := schemaPath(t)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("budget.schema.json not found at %s; skipping schema validation", path)
	}

	c := jsonschema.NewCompiler()
	sch, err := c.Compile("file://" + path)
	if err != nil {
		t.Fatalf("compile budget schema: %v", err)
	}
	return sch
}

// validateAgainstSchema asserts that the JSON in buf validates against sch.
func validateAgainstSchema(t *testing.T, sch *jsonschema.Schema, buf *bytes.Buffer) {
	t.Helper()
	var v any
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}
	if err := sch.Validate(v); err != nil {
		t.Errorf("output does not conform to budget.schema.json:\n%v\nraw: %s", err, buf.String())
	}
}

// --- schema validation tests ---

// TestBudgetSchema_SuccessOutputs validates every successful budget output
// variant against the schema.
func TestBudgetSchema_SuccessOutputs(t *testing.T) {
	sch := loadSchema(t)

	cases := []struct {
		name string
		info map[string]any
	}{
		{
			name: "premium_interactions",
			info: map[string]any{
				"login":        "octocat",
				"copilot_plan": "pro_plus",
				"quota_snapshots": map[string]any{
					"premium_interactions": map[string]any{
						"entitlement": 300.0,
						"remaining":   250.0,
					},
				},
				"quota_reset_date": "2026-09-01",
			},
		},
		{
			name: "chat_fallback",
			info: map[string]any{
				"login": "octocat",
				"quota_snapshots": map[string]any{
					"chat": map[string]any{
						"entitlement": 500.0,
						"remaining":   400.0,
					},
				},
			},
		},
		{
			name: "limited_user_quotas",
			info: map[string]any{
				"login": "octocat",
				"limited_user_quotas": map[string]any{
					"chat":        42.0,
					"completions": 100.0,
				},
				"limited_user_reset_date": "2026-09-01",
			},
		},
		{
			name: "unlimited",
			info: map[string]any{
				"login": "octocat",
				"quota_snapshots": map[string]any{
					"premium_interactions": map[string]any{
						"unlimited": true,
					},
				},
			},
		},
		{
			name: "overage",
			info: map[string]any{
				"login": "octocat",
				"quota_snapshots": map[string]any{
					"premium_interactions": map[string]any{
						"entitlement":       300.0,
						"remaining":         0.0,
						"overage_count":     5.0,
						"overage_permitted": true,
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configDir := t.TempDir()
			cacheDir := t.TempDir()
			t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
			t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
			seedGitHubToken(t, configDir, cacheDir)

			srv := httptest.NewServer(userInfoHandler(t, tc.info))
			t.Cleanup(srv.Close)

			cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
			var out, errOut bytes.Buffer
			if err := runBudget(t, cfg, srv.URL, &out, &errOut); err != nil {
				t.Fatalf("budget failed: %v\nstderr: %s", err, errOut.String())
			}

			validateAgainstSchema(t, sch, &out)
		})
	}
}

// TestBudgetSchema_ErrorOutputs validates every error budget output against
// the schema.
func TestBudgetSchema_ErrorOutputs(t *testing.T) {
	sch := loadSchema(t)

	t.Run("no_quota_no_access", func(t *testing.T) {
		configDir := t.TempDir()
		cacheDir := t.TempDir()
		t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
		t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
		seedGitHubToken(t, configDir, cacheDir)

		info := map[string]any{
			"login":           "TheLonelyGhost",
			"copilot_plan":    "individual",
			"access_type_sku": "no_access",
		}
		srv := httptest.NewServer(userInfoHandler(t, info))
		t.Cleanup(srv.Close)

		cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
		var out, errOut bytes.Buffer
		_ = runBudget(t, cfg, srv.URL, &out, &errOut) // error expected; we care about stdout shape

		validateAgainstSchema(t, sch, &out)
	})

	t.Run("no_quota_active_plan", func(t *testing.T) {
		configDir := t.TempDir()
		cacheDir := t.TempDir()
		t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
		t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
		seedGitHubToken(t, configDir, cacheDir)

		info := map[string]any{
			"login":           "octocat",
			"copilot_plan":    "business",
			"access_type_sku": "business",
		}
		srv := httptest.NewServer(userInfoHandler(t, info))
		t.Cleanup(srv.Close)

		cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
		var out, errOut bytes.Buffer
		_ = runBudget(t, cfg, srv.URL, &out, &errOut)

		validateAgainstSchema(t, sch, &out)
	})

	t.Run("not_authenticated", func(t *testing.T) {
		configDir := t.TempDir()
		cacheDir := t.TempDir()
		t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
		t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
		// No token seeded; auth will fail.

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		t.Cleanup(srv.Close)

		cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
		var out, errOut bytes.Buffer
		_ = runBudget(t, cfg, srv.URL, &out, &errOut)

		validateAgainstSchema(t, sch, &out)
	})
}

// runBudget is a thin helper that calls RunTool for the budget sub-command.
func runBudget(t *testing.T, cfg *server.Config, _ string, out, errOut *bytes.Buffer) error {
	t.Helper()
	return tool.RunTool(context.Background(), out, errOut, cfg, "budget", nil)
}
