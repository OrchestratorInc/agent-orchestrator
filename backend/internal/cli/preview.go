package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// previewAPIRequest mirrors the daemon's body for
// POST /api/v1/sessions/{id}/preview. An empty Url asks the daemon to
// autodetect a static entry in the workspace. The CLI keeps its own copy so it
// need not import httpd.
type previewAPIRequest struct {
	Url string `json:"url"`
}

type previewServerStartRequest struct {
	Configuration string                   `json:"configuration,omitempty"`
	Run           *previewInlineRunRequest `json:"run,omitempty"`
}

// previewInlineRunRequest mirrors controllers.InlinePreviewServerRequest for
// `ao preview start --run -- <command>`.
type previewInlineRunRequest struct {
	RuntimeExecutable string   `json:"runtimeExecutable"`
	RuntimeArgs       []string `json:"runtimeArgs,omitempty"`
	Cwd               string   `json:"cwd,omitempty"`
	Port              int      `json:"port,omitempty"`
	AutoPort          bool     `json:"autoPort,omitempty"`
	URL               string   `json:"url,omitempty"`
	TargetKind        string   `json:"targetKind,omitempty"`
}

const previewCapabilityHeader = "X-AO-Preview-Capability"

type previewServerStatusDTO struct {
	SessionID     string    `json:"sessionId"`
	State         string    `json:"state"`
	Configuration string    `json:"configuration,omitempty"`
	TargetKind    string    `json:"targetKind,omitempty"`
	URL           string    `json:"url,omitempty"`
	Port          int       `json:"port,omitempty"`
	StartedAt     time.Time `json:"startedAt,omitempty"`
	Error         string    `json:"error,omitempty"`
	Logs          []string  `json:"logs"`
}

func newPreviewCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview [url]",
		Short: "Open a URL (or the workspace's index.html) in the desktop browser panel for the current session",
		Long: "Open a URL in the desktop browser panel for the current session.\n\n" +
			"With no argument it opens the workspace's static entry point, falling\n" +
			"back to this session's existing preview target when no entry point exists.\n" +
			"A workspace-root-relative Markdown or HTML path opens through AO's isolated\n" +
			"file preview. Use `ao preview start` for a configured dev server and\n" +
			"`ao preview clear` to empty the panel.",
		Example: `  ao preview
  ao preview README.md
  ao preview http://localhost:5173
  ao preview start
  ao preview start web
  ao preview status
  ao preview stop
  ao preview clear`,
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			var target string
			if len(args) == 1 {
				target = args[0]
			}
			return ctx.openPreview(cmd.Context(), target)
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Clear the desktop browser panel for the current session",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.clearPreview(cmd.Context())
		},
	})
	var startJSON bool
	var run previewRunOptions
	startCmd := &cobra.Command{
		Use:   "start [configuration] | start --run [flags] -- <command> [args...]",
		Short: "Start a session-owned dev server from .ao/launch.json or a one-off command and open its preview",
		Long: "Start a session-owned dev server and open its preview in this worker's Browser panel.\n\n" +
			"With a configuration name (or no name when launch.json has exactly one entry),\n" +
			"the server comes from .ao/launch.json. With --run, the command after -- is run\n" +
			"as a one-off managed server. Nothing is written to the workspace.",
		Example: `  ao preview start
  ao preview start web
  ao preview start --run --port 3000 --url 'http://127.0.0.1:${PORT}/' -- npx next dev -p '${PORT}'
  ao preview start --run --auto-port -- npm run dev -- --port '${PORT}' --host 127.0.0.1`,
		Args: func(cmd *cobra.Command, args []string) error {
			if run.enabled {
				if cmd.ArgsLenAtDash() < 0 || len(args) == 0 {
					return usageError{errors.New("ao preview start --run requires a command after --")}
				}
				return nil
			}
			return atMostOneArg(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if run.enabled {
				if cmd.ArgsLenAtDash() > 0 {
					return usageError{errors.New("--run takes no configuration name; put the command after --")}
				}
				request, err := run.request(cmd, args)
				if err != nil {
					return err
				}
				status, err := ctx.startInlinePreviewServer(cmd.Context(), request)
				if err != nil {
					return err
				}
				return writePreviewServerStatus(cmd.OutOrStdout(), status, startJSON)
			}
			if err := run.rejectFlagsWithoutRun(cmd); err != nil {
				return err
			}
			configuration := ""
			if len(args) == 1 {
				configuration = args[0]
			}
			status, err := ctx.startPreviewServer(cmd.Context(), configuration)
			if err != nil {
				return err
			}
			return writePreviewServerStatus(cmd.OutOrStdout(), status, startJSON)
		},
	}
	startCmd.Flags().BoolVar(&startJSON, "json", false, "print JSON")
	run.bind(startCmd.Flags())
	cmd.AddCommand(startCmd)

	var statusJSON bool
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show this session's managed preview server status and recent logs",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := ctx.previewServerStatus(cmd.Context())
			if err != nil {
				return err
			}
			return writePreviewServerStatus(cmd.OutOrStdout(), status, statusJSON)
		},
	}
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "print JSON")
	cmd.AddCommand(statusCmd)

	var stopJSON bool
	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop this session's managed preview server",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := ctx.stopPreviewServer(cmd.Context())
			if err != nil {
				return err
			}
			return writePreviewServerStatus(cmd.OutOrStdout(), status, stopJSON)
		},
	}
	stopCmd.Flags().BoolVar(&stopJSON, "json", false, "print JSON")
	cmd.AddCommand(stopCmd)
	return cmd
}

