package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type agentAccount struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Device   bool   `json:"device"`
	Email    string `json:"email,omitempty"`
	Plan     string `json:"plan,omitempty"`
	Default  bool   `json:"default"`
	SignedIn bool   `json:"signedIn"`
	LoggedIn bool   `json:"loggedIn"`
	Problem  string `json:"problem,omitempty"`
	OrgID    string `json:"orgId,omitempty"`
	OrgName  string `json:"orgName,omitempty"`
}

type agentAccountsResponse struct {
	Accounts []agentAccount `json:"accounts"`
}

type sessionAgentAccounts struct {
	Claude string `json:"claude,omitempty"`
	Codex  string `json:"codex,omitempty"`
}

type switchSessionAccountRequest struct {
	AccountID string `json:"accountId"`
	StopTurn  bool   `json:"stopTurn,omitempty"`
}

type accountTransition struct {
	ID              string `json:"id"`
	Phase           string `json:"phase"`
	SourceAccountID string `json:"sourceAccountId,omitempty"`
	TargetAccountID string `json:"targetAccountId,omitempty"`
	ErrorCode       string `json:"errorCode,omitempty"`
	ErrorDetail     string `json:"errorDetail,omitempty"`
}

type switchSessionAccountResponse struct {
	SessionID  string            `json:"sessionId"`
	Transition accountTransition `json:"transition"`
}

type sessionTransitionStatus struct {
	Transition *accountTransition `json:"transition,omitempty"`
}

var accountSwitchPoll = time.Second

func newAccountCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "List Claude Code and Codex subscription accounts, set the default, and move a session to another account",
	}
	cmd.AddCommand(newAccountListCommand(ctx), newAccountDefaultCommand(ctx), newAccountShowCommand(ctx), newAccountSwitchCommand(ctx))
	return cmd
}

func accountProvider(agent string) (string, error) {
	switch strings.TrimSpace(agent) {
	case "":
		return "", nil
	case "claude", "claude-code":
		return "claude", nil
	case "codex":
		return "codex", nil
	}
	return "", usageError{fmt.Errorf("--agent must be claude-code or codex, got %q", agent)}
}

func newAccountListCommand(ctx *commandContext) *cobra.Command {
	var agent string
	var refresh, asJSON bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List this machine's subscription accounts",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, err := accountProvider(agent)
			if err != nil {
				return err
			}
			path := "agents/accounts"
			if refresh {
				path += "?refresh=true"
			}
			var res agentAccountsResponse
			if err := ctx.getJSON(cmd.Context(), path, &res); err != nil {
				return err
			}
			accounts := make([]agentAccount, 0, len(res.Accounts))
			for _, account := range res.Accounts {
				if provider == "" || account.Provider == provider {
					accounts = append(accounts, account)
				}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), agentAccountsResponse{Accounts: accounts})
			}
			return writeAccountList(cmd, accounts)
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Only this agent's accounts: claude-code or codex")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Read each account's sign-in and limits again instead of using cached values")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	return cmd
}

