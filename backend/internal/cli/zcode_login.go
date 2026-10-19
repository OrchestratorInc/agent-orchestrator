package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/zcode"
)

// newZcodeLoginCommand is an internal, trusted terminal entry point. It lets
// the user pick between ZCode's browser login (an hourly z.ai OAuth token in
// the AO-owned credential store the daemon pins through ZCODE_DATA_BASE_DIR)
// and a Z.ai Coding Plan API key, which AO saves to its own provider config
// file. The key is read without echo and never printed, logged, or passed on
// argv or in the environment: only the file path is.
func newZcodeLoginCommand(ctx *commandContext) *cobra.Command {
	var executable string
	var providerConfig string
	cmd := &cobra.Command{
		Use:    "zcode-login",
		Short:  "Sign in to ZCode (internal)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runZcodeLogin(cmd.Context(), executable, providerConfig, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&executable, "executable", "", "resolved ZCode executable (internal)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "", "AO-owned personal provider config path for the API key option (internal)")
	_ = cmd.Flags().MarkHidden("executable")
	_ = cmd.Flags().MarkHidden("provider-config")
	return cmd
}

func (c *commandContext) runZcodeLogin(ctx context.Context, executable, providerConfig string, in io.Reader, out, stderr io.Writer) error {
	zcodeBin, err := resolveLoginExecutable(executable, "zcode", c.deps.LookPath)
	if err != nil {
		return err
	}
	style := newCodexLoginStyle(out)
	if err := writeZcodeLoginMenu(out, style); err != nil {
		return err
	}
	selection, err := readCodexLoginSelection(in)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read login method: %w", err)
	}

	switch strings.TrimSpace(selection) {
	case "1":
		if err := c.deps.RunInteractiveCommand(ctx, zcodeBin, []string{"login", "zai"}, in, out, stderr); err != nil {
			return fmt.Errorf("zcode login failed: %w", err)
		}
		_, err = fmt.Fprintf(out, "\n%s\n", style.success("ZCode sign-in complete."))
		return err
	case "2":
		if strings.TrimSpace(providerConfig) == "" {
			return usageError{errors.New("--provider-config is required for the API key option")}
		}
		if _, err := fmt.Fprintf(out, "\n%s\n%s: ", style.dim("Input stays hidden while you type."), style.bold("Enter Z.ai Coding Plan API key")); err != nil {
			return err
		}
		secret, err := c.deps.ReadSecret(in)
		if _, writeErr := fmt.Fprintln(out); writeErr != nil {
			return writeErr
		}
		if err != nil {
			return fmt.Errorf("read API key: %w", err)
		}
		defer zeroBytes(secret)
		key := bytes.TrimSpace(secret)
		if len(key) == 0 {
			return errors.New("API key is required")
		}
		if err := zcode.WritePersonalProviderConfig(ctx, providerConfig, key); err != nil {
			return fmt.Errorf("save API key: %w", err)
		}
		_, err = fmt.Fprintf(out, "\n%s\n", style.success("API key saved to "+providerConfig))
		return err
	default:
		return usageError{errors.New("login method must be 1 or 2")}
	}
}

func writeZcodeLoginMenu(out io.Writer, style codexLoginStyle) error {
	lines := []string{
		style.accent("Sign in to ZCode"),
		style.dim("Choose how you want to authenticate."),
		"",
		fmt.Sprintf("  %s  %s %s", style.accent("1"), style.bold("Z.AI account in browser"), style.dim("· Token lives one hour")),
		fmt.Sprintf("  %s  %s  %s %s", style.accent("2"), style.bold("Z.ai Coding Plan API key"), style.success("Recommended"), style.dim("· Saved by AO, no expiry")),
		"",
		style.bold("Enter 1-2 and press Return"),
		style.dim("Ctrl+C to cancel · Secret input stays hidden"),
	}
	if _, err := fmt.Fprintln(out, strings.Join(lines, "\n")); err != nil {
		return err
	}
	_, err := fmt.Fprint(out, style.accent("Selection [1-2]: "))
	return err
}
