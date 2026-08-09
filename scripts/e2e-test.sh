#!/usr/bin/env bash
# scripts/e2e-test.sh – Run llm-proxy with the copilot plugin under development,
# fully isolated from any global llm-proxy configuration and token cache.
#
# USAGE
#   scripts/e2e-test.sh [serve|login|logout|models|smoke]
#
#   serve    (default) Build the plugin, sideload it, then start llm-proxy.
#            Ctrl-C to stop.  The proxy listens on 127.0.0.1:14980.
#
#   login    Run the GitHub Copilot device-code OAuth flow, writing the token
#            into the isolated config dir.  Do this once before `serve`.
#
#   logout   Remove any stored GitHub Copilot credentials from the isolated
#            config and cache dirs.
#
#   models   Start the proxy in the background, call GET /v1/models, then
#            stop it.  Quick check that the plugin is wired up correctly.
#
#   smoke    Start the proxy in the background, send one chat-completions
#            request to POST /v1/chat/completions (model: gpt-4o), print the
#            response, then stop it.  Requires real credentials.
#
# ENVIRONMENT VARIABLES
#   Set these before invoking the script (or export them in your shell):
#
#   Optional:
#   COPILOT_MODELS      Comma-separated model allow-list passed to the plugin
#                       sidecar (default: "gpt-4o").  This env var is read by
#                       the sidecar directly; the HCL config already sets
#                       models = ["gpt-4o", "claude-3.5-sonnet"] independently.
#   E2E_MODEL           Model id used for the smoke test (default: "gpt-4o").
#   E2E_PORT            Proxy listen port (default: 14980).
#   E2E_LOG_LEVEL       llm-proxy log level: debug|info|warn|error (default: info).
#
# ISOLATION GUARANTEES
#   - LLM_PROXY_CONFIG      → dev/llm-proxy.hcl  (this repo, not ~/.config)
#   - LLM_PROXY_CONFIG_DIR  → $TMPDIR/llm-proxy-e2e-<pid>/config
#   - LLM_PROXY_CACHE_DIR   → $TMPDIR/llm-proxy-e2e-<pid>/cache
#   The plugin binary is sideloaded from bin/ into the temp cache dir.
#   No global config, no global plugin cache, no shared token files.
#
# PREREQUISITES
#   - llm-proxy must be on $PATH.
#   - task (Taskfile) or `go build` must be available to build the plugin.
#   - jq must be on $PATH (used by the smoke subcommand).

set -euo pipefail

# ---------------------------------------------------------------------------
# Resolve repo root (directory containing this script's parent).
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
PLUGIN_NAME="copilot"
PLUGIN_ORG="thelonelyghost"
PLUGIN_BINARY="llm-proxy-provider-copilot"
PLUGIN_MANIFEST="${PLUGIN_BINARY}.manifest.json"
PLUGIN_VERSION="$(jq -r .version "${REPO_ROOT}/manifest/${PLUGIN_MANIFEST}")"

DEV_CONFIG="${REPO_ROOT}/dev/llm-proxy.hcl"

E2E_PORT="${E2E_PORT:-14980}"
E2E_LOG_LEVEL="${E2E_LOG_LEVEL:-info}"
PROXY_BASE="http://127.0.0.1:${E2E_PORT}"

# Subcommand (default: serve)
SUBCOMMAND="${1:-serve}"

# ---------------------------------------------------------------------------
# Isolated temp dirs – scoped to this process so parallel runs don't collide.
# ---------------------------------------------------------------------------
E2E_TMP="${TMPDIR:-/tmp}/llm-proxy-e2e-$$"
E2E_CONFIG_DIR="${E2E_TMP}/config"
E2E_CACHE_DIR="${E2E_TMP}/cache"

export LLM_PROXY_CONFIG="${DEV_CONFIG}"
export LLM_PROXY_CONFIG_DIR="${E2E_CONFIG_DIR}"
export LLM_PROXY_CACHE_DIR="${E2E_CACHE_DIR}"

# The sidecar itself also reads LLM_PROXY_CONFIG_DIR for the GitHub token and
# LLM_PROXY_CACHE_DIR for the short-lived Copilot token cache.
# No additional env vars are needed; the sidecar inherits the process env.

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

