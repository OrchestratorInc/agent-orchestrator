//go:build !windows

package daemon

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Use a subprocess because restoring SIGTERM's default behavior would kill
// the test runner. Exercise both signal and HTTP-style worker cancellation.
func TestShutdownRetainsSignalHandlingThroughRestoration(t *testing.T) {
	if mode := os.Getenv("AO_DAEMON_SHUTDOWN_SIGNAL_HELPER"); mode != "" {
		ctx, stop, stopSignals := daemonShutdownContext()
		defer stopSignals()
		defer stop()
		input := bufio.NewScanner(os.Stdin)
		fmt.Println("ready")
		if mode == "signal" {
			<-ctx.Done()
		} else if !input.Scan() {
			t.Fatal("missing shutdown request")
		}
		stop()
		<-ctx.Done()
		fmt.Println("cleanup")
		if !input.Scan() {
			t.Fatal("missing restoration request")
		}
		// Give the repeated signal time to take effect before restoration.
		time.Sleep(100 * time.Millisecond)
		fmt.Println("restored")
		return
	}

	for _, mode := range []string{"signal", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := aoprocess.CommandContext(ctx, os.Args[0], "-test.run=^TestShutdownRetainsSignalHandlingThroughRestoration$")
			cmd.Env = append(os.Environ(), "AO_DAEMON_SHUTDOWN_SIGNAL_HELPER="+mode)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			t.Cleanup(func() {
				cancel()
				if !waited {
					_ = cmd.Wait()
				}
			})
			output := bufio.NewScanner(stdout)
			expect := func(want string) {
				t.Helper()
				if !output.Scan() || output.Text() != want {
					t.Fatalf("helper output = %q (%v), want %q", output.Text(), output.Err(), want)
				}
			}
			expect("ready")
			if mode == "signal" {
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			} else if _, err := fmt.Fprintln(stdin, "shutdown"); err != nil {
				t.Fatal(err)
			}
			expect("cleanup")
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintln(stdin, "restore"); err != nil {
				t.Fatal(err)
			}
			expect("restored")
			if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			if err != nil {
				t.Fatalf("helper did not survive SIGTERM during cleanup: %v\n%s", err, stderr.String())
			}
		})
	}
}