func writeAccountList(cmd *cobra.Command, accounts []agentAccount) error {
	out := cmd.OutOrStdout()
	if len(accounts) == 0 {
		_, err := fmt.Fprintln(out, "No accounts.")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tAGENT\tACCOUNT\tORG\tPLAN\tSTATE")
	for _, account := range accounts {
		state := "signed in"
		if !account.SignedIn {
			state = "signed out"
		}
		if account.Default {
			state += ", default"
		}
		if account.Problem != "" {
			state += " (" + account.Problem + ")"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", account.ID, accountAgentLabel(account.Provider), accountLabel(account), accountOrg(account, accounts), dash(account.Plan), state)
	}
	return tw.Flush()
}

func accountAgentLabel(provider string) string {
	if provider == "claude" {
		return "claude-code"
	}
	return provider
}

func accountLabel(account agentAccount) string {
	label := account.Email
	if label == "" {
		label = "(no email)"
	}
	if account.Device {
		label += " [this computer's login]"
	}
	return label
}

func accountOrg(account agentAccount, peers []agentAccount) string {
	label := account.OrgName
	for _, peer := range peers {
		if peer.ID == account.ID || account.OrgID == "" || peer.OrgID == account.OrgID || peer.OrgName != account.OrgName || !strings.EqualFold(peer.Email, account.Email) {
			continue
		}
		short := account.OrgID[:min(8, len(account.OrgID))]
		if label == "" {
			return short
		}
		return label + " (" + short + ")"
	}
	return dash(label)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func newAccountDefaultCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "default <account-id>",
		Short: "Make an account the default for new sessions of its agent",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res agentAccountsResponse
			if err := ctx.postJSON(cmd.Context(), "agents/accounts/"+url.PathEscape(args[0])+"/default", struct{}{}, &res); err != nil {
				return err
			}
			for _, account := range res.Accounts {
				if account.ID == args[0] {
					_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s (%s) is now the default for new %s sessions\n", account.ID, accountLabel(account), accountAgentLabel(account.Provider))
					return err
				}
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s is now the default\n", args[0])
			return err
		},
	}
}

func newAccountShowCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show [session-id]",
		Short: "Show the account a session runs on (default: this session)",
		Args:  rangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID, err := accountSessionID(args)
			if err != nil {
				return err
			}
			if _, err := ctx.fetchScopedSession(cmd.Context(), sessionID, ""); err != nil {
				return err
			}
			var res sessionAgentAccounts
			if err := ctx.getJSON(cmd.Context(), "agents/accounts/sessions/"+url.PathEscape(sessionID), &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			out := cmd.OutOrStdout()
			_, err = fmt.Fprintf(out, "claude-code: %s\ncodex: %s\n", dash(res.Claude), dash(res.Codex))
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	return cmd
}

func accountSessionID(args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return strings.TrimSpace(args[0]), nil
	}
	if id := strings.TrimSpace(os.Getenv("AO_SESSION_ID")); id != "" {
		return id, nil
	}
	return "", usageError{fmt.Errorf("pass a session id, or run inside an Orchestrator session")}
}

func newAccountSwitchCommand(ctx *commandContext) *cobra.Command {
	var stopTurn, wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "switch <session-id> <account-id>",
		Short: "Move a session to another account; the agent continues the same conversation there",
		Long: "Move a session to another account. The agent restarts in the same conversation on the new account.\n" +
			"A working agent finishes its turn first, unless --stop-turn stops it now.\n" +
			"An agent that moves its own session without --stop-turn moves after its turn ends, so do not pass --wait then.",
		Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID, accountID := args[0], args[1]
			var res switchSessionAccountResponse
			if err := ctx.putJSON(cmd.Context(), "agents/accounts/sessions/"+url.PathEscape(sessionID), switchSessionAccountRequest{AccountID: accountID, StopTurn: stopTurn}, &res); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !wait {
				_, err := fmt.Fprintf(out, "moving %s to %s (transition %s, %s)\n", sessionID, accountID, res.Transition.ID, res.Transition.Phase)
				return err
			}
			done, err := ctx.awaitAccountSwitch(cmd.Context(), sessionID, res.Transition, timeout)
			if err != nil {
				return err
			}
			if done.Phase != "completed" {
				detail := done.ErrorDetail
				if detail == "" {
					detail = done.ErrorCode
				}
				return fmt.Errorf("move of %s to %s ended %s: %s", sessionID, accountID, done.Phase, dash(detail))
			}
			_, err = fmt.Fprintf(out, "moved %s to %s\n", sessionID, accountID)
			return err
		},
	}
	cmd.Flags().BoolVar(&stopTurn, "stop-turn", false, "Stop a turn in progress now instead of waiting for it to finish")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait until the move completes or fails")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "How long --wait waits")
	return cmd
}

func (c *commandContext) awaitAccountSwitch(ctx context.Context, sessionID string, started accountTransition, timeout time.Duration) (accountTransition, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	current := started
	for {
		switch current.Phase {
		case "completed", "failed", "cancelled", "recovery_required":
			return current, nil
		}
		select {
		case <-ctx.Done():
			return current, fmt.Errorf("move of %s still %s after %s; check it with `ao account show %s`", sessionID, current.Phase, timeout, sessionID)
		case <-time.After(accountSwitchPoll):
		}
		var status sessionTransitionStatus
		if err := c.getJSON(ctx, "sessions/"+url.PathEscape(sessionID)+"/interface-transition", &status); err != nil {
			return current, err
		}
		if status.Transition != nil && status.Transition.ID != started.ID {
			return current, fmt.Errorf("move of %s ended, but a later change (transition %s) replaced it before its outcome was read; check it with `ao account show %s`", sessionID, status.Transition.ID, sessionID)
		}
		if status.Transition != nil {
			current = *status.Transition
		}
	}
}
