# llm-proxy configuration for isolated end-to-end testing of the copilot plugin.
#
# This file is used for local development and e2e testing. It references no
# global paths; secrets come from environment variables.
#
# Before using, run:
#   llm-proxy plugin run copilot login
#
# Then start the proxy:
#   llm-proxy serve --config dev/llm-proxy.hcl

server {
  address = "127.0.0.1"
  port    = 14980

  # Debug body logging. Keep false unless actively diagnosing wire-level issues.
  log_bodies = false
}

plugin "copilot" {
  source  = "github.com/thelonelyghost/llm-proxy-provider-copilot"
  version = "0.1.0"
}

backend "copilot" {
  type = "github-copilot"

  # Optional allow-list. Remove to query the upstream Copilot /models catalogue.
  models = ["gpt-4o", "claude-3.5-sonnet"]

  request_timeout = "60s"
}
