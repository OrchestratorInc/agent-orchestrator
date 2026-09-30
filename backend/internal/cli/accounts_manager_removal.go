package cli

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

func newManagedAccountsImpactCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "removal-impact <account-id>", Short: "Preview every managed binding affected by removal", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateSessionAccountID(args[0]); err != nil {
			return err
		}
		var response managedRemovalImpactDTO
		if err := ctx.getJSON(cmd.Context(), managedAccountsPath+"/"+url.PathEscape(args[0])+"/removal-impact", &response); err != nil {
			return safeAccountCommandError(err)
		}
		if response.AccountID != args[0] {
			return errors.New("daemon returned removal impact for another account")
		}
		if asJSON {
			return writeJSON(cmd.OutOrStdout(), response)
		}
		return writeManagedRemovalImpact(cmd, response)
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the observed impact as JSON")
	return cmd
}

func newManagedAccountsRemoveCommand(ctx *commandContext) *cobra.Command {
	var request struct {
		OperationID      string `json:"operationId"`
		ExpectedRevision int64  `json:"expectedRevision"`
		Confirmed        bool   `json:"confirmed"`
	}
	var asJSON bool
	cmd := &cobra.Command{Use: "remove <account-id>", Short: "Confirm coordinated removal using an observed impact revision", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		for _, id := range []string{args[0], request.OperationID} {
			if err := validateSessionAccountID(id); err != nil {
				return err
			}
		}
		if !cmd.Flags().Changed("expected-revision") || request.ExpectedRevision < 0 || request.ExpectedRevision > 9007199254740991 || !request.Confirmed {
			return usageError{errors.New("removal requires --confirm and the explicit --expected-revision from removal-impact")}
		}
		var response managedRemovalDTO
		if err := ctx.postJSON(cmd.Context(), managedAccountsPath+"/"+url.PathEscape(args[0])+"/removals", request, &response); err != nil {
			return safeAccountCommandError(err)
		}
		if !response.matches(args[0], request.OperationID) {
			return errors.New("daemon returned a different account removal")
		}
		return writeManagedRemoval(cmd, response, asJSON)
	}}
	cmd.Flags().StringVar(&request.OperationID, "operation-id", "", "Stable ID for this removal and identical retries")
	cmd.Flags().Int64Var(&request.ExpectedRevision, "expected-revision", 0, "Observed removal-impact revision; zero must be explicit")
	cmd.Flags().BoolVar(&request.Confirmed, "confirm", false, "Confirm stopping bound sessions and removing this credential")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the accepted removal as JSON")
	return cmd
}

func newManagedAccountsRemovalCommand(ctx *commandContext, action string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "removal-" + action + " <account-id> <operation-id>", Short: action + " a coordinated account removal", Args: usageArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		for _, id := range args {
			if err := validateSessionAccountID(id); err != nil {
				return err
			}
		}
		path := "accounts-manager/removals/" + url.PathEscape(args[1])
		var response managedRemovalDTO
		if err := ctx.getJSON(cmd.Context(), path, &response); err != nil {
			return safeAccountCommandError(err)
		}
		if !response.matches(args[0], args[1]) {
			return errors.New("daemon returned a different account removal")
		}
		if action != "status" {
			response = managedRemovalDTO{}
			if err := ctx.postJSON(cmd.Context(), path+"/"+action, struct{}{}, &response); err != nil {
				return safeAccountCommandError(err)
			}
			if !response.matches(args[0], args[1]) {
				return errors.New("daemon returned a different account removal")
			}
		}
		return writeManagedRemoval(cmd, response, asJSON)
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output safe removal state as JSON")
	return cmd
}

func (r managedRemovalDTO) matches(accountID, operationID string) bool {
	return r.AccountID == accountID && r.ID == operationID && r.Impact.AccountID == accountID
}

func writeManagedRemoval(cmd *cobra.Command, response managedRemovalDTO, asJSON bool) error {
	response.ErrorCode = accountControlFailureCode(response.Phase, response.ErrorCode)
	if asJSON {
		return writeJSON(cmd.OutOrStdout(), response)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "operation: %s\nphase: %s\ncan cancel: %t\nrecovery required: %t\n", response.ID, response.Phase, response.CanCancel, response.RecoveryRequired); err != nil {
		return err
	}
	if response.ErrorCode != "" {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "error code: %s\n", response.ErrorCode); err != nil {
			return err
		}
	}
	return writeManagedRemovalImpact(cmd, response.Impact)
}

func writeManagedRemovalImpact(cmd *cobra.Command, impact managedRemovalImpactDTO) error {
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "account: %s\nimpact revision: %d\nbound sessions: %d\n", impact.AccountID, impact.Revision, len(impact.Sessions)); err != nil {
		return err
	}
	for _, session := range impact.Sessions {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "session: %s revision: %d stopped: %t\n", session.SessionID, session.BindingRevision, session.Stopped); err != nil {
			return err
		}
	}
	return nil
}
