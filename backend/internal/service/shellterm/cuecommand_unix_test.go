//go:build !windows

package shellterm

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type cuePTYRuntime struct {
	*fakeShellRuntime
	t          *testing.T
	terminal   *os.File
	beforeSend func()
	lastInput  string
}

func (r *cuePTYRuntime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	cmd := exec.Command(cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Dir = cfg.WorkspacePath
	cmd.Env = os.Environ()
	for key, value := range cfg.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	terminal, err := pty.Start(cmd)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	r.terminal = terminal
	done := make(chan struct{})
	go func() { _, _ = io.Copy(r, terminal); close(done) }()
	r.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = terminal.Close(); _ = cmd.Wait(); <-done })
	return r.fakeShellRuntime.Create(ctx, cfg)
}

func (r *cuePTYRuntime) Write(data []byte) (int, error) {
	r.outputMu.Lock()
	r.output += string(data)
	r.outputMu.Unlock()
	return len(data), nil
}

func (r *cuePTYRuntime) SendMessage(_ context.Context, _ ports.RuntimeHandle, input string) error {
	if r.beforeSend != nil {
		r.beforeSend()
	}
	r.lastInput = input
	_, err := io.WriteString(r.terminal, input+"\r")
	return err
}

func TestCueCommandPreservesPreviousHistoryWhenDispatcherIgnored(t *testing.T) {
	for _, mode := range []string{"default", "ignore dispatcher", "ignore space-prefixed cue", "history disabled"} {
		t.Run(mode, func(t *testing.T) {
			path, err := exec.LookPath("bash")
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SHELL", path)
			t.Setenv("PROMPT_COMMAND", "")
			t.Setenv("HISTFILE", filepath.Join(home, "bash-history"))
			t.Setenv("HISTIGNORE", "")
			t.Setenv("HISTCONTROL", "")
			t.Setenv("HISTTIMEFORMAT", "%F %T ")
			command := "printf cue > cue-output"
			if mode == "ignore dispatcher" {
				t.Setenv("HISTIGNORE", ". *")
			}
			if mode == "ignore space-prefixed cue" {
				t.Setenv("HISTCONTROL", "ignorespace")
				command = " " + command
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			waitForFile := func(name, want string) {
				t.Helper()
				for ctx.Err() == nil {
					data, _ := os.ReadFile(filepath.Join(home, name))
					if string(data) == want {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("shell did not write %s", name)
			}
			previous := "printf '%s\\n' previous-user-command > previous-output"
			rt := &cuePTYRuntime{fakeShellRuntime: newFakeShellRuntime(), t: t}
			rt.beforeSend = func() {
				if _, err := io.WriteString(rt.terminal, previous+"\r"); err != nil {
					t.Fatal(err)
				}
				waitForFile("previous-output", "previous-user-command\n")
				if mode == "history disabled" {
					if _, err := io.WriteString(rt.terminal, "set +o history; printf disabled > history-disabled\r"); err != nil {
						t.Fatal(err)
					}
					waitForFile("history-disabled", "disabled")
				}
			}
			svc := NewService(rt, &fakeShellTerminalStore{}, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"project": home}}, nil, t.TempDir(), "test", testLogger())
			if _, err := svc.RunCueCommand(ctx, RunCueCommandInput{ProjectID: "project", Command: command}); err != nil {
				t.Fatal(err)
			}
			waitForFile("cue-output", "cue")
			if _, err := io.WriteString(rt.terminal, "builtin history > history-output; printf done > history-probed\r"); err != nil {
				t.Fatal(err)
			}
			waitForFile("history-probed", "done")
			data, err := os.ReadFile(filepath.Join(home, "history-output"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), previous) {
				t.Fatalf("previous interactive command was deleted: %s", data)
			}
			if strings.Contains(string(data), rt.lastInput) {
				t.Fatalf("temporary dispatcher remains in history: %s", data)
			}
			if (mode == "ignore space-prefixed cue" || mode == "history disabled") && strings.Contains(string(data), command) {
				t.Fatalf("Cue bypassed history privacy settings: %s", data)
			}
		})
	}
}

func TestCueCommandPreservesNativeEvalEOFAndOptionBehavior(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "sh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name, command, want string
			}{
				{"EOF backslash", "cue_eof_function foo\\", "foo\\"},
				{"intentional final newline", "cue_eof_function foo\\\n", "foo"},
				{"option-like command", "-n", "option-like"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					home := t.TempDir()
					t.Setenv("HOME", home)
					t.Setenv("SHELL", path)
					t.Setenv("ZDOTDIR", "")
					t.Setenv("PROMPT_COMMAND", "")
					t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
					profile := "cue_eof_function() { printf '%s' \"$1\" > output; }\n"
					startup := filepath.Join(home, ".bashrc")
					switch shell {
					case "zsh":
						startup = filepath.Join(home, ".zshrc")
					case "sh":
						startup = filepath.Join(home, ".shrc")
					case "fish":
						startup = filepath.Join(home, ".config", "fish", "config.fish")
						profile = "function cue_eof_function; printf '%s' $argv[1] > output; end\n"
					}
					t.Setenv("ENV", startup)
					if err := os.MkdirAll(filepath.Dir(startup), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(startup, []byte(profile), 0o600); err != nil {
						t.Fatal(err)
					}
					bin := filepath.Join(home, "bin")
					if err := os.Mkdir(bin, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(bin, "-n"), []byte("#!/bin/sh\nprintf '%s' option-like > output\n"), 0o700); err != nil {
						t.Fatal(err)
					}
					t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
					if err := os.WriteFile(filepath.Join(home, "output"), []byte("not executed"), 0o600); err != nil {
						t.Fatal(err)
					}
					want := tc.want
					if tc.name == "EOF backslash" {
						if shell == "zsh" {
							want = "foo"
						}
						if shell == "fish" {
							want = "not executed"
						}
					}
					rt := &cuePTYRuntime{fakeShellRuntime: newFakeShellRuntime(), t: t}
					svc := NewService(rt, &fakeShellTerminalStore{}, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"project": home}}, nil, filepath.Join(t.TempDir(), "Cue's 雪 data"), "test", testLogger())
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := svc.RunCueCommand(ctx, RunCueCommandInput{ProjectID: "project", Command: tc.command}); err != nil {
						t.Fatal(err)
					}
					if _, err := io.WriteString(rt.terminal, "printf checked > checked\r"); err != nil {
						t.Fatal(err)
					}
					for ctx.Err() == nil {
						data, _ := os.ReadFile(filepath.Join(home, "checked"))
						if string(data) == "checked" {
							got, err := os.ReadFile(filepath.Join(home, "output"))
							if err != nil || string(got) != want {
								t.Fatalf("native %s output = %q, error = %v, want %q", shell, got, err, want)
							}
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
					t.Fatal("native shell did not finish the command and probe")
				})
			}
		})
	}
}

