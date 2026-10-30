package telemetrymeta

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  domain.SessionRecord
		want string
	}{
		{"standalone worker", domain.SessionRecord{Kind: domain.KindWorker}, "standalone"},
		{"project worker", domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker}, "worker"},
		{"project orchestrator", domain.SessionRecord{ProjectID: "p", Kind: domain.KindOrchestrator}, "orchestrator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SessionKind(tc.rec); got != tc.want {
				t.Fatalf("SessionKind = %q, want %q", got, tc.want)
			}
		})
	}
}
