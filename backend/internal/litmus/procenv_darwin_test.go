package litmus

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func TestProcessEnvironReadsDarwinChildMarker(t *testing.T) {
	const entry = "LITMUS_LEG_MARK=0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name, path string
		args       []string
		wantMarker bool
	}{
		{"test binary", os.Args[0], []string{"-test.run=^TestProcessEnvironHelper$"}, true},
		{"platform binary", "/bin/sleep", []string{"5"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := process.Command(tc.path, tc.args...)
			cmd.Env = []string{entry, "LITMUS_PROCENV_HELPER=1"}
			// An argv entry resembling an environment variable must stay out of environ.
			cmd.Args[0] = "LITMUS_LEG_MARK=argv-only"
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := cmd.Process.Kill(); err != nil {
					t.Errorf("kill test child: %v", err)
				}
				var exit *exec.ExitError
				if err := cmd.Wait(); !errors.As(err, &exit) {
					t.Errorf("wait for killed test child: %v", err)
				}
			})
			env, err := processEnviron(cmd.Process.Pid)
			if err != nil || slices.Contains(env, entry) != tc.wantMarker || slices.Contains(env, cmd.Args[0]) {
				t.Fatalf("child environment = %v, error = %v, want marker = %t and no argv", env, err, tc.wantMarker)
			}
		})
	}
}

func TestProcessEnvironHelper(t *testing.T) {
	if os.Getenv("LITMUS_PROCENV_HELPER") == "1" {
		time.Sleep(5 * time.Second)
	}
}
