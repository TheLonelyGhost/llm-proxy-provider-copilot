package main

import (
	"os"
	"testing"
)

// TestBinaryBuilds is a compile-time guard.
func TestBinaryBuilds(_ *testing.T) {}

func TestParseToolFlag_NoFlag(t *testing.T) {
	name, args := parseToolFlag([]string{"--port", "9001"})
	if name != "" || args != nil {
		t.Errorf("got (%q, %v), want (\"\", nil)", name, args)
	}
}

func TestParseToolFlag_SpaceSeparated(t *testing.T) {
	name, args := parseToolFlag([]string{"--tool", "login", "--browser"})
	if name != "login" {
		t.Errorf("name = %q, want %q", name, "login")
	}
	if len(args) != 1 || args[0] != "--browser" {
		t.Errorf("args = %v", args)
	}
}

func TestParseToolFlag_EqualsSeparated(t *testing.T) {
	name, args := parseToolFlag([]string{"--tool=logout"})
	if name != "logout" {
		t.Errorf("name = %q, want %q", name, "logout")
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestResolvePort_FromEnv(t *testing.T) {
	t.Setenv("LLM_PROXY_PLUGIN_PORT", "9999")
	port, err := resolvePort()
	if err != nil {
		t.Fatal(err)
	}
	if port != 9999 {
		t.Errorf("port = %d, want 9999", port)
	}
}

func TestResolvePort_Default(t *testing.T) {
	t.Setenv("LLM_PROXY_PLUGIN_PORT", "")
	port, err := resolvePort()
	if err != nil {
		t.Fatal(err)
	}
	if port != 9001 {
		t.Errorf("port = %d, want 9001", port)
	}
}

func TestResolvePort_Invalid(t *testing.T) {
	t.Setenv("LLM_PROXY_PLUGIN_PORT", "notanumber")
	_, err := resolvePort()
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("COPILOT_NAME", "")
	t.Setenv("COPILOT_MODELS", "")
	t.Setenv("COPILOT_API_BASE", "")

	cfg := configFromEnv()
	if cfg.Name != "copilot" {
		t.Errorf("Name = %q, want %q", cfg.Name, "copilot")
	}
	if len(cfg.Models) != 0 {
		t.Errorf("Models = %v, want empty", cfg.Models)
	}
}

func TestConfigFromEnv_Override(t *testing.T) {
	t.Setenv("COPILOT_NAME", "mycop")
	t.Setenv("COPILOT_MODELS", "gpt-4o, claude-3.5-sonnet")
	t.Setenv("COPILOT_API_BASE", "https://custom.api")

	cfg := configFromEnv()
	if cfg.Name != "mycop" {
		t.Errorf("Name = %q, want %q", cfg.Name, "mycop")
	}
	if len(cfg.Models) != 2 {
		t.Errorf("Models = %v, want 2 items", cfg.Models)
	}
	if cfg.APIBase != "https://custom.api" {
		t.Errorf("APIBase = %q", cfg.APIBase)
	}
	_ = os.Getenv("COPILOT_NAME")
}

// TestConfigFromEnv_BackendConfig verifies that LLM_PROXY_BACKEND_CONFIG is
// parsed and its values are applied before COPILOT_* overrides.
func TestConfigFromEnv_BackendConfig(t *testing.T) {
	// Clear any COPILOT_* overrides so the JSON config is the only source.
	for _, key := range []string{
		"COPILOT_NAME", "COPILOT_API_BASE", "COPILOT_GITHUB_API_BASE",
		"COPILOT_GITHUB_LOGIN_BASE", "COPILOT_OAUTH_CLIENT_ID",
		"COPILOT_EDITOR_VERSION", "COPILOT_EDITOR_PLUGIN_VERSION",
		"COPILOT_USER_AGENT", "COPILOT_INTEGRATION_ID",
		"COPILOT_REQUEST_TIMEOUT", "COPILOT_MODELS",
		"LLM_PROXY_BACKEND_LABEL", "LLM_PROXY_BACKEND_TYPE",
	} {
		t.Setenv(key, "")
	}

	bcJSON := `{
		"name": "work-copilot",
		"type": "github-copilot",
		"api_base": "https://api.example.com",
		"github_api_base": "https://ghapi.example.com",
		"oauth_client_id": "test-client-id",
		"request_timeout": "30s",
		"models": ["gpt-4o", "o3-mini"]
	}`
	t.Setenv("LLM_PROXY_BACKEND_CONFIG", bcJSON)

	cfg := configFromEnv()

	if cfg.Name != "work-copilot" {
		t.Errorf("Name = %q, want %q", cfg.Name, "work-copilot")
	}
	if cfg.APIBase != "https://api.example.com" {
		t.Errorf("APIBase = %q", cfg.APIBase)
	}
	if cfg.GitHubAPIBase != "https://ghapi.example.com" {
		t.Errorf("GitHubAPIBase = %q", cfg.GitHubAPIBase)
	}
	if cfg.OAuthClientID != "test-client-id" {
		t.Errorf("OAuthClientID = %q", cfg.OAuthClientID)
	}
	if cfg.RequestTimeout != "30s" {
		t.Errorf("RequestTimeout = %q", cfg.RequestTimeout)
	}
	if len(cfg.Models) != 2 || cfg.Models[0] != "gpt-4o" || cfg.Models[1] != "o3-mini" {
		t.Errorf("Models = %v", cfg.Models)
	}
}

// TestConfigFromEnv_BackendConfig_CopilotOverrides verifies that explicit
// COPILOT_* variables override values from LLM_PROXY_BACKEND_CONFIG.
func TestConfigFromEnv_BackendConfig_CopilotOverrides(t *testing.T) {
	for _, key := range []string{
		"COPILOT_GITHUB_API_BASE", "COPILOT_GITHUB_LOGIN_BASE",
		"COPILOT_OAUTH_CLIENT_ID", "COPILOT_EDITOR_VERSION",
		"COPILOT_EDITOR_PLUGIN_VERSION", "COPILOT_USER_AGENT",
		"COPILOT_INTEGRATION_ID", "COPILOT_REQUEST_TIMEOUT", "COPILOT_MODELS",
		"LLM_PROXY_BACKEND_LABEL", "LLM_PROXY_BACKEND_TYPE",
	} {
		t.Setenv(key, "")
	}

	bcJSON := `{"name":"from-json","api_base":"https://json.example.com"}`
	t.Setenv("LLM_PROXY_BACKEND_CONFIG", bcJSON)
	t.Setenv("COPILOT_NAME", "from-env")
	t.Setenv("COPILOT_API_BASE", "https://env.example.com")

	cfg := configFromEnv()

	if cfg.Name != "from-env" {
		t.Errorf("Name = %q, want %q (COPILOT_NAME should override JSON)", cfg.Name, "from-env")
	}
	if cfg.APIBase != "https://env.example.com" {
		t.Errorf("APIBase = %q, want %q (COPILOT_API_BASE should override JSON)", cfg.APIBase, "https://env.example.com")
	}
}

// TestConfigFromEnv_BackendLabel_FallsBackToLabel verifies that
// LLM_PROXY_BACKEND_LABEL is used as the name when neither
// LLM_PROXY_BACKEND_CONFIG nor COPILOT_NAME provides one.
func TestConfigFromEnv_BackendLabel_FallsBackToLabel(t *testing.T) {
	for _, key := range []string{
		"LLM_PROXY_BACKEND_CONFIG", "COPILOT_NAME",
		"COPILOT_API_BASE", "COPILOT_MODELS",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("LLM_PROXY_BACKEND_LABEL", "my-backend")

	cfg := configFromEnv()
	if cfg.Name != "my-backend" {
		t.Errorf("Name = %q, want %q", cfg.Name, "my-backend")
	}
}

// TestConfigFromEnv_BackendConfig_ModelsCommaSeparated verifies that the
// models field in LLM_PROXY_BACKEND_CONFIG also accepts a comma-separated
// string (in case the HCL serialiser emits it that way).
func TestConfigFromEnv_BackendConfig_ModelsCommaSeparated(t *testing.T) {
	for _, key := range []string{"COPILOT_NAME", "COPILOT_MODELS", "LLM_PROXY_BACKEND_LABEL"} {
		t.Setenv(key, "")
	}

	bcJSON := `{"name":"copilot","models":"gpt-4o, o3-mini"}`
	t.Setenv("LLM_PROXY_BACKEND_CONFIG", bcJSON)

	cfg := configFromEnv()
	if len(cfg.Models) != 2 {
		t.Errorf("Models = %v, want 2 items from comma-separated string", cfg.Models)
	}
}
