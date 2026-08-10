package tool

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/auth"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
)

// budgetOutput is the JSON shape written to stdout by the budget tool call.
// Field names are stable snake_case to match the upstream /v1/usage/budget
// surface documented in llm-proxy.
type budgetOutput struct {
	Object    string            `json:"object"`
	Currency  string            `json:"currency,omitempty"`
	MaxBudget float64           `json:"max_budget"`
	Spend     float64           `json:"spend"`
	Remaining float64           `json:"remaining"`
	Unlimited bool              `json:"unlimited,omitempty"`
	Extras    map[string]string `json:"extras,omitempty"`
}

// Currency tag constants matching the upstream provider mapping.
const (
	currencyPremiumRequests = "premium_requests"
	currencyInteractions    = "interactions"
)

// Snapshot key names from GitHub's /copilot_internal/user response.
const (
	snapshotPremiumInteractions = "premium_interactions"
	snapshotChat                = "chat"
)

// budgetError is the JSON shape written to stdout when the budget command
// cannot produce a budget result. Using a structured error object lets
// machine consumers (jq, scripts, opencode) parse failures the same way
// they parse successes instead of receiving empty or non-JSON output.
type budgetError struct {
	Object string `json:"object"`
	Error  string `json:"error"`
}

// writeBudgetError encodes a budgetError to out and returns the original
// error unchanged so the caller can still return it to cobra (exit code).
func writeBudgetError(out io.Writer, cause error) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(budgetError{ //nolint:errcheck // best-effort; original error is returned
		Object: "usage.budget",
		Error:  cause.Error(),
	})
	return cause
}

func newBudgetCmd(out io.Writer, cfg *server.Config) *cobra.Command {
	var (
		gitHubAPIBase string
	)
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Report GitHub Copilot quota / spend information as JSON",
		Long: "Fetches quota snapshot data from GitHub's /copilot_internal/user\n" +
			"endpoint using the stored GitHub OAuth token and prints a JSON\n" +
			"object to stdout.\n\n" +
			"The output format mirrors the llm-proxy /v1/usage/budget response:\n" +
			"  object        always \"usage.budget\"\n" +
			"  currency      \"premium_requests\" or \"interactions\"\n" +
			"  max_budget    entitlement ceiling (0 when unlimited)\n" +
			"  spend         entitlement minus remaining (clamped at 0)\n" +
			"  remaining     remaining quota\n" +
			"  unlimited     true when the plan has no ceiling\n" +
			"  extras        additional plan metadata as string key-value pairs\n\n" +
			"Exit codes:\n" +
			"  0  budget data written to stdout\n" +
			"  1  authentication, network, or mapping error",
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolvedCfg := server.Config{}
			if cfg != nil {
				resolvedCfg = *cfg
			}
			if gitHubAPIBase != "" {
				resolvedCfg.GitHubAPIBase = gitHubAPIBase
			}

			authenticator, err := server.NewAuthenticatorFromConfig(resolvedCfg, nil)
			if err != nil {
				return writeBudgetError(out, fmt.Errorf("init authenticator: %w", err))
			}

			info, err := authenticator.FetchUserInfo(cmd.Context())
			if err != nil {
				return writeBudgetError(out, fmt.Errorf("fetch budget: %w", err))
			}

			result, ok := mapBudget(info)
			if !ok {
				return writeBudgetError(out, noQuotaError(info))
			}

			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		},
	}
	cmd.Flags().StringVar(&gitHubAPIBase, "github-api-base", "", "GitHub API base URL")
	return cmd
}

// noQuotaError returns an error that explains why no quota snapshot was found,
// surfacing the identity and plan fields already decoded from the upstream
// response so the operator can distinguish a missing subscription from a
// code or parsing bug.
func noQuotaError(info *auth.CopilotUserInfo) error {
	login := info.Login
	if login == "" {
		login = "(unknown)"
	}
	plan := info.CopilotPlan
	sku := info.AccessTypeSKU
	switch {
	case sku == "no_access" || plan == "" || plan == "individual" && sku == "no_access":
		return fmt.Errorf(
			"no Copilot quota available for %s (plan=%q, access_type_sku=%q): "+
				"the account does not have an active Copilot subscription",
			login, plan, sku,
		)
	case plan != "" && sku != "" && sku != "no_access":
		return fmt.Errorf(
			"no quota snapshot returned for %s (plan=%q, access_type_sku=%q): "+
				"the account appears active but GitHub returned no quota data",
			login, plan, sku,
		)
	default:
		return fmt.Errorf(
			"no quota snapshot returned for %s (plan=%q, access_type_sku=%q)",
			login, plan, sku,
		)
	}
}

