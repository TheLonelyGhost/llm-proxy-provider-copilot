# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial release. GitHub Copilot provider sidecar for llm-proxy.
- `POST /v1/chat/completions` — forwards to Copilot's `/chat/completions`.
- `POST /v1/embeddings` — forwards to Copilot's `/embeddings`.
- `POST /v1/responses` — Responses API shim translated to `/chat/completions`.
- `POST /v1/completions` — returns 501 (not supported by upstream).
- `GET /v1/models` — returns configured allow-list or queries upstream catalogue.
- `GET /healthz` and `POST /management/reload` management endpoints.
- `--tool login` — GitHub device-code OAuth flow; writes token to config dir.
- `--tool logout` — removes stored credentials.
- Token refresh: short-lived Copilot API tokens cached and refreshed transparently before expiry.
- 401 retry: automatically re-exchanges the Copilot token and retries once on upstream 401.
- `COPILOT_*` environment variables for direct binary invocation.
- `scripts/e2e-test.sh` helper for end-to-end testing: builds and sideloads the plugin into an isolated temp dir, then drives `llm-proxy` against `dev/llm-proxy.hcl`. Supports `serve`, `login`, `logout`, `models`, and `smoke` subcommands.
