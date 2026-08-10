// Package tool implements the tooling subcommands exposed by the plugin
// binary via --tool <name>. These mirror the CLI helpers that were previously
// compiled into llm-proxy as `llm-proxy login` and `llm-proxy providers list-models`.
package tool

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
)

// NewRootCmd returns the root cobra command for tooling subcommands.
func NewRootCmd(out, errOut io.Writer, cfg *server.Config) *cobra.Command {
	root := &cobra.Command{
		Use:          "copilot-tool",
		SilenceUsage: true,
	}
	root.AddCommand(newLoginCmd(out, errOut, cfg))
	root.AddCommand(newLogoutCmd(out, cfg))
	root.AddCommand(newBudgetCmd(out, cfg))
	root.AddCommand(newListModelsCmd(out, errOut, cfg))
	return root
}

// --- login ---

func newLoginCmd(out, errOut io.Writer, cfg *server.Config) *cobra.Command {
	var (
		oauthClientID   string
		gitHubLoginBase string
		gitHubAPIBase   string
		openBrowser     bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with GitHub Copilot via device-code flow",
		Long: "Initiates the GitHub device-code OAuth flow. Prints a URL and\n" +
			"one-time code; open the URL in your browser, enter the code, and\n" +
			"authorise. The resulting token is written to the user config dir\n" +
			"(mode 0600).\n\n" +
			"The running proxy picks up the new token on the next request or\n" +
			"immediately when it receives a management/reload POST.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Pre-merge: flags > cfg > defaults.
			resolvedCfg := server.Config{
				OAuthClientID:   oauthClientID,
				GitHubLoginBase: gitHubLoginBase,
				GitHubAPIBase:   gitHubAPIBase,
			}
			if resolvedCfg.OAuthClientID == "" && cfg != nil {
				resolvedCfg.OAuthClientID = cfg.OAuthClientID
			}
			if resolvedCfg.GitHubLoginBase == "" && cfg != nil {
				resolvedCfg.GitHubLoginBase = cfg.GitHubLoginBase
			}
			if resolvedCfg.GitHubAPIBase == "" && cfg != nil {
				resolvedCfg.GitHubAPIBase = cfg.GitHubAPIBase
			}
			if cfg != nil {
				resolvedCfg.UserAgent = cfg.UserAgent
				resolvedCfg.EditorVersion = cfg.EditorVersion
			}

			authenticator, err := server.NewAuthenticatorFromConfig(resolvedCfg, nil)
			if err != nil {
				return fmt.Errorf("init authenticator: %w", err)
			}

			code, err := authenticator.RequestDeviceCode(cmd.Context())
			if err != nil {
				return fmt.Errorf("request device code: %w", err)
			}

			verifyURL := code.VerificationURI
			if verifyURL == "" {
				verifyURL = resolvedCfg.GitHubLoginBase + "/login/device"
			}

			if openBrowser {
				if berr := openBrowserURL(verifyURL); berr != nil {
					fmt.Fprintf(errOut, "Could not open browser (%v). Open this URL manually:\n\n  %s\n\n", berr, verifyURL) //nolint:errcheck
				} else {
					fmt.Fprintf(out, "\nOpened browser. Enter code: %s\nWaiting for authentication...\n", code.UserCode) //nolint:errcheck
				}
			} else {
				fmt.Fprintf(out, "\nOpen this URL in your browser:\n\n  %s\n\nEnter code: %s\n\nWaiting for authentication...\n", verifyURL, code.UserCode) //nolint:errcheck
			}

			tok, err := authenticator.PollForToken(cmd.Context(), code)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Authenticated as: %s\nToken saved.\n", tok.TokenType) //nolint:errcheck
			return nil
		},
	}
	cmd.Flags().StringVar(&oauthClientID, "client-id", "", "GitHub OAuth client ID (default: public Copilot client id)")
	cmd.Flags().StringVar(&gitHubLoginBase, "github-login-base", "", "GitHub login base URL")
	cmd.Flags().StringVar(&gitHubAPIBase, "github-api-base", "", "GitHub API base URL")
	cmd.Flags().BoolVarP(&openBrowser, "browser", "b", false, "open the authorization URL in the default browser automatically")
	return cmd
}