func TestCueCommandPreservesDataInInteractiveShell(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "sh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s unavailable: %v", shell, err)
			}
			for _, tc := range []struct {
				name, command, want string
				posix               bool
			}{
				{"quoted tab", "printf '%s\\n' 'left\tright' > output", "left\tright\n", false},
				{"tab separators", "printf\t'%s\\n'\tleft\tright > output", "left\nright\n", false},
				{"multiline", "printf '%s\\n' first > output\nprintf '%s\\n' second >> output", "first\nsecond\n", false},
				{"heredoc", "cat > output <<'END'\nleft\tright\n'$HOME' \"quoted\"\nEND\n", "left\tright\n'$HOME' \"quoted\"\n", true},
				{"CRLF quoted data", "printf '%s' 'left\r\nright' > output", "left\r\nright", true},
				{"quoting", "printf '%s\\n' \"quote ' and \\\"\" '$HOME `pwd` $(pwd) \\ 雪' > output", "quote ' and \"\n$HOME `pwd` $(pwd) \\ 雪\n", false},
				{"backslashes", "printf '%s' \"left\\\\right\" > output", "left\\right", false},
				{"large data", "printf '%s' '" + strings.Repeat("\t", 3900) + "' > output", strings.Repeat("\t", 3900), false},
				{"larger than Linux environment string limit", "printf '%s' '" + strings.Repeat("large-data-", 16000) + "' > output", strings.Repeat("large-data-", 16000), false},
				{"startup and project environment", "cue_alias > output; printf '%s\\n' \"$PROJECT_VALUE\" >> output", "startup\nproject\n", false},
				{"payload removed", "printenv AO_CUE_COMMAND > output; printf done >> output", "done", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if shell == "fish" && tc.posix {
						t.Skip("fish does not use POSIX shell syntax")
					}
					home := t.TempDir()
					t.Setenv("HOME", home)
					t.Setenv("SHELL", path)
					t.Setenv("ZDOTDIR", "")
					t.Setenv("PROMPT_COMMAND", "")
					t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
					profile := "cue_function() { printf '%s\\n' startup; }\nalias cue_alias=cue_function\n"
					startup := filepath.Join(home, ".bashrc")
					switch shell {
					case "zsh":
						startup = filepath.Join(home, ".zshrc")
					case "sh":
						startup = filepath.Join(home, ".shrc")
					case "fish":
						startup = filepath.Join(home, ".config", "fish", "config.fish")
						profile = "function cue_function; printf '%s\\n' startup; end\nalias cue_alias cue_function\n"
					}
					t.Setenv("ENV", startup)
					if err := os.MkdirAll(filepath.Dir(startup), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(startup, []byte(profile), 0o600); err != nil {
						t.Fatal(err)
					}
					rt := &cuePTYRuntime{fakeShellRuntime: newFakeShellRuntime(), t: t}
					svc := NewService(rt, &fakeShellTerminalStore{}, &fakeProjectRootLocator{
						roots: map[domain.ProjectID]string{"project": home},
						envs:  map[domain.ProjectID]map[string]string{"project": {"PROJECT_VALUE": "project", "PROJECT_LARGE": strings.Repeat("p", 12*1024), "AO_CUE_COMMAND": "untrusted"}},
					}, nil, filepath.Join(t.TempDir(), "Cue's 雪 data"), "test", nil)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := svc.RunCueCommand(ctx, RunCueCommandInput{ProjectID: "project", Command: tc.command}); err != nil {
						transcript, _ := rt.GetOutput(ctx, ports.RuntimeHandle{}, 0)
						t.Fatalf("%v; terminal: %q", err, transcript)
					}
					var got []byte
					var transcript string
					for ctx.Err() == nil {
						got, _ = os.ReadFile(filepath.Join(home, "output"))
						if string(got) == tc.want {
							transcript, _ = rt.GetOutput(ctx, ports.RuntimeHandle{}, 0)
							if strings.Contains(strings.ReplaceAll(transcript, "\r", ""), strings.ReplaceAll(tc.command, "\r", "")) {
								return
							}
						}
						time.Sleep(10 * time.Millisecond)
					}
					if string(got) == tc.want {
						t.Fatalf("terminal did not display the original command: %q", transcript)
					}
					t.Fatalf("output = %q (%x), want %q (%x)", got, got, tc.want, tc.want)
				})
			}
		})
	}
}

