package session

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// When another worktree holds the orchestrator branch, the spawn moves to
// <branch>-N. Replacement must accept that branch instead of failing a
// running orchestrator with a 500.
func TestVerifyOrchestratorReplacementBranch(t *testing.T) {
	s := &Service{dataDir: t.TempDir()}
	project := domain.ProjectRecord{ID: "repro"}
	session := func(branch string) domain.Session {
		var sess domain.Session
		sess.ID = "repro-4"
		sess.Kind = domain.KindOrchestrator
		sess.Metadata.Branch = branch
		return sess
	}

	for _, branch := range []string{"", "ao/repro-orchestrator", "ao/repro-orchestrator-2", "ao/repro-orchestrator-13"} {
		if err := s.verifyOrchestratorReplacement(project, session(branch)); err != nil {
			t.Errorf("branch %q: unexpected error: %v", branch, err)
		}
	}

	for _, branch := range []string{"ao/repro-orchestrator-", "ao/repro-orchestrator-x", "ao/repro-orchestrator2", "ao/other-orchestrator", "main"} {
		err := s.verifyOrchestratorReplacement(project, session(branch))
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "ORCHESTRATOR_REPLACEMENT_UNVERIFIED" {
			t.Errorf("branch %q: got %v, want ORCHESTRATOR_REPLACEMENT_UNVERIFIED", branch, err)
		}
	}
}
