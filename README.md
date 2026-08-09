# llm-proxy-provider-copilot

An [llm-proxy](https://github.com/thelonelyghost/llm-proxy) plugin that adds
GitHub Copilot as a backend. It runs as an out-of-process HTTP sidecar managed
by the proxy, exposing the full OpenAI-compatible surface:

- `POST /v1/chat/completions`
- `POST /v1/completions` (returns 501 — not supported upstream)
- `POST /v1/embeddings`
- `POST /v1/responses` (translated through chat-completions)
- `GET /v1/models`

It also ships two tooling subcommands invoked via `llm-proxy plugin run`:

| Tool     | Purpose                                           |
|----------|---------------------------------------------------|
| `login`  | Run the GitHub device-code OAuth flow             |
| `logout` | Remove any stored credentials                     |

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
- [Headers sent upstream](#headers-sent-upstream)
- [Responses API shim](#responses-api-shim)
- [Environment variables](#environment-variables)
- [End-to-end testing](#end-to-end-testing)
  - [Prerequisites](#e2e-prerequisites)
  - [Quick start](#e2e-quick-start)
  - [Subcommands](#e2e-subcommands)
  - [Isolation](#e2e-isolation)
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

| Variable                     | Description                               | Default                          |
|------------------------------|-------------------------------------------|----------------------------------|
| `LLM_PROXY_PLUGIN_PORT`      | TCP port the sidecar binds on             | `9001`                           |
| `COPILOT_NAME`               | Backend routing prefix                    | `copilot`                        |
| `COPILOT_API_BASE`           | Upstream Copilot API base URL             | `https://api.githubcopilot.com`  |
| `COPILOT_GITHUB_API_BASE`    | GitHub API base URL                       | `https://api.github.com`         |
| `COPILOT_GITHUB_LOGIN_BASE`  | GitHub login base URL                     | `https://github.com`             |
| `COPILOT_OAUTH_CLIENT_ID`    | OAuth app client ID                       | `Iv1.b507a08c87ecfe98`           |
| `COPILOT_EDITOR_VERSION`     | `Editor-Version` header                   | `vscode/1.95.0`                  |
| `COPILOT_EDITOR_PLUGIN_VERSION` | `Editor-Plugin-Version` header         | `copilot-chat/0.22.0`            |
| `COPILOT_USER_AGENT`         | `User-Agent` header                       | `GitHubCopilotChat/0.22.0`       |
| `COPILOT_INTEGRATION_ID`     | `Copilot-Integration-Id` header           | `vscode-chat`                    |
| `COPILOT_REQUEST_TIMEOUT`    | Per-request timeout (e.g. `"60s"`)        | `60s`                            |
| `COPILOT_MODELS`             | Comma-separated allow-list of model ids   | _(all allowed)_                  |
| `LLM_PROXY_CACHE_DIR`        | Cache directory override                  | OS default                       |
| `LLM_PROXY_CONFIG_DIR`       | Config directory override                 | OS default                       |

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

```sh
# 1. Authenticate once (opens the GitHub device-code URL in your terminal).
scripts/e2e-test.sh login

# 2. Start the proxy (Ctrl-C to stop).
scripts/e2e-test.sh serve

# Or run a full smoke test (no foreground proxy needed):
scripts/e2e-test.sh smoke
```

#### <a id="e2e-subcommands"></a>Subcommands

| Subcommand | Description |
|------------|-------------|
| `serve` (default) | Build, sideload, then start `llm-proxy` in the foreground. |
| `login`  | Run the GitHub device-code OAuth flow; writes `github_token.json` into the isolated config dir. |
| `logout` | Remove any stored credentials from the isolated dirs. |
| `models` | Start the proxy in the background, call `GET /v1/models`, then stop it. |
| `smoke`  | Start the proxy in the background, send one `POST /v1/chat/completions` request, then stop it. |

Key environment variables for the script:

| Variable | Default | Description |
|----------|---------|-------------|
| `E2E_PORT` | `14980` | Proxy listen port. |
| `E2E_LOG_LEVEL` | `info` | llm-proxy log level. |
| `E2E_MODEL` | `gpt-4o` | Model id used by the smoke subcommand. |
| `COPILOT_MODELS` | — | Comma-separated allow-list forwarded to the sidecar. |

#### <a id="e2e-isolation"></a>Isolation

Every run creates a per-process temp tree that is deleted on exit:

| Variable | Value |
|---|---|
| `LLM_PROXY_CONFIG` | `dev/llm-proxy.hcl` |
| `LLM_PROXY_CONFIG_DIR` | `$TMPDIR/llm-proxy-e2e-<pid>/config` |
| `LLM_PROXY_CACHE_DIR` | `$TMPDIR/llm-proxy-e2e-<pid>/cache` |

The plugin binary is sideloaded from `bin/` into the temp cache dir. No
global config or token files are read or written.

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
