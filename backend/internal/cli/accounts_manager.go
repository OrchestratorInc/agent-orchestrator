package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

const managedAccountsPath = "accounts-manager/accounts"

func newManagedAccountsCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{Use: "accounts", Short: "Manage saved accounts through the local daemon"}
	cmd.AddCommand(newManagedAccountsListCommand(ctx), newManagedAccountsLoginCommand(ctx), newManagedAccountsLoginStateCommand(ctx, false), newManagedAccountsLoginStateCommand(ctx, true))
	cmd.AddCommand(newManagedAccountsCredentialCommand(ctx, false), newManagedAccountsCredentialCommand(ctx, true))
	for _, action := range []string{"refresh", "disable", "enable", "rename"} {
		cmd.AddCommand(newManagedAccountsMaintenanceCommand(ctx, action))
	}
	cmd.AddCommand(newManagedAccountsImpactCommand(ctx), newManagedAccountsRemoveCommand(ctx))
	for _, action := range []string{"status", "retry", "cancel"} {
		cmd.AddCommand(newManagedAccountsRemovalCommand(ctx, action))
	}
	return cmd
}

func newManagedAccountsListCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "ls", Aliases: []string{"list"}, Short: "List saved accounts without sign-in secrets", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		var response managedAccountsDTO
		if err := ctx.getJSON(cmd.Context(), managedAccountsPath, &response); err != nil {
			return safeAccountCommandError(err)
		}
		return writeManagedAccounts(cmd, response, asJSON)
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output public inventory as JSON")
	return cmd
}

func newManagedAccountsLoginCommand(ctx *commandContext) *cobra.Command {
	var request struct {
		Provider   string `json:"provider"`
		Mode       string `json:"mode"`
		AccountID  string `json:"accountId,omitempty"`
		Generation uint64 `json:"generation,omitempty"`
	}
	var asJSON bool
	cmd := &cobra.Command{Use: "login", Short: "Start an explicit sign-in or reconnect", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if validateSessionAccountID(request.Provider) != nil || (request.Mode != "callback" && request.Mode != "device") {
			return usageError{errors.New("choose --provider and --mode callback or device")}
		}
		if cmd.Flags().Changed("account") || cmd.Flags().Changed("generation") {
			if validateSessionAccountID(request.AccountID) != nil || request.Generation == 0 {
				return usageError{errors.New("reconnect requires --account and its observed --generation")}
			}
		}
		var response managedLoginInstructions
		if err := ctx.postJSON(cmd.Context(), "accounts-manager/oauth-sessions", request, &response); err != nil {
			return safeAccountCommandError(err)
		}
		if validateSessionAccountID(response.ID) != nil || response.Provider != request.Provider || response.Mode != request.Mode || response.AccountID != request.AccountID {
			return errors.New("daemon returned a different account sign-in")
		}
		if response.AuthorizationURL != "" {
			u, err := url.Parse(response.AuthorizationURL)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
				return errors.New("daemon returned invalid sign-in instructions")
			}
		}
		if asJSON {
			return writeJSON(cmd.OutOrStdout(), response)
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "operation: %s\nstatus: %s\nopen: %q\ncode: %q\n", response.ID, response.Status, response.AuthorizationURL, response.UserCode)
		return err
	}}
	cmd.Flags().StringVar(&request.Provider, "provider", "", "Explicit provider from the account catalog")
	cmd.Flags().StringVar(&request.Mode, "mode", "", "Choose callback or device sign-in")
	cmd.Flags().StringVar(&request.AccountID, "account", "", "Reconnect this account without replacing its identity")
	cmd.Flags().Uint64Var(&request.Generation, "generation", 0, "Observed credential generation for reconnect")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output explicit sign-in instructions as JSON")
	return cmd
}

