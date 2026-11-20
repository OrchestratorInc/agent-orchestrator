package chat_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

type outputConversation struct {
	*backgroundConversation
	output ports.ChatBackgroundTaskOutput
	read   []string
}

func (c *outputConversation) BackgroundTaskOutput(_ context.Context, id string) (ports.ChatBackgroundTaskOutput, error) {
	c.read = append(c.read, id)
	return c.output, nil
}

func TestBackgroundTaskOutputReadsTheProviderAndNeverWakesASession(t *testing.T) {
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation()}, output: ports.ChatBackgroundTaskOutput{Text: "tick-3"}}
	h := newHarnessWithConversation(t, conv)
	got, err := h.svc.BackgroundTaskOutput(context.Background(), testSession, "shell")
	if err != nil || got.Text != "tick-3" || len(conv.read) != 1 || conv.read[0] != "shell" {
		t.Fatalf("output=%+v read=%v err=%v", got, conv.read, err)
	}
	if _, err := h.svc.BackgroundTaskOutput(context.Background(), "missing-session", "shell"); !errors.Is(err, ports.ErrSessionNotFound) {
		t.Fatalf("missing session err=%v", err)
	}
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTaskOutput(context.Background(), testSession, "shell"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) || len(conv.read) != 1 {
		t.Fatalf("stopped controller err=%v read=%v", err, conv.read)
	}
}

func TestBackgroundTaskOutputIsUnavailableWithoutAReader(t *testing.T) {
	h := newHarnessWithConversation(t, &backgroundConversation{fakeConversation: newFakeConversation()})
	if _, err := h.svc.BackgroundTaskOutput(context.Background(), testSession, "shell"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestBackgroundTaskOutputShowsTheLogACommandWritesTo(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	task := ports.ChatBackgroundTask{ID: "shell", Kind: "shell", State: "running", Command: "npm run dev > dev.log 2>&1", StartedAt: &started}

	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{task}}}
	h := newHarnessWithConversation(t, conv)
	if err := os.WriteFile(filepath.Join(h.workspace, "dev.log"), []byte("ready on :3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Until a reading reports the command, the provider's empty output stands.
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "" {
		t.Fatalf("before a reading: output=%+v err=%v", got, err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "ready on :3000" {
		t.Fatalf("log output=%+v err=%v", got, err)
	}
	// What the task printed itself follows the log: stderr the command left
	// alone, or the marker Claude Code appends when the task is stopped.
	conv.output = ports.ChatBackgroundTaskOutput{Text: "warning: deprecated"}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "ready on :3000\nwarning: deprecated" {
		t.Fatalf("own output=%+v err=%v", got, err)
	}
	conv.output = ports.ChatBackgroundTaskOutput{Text: "\n[killed]"}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "ready on :3000\n[killed]" {
		t.Fatalf("stopped output=%+v err=%v", got, err)
	}

	// A harness that keeps no output still shows the log its command names.
	plain := &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{task}}
	h = newHarnessWithConversation(t, plain)
	if err := os.WriteFile(filepath.Join(h.workspace, "dev.log"), []byte("ready on :3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "ready on :3000" {
		t.Fatalf("no reader: output=%+v err=%v", got, err)
	}
	if _, err := h.svc.BackgroundTaskOutput(ctx, testSession, "other"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
		t.Fatalf("unknown task err=%v", err)
	}
}

// stoppableOutputConversation prints Claude Code's "[killed]" marker once a
// task is stopped.
type stoppableOutputConversation struct {
	*outputConversation
}

func (c *stoppableOutputConversation) StopBackgroundTask(context.Context, string) error {
	c.output = ports.ChatBackgroundTaskOutput{Text: "\n[killed]"}
	return nil
}

func TestBackgroundTaskOutputKeepsTheLogAfterTheTaskLeavesTheInventory(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	task := ports.ChatBackgroundTask{ID: "shell", Kind: "shell", State: "running", Stoppable: true, Command: "npm run dev > server.log 2>&1", StartedAt: &started}
	conv := &stoppableOutputConversation{outputConversation: &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{task}}}}
	h := newHarnessWithConversation(t, conv)
	if err := os.WriteFile(filepath.Join(h.workspace, "server.log"), []byte("ready on :3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.StopBackgroundTask(ctx, testSession, "shell"); err != nil {
		t.Fatal(err)
	}

	// 30 seconds after the stop, the provider's inventory no longer has it.
	conv.tasks = nil
	h.advance(31 * time.Second)
	if reading, err := h.svc.BackgroundTasks(ctx, testSession); err != nil || len(reading.Tasks) != 0 {
		t.Fatalf("expired reading=%+v err=%v", reading, err)
	}
	// Switching back to its open tab reads the log again.
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "ready on :3000\n[killed]" {
		t.Fatalf("output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputKeepsAFinishedRunsLogWhenARestartReusesThePath(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	first := ports.ChatBackgroundTask{ID: "first", Kind: "shell", State: "running", Stoppable: true, Command: "npm run dev > server.log 2>&1", StartedAt: &started}
	conv := &stoppableOutputConversation{outputConversation: &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{first}}}}
	h := newHarnessWithConversation(t, conv)
	logPath := filepath.Join(h.workspace, "server.log")
	if err := os.WriteFile(logPath, []byte("first run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.StopBackgroundTask(ctx, testSession, "first"); err != nil {
		t.Fatal(err)
	}
	// A reading sees the first run settle.
	ended := time.Now()
	first.State, first.EndedAt = "stopped", &ended
	conv.tasks = []ports.ChatBackgroundTask{first}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}

	// The server restarts and rewrites the same log.
	restarted := time.Now()
	second := ports.ChatBackgroundTask{ID: "second", Kind: "shell", State: "running", Command: first.Command, StartedAt: &restarted}
	conv.tasks = []ports.ChatBackgroundTask{first, second}
	if err := os.WriteFile(logPath, []byte("second run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "first"); err != nil || got.Text != "first run ready\n[killed]" {
		t.Fatalf("first run output=%+v err=%v", got, err)
	}
}
