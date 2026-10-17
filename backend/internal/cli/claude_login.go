package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

var claudeSetupTokenPattern = regexp.MustCompile(`^sk-ant-oat[0-9A-Za-z_-]{10,}$`)

// newClaudeLoginCommand is an internal, trusted terminal entry point that
// exposes every login mode supported by Claude Code without accepting raw
// commands or credentials from the renderer.
func newClaudeLoginCommand(ctx *commandContext) *cobra.Command {
	var executable string
	cmd := &cobra.Command{
		Use:    "claude-login",
		Short:  "Sign in to Claude Code (internal)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runClaudeLogin(cmd.Context(), executable, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&executable, "executable", "", "resolved Claude executable (internal)")
	_ = cmd.Flags().MarkHidden("executable")
	return cmd
}

func (c *commandContext) runClaudeLogin(ctx context.Context, executable string, in io.Reader, out, stderr io.Writer) error {
	claude, err := resolveLoginExecutable(executable, "claude", c.deps.LookPath)
	if err != nil {
		return err
	}
	style := newCodexLoginStyle(out)
	if err := writeClaudeLoginMenu(out, style); err != nil {
		return err
	}
	selection, err := readCodexLoginSelection(in)
	if err != nil {
		return fmt.Errorf("read login method: %w", err)
	}

	args := []string{"auth", "login"}
	switch strings.TrimSpace(selection) {
	case "1":
		args = append(args, "--claudeai")
	case "2":
		args = append(args, "--console")
	case "3":
		args = append(args, "--sso")
	case "4":
		return c.saveClaudeSetupToken(in, out, style)
	default:
		return usageError{fmt.Errorf("login method must be 1, 2, 3, or 4")}
	}
	if err := c.deps.RunInteractiveCommand(ctx, claude, args, in, out, stderr); err != nil {
		return fmt.Errorf("claude login failed: %w", err)
	}
	_, err = fmt.Fprintf(out, "\n%s\n", style.success("Claude Code sign-in complete."))
	return err
}

func (c *commandContext) saveClaudeSetupToken(in io.Reader, out io.Writer, style codexLoginStyle) error {
	if _, err := fmt.Fprintf(out, "\nRun claude setup-token in another terminal, then paste its token here.\n%s: ", style.bold("Setup token")); err != nil {
		return err
	}
	secret, err := c.deps.ReadSecret(in)
	if _, writeErr := fmt.Fprintln(out); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("read setup token: %w", err)
	}
	defer zeroBytes(secret)
	secret = bytes.TrimSpace(secret)
	if !claudeSetupTokenPattern.Match(secret) {
		return usageError{fmt.Errorf("setup token must start with sk-ant-oat and contain no spaces")}
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("resolve AO data directory: %w", err)
	}
	dir := filepath.Join(cfg.DataDir, "harnesses", "claude-code")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create Claude credential directory: %w", err)
	}
	path := filepath.Join(dir, "oauth-token")
	if err := os.WriteFile(path, secret, 0o600); err != nil {
		return fmt.Errorf("save Claude setup token: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect Claude setup token: %w", err)
	}
	_, err = fmt.Fprintln(out, style.success("Claude setup token saved for local sessions."))
	return err
}

func writeClaudeLoginMenu(out io.Writer, style codexLoginStyle) error {
	lines := []string{
		style.accent("Sign in to Claude Code"),
		style.dim("Choose how you want to authenticate."),
		"",
		fmt.Sprintf("  %s  %s  %s", style.accent("1"), style.bold("Claude subscription"), style.success("Recommended")),
		fmt.Sprintf("  %s  %s %s", style.accent("2"), style.bold("Anthropic Console"), style.dim("· API usage billing")),
		fmt.Sprintf("  %s  %s %s", style.accent("3"), style.bold("SSO"), style.dim("· Organization sign-in")),
		fmt.Sprintf("  %s  %s", style.accent("4"), style.bold("Setup token")),
		"",
		style.bold("Enter 1-4 and press Return"),
		style.dim("Ctrl+C to cancel"),
	}
	if _, err := fmt.Fprintln(out, strings.Join(lines, "\n")); err != nil {
		return err
	}
	_, err := fmt.Fprint(out, style.accent("Selection [1-4]: "))
	return err
}
