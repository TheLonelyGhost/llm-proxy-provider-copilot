// Package server implements the GitHub Copilot sidecar HTTP server.
//
// It exposes the OpenAI-compatible surface (/v1/chat/completions,
// /v1/completions, /v1/embeddings, /v1/responses, /v1/models) plus the
// management endpoints (/healthz, /management/reload) required by the
// llm-proxy plugin protocol.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
)

// Default constants mirroring the compiled-in provider.
const (
	DefaultAPIBase             = "https://api.githubcopilot.com"
	DefaultGitHubAPIBase       = "https://api.github.com"
	DefaultGitHubLoginBase     = "https://github.com"
	DefaultOAuthClientID       = "Iv1.b507a08c87ecfe98"
	DefaultEditorVersion       = "vscode/1.95.0"
	DefaultEditorPluginVersion = "copilot-chat/0.22.0"
	DefaultUserAgent           = "GitHubCopilotChat/0.22.0"
	DefaultIntegrationID       = "vscode-chat"
	DefaultRequestTimeout      = 60 * time.Second
)

// Config is the complete configuration for the sidecar HTTP server.
type Config struct {
	// Name is the backend routing prefix (HCL block label).
	Name string
	// APIBase is the upstream Copilot API base URL.
	APIBase string
	// GitHubAPIBase is the GitHub API base URL for token exchange.
	GitHubAPIBase string
	// GitHubLoginBase is the GitHub login base URL for device-code flow.
	GitHubLoginBase string
	// OAuthClientID is the GitHub OAuth app client ID.
	OAuthClientID string
	// EditorVersion is the Editor-Version header value.
	EditorVersion string
	// EditorPluginVersion is the Editor-Plugin-Version header value.
	EditorPluginVersion string
	// UserAgent is the User-Agent header value.
	UserAgent string
	// IntegrationID is the Copilot-Integration-Id header value.
	IntegrationID string
	// RequestTimeout is the per-request HTTP timeout string (e.g. "60s").
	RequestTimeout string
	// Models is the allow-listed model ids. If empty all models are allowed.
	Models []string
}

// Server is the GitHub Copilot sidecar HTTP server.
type Server struct {
	cfg        Config
	logger     *slog.Logger
	httpClient *http.Client
	auth       *auth.Authenticator
	allowedSet map[string]struct{} // nil = all allowed
}

// NewAuthenticatorFromConfig constructs an Authenticator from a Config,
// applying defaults for any zero-value fields. store may be nil, in which
// case a FileTokenStore rooted at the platform-default directories is used.
func NewAuthenticatorFromConfig(cfg Config, store auth.TokenStore) (*auth.Authenticator, error) {
	if cfg.OAuthClientID == "" {
		cfg.OAuthClientID = DefaultOAuthClientID
	}
	if cfg.GitHubAPIBase == "" {
		cfg.GitHubAPIBase = DefaultGitHubAPIBase
	}
	if cfg.GitHubLoginBase == "" {
		cfg.GitHubLoginBase = DefaultGitHubLoginBase
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	if cfg.EditorVersion == "" {
		cfg.EditorVersion = DefaultEditorVersion
	}
	if store == nil {
		var err error
		store, err = auth.NewFileTokenStore()
		if err != nil {
			return nil, fmt.Errorf("init token store: %w", err)
		}
	}
	return auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        cfg.OAuthClientID,
		GitHubAPIBase:   cfg.GitHubAPIBase,
		GitHubLoginBase: cfg.GitHubLoginBase,
		UserAgent:       cfg.UserAgent,
		EditorVersion:   cfg.EditorVersion,
		Store:           store,
	}), nil
}

