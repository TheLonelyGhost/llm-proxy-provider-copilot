// Package copilot implements the GitHub Copilot provider for llm-proxy.
//
// Authentication is a two-step process:
//
//  1. The proxy obtains a long-lived GitHub OAuth token via the device-code
//     flow (interactive, one-time) and stores it under the user's config
//     directory with mode 0600.
//
//  2. On each API call the proxy exchanges that GitHub token for a
//     short-lived Copilot API token, which it caches under the user's cache
//     directory and reuses until shortly before its expiry.
//
// This package is the shared core used by both the HTTP sidecar (cmd/) and
// the tooling subcommands (login, logout, list-models).
package copilot
