package runner

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

// Version is supplied by the build for daemon compatibility checks.
var Version = "dev"

// UpstreamVersion identifies the embedded engine contract.
const UpstreamVersion = "v7.3.8"

// RunCLI separates usage errors from runtime failure exit codes.
func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: ao-accounts-manager <serve|version>")
		return 2
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			_, _ = fmt.Fprintln(stderr, "version does not accept arguments")
			return 2
		}
		_, _ = fmt.Fprintf(stdout, "ao-accounts-manager %s (CLIProxyAPI %s)\n", Version, UpstreamVersion)
		return 0
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		flags.SetOutput(stderr)
		stateDir := flags.String("state-dir", "", "absolute AO Accounts Manager state directory")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if *stateDir == "" || !filepath.IsAbs(*stateDir) || flags.NArg() != 0 {
			_, _ = fmt.Fprintln(stderr, "serve requires --state-dir with an absolute path")
			return 2
		}
		if err := Serve(ctx, *stateDir); err != nil {
			_, _ = fmt.Fprintf(stderr, "accounts manager stopped: %v\n", err)
			return 1
		}
		return 0
	case "_codex-device-login":
		flags := flag.NewFlagSet("_codex-device-login", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		stateDir := flags.String("state-dir", "", "")
		if err := flags.Parse(args[1:]); err != nil || *stateDir == "" || !filepath.IsAbs(*stateDir) || flags.NArg() != 0 {
			return 2
		}
		if err := runCodexDeviceLogin(ctx, *stateDir, stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, "Codex device sign-in failed")
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}
