package tool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/tool"
)

// userInfoHandler builds a handler that serves GET /copilot_internal/user
// and the token exchange used by the authenticator to satisfy Token().
func userInfoHandler(t *testing.T, info map[string]any) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/user":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(info)
		default:
			http.NotFound(w, r)
		}
	})
}

// seedGitHubToken writes a GitHubToken into configDir so FetchUserInfo can
// load it without going through the OAuth flow.
func seedGitHubToken(t *testing.T, configDir, cacheDir string) {
	t.Helper()
	store := auth.NewFileTokenStoreAt(configDir, cacheDir)
	err := store.SaveGitHubToken(&auth.GitHubToken{AccessToken: "test-gh-token"})
	if err != nil {
		t.Fatalf("seed github token: %v", err)
	}
}

// --- helpers for decoding output ---

type budgetOutput struct {
	Object    string            `json:"object"`
	Currency  string            `json:"currency"`
	MaxBudget float64           `json:"max_budget"`
	Spend     float64           `json:"spend"`
	Remaining float64           `json:"remaining"`
	Unlimited bool              `json:"unlimited"`
	Extras    map[string]string `json:"extras"`
}

func decodeBudget(t *testing.T, b *bytes.Buffer) budgetOutput {
	t.Helper()
	var out budgetOutput
	if err := json.Unmarshal(b.Bytes(), &out); err != nil {
		t.Fatalf("decode budget output: %v\nraw: %s", err, b.String())
	}
	return out
}

