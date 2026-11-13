package sessionmanager

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func isGeneratedBranch(branch, prefix string, root bool) bool {
	if root {
		var ok bool
		branch, ok = strings.CutSuffix(branch, "/root")
		if !ok {
			return false
		}
	}
	token, ok := strings.CutPrefix(branch, prefix+"-")
	if !ok {
		return false
	}
	_, err := uuid.Parse(token)
	return err == nil
}

func namedBranch(original, slug string) string {
	name := strings.TrimSuffix(original, "/root")
	if len(name) > 36 {
		slug += name[len(name)-37:]
	}
	return "ao/" + slug + "/root"
}

func TestGeneratedBranchesDifferAcrossInstallations(t *testing.T) {
	for _, kind := range []domain.ProjectKind{domain.ProjectKindSingleRepo, domain.ProjectKindWorkspace} {
		t.Run(string(kind), func(t *testing.T) {
			a := DefaultSpawnBranch("example-42", domain.KindWorker, "example", kind, t.TempDir())
			b := DefaultSpawnBranch("example-42", domain.KindWorker, "example", kind, t.TempDir())
			if a == b || !isGeneratedBranch(a, "ao/example-42", kind != domain.ProjectKindWorkspace) || !isGeneratedBranch(b, "ao/example-42", kind != domain.ProjectKindWorkspace) {
				t.Fatalf("branches do not have independent tokens: %q %q", a, b)
			}
		})
	}
}
