package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// cuaStatus mirrors the daemon's ComputerUseStatus.
type cuaStatus struct {
	Platform        string          `json:"platform"`
	Installed       bool            `json:"installed"`
	BinaryPath      string          `json:"binaryPath,omitempty"`
	Version         string          `json:"version,omitempty"`
	LatestVersion   string          `json:"latestVersion,omitempty"`
	UpdateAvailable bool            `json:"updateAvailable"`
	ReleaseNotesURL string          `json:"releaseNotesUrl,omitempty"`
	Daemon          string          `json:"daemon"`
	Permissions     *cuaPermissions `json:"permissions,omitempty"`
	ImageDirectory  string          `json:"imageDirectory"`
}

type cuaPermissions struct {
	Accessibility   string `json:"accessibility"`
	ScreenRecording string `json:"screenRecording"`
	Granting        bool   `json:"granting"`
	GrantError      string `json:"grantError,omitempty"`
}

type cuaTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type cuaToolsResponse struct {
	Tools []cuaTool `json:"tools"`
}

type cuaToolDescription struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type cuaCallRequest struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
}

type cuaImage struct {
	Path     string `json:"path"`
	MimeType string `json:"mimeType"`
}

type cuaCallResponse struct {
	Tool   string     `json:"tool"`
	Result any        `json:"result"`
	Images []cuaImage `json:"images"`
}

func newCuaCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cua",
		Short: "Drive desktop apps through Cua Driver; screenshots are saved to files",
		Long: "Computer use through Cua Driver (https://cua.ai). Every call goes through the AO daemon,\n" +
			"which saves screenshots to files and prints their paths instead of inline base64.\n" +
			"Run `ao cua guide` for the full how-to before driving an app.\n" +
			"Install Cua Driver in Settings > Computer use agents.",
	}
	cmd.AddCommand(newCuaGuideCommand(), newCuaStatusCommand(ctx), newCuaStartCommand(ctx), newCuaToolsCommand(ctx), newCuaDescribeCommand(ctx), newCuaCallCommand(ctx))
	return cmd
}

// newCuaGuideCommand prints the computer-use how-to agents follow. It is the
// skill's commands/cua.md, embedded in the binary, so it needs no daemon.
func newCuaGuideCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "guide",
		Short: "Print the computer-use how-to: workflow, tips, safety, and errors",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			guide, err := skillassets.CommandGuide("cua")
			if err != nil {
				return err
			}
			_, err = io.WriteString(cmd.OutOrStdout(), guide)
			return err
		},
	}
}

func newCuaStatusCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether Cua Driver is installed, running, and permitted",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var status cuaStatus
			if err := ctx.getJSON(cmd.Context(), "computer-use/status", &status); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			return writeCuaStatus(cmd.OutOrStdout(), status)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output status as JSON")
	return cmd
}

func newCuaStartCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start Cua Driver's background service",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var status cuaStatus
			if err := ctx.postJSON(cmd.Context(), "computer-use/daemon/start", nil, &status); err != nil {
				return err
			}
			return writeCuaStatus(cmd.OutOrStdout(), status)
		},
	}
}

func newCuaToolsCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "List Cua Driver's tools",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var response cuaToolsResponse
			if err := ctx.getJSON(cmd.Context(), "computer-use/tools", &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response.Tools)
			}
			for _, tool := range response.Tools {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", tool.Name, tool.Description); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output tools as JSON")
	return cmd
}

func newCuaDescribeCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "describe <tool>",
		Short: "Show one tool's description and JSON input schema",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var response cuaToolDescription
			if err := ctx.getJSON(cmd.Context(), "computer-use/tools/"+url.PathEscape(args[0]), &response); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), response.Description)
			return err
		},
	}
}

func newCuaCallCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "call <tool> [json-arguments|-]",
		Short: "Run one Cua Driver tool; prints its result with screenshot file paths",
		Long: "Runs one Cua Driver tool. Window reads, actions, and run_steps print the way Cua Driver's\n" +
			"MCP server shows them: the window line, the tree or the diff since the last read, and the\n" +
			"screenshot's file path where MCP shows the image. Other tools print JSON; --json prints\n" +
			"the raw result for every tool.",
		Example: "  ao cua call list_windows\n" +
			"  ao cua call get_window_state '{\"pid\":123,\"window_id\":456}'\n" +
			"  echo '{\"element_token\":\"s00000019:12\"}' | ao cua call click -",
		Args: rangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments, err := cuaArguments(cmd.InOrStdin(), args[1:])
			if err != nil {
				return err
			}
			request := cuaCallRequest{Tool: args[0], Arguments: arguments, SessionID: strings.TrimSpace(os.Getenv("AO_SESSION_ID"))}
			var response cuaCallResponse
			if err := ctx.postJSON(cmd.Context(), "computer-use/call", request, &response); err != nil {
				return err
			}
			if !jsonOutput {
				if text, ok := renderCuaCall(response); ok {
					_, err := io.WriteString(cmd.OutOrStdout(), text)
					return err
				}
			}
			if err := writeJSON(cmd.OutOrStdout(), response); err != nil {
				return err
			}
			// The hint goes to stderr so stdout stays one JSON document.
			for _, image := range response.Images {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "screenshot saved: %s (open it with your image viewer)\n", image.Path); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print the raw JSON result")
	return cmd
}

// cuaArguments reads a call's JSON object from the argument, or stdin for "-".
func cuaArguments(stdin io.Reader, args []string) (map[string]any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	raw := []byte(args[0])
	if args[0] == "-" {
		var err error
		if raw, err = io.ReadAll(stdin); err != nil {
			return nil, fmt.Errorf("read arguments from stdin: %w", err)
		}
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, nil
	}
	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err != nil || arguments == nil {
		return nil, usageError{errors.New("usage: tool arguments must be a JSON object, e.g. '{\"pid\":123}'")}
	}
	return arguments, nil
}

func writeCuaStatus(w io.Writer, status cuaStatus) error {
	if !status.Installed {
		_, err := fmt.Fprintln(w, "Cua Driver is not installed. Install it in Settings > Computer use agents.")
		return err
	}
	version := status.Version
	if version == "" {
		version = "unknown version"
	}
	if status.UpdateAvailable && status.LatestVersion != "" {
		version += " (" + status.LatestVersion + " available)"
	}
	lines := []string{
		"Cua Driver " + version,
		"  executable:   " + status.BinaryPath,
		"  service:      " + status.Daemon,
	}
	if status.Permissions != nil {
		lines = append(lines,
			"  accessibility:    "+status.Permissions.Accessibility,
			"  screen recording: "+status.Permissions.ScreenRecording)
	}
	lines = append(lines, "  screenshots:  "+status.ImageDirectory)
	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}
