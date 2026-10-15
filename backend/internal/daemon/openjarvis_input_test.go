package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/openjarvis"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sqlitetest "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type lineAgentResolver struct{}

func (lineAgentResolver) Agent(domain.AgentHarness) (ports.Agent, bool) {
	return openjarvis.New(), true
}

type lineInputRuntime struct {
	mu       sync.Mutex
	screen   string
	messages []string
}

func (r *lineInputRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.screen, nil
}
func (r *lineInputRuntime) SendMessage(_ context.Context, _ ports.RuntimeHandle, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, message)
	go func() { time.Sleep(50 * time.Millisecond); r.mu.Lock(); r.screen = "You> " + message; r.mu.Unlock() }()
	return nil
}
func TestOpenJarvisEveryGuardedWriteRequiresCurrentEmptyComposer(t *testing.T) {
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/repo/p", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessOpenJarvis, Activity: domain.Activity{State: domain.ActivityIdle}, Metadata: domain.SessionMetadata{RuntimeHandleID: "pane", RuntimeLaunchID: "generation"}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &lineInputRuntime{screen: "You> Confirm: Allow shell_exec?\n[y/N]"}
	messenger := newSessionMessenger(store, runtime, nil, lineAgentResolver{})
	if err := messenger.Send(ctx, rec.ID, "yes"); err == nil {
		t.Fatal("answered native decision using stale durable idle")
	}
	runtime.screen = "You> "
	if err := messenger.Send(ctx, rec.ID, "first\nsecond"); err == nil {
		t.Fatal("multiline crossed shared writer")
	}
	if len(runtime.messages) != 0 {
		t.Fatal("refused messages wrote bytes")
	}
	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := messenger.Send(ctx, rec.ID, "plain task"); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 || len(runtime.messages) != 1 {
		t.Fatalf("competing writes: successes=%d messages=%v", successes, runtime.messages)
	}
}
