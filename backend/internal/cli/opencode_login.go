package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
)

// claudePlanWarmupAttempts bounds the OpenCode 2 plugin preload. Its first
// activation downloads the plugin and can outlast OpenCode's activation wait;
// later starts load it from the cache.
const claudePlanWarmupAttempts = 3

// newOpenCodeLoginCommand is an internal, trusted terminal entry point for
// OpenCode 1 and 2. It offers OpenCode's own provider chooser, or a Claude
// Pro/Max plan through the pinned subscription plugin, which OpenCode does not
// offer natively.
func newOpenCodeLoginCommand(ctx *commandContext) *cobra.Command {
	var executable string
	var major int
	cmd := &cobra.Command{
		Use:    "opencode-login",
		Short:  "Sign in to OpenCode (internal)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runOpenCodeLogin(cmd.Context(), executable, major, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&executable, "executable", "", "resolved OpenCode executable (internal)")
	cmd.Flags().IntVar(&major, "major", 1, "OpenCode major version (internal)")
	_ = cmd.Flags().MarkHidden("executable")
	_ = cmd.Flags().MarkHidden("major")
	return cmd
}

func (c *commandContext) runOpenCodeLogin(ctx context.Context, executable string, major int, in io.Reader, out, stderr io.Writer) error {
	if major != 1 && major != 2 {
		return usageError{fmt.Errorf("OpenCode major version must be 1 or 2")}
	}
	binary, err := resolveLoginExecutable(executable, "opencode", c.deps.LookPath)
	if err != nil {
		return err
	}
	style := newCodexLoginStyle(out)
	if err := writeOpenCodeLoginMenu(out, style, major); err != nil {
		return err
	}
	selection, err := readCodexLoginSelection(in)
	if err != nil {
		return fmt.Errorf("read login method: %w", err)
	}

	switch strings.TrimSpace(selection) {
	case "1":
		if err := c.deps.RunInteractiveCommand(ctx, binary, []string{"auth", "login"}, in, out, stderr); err != nil {
			return fmt.Errorf("opencode login failed: %w", err)
		}
	case "2":
		if err := c.runOpenCodeClaudePlanLogin(ctx, binary, major, in, out, stderr, style); err != nil {
			return err
		}
	default:
		return usageError{fmt.Errorf("login method must be 1 or 2")}
	}
	_, err = fmt.Fprintf(out, "\n%s\n", style.success("OpenCode sign-in complete."))
	return err
}

// runOpenCodeClaudePlanLogin loads the pinned plugin for this login only, via
// the inline config layer, so the user's config files are never edited. AO
// sessions load the same plugin again once they see the stored credential.
func (c *commandContext) runOpenCodeClaudePlanLogin(ctx context.Context, binary string, major int, in io.Reader, out, stderr io.Writer, style codexLoginStyle) error {
	content, err := opencode.AddConfigPlugins(os.Getenv("OPENCODE_CONFIG_CONTENT"), opencode.ClaudePlanPluginKey(major), []string{opencode.ClaudePlanPlugin(major)})
	if err != nil {
		return err
	}
	if err := os.Setenv("OPENCODE_CONFIG_CONTENT", content); err != nil {
		return fmt.Errorf("prepare Claude plan login: %w", err)
	}
	if _, err := fmt.Fprintf(out, "\n%s\n", style.dim("Uses the Claude Code login on this device. Sign in to Claude Code first if you have not.")); err != nil {
		return err
	}
	args := []string{"auth", "login", "-p", "anthropic"}
	if major == 2 {
		if _, err := fmt.Fprintln(out, style.dim("Preparing the Claude plan plugin…")); err != nil {
			return err
		}
		for attempt := 0; attempt < claudePlanWarmupAttempts; attempt++ {
			if c.deps.RunInteractiveCommand(ctx, binary, []string{"auth", "list", "--standalone", "--format", "json"}, strings.NewReader(""), io.Discard, io.Discard) == nil {
				break
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		// --standalone keeps a running background service, which never saw this
		// login's plugin, from handling the request.
		args = []string{"auth", "login", "anthropic", "--standalone", "--method", opencode.ClaudePlanMethodV2}
	}
	if err := c.deps.RunInteractiveCommand(ctx, binary, args, in, out, stderr); err != nil {
		return fmt.Errorf("opencode Claude plan login failed: %w", err)
	}
	return nil
}

func writeOpenCodeLoginMenu(out io.Writer, style codexLoginStyle, major int) error {
	title := "Sign in to OpenCode"
	if major == 2 {
		title = "Sign in to OpenCode 2"
	}
	lines := []string{
		style.accent(title),
		style.dim("Choose how you want to authenticate."),
		"",
		fmt.Sprintf("  %s  %s %s", style.accent("1"), style.bold("Choose a provider"), style.dim("· OpenCode's own provider list")),
		fmt.Sprintf("  %s  %s %s", style.accent("2"), style.bold("Claude Pro/Max plan"), style.dim("· Imports your Claude Code login (community plugin)")),
		"",
		style.bold("Enter 1-2 and press Return"),
		style.dim("Ctrl+C to cancel"),
	}
	if _, err := fmt.Fprintln(out, strings.Join(lines, "\n")); err != nil {
		return err
	}
	_, err := fmt.Fprint(out, style.accent("Selection [1-2]: "))
	return err
}
