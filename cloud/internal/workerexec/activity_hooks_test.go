package workerexec

import (
	"strings"
	"testing"
)

func TestCodexActivityHooksInstallInterrupt(t *testing.T) {
	args := codexActivityHookArgs("/usr/local/bin/ao")
	for _, arg := range args {
		if strings.Contains(arg, "hooks.Interrupt=") && strings.Contains(arg, "ao hooks codex interrupt") {
			return
		}
	}
	t.Fatalf("Codex launch args %q do not install the interrupt activity hook", args)
}

func TestClaudeActivityHooksInstallStopFailure(t *testing.T) {
	settings := map[string]any{}
	installClaudeActivityHooks("/usr/local/bin/ao", settings)
	hooks, _ := settings["hooks"].(map[string]any)
	if _, ok := hooks["StopFailure"]; !ok {
		t.Fatalf("Claude hooks %v do not report a turn that ended on an API error", hooks)
	}
}
