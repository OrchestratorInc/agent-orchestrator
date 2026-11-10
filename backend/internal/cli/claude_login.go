package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
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
		return c.saveClaudeSetupToken(ctx, in, out, style)
	default:
		return usageError{fmt.Errorf("login method must be 1, 2, 3, or 4")}
	}
	if err := c.deps.RunInteractiveCommand(ctx, claude, args, in, out, stderr); err != nil {
		return fmt.Errorf("claude login failed: %w", err)
	}
	// The newest sign-in wins: a setup token saved earlier would otherwise keep
	// signing sessions in over the login the user just completed. The sign-in
	// itself already succeeded, so failing to clear the token is only a warning.
	if err := clearClaudeSetupToken(); err != nil {
		if _, writeErr := fmt.Fprintf(stderr, "Warning: %v; Orchestrator-managed sessions may keep using the earlier setup token.\n", err); writeErr != nil {
			return writeErr
		}
	}
	// Likewise an Anthropic API key saved from Claude Code's AO panel, the
	// same release the daemon's own sign-ins do. The key stays saved for
	// other harnesses.
	if _, err := releaseClaudeAPIKey(ctx); err != nil {
		if _, writeErr := fmt.Fprintf(stderr, "Warning: %v; AO sessions may keep using the saved Anthropic API key.\n", err); writeErr != nil {
			return writeErr
		}
	}
	_, err = fmt.Fprintf(out, "\n%s\n", style.success("Claude Code sign-in complete."))
	return err
}

func clearClaudeSetupToken() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("resolve Orchestrator data directory to clear the saved setup token: %w", err)
	}
	return claudecode.RemoveLocalOAuthToken(cfg.DataDir)
}

// releaseClaudeAPIKey stops AO passing Claude Code the ANTHROPIC_API_KEY
// saved from its own panel, which would otherwise override the sign-in just
// completed. It returns the variable released ("" when none).
func releaseClaudeAPIKey(ctx context.Context) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("resolve AO data directory to switch off the saved Anthropic API key: %w", err)
	}
	released, err := providerkeys.ReleaseHarness(ctx, cfg.DataDir, "claude-code")
	if err != nil {
		return "", fmt.Errorf("switch off the saved Anthropic API key: %w", err)
	}
	return released, nil
}

func (c *commandContext) saveClaudeSetupToken(ctx context.Context, in io.Reader, out io.Writer, style codexLoginStyle) error {
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
		return fmt.Errorf("resolve Orchestrator data directory: %w", err)
	}
	// Claude Code's saved ANTHROPIC_API_KEY would keep winning over the token.
	// Release it first and give it back if the token cannot be written, as the
	// daemon does, so a failure leaves the previous sign-in in place.
	released, err := providerkeys.ReleaseHarness(ctx, cfg.DataDir, "claude-code")
	if err != nil {
		return fmt.Errorf("switch off the saved Anthropic API key, so the setup token was not saved: %w", err)
	}
	if err := claudecode.SaveLocalOAuthToken(cfg.DataDir, secret); err != nil {
		if released != "" {
			if restoreErr := providerkeys.ClaimForHarness(ctx, cfg.DataDir, released, "claude-code"); restoreErr != nil {
				return fmt.Errorf("%w; the saved Anthropic API key could not be switched back on either: %w", err, restoreErr)
			}
		}
		return err
	}
	if _, err := fmt.Fprintln(out, style.success("Claude Code will sign in to local sessions with this setup token.")); err != nil {
		return err
	}
	// Name the file so a data directory other than the daemon's is visible.
	_, err = fmt.Fprintln(out, style.dim("Saved to "+claudecode.LocalOAuthTokenPath(cfg.DataDir)))
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
		fmt.Sprintf("  %s  %s %s", style.accent("4"), style.bold("Setup token"), style.dim("· Token from claude setup-token")),
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
