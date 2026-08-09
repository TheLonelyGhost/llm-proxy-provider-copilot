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
