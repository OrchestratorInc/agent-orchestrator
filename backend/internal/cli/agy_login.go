package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	agentagy "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// agyADCAuthEnv mirrors the Agy adapter's ADC selector. The Google option
// clears it for the interactive child so Agy opens its account sign-in rather
// than reusing an inherited ADC selection.
const agyADCAuthEnv = "AGY_ADC_AUTH"

// newAgyLoginCommand is an internal, trusted terminal entry point that offers
// the Agy sign-in methods AO can launch and persist without accepting raw
// commands or credentials from the renderer.
func newAgyLoginCommand(ctx *commandContext) *cobra.Command {
	var executable string
	cmd := &cobra.Command{
		Use:    "agy-login",
		Short:  "Sign in to Agy (internal)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runAgyLogin(cmd.Context(), executable, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&executable, "executable", "", "resolved Agy executable (internal)")
	_ = cmd.Flags().MarkHidden("executable")
	return cmd
}

func (c *commandContext) runAgyLogin(ctx context.Context, executable string, in io.Reader, out, stderr io.Writer) error {
	agy, err := resolveLoginExecutable(executable, "agy", c.deps.LookPath)
	if err != nil {
		return err
	}
	style := newCodexLoginStyle(out)
	if err := writeAgyLoginMenu(out, style); err != nil {
		return err
	}
	selection, err := readCodexLoginSelection(in)
	if err != nil {
		return fmt.Errorf("read login method: %w", err)
	}
	switch strings.TrimSpace(selection) {
	case "1":
		return c.runAgyGoogleLogin(ctx, agy, in, out, stderr, style)
	case "2":
		return c.runAgyADCLogin(ctx, in, out, stderr, style)
	default:
		return usageError{fmt.Errorf("login method must be 1 or 2")}
	}
}

// runAgyGoogleLogin opens Agy itself: an unsigned-in Agy starts its native
// browser sign-in on first run, and a signed-in Agy accepts /login to switch
// accounts. Agy has no standalone login subcommand.
func (c *commandContext) runAgyGoogleLogin(ctx context.Context, agy string, in io.Reader, out, stderr io.Writer, style codexLoginStyle) error {
	dataDir, err := agyLoginDataDir()
	if err != nil {
		return err
	}
	// The choice is recorded before Agy opens: quitting the TUI with Ctrl+C
	// exits non-zero even after a successful sign-in.
	if err := agentagy.WriteAuthMode(dataDir, agentagy.AuthModeGoogle); err != nil {
		return err
	}
	if err := os.Unsetenv(agyADCAuthEnv); err != nil {
		return fmt.Errorf("clear %s: %w", agyADCAuthEnv, err)
	}
	lines := []string{
		"",
		style.bold("Agy will open next."),
		"  " + style.dim("·") + " Not signed in yet? Agy opens Google sign-in in your browser.",
		"  " + style.dim("·") + " Already signed in? Type " + style.accent("/login") + " to switch accounts.",
		"  " + style.dim("·") + " When you're done, type " + style.accent("/exit") + " to finish.",
		"",
	}
	if _, err := fmt.Fprintln(out, strings.Join(lines, "\n")); err != nil {
		return err
	}
	if err := c.deps.RunInteractiveCommand(ctx, agy, nil, in, out, stderr); err != nil {
		return fmt.Errorf("agy sign-in failed: %w", err)
	}
	_, err = fmt.Fprintf(out, "\n%s\n", style.success("Agy will use your Google account sign-in."))
	return err
}

// runAgyADCLogin signs in to Google Cloud Application Default Credentials
// through gcloud and records ADC mode, which the Agy adapter applies to every
// launch as AGY_ADC_AUTH. A configured GOOGLE_APPLICATION_CREDENTIALS file
// needs no gcloud sign-in.
func (c *commandContext) runAgyADCLogin(ctx context.Context, in io.Reader, out, stderr io.Writer, style codexLoginStyle) error {
	dataDir, err := agyLoginDataDir()
	if err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")) == "" {
		gcloud, err := c.deps.LookPath("gcloud")
		if err != nil {
			return fmt.Errorf("the Google Cloud CLI (gcloud) is required for Application Default Credentials: install it from https://cloud.google.com/sdk/docs/install, or set GOOGLE_APPLICATION_CREDENTIALS")
		}
		if _, err := fmt.Fprintf(out, "\n%s\n\n", style.dim("Opening Google Cloud sign-in in your browser…")); err != nil {
			return err
		}
		if err := c.deps.RunInteractiveCommand(ctx, gcloud, []string{"auth", "application-default", "login"}, in, out, stderr); err != nil {
			return fmt.Errorf("gcloud application-default login failed: %w", err)
		}
	}
	if err := agentagy.WriteAuthMode(dataDir, agentagy.AuthModeADC); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "\n%s\n%s\n", style.success("Agy will use Application Default Credentials."), style.dim("New Agy sessions launch with AGY_ADC_AUTH set."))
	return err
}

func agyLoginDataDir() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("resolve AO data dir: %w", err)
	}
	return cfg.DataDir, nil
}

func writeAgyLoginMenu(out io.Writer, style codexLoginStyle) error {
	lines := []string{
		style.accent("Sign in to Agy"),
		style.dim("Choose how you want to authenticate."),
		"",
		fmt.Sprintf("  %s  %s  %s %s", style.accent("1"), style.bold("Google account"), style.success("Recommended"), style.dim("· Sign in with your browser")),
		fmt.Sprintf("  %s  %s %s", style.accent("2"), style.bold("Google Cloud (ADC)"), style.dim("· Application Default Credentials via gcloud")),
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
