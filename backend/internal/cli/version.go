package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func VersionString() string {
	parts := []string{Version}
	if Commit != "" {
		parts = append(parts, "commit "+Commit)
	}
	if Date != "" {
		parts = append(parts, "built "+Date)
	}
	return strings.Join(parts, " ")
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), VersionString())
			return err
		},
	}
}
