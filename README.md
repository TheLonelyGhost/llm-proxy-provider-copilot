# llm-proxy-provider-copilot

An [llm-proxy](https://github.com/thelonelyghost/llm-proxy) plugin that adds
GitHub Copilot as a backend. It runs as an out-of-process HTTP sidecar managed
by the proxy, exposing the full OpenAI-compatible surface:

- `POST /v1/chat/completions`
- `POST /v1/completions` (returns 501 — not supported upstream)
- `POST /v1/embeddings`
- `POST /v1/responses` (translated through chat-completions)
- `GET /v1/models`

It also ships tooling subcommands invoked via `llm-proxy plugin run`:

| Tool     | Purpose                                           |
|----------|---------------------------------------------------|
| `login`  | Run the GitHub device-code OAuth flow             |
| `logout` | Remove any stored credentials                     |
| `budget` | Report quota and spend as JSON                    |

> **You must have an active GitHub Copilot subscription** on the GitHub account
> you authorise with. This project is not affiliated with GitHub or Microsoft.
> Use at your own risk.

---

## Contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
  - [All HCL options](#all-hcl-options)
- [Login](#login)
- [Available models](#available-models)
- [Budget](#budget)
- [Headers sent upstream](#headers-sent-upstream)
- [Responses API shim](#responses-api-shim)
- [Environment variables](#environment-variables)
- [End-to-end testing](#end-to-end-testing)
  - [Prerequisites](#e2e-prerequisites)
  - [Quick start](#e2e-quick-start)
  - [Subcommands](#e2e-subcommands)
  - [Isolation and credential persistence](#e2e-isolation)
- [Building and releasing](#building-and-releasing)

---

## Prerequisites

- [llm-proxy](https://github.com/thelonelyghost/llm-proxy) installed and on `$PATH`.
- An active GitHub Copilot subscription (Individual, Pro, Pro+, Business, or Enterprise).

---

## Installation

### Managed install via llm-proxy (recommended)

Add a `plugin` block to your `llm-proxy` HCL config:

```hcl
plugin "copilot" {
  source  = "github.com/thelonelyghost/llm-proxy-provider-copilot"
  version = "~> 0.1"
}
```

Then install it:

```sh
llm-proxy plugin install
```

### Build from source

```sh
git clone https://github.com/thelonelyghost/llm-proxy-provider-copilot
cd llm-proxy-provider-copilot
task build
```

The binary is written to `bin/llm-proxy-provider-copilot`. Copy it and
`manifest/llm-proxy-provider-copilot.manifest.json` into the plugin cache, or
use a `file://` source in the plugin block.

Run tests:

```sh
task test
```

---

## Configuration

### Minimal example

```hcl
plugin "copilot" {
  source  = "github.com/thelonelyghost/llm-proxy-provider-copilot"
  version = "~> 0.1"
}

backend "copilot" {
  type   = "github-copilot"
  models = ["gpt-4o", "claude-3.5-sonnet"]
}
```

Run login once, then start the proxy:

```sh
llm-proxy plugin run copilot login
llm-proxy serve --config config.hcl
```

### All HCL options

```hcl
backend "copilot" {
  type = "github-copilot"

  # Optional allow-list. If empty, the proxy queries Copilot's /models endpoint.
  models = ["gpt-4o", "claude-3.5-sonnet"]

  # Upstream endpoint overrides (defaults match the official VS Code extension).
  api_base              = "https://api.githubcopilot.com"
  github_api_base       = "https://api.github.com"
  github_login_base     = "https://github.com"
  oauth_client_id       = "Iv1.b507a08c87ecfe98"   # public VS Code Copilot client id
  editor_version        = "vscode/1.95.0"
  editor_plugin_version = "copilot-chat/0.22.0"
  user_agent            = "GitHubCopilotChat/0.22.0"
  integration_id        = "vscode-chat"
  request_timeout       = "60s"
}
```

---

## Login

Copilot requires a GitHub OAuth token obtained via the device-code flow:

```sh
# Basic: prints the URL and code, waits for browser authorisation.
llm-proxy plugin run copilot login

# Open the URL automatically in the default browser.
llm-proxy plugin run copilot login -- --browser
```

The resulting token is written to
`<user-config-dir>/llm-proxy/github_token.json` with mode 0600. A short-lived
Copilot API token is cached at `<user-cache-dir>/llm-proxy/copilot_token.json`
and refreshed transparently before expiry.

To remove credentials:

```sh
llm-proxy plugin run copilot logout
```

---

## <a id="available-models"></a>Available models

To discover which model IDs are available to your Copilot credential — without
starting the proxy:

```sh
llm-proxy plugin run copilot list-models
```

Output is one model ID per line, unfiltered by any backend allow-list:

```
claude-3.5-sonnet
gpt-4o
gpt-4o-mini
o1
o3-mini
```

This is useful for building or verifying a `models` allow-list in the backend
block, or for confirming that credentials are working before starting the proxy.

---

## Budget

The `budget` tool call fetches quota and spend data directly from GitHub's
`/copilot_internal/user` endpoint using the stored GitHub OAuth token. No
running proxy is required.

```sh
llm-proxy plugin run copilot budget
```

Output is a single JSON object on both success and failure. On success:

```json
{
  "object": "usage.budget",
  "currency": "premium_requests",
  "max_budget": 300,
  "spend": 42,
  "remaining": 258,
  "unlimited": false,
  "extras": {
    "copilot_plan": "pro_plus",
    "login": "octocat",
    "quota_reset_date": "2026-09-01",
    "primary_source": "premium_interactions",
    "snapshot_chat_entitlement": "1000",
    "snapshot_chat_remaining": "800"
  }
}
```

On failure (e.g. no active subscription, network error, missing credentials):

```json
{
  "object": "error",
  "error": "no Copilot quota available for octocat (plan=\"individual\", access_type_sku=\"no_access\"): the account does not have an active Copilot subscription"
}
```

The exit code is non-zero on failure. Stdout is always valid JSON so the output can be piped safely to `jq` regardless of outcome.

```json
{
  "object": "usage.budget",
  "currency": "premium_requests",
  "max_budget": 300,
  "spend": 42,
  "remaining": 258,
  "unlimited": false,
  "extras": {
    "copilot_plan": "pro_plus",
    "login": "octocat",
    "quota_reset_date": "2026-09-01",
    "primary_source": "premium_interactions",
    "snapshot_chat_entitlement": "1000",
    "snapshot_chat_remaining": "800"
  }
}
```

| Field        | Description |
|--------------|-------------|
| `object`     | Always `"usage.budget"` |
| `currency`   | `"premium_requests"` (Pro / Pro+ / Business / Enterprise) or `"interactions"` (older / Free plans) |
| `max_budget` | Monthly entitlement ceiling (`0` when `unlimited` is `true`) |
| `spend`      | Entitlement minus remaining, clamped at zero |
| `remaining`  | Remaining quota this period |
| `unlimited`  | `true` when the plan has no ceiling |
| `extras`     | Additional plan metadata: plan name, login, reset date, per-category snapshots |

The mapping precedence for the primary counter follows the upstream provider:
1. `quota_snapshots.premium_interactions` — currency `premium_requests`
2. `quota_snapshots.chat` — currency `interactions`
3. `limited_user_quotas` — currency `interactions`, `max_budget` is `0` (ceiling unknown)

All raw snapshot categories are also included in `extras` with the prefix
`snapshot_<category>_`.

---

## Headers sent upstream

For each API call the proxy sets:

| Header                   | Value                          |
| ------------------------ | ------------------------------ |
| `Authorization`          | `Bearer <copilot-token>`       |
| `Editor-Version`         | `editor_version` setting       |
| `Editor-Plugin-Version`  | `editor_plugin_version`        |
| `Copilot-Integration-Id` | `integration_id` setting       |
| `User-Agent`             | `user_agent` setting           |
| `Content-Type`           | `application/json`             |
| `Openai-Intent`          | `conversation-panel`           |

The client's `Authorization` header is **not** forwarded.

---

## Responses API shim

GitHub Copilot's upstream does not expose `/v1/responses`. The provider
implements the endpoint as an in-proxy translation shim over
`/chat/completions`, so OpenAI Responses-API clients work against
`copilot/<model>` without code changes.

**Supported subset:**

- `input` as a string or an array of input items.
- `instructions` — prepended as a `system` message.
- `tools`, `tool_choice`, `temperature`, `top_p`, `max_output_tokens`
  (→ `max_tokens`), `metadata`, `parallel_tool_calls`, `user`.
- `stream: true`.
- `reasoning.effort` — forwarded verbatim.

---

## Environment variables

### Set by llm-proxy (tooling mode)

Before exec-ing the binary as a tool, llm-proxy sets these variables so the
binary knows which backend instance it is operating for:

| Variable                  | Description                                          |
|---------------------------|------------------------------------------------------|
| `LLM_PROXY_BACKEND_LABEL` | The `backend` block label (e.g. `copilot`)           |
| `LLM_PROXY_BACKEND_TYPE`  | The plugin type name (e.g. `github-copilot`)         |
| `LLM_PROXY_BACKEND_CONFIG`| JSON object with all backend attributes; `env()` references already evaluated |

`LLM_PROXY_BACKEND_CONFIG` is the primary source of configuration in tooling
mode. `COPILOT_*` variables override individual fields if set.

### Set by the operator

| Variable                        | Description                               | Default                          |
|---------------------------------|-------------------------------------------|----------------------------------|
| `LLM_PROXY_PLUGIN_PORT`         | TCP port the sidecar binds on             | `9001`                           |
| `COPILOT_NAME`                  | Backend routing prefix                    | from `LLM_PROXY_BACKEND_LABEL` or `copilot` |
| `COPILOT_API_BASE`              | Upstream Copilot API base URL             | `https://api.githubcopilot.com`  |
| `COPILOT_GITHUB_API_BASE`       | GitHub API base URL                       | `https://api.github.com`         |
| `COPILOT_GITHUB_LOGIN_BASE`     | GitHub login base URL                     | `https://github.com`             |
| `COPILOT_OAUTH_CLIENT_ID`       | OAuth app client ID                       | `Iv1.b507a08c87ecfe98`           |
| `COPILOT_EDITOR_VERSION`        | `Editor-Version` header                   | `vscode/1.95.0`                  |
| `COPILOT_EDITOR_PLUGIN_VERSION` | `Editor-Plugin-Version` header            | `copilot-chat/0.22.0`            |
| `COPILOT_USER_AGENT`            | `User-Agent` header                       | `GitHubCopilotChat/0.22.0`       |
| `COPILOT_INTEGRATION_ID`        | `Copilot-Integration-Id` header           | `vscode-chat`                    |
| `COPILOT_REQUEST_TIMEOUT`       | Per-request timeout (e.g. `"60s"`)        | `60s`                            |
| `COPILOT_MODELS`                | Comma-separated allow-list of model ids   | _(all allowed)_                  |
| `LLM_PROXY_CACHE_DIR`           | Cache directory override                  | OS default                       |
| `LLM_PROXY_CONFIG_DIR`          | Config directory override                 | OS default                       |

---

## End-to-end testing

`scripts/e2e-test.sh` drives a full round-trip against the real GitHub Copilot
API. It builds the plugin from source, sideloads it into a fully isolated
temp directory tree, and runs `llm-proxy` against `dev/llm-proxy.hcl` — all
without touching global config, plugin cache, or token files.

#### <a id="e2e-prerequisites"></a>Prerequisites

- `llm-proxy` on `$PATH`.
- `jq` on `$PATH` (smoke subcommand only).
- `task` **or** `go` available to build the plugin.
- A valid GitHub account with an active Copilot subscription.

#### <a id="e2e-quick-start"></a>Quick start

Credentials are ephemeral by default (deleted when the script exits). To
persist them across separate invocations, export `E2E_CREDS_DIR` first:

```sh
export E2E_CREDS_DIR=~/.local/share/llm-proxy-e2e
```

Then authenticate and run any subcommand:

```sh
# Authenticate once (prints a GitHub device-code URL).
scripts/e2e-test.sh login

# Start the proxy in the foreground (Ctrl-C to stop).
scripts/e2e-test.sh serve

# Or run individual checks (each works in its own shell):
scripts/e2e-test.sh budget
scripts/e2e-test.sh smoke
```

#### <a id="e2e-subcommands"></a>Subcommands

| Subcommand | Description |
|------------|-------------|
| `serve` (default) | Build, sideload, then start `llm-proxy` in the foreground. |
| `login`  | Run the GitHub device-code OAuth flow; writes `github_token.json` into the credential dir. |
| `logout` | Remove any stored credentials. |
| `models` | Start the proxy in the background, call `GET /v1/models`, then stop it. |
| `smoke`  | Start the proxy in the background, send one `POST /v1/chat/completions` request, then stop it. |
| `budget` | Invoke the budget tool call (no proxy needed), print the JSON quota/spend response. |

Key environment variables for the script:

| Variable | Default | Description |
|----------|---------|-------------|
| `E2E_CREDS_DIR` | — | Persistent directory for credentials (see below). |
| `E2E_PORT` | `14980` | Proxy listen port. |
| `E2E_LOG_LEVEL` | `info` | llm-proxy log level. |
| `E2E_MODEL` | `gpt-4o` | Model id used by the smoke subcommand. |
| `COPILOT_MODELS` | — | Comma-separated allow-list forwarded to the sidecar. |

#### <a id="e2e-isolation"></a>Isolation and credential persistence

The script never touches your global `llm-proxy` config or token files.
`LLM_PROXY_CONFIG` always points at `dev/llm-proxy.hcl` in this repo.

Where credentials and the plugin binary live depends on whether
`E2E_CREDS_DIR` is set:

| | `E2E_CREDS_DIR` unset | `E2E_CREDS_DIR` set |
|---|---|---|
| `LLM_PROXY_CONFIG_DIR` | `$TMPDIR/llm-proxy-e2e-<pid>/config` | `$E2E_CREDS_DIR/config` |
| `LLM_PROXY_CACHE_DIR` | `$TMPDIR/llm-proxy-e2e-<pid>/cache` | `$E2E_CREDS_DIR/cache` |
| Deleted on exit? | **Yes** | **No** |

The plugin binary is sideloaded from `bin/` into `LLM_PROXY_CACHE_DIR/plugins/`
on every run so a freshly built binary is always used.

**Without `E2E_CREDS_DIR`** every subcommand is self-contained: tokens are
written and deleted within a single invocation, which is fine if you run
`serve` or `smoke` in one uninterrupted session.

**With `E2E_CREDS_DIR`** a single `login` run is enough for the whole
session — subsequent subcommands in separate shells share the same tokens:

```sh
export E2E_CREDS_DIR=~/.local/share/llm-proxy-e2e

# Authenticate once.
scripts/e2e-test.sh login

# Any later invocation (separate shell, any time) reuses those credentials.
scripts/e2e-test.sh budget
scripts/e2e-test.sh smoke
scripts/e2e-test.sh models

# When you're done:
scripts/e2e-test.sh logout
```

---

## Building and releasing

Archives for all supported platforms are expected at:

```text
https://github.com/thelonelyghost/llm-proxy-provider-copilot/releases/download/v<version>/
  llm-proxy-provider-copilot_<version>_<os>_<arch>.tar.gz
```

Each archive must contain:

```text
llm-proxy-provider-copilot              ← the sidecar binary
llm-proxy-provider-copilot.manifest.json
```

To tag and build:

```sh
task release
```

---

## Disclaimer

This plugin talks to undocumented GitHub Copilot endpoints used by official
editor extensions. Those endpoints may change at any time. The defaults here
match publicly known values used by VS Code at time of writing, but are not
officially supported by GitHub or Microsoft. Use at your own risk.
