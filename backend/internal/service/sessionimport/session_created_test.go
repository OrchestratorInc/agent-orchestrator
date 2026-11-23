package sessionimport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestImportReportsEachSessionItCreates(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, aiderImportHistory), "#### make a greeting\n\nHello\n")
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{}
	var created []string
	svc.SetSessionCreated(func(_ context.Context, sessionID string) error {
		created = append(created, sessionID)
		return nil
	})
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %+v, %v", preview, err)
	}
	ids := []string{preview.Candidates[0].ID}
	results, err := svc.Import(context.Background(), ids)
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %+v, %v", results, err)
	}
	if len(created) != 1 || created[0] != string(results[0].SessionID) {
		t.Fatalf("reported %v, want the created session %s", created, results[0].SessionID)
	}
	preview, err = svc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range preview.Candidates {
		if _, err := svc.Import(context.Background(), []string{candidate.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if len(created) != 1 {
		t.Errorf("a session that was already imported must not be reported again: %v", created)
	}
}
