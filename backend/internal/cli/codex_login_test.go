package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
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
	openAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(openAI.Close)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_BASE_URL", "")
	prevSystem := codexSystemConfig
	codexSystemConfig = filepath.Join(t.TempDir(), "config.toml")
	t.Cleanup(func() { codexSystemConfig = prevSystem })
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
				OpenAIRESTBase: openAI.URL,
				RunInteractiveCommand: func(_ context.Context, name, dir string, args []string, stdin io.Reader, _, _ io.Writer) error {
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
		RunInteractiveCommand: func(_ context.Context, name, dir string, args []string, _ io.Reader, _, _ io.Writer) error {
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
				RunInteractiveCommand: func(_ context.Context, _, _ string, args []string, _ io.Reader, _, _ io.Writer) error {
					if slices.Equal(args, []string{"--version"}) {
						return nil // Codex starts; the sign-in itself failed
					}
					return errors.New("exit status 1")
				},
			},
			want: "codex login failed",
		},
		{
			// An npm launcher whose native binary is gone fails every run with a
			// raw spawn error (#7579).
			name: "codex cannot start",
			deps: Deps{
				In:       strings.NewReader("1\n"),
				LookPath: func(string) (string, error) { return "/nvm/bin/codex", nil },
				RunInteractiveCommand: func(_ context.Context, _, _ string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
					_, _ = io.WriteString(stdout, "Error: spawn /nvm/lib/node_modules/@openai/codex/vendor/aarch64-apple-darwin/codex/codex ENOENT\n")
					return errors.New("exit status 1")
				},
			},
			want: "the Codex CLI at /nvm/bin/codex could not start (Error: spawn /nvm/lib/node_modules/@openai/codex/vendor/aarch64-apple-darwin/codex/codex ENOENT). Reinstall or update Codex, then try again",
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
	if err := runInteractiveCommand(context.Background(), launcher, "", []string{"--device-auth"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("runInteractiveCommand: %v\n%s", err, out.String())
	}
	if got := strings.TrimSpace(out.String()); got != "login --device-auth" {
		t.Fatalf("output = %q, want npm launcher to run under node", got)
	}
}

// `codex login --with-api-key` saves any string and reports success, so AO asks
// OpenAI first (#5029). A refused key is not saved; a key for another provider,
// or one OpenAI can't be reached to check, is saved unchecked and says so.
func TestCodexLoginChecksAnAPIKeyWithOpenAIFirst(t *testing.T) {
	const key = "sk-garbage"
	for _, tc := range []struct {
		name       string
		status     int // what OpenAI answers; 0 means unreachable
		provider   string
		config     string // a raw config.toml
		system     string // a raw system-wide /etc/codex/config.toml
		wantErr    string
		wantSaved  bool
		wantAsked  bool
		wantOutput string
	}{
		{name: "refused", status: http.StatusUnauthorized, wantErr: "OpenAI refused this API key (HTTP 401), so it was not saved", wantAsked: true},
		{name: "accepted", status: http.StatusOK, wantSaved: true, wantAsked: true, wantOutput: "OpenAI accepted the key."},
		{name: "another provider", status: http.StatusUnauthorized, provider: "azure", wantSaved: true, wantOutput: "Saved without checking"},
		// A restricted key without Models: Read, or another org or region.
		{name: "forbidden", status: http.StatusForbidden, wantSaved: true, wantAsked: true, wantOutput: "OpenAI answered HTTP 403, so Codex saves the key unchecked."},
		{name: "openai_base_url", status: http.StatusUnauthorized, config: "openai_base_url = \"https://proxy.example.invalid/v1\"\n", wantSaved: true, wantOutput: "Saved without checking"},
		{name: "profile", status: http.StatusUnauthorized, config: "profile = \"work\"\n\n[profiles.work]\nmodel_provider = \"azure\"\n", wantSaved: true, wantOutput: "Saved without checking"},
		{name: "quoted openai_base_url", status: http.StatusUnauthorized, config: "\"openai_base_url\" = \"https://proxy.example.invalid/v1\"\n", wantSaved: true, wantOutput: "Saved without checking"},
		{name: "single-quoted openai_base_url", status: http.StatusUnauthorized, config: "'openai_base_url' = 'https://proxy.example.invalid/v1'\n", wantSaved: true, wantOutput: "Saved without checking"},
		{name: "quoted profile", status: http.StatusUnauthorized, config: "\"profile\" = \"work\"\n", wantSaved: true, wantOutput: "Saved without checking"},
		{name: "single-quoted profile", status: http.StatusUnauthorized, config: "'profile' = 'work'\n", wantSaved: true, wantOutput: "Saved without checking"},
		{name: "triple-quoted openai_base_url", status: http.StatusUnauthorized, config: "openai_base_url = \"\"\"https://proxy.example.invalid/v1\"\"\"\n", wantSaved: true, wantOutput: "Saved without checking"},
		{name: "openai_base_url inside a table", status: http.StatusUnauthorized, config: "[tui]\nopenai_base_url = \"https://proxy.example.invalid/v1\"\n", wantErr: "OpenAI refused this API key (HTTP 401)", wantAsked: true},
		{name: "system openai_base_url", status: http.StatusUnauthorized, system: "openai_base_url = \"https://proxy.example.invalid/v1\"\n", wantSaved: true, wantOutput: "Saved without checking"},
		{name: "openai provider named", status: http.StatusUnauthorized, config: "model_provider = \"openai\"\n", wantErr: "OpenAI refused this API key (HTTP 401)", wantAsked: true},
		{name: "unreachable", wantSaved: true, wantOutput: "Couldn't reach OpenAI to check the key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			t.Setenv("OPENAI_BASE_URL", "")
			system := filepath.Join(t.TempDir(), "config.toml")
			if tc.system != "" {
				if err := os.WriteFile(system, []byte(tc.system), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			prevSystem := codexSystemConfig
			codexSystemConfig = system
			t.Cleanup(func() { codexSystemConfig = prevSystem })
			config := tc.config
			if tc.provider != "" {
				config = "model_provider = \"" + tc.provider + "\"\n\n[model_providers." + tc.provider + "]\nbase_url = \"https://example.invalid/openai/v1\"\n"
			}
			if config != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			asked, gotAuth := false, ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asked, gotAuth = true, r.Header.Get("Authorization")
				w.WriteHeader(tc.status)
			}))
			base := server.URL
			if tc.status == 0 {
				server.Close() // nothing listens there now
			} else {
				t.Cleanup(server.Close)
			}
			var stdout bytes.Buffer
			saved := false
			deps := Deps{
				In: strings.NewReader("3\n"), Out: &stdout, Err: &stdout,
				LookPath:       func(string) (string, error) { return "/usr/local/bin/codex", nil },
				ReadSecret:     func(io.Reader) ([]byte, error) { return []byte(key), nil },
				OpenAIRESTBase: base,
				RunInteractiveCommand: func(context.Context, string, string, []string, io.Reader, io.Writer, io.Writer) error {
					saved = true
					return nil
				},
			}
			cmd := newCodexLoginCommand(&commandContext{deps: deps.withDefaults()})
			cmd.SetIn(deps.In)
			cmd.SetOut(deps.Out)
			cmd.SetErr(deps.Err)
			err := cmd.Execute()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Execute error = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if saved != tc.wantSaved || asked != tc.wantAsked {
				t.Fatalf("saved %v, OpenAI asked %v; want saved %v, asked %v", saved, asked, tc.wantSaved, tc.wantAsked)
			}
			if asked && gotAuth != "Bearer "+key {
				t.Fatalf("Authorization = %q, want the key as a Bearer token", gotAuth)
			}
			if !strings.Contains(stdout.String(), tc.wantOutput) {
				t.Fatalf("output %q lacks %q", stdout.String(), tc.wantOutput)
			}
			if strings.Contains(stdout.String(), key) || (err != nil && strings.Contains(err.Error(), key)) {
				t.Fatal("the key leaked into output or the error")
			}
		})
	}
}

func TestLastLineKeepsTheLauncherErrorWhole(t *testing.T) {
	long := strings.Repeat("a", 299) + "é" + "tail"
	for _, tt := range []struct{ name, in, want string }{
		{name: "last non-blank line", in: "starting\nError: spawn codex ENOENT\n\n", want: "Error: spawn codex ENOENT"},
		{name: "cut by character", in: long, want: strings.Repeat("a", 299) + "é..."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := lastLine(tt.in)
			if got != tt.want || !utf8.ValidString(got) {
				t.Fatalf("lastLine = %q, want %q", got, tt.want)
			}
		})
	}
}
