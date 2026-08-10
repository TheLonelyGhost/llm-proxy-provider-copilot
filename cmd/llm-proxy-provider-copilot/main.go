// Command llm-proxy-provider-copilot is the GitHub Copilot provider sidecar
// for llm-proxy.
//
// In sidecar mode (no --tool flag) it reads its assigned port from the
// LLM_PROXY_PLUGIN_PORT environment variable, binds an HTTP server on
// 127.0.0.1:<port>, and serves the OpenAI-compatible surface plus management
// endpoints (/healthz, /management/reload).
//
// In tooling mode (--tool <name>) it runs a provider-specific subcommand
// without starting the HTTP server:
//
//	llm-proxy-provider-copilot --tool login [flags]
//	llm-proxy-provider-copilot --tool logout
//	llm-proxy-provider-copilot --tool list-models
//
// Configuration is read from environment variables. llm-proxy sets
// LLM_PROXY_BACKEND_CONFIG (a JSON object) before exec-ing the binary in
// tooling mode; COPILOT_* variables override individual fields.
//
//	LLM_PROXY_BACKEND_CONFIG     JSON object with all backend attributes (set by llm-proxy)
//	LLM_PROXY_BACKEND_LABEL      backend block label (set by llm-proxy)
//	LLM_PROXY_BACKEND_TYPE       plugin type name (set by llm-proxy)
//	COPILOT_NAME                 backend routing prefix (default: from LLM_PROXY_BACKEND_LABEL or "copilot")
//	COPILOT_API_BASE             upstream Copilot API base URL override
//	COPILOT_GITHUB_API_BASE      GitHub API base URL override
//	COPILOT_GITHUB_LOGIN_BASE    GitHub login base URL override
//	COPILOT_OAUTH_CLIENT_ID      OAuth app client ID override
//	COPILOT_EDITOR_VERSION       Editor-Version header override
//	COPILOT_EDITOR_PLUGIN_VERSION Editor-Plugin-Version header override
//	COPILOT_USER_AGENT           User-Agent header override
//	COPILOT_INTEGRATION_ID       Copilot-Integration-Id header override
//	COPILOT_REQUEST_TIMEOUT      per-request timeout (e.g. "60s")
//	COPILOT_MODELS               comma-separated allow-list of model ids
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/tool"
)

// Version is overridden via -ldflags at build time.
var Version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	// Parse --tool <name> from argv before loading cobra, so tooling mode
	// never requires the HTTP port to be set.
	toolName, toolArgs := parseToolFlag(os.Args[1:])
	if toolName != "" {
		cfg := configFromEnv()
		return tool.RunTool(context.Background(), os.Stdout, os.Stderr, cfg, toolName, toolArgs)
	}

	// Sidecar mode: resolve listening port.
	port, err := resolvePort()
	if err != nil {
		return err
	}

	cfg := configFromEnv()

	logger := slog.Default()
	s, err := server.New(*cfg, nil, logger)
	if err != nil {
		return fmt.Errorf("init provider: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := "127.0.0.1:" + strconv.Itoa(port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	logger.Info("llm-proxy-provider-copilot starting",
		"version", Version,
		"addr", ln.Addr().String(),
		"backend", cfg.Name,
	)
	return s.Serve(ctx, ln)
}

// parseToolFlag scans argv for --tool <name> and returns (toolName, remainingArgs).
func parseToolFlag(argv []string) (string, []string) {
	for i, arg := range argv {
		if arg == "--tool" && i+1 < len(argv) {
			return argv[i+1], argv[i+2:]
		}
		if strings.HasPrefix(arg, "--tool=") {
			return strings.TrimPrefix(arg, "--tool="), argv[i+1:]
		}
	}
	return "", nil
}

// resolvePort returns the integer port from LLM_PROXY_PLUGIN_PORT env or
// falls back to 9001.
func resolvePort() (int, error) {
	if v := os.Getenv("LLM_PROXY_PLUGIN_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 {
			return 0, fmt.Errorf("invalid LLM_PROXY_PLUGIN_PORT %q", v)
		}
		return p, nil
	}
	return 9001, nil
}

