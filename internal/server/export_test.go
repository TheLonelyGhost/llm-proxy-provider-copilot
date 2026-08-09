// export_test.go exposes internal hooks for the server_test package.
package server

import (
	"log/slog"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
)

// ResponsesToChat exposes the internal responsesToChat translator for fuzz testing.
var ResponsesToChat = responsesToChat

// NewWithAuth constructs a Server with a pre-built Authenticator instead of
// initialising one from disk. Used only in tests.
func NewWithAuth(cfg Config, authenticator *auth.Authenticator, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	// Apply defaults.
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	if cfg.EditorVersion == "" {
		cfg.EditorVersion = DefaultEditorVersion
	}
	if cfg.EditorPluginVersion == "" {
		cfg.EditorPluginVersion = DefaultEditorPluginVersion
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	if cfg.IntegrationID == "" {
		cfg.IntegrationID = DefaultIntegrationID
	}

	var allowedSet map[string]struct{}
	if len(cfg.Models) > 0 {
		allowedSet = make(map[string]struct{}, len(cfg.Models))
		for _, m := range cfg.Models {
			allowedSet[m] = struct{}{}
		}
	}

	return &Server{
		cfg:        cfg,
		logger:     logger,
		httpClient: authenticator.HTTPClient(),
		auth:       authenticator,
		allowedSet: allowedSet,
	}
}
