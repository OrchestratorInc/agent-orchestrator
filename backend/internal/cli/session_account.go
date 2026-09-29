package cli

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

type sessionAccountSwitchRequest struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Mode             string `json:"mode"`
	AccountID        string `json:"accountId,omitempty"`
	Policy           string `json:"policy"`
	NewConversation  bool   `json:"newConversation,omitempty"`
}

type sessionAccountSwitchDTO struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"sessionId"`
	Provider         string    `json:"provider"`
	SourceMode       string    `json:"sourceMode"`
	SourceAccountID  string    `json:"sourceAccountId,omitempty"`
	SourceRevision   int64     `json:"sourceRevision"`
	TargetMode       string    `json:"targetMode"`
	TargetAccountID  string    `json:"targetAccountId,omitempty"`
	TargetRevision   int64     `json:"targetRevision"`
	Policy           string    `json:"policy"`
	NewConversation  bool      `json:"newConversation"`
	Phase            string    `json:"phase"`
	RecoveryRequired bool      `json:"recoveryRequired"`
	CanRetry         bool      `json:"canRetry"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type sessionAccountDTO struct {
	SessionID string                   `json:"sessionId"`
	Provider  string                   `json:"provider"`
	Mode      string                   `json:"mode"`
	AccountID string                   `json:"accountId,omitempty"`
	Revision  int64                    `json:"revision"`
	Blocked   bool                     `json:"blocked"`
	Switch    *sessionAccountSwitchDTO `json:"switch,omitempty"`
}

func newSessionAccountCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{Use: "account", Short: "Inspect or switch this session's account"}
	cmd.AddCommand(newSessionAccountGetCommand(ctx), newSessionAccountSwitchCommand(ctx))
	for _, action := range []string{"status", "retry", "cancel"} {
		cmd.AddCommand(newSessionAccountOperationCommand(ctx, action))
	}
	return cmd
}

func newSessionAccountGetCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use: "get <session-id>", Short: "Show the selected account and pending switch", Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateSessionAccountID(args[0]); err != nil {
				return err
			}
			var response sessionAccountDTO
			if err := ctx.getJSON(cmd.Context(), "sessions/"+url.PathEscape(args[0])+"/account", &response); err != nil {
				return safeAccountCommandError(err)
			}
			if response.SessionID != args[0] || (response.Switch != nil && response.Switch.SessionID != args[0]) {
				return errors.New("daemon returned account state for another session")
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "session: %s\nmode: %s\naccount: %s\nrevision: %d\nblocked: %t\n", response.SessionID, response.Mode, response.AccountID, response.Revision, response.Blocked); err != nil {
				return err
			}
			if response.Switch != nil {
				return writeSessionAccountSwitch(cmd, *response.Switch, false)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output safe account state as JSON")
	return cmd
}

func newSessionAccountSwitchCommand(ctx *commandContext) *cobra.Command {
	var request sessionAccountSwitchRequest
	var native, asJSON bool
	cmd := &cobra.Command{
		Use: "switch <session-id>", Short: "Select an account for only this session", Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range []string{args[0], request.OperationID} {
				if err := validateSessionAccountID(id); err != nil {
					return err
				}
			}
			if request.ExpectedRevision <= 0 || request.ExpectedRevision > 9007199254740991 {
				return usageError{errors.New("--expected-revision must be the positive revision shown by session account get")}
			}
			if request.Policy != "drain" && request.Policy != "interrupt" {
				return usageError{errors.New("choose --policy drain or --policy interrupt explicitly")}
			}
			request.Mode = "managed"
			if native {
				if request.AccountID != "" {
					return usageError{errors.New("--native and --account cannot be combined")}
				}
				request.Mode = "native"
			} else if err := validateSessionAccountID(request.AccountID); err != nil {
				return usageError{errors.New("choose exactly one --account or explicitly use --native")}
			}
			var response sessionAccountSwitchDTO
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(args[0])+"/account-switches", request, &response); err != nil {
				return safeAccountCommandError(err)
			}
			if response.SessionID != args[0] || response.ID != request.OperationID {
				return errors.New("daemon returned a different account switch")
			}
			return writeSessionAccountSwitch(cmd, response, asJSON)
		},
	}
	cmd.Flags().StringVar(&request.AccountID, "account", "", "Explicit managed account ID")
	cmd.Flags().BoolVar(&native, "native", false, "Explicitly return this session to its native connection")
	cmd.Flags().Int64Var(&request.ExpectedRevision, "expected-revision", 0, "Observed binding revision")
	cmd.Flags().StringVar(&request.Policy, "policy", "", "Choose drain or interrupt")
	cmd.Flags().StringVar(&request.OperationID, "operation-id", "", "Stable ID for this switch and identical retries")
	cmd.Flags().BoolVar(&request.NewConversation, "new-conversation", false, "Explicitly permit starting a new conversation")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the accepted operation as JSON")
	return cmd
}

func newSessionAccountOperationCommand(ctx *commandContext, action string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use: action + " <session-id> <operation-id>", Short: action + " a session account switch", Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range args {
				if err := validateSessionAccountID(id); err != nil {
					return err
				}
			}
			path := "sessions/" + url.PathEscape(args[0]) + "/account-switches/" + url.PathEscape(args[1])
			var response sessionAccountSwitchDTO
			var err error
			if action == "status" {
				err = ctx.getJSON(cmd.Context(), path, &response)
			} else {
				err = ctx.postJSON(cmd.Context(), path+"/"+action, struct{}{}, &response)
			}
			if err != nil {
				return safeAccountCommandError(err)
			}
			if response.SessionID != args[0] || response.ID != args[1] {
				return errors.New("daemon returned a different account switch")
			}
			return writeSessionAccountSwitch(cmd, response, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output safe operation state as JSON")
	return cmd
}

func validateSessionAccountID(id string) error {
	if id == "" || len(id) > 128 {
		return usageError{errors.New("session, account and operation IDs must contain 1 to 128 letters, digits, hyphens or underscores")}
	}
	for _, c := range id {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return usageError{errors.New("invalid session, account or operation ID")}
	}
	return nil
}

func writeSessionAccountSwitch(cmd *cobra.Command, response sessionAccountSwitchDTO, asJSON bool) error {
	if asJSON {
		return writeJSON(cmd.OutOrStdout(), response)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "operation: %s\nsession: %s\nsource mode: %s\nsource account: %s\ntarget mode: %s\ntarget account: %s\npolicy: %s\nnew conversation: %t\nphase: %s\nrecovery required: %t\nretry available: %t\n", response.ID, response.SessionID, response.SourceMode, response.SourceAccountID, response.TargetMode, response.TargetAccountID, response.Policy, response.NewConversation, response.Phase, response.RecoveryRequired, response.CanRetry)
	return err
}
