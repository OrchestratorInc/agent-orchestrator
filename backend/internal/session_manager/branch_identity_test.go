package sessionmanager

import (
	"context"
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
	suffix, ok := strings.CutPrefix(branch, prefix+"-")
	return ok && uuid.Validate(suffix) == nil
}

func TestGeneratedBranchesDifferAcrossInstallations(t *testing.T) {
	for _, kind := range []domain.ProjectKind{domain.ProjectKindSingleRepo, domain.ProjectKindWorkspace} {
		t.Run(string(kind), func(t *testing.T) {
			first := DefaultSpawnBranch("p-1", domain.KindWorker, "p", kind, t.TempDir())
			second := DefaultSpawnBranch("p-1", domain.KindWorker, "p", kind, t.TempDir())
			root := kind != domain.ProjectKindWorkspace
			if first == second || !isGeneratedBranch(first, "ao/p-1", root) || !isGeneratedBranch(second, "ao/p-1", root) {
				t.Fatalf("worker namespaces are not unique: %q, %q", first, second)
			}
		})
	}
	if got := DefaultOrchestratorBranch("p", t.TempDir()); got != "ao/p-orchestrator" {
		t.Fatalf("canonical orchestrator branch = %q", got)
	}
	if got := DefaultSpawnBranch("p-1", domain.KindOrchestrator, "p", domain.ProjectKindWorkspace, t.TempDir()); got != "ao/p-1" {
		t.Fatalf("workspace orchestrator branch = %q", got)
	}
}

func TestTaskNamesDifferAcrossInstallations(t *testing.T) {
	var branches [2]string
	for i := range branches {
		m, st, _, _ := newGitTaskPreparationManager(t)
		m.dataDir = t.TempDir()
		m.chat = &taskNamingLauncher{result: `{"title":"Fix login","slug":"fix-login"}`}
		cfg := asyncChatSpawnConfig("Fix login")
		cfg.NameFromPrompt, cfg.Async = true, false
		rec, _, _, err := m.Spawn(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		actual := st.sessions[rec.ID]
		branches[i] = actual.Metadata.Branch
		if rec.ID != "mer-1" || !isGeneratedBranch(branches[i], "ao/fix-login", true) || !isGeneratedBranch(actual.TemporaryBranch, "ao/mer-1", true) {
			t.Fatalf("named worker = %+v", actual)
		}
	}
	if branches[0] == branches[1] {
		t.Fatalf("same task name reused namespace %q", branches[0])
	}
}
