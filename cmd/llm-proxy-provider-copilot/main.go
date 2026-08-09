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
//
// Configuration is read from environment variables:
//
//	COPILOT_NAME                 backend routing prefix (default: "copilot")
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

// configFromEnv builds a server.Config from COPILOT_* environment variables.
// Always returns a non-nil Config; the server applies its own defaults.
func configFromEnv() *server.Config {
	name := os.Getenv("COPILOT_NAME")
	if name == "" {
		name = "copilot"
	}

	var models []string
	if m := os.Getenv("COPILOT_MODELS"); m != "" {
		for _, id := range strings.Split(m, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				models = append(models, id)
			}
		}
	}

	return &server.Config{
		Name:                name,
		APIBase:             os.Getenv("COPILOT_API_BASE"),
		GitHubAPIBase:       os.Getenv("COPILOT_GITHUB_API_BASE"),
		GitHubLoginBase:     os.Getenv("COPILOT_GITHUB_LOGIN_BASE"),
		OAuthClientID:       os.Getenv("COPILOT_OAUTH_CLIENT_ID"),
		EditorVersion:       os.Getenv("COPILOT_EDITOR_VERSION"),
		EditorPluginVersion: os.Getenv("COPILOT_EDITOR_PLUGIN_VERSION"),
		UserAgent:           os.Getenv("COPILOT_USER_AGENT"),
		IntegrationID:       os.Getenv("COPILOT_INTEGRATION_ID"),
		RequestTimeout:      os.Getenv("COPILOT_REQUEST_TIMEOUT"),
		Models:              models,
	}
}
