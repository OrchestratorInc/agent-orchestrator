package project_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type slowTeardown struct{}

func (slowTeardown) TeardownProject(ctx context.Context, _ domain.ProjectID) error {
	<-ctx.Done() // teardown consumed the whole request budget
	return nil
}

// Teardown can spend the whole request budget; the archive must still land.
func TestManager_RemoveArchivesAfterTeardownExhaustsDeadline(t *testing.T) {
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store, Sessions: slowTeardown{}})
	if _, err := m.Add(context.Background(), project.AddInput{Path: gitRepo(t), ProjectID: ptr("ao")}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = m.Remove(ctx, "ao")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, err = m.Get(context.Background(), "ao")
	wantCode(t, err, "PROJECT_NOT_FOUND")
}