func TestCueCommandCanBeRecalledFromInteractiveHistory(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SHELL", path)
			t.Setenv("ZDOTDIR", "")
			t.Setenv("PROMPT_COMMAND", "")
			t.Setenv("HISTFILE", filepath.Join(home, "history"))
			t.Setenv("TERM", "xterm-256color")
			rt := &cuePTYRuntime{fakeShellRuntime: newFakeShellRuntime(), t: t}
			dataDir := filepath.Join(t.TempDir(), "Cue's 雪 data")
			svc := NewService(rt, &fakeShellTerminalStore{}, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"project": home}}, nil, dataDir, "test", testLogger())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := "printf '%s\\n' 'left\tright 雪' >> output"
			if _, err := svc.RunCueCommand(ctx, RunCueCommandInput{ProjectID: "project", Command: command}); err != nil {
				t.Fatal(err)
			}
			want := "left\tright 雪\n"
			for ctx.Err() == nil {
				data, _ := os.ReadFile(filepath.Join(home, "output"))
				if string(data) == want {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := io.WriteString(rt.terminal, "\x1b[A\r"); err != nil {
				t.Fatal(err)
			}
			for ctx.Err() == nil {
				data, _ := os.ReadFile(filepath.Join(home, "output"))
				if string(data) == want+want {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			transcript, _ := rt.GetOutput(ctx, ports.RuntimeHandle{}, 0)
			t.Fatalf("up-arrow did not replay the original command: %q", transcript)
		})
	}
}

func TestCueCommandPayloadPermissions(t *testing.T) {
	delivery, err := prepareCueCommand(t.TempDir(), []string{"bash"}, "echo private")
	if err != nil {
		t.Fatal(err)
	}
	defer delivery.cleanup()
	for path, want := range map[string]os.FileMode{delivery.file: 0o600, filepath.Dir(delivery.file): 0o700} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("payload permissions for %s = %v, error = %v, want %v", path, info, err, want)
		}
	}
}
