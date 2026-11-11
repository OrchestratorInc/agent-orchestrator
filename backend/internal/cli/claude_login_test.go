package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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

func runClaudeSetupTokenLogin(t *testing.T, token string) string {
	t.Helper()
	var output bytes.Buffer
	deps := Deps{
		In: strings.NewReader("4\n"), Out: &output, Err: io.Discard,
		ReadSecret: func(io.Reader) ([]byte, error) { return []byte(token), nil },
		RunInteractiveCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
			t.Fatal("setup token must not be sent to the Claude CLI")
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
	return output.String()
}

func TestClaudeLoginSavesSetupTokenForLocalSessions(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	const token = "sk-ant-oat01-local-setup-token"
	output := runClaudeSetupTokenLogin(t, token)
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

func TestClaudeLoginRejectsInvalidSetupToken(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	deps := Deps{
		In: strings.NewReader("4\n"), Out: io.Discard, Err: io.Discard,
		ReadSecret: func(io.Reader) ([]byte, error) { return []byte("invalid token"), nil },
	}
	cmd := newClaudeLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", "/managed/bin/claude"})
	cmd.SetIn(deps.In)
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	if err := cmd.Execute(); err == nil || ExitCode(err) != 2 {
		t.Fatalf("Execute error = %v, want usage error", err)
	}
	if _, err := os.Stat(claudecode.LocalOAuthTokenPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("invalid token must not be saved, stat error = %v", err)
	}
}

// The sign-in already succeeded by the time AO clears an earlier setup token,
// so a failed cleanup is reported as a warning, not a failed sign-in.
func TestClaudeLoginSucceedsWhenSetupTokenCleanupFails(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	// A non-empty directory where the token file belongs cannot be removed.
	blocker := filepath.Join(claudecode.LocalOAuthTokenPath(dataDir), "child")
	if err := os.MkdirAll(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	deps := Deps{
		In:  strings.NewReader("1\n"),
		Out: &stdout,
		Err: &stderr,
		LookPath: func(string) (string, error) {
			return "", errors.New("not on PATH")
		},
		RunInteractiveCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
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
	if !strings.Contains(stdout.String(), "sign-in complete") {
		t.Fatalf("stdout = %q, want the sign-in reported as complete", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Warning:") {
		t.Fatalf("stderr = %q, want a cleanup warning", stderr.String())
	}
}
