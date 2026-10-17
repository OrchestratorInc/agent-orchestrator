package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

// Keep stdin/stderr inherited and relay termination to the real command. A
// terminal interrupt reaches both processes; a direct kill of the wrapper must
// also stop gh rather than leave a PR creation running unseen.
func runGHCommand(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // resolved gh executable; arguments are forwarded verbatim
	cmd.Env = os.Environ()
	for i, entry := range cmd.Env {
		key, value, _ := strings.Cut(entry, "=")
		if key == "PATH" || runtime.GOOS == "windows" && strings.EqualFold(key, "PATH") {
			cmd.Env[i] = key + "=" + agentlaunch.GHPath(value)
		}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case sig := <-signals:
				_ = cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	<-stopped
	return err
}