// assertErrorJSON verifies that b contains a valid JSON object with
// object="error" and that the error string contains wantSubstr.
func assertErrorJSON(t *testing.T, b *bytes.Buffer, wantSubstr string) {
	t.Helper()
	var obj struct {
		Object string `json:"object"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(b.Bytes(), &obj); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nraw: %s", err, b.String())
	}
	if obj.Object != "usage.budget" {
		t.Errorf("stdout JSON object=%q, want %q\nraw: %s", obj.Object, "usage.budget", b.String())
	}
	if wantSubstr != "" && !strings.Contains(obj.Error, wantSubstr) {
		t.Errorf("stdout JSON error field does not contain %q\ngot: %s", wantSubstr, obj.Error)
	}
}

// --- tests ---

func TestRunTool_Budget_Help(t *testing.T) {
	var out, errOut bytes.Buffer
	_ = tool.RunTool(context.Background(), &out, &errOut, nil, "budget", []string{"--help"})
	if out.Len() == 0 && errOut.Len() == 0 {
		t.Error("expected some output for budget --help")
	}
}

func TestRunTool_Budget_PremiumInteractions(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
	seedGitHubToken(t, configDir, cacheDir)

	info := map[string]any{
		"login":        "octocat",
		"copilot_plan": "pro_plus",
		"quota_snapshots": map[string]any{
			"premium_interactions": map[string]any{
				"entitlement": 300.0,
				"remaining":   250.0,
			},
			"chat": map[string]any{
				"entitlement": 1000.0,
				"remaining":   800.0,
			},
		},
		"quota_reset_date": "2026-09-01",
	}

	srv := httptest.NewServer(userInfoHandler(t, info))
	defer srv.Close()

	cfg := &server.Config{
		Name:          "copilot",
		GitHubAPIBase: srv.URL,
	}

	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget", nil)
	if err != nil {
		t.Fatalf("budget failed: %v\nstderr: %s", err, errOut.String())
	}

	result := decodeBudget(t, &out)

	if result.Object != "usage.budget" {
		t.Errorf("object = %q, want %q", result.Object, "usage.budget")
	}
	if result.Currency != "premium_requests" {
		t.Errorf("currency = %q, want %q", result.Currency, "premium_requests")
	}
	if result.MaxBudget != 300 {
		t.Errorf("max_budget = %v, want 300", result.MaxBudget)
	}
	if result.Remaining != 250 {
		t.Errorf("remaining = %v, want 250", result.Remaining)
	}
	if result.Spend != 50 {
		t.Errorf("spend = %v, want 50", result.Spend)
	}
	if result.Unlimited {
		t.Error("unlimited should be false")
	}
	if result.Extras["copilot_plan"] != "pro_plus" {
		t.Errorf("extras.copilot_plan = %q, want %q", result.Extras["copilot_plan"], "pro_plus")
	}
	if result.Extras["login"] != "octocat" {
		t.Errorf("extras.login = %q, want %q", result.Extras["login"], "octocat")
	}
	if result.Extras["quota_reset_date"] != "2026-09-01" {
		t.Errorf("extras.quota_reset_date = %q, want %q", result.Extras["quota_reset_date"], "2026-09-01")
	}
	if result.Extras["primary_source"] != "premium_interactions" {
		t.Errorf("extras.primary_source = %q, want %q", result.Extras["primary_source"], "premium_interactions")
	}
	// Both snapshots should appear in extras.
	if result.Extras["snapshot_chat_entitlement"] != "1000" {
		t.Errorf("snapshot_chat_entitlement = %q, want %q", result.Extras["snapshot_chat_entitlement"], "1000")
	}
}

func TestRunTool_Budget_ChatFallback(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
	seedGitHubToken(t, configDir, cacheDir)

	info := map[string]any{
		"login": "octocat",
		"quota_snapshots": map[string]any{
			"chat": map[string]any{
				"entitlement": 500.0,
				"remaining":   400.0,
			},
		},
	}

	srv := httptest.NewServer(userInfoHandler(t, info))
	defer srv.Close()

	cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
	var out, errOut bytes.Buffer
	if err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget", nil); err != nil {
		t.Fatalf("budget failed: %v", err)
	}

	result := decodeBudget(t, &out)
	if result.Currency != "interactions" {
		t.Errorf("currency = %q, want %q", result.Currency, "interactions")
	}
	if result.MaxBudget != 500 {
		t.Errorf("max_budget = %v, want 500", result.MaxBudget)
	}
	if result.Spend != 100 {
		t.Errorf("spend = %v, want 100", result.Spend)
	}
}

func TestRunTool_Budget_LimitedUserQuotas(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
	seedGitHubToken(t, configDir, cacheDir)

	info := map[string]any{
		"login": "octocat",
		"limited_user_quotas": map[string]any{
			"chat":        42.0,
			"completions": 100.0,
		},
		"limited_user_reset_date": "2026-09-01",
	}

	srv := httptest.NewServer(userInfoHandler(t, info))
	defer srv.Close()

	cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
	var out, errOut bytes.Buffer
	if err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget", nil); err != nil {
		t.Fatalf("budget failed: %v", err)
	}

	result := decodeBudget(t, &out)
	if result.Currency != "interactions" {
		t.Errorf("currency = %q, want %q", result.Currency, "interactions")
	}
	// chat is preferred primary; remaining is its count.
	if result.Remaining != 42 {
		t.Errorf("remaining = %v, want 42", result.Remaining)
	}
	if result.MaxBudget != 0 {
		t.Errorf("max_budget = %v, want 0 (unknown ceiling)", result.MaxBudget)
	}
	if result.Extras["limited_user_primary"] != "chat" {
		t.Errorf("extras.limited_user_primary = %q, want %q", result.Extras["limited_user_primary"], "chat")
	}
	if result.Extras["limited_user_reset_date"] != "2026-09-01" {
		t.Errorf("extras.limited_user_reset_date = %q", result.Extras["limited_user_reset_date"])
	}
}

func TestRunTool_Budget_Unlimited(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
	seedGitHubToken(t, configDir, cacheDir)

	info := map[string]any{
		"login": "octocat",
		"quota_snapshots": map[string]any{
			"premium_interactions": map[string]any{
				"unlimited": true,
			},
		},
	}

	srv := httptest.NewServer(userInfoHandler(t, info))
	defer srv.Close()

	cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
	var out, errOut bytes.Buffer
	if err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget", nil); err != nil {
		t.Fatalf("budget failed: %v", err)
	}

	result := decodeBudget(t, &out)
	if !result.Unlimited {
		t.Error("expected unlimited=true")
	}
	if result.MaxBudget != 0 {
		t.Errorf("max_budget = %v, want 0 for unlimited plan", result.MaxBudget)
	}
	if result.Spend != 0 {
		t.Errorf("spend = %v, want 0 for unlimited plan", result.Spend)
	}
}

func TestRunTool_Budget_NoQuota_NoAccess(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
	seedGitHubToken(t, configDir, cacheDir)

	// Mirrors the real upstream response for an account with no active subscription.
	info := map[string]any{
		"login":           "TheLonelyGhost",
		"copilot_plan":    "individual",
		"access_type_sku": "no_access",
	}

	srv := httptest.NewServer(userInfoHandler(t, info))
	defer srv.Close()

	cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget", nil)
	if err == nil {
		t.Fatal("expected error when account has no Copilot subscription")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "no_access") {
		t.Errorf("error should mention access_type_sku; got: %v", errMsg)
	}
	if !strings.Contains(errMsg, "TheLonelyGhost") {
		t.Errorf("error should mention the login; got: %v", errMsg)
	}
	if !strings.Contains(errMsg, "subscription") {
		t.Errorf("error should mention subscription; got: %v", errMsg)
	}
	assertErrorJSON(t, &out, "no_access")
}

func TestRunTool_Budget_NoQuota_ActivePlanNoData(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
	seedGitHubToken(t, configDir, cacheDir)

	// Account appears active (non-empty sku, plan set) but no quota data returned.
	info := map[string]any{
		"login":           "octocat",
		"copilot_plan":    "business",
		"access_type_sku": "business",
	}

	srv := httptest.NewServer(userInfoHandler(t, info))
	defer srv.Close()

	cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget", nil)
	if err == nil {
		t.Fatal("expected error when active account returns no quota data")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "octocat") {
		t.Errorf("error should mention the login; got: %v", errMsg)
	}
	if !strings.Contains(errMsg, "business") {
		t.Errorf("error should mention the plan/sku; got: %v", errMsg)
	}
	assertErrorJSON(t, &out, "octocat")
}

func TestRunTool_Budget_NoQuota_Unknown(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
	seedGitHubToken(t, configDir, cacheDir)

	// Bare response: login only, no plan/sku fields decoded.
	info := map[string]any{
		"login": "octocat",
	}

	srv := httptest.NewServer(userInfoHandler(t, info))
	defer srv.Close()

	cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget", nil)
	if err == nil {
		t.Error("expected error when no quota info available")
	} else if !strings.Contains(err.Error(), "octocat") {
		t.Errorf("error should mention the login; got: %v", err)
	}
	assertErrorJSON(t, &out, "octocat")
}

func TestRunTool_Budget_NotAuthenticated(t *testing.T) {
	// No token written to disk; should fail with auth error.
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget", nil)
	if err == nil {
		t.Error("expected error when not authenticated")
	}
	assertErrorJSON(t, &out, "not authenticated")
}

func TestRunTool_Budget_GitHubAPIBaseFlag(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
	seedGitHubToken(t, configDir, cacheDir)

	info := map[string]any{
		"login": "octocat",
		"quota_snapshots": map[string]any{
			"premium_interactions": map[string]any{
				"entitlement": 100.0,
				"remaining":   75.0,
			},
		},
	}

	srv := httptest.NewServer(userInfoHandler(t, info))
	defer srv.Close()

	// Pass the API base via flag rather than cfg, with cfg holding a wrong value.
	cfg := &server.Config{Name: "copilot", GitHubAPIBase: "http://wrong.invalid"}
	var out, errOut bytes.Buffer
	err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget",
		[]string{"--github-api-base", srv.URL})
	if err != nil {
		t.Fatalf("budget with flag override failed: %v", err)
	}

	result := decodeBudget(t, &out)
	if result.MaxBudget != 100 {
		t.Errorf("max_budget = %v, want 100", result.MaxBudget)
	}
}

func TestRunTool_Budget_Overage(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", configDir)
	t.Setenv("LLM_PROXY_CACHE_DIR", cacheDir)
	seedGitHubToken(t, configDir, cacheDir)

	info := map[string]any{
		"login": "octocat",
		"quota_snapshots": map[string]any{
			"premium_interactions": map[string]any{
				"entitlement":       300.0,
				"remaining":         0.0,
				"overage_count":     5.0,
				"overage_permitted": true,
			},
		},
	}

	srv := httptest.NewServer(userInfoHandler(t, info))
	defer srv.Close()

	cfg := &server.Config{Name: "copilot", GitHubAPIBase: srv.URL}
	var out, errOut bytes.Buffer
	if err := tool.RunTool(context.Background(), &out, &errOut, cfg, "budget", nil); err != nil {
		t.Fatalf("budget failed: %v", err)
	}

	result := decodeBudget(t, &out)
	if result.Spend != 300 {
		t.Errorf("spend = %v, want 300", result.Spend)
	}
	if result.Extras["snapshot_premium_interactions_overage_count"] != "5" {
		t.Errorf("overage_count extra = %q", result.Extras["snapshot_premium_interactions_overage_count"])
	}
	if result.Extras["snapshot_premium_interactions_overage_permitted"] != "true" {
		t.Error("expected overage_permitted extra to be set")
	}
}
