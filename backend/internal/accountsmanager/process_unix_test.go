//go:build !windows

package accountsmanager

import (
	"os/exec"
	"testing"
)

func TestConfigureRunnerProcessCreatesIndependentSession(t *testing.T) {
	t.Parallel()

	command := exec.Command("true")
	configureRunnerProcess(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.Setsid {
		t.Fatal("runner process was not configured with an independent session")
	}
}