func (c *commandContext) openPreview(ctx context.Context, target string) error {
	path, err := sessionPreviewPath()
	if err != nil {
		return err
	}
	return c.postJSON(ctx, path, previewAPIRequest{Url: target}, nil)
}

// clearPreview empties the desktop browser panel for the current session
// (`ao preview clear`) by deleting the session's stored preview target.
func (c *commandContext) clearPreview(ctx context.Context) error {
	path, err := sessionPreviewPath()
	if err != nil {
		return err
	}
	return c.deleteJSON(ctx, path, nil)
}

func sessionPreviewPath() (string, error) {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return "", usageError{errors.New("ao preview must run inside an AO session (AO_SESSION_ID is not set)")}
	}
	// PathEscape: session ids are already "-"/digit safe, but keep the URL
	// well-formed regardless.
	return "sessions/" + url.PathEscape(sessionID) + "/preview", nil
}

func previewServerPath() (string, error) {
	path, err := sessionPreviewPath()
	if err != nil {
		return "", err
	}
	return path + "/server", nil
}

func (c *commandContext) startPreviewServer(
	ctx context.Context,
	configuration string,
) (previewServerStatusDTO, error) {
	path, err := previewServerPath()
	if err != nil {
		return previewServerStatusDTO{}, err
	}
	var out previewServerStatusDTO
	headers, err := previewServerHeaders()
	if err != nil {
		return previewServerStatusDTO{}, err
	}
	err = c.doJSONPathWithHeaders(
		ctx,
		http.MethodPost,
		"/api/v1/"+path,
		previewServerStartRequest{Configuration: configuration},
		&out,
		headers,
	)
	return out, err
}

// previewRunOptions holds the flags for `ao preview start --run`.
type previewRunOptions struct {
	enabled bool
	url     string
	port    int
	auto    bool
	target  string
	cwd     string
}

func (o *previewRunOptions) bind(flags *pflag.FlagSet) {
	flags.BoolVar(&o.enabled, "run", false, "run the command after -- as a one-off managed server (not written to .ao/launch.json)")
	flags.StringVar(&o.url, "url", "", "loopback http URL that becomes ready; ${PORT} is expanded (default http://127.0.0.1:${PORT}/)")
	flags.IntVar(&o.port, "port", 0, "preferred loopback port; omit with --auto-port to let AO choose")
	flags.BoolVar(&o.auto, "auto-port", false, "choose a free loopback port automatically (default when --port is omitted)")
	flags.StringVar(&o.target, "target", "", "app (open in the Browser panel, default) or api (health-check only)")
	flags.StringVar(&o.cwd, "cwd", "", "workspace-relative working directory (default: workspace root)")
}

