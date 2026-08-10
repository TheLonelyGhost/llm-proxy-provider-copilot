#!/usr/bin/env bash
# scripts/e2e-test.sh – Run llm-proxy with the copilot plugin under development,
# fully isolated from any global llm-proxy configuration and token cache.
#
# USAGE
#   scripts/e2e-test.sh [serve|login|logout|models|smoke|budget]
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
#   budget   Invoke the budget tool call directly (no proxy required) and
#            print the JSON quota/spend response.  Requires real credentials
#            (run `login` first).  Uses the stored GitHub OAuth token to call
#            GET /copilot_internal/user and maps the result to the llm-proxy
#            /v1/usage/budget JSON shape.
#
# ENVIRONMENT VARIABLES
#   Set these before invoking the script (or export them in your shell):
#
#   Optional:
#   E2E_CREDS_DIR       Persistent directory that holds login credentials
#                       (github_token.json and copilot_token.json) across
#                       script invocations.  When set, login writes tokens
#                       here and all other subcommands read from here instead
#                       of the ephemeral per-run temp dir.  The directory is
#                       NOT removed on exit, so a single `login` run is
#                       enough for the whole session.
#                       Default: unset (credentials live only for the
#                       lifetime of the current invocation).
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
#   - LLM_PROXY_CONFIG_DIR  → $E2E_CREDS_DIR/config  (when E2E_CREDS_DIR is set)
#                             $TMPDIR/llm-proxy-e2e-<pid>/config  (otherwise)
#   - LLM_PROXY_CACHE_DIR   → $E2E_CREDS_DIR/cache   (when E2E_CREDS_DIR is set)
#                             $TMPDIR/llm-proxy-e2e-<pid>/cache   (otherwise)
#   The plugin binary is sideloaded from bin/ into LLM_PROXY_CACHE_DIR/plugins/
#   on every run so a freshly built binary is always used.
#   No global config and no global plugin cache are used.
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

# Model used for smoke test.
E2E_MODEL="${E2E_MODEL:-gpt-4o}"

# ---------------------------------------------------------------------------
# Directories
#
# E2E_TMP    – ephemeral, PID-scoped scratch space.  Deleted on exit.
#
# Credential dirs – where github_token.json / copilot_token.json live,
# and where the sideloaded plugin binary is installed:
#   * When E2E_CREDS_DIR is set: use $E2E_CREDS_DIR/{config,cache}.  These
#     are NOT deleted on exit, so credentials and the cached binary persist
#     across invocations.
#   * Otherwise: use subdirs of E2E_TMP (deleted on exit; credentials and
#     binary are ephemeral, fine for a single uninterrupted serve/smoke run).
# ---------------------------------------------------------------------------
E2E_TMP="${TMPDIR:-/tmp}/llm-proxy-e2e-$$"

if [[ -n "${E2E_CREDS_DIR:-}" ]]; then
  E2E_CONFIG_DIR="${E2E_CREDS_DIR}/config"
  E2E_CACHE_DIR="${E2E_CREDS_DIR}/cache"
else
  E2E_CONFIG_DIR="${E2E_TMP}/config"
  E2E_CACHE_DIR="${E2E_TMP}/cache"
fi

export LLM_PROXY_CONFIG="${DEV_CONFIG}"
export LLM_PROXY_CONFIG_DIR="${E2E_CONFIG_DIR}"
export LLM_PROXY_CACHE_DIR="${E2E_CACHE_DIR}"

# The sidecar inherits these env vars so it reads/writes tokens in the same
# directories as the script.  No additional wiring is required.

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
  # Remove the ephemeral temp tree for this run.  Never remove E2E_CREDS_DIR:
  # it is operator-managed and intended to persist across invocations.
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
#
# When E2E_CREDS_DIR is set, the plugin binary is written into
# E2E_CACHE_DIR (which lives under E2E_CREDS_DIR) so that llm-proxy finds
# it there across invocations.  When E2E_CREDS_DIR is not set, E2E_CACHE_DIR
# is already under the ephemeral E2E_TMP, so behaviour is unchanged.
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
# Step: create config and cache dirs.
# ---------------------------------------------------------------------------
init_dirs() {
  mkdir -p "${E2E_CONFIG_DIR}" "${E2E_CACHE_DIR}"
  if [[ -n "${E2E_CREDS_DIR:-}" ]]; then
    log "Persistent creds dir: ${E2E_CREDS_DIR}"
  fi
  log "Config dir (tokens) : ${E2E_CONFIG_DIR}"
  log "Cache dir (tokens)  : ${E2E_CACHE_DIR}"
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
  # ensures the EXIT trap fires on Ctrl-C or any exit path.
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
  if [[ -n "${E2E_CREDS_DIR:-}" ]]; then
    log "  Credentials will persist across invocations (E2E_CREDS_DIR is set)."
    log "  After login completes, re-export E2E_CREDS_DIR and run any subcommand."
  else
    log "  WARNING: E2E_CREDS_DIR is not set.  Token is ephemeral and will be"
    log "  deleted when this script exits.  Export E2E_CREDS_DIR to a persistent"
    log "  directory before running login if you want credentials to survive:"
    log "    export E2E_CREDS_DIR=~/.local/share/llm-proxy-e2e"
    log "    $0 login"
    log "    $0 budget   # works in a separate shell if E2E_CREDS_DIR is exported"
  fi
  log ""

  llm-proxy plugin run --config "${DEV_CONFIG}" copilot login
  log "Login complete.  Token written to: ${E2E_CONFIG_DIR}/github_token.json"
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
# Subcommand: budget (quota / spend report via tool call)
# ---------------------------------------------------------------------------
cmd_budget() {
  require_cmd llm-proxy
  require_cmd jq
  init_dirs
  build_plugin
  sideload_plugin

  log "Running budget tool call (no proxy required)..."
  log "  Config dir: ${E2E_CONFIG_DIR}"
  log ""

  local output
  output="$(llm-proxy plugin run --config "${DEV_CONFIG}" copilot budget)"

  echo "${output}" | jq .

  # Basic sanity: the JSON must have an "object" key.
  local obj
  obj="$(echo "${output}" | jq -r '.object // empty')"
  if [[ "${obj}" != "usage.budget" ]]; then
    die "budget tool call returned unexpected object: ${obj:-<empty>}"
  fi

  log "budget check passed"
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
  budget)  cmd_budget  ;;
  *)
    err "Unknown subcommand: ${SUBCOMMAND}"
    echo ""
    echo "Usage: $0 [serve|login|logout|models|smoke|budget]"
    exit 1
    ;;
esac
