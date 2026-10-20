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
	sent       chan string
}

func (r *lineInputRuntime) GetStyledOutput(_ context.Context, _ ports.RuntimeHandle, lines int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := strings.Split(r.screen, "\n")
	if lines > 0 && len(rows) > lines {
		return strings.Join(rows[len(rows)-lines:], "\n"), nil
	}
	return r.screen, nil
}
func (r *lineInputRuntime) SendMessage(_ context.Context, _ ports.RuntimeHandle, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sendErr != nil {
		return r.sendErr
	}
	r.messages = append(r.messages, message)
	if r.sent != nil {
		r.sent <- message
	}
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

func TestHermesAdmissionKeepsPendingAfterAmbiguousSendError(t *testing.T) {
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
	rule := strings.Repeat("─", 40)
	idle := rule + "\n❯ \n" + rule
	runtime := &lineInputRuntime{screen: idle, sendErr: errors.New("tmux acknowledged after applying input")}
	messenger := newSessionMessenger(store, runtime, nil, lineAgentResolver{})
	if err := messenger.Send(ctx, rec.ID, "first"); err == nil {
		t.Fatal("write failure ignored")
	}
	// The write may have landed: an unchanged frame must not admit a retry.
	runtime.sendErr = nil
	if err := messenger.Send(ctx, rec.ID, "first"); err == nil || !strings.Contains(err.Error(), "prior terminal submission remains unconfirmed") {
		t.Fatalf("ambiguous write admitted a retry: %v", err)
	}
	if len(runtime.messages) != 0 {
		t.Fatalf("retry was written: %v", runtime.messages)
	}
	// Hermes's committed echo of the first write releases admission.
	runtime.screen = rule + "\n● first\n" + rule + "\n" + idle
	if err := messenger.Send(ctx, rec.ID, "second"); err != nil {
		t.Fatalf("native submission witness did not release admission: %v", err)
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
	runtime := &lineInputRuntime{screen: idle, nextScreen: "☤ ❯ msg=interrupt · Ctrl+C cancel"}
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
	if err := messenger.Send(ctx, rec.ID, "second"); err == nil || !strings.Contains(err.Error(), "prior terminal submission remains unconfirmed") {
		t.Fatalf("changed idle screen released unconfirmed submission: %v", err)
	}
	if len(runtime.messages) != 1 {
		t.Fatalf("redraw wrote into an unconfirmed composer: %v", runtime.messages)
	}
	rule := strings.Repeat("─", 40)
	runtime.screen = rule + "\n● first\n" + rule + "\nfirst turn complete\n" + idle
	runtime.holdScreen = false
	runtime.nextScreen = "☤ ❯ msg=interrupt · Ctrl+C cancel"
	if err := messenger.Send(ctx, rec.ID, "second"); err != nil {
		t.Fatalf("confirmed prior submission blocked input after returning idle: %v", err)
	}
	if len(runtime.messages) != 2 || runtime.messages[1] != "second" {
		t.Fatalf("unexpected writes after timeout recovery: %v", runtime.messages)
	}
}

func TestHermesAdmissionRequiresNativeWitnessAfterTimeout(t *testing.T) {
	rule := strings.Repeat("─", 40)
	idle := rule + "\n❯ \n" + rule
	longMessage := strings.Repeat("a long task prompt ", 10)
	echo := "● " + longMessage
	wrappedEcho := echo[:50] + "\n" + echo[50:100] + "\n" + echo[100:]
	tests := []struct {
		name   string
		screen string
		allow  bool
	}{
		{name: "wrapped echo", screen: rule + "\n" + wrappedEcho + "\n" + rule + "\n" + idle, allow: true},
		{name: "echo beyond old 40 line window", screen: rule + "\n" + echo + "\n" + rule + "\n" + strings.Repeat("completed output\n", 60) + idle, allow: true},
		{name: "echo beyond capture history", screen: rule + "\n" + echo + "\n" + rule + "\n" + strings.Repeat("completed output\n", 220) + idle},
		{name: "unrelated status redraw", screen: "status changed\n" + idle},
		{name: "unchanged screen", screen: idle},
		{name: "text remains in composer", screen: "completed output\n" + rule + "\n❯ still composing\n" + rule},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			runtime := &lineInputRuntime{screen: tt.screen, nextScreen: "☤ ❯ msg=interrupt · Ctrl+C cancel"}
			messenger := newSessionMessenger(store, runtime, nil, lineAgentResolver{}).(runtimeMessenger)
			messenger.inputLocks.Store(rec.ID, &terminalMessageAdmission{
				pending: longMessage, baseline: idle, handleID: "pane", launchID: "generation",
			})
			err = messenger.Send(ctx, rec.ID, "next task")
			if tt.allow && err != nil {
				t.Fatalf("completed turn left admission blocked: %v", err)
			}
			if !tt.allow && (err == nil || len(runtime.messages) != 0) {
				t.Fatalf("unconfirmed submission admitted another write: err=%v messages=%v", err, runtime.messages)
			}
			if tt.allow && (len(runtime.messages) != 1 || runtime.messages[0] != "next task") {
				t.Fatalf("unexpected writes after recovery: %v", runtime.messages)
			}
		})
	}
}