// rejectFlagsWithoutRun keeps run-only flags from being silently ignored when
// the command starts a configured server.
func (o *previewRunOptions) rejectFlagsWithoutRun(cmd *cobra.Command) error {
	for _, name := range []string{"url", "port", "auto-port", "target", "cwd"} {
		if cmd.Flags().Changed(name) {
			return usageError{fmt.Errorf("--%s requires --run", name)}
		}
	}
	return nil
}

func (o *previewRunOptions) request(cmd *cobra.Command, args []string) (previewInlineRunRequest, error) {
	if o.port < 0 || o.port > 65535 {
		return previewInlineRunRequest{}, usageError{errors.New("--port must be between 0 and 65535")}
	}
	if o.port > 0 && o.auto {
		return previewInlineRunRequest{}, usageError{errors.New("--port and --auto-port are mutually exclusive")}
	}
	dash := cmd.ArgsLenAtDash()
	command := args
	if dash >= 0 {
		command = args[dash:]
	}
	if len(command) == 0 {
		return previewInlineRunRequest{}, usageError{errors.New("ao preview start --run requires a command after --")}
	}
	return previewInlineRunRequest{
		RuntimeExecutable: command[0],
		RuntimeArgs:       command[1:],
		Cwd:               o.cwd,
		Port:              o.port,
		AutoPort:          o.port == 0,
		URL:               o.url,
		TargetKind:        o.target,
	}, nil
}

func (c *commandContext) startInlinePreviewServer(ctx context.Context, run previewInlineRunRequest) (previewServerStatusDTO, error) {
	path, err := previewServerPath()
	if err != nil {
		return previewServerStatusDTO{}, err
	}
	var out previewServerStatusDTO
	headers, err := previewServerHeaders()
	if err != nil {
		return previewServerStatusDTO{}, err
	}
	err = c.doJSONPathWithHeaders(
		ctx,
		http.MethodPost,
		"/api/v1/"+path,
		previewServerStartRequest{Run: &run},
		&out,
		headers,
	)
	return out, err
}

func (c *commandContext) previewServerStatus(ctx context.Context) (previewServerStatusDTO, error) {
	path, err := previewServerPath()
	if err != nil {
		return previewServerStatusDTO{}, err
	}
	var out previewServerStatusDTO
	headers, err := previewServerHeaders()
	if err != nil {
		return previewServerStatusDTO{}, err
	}
	err = c.doJSONPathWithHeaders(ctx, http.MethodGet, "/api/v1/"+path, nil, &out, headers)
	return out, err
}

func (c *commandContext) stopPreviewServer(ctx context.Context) (previewServerStatusDTO, error) {
	path, err := previewServerPath()
	if err != nil {
		return previewServerStatusDTO{}, err
	}
	var out previewServerStatusDTO
	headers, err := previewServerHeaders()
	if err != nil {
		return previewServerStatusDTO{}, err
	}
	err = c.doJSONPathWithHeaders(ctx, http.MethodDelete, "/api/v1/"+path, nil, &out, headers)
	return out, err
}

func previewServerHeaders() (map[string]string, error) {
	capability := strings.TrimSpace(os.Getenv("AO_BROWSER_CAPABILITY"))
	if capability != "" {
		return map[string]string{browserCapabilityHeader: capability}, nil
	}
	capability = strings.TrimSpace(os.Getenv("AO_PREVIEW_CAPABILITY"))
	if capability != "" {
		return map[string]string{previewCapabilityHeader: capability}, nil
	}
	return nil, usageError{errors.New("ao preview server commands require AO_BROWSER_CAPABILITY or AO_PREVIEW_CAPABILITY")}
}

func writePreviewServerStatus(out io.Writer, status previewServerStatusDTO, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(out, status)
	}
	summary := status.State
	if status.Configuration != "" {
		summary += " " + status.Configuration
	}
	if status.URL != "" {
		summary += " " + status.URL
	}
	if _, err := fmt.Fprintln(out, summary); err != nil {
		return err
	}
	if status.Error != "" {
		if _, err := fmt.Fprintln(out, "Error:", status.Error); err != nil {
			return err
		}
	}
	for _, line := range status.Logs {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}
