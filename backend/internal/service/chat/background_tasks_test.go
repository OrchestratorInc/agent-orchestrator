package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type backgroundConversation struct {
	*fakeConversation
	tasks      []ports.ChatBackgroundTask
	err        error
	calls      int
	duringRead func()
}

func (c *backgroundConversation) BackgroundTasks(context.Context) ([]ports.ChatBackgroundTask, error) {
	c.calls++
	if c.duringRead != nil {
		c.duringRead()
	}
	return c.tasks, c.err
}

func TestBackgroundTasksSurviveTurnSettlementAndShareControllerCache(t *testing.T) {
	conv := &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{{ID: "shell", Kind: "shell", State: "running"}}}
	h := newHarnessWithConversation(t, conv)
	for range 2 {
		reading, err := h.svc.BackgroundTasks(context.Background(), testSession)
		if err != nil || reading.Availability != "ready" || len(reading.Tasks) != 1 || reading.Tasks[0].ID != "shell" {
			t.Fatalf("reading=%+v err=%v", reading, err)
		}
	}
	if conv.calls != 1 {
		t.Fatalf("provider reads=%d; want shared cache", conv.calls)
	}
	h.advance(2 * time.Second)
	conv.tasks = nil
	reading, err := h.svc.BackgroundTasks(context.Background(), testSession)
	if err != nil || reading.Availability != "ready" || reading.Tasks == nil || len(reading.Tasks) != 0 {
		t.Fatalf("empty reading=%+v err=%v", reading, err)
	}
}

func TestBackgroundTasksUnavailableDoesNotWakeAController(t *testing.T) {
	h := newHarness(t)
	reading, err := h.svc.BackgroundTasks(context.Background(), testSession)
	if err != nil || reading.Availability != "unavailable" {
		t.Fatalf("unsupported=%+v err=%v", reading, err)
	}
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatal(err)
	}
	reading, err = h.svc.BackgroundTasks(context.Background(), testSession)
	if err != nil || reading.Availability != "unavailable" {
		t.Fatalf("stopped=%+v err=%v", reading, err)
	}
}

func TestBackgroundTasksRejectControllerReplacementDuringRead(t *testing.T) {
	conv := &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{{ID: "old", State: "running"}}}
	h := newHarnessWithConversation(t, conv)
	conv.duringRead = func() {
		rec, _, err := h.st.GetSession(context.Background(), testSession)
		if err != nil {
			t.Fatal(err)
		}
		rec.Metadata.ControllerGeneration = "new-generation"
		if err := h.st.UpdateSession(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	reading, err := h.svc.BackgroundTasks(context.Background(), testSession)
	if err != nil || reading.Availability != "unavailable" || len(reading.Tasks) != 0 {
		t.Fatalf("stale=%+v err=%v", reading, err)
	}
}

func TestBackgroundTasksRecentExpiryAndProviderErrors(t *testing.T) {
	conv := &backgroundConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, conv)
	ended := h.now()
	conv.tasks = []ports.ChatBackgroundTask{{ID: "agent", Kind: "agent", State: "completed", EndedAt: &ended}}
	reading, err := h.svc.BackgroundTasks(context.Background(), testSession)
	if err != nil || len(reading.Tasks) != 1 {
		t.Fatalf("recent=%+v err=%v", reading, err)
	}
	h.advance(31 * time.Second)
	reading, err = h.svc.BackgroundTasks(context.Background(), testSession)
	if err != nil || len(reading.Tasks) != 0 {
		t.Fatalf("expired=%+v err=%v", reading, err)
	}
	h.advance(2 * time.Second)
	conv.err = errors.New("provider disconnected")
	if _, err = h.svc.BackgroundTasks(context.Background(), testSession); err == nil {
		t.Fatal("provider failure became a zero")
	}
}

func TestBackgroundTasksPartialReadingIsNotCompleteOrUnavailable(t *testing.T) {
	conv := &backgroundConversation{fakeConversation: newFakeConversation(), err: ports.ErrBackgroundTasksPartial, tasks: []ports.ChatBackgroundTask{{ID: "job", Kind: "shell", State: "running"}}}
	h := newHarnessWithConversation(t, conv)
	for range 2 {
		reading, err := h.svc.BackgroundTasks(context.Background(), testSession)
		if err != nil || reading.Availability != "partial" || len(reading.Tasks) != 1 {
			t.Fatalf("reading=%+v err=%v", reading, err)
		}
	}
	if conv.calls != 1 {
		t.Fatal("partial reading bypassed controller cache")
	}
}

type stoppableConversation struct {
	*backgroundConversation
	stopped []string
	stopErr error
}

func (c *stoppableConversation) StopBackgroundTask(_ context.Context, id string) error {
	c.stopped = append(c.stopped, id)
	return c.stopErr
}

func TestStopBackgroundTaskStopsThroughTheProviderAndRereadsAtOnce(t *testing.T) {
	conv := &stoppableConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{{ID: "shell", Kind: "shell", State: "running", Stoppable: true}}}}
	h := newHarnessWithConversation(t, conv)
	if _, err := h.svc.BackgroundTasks(context.Background(), testSession); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.StopBackgroundTask(context.Background(), testSession, "shell"); err != nil {
		t.Fatal(err)
	}
	if len(conv.stopped) != 1 || conv.stopped[0] != "shell" {
		t.Fatalf("stopped=%v", conv.stopped)
	}
	// Within the one-second sample window, a stop still forces a fresh read.
	conv.tasks = nil
	reading, err := h.svc.BackgroundTasks(context.Background(), testSession)
	if err != nil || len(reading.Tasks) != 0 || conv.calls != 2 {
		t.Fatalf("reading=%+v calls=%d err=%v", reading, conv.calls, err)
	}
	conv.stopErr = ports.ErrBackgroundTaskNotStoppable
	if err := h.svc.StopBackgroundTask(context.Background(), testSession, "shell"); !errors.Is(err, ports.ErrBackgroundTaskNotStoppable) {
		t.Fatalf("settled task err=%v", err)
	}
}

func TestStopBackgroundTaskRefusesProvidersWithoutStopAndNeverWakesASession(t *testing.T) {
	conv := &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{{ID: "shell", Kind: "shell", State: "running"}}}
	h := newHarnessWithConversation(t, conv)
	if err := h.svc.StopBackgroundTask(context.Background(), testSession, "shell"); !errors.Is(err, ports.ErrBackgroundTaskNotStoppable) {
		t.Fatalf("no stop control err=%v", err)
	}
	if err := h.svc.StopBackgroundTask(context.Background(), "missing-session", "shell"); !errors.Is(err, ports.ErrSessionNotFound) {
		t.Fatalf("missing session err=%v", err)
	}
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.StopBackgroundTask(context.Background(), testSession, "shell"); !errors.Is(err, ports.ErrBackgroundTaskNotStoppable) {
		t.Fatalf("stopped controller err=%v", err)
	}
}
