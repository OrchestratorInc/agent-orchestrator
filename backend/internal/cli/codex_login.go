package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

const codexFileStoreOverride = `cli_auth_credentials_store="file"`

const (
	ansiReset     = "\x1b[0m"
	ansiBold      = "\x1b[1m"
	ansiDim       = "\x1b[2m"
	ansiCyanBold  = "\x1b[1;36m"
	ansiGreenBold = "\x1b[1;32m"
)

// newCodexLoginCommand is an internal, trusted terminal entry point. It gives
// the user an interactive choice of Codex-supported authentication methods
// while CODEX_HOME points at AO's private pending account slot.
func newCodexLoginCommand(ctx *commandContext) *cobra.Command {
	var executable string
	var useDefaultCredentialStore bool
	cmd := &cobra.Command{
		Use:    "codex-login",
		Short:  "Sign a managed Codex account in (internal)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runCodexLogin(cmd.Context(), executable, useDefaultCredentialStore, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&executable, "executable", "", "resolved Codex executable (internal)")
	cmd.Flags().BoolVar(&useDefaultCredentialStore, "use-default-credential-store", false, "respect the user's Codex credential store (internal)")
	_ = cmd.Flags().MarkHidden("executable")
	_ = cmd.Flags().MarkHidden("use-default-credential-store")
	return cmd
}

func (c *commandContext) runCodexLogin(ctx context.Context, executable string, useDefaultCredentialStore bool, in io.Reader, out, stderr io.Writer) error {
	codex, err := resolveLoginExecutable(executable, "codex", c.deps.LookPath)
	if err != nil {
		return err
	}
	style := newCodexLoginStyle(out)
	if err := writeCodexLoginMenu(out, style); err != nil {
		return err
	}
	selection, err := readCodexLoginSelection(in)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read login method: %w", err)
	}

	args := []string{"login"}
	if !useDefaultCredentialStore {
		args = append([]string{"-c", codexFileStoreOverride}, args...)
	}
	childInput := in
	switch strings.TrimSpace(selection) {
	case "1":
	case "2":
		args = append(args, "--device-auth")
	case "3", "4":
		flag, label := "--with-api-key", "API key"
		if strings.TrimSpace(selection) == "4" {
			flag, label = "--with-access-token", "access token"
		}
		if _, err := fmt.Fprintf(out, "\n%s\n%s: ", style.dim("Input stays hidden while you type."), style.bold("Enter "+label)); err != nil {
			return err
		}
		secret, err := c.deps.ReadSecret(in)
		if _, writeErr := fmt.Fprintln(out); writeErr != nil {
			return writeErr
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", label, err)
		}
		secret = bytes.TrimSpace(secret)
		if len(secret) == 0 {
			return fmt.Errorf("%s is required", label)
		}
		defer zeroBytes(secret)
		if flag == "--with-api-key" {
			if err := c.checkOpenAIKey(ctx, secret, out, style); err != nil {
				return err
			}
		}
		args = append(args, flag)
		payload := append(append([]byte(nil), secret...), '\n')
		defer zeroBytes(payload)
		childInput = bytes.NewReader(payload)
	default:
		return usageError{fmt.Errorf("login method must be 1, 2, 3, or 4")}
	}

	if err := c.deps.RunInteractiveCommand(ctx, codex, "", args, childInput, out, stderr); err != nil {
		if startErr := c.codexStartFailure(ctx, codex); startErr != nil {
			return startErr
		}
		return fmt.Errorf("codex login failed: %w", err)
	}
	if _, err := fmt.Fprintf(out, "\n%s\n", style.success("Codex sign-in complete.")); err != nil {
		return err
	}
	return nil
}

const (
	defaultOpenAIRESTBase = "https://api.openai.com/v1"
	openAIKeyCheckTimeout = 15 * time.Second
)