// --- logout ---

func newLogoutCmd(out io.Writer, cfg *server.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove any stored GitHub Copilot credentials",
		Long: "Deletes the stored GitHub OAuth token and any cached short-lived\n" +
			"Copilot API token from disk. The proxy will require `login` before\n" +
			"it can serve requests again.",
		RunE: func(_ *cobra.Command, _ []string) error {
			var resolvedCfg server.Config
			if cfg != nil {
				resolvedCfg = *cfg
			}

			authenticator, err := server.NewAuthenticatorFromConfig(resolvedCfg, nil)
			if err != nil {
				return fmt.Errorf("init authenticator: %w", err)
			}
			if err := authenticator.Logout(); err != nil {
				return fmt.Errorf("logout: %w", err)
			}
			fmt.Fprintln(out, "Logged out. Credentials removed.") //nolint:errcheck
			return nil
		},
	}
}

// --- list-models ---

func newListModelsCmd(out, errOut io.Writer, cfg *server.Config) *cobra.Command {
	var gitHubAPIBase string
	cmd := &cobra.Command{
		Use:   "list-models",
		Short: "List upstream Copilot models available to the configured credential",
		Long: "Queries the GitHub Copilot /models catalogue using the stored\n" +
			"credentials and prints one model ID per line to stdout.\n\n" +
			"This is a standalone discovery path: it does not start the proxy\n" +
			"and is not filtered by any configured allow-list. Use it to learn\n" +
			"which model IDs to place in a backend allow-list, or to verify\n" +
			"that credentials are working.\n\n" +
			"Exit codes:\n" +
			"  0  model list written to stdout\n" +
			"  1  authentication, network, or upstream error",
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolvedCfg := server.Config{}
			if cfg != nil {
				resolvedCfg = *cfg
			}
			if gitHubAPIBase != "" {
				resolvedCfg.GitHubAPIBase = gitHubAPIBase
			}
			// list-models needs a server instance so it can call the upstream
			// /models catalogue.  We give it a no-op name so New doesn't
			// complain; the name is never used in this code path.
			if resolvedCfg.Name == "" {
				resolvedCfg.Name = "copilot"
			}
			srv, err := server.New(resolvedCfg, nil, nil)
			if err != nil {
				fmt.Fprintf(errOut, "error: init server: %v\n", err) //nolint:errcheck // best-effort stderr
				return err
			}
			models, err := srv.ListModelsForTool(cmd.Context())
			if err != nil {
				fmt.Fprintf(errOut, "error: %v\n", err) //nolint:errcheck // best-effort stderr
				return err
			}
			for _, m := range models {
				fmt.Fprintln(out, m) //nolint:errcheck // best-effort stdout
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&gitHubAPIBase, "github-api-base", "", "GitHub API base URL")
	return cmd
}

// openBrowserURL attempts to open url in the system default browser.
func openBrowserURL(rawURL string) error {
	var cmd *exec.Cmd
	switch {
	case commandExists("xdg-open"):
		cmd = exec.Command("xdg-open", rawURL) //nolint:gosec // operator-controlled URL
	case commandExists("open"):
		cmd = exec.Command("open", rawURL) //nolint:gosec // operator-controlled URL
	case commandExists("start"):
		cmd = exec.Command("cmd", "/c", "start", rawURL) //nolint:gosec // operator-controlled URL
	default:
		return fmt.Errorf("no browser-open command found")
	}
	return cmd.Start()
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// RunTool dispatches --tool <name> by building the tooling root command and
// executing args through it.
func RunTool(ctx context.Context, out, errOut io.Writer, cfg *server.Config, toolName string, args []string) error {
	root := NewRootCmd(out, errOut, cfg)
	root.SetArgs(append([]string{toolName}, args...))
	root.SetOut(out)
	root.SetErr(errOut)
	return root.ExecuteContext(ctx)
}