// mapBudget translates a CopilotUserInfo to budgetOutput using the same
// precedence rules as the upstream llm-proxy copilot budget mapping:
//
//  1. quota_snapshots.premium_interactions  → currency: "premium_requests"
//  2. quota_snapshots.chat                  → currency: "interactions"
//  3. limited_user_quotas                   → currency: "interactions", max_budget=0
//
// Returns ok=false when no usable quota signal is present.
func mapBudget(info *auth.CopilotUserInfo) (budgetOutput, bool) {
	if info == nil {
		return budgetOutput{}, false
	}

	result := budgetOutput{
		Object: "usage.budget",
		Extras: map[string]string{},
	}

	switch {
	case hasSnapshot(info.QuotaSnapshots, snapshotPremiumInteractions):
		applySnapshot(&result, info.QuotaSnapshots[snapshotPremiumInteractions], currencyPremiumRequests, snapshotPremiumInteractions)
	case hasSnapshot(info.QuotaSnapshots, snapshotChat):
		applySnapshot(&result, info.QuotaSnapshots[snapshotChat], currencyInteractions, snapshotChat)
	case len(info.LimitedUserQuotas) > 0:
		result.Currency = currencyInteractions
		key := pickLimitedUserPrimary(info.LimitedUserQuotas)
		result.Remaining = info.LimitedUserQuotas[key]
		result.Extras["limited_user_primary"] = key
	default:
		return budgetOutput{}, false
	}

	// Expose every snapshot line for dashboards and tooling.
	for _, key := range sortedKeys(info.QuotaSnapshots) {
		s := info.QuotaSnapshots[key]
		prefix := "snapshot_" + key
		result.Extras[prefix+"_entitlement"] = formatFloat(s.Entitlement)
		result.Extras[prefix+"_remaining"] = formatFloat(s.Remaining)
		if s.PercentRemaining != 0 {
			result.Extras[prefix+"_percent_remaining"] = formatFloat(s.PercentRemaining)
		}
		if s.Unlimited {
			result.Extras[prefix+"_unlimited"] = "true"
		}
		if s.OverageCount != 0 {
			result.Extras[prefix+"_overage_count"] = formatFloat(s.OverageCount)
		}
		if s.OveragePermitted {
			result.Extras[prefix+"_overage_permitted"] = "true"
		}
	}
	for _, key := range sortedKeys(info.LimitedUserQuotas) {
		result.Extras["limited_user_"+key] = formatFloat(info.LimitedUserQuotas[key])
	}
	if info.QuotaResetDate != "" {
		result.Extras["quota_reset_date"] = info.QuotaResetDate
	}
	if info.QuotaResetDateUTC != "" {
		result.Extras["quota_reset_date_utc"] = info.QuotaResetDateUTC
	}
	if info.LimitedUserResetDate != "" {
		result.Extras["limited_user_reset_date"] = info.LimitedUserResetDate
	}
	if info.ChatEnabled {
		result.Extras["chat_enabled"] = "true"
	}
	if info.CopilotPlan != "" {
		result.Extras["copilot_plan"] = info.CopilotPlan
	}
	if info.AccessTypeSKU != "" {
		result.Extras["access_type_sku"] = info.AccessTypeSKU
	}
	if info.Login != "" {
		result.Extras["login"] = info.Login
	}
	if len(info.OrganizationList) > 0 {
		result.Extras["organization_login"] = info.OrganizationList[0].Login
		if info.OrganizationList[0].Name != "" {
			result.Extras["organization_name"] = info.OrganizationList[0].Name
		}
	}

	if len(result.Extras) == 0 {
		result.Extras = nil
	}
	return result, true
}

// hasSnapshot reports whether map m contains key.
func hasSnapshot(m map[string]auth.CopilotQuotaSnapshot, key string) bool {
	if m == nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// applySnapshot writes primary fields onto result from one snapshot entry.
//
// Semantics:
//   - Unlimited: MaxBudget=0, Remaining=0, Unlimited=true
//   - Otherwise: MaxBudget=entitlement, Remaining=remaining, Spend=entitlement-remaining (≥0)
func applySnapshot(result *budgetOutput, s auth.CopilotQuotaSnapshot, currency, source string) {
	result.Currency = currency
	result.Extras["primary_source"] = source
	if s.Unlimited {
		result.Unlimited = true
		return
	}
	result.MaxBudget = s.Entitlement
	result.Remaining = s.Remaining
	result.Spend = s.Entitlement - s.Remaining
	if result.Spend < 0 {
		result.Spend = 0
	}
}

// pickLimitedUserPrimary returns "chat" when present, otherwise the
// lexicographically smallest key. Callers must only call this when m is
// non-empty.
func pickLimitedUserPrimary(m map[string]float64) string {
	if _, ok := m[snapshotChat]; ok {
		return snapshotChat
	}
	keys := sortedKeys(m)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// sortedKeys returns the sorted keys of any map[string]V.
func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// formatFloat renders a float64 with the minimum digits needed to round-trip.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
