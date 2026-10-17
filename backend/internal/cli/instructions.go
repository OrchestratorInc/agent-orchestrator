package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// These DTOs mirror the daemon's standing-instructions API without importing
// controller types.
type instructionsDTO struct {
	ProjectID      string                 `json:"projectId,omitempty"`
	Body           string                 `json:"body"`
	Revision       int64                  `json:"revision"`
	UpdatedAt      *time.Time             `json:"updatedAt,omitempty"`
	UpdatedBy      *instructionsAuthorDTO `json:"updatedBy,omitempty"`
	ProtectedLines []string               `json:"protectedLines"`
	RemovedLines   []string               `json:"removedLines"`
}

type instructionsAuthorDTO struct {
	SessionID string `json:"sessionId,omitempty"`
	Title     string `json:"title,omitempty"`
	Dreamed   bool   `json:"dreamed"`
}

type instructionsEnvelopeDTO struct {
	Instructions instructionsDTO `json:"instructions"`
}

type saveInstructionsDTO struct {
	Body      string `json:"body"`
	Revision  *int64 `json:"revision,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

func newInstructionsCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instructions",
		Short: "Show or save the standing instructions AO adds to new sessions",
		Long: "AO adds your instructions to every new worker and orchestrator, and a project's instructions to that project's sessions. " +
			"Without --project these commands use your own instructions. Dreaming automations update both after a session ends; " +
			"you can also edit them on the desktop app's Automations page.",
	}
	cmd.AddCommand(newInstructionsShowCommand(ctx), newInstructionsSetCommand(ctx))
	return cmd
}

func instructionsPath(project string) string {
	if project = strings.TrimSpace(project); project != "" {
		return "projects/" + url.PathEscape(project) + "/instructions"
	}
	return "instructions"
}

func newInstructionsShowCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the instructions",
		Long:  "Print the instructions. --json adds the revision to pass to `set --revision`, the lines a dreaming save must keep (protectedLines), and lines people deleted (removedLines).",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var response instructionsEnvelopeDTO
			if err := ctx.getJSON(cmd.Context(), instructionsPath(project), &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response.Instructions)
			}
			if response.Instructions.Body == "" {
				return nil
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), response.Instructions.Body)
			return err
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id; omit for your own instructions")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON with the revision and line lists")
	return cmd
}

func newInstructionsSetCommand(ctx *commandContext) *cobra.Command {
	var project, file string
	var revision int64
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Replace the instructions with a file's contents",
		Long: "Replace the instructions with the contents of --file (- reads stdin). With --revision the save fails if the instructions changed since that revision. " +
			"Inside an AO session only a dreaming session may save; other sessions can only read.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(file) == "" {
				return usageError{errors.New("--file is required (use - for stdin)")}
			}
			var data []byte
			var err error
			if file == "-" {
				data, err = io.ReadAll(cmd.InOrStdin())
			} else {
				data, err = os.ReadFile(file)
			}
			if err != nil {
				return usageError{fmt.Errorf("read --file: %w", err)}
			}
			body := saveInstructionsDTO{Body: string(data), SessionID: strings.TrimSpace(os.Getenv("AO_SESSION_ID"))}
			if cmd.Flags().Changed("revision") {
				body.Revision = &revision
			}
			var response instructionsEnvelopeDTO
			if err := ctx.putJSON(cmd.Context(), instructionsPath(project), body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response.Instructions)
			}
			scope := "your instructions"
			if response.Instructions.ProjectID != "" {
				scope = "project " + response.Instructions.ProjectID + " instructions"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "saved %s (revision %d)\n", scope, response.Instructions.Revision)
			return err
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id; omit for your own instructions")
	cmd.Flags().StringVar(&file, "file", "", "File with the new instructions; - reads stdin")
	cmd.Flags().Int64Var(&revision, "revision", 0, "Fail unless this is still the latest revision (0: none saved yet)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print the saved instructions as JSON")
	return cmd
}
