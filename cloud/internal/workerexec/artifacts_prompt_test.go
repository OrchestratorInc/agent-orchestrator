package workerexec

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestSystemPromptArtifactSections(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.ToSlash(worker.ArtifactsDir(dataDir, "session-1"))
	cases := []struct {
		name        string
		launch      worker.LaunchContext
		wantSection bool
		wantDeliver bool
	}{
		{"artifacts off", worker.LaunchContext{SessionID: "session-1", Kind: "worker", RequestedOutput: domain.RequestedOutputArtifact}, false, false},
		{"worker", worker.LaunchContext{SessionID: "session-1", Kind: "worker", Artifacts: true}, true, false},
		{"artifact worker", worker.LaunchContext{SessionID: "session-1", Kind: "worker", Artifacts: true, RequestedOutput: domain.RequestedOutputArtifact}, true, true},
		{"orchestrator", worker.LaunchContext{SessionID: "session-1", Kind: "orchestrator", Artifacts: true, RequestedOutput: domain.RequestedOutputArtifact}, true, false},
		{"reviewer", worker.LaunchContext{SessionID: "session-1", Kind: "reviewer", Artifacts: true}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt := HarnessBuilder{DataDir: dataDir}.systemPrompt(tc.launch, "/work")
			if got := strings.Contains(prompt, "## Session Artifacts") && strings.Contains(prompt, dir); got != tc.wantSection {
				t.Fatalf("artifacts section present = %v, want %v:\n%s", got, tc.wantSection, prompt)
			}
			if got := strings.Contains(prompt, "## Artifact Output"); got != tc.wantDeliver {
				t.Fatalf("artifact output section present = %v, want %v", got, tc.wantDeliver)
			}
		})
	}
}
