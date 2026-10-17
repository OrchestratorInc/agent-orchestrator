package sessionmanager

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
)

// TestMain lets native-runtime tests re-exec this test binary as the detached
// pty-host and as a stand-in for `ao agent-process supervise`.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "pty-host" {
		os.Exit(conpty.RunHost(os.Args[2:], os.Stdout))
	}
	if len(os.Args) > 2 && os.Args[1] == "agent-process" && os.Args[2] == "supervise" {
		os.Exit(runTestSupervisor(os.Args[3:]))
	}
	os.Exit(m.Run())
}

// runTestSupervisor owns one managed agent like the AO supervisor. An optional
// start gate keeps it alive without an agent, the state of a healthy launch
// before child.Start.
func runTestSupervisor(args []string) int {
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		return 2
	}
	if gate := os.Getenv("AO_TEST_SUPERVISOR_START_GATE"); gate != "" {
		_ = os.WriteFile(os.Getenv("AO_TEST_SUPERVISOR_STARTING"), []byte("starting"), 0o600)
		for {
			if _, err := os.Stat(gate); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	child := exec.Command(args[1], args[2:]...) //nolint:forbidigo // test stand-in for the AO supervisor
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		return 1
	}
	_ = child.Wait()
	return 0
}
