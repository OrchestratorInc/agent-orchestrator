package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hermes"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sqlitetest "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type lineAgentResolver struct{}

func (lineAgentResolver) Agent(domain.AgentHarness) (ports.Agent, bool) {
	return hermes.New(), true
}

type lineInputRuntime struct {
	mu         sync.Mutex
	screen     string
	messages   []string
	sendErr    error
	nextScreen string
	holdScreen bool
}

func (r *lineInputRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.screen, nil
}
func (r *lineInputRuntime) SendMessage(_ context.Context, _ ports.RuntimeHandle, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sendErr != nil {
		return r.sendErr
	}
	r.messages = append(r.messages, message)
	if r.holdScreen {
		return nil
	}
	if r.nextScreen != "" {
		r.screen = r.nextScreen
		return nil
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		r.mu.Lock()
		r.screen = "☤ ❯ msg=interrupt · Ctrl+C cancel"
		r.mu.Unlock()
	}()
	return nil
}
func TestHermesEveryGuardedWriteRequiresCurrentEmptyComposer(t *testing.T) {
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/repo/p", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessHermes, Activity: domain.Activity{State: domain.ActivityIdle}, Metadata: domain.SessionMetadata{RuntimeHandleID: "pane", RuntimeLaunchID: "generation"}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &lineInputRuntime{screen: "Dangerous Command\n⚠ ❯"}
	messenger := newSessionMessenger(store, runtime, nil, lineAgentResolver{})
	if err := messenger.Send(ctx, rec.ID, "yes"); err == nil {
		t.Fatal("answered native decision using stale durable idle")
	}
	if err := messenger.Send(ctx, rec.ID, "task"); err == nil {
		t.Fatal("modal crossed shared writer")
	}
	if len(runtime.messages) != 0 {
		t.Fatal("refused messages wrote bytes")
	}
	runtime.screen = "────────────────────────────────────────\n☤ ❯ \x1b[3mmsg=interrupt · Ctrl+C cancel\x1b[m\n────────────────────────────────────────"
	if err := messenger.Send(ctx, rec.ID, "next task"); err == nil {
		t.Fatal("bordered running composer admitted input using stale durable idle")
	}
	if len(runtime.messages) != 0 {
		t.Fatal("running composer refusal wrote bytes")
	}
	runtime.screen = "────────────────────────────────────────\n❯ \n────────────────────────────────────────"
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

func TestHermesAdmissionRecoversAfterSendErrorAndFastTurn(t *testing.T) {
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/repo/p", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessHermes, Activity: domain.Activity{State: domain.ActivityIdle}, Metadata: domain.SessionMetadata{RuntimeHandleID: "pane", RuntimeLaunchID: "generation"}})
	if err != nil {
		t.Fatal(err)
	}
	idle := "────────────────────────────────────────\n❯ \n────────────────────────────────────────"
	runtime := &lineInputRuntime{screen: idle, sendErr: errors.New("tmux write failed")}
	messenger := newSessionMessenger(store, runtime, nil, lineAgentResolver{})
	if err := messenger.Send(ctx, rec.ID, "first"); err == nil {
		t.Fatal("write failure ignored")
	}
	runtime.sendErr = nil
	runtime.nextScreen = "turn complete\n" + idle
	if err := messenger.Send(ctx, rec.ID, "second"); err != nil {
		t.Fatalf("fast settled turn remained pending: %v", err)
	}
	if len(runtime.messages) != 1 || runtime.messages[0] != "second" {
		t.Fatalf("unexpected writes: %v", runtime.messages)
	}
}

func TestHermesAdmissionDropsPriorRuntimeGeneration(t *testing.T) {
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/repo/p", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessHermes, Activity: domain.Activity{State: domain.ActivityIdle}, Metadata: domain.SessionMetadata{RuntimeHandleID: "new-pane", RuntimeLaunchID: "new-launch"}})
	if err != nil {
		t.Fatal(err)
	}
	idle := "────────────────────────────────────────\n❯ \n────────────────────────────────────────"
	runtime := &lineInputRuntime{screen: idle, nextScreen: "new turn complete\n" + idle}
	messenger := newSessionMessenger(store, runtime, nil, lineAgentResolver{}).(runtimeMessenger)
	messenger.inputLocks.Store(rec.ID, &terminalMessageAdmission{pending: "old task", baseline: idle, handleID: "old-pane", launchID: "old-launch"})
	if err := messenger.Send(ctx, rec.ID, "new task"); err != nil {
		t.Fatalf("prior runtime generation blocked input: %v", err)
	}
	if len(runtime.messages) != 1 || runtime.messages[0] != "new task" {
		t.Fatalf("unexpected writes: %v", runtime.messages)
	}
}

func TestHermesAdmissionRecoversAfterEchoTimeout(t *testing.T) {
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/repo/p", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessHermes, Activity: domain.Activity{State: domain.ActivityIdle}, Metadata: domain.SessionMetadata{RuntimeHandleID: "pane", RuntimeLaunchID: "generation"}})
	if err != nil {
		t.Fatal(err)
	}
	idle := "────────────────────────────────────────\n❯ \n────────────────────────────────────────"
	runtime := &lineInputRuntime{screen: idle, holdScreen: true}
	messenger := newSessionMessenger(store, runtime, nil, lineAgentResolver{})
	if err := messenger.Send(ctx, rec.ID, "first"); err == nil || !strings.Contains(err.Error(), "native echo unconfirmed") {
		t.Fatalf("submission with no observed echo did not time out: %v", err)
	}
	if len(runtime.messages) != 1 {
		t.Fatalf("timed-out submission was not written exactly once: %v", runtime.messages)
	}
	if err := messenger.Send(ctx, rec.ID, "second"); err == nil || !strings.Contains(err.Error(), "prior terminal submission remains unconfirmed") {
		t.Fatalf("unchanged screen allowed another submission: %v", err)
	}
	runtime.screen = "first turn complete\n" + idle
	runtime.holdScreen = false
	runtime.nextScreen = "second turn complete\n" + idle
	if err := messenger.Send(ctx, rec.ID, "second"); err != nil {
		t.Fatalf("changed idle screen did not release pending admission: %v", err)
	}
	if len(runtime.messages) != 2 || runtime.messages[1] != "second" {
		t.Fatalf("unexpected writes after timeout recovery: %v", runtime.messages)
	}
}
