package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// workspaceCommandResult is the common result model for project setup and
// startup cues. A zero outputLimit preserves the historical postCreate output;
// startup cues pass a bounded limit because their result is persisted in the
// session record.
type workspaceCommandResult struct {
	Output   string
	ExitCode *int
	Duration time.Duration
	Err      error
}

func runWorkspaceCommand(ctx context.Context, command, shell, workspace string, env map[string]string, outputLimit int) workspaceCommandResult {
	shell, args, err := workspaceShell(shell, command)
	if err != nil {
		return workspaceCommandResult{Err: err}
	}
	cmd := aoprocess.CommandContext(ctx, shell, args...)
	cmd.Dir = workspace
	cmd.Env = mergedCommandEnv(env)
	configureWorkspaceCommandProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	output := newBoundedWorkspaceOutput(outputLimit)
	cmd.Stdout, cmd.Stderr = output, output
	started := time.Now()
	err = cmd.Run()
	var code *int
	if cmd.ProcessState != nil {
		value := cmd.ProcessState.ExitCode()
		code = &value
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			err = fmt.Errorf("command exited with code %d", exit.ExitCode())
		}
	}
	return workspaceCommandResult{Output: output.String(), ExitCode: code, Duration: time.Since(started), Err: err}
}

func workspaceShell(shell, command string) (string, []string, error) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "cmd"
		} else {
			shell = "sh"
		}
	}
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(shell)), ".exe") {
	case "cmd":
		return shell, []string{"/d", "/s", "/c", command}, nil
	case "powershell", "pwsh":
		return shell, []string{"-NoProfile", "-NonInteractive", "-Command", command}, nil
	case "sh", "bash", "zsh", "fish":
		return shell, []string{"-c", command}, nil
	default:
		return "", nil, fmt.Errorf("unsupported startup shell: %s", shell)
	}
}

func mergedCommandEnv(extra map[string]string) []string {
	merged := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			merged[key] = value
		}
	}
	for key, value := range extra {
		merged[key] = value
	}
	env := make([]string, 0, len(merged))
	for key, value := range merged {
		env = append(env, key+"="+value)
	}
	return env
}

type boundedWorkspaceOutput struct {
	data  []byte
	limit int
}

func newBoundedWorkspaceOutput(limit int) *boundedWorkspaceOutput {
	return &boundedWorkspaceOutput{limit: limit}
}

func (b *boundedWorkspaceOutput) Write(p []byte) (int, error) {
	if b.limit == 0 {
		b.data = append(b.data, p...)
		return len(p), nil
	}
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
	} else {
		if len(b.data)+len(p) > b.limit {
			b.data = b.data[len(b.data)+len(p)-b.limit:]
		}
		b.data = append(b.data, p...)
	}
	return len(p), nil
}

func (b *boundedWorkspaceOutput) String() string {
	return strings.ToValidUTF8(string(b.data), "�")
}