// checkOpenAIKey asks OpenAI whether it accepts key before Codex saves it.
// `codex login --with-api-key` stores any string and reports success, so a
// mistyped key failed only at a session's first model call (#5029). A refused
// key (401) is not saved. A key Codex sends somewhere else (another
// model_provider, an openai_base_url or OPENAI_BASE_URL, or a profile that may
// pick either) is not OpenAI's to judge and is never sent to it. A 403 can mean
// a restricted key without Models: Read, another org or region, and an
// unreachable OpenAI is no verdict: those keys are saved unchecked, and the
// output says so. The key never appears in output or errors.
func (c *commandContext) checkOpenAIKey(ctx context.Context, key []byte, out io.Writer, style codexLoginStyle) error {
	if codexKeyGoesElsewhere() || strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")) != "" {
		_, err := fmt.Fprintln(out, style.dim("Saved without checking: Codex uses another provider than OpenAI for this key."))
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, openAIKeyCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(c.deps.OpenAIRESTBase, "/")+"/models", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+string(key))
	client := &http.Client{Timeout: openAIKeyCheckTimeout}
	if c.deps.HTTPClient != nil {
		client.Transport = c.deps.HTTPClient.Transport
	}
	resp, err := client.Do(req)
	if err != nil {
		_, writeErr := fmt.Fprintln(out, style.dim("Couldn't reach OpenAI to check the key, so Codex saves it unchecked."))
		return writeErr
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("OpenAI refused this API key (HTTP %d), so it was not saved. Check the key and try again", resp.StatusCode)
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		_, err := fmt.Fprintln(out, style.success("OpenAI accepted the key."))
		return err
	default:
		_, err := fmt.Fprintln(out, style.dim(fmt.Sprintf("OpenAI answered HTTP %d, so Codex saves the key unchecked.", resp.StatusCode)))
		return err
	}
}

// codexSystemConfig is the system-wide config.toml Codex also loads; a
// variable so tests never read the host's.
var codexSystemConfig = "/etc/codex/config.toml"

// codexKeyGoesElsewhere reports whether Codex's user or system config.toml, at
// its top level (before any [table]), selects another model_provider than
// openai, points the built-in openai provider at another openai_base_url, or
// picks a profile, which can do either. Either file is enough: the user file
// can only override a system setting, so a key that might not reach OpenAI is
// saved unchecked rather than judged by it.
func codexKeyGoesElsewhere() bool {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".codex")
		}
	}
	if home != "" && codexConfigRoutesElsewhere(filepath.Join(home, "config.toml")) {
		return true
	}
	return codexConfigRoutesElsewhere(codexSystemConfig)
}

// codexConfigRoutesElsewhere decodes the config.toml at path, as TOML so every
// string form counts, and reports whether a top-level model_provider other
// than openai, an openai_base_url, or a profile is set.
func codexConfigRoutesElsewhere(path string) bool {
	data, err := os.ReadFile(path) //nolint:gosec // Codex's own config files.
	if err != nil {
		return false
	}
	var config struct {
		ModelProvider string `toml:"model_provider"`
		OpenAIBaseURL string `toml:"openai_base_url"`
		Profile       string `toml:"profile"`
	}
	if toml.Unmarshal(data, &config) != nil {
		return false
	}
	provider := strings.TrimSpace(config.ModelProvider)
	return (provider != "" && provider != "openai") ||
		strings.TrimSpace(config.OpenAIBaseURL) != "" ||
		strings.TrimSpace(config.Profile) != ""
}

// codexStartProbeTimeout bounds the `codex --version` run that tells a Codex
// that cannot start from a sign-in that failed.
const codexStartProbeTimeout = 15 * time.Second

// codexStartFailure reports a Codex that cannot start at all, after its login
// failed. An npm launcher whose native binary is gone (macOS can quarantine
// it) fails both runs with only a raw spawn error, which says nothing about
// what to do (#7579). A Codex whose --version works failed the sign-in itself,
// and is left to that error.
func (c *commandContext) codexStartFailure(ctx context.Context, codex string) error {
	probeCtx, cancel := context.WithTimeout(ctx, codexStartProbeTimeout)
	defer cancel()
	var output bytes.Buffer
	err := c.deps.RunInteractiveCommand(probeCtx, codex, "", []string{"--version"}, strings.NewReader(""), &output, &output)
	if err == nil || ctx.Err() != nil {
		return nil
	}
	reason := lastLine(output.String())
	if reason == "" {
		reason = err.Error()
	}
	return fmt.Errorf("the Codex CLI at %s could not start (%s). Reinstall or update Codex, then try again", codex, reason)
}

