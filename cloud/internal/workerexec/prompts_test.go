package workerexec

import (
	"strings"
	"testing"
)

func TestStandingPromptsChooseDelegationByWork(t *testing.T) {
	shared := []string{
		"Choose how to get help by the work",
		"subagents or for AO workers, do that within these",
		"is not an AO worker session; never report one as a started worker",
	}
	workerOnly := []string{
		"Do small or step-by-step work yourself",
		"bounded side task that only reads and reports back",
		"if available; otherwise do it yourself",
		"Native subagents share this workspace: never let one edit files while you or another subagent are editing",
		"needs its own branch or PR, should outlive this task, or should be visible on the board",
	}
	for name, tc := range map[string]struct {
		got  string
		want []string
	}{
		"orchestrator": {orchestratorSystemPrompt("/skills"), append(append([]string{}, shared...),
			"may only assist read-only investigation that helps you",
			"Implementation, fixes, and tests always go to AO workers",
			"Treat this session as coordination-only",
			"A provider-native subagent or a name such as /root/task is not an AO worker session",
		)},
		"worker without orchestrator": {workerSystemPrompt("/skills", false), append(append(append([]string{}, shared...), workerOnly...),
			"No orchestrator is attached, so tell the human and continue what you can in this session",
		)},
		"worker with orchestrator": {workerSystemPrompt("/skills", true), append(append(append([]string{}, shared...), workerOnly...),
			"ask the orchestrator to spawn one with a one-line `ao report`",
		)},
	} {
		got := strings.Join(strings.Fields(tc.got), " ")
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Fatalf("%s prompt missing %q:\n%s", name, want, tc.got)
			}
		}
		for _, banned := range []string{
			"Do not use your own runtime's built-in subagent",
			"Do not use your runtime's built-in subagent",
			"AO workers only",
			"%!",
		} {
			if strings.Contains(got, banned) {
				t.Fatalf("%s prompt contains %q:\n%s", name, banned, tc.got)
			}
		}
	}
	if strings.Contains(workerSystemPrompt("/skills", true), "No orchestrator is attached") {
		t.Fatal("attached cloud worker prompt should not include the standalone fallback")
	}
}
