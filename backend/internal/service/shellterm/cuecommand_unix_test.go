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
	t        *testing.T
	terminal *os.File
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
	go func() { _, _ = io.Copy(io.Discard, terminal); close(done) }()
	r.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = terminal.Close(); _ = cmd.Wait(); <-done })
	return r.fakeShellRuntime.Create(ctx, cfg)
}

func (r *cuePTYRuntime) SendMessage(_ context.Context, _ ports.RuntimeHandle, input string) error {
	_, err := io.WriteString(r.terminal, input+"\r")
	return err
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
				{"large data", "printf '%s' '" + strings.Repeat("\t", 3900) + "' > output", strings.Repeat("\t", 3900), false},
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
						envs:  map[domain.ProjectID]map[string]string{"project": {"PROJECT_VALUE": "project", "AO_CUE_COMMAND": "untrusted"}},
					}, nil, t.TempDir(), "test", nil)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := svc.RunCueCommand(ctx, RunCueCommandInput{ProjectID: "project", Command: tc.command}); err != nil {
						t.Fatal(err)
					}
					var got []byte
					for ctx.Err() == nil {
						got, _ = os.ReadFile(filepath.Join(home, "output"))
						if string(got) == tc.want {
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
					t.Fatalf("output = %q (%x), want %q (%x)", got, got, tc.want, tc.want)
				})
			}
		})
	}
}