log() { printf '\033[1;34m[e2e]\033[0m %s\n' "$*" >&2; }
err() { printf '\033[1;31m[e2e ERROR]\033[0m %s\n' "$*" >&2; }
die() { err "$*"; exit 1; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

cleanup() {
  local exit_code=$?
  # Kill the proxy if we started one in the background.
  if [[ -n "${PROXY_PID:-}" ]] && kill -0 "${PROXY_PID}" 2>/dev/null; then
    log "Stopping proxy (pid ${PROXY_PID})..."
    kill "${PROXY_PID}" 2>/dev/null || true
    wait "${PROXY_PID}" 2>/dev/null || true
  fi
  # Remove the temp tree created by this run.
  if [[ -d "${E2E_TMP}" ]]; then
    rm -rf "${E2E_TMP}"
    log "Removed temp dir ${E2E_TMP}"
  fi
  exit "${exit_code}"
}
trap cleanup EXIT INT TERM

# ---------------------------------------------------------------------------
# Step: build the plugin binary.
# ---------------------------------------------------------------------------
build_plugin() {
  log "Building plugin binary..."
  if command -v task >/dev/null 2>&1; then
    (cd "${REPO_ROOT}" && task build)
  else
    require_cmd go
    BINARY_VERSION="${PLUGIN_VERSION}" \
      go build \
        -trimpath \
        -ldflags="-s -w -X main.Version=${PLUGIN_VERSION}" \
        -o "${REPO_ROOT}/bin/${PLUGIN_BINARY}" \
        "${REPO_ROOT}/cmd/${PLUGIN_BINARY}"
  fi
  [[ -f "${REPO_ROOT}/bin/${PLUGIN_BINARY}" ]] \
    || die "Build succeeded but bin/${PLUGIN_BINARY} not found"
  log "Built bin/${PLUGIN_BINARY} (version ${PLUGIN_VERSION})"
}

# ---------------------------------------------------------------------------
# Step: sideload binary + manifest into the isolated plugin cache.
# ---------------------------------------------------------------------------
sideload_plugin() {
  local os_arch
  os_arch="$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"

  local plugin_dir="${E2E_CACHE_DIR}/plugins/${PLUGIN_ORG}/${PLUGIN_NAME}/${PLUGIN_VERSION}/${os_arch}"
  mkdir -p "${plugin_dir}"

  install -m 0755 \
    "${REPO_ROOT}/bin/${PLUGIN_BINARY}" \
    "${plugin_dir}/${PLUGIN_BINARY}"

  install -m 0644 \
    "${REPO_ROOT}/manifest/${PLUGIN_MANIFEST}" \
    "${plugin_dir}/${PLUGIN_MANIFEST}"

  log "Sideloaded plugin → ${plugin_dir}"
}

# ---------------------------------------------------------------------------
# Step: create isolated config and cache dirs.
# ---------------------------------------------------------------------------
init_dirs() {
  mkdir -p "${E2E_CONFIG_DIR}" "${E2E_CACHE_DIR}"
  log "Isolated config dir : ${E2E_CONFIG_DIR}"
  log "Isolated cache dir  : ${E2E_CACHE_DIR}"
  log "Using config file   : ${DEV_CONFIG}"
}

# ---------------------------------------------------------------------------
# Step: wait for the proxy to become healthy.
# ---------------------------------------------------------------------------
wait_for_proxy() {
  local url="${PROXY_BASE}/healthz"
  local max_wait=15
  local elapsed=0
  log "Waiting for proxy at ${url}..."
  while ! curl -sf "${url}" >/dev/null 2>&1; do
    sleep 0.5
    elapsed=$(( elapsed + 1 ))
    if (( elapsed > max_wait * 2 )); then
      die "Proxy did not become healthy after ${max_wait}s"
    fi
  done
  log "Proxy is healthy (${PROXY_BASE})"
}

# ---------------------------------------------------------------------------
# Step: start proxy in the background, capture pid.
# ---------------------------------------------------------------------------
start_proxy_background() {
  log "Starting llm-proxy serve (background)..."
  llm-proxy serve \
    --config "${DEV_CONFIG}" \
    --port   "${E2E_PORT}" \
    --log-level "${E2E_LOG_LEVEL}" \
    &
  PROXY_PID=$!
  log "Proxy pid: ${PROXY_PID}"
  wait_for_proxy
}

# ---------------------------------------------------------------------------
# Subcommand: serve (foreground)
# ---------------------------------------------------------------------------
cmd_serve() {
  require_cmd llm-proxy
  init_dirs
  build_plugin
  sideload_plugin

  log "Starting llm-proxy serve (foreground, Ctrl-C to stop)..."
  log "  Proxy address : ${PROXY_BASE}"
  log "  Config file   : ${DEV_CONFIG}"
  log "  Cache dir     : ${E2E_CACHE_DIR}"
  log ""

  # Run in the foreground.  Do NOT exec here: keeping the shell process alive
  # ensures the EXIT trap fires on Ctrl-C or any exit path, which removes
  # E2E_TMP regardless of how the proxy terminates.
  llm-proxy serve \
    --config    "${DEV_CONFIG}" \
    --port      "${E2E_PORT}" \
    --log-level "${E2E_LOG_LEVEL}"
}

# ---------------------------------------------------------------------------
# Subcommand: login (GitHub device-code OAuth flow)
# ---------------------------------------------------------------------------
cmd_login() {
  require_cmd llm-proxy
  init_dirs
  build_plugin
  sideload_plugin

  log "Running GitHub Copilot device-code login flow..."
  log "  Token will be written to: ${E2E_CONFIG_DIR}/github_token.json"
  log "  After login completes, run: scripts/e2e-test.sh serve"
  log ""

  llm-proxy plugin run --config "${DEV_CONFIG}" copilot login
}

# ---------------------------------------------------------------------------
# Subcommand: logout (remove stored credentials)
# ---------------------------------------------------------------------------
cmd_logout() {
  require_cmd llm-proxy
  init_dirs
  build_plugin
  sideload_plugin

  log "Removing stored GitHub Copilot credentials..."
  llm-proxy plugin run --config "${DEV_CONFIG}" copilot logout
  log "Credentials removed."
}

# ---------------------------------------------------------------------------
# Subcommand: models (quick connectivity check)
# ---------------------------------------------------------------------------
cmd_models() {
  require_cmd llm-proxy
  require_cmd curl
  init_dirs
  build_plugin
  sideload_plugin
  start_proxy_background

  log "GET ${PROXY_BASE}/v1/models"
  curl -sf "${PROXY_BASE}/v1/models" | (command -v jq >/dev/null 2>&1 && jq . || cat)
  echo ""
  log "models check passed"
}

# ---------------------------------------------------------------------------
# Subcommand: smoke (single chat-completions round-trip)
# ---------------------------------------------------------------------------
cmd_smoke() {
  require_cmd llm-proxy
  require_cmd curl
  require_cmd jq
  init_dirs
  build_plugin
  sideload_plugin
  start_proxy_background

  local model="${E2E_MODEL:-gpt-4o}"
  # The proxy routes by backend-prefix/model; the backend label in the config
  # is "copilot", so the routed model id is "copilot/<model>".
  local routed_model="copilot/${model}"

  local request
  request="$(jq -nc \
    --arg model "${routed_model}" \
    '{"model": $model, "messages": [{"role": "user", "content": "Reply with only the word PONG."}], "max_tokens": 16}')"

  log "POST ${PROXY_BASE}/v1/chat/completions"
  log "  model: ${routed_model}"
  log ""

  local response
  response="$(curl -sf \
    -X POST "${PROXY_BASE}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "${request}")"

  echo "${response}" | jq .

  # Verify the response has at least one choice.
  local n_choices
  n_choices="$(echo "${response}" | jq '.choices | length')"
  if (( n_choices < 1 )); then
    die "smoke test failed: response has no choices"
  fi

  log "smoke test passed (${n_choices} choice(s) returned)"
}

# ---------------------------------------------------------------------------
# Dispatch
# ---------------------------------------------------------------------------
case "${SUBCOMMAND}" in
  serve)   cmd_serve   ;;
  login)   cmd_login   ;;
  logout)  cmd_logout  ;;
  models)  cmd_models  ;;
  smoke)   cmd_smoke   ;;
  *)
    err "Unknown subcommand: ${SUBCOMMAND}"
    echo ""
    echo "Usage: $0 [serve|login|logout|models|smoke]"
    exit 1
    ;;
esac