// lastLine is the last non-blank line of s, where a failed launcher leaves
// its error, cut to a length an error message can carry.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	const limit = 300 // characters, so a cut never splits one
	if runes := []rune(line); len(runes) > limit {
		line = string(runes[:limit]) + "..."
	}
	return line
}

func resolveLoginExecutable(explicit, name string, lookPath func(string) (string, error)) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	resolved, err := lookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s CLI is not installed or is not available on PATH", name)
	}
	return resolved, nil
}

type codexLoginStyle struct {
	enabled bool
}

func newCodexLoginStyle(out io.Writer) codexLoginStyle {
	if os.Getenv("NO_COLOR") != "" {
		return codexLoginStyle{}
	}
	file, ok := out.(*os.File)
	return codexLoginStyle{enabled: ok && term.IsTerminal(file.Fd())}
}

func (s codexLoginStyle) wrap(code, value string) string {
	if !s.enabled {
		return value
	}
	return code + value + ansiReset
}

func (s codexLoginStyle) bold(value string) string {
	return s.wrap(ansiBold, value)
}

func (s codexLoginStyle) dim(value string) string {
	return s.wrap(ansiDim, value)
}

func (s codexLoginStyle) accent(value string) string {
	return s.wrap(ansiCyanBold, value)
}

func (s codexLoginStyle) success(value string) string {
	return s.wrap(ansiGreenBold, value)
}

func writeCodexLoginMenu(out io.Writer, style codexLoginStyle) error {
	lines := []string{
		style.accent("Sign in to Codex"),
		style.dim("Choose how you want to authenticate this account."),
		"",
		style.dim("PERSONAL ACCOUNT"),
		fmt.Sprintf("  %s  %s  %s %s", style.accent("1"), style.bold("ChatGPT in browser"), style.success("Recommended"), style.dim("· Continue using your ChatGPT account")),
		fmt.Sprintf("  %s  %s %s", style.accent("2"), style.bold("Device code"), style.dim("· Sign in on another device")),
		"",
		style.dim("DEVELOPER CREDENTIALS"),
		fmt.Sprintf("  %s  %s %s", style.accent("3"), style.bold("OpenAI API key"), style.dim("· Checked with OpenAI, then saved by Codex")),
		fmt.Sprintf("  %s  %s %s", style.accent("4"), style.bold("Access token"), style.dim("· For advanced or managed environments")),
		"",
		style.bold("Enter 1-4 and press Return"),
		style.dim("Ctrl+C to cancel · Secret input stays hidden"),
	}
	if _, err := fmt.Fprintln(out, strings.Join(lines, "\n")); err != nil {
		return err
	}
	_, err := fmt.Fprint(out, style.accent("Selection [1-4]: "))
	return err
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func readCodexLoginSelection(in io.Reader) (string, error) {
	var value strings.Builder
	var one [1]byte
	for {
		n, err := in.Read(one[:])
		if n == 1 {
			value.WriteByte(one[0])
			if one[0] == '\n' {
				return value.String(), nil
			}
		}
		if err != nil {
			if err == io.EOF && value.Len() > 0 {
				return value.String(), nil
			}
			return "", err
		}
	}
}

func runInteractiveCommand(ctx context.Context, name, dir string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec,forbidigo // interactive login must inherit the caller's console.
	cmd.Env = agentlaunch.ProcessEnvForLaunchBinary(ctx, name)
	configureCLIBatch(cmd)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A child the launcher started can keep a captured output pipe open after
	// the context kills the launcher; stop waiting for it shortly after.
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd.Run()
}

func readSecret(in io.Reader) ([]byte, error) {
	if file, ok := in.(*os.File); ok && term.IsTerminal(file.Fd()) {
		return term.ReadPassword(file.Fd())
	}
	value, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	return []byte(value), nil
}
