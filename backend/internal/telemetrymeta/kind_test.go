package telemetrymeta

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionKind(t *testing.T) {
	tests := []struct {
		name      string
		kind      domain.SessionKind
		projectID domain.ProjectID
		want      string
	}{
		{name: "standalone", kind: domain.KindWorker, want: "standalone"},
		{name: "worker", kind: domain.KindWorker, projectID: "project", want: "worker"},
		{name: "orchestrator", kind: domain.KindOrchestrator, projectID: "project", want: "orchestrator"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionKind(tt.kind, tt.projectID); got != tt.want {
				t.Fatalf("SessionKind(%q, %q) = %q, want %q", tt.kind, tt.projectID, got, tt.want)
			}
		})
	}
}
