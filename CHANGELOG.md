# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `--tool list-models` — lists upstream Copilot model IDs one per line without starting the proxy. Output is never filtered by the backend `models` allow-list; use it to discover available model IDs or verify credentials. Documented under **"Available models"** in the README.
- `--tool budget` — reports Copilot quota and spend as JSON (`object`, `currency`, `max_budget`, `spend`, `remaining`, `unlimited`, `extras`). Always outputs valid JSON; errors are returned as `{"object":"error","error":"..."}` with a non-zero exit code so the result can be piped to `jq` unconditionally. Requires an active Copilot subscription; accounts without access receive a descriptive error identifying the plan and `access_type_sku`.
- `LLM_PROXY_BACKEND_CONFIG` environment variable is now parsed in tooling mode. llm-proxy sets this JSON object before exec-ing the binary so the tool operates on the correct backend when multiple backends share the same plugin type. `COPILOT_*` variables continue to override individual fields. `LLM_PROXY_BACKEND_LABEL` is used as the backend name fallback when neither `LLM_PROXY_BACKEND_CONFIG` nor `COPILOT_NAME` provides one.
- `SHA256SUMS` file is now generated and published with each GitHub release, listing SHA-256 checksums for all release archives.

## [0.1.0] -- 2026-08-09

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

[Unreleased]: https://github.com/thelonelyghost/llm-proxy-provider-copilot/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/thelonelyghost/llm-proxy-provider-copilot/compare/v0.1.0...v0.1.0