// configFromEnv builds a server.Config by merging, in increasing priority order:
//  1. Defaults baked into server.New.
//  2. LLM_PROXY_BACKEND_CONFIG — a JSON object set by llm-proxy before exec-ing
//     the binary in tooling mode. Keys mirror the HCL backend attributes (name,
//     type, api_base, github_api_base, github_login_base, oauth_client_id,
//     editor_version, editor_plugin_version, user_agent, integration_id,
//     request_timeout, models).
//  3. COPILOT_* environment variables — per-variable overrides that always win.
//
// Always returns a non-nil Config; the server applies its own defaults for any
// remaining zero-value fields.
func configFromEnv() *server.Config {
	cfg := &server.Config{}

	// Layer 1: LLM_PROXY_BACKEND_CONFIG (set by llm-proxy in tooling mode).
	if raw := os.Getenv("LLM_PROXY_BACKEND_CONFIG"); raw != "" {
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &attrs); err == nil {
			setStringField := func(dst *string, key string) {
				if v, ok := attrs[key]; ok {
					var s string
					if json.Unmarshal(v, &s) == nil {
						*dst = s
					}
				}
			}
			setStringField(&cfg.Name, "name")
			setStringField(&cfg.APIBase, "api_base")
			setStringField(&cfg.GitHubAPIBase, "github_api_base")
			setStringField(&cfg.GitHubLoginBase, "github_login_base")
			setStringField(&cfg.OAuthClientID, "oauth_client_id")
			setStringField(&cfg.EditorVersion, "editor_version")
			setStringField(&cfg.EditorPluginVersion, "editor_plugin_version")
			setStringField(&cfg.UserAgent, "user_agent")
			setStringField(&cfg.IntegrationID, "integration_id")
			setStringField(&cfg.RequestTimeout, "request_timeout")
			// models: accept both a JSON array and a comma-separated string.
			if v, ok := attrs["models"]; ok {
				var arr []string
				if json.Unmarshal(v, &arr) == nil {
					cfg.Models = arr
				} else {
					var s string
					if json.Unmarshal(v, &s) == nil {
						for _, id := range strings.Split(s, ",") {
							if id = strings.TrimSpace(id); id != "" {
								cfg.Models = append(cfg.Models, id)
							}
						}
					}
				}
			}
		}
	}

	// Layer 2: LLM_PROXY_BACKEND_LABEL provides the name when the JSON block
	// did not include one (or was absent).
	if cfg.Name == "" {
		if label := os.Getenv("LLM_PROXY_BACKEND_LABEL"); label != "" {
			cfg.Name = label
		}
	}

	// Layer 3: COPILOT_* overrides — explicit per-variable values always win.
	overrideString := func(dst *string, envKey string) {
		if v := os.Getenv(envKey); v != "" {
			*dst = v
		}
	}
	overrideString(&cfg.Name, "COPILOT_NAME")
	overrideString(&cfg.APIBase, "COPILOT_API_BASE")
	overrideString(&cfg.GitHubAPIBase, "COPILOT_GITHUB_API_BASE")
	overrideString(&cfg.GitHubLoginBase, "COPILOT_GITHUB_LOGIN_BASE")
	overrideString(&cfg.OAuthClientID, "COPILOT_OAUTH_CLIENT_ID")
	overrideString(&cfg.EditorVersion, "COPILOT_EDITOR_VERSION")
	overrideString(&cfg.EditorPluginVersion, "COPILOT_EDITOR_PLUGIN_VERSION")
	overrideString(&cfg.UserAgent, "COPILOT_USER_AGENT")
	overrideString(&cfg.IntegrationID, "COPILOT_INTEGRATION_ID")
	overrideString(&cfg.RequestTimeout, "COPILOT_REQUEST_TIMEOUT")
	if m := os.Getenv("COPILOT_MODELS"); m != "" {
		cfg.Models = nil
		for _, id := range strings.Split(m, ",") {
			if id = strings.TrimSpace(id); id != "" {
				cfg.Models = append(cfg.Models, id)
			}
		}
	}

	// Final fallback: name must never be empty.
	if cfg.Name == "" {
		cfg.Name = "copilot"
	}

	return cfg
}