func TestHermesAdmissionConfirmsFastTurnWithoutBusyFrame(t *testing.T) {
	for _, message := range []string{"first task", "/help"} {
		t.Run(message, func(t *testing.T) {
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
			rule := strings.Repeat("─", 40)
			idle := rule + "\n❯ \n" + rule
			echo := rule + "\n● " + message + "\n" + rule + "\n"
			if strings.HasPrefix(message, "/") {
				echo = "⚙️  " + message + "\nhelp output\n"
			}
			runtime := &lineInputRuntime{screen: idle, nextScreen: echo + idle}
			messenger := newSessionMessenger(store, runtime, nil, lineAgentResolver{})
			if err := messenger.Send(ctx, rec.ID, message); err != nil {
				t.Fatalf("committed native echo did not confirm fast submission: %v", err)
			}
			runtime.nextScreen = "☤ ❯ msg=interrupt · Ctrl+C cancel"
			if err := messenger.Send(ctx, rec.ID, "second task"); err != nil {
				t.Fatalf("fast submission left admission blocked: %v", err)
			}
			if len(runtime.messages) != 2 || runtime.messages[0] != message || runtime.messages[1] != "second task" {
				t.Fatalf("unexpected terminal writes: %v", runtime.messages)
			}
		})
	}
}

func TestHermesAdmissionRejectsIdleRedrawDuringConcurrentSend(t *testing.T) {
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
	runtime := &lineInputRuntime{screen: idle, nextScreen: "unrelated status redraw\n" + idle, sent: make(chan string, 2)}
	messenger := newSessionMessenger(store, runtime, nil, lineAgentResolver{})
	firstCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() { firstDone <- messenger.Send(firstCtx, rec.ID, "first task") }()
	select {
	case <-runtime.sent:
	case <-time.After(5 * time.Second):
		t.Fatal("first submission was not written")
	}
	secondDone := make(chan error, 1)
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		secondDone <- messenger.Send(ctx, rec.ID, "second task")
	}()
	<-secondStarted
	cancel()
	firstErr := <-firstDone
	secondErr := <-secondDone
	if firstErr == nil || !strings.Contains(firstErr.Error(), "unconfirmed") {
		t.Fatalf("idle redraw falsely confirmed first submission: %v", firstErr)
	}
	if secondErr == nil || !strings.Contains(secondErr.Error(), "prior terminal submission remains unconfirmed") {
		t.Fatalf("concurrent send crossed the unconfirmed submission: %v", secondErr)
	}
	if len(runtime.messages) != 1 || runtime.messages[0] != "first task" {
		t.Fatalf("idle redraw admitted additional terminal writes: %v", runtime.messages)
	}
}