func newManagedAccountsLoginStateCommand(ctx *commandContext, cancel bool) *cobra.Command {
	var asJSON bool
	action := "status"
	if cancel {
		action = "cancel"
	}
	cmd := &cobra.Command{Use: "login-" + action + " <operation-id>", Short: action + " a sign-in by its operation ID", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateSessionAccountID(args[0]); err != nil {
			return err
		}
		var response managedLoginDTO
		if cancel {
			if err := ctx.deleteJSON(cmd.Context(), "accounts-manager/oauth-sessions/"+url.PathEscape(args[0]), nil); err != nil {
				return safeAccountCommandError(err)
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), managedLoginCancellationAcknowledgement{OperationID: args[0], CancellationRequestAcknowledged: true})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "operation: %s\ncancellation request acknowledged; sign-in outcome not confirmed\n", args[0])
			return err
		}
		var inventory managedAccountsDTO
		if err := ctx.getJSON(cmd.Context(), managedAccountsPath, &inventory); err != nil {
			return safeAccountCommandError(err)
		}
		if inventory.Stale || inventory.Availability != "ready" {
			return errors.New("sign-in state is unavailable; refresh the account inventory")
		}
		for _, login := range inventory.OAuthSessions {
			if login.ID == args[0] {
				response = login
				break
			}
		}
		if response.ID == "" {
			return errors.New("account sign-in was not found")
		}
		if asJSON {
			return writeJSON(cmd.OutOrStdout(), response)
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "operation: %s\nstatus: %s\n", response.ID, response.Status)
		return err
	}}
	jsonHelp := "Output safe sign-in state as JSON"
	if cancel {
		cmd.Short = "Request sign-in cancellation by its operation ID"
		jsonHelp = "Output cancellation request acknowledgement as JSON"
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, jsonHelp)
	return cmd
}

func newManagedAccountsCredentialCommand(ctx *commandContext, imported bool) *cobra.Command {
	var provider, operationID string
	var stdin, asJSON bool
	action := "add-key"
	if imported {
		action = "import"
	}
	cmd := &cobra.Command{Use: action, Short: "Add a credential from bounded, piped standard input", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if !stdin || validateSessionAccountID(provider) != nil || validateSessionAccountID(operationID) != nil {
			return usageError{errors.New("choose --provider, --operation-id and --stdin explicitly")}
		}
		if file, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(file.Fd()) {
			return usageError{errors.New("pipe credential input; terminal echo is not permitted")}
		}
		limit := 8192
		if imported {
			limit = 1 << 20
		}
		input, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), int64(limit+1)))
		if err != nil {
			return errors.New("could not read credential input")
		}
		defer clear(input)
		if len(input) == 0 || len(input) > limit {
			return usageError{errors.New("credential input is empty or exceeds the size limit")}
		}
		request := map[string]any{"provider": provider, "operationId": operationID}
		path := managedAccountsPath + "/api-key"
		maxPayload := 16 << 10
		if imported {
			var object map[string]json.RawMessage
			if json.Unmarshal(input, &object) != nil || len(object) == 0 {
				return usageError{errors.New("credential input must be one nonempty JSON object")}
			}
			request["filename"], request["credential"] = "credential.json", json.RawMessage(input)
			path = managedAccountsPath + "/import"
			maxPayload += 1 << 20
		} else {
			key := strings.TrimSpace(string(input))
			if key == "" || strings.ContainsAny(key, "\r\n\x00") {
				return usageError{errors.New("credential input must contain one nonempty key")}
			}
			request["key"] = key
		}
		encoded, err := json.Marshal(request)
		if err != nil || len(encoded) > maxPayload {
			return usageError{errors.New("encoded credential input exceeds the request limit")}
		}
		clear(encoded)
		var response managedAccountsDTO
		if err := ctx.postJSON(cmd.Context(), path, request, &response); err != nil {
			return safeAccountCommandError(err)
		}
		return writeManagedAccounts(cmd, response, asJSON)
	}}
	cmd.Flags().StringVar(&provider, "provider", "", "Explicit provider from the account catalog")
	cmd.Flags().StringVar(&operationID, "operation-id", "", "Stable ID for identical add retries")
	cmd.Flags().BoolVar(&stdin, "stdin", false, "Read the credential from piped standard input")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output public inventory as JSON")
	return cmd
}