// New constructs a Server from Config.
func New(cfg Config, store auth.TokenStore, logger *slog.Logger) (*Server, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("server.New: Name is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Apply defaults.
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	if cfg.GitHubAPIBase == "" {
		cfg.GitHubAPIBase = DefaultGitHubAPIBase
	}
	if cfg.GitHubLoginBase == "" {
		cfg.GitHubLoginBase = DefaultGitHubLoginBase
	}
	if cfg.OAuthClientID == "" {
		cfg.OAuthClientID = DefaultOAuthClientID
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

	timeout := DefaultRequestTimeout
	if cfg.RequestTimeout != "" {
		d, err := time.ParseDuration(cfg.RequestTimeout)
		if err != nil {
			return nil, fmt.Errorf("parse request_timeout %q: %w", cfg.RequestTimeout, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("request_timeout must be positive, got %s", d)
		}
		timeout = d
	}

	if store == nil {
		fileStore, err := auth.NewFileTokenStore()
		if err != nil {
			return nil, fmt.Errorf("init token store: %w", err)
		}
		store = fileStore
	}

	authenticator := auth.NewAuthenticator(auth.AuthConfig{
		ClientID:        cfg.OAuthClientID,
		GitHubAPIBase:   cfg.GitHubAPIBase,
		GitHubLoginBase: cfg.GitHubLoginBase,
		UserAgent:       cfg.UserAgent,
		EditorVersion:   cfg.EditorVersion,
		Store:           store,
		HTTPClient:      &http.Client{Timeout: timeout},
	})

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
		httpClient: &http.Client{Timeout: timeout},
		auth:       authenticator,
		allowedSet: allowedSet,
	}, nil
}

// Handler returns an http.Handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/management/reload", s.handleReload)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/chat/completions", s.handleChat)
	mux.HandleFunc("/v1/completions", s.handleCompletions)
	mux.HandleFunc("/v1/embeddings", s.handleEmbeddings)
	mux.HandleFunc("/v1/responses", s.handleResponses)
	return mux
}

// Auth exposes the underlying Authenticator for tooling subcommands.
func (s *Server) Auth() *auth.Authenticator { return s.auth }

// Reload clears the in-memory Copilot token cache.
func (s *Server) Reload() {
	s.auth.Invalidate()
	s.logger.Info("copilot: token cache cleared (reload)", "backend", s.cfg.Name)
}

// --- management endpoints ---

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.Reload()
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "reloaded\n")
}

// --- model listing ---

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.listModels(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   models,
	})
}

type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

