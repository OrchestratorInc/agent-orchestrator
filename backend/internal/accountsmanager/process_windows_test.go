//go:build windows

package accountsmanager

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureRunnerProcessCreatesIndependentGroup(t *testing.T) {
	t.Parallel()

	command := exec.Command("cmd.exe", "/c", "exit", "0")
	configureRunnerProcess(command)
	if command.SysProcAttr == nil || command.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("runner process was not configured with an independent process group")
	}
}
