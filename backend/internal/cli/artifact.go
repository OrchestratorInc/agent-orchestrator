package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// maxListedRootFiles bounds how many unregistered root files one
// ao artifact add names; the rest are counted.
const maxListedRootFiles = 5

type artifactRegisterRequest struct {
	Paths []string `json:"paths"`
}

type artifactRegisterResponse struct {
	Registered     []string `json:"registered"`
	OtherFiles     int      `json:"otherFiles"`
	OtherRootFiles []string `json:"otherRootFiles"`
}

func newArtifactCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifact",
		Short: "Manage this session's artifacts",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "add <path>...",
		Short: "Register files in the artifact directory as deliverables",
		Long: "Register files or bundle directories in this session's artifact directory as\n" +
			"its deliverables. Only registered files are listed as artifacts. Other files\n" +
			"stay in the directory, so links between them keep working.\n\n" +
			"A path is absolute, or relative to the artifact directory. Run this once per\n" +
			"path, not after every edit. A renamed or copied file is a new path.",
		Example: `  ao artifact add "$AO_SESSION_ARTIFACTS_DIR/report.html"
  ao artifact add brand.html icons/app.icon`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.artifactAdd(cmd.Context(), cmd.OutOrStdout(), args)
		},
	})
	return cmd
}

func (c *commandContext) artifactAdd(ctx context.Context, out io.Writer, paths []string) error {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return usageError{errors.New("usage: AO_SESSION_ID is required")}
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return usageError{errors.New("usage: paths must be non-empty")}
		}
	}
	var response artifactRegisterResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/artifact-registrations"
	if err := c.postJSON(ctx, path, artifactRegisterRequest{Paths: paths}, &response); err != nil {
		return err
	}
	_, err := io.WriteString(out, artifactAddSummary(response))
	return err
}

// artifactAddSummary tells the agent what the user will and won't see. Files
// left unregistered do not appear as artifacts, and nothing else says so.
func artifactAddSummary(r artifactRegisterResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Registered %s.\n", strings.Join(r.Registered, ", "))
	if r.OtherFiles > 0 {
		noun := "files stay"
		if r.OtherFiles == 1 {
			noun = "file stays"
		}
		fmt.Fprintf(&b, "%d other %s in the folder but won't be listed as artifacts.\n", r.OtherFiles, noun)
	}
	if len(r.OtherRootFiles) > 0 {
		shown := r.OtherRootFiles[:min(len(r.OtherRootFiles), maxListedRootFiles)]
		names := strings.Join(shown, ", ")
		if extra := len(r.OtherRootFiles) - len(shown); extra > 0 {
			names = fmt.Sprintf("%s and %d more", names, extra)
		}
		fmt.Fprintf(&b, "Not listed at the folder root: %s. Run ao artifact add on any that are deliverables.\n", names)
	}
	return b.String()
}