func (s *Server) listModels(ctx context.Context) ([]modelEntry, error) {
	// If an explicit allow-list is configured, return it directly without
	// hitting the upstream catalogue.
	if len(s.allowedSet) > 0 {
		out := make([]modelEntry, 0, len(s.allowedSet))
		for id := range s.allowedSet {
			out = append(out, modelEntry{ID: id, Object: "model", OwnedBy: s.cfg.Name})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out, nil
	}
	// No allow-list: query upstream catalogue.
	upstream, err := s.fetchUpstreamModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]modelEntry, 0, len(upstream))
	for _, m := range upstream {
		out = append(out, modelEntry{ID: m, Object: "model", OwnedBy: s.cfg.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// fetchUpstreamModels queries Copilot's /models catalogue and returns
// the list of non-disabled model IDs.
func (s *Server) fetchUpstreamModels(ctx context.Context) ([]string, error) {
	resp, err := s.doOnce(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, fmt.Errorf("copilot list models: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		s.auth.Invalidate()
		resp, err = s.doOnce(ctx, http.MethodGet, "/models", nil)
		if err != nil {
			return nil, fmt.Errorf("copilot list models (retry): %w", err)
		}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read copilot models response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("copilot /models returned HTTP %d: %s",
			resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Data []struct {
			ID     string `json:"id"`
			Policy *struct {
				State string `json:"state"`
			} `json:"policy"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode copilot models: %w", err)
	}
	var out []string
	for _, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		if m.Policy != nil && strings.EqualFold(m.Policy.State, "disabled") {
			continue
		}
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out, nil
}

// --- inference endpoints ---

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	s.proxyInference(w, r, "/chat/completions")
}

func (s *Server) handleCompletions(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, `{"error":{"message":"legacy /v1/completions is supported; use /v1/chat/completions","type":"invalid_request_error","code":"unsupported"}}`, http.StatusNotImplemented)
}

func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	s.proxyInference(w, r, "/embeddings")
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	var req struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &req)
	if !s.allowed(req.Model) {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"model %q not in allow-list","type":"invalid_request_error"}}`, req.Model), http.StatusForbidden)
		return
	}

	// Translate Responses body → chat-completions body.
	chatBody, stream, err := responsesToChat(body)
	if err != nil {
		http.Error(w, fmt.Sprintf("translate request: %v", err), http.StatusBadRequest)
		return
	}

	upstream, err := s.forward(r.Context(), http.MethodPost, "/chat/completions", chatBody, stream)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Body.Close() }()
	for k, vs := range upstream.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(upstream.StatusCode)
	_, _ = io.Copy(w, upstream.Body)
}

// proxyInference reads the request body, checks the allow-list, and forwards.
func (s *Server) proxyInference(w http.ResponseWriter, r *http.Request, upstreamPath string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &req)
	if !s.allowed(req.Model) {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"model %q not in allow-list","type":"invalid_request_error"}}`, req.Model), http.StatusForbidden)
		return
	}

	resp, err := s.forward(r.Context(), http.MethodPost, upstreamPath, body, req.Stream)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// allowed reports whether model is in the allow-list (or no list is set).
func (s *Server) allowed(model string) bool {
	if s.allowedSet == nil {
		return true
	}
	_, ok := s.allowedSet[model]
	return ok
}

// --- HTTP forwarding ---

// forward dispatches an upstream HTTP request to Copilot, refreshing the
// token once on a 401.
func (s *Server) forward(ctx context.Context, method, path string, body []byte, _ bool) (*http.Response, error) {
	resp, err := s.doOnce(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// Drain and close, then refresh and retry exactly once.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		s.auth.Invalidate()
		retry, err := s.doOnce(ctx, method, path, body) //nolint:bodyclose // caller closes
		if err != nil {
			return nil, err
		}
		resp = retry
	}
	return resp, nil
}

func (s *Server) doOnce(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	tok, err := s.auth.Token(ctx)
	if err != nil {
		return nil, err
	}
	rawURL := s.cfg.APIBase + path
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	s.setHeaders(req, tok.Token)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot request: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("nil response from HTTP client")
	}
	return resp, nil
}

func (s *Server) setHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	req.Header.Set("Editor-Version", s.cfg.EditorVersion)
	req.Header.Set("Editor-Plugin-Version", s.cfg.EditorPluginVersion)
	req.Header.Set("Copilot-Integration-Id", s.cfg.IntegrationID)
	req.Header.Set("Openai-Intent", "conversation-panel")
}

// responsesToChat performs a minimal Responses API → chat-completions body
// translation.
func responsesToChat(body []byte) ([]byte, bool, error) {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false, fmt.Errorf("decode responses body: %w", err)
	}
	chat := make(map[string]json.RawMessage)

	if v, ok := req["model"]; ok {
		chat["model"] = v
	}

	var stream bool
	if v, ok := req["stream"]; ok {
		chat["stream"] = v
		_ = json.Unmarshal(v, &stream)
	}

	// max_output_tokens → max_tokens
	if v, ok := req["max_output_tokens"]; ok {
		chat["max_tokens"] = v
	}

	// instructions → prepend as system message
	var systemMsg json.RawMessage
	if v, ok := req["instructions"]; ok {
		var inst string
		if err := json.Unmarshal(v, &inst); err == nil && inst != "" {
			msg, _ := json.Marshal(map[string]string{"role": "system", "content": inst})
			systemMsg = msg
		}
	}

	// input → messages
	if v, ok := req["input"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			var msgs []json.RawMessage
			if systemMsg != nil {
				msgs = append(msgs, systemMsg)
			}
			userMsg, _ := json.Marshal(map[string]string{"role": "user", "content": s})
			msgs = append(msgs, userMsg)
			msgsBytes, _ := json.Marshal(msgs)
			chat["messages"] = msgsBytes
		} else {
			// Array of input items — pass through, optionally prepend system.
			if systemMsg != nil {
				var items []json.RawMessage
				if err := json.Unmarshal(v, &items); err == nil {
					combined := append([]json.RawMessage{systemMsg}, items...)
					combinedBytes, _ := json.Marshal(combined)
					chat["messages"] = combinedBytes
				} else {
					chat["messages"] = v
				}
			} else {
				chat["messages"] = v
			}
		}
	}

	// reasoning.effort passthrough
	if v, ok := req["reasoning"]; ok {
		chat["reasoning"] = v
	}

	// Standard chat-completions fields.
	passthrough := map[string]struct{}{
		"temperature": {}, "top_p": {}, "n": {}, "stop": {},
		"presence_penalty": {}, "frequency_penalty": {}, "logit_bias": {},
		"user": {}, "tools": {}, "tool_choice": {},
		"parallel_tool_calls": {}, "metadata": {},
	}
	for k, v := range req {
		if _, ok := passthrough[k]; ok {
			chat[k] = v
		}
	}

	out, err := json.Marshal(chat)
	return out, stream, err
}

// Serve binds on ln and runs the HTTP server until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler()} //nolint:gosec // sidecar server
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}

// Port returns the string form of port for convenient logging.
func Port(p int) string { return strconv.Itoa(p) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
