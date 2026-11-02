package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserchrome"
)

type browserChromeChannelDTO struct {
	Name         string `json:"name"`
	Available    bool   `json:"available"`
	Registered   bool   `json:"registered"`
	ManifestPath string `json:"manifestPath"`
}

type browserChromeStatusDTO struct {
	Enabled          bool                      `json:"enabled"`
	Connected        bool                      `json:"connected"`
	ExtensionID      string                    `json:"extensionId"`
	ExtensionPath    string                    `json:"extensionPath"`
	ExtensionBundled bool                      `json:"extensionBundled"`
	ExtensionVersion string                    `json:"extensionVersion,omitempty"`
	ClientVersion    string                    `json:"clientVersion,omitempty"`
	HostPath         string                    `json:"hostPath"`
	Channels         []browserChromeChannelDTO `json:"channels"`
}

// newBrowserTargetCommand switches the current session between AO's Browser
// panel and the user's own Chrome.
func newBrowserTargetCommand(ctx *commandContext, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "target [panel|chrome]",
		Short: "Show or choose which browser this session drives: AO's Browser panel or your Chrome",
		Long: "Without an argument, print this session's browser target.\n\n" +
			"panel   AO's Browser panel (the default).\n" +
			"chrome  Your own Google Chrome through the AO Browser Bridge extension. The first\n" +
			"        command asks you in Chrome to allow this session; the session then works\n" +
			"        only in its own AO tab group.",
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			actionArgs := map[string]any{}
			if len(args) == 1 {
				actionArgs["target"] = args[0]
			}
			resp, err := ctx.browserAction(cmd.Context(), "target", actionArgs)
			if err != nil {
				return err
			}
			if *jsonOutput {
				return writeJSON(cmd.OutOrStdout(), resp)
			}
			target, _ := resp.Result["target"].(string)
			connected, _ := resp.Result["connected"].(bool)
			state := "not connected"
			if connected {
				state = "connected"
			}
			label := "AO Browser panel"
			if target == "chrome" {
				label = "your Chrome (AO Browser Bridge)"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Browser target: %s, %s\n", label, state)
			if err == nil && target == "chrome" && !connected {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Chrome is not connected. Ask the user to run `ao browser chrome setup` and load the extension.")
			}
			return err
		},
	}
}

// newBrowserChromeCommand manages Chrome mode for this machine.
func newBrowserChromeCommand(ctx *commandContext, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chrome",
		Short: "Set up, inspect, or remove Chrome mode (agents in your own Chrome)",
		Args:  noArgs,
	}
	run := func(method string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			var status browserChromeStatusDTO
			if err := ctx.doJSONPathWithHeaders(cmd.Context(), method, "/api/v1/browser/chrome", nil, &status, nil); err != nil {
				return err
			}
			if *jsonOutput {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			return writeBrowserChromeStatus(cmd.OutOrStdout(), status, method)
		}
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show whether Chrome mode is set up and the extension is connected",
		Args:  noArgs,
		RunE:  run(http.MethodGet),
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "setup",
		Short: "Install the AO Browser Bridge extension files and register AO with Chrome",
		Long: "Installs the unpacked AO Browser Bridge extension under the AO data directory and\n" +
			"registers AO's native messaging host with each Chrome channel in use. This writes\n" +
			"one small manifest file in Chrome's NativeMessagingHosts directory (on Windows, a\n" +
			"registry value). Then load the extension in chrome://extensions.",
		Args: noArgs,
		RunE: run(http.MethodPost),
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove",
		Short: "Unregister AO from Chrome and delete the installed extension files",
		Args:  noArgs,
		RunE:  run(http.MethodDelete),
	})
	return cmd
}

func writeBrowserChromeStatus(out io.Writer, status browserChromeStatusDTO, method string) error {
	var b strings.Builder
	switch {
	case method == http.MethodDelete:
		b.WriteString("Chrome mode removed. Also remove \"AO Browser Bridge\" in chrome://extensions.\n")
	case status.Enabled:
		b.WriteString("Chrome mode: set up\n")
	default:
		b.WriteString("Chrome mode: not set up (run `ao browser chrome setup`)\n")
	}
	if status.Connected {
		fmt.Fprintf(&b, "Extension: connected (version %s)\n", status.ClientVersion)
	} else if status.Enabled {
		b.WriteString("Extension: not connected\n")
	}
	for _, channel := range status.Channels {
		state := "not installed"
		switch {
		case channel.Registered:
			state = "registered"
		case channel.Available:
			state = "not registered"
		}
		fmt.Fprintf(&b, "  %s: %s\n", channel.Name, state)
	}
	if method != http.MethodDelete && status.Enabled {
		if !status.ExtensionBundled {
			b.WriteString("The extension bundle is missing from this AO build.\n")
		} else if !status.Connected {
			fmt.Fprintf(&b, "\nTo finish in Chrome:\n"+
				"  1. Open chrome://extensions and turn on Developer mode.\n"+
				"  2. Click \"Load unpacked\" and choose:\n     %s\n"+
				"  3. Check the extension ID is %s.\n", status.ExtensionPath, status.ExtensionID)
		}
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// newBrowserHostCommand is the Chrome native messaging host. Chrome starts it
// through the wrapper written by `ao browser chrome setup` and talks to it on
// stdin/stdout; it is not meant to be run by hand.
func newBrowserHostCommand() *cobra.Command {
	var endpoint string
	cmd := &cobra.Command{
		Use:    "browser-host",
		Short:  "Chrome native messaging host for the AO Browser Bridge (internal)",
		Hidden: true,
		// Chrome appends the caller origin (and --parent-window on Windows).
		Args:               cobra.ArbitraryArgs,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if endpoint == "" {
				return usageError{errors.New("--endpoint is required")}
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			err := browserchrome.RunHost(ctx, os.Stdin, os.Stdout, browserchrome.HostOptions{
				EndpointPath: endpoint,
				Dial:         browserchrome.DialRuntime,
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				// Chrome shows the host's stderr in its own log only.
				fmt.Fprintln(os.Stderr, "ao browser-host:", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "path to the daemon endpoint file")
	return cmd
}
