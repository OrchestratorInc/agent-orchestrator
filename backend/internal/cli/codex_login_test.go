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
	"time"
)

func TestCodexLoginRunsSelectedNativeLoginMethod(t *testing.T) {
	tests := []struct {
		name      string
		selection string
		secret    string
		wantArgs  []string
		wantStdin string
	}{
		{name: "ChatGPT browser", selection: "1\n", wantArgs: []string{"-c", `cli_auth_credentials_store="file"`, "login"}},
		{name: "device code", selection: "2\n", wantArgs: []string{"-c", `cli_auth_credentials_store="file"`, "login", "--device-auth"}},
		{name: "API key", selection: "3\n", secret: "sk-test-secret", wantArgs: []string{"-c", `cli_auth_credentials_store="file"`, "login", "--with-api-key"}, wantStdin: "sk-test-secret\n"},
		{name: "access token", selection: "4\n", secret: "token-test-secret", wantArgs: []string{"-c", `cli_auth_credentials_store="file"`, "login", "--with-access-token"}, wantStdin: "token-test-secret\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var gotName string
			var gotArgs []string
			var gotStdin string
			deps := Deps{
				In:       strings.NewReader(tt.selection),
				Out:      &stdout,
				Err:      &stdout,
				LookPath: func(string) (string, error) { return "/usr/local/bin/codex", nil },
				ReadSecret: func(io.Reader) ([]byte, error) {
					return []byte(tt.secret), nil
				},
				RunInteractiveCommand: func(_ context.Context, name string, args []string, stdin io.Reader, _, _ io.Writer) error {
					gotName = name
					gotArgs = append([]string(nil), args...)
					if stdin != nil {
						data, _ := io.ReadAll(stdin)
						gotStdin = string(data)
					}
					return nil
				},
			}

			cmd := newCodexLoginCommand(&commandContext{deps: deps.withDefaults()})
			cmd.SetIn(deps.In)
			cmd.SetOut(deps.Out)
			cmd.SetErr(deps.Err)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}

			if gotName != "/usr/local/bin/codex" {
				t.Errorf("executable = %q, want resolved Codex", gotName)
			}
			if !slices.Equal(gotArgs, tt.wantArgs) {
				t.Errorf("args = %q, want %q", gotArgs, tt.wantArgs)
			}
			if gotStdin != tt.wantStdin {
				t.Errorf("stdin = %q, want %q", gotStdin, tt.wantStdin)
			}
			if tt.secret != "" && strings.Contains(stdout.String(), tt.secret) {
				t.Fatalf("secret leaked into output: %q", stdout.String())
			}
			for _, arg := range gotArgs {
				if tt.secret != "" && strings.Contains(arg, tt.secret) {
					t.Fatalf("secret leaked into argv: %q", gotArgs)
				}
			}
		})
	}
}

func TestCodexLoginUsesExplicitResolvedExecutable(t *testing.T) {
	var gotName string
	var gotArgs []string
	deps := Deps{
		In:  strings.NewReader("2\n"),
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

	cmd := newCodexLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", "/managed/bin/codex", "--use-default-credential-store"})
	cmd.SetIn(deps.In)
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotName != "/managed/bin/codex" {
		t.Fatalf("executable = %q, want adapter-resolved Codex", gotName)
	}
	if want := []string{"login", "--device-auth"}; !slices.Equal(gotArgs, want) {
		t.Fatalf("args = %q, want default credential store args %q", gotArgs, want)
	}
}

func TestCodexLoginMenuListsEverySupportedMethod(t *testing.T) {
	var stdout bytes.Buffer
	if err := writeCodexLoginMenu(&stdout, codexLoginStyle{}); err != nil {
		t.Fatalf("writeCodexLoginMenu: %v", err)
	}
	for _, fragment := range []string{"Sign in to Codex", "ChatGPT in browser", "Device code", "OpenAI API key", "Access token", "Selection [1-4]"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("menu missing %q:\n%s", fragment, stdout.String())
		}
	}
	const maxRows = 14
	if rows := strings.Count(stdout.String(), "\n") + 1; rows > maxRows {
		t.Fatalf("menu uses %d rows, want at most %d", rows, maxRows)
	}
}

