package scheduler

import (
	"context"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// 成功 Claim 会移除 READY 行；分页若仍计入已移除行，下一轮会跳过更优先的候选。
func TestReadyRemovalPreservesNextCandidate(t *testing.T) {
	f := newFixture(t)
	f.policy("", 10)
	f.policy(domain.HarnessCodex, 10)
	in := workflow.CreateInput{Title: "Priority paging", Goal: "Preserve priority after successful claims", Repositories: []workflow.RepositoryInput{{RepoKey: "repo", ProjectID: "project"}}}
	for i := 0; i < 6; i++ {
		in.Tasks = append(in.Tasks, workflow.TaskInput{ID: domain.WorkflowTaskID(fmt.Sprintf("rank-%d", i)), Title: "Ranked task", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex, Priority: 100 - int64(i)})
	}
	w, _, err := f.workflow.Create(context.Background(), "review-priority", in)
	if err != nil {
		t.Fatal(err)
	}
	w, _, err = f.workflow.Activate(context.Background(), w.ID, w.Revision, "review-priority-activate")
	if err != nil {
		t.Fatal(err)
	}
	first := f.tick()
	if len(first.Dispatches) != 1 {
		t.Fatalf("first dispatch=%+v", first)
	}
	remaining, err := f.store.ListSchedulerCandidates(context.Background(), w.ID, f.clock.Now(), 5, 0)
	if err != nil || len(remaining) != 5 {
		t.Fatalf("remaining=%+v error=%v", remaining, err)
	}
	second := f.tick()
	if len(second.Dispatches) != 1 || second.Dispatches[0].TaskID != remaining[0].ID {
		t.Fatalf("READY row removal skipped the next ordered task: expected=%s got=%+v", remaining[0].ID, second.Dispatches)
	}
}