func newManagedAccountsMaintenanceCommand(ctx *commandContext, action string) *cobra.Command {
	var asJSON bool
	var generation uint64
	use, count := action+" <account-id>", 1
	if action == "rename" {
		use, count = action+" <account-id> <label>", 2
	}
	cmd := &cobra.Command{Use: use, Short: action + " an explicitly selected managed account", Args: usageArgs(cobra.ExactArgs(count)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateSessionAccountID(args[0]); err != nil {
			return err
		}
		var response managedAccountsDTO
		path := managedAccountsPath + "/" + url.PathEscape(args[0])
		var err error
		switch action {
		case "refresh":
			err = ctx.postJSON(cmd.Context(), path+"/refresh", struct{}{}, &response)
		case "rename":
			if generation == 0 || strings.TrimSpace(args[1]) == "" || len(args[1]) > 256 {
				return usageError{errors.New("rename requires a nonempty label and its observed --generation")}
			}
			err = ctx.patchJSON(cmd.Context(), path, map[string]any{"label": args[1], "generation": generation}, &response)
		default:
			err = ctx.patchJSON(cmd.Context(), path, map[string]bool{"disabled": action == "disable"}, &response)
		}
		if err != nil {
			return safeAccountCommandError(err)
		}
		return writeManagedAccounts(cmd, response, asJSON)
	}}
	if action == "rename" {
		cmd.Flags().Uint64Var(&generation, "generation", 0, "Observed credential generation")
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output public inventory as JSON")
	return cmd
}

func writeManagedAccounts(cmd *cobra.Command, response managedAccountsDTO, asJSON bool) error {
	if response.Availability != "ready" && response.Availability != "starting" && response.Availability != "degraded" {
		return errors.New("daemon returned invalid account inventory")
	}
	if asJSON {
		return writeJSON(cmd.OutOrStdout(), response)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintf(w, "availability: %s\nstale: %t\nrevision: %d\nID\tPROVIDER\tLABEL\tSTATUS\tGENERATION\tDISABLED\n", response.Availability, response.Stale, response.Revision); err != nil {
		return err
	}
	for _, account := range response.Accounts {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%q\t%s\t%d\t%t\n", account.ID, account.Provider, account.Label, account.Status, account.Generation, account.Disabled); err != nil {
			return err
		}
	}
	return w.Flush()
}

func safeAccountCommandError(err error) error {
	var response apiResponseError
	if errors.As(err, &response) {
		response.ErrorBody.Message = "Account request could not be completed"
		switch response.StatusCode {
		case http.StatusConflict:
			response.ErrorBody.Message = "Account state changed; inspect it before retrying"
		case http.StatusNotImplemented, http.StatusServiceUnavailable:
			response.ErrorBody.Message = "Account control is unavailable"
		case http.StatusBadRequest:
			response.ErrorBody.Message = "Account request was rejected"
		}
		if !safeAccountDiagnosticToken(response.ErrorBody.Code) {
			response.ErrorBody.Code = "ACCOUNT_REQUEST_FAILED"
		}
		if !safeAccountDiagnosticToken(response.ErrorBody.RequestID) {
			response.ErrorBody.RequestID = ""
		}
		return response
	}
	if errors.Is(err, errDaemonUnavailable) {
		return fmt.Errorf("account request: %w", errDaemonUnavailable)
	}
	return errors.New("account request failed before a valid daemon response; inspect daemon status")
}

func safeAccountDiagnosticToken(value string) bool {
	if len(value) > 128 {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '/' || c == '.' {
			continue
		}
		return false
	}
	return true
}