func TestCodexLoginReportsMissingBinaryAndNativeFailure(t *testing.T) {
	tests := []struct {
		name string
		deps Deps
		want string
	}{
		{
			name: "missing binary",
			deps: Deps{LookPath: func(string) (string, error) { return "", errors.New("missing") }},
			want: "codex CLI is not installed",
		},
		{
			name: "native failure",
			deps: Deps{
				In:       strings.NewReader("1\n"),
				LookPath: func(string) (string, error) { return "/codex", nil },
				RunInteractiveCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
					return errors.New("exit status 1")
				},
			},
			want: "codex login failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newCodexLoginCommand(&commandContext{deps: tt.deps.withDefaults()})
			if tt.deps.In == nil {
				cmd.SetIn(strings.NewReader("1\n"))
			} else {
				cmd.SetIn(tt.deps.In)
			}
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCodexLoginExplainsCodexInstallThatCannotStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell launcher fixture is Unix-specific")
	}
	tests := []struct {
		name       string
		launcher   string
		wantReason string
	}{
		{
			// npm launchers up to 0.132 spawn vendor/<triple>/codex/codex
			// without checking it exists; Node prints the spawn error.
			name:       "native binary missing, old launcher",
			launcher:   "#!/bin/sh\necho \"Error: spawn /x/vendor/aarch64-apple-darwin/codex/codex ENOENT\" >&2\necho '    at ChildProcess._handle.onexit (node:internal/child_process:286:19)' >&2\nexit 1\n",
			wantReason: "(Error: spawn /x/vendor/aarch64-apple-darwin/codex/codex ENOENT)",
		},
		{
			// 0.133+ launchers throw instead; Node prints the source location first.
			name:       "native binary missing, new launcher",
			launcher:   "#!/bin/sh\nprintf 'file:///x/bin/codex.js:123\\n    throw new Error(\\n    ^\\n\\nError: Missing optional dependency @openai/codex-darwin-arm64. Reinstall Codex: npm install -g @openai/codex@latest\\n' >&2\nexit 1\n",
			wantReason: "(Error: Missing optional dependency @openai/codex-darwin-arm64.",
		},
		{
			// A working Codex that rejects the login keeps Codex's own failure.
			name:     "login rejected",
			launcher: "#!/bin/sh\n[ \"$1\" = --version ] && { echo codex-cli 0.166.0; exit 0; }\nexit 1\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := filepath.Join(t.TempDir(), "codex")
			if err := os.WriteFile(launcher, []byte(tt.launcher), 0o755); err != nil {
				t.Fatal(err)
			}
			deps := Deps{In: strings.NewReader("1\n"), Out: io.Discard, Err: io.Discard}
			cmd := newCodexLoginCommand(&commandContext{deps: deps.withDefaults()})
			cmd.SetArgs([]string{"--executable", launcher})
			cmd.SetIn(deps.In)
			cmd.SetOut(deps.Out)
			cmd.SetErr(deps.Err)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("Execute succeeded, want login failure")
			}
			msg := err.Error()
			hinted := strings.Contains(msg, "the Codex CLI at "+launcher+" could not start") && strings.Contains(msg, "Reinstall or update Codex")
			if tt.wantReason == "" {
				if hinted {
					t.Fatalf("error = %q, want Codex's own login failure", msg)
				}
				return
			}
			if !hinted || !strings.Contains(msg, tt.wantReason) {
				t.Fatalf("error = %q, want reinstall hint with reason %q", msg, tt.wantReason)
			}
		})
	}
}

func TestCodexLoginStartProbeTimesOutWhenLauncherChildHoldsPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell launcher fixture is Unix-specific")
	}
	previous := codexStartProbeTimeout
	codexStartProbeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { codexStartProbeTimeout = previous })

	// Login fails; --version hangs and leaves a background child that inherits
	// stdout/stderr, so killing only the launcher would not close the pipes.
	launcher := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\n[ \"$1\" = --version ] || exit 1\nsleep 30 &\nsleep 30\n"
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	deps := Deps{In: strings.NewReader("1\n"), Out: io.Discard, Err: io.Discard}
	cmd := newCodexLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", launcher})
	cmd.SetIn(deps.In)
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)

	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case err := <-done:
		if err == nil || err.Error() != "codex login failed: exit status 1" {
			t.Fatalf("Execute error = %v, want Codex's own login failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("codex-login still waiting on the start probe 10s after its 300ms timeout")
	}
}

func TestRunInteractiveCommandFindsNodeForNPMLauncherOutsidePATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("npm env-node launcher is Unix-specific")
	}
	home := t.TempDir()
	nodeDir := filepath.Join(home, ".nvm", "versions", "node", "v22.11.0", "bin")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "node"), []byte("#!/bin/sh\nscript=$1\nshift\nexec /bin/sh \"$script\" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(home, "npm-global", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/usr/bin/env node\necho \"login $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	var out bytes.Buffer
	if err := runInteractiveCommand(context.Background(), launcher, []string{"--device-auth"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("runInteractiveCommand: %v\n%s", err, out.String())
	}
	if got := strings.TrimSpace(out.String()); got != "login --device-auth" {
		t.Fatalf("output = %q, want npm launcher to run under node", got)
	}
}
