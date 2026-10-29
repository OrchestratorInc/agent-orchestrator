package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
)

func TestClaudeLoginRunsEverySupportedLoginMethod(t *testing.T) {
	tests := []struct {
		name      string
		selection string
		wantArgs  []string
	}{
		{name: "Claude subscription", selection: "1\n", wantArgs: []string{"auth", "login", "--claudeai"}},
		{name: "Anthropic Console", selection: "2\n", wantArgs: []string{"auth", "login", "--console"}},
		{name: "SSO", selection: "3\n", wantArgs: []string{"auth", "login", "--sso"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A setup token from an earlier sign-in must not outrank this one.
			dataDir := t.TempDir()
			t.Setenv("AO_DATA_DIR", dataDir)
			if err := claudecode.SaveLocalOAuthToken(dataDir, []byte("sk-ant-oat01-earlier-setup-token")); err != nil {
				t.Fatal(err)
			}
			var gotName string
			var gotArgs []string
			deps := Deps{
				In:  strings.NewReader(tt.selection),
				Out: io.Discard,
				Err: io.Discard,
				LookPath: func(string) (string, error) {
					return "", errors.New("not on PATH")
				},
				RunInteractiveCommand: func(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
					gotName = name
					gotArgs = append([]string(nil), args...)
					return nil
				},
			}

			cmd := newClaudeLoginCommand(&commandContext{deps: deps.withDefaults()})
			cmd.SetArgs([]string{"--executable", "/managed/bin/claude"})
			cmd.SetIn(deps.In)
			cmd.SetOut(deps.Out)
			cmd.SetErr(deps.Err)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if gotName != "/managed/bin/claude" {
				t.Errorf("executable = %q, want adapter-resolved Claude", gotName)
			}
			if !slices.Equal(gotArgs, tt.wantArgs) {
				t.Errorf("args = %q, want %q", gotArgs, tt.wantArgs)
			}
			if _, err := os.Stat(claudecode.LocalOAuthTokenPath(dataDir)); !os.IsNotExist(err) {
				t.Errorf("earlier setup token survived a new sign-in, stat error = %v", err)
			}
		})
	}
}

func TestClaudeLoginMenuListsEverySupportedMethod(t *testing.T) {
	var stdout bytes.Buffer
	if err := writeClaudeLoginMenu(&stdout, codexLoginStyle{}); err != nil {
		t.Fatalf("writeClaudeLoginMenu: %v", err)
	}
	for _, fragment := range []string{"Sign in to Claude Code", "Claude subscription", "Anthropic Console", "SSO", "Setup token", "Selection [1-4]"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("menu missing %q:\n%s", fragment, stdout.String())
		}
	}
}

func runClaudeSetupTokenLogin(t *testing.T, screen terminalScreen, printed string) (string, error) {
	t.Helper()
	var output bytes.Buffer
	deps := Deps{
		In: strings.NewReader("4\n"), Out: &output, Err: io.Discard,
		RunInteractiveCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
			t.Fatal("setup token flow must not run claude auth login")
			return nil
		},
		RunCapturedTerminalCommand: func(_ context.Context, name string, args []string, _ io.Reader, stdout io.Writer) (terminalScreen, error) {
			if name != "/managed/bin/claude" || !slices.Equal(args, []string{"setup-token"}) {
				t.Fatalf("captured command = %q %q, want claude setup-token", name, args)
			}
			_, _ = io.WriteString(stdout, printed)
			return screen, nil
		},
	}
	cmd := newClaudeLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", "/managed/bin/claude"})
	cmd.SetIn(deps.In)
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	err := cmd.Execute()
	return output.String(), err
}

func TestClaudeLoginSavesSetupTokenForLocalSessions(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	const token = "sk-ant-oat01-local-setup-token"
	screen := terminalScreen{Cols: 80, Rows: []string{"Your OAuth token (valid for 1 year):", "", token, "", "Store this token securely."}}
	output, err := runClaudeSetupTokenLogin(t, screen, "Your OAuth token (valid for 1 year):\r\n\r\n\x1b[33m"+token+"\x1b[39m\r\n")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	path := claudecode.LocalOAuthTokenPath(dataDir)
	got, err := os.ReadFile(path)
	if err != nil || string(got) != token {
		t.Fatalf("saved setup token = %q, err = %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("setup token file mode = %v, want 0600", info.Mode().Perm())
	}
	if strings.Contains(output, token) {
		t.Fatal("setup token appeared in terminal output")
	}
	if !strings.Contains(output, path) {
		t.Fatalf("output does not name the saved file %q:\n%s", path, output)
	}
}

func TestClaudeLoginFailsWhenSetupTokenPrintsNoToken(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	screen := terminalScreen{Cols: 80, Rows: []string{"OAuth error: cancelled"}}
	if _, err := runClaudeSetupTokenLogin(t, screen, "OAuth error: cancelled\r\n"); err == nil {
		t.Fatal("Execute succeeded without a token")
	}
	if _, err := os.Stat(claudecode.LocalOAuthTokenPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("no token must be saved, stat error = %v", err)
	}
}

func TestClaudeSetupTokenFromScreenJoinsWrappedRows(t *testing.T) {
	const token = "sk-ant-oat01-abcdefghijklmnopqrstuvwxyz0123456789_ABCDEFGHIJ-KLMNOP"
	screen := terminalScreen{Cols: 20, Rows: []string{
		"✓ Created!", "", token[:20], token[20:40], token[40:60], token[60:], "", "Store this token",
	}}
	if got := string(claudeSetupTokenFromScreen(screen)); got != token {
		t.Fatalf("token = %q, want %q", got, token)
	}
	// A token that exactly fills its row must not swallow the next row.
	exact := terminalScreen{Cols: 20, Rows: []string{token[:20], "Store this token"}}
	if got := claudeSetupTokenFromScreen(exact); string(got) != token[:20] {
		t.Fatalf("exact-width token = %q, want %q", got, token[:20])
	}
	if got := claudeSetupTokenFromScreen(terminalScreen{Cols: 80, Rows: []string{"sk-ant-oat"}}); got != nil {
		t.Fatalf("truncated token = %q, want nil", got)
	}
}

func TestSecretCutoffWriterHidesTokenSplitAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	w := newSecretCutoffWriter(&out, claudeSetupTokenPrefix)
	for _, chunk := range []string{"Your token:\r\n\x1b[33msk-a", "nt-oat01-secret", "\x1b[39m\r\nStore it"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if got := out.String(); strings.Contains(got, "sk-a") || strings.Contains(got, "secret") || !strings.HasPrefix(got, "Your token:\r\n\x1b[33m") {
		t.Fatalf("mirrored output = %q", got)
	}
	// Text that only resembles the start of the marker is not held back.
	out.Reset()
	w = newSecretCutoffWriter(&out, claudeSetupTokenPrefix)
	_, _ = w.Write([]byte("Paste code here > "))
	if out.String() != "Paste code here > " {
		t.Fatalf("prompt held back: %q", out.String())
	}
}
