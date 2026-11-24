package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
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

func TestBackgroundTasksFailedReadBacksOffLongerThanTimeout(t *testing.T) {
	conv := &backgroundConversation{fakeConversation: newFakeConversation(), err: errors.New("provider hung")}
	h := newHarnessWithConversation(t, conv)
	for range 3 {
		if _, err := h.svc.BackgroundTasks(context.Background(), testSession); err == nil {
			t.Fatal("provider failure became a reading")
		}
	}
	if conv.calls != 1 {
		t.Fatalf("failed reads in the same second made %d provider calls, want 1", conv.calls)
	}
	h.advance(3 * time.Second)
	if _, err := h.svc.BackgroundTasks(context.Background(), testSession); err == nil || conv.calls != 1 {
		t.Fatalf("failure cache expired before backoff: %v %d", err, conv.calls)
	}
	h.advance(2 * time.Second)
	conv.err = nil
	if reading, err := h.svc.BackgroundTasks(context.Background(), testSession); err != nil || reading.Availability != "ready" || conv.calls != 2 {
		t.Fatalf("reading after the failed sample expired=%+v err=%v calls=%d", reading, err, conv.calls)
	}
}

type backgroundSessionReader struct {
	chatsvc.SessionReader
	fail atomic.Bool
}

func (r *backgroundSessionReader) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if r.fail.Load() && id == testSession {
		return domain.SessionRecord{}, false, errors.New("lookup failed")
	}
	return r.SessionReader.GetSession(ctx, id)
}

func TestBackgroundTasksProjectBatchIsolatesSessionLookupFailure(t *testing.T) {
	st := openStore(t)
	healthyRecord, err := st.CreateSession(t.Context(), domain.SessionRecord{ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	healthy := healthyRecord.ID
	reader := &backgroundSessionReader{SessionReader: st}
	svc := chatsvc.New(chatsvc.Options{NewID: uuid.NewString, Store: st, Reader: fullSnapshotReader(st), Sessions: reader, Drivers: fakeRegistry{driver: fakeDriver{start: func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		base := newFakeConversation()
		base.providerConversationID = string(cfg.SessionID)
		return &backgroundConversation{fakeConversation: base, tasks: []ports.ChatBackgroundTask{{ID: "live", Kind: "shell", State: "running"}}}, nil
	}}}, Log: slog.New(slog.DiscardHandler)})
	for _, id := range []domain.SessionID{testSession, healthy} {
		if _, err := svc.Start(t.Context(), chatsvc.StartConfig{SessionID: id, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reader.fail.Store(false); _ = svc.Stop(context.Background(), id) })
	}
	reader.fail.Store(true)
	readings, err := svc.ListBackgroundTasks(t.Context(), testProject)
	if err != nil || len(readings) != 1 || readings[0].SessionID != healthy || readings[0].Availability != "ready" || len(readings[0].Tasks) != 1 {
		t.Fatalf("readings=%+v err=%v", readings, err)
	}
}

// Holds the first provider read while another reader cancels or stops a task.
type blockingBackgroundConversation struct {
	*fakeConversation
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (c *blockingBackgroundConversation) BackgroundTasks(ctx context.Context) ([]ports.ChatBackgroundTask, error) {
	call := c.calls.Add(1)
	if call == 1 {
		close(c.entered)
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	state := "running"
	if call > 1 {
		state = "stopped"
	}
	now := time.Now()
	return []ports.ChatBackgroundTask{{ID: "shell", Kind: "shell", State: state, EndedAt: &now}}, nil
}
func (c *blockingBackgroundConversation) StopBackgroundTask(context.Context, string) error {
	return nil
}

func TestBackgroundTasksSharedReadWaiterCanCancel(t *testing.T) {
	conv := &blockingBackgroundConversation{fakeConversation: newFakeConversation(), entered: make(chan struct{}), release: make(chan struct{})}
	h := newHarnessWithConversation(t, conv)
	done := make(chan error, 1)
	go func() { _, err := h.svc.BackgroundTasks(t.Context(), testSession); done <- err }()
	<-conv.entered
	ctx, cancel := context.WithCancel(t.Context())
	waiter := make(chan error, 1)
	go func() { _, err := h.svc.BackgroundTasks(ctx, testSession); waiter <- err }()
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("canceled reader waited for provider")
	}
	if conv.calls.Load() != 1 {
		t.Error("concurrent readers duplicated the provider read")
	}
	close(conv.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundTasksStopDoesNotWaitOrCachePreStopRead(t *testing.T) {
	conv := &blockingBackgroundConversation{fakeConversation: newFakeConversation(), entered: make(chan struct{}), release: make(chan struct{})}
	h := newHarnessWithConversation(t, conv)
	done := make(chan error, 1)
	go func() {
		reading, err := h.svc.BackgroundTasks(t.Context(), testSession)
		if err == nil && (len(reading.Tasks) != 1 || reading.Tasks[0].State != "stopped") {
			err = errors.New("published pre-stop read")
		}
		done <- err
	}()
	<-conv.entered
	stopped := make(chan error, 1)
	go func() { stopped <- h.svc.StopBackgroundTask(t.Context(), testSession, "shell") }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("stop waited for provider read lock")
	}
	close(conv.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read did not finish")
	}
	if conv.calls.Load() != 2 {
		t.Fatalf("reads=%d, want a fresh post-stop sample", conv.calls.Load())
	}
}

func TestBackgroundTasksCanceledLeaderDoesNotPoisonNextRead(t *testing.T) {
	conv := &blockingBackgroundConversation{fakeConversation: newFakeConversation(), entered: make(chan struct{}), release: make(chan struct{})}
	h := newHarnessWithConversation(t, conv)
	ctx, cancel := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() { _, err := h.svc.BackgroundTasks(ctx, testSession); first <- err }()
	<-conv.entered
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	reading, err := h.svc.BackgroundTasks(t.Context(), testSession)
	if err != nil || reading.Availability != "ready" || conv.calls.Load() != 2 {
		t.Fatalf("reading=%+v err=%v calls=%d", reading, err, conv.calls.Load())
	}
}

func TestBackgroundTasksConcurrentReadersShareOneSample(t *testing.T) {
	conv := &blockingBackgroundConversation{fakeConversation: newFakeConversation(), entered: make(chan struct{}), release: make(chan struct{})}
	h := newHarnessWithConversation(t, conv)
	results := make(chan error, 8)
	for range 8 {
		go func() {
			reading, err := h.svc.BackgroundTasks(t.Context(), testSession)
			if err == nil && (reading.Availability != "ready" || len(reading.Tasks) != 1 || reading.Tasks[0].State != "running") {
				err = errors.New("shared sample differs")
			}
			results <- err
		}()
	}
	<-conv.entered
	close(conv.release)
	for range 8 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if conv.calls.Load() != 1 {
		t.Fatalf("provider reads=%d, want 1", conv.calls.Load())
	}
}

func TestBackgroundTasksProviderPanicReleasesSharedRead(t *testing.T) {
	conv := &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{{ID: "live", Kind: "shell", State: "running"}}}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	conv.duringRead = func() {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			panic("provider panic")
		}
	}
	h := newHarnessWithConversation(t, conv)
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		_, _ = h.svc.BackgroundTasks(t.Context(), testSession)
	}()
	<-entered
	waiterCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	read := make(chan error, 1)
	go func() {
		reading, err := h.svc.BackgroundTasks(waiterCtx, testSession)
		if err == nil && (reading.Availability != "ready" || len(reading.Tasks) != 1 || reading.Tasks[0].ID != "live") {
			err = errors.New("panic cached an incomplete reading")
		}
		read <- err
	}()
	close(release)
	if got := <-panicked; got != "provider panic" {
		t.Fatalf("panic = %v", got)
	}
	if err := <-read; err != nil {
		t.Fatalf("reader after provider panic: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("provider reads=%d, want a fresh sample after panic", calls.Load())
	}
}

type outputConversation struct {
	*backgroundConversation
	output     ports.ChatBackgroundTaskOutput
	outputErr  error
	read       []string
	duringRead func()
}

func (c *outputConversation) BackgroundTaskOutput(_ context.Context, id string) (ports.ChatBackgroundTaskOutput, error) {
	c.read = append(c.read, id)
	if c.duringRead != nil {
		c.duringRead()
	}
	return c.output, c.outputErr
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

func TestBackgroundTaskOutputProviderFailuresAreUnavailable(t *testing.T) {
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation()}}
	h := newHarnessWithConversation(t, conv)
	for _, err := range []error{errors.New("conversation closed"), context.DeadlineExceeded, errors.New("EACCES")} {
		conv.outputErr = err
		if _, got := h.svc.BackgroundTaskOutput(context.Background(), testSession, "shell"); !errors.Is(got, ports.ErrBackgroundTaskOutputUnavailable) {
			t.Fatalf("%v became %v", err, got)
		}
	}
	// A poll the caller cancelled is a moment without output, not a 500.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, got := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); !errors.Is(got, ports.ErrBackgroundTaskOutputUnavailable) {
		t.Fatalf("canceled read=%v", got)
	}
}

func TestBackgroundTaskOutputRejectsReadsAcrossAGenerationOrHibernation(t *testing.T) {
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation()}, output: ports.ChatBackgroundTaskOutput{Text: "old controller"}}
	h := newHarnessWithConversation(t, conv)
	update := func(change func(*domain.SessionRecord)) {
		rec, _, err := h.st.GetSession(context.Background(), testSession)
		if err != nil {
			t.Fatal(err)
		}
		change(&rec)
		if err := h.st.UpdateSession(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	generation := ""
	update(func(rec *domain.SessionRecord) { generation = rec.Metadata.ControllerGeneration })
	conv.duringRead = func() {
		update(func(rec *domain.SessionRecord) { rec.Metadata.ControllerGeneration = "new-generation" })
	}
	if got, err := h.svc.BackgroundTaskOutput(context.Background(), testSession, "shell"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
		t.Fatalf("read across a handoff=%+v err=%v", got, err)
	}
	conv.duringRead = nil
	reads := len(conv.read)
	if _, err := h.svc.BackgroundTaskOutput(context.Background(), testSession, "shell"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) || len(conv.read) != reads {
		t.Fatalf("generation mismatch read the provider: err=%v", err)
	}
	update(func(rec *domain.SessionRecord) { rec.Metadata.ControllerGeneration = generation })
	if _, err := h.svc.BackgroundTaskOutput(context.Background(), testSession, "shell"); err != nil || len(conv.read) != reads+1 {
		t.Fatalf("current generation: err=%v", err)
	}
	rec, _, err := h.st.GetSession(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if ok, err := h.st.SetSessionHibernated(context.Background(), testSession, rec.Revision, &now); err != nil || !ok {
		t.Fatalf("hibernate: ok=%v err=%v", ok, err)
	}
	reads = len(conv.read)
	if _, err := h.svc.BackgroundTaskOutput(context.Background(), testSession, "shell"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) || len(conv.read) != reads {
		t.Fatalf("hibernated read the provider: err=%v", err)
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

func TestBackgroundTaskOutputKeepsAVanishedRunsLogWhenARestartReusesThePath(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	first := ports.ChatBackgroundTask{ID: "first", Kind: "shell", State: "running", Command: "npm run dev > server.log 2>&1", StartedAt: &started}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{first}}}
	h := newHarnessWithConversation(t, conv)
	logPath := filepath.Join(h.workspace, "server.log")
	if err := os.WriteFile(logPath, []byte("first run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	// The provider drops the terminal while it is still running, as Codex
	// does with one that vanishes: no reading ever sees it settle.
	conv.tasks = nil
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}

	// A restart rewrites the same log.
	restarted := time.Now()
	second := ports.ChatBackgroundTask{ID: "second", Kind: "shell", State: "running", Command: first.Command, StartedAt: &restarted}
	conv.tasks = []ports.ChatBackgroundTask{second}
	if err := os.WriteFile(logPath, []byte("second run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "first"); err != nil || got.Text != "first run ready" {
		t.Fatalf("first run output=%+v err=%v", got, err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "second"); err != nil || got.Text != "second run ready" {
		t.Fatalf("second run output=%+v err=%v", got, err)
	}

	// Listed live again, the first run still doesn't show the log: the later
	// run writes it now, so what is in it is that run's.
	conv.tasks = []ports.ChatBackgroundTask{first, second}
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "first"); err != nil || !got.LogOmitted || got.Text != "" {
		t.Fatalf("listed again output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputDropsALogARestartTookOverBeforeAnyReadingSawTheRunEnd(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	first := ports.ChatBackgroundTask{ID: "first", Kind: "shell", State: "running", Command: "npm run dev > server.log 2>&1", StartedAt: &started}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{first}}, output: ports.ChatBackgroundTaskOutput{Text: "[killed]"}}
	h := newHarnessWithConversation(t, conv)
	logPath := filepath.Join(h.workspace, "server.log")
	if err := os.WriteFile(logPath, []byte("first run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	// While nobody read, the first run ended, aged out, and a restart took
	// over its log.
	restarted := time.Now()
	second := ports.ChatBackgroundTask{ID: "second", Kind: "shell", State: "running", Command: first.Command, StartedAt: &restarted}
	conv.tasks = []ports.ChatBackgroundTask{second}
	if err := os.WriteFile(logPath, []byte("second run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	// The second run's log is not the first run's output; what the first
	// run printed itself is all there is.
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "first"); err != nil || got.Text != "[killed]" || !got.LogOmitted {
		t.Fatalf("first run output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputSaysALogOutsideTheWorkspaceIsNotShown(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	outside := filepath.ToSlash(filepath.Join(t.TempDir(), "dev.log"))
	if err := os.WriteFile(filepath.FromSlash(outside), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task := ports.ChatBackgroundTask{ID: "shell", Kind: "shell", State: "running", Command: "npm run dev > " + outside + " 2>&1", StartedAt: &started}
	// A harness that keeps no output of its own.
	plain := &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{task}}
	h := newHarnessWithConversation(t, plain)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell")
	if err != nil || got.Text != "" || !got.LogOutsideWorkspace {
		t.Fatalf("output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputKeepsAFailedReadUnavailableWhenTheLogIsOutsideTheWorkspace(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	outside := filepath.ToSlash(filepath.Join(t.TempDir(), "dev.log"))
	task := ports.ChatBackgroundTask{ID: "shell", Kind: "shell", State: "running", Command: "npm run dev > " + outside + " 2>&1", StartedAt: &started}
	conv := &outputConversation{
		backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{task}},
		output:                 ports.ChatBackgroundTaskOutput{Text: "stderr line\n"},
	}
	h := newHarnessWithConversation(t, conv)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	// A good read keeps what the task printed itself, and says why the log isn't shown.
	got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell")
	if err != nil || got.Text != "stderr line\n" || !got.LogOutsideWorkspace {
		t.Fatalf("output=%+v err=%v", got, err)
	}
	// A provider error is a failed read, not an empty one.
	conv.outputErr = errors.New("provider timed out")
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
		t.Fatalf("failed read output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputKeepsTheLogReadDuringTheRunWhenAnUnlistedWriterTookItOver(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	first := ports.ChatBackgroundTask{ID: "first", RunID: "r1", Kind: "shell", State: "running", Command: "npm run dev > server.log 2>&1", StartedAt: &started}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{first}}}
	h := newHarnessWithConversation(t, conv)
	logPath := filepath.Join(h.workspace, "server.log")
	if err := os.WriteFile(logPath, []byte("first run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	// The panel reads the log while the run is live.
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "first"); err != nil || got.Text != "first run ready" {
		t.Fatalf("live output=%+v err=%v", got, err)
	}
	// The run ends, then a restart rewrites the log before any reading sees
	// the end, and the next reading doesn't list that restart yet.
	ended := time.Now().Add(-20 * time.Second)
	first.State, first.EndedAt = "failed", &ended
	conv.tasks = []ports.ChatBackgroundTask{first}
	if err := os.WriteFile(logPath, []byte("second run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "first"); err != nil || got.Text != "first run ready" {
		t.Fatalf("first run output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputStartsAfreshWhenAProviderReusesATaskID(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	ended := started.Add(10 * time.Second)
	task := ports.ChatBackgroundTask{ID: "shell", RunID: "r1", Kind: "shell", State: "completed", Command: "npm run build > build.log 2>&1", StartedAt: &started, EndedAt: &ended}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{task}}}
	h := newHarnessWithConversation(t, conv)
	logPath := filepath.Join(h.workspace, "build.log")
	if err := os.WriteFile(logPath, []byte("first build\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(logPath, ended, ended); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "first build" {
		t.Fatalf("first run output=%+v err=%v", got, err)
	}
	// The same ID, a new run writing the same log.
	restarted := time.Now()
	rerun := ports.ChatBackgroundTask{ID: "shell", RunID: "r2", Kind: "shell", State: "running", Command: task.Command, StartedAt: &restarted}
	conv.tasks = []ports.ChatBackgroundTask{rerun}
	if err := os.WriteFile(logPath, []byte("second build\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "second build" {
		t.Fatalf("second run output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputKeepsTheLogOfEveryListedTask(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	var tasks []ports.ChatBackgroundTask
	for i := range 40 {
		tasks = append(tasks, ports.ChatBackgroundTask{ID: fmt.Sprint("shell-", i), Kind: "shell", State: "running", Command: fmt.Sprintf("./server > log-%d.txt 2>&1", i), StartedAt: &started})
	}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: tasks}}
	h := newHarnessWithConversation(t, conv)
	if err := os.WriteFile(filepath.Join(h.workspace, "log-0.txt"), []byte("first server ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
			t.Fatal(err)
		}
		h.advance(2 * time.Second)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell-0"); err != nil || got.Text != "first server ready" {
		t.Fatalf("first listed task output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputWaitsForACompleteReadingBeforeEndingAnUnlistedTask(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	dev := ports.ChatBackgroundTask{ID: "dev", Kind: "shell", State: "running", Command: "npm run dev > dev.log 2>&1", StartedAt: &started}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{dev}}}
	h := newHarnessWithConversation(t, conv)
	logPath := filepath.Join(h.workspace, "dev.log")
	if err := os.WriteFile(logPath, []byte("starting\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	// A partial reading leaves the running task out: it hasn't ended.
	conv.tasks, conv.err = nil, ports.ErrBackgroundTasksPartial
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("starting\nlast words\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ended := time.Now()
	dev.State, dev.EndedAt = "failed", &ended
	conv.tasks, conv.err = []ports.ChatBackgroundTask{dev}, nil
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "dev"); err != nil || got.Text != "starting\nlast words" {
		t.Fatalf("output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputDropsTheLastRunsLogForANewRunWithNoCommandYet(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	task := ports.ChatBackgroundTask{ID: "shell", RunID: "r1", Kind: "shell", State: "running", Command: "npm run dev > dev.log 2>&1", StartedAt: &started}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{task}}}
	h := newHarnessWithConversation(t, conv)
	if err := os.WriteFile(filepath.Join(h.workspace, "dev.log"), []byte("first run\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	// A sparse update of the same run keeps its log.
	sparse := task
	sparse.Command = ""
	conv.tasks = []ports.ChatBackgroundTask{sparse}
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "first run" {
		t.Fatalf("same run output=%+v err=%v", got, err)
	}
	// A new run under the same ID, its command not reported yet.
	rerun := ports.ChatBackgroundTask{ID: "shell", RunID: "r2", Kind: "shell", State: "running", StartedAt: &started}
	conv.tasks = []ports.ChatBackgroundTask{rerun}
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "" {
		t.Fatalf("new run output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputKeepsTheProvidersFlagsBesideTheLog(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	task := ports.ChatBackgroundTask{ID: "shell", Kind: "shell", State: "running", Command: "npm run dev > dev.log", StartedAt: &started}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{task}}, output: ports.ChatBackgroundTaskOutput{Text: "warning on stderr", Stale: true}}
	h := newHarnessWithConversation(t, conv)
	if err := os.WriteFile(filepath.Join(h.workspace, "dev.log"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "ready\nwarning on stderr" || !got.Stale {
		t.Fatalf("with text output=%+v err=%v", got, err)
	}
	conv.output = ports.ChatBackgroundTaskOutput{Truncated: true, Stale: true}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "shell"); err != nil || got.Text != "ready" || !got.Stale || !got.Truncated {
		t.Fatalf("without text output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputTriesAFinalLogAgainAfterAFailureThatPasses(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file its owner can't open")
	}
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	ended := time.Now()
	task := ports.ChatBackgroundTask{ID: "build", Kind: "shell", State: "failed", Command: "npm run build > build.log 2>&1", StartedAt: &started, EndedAt: &ended}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{task}}}
	h := newHarnessWithConversation(t, conv)
	logPath := filepath.Join(h.workspace, "build.log")
	if err := os.WriteFile(logPath, []byte("error TS2322\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(logPath, ended, ended); err != nil {
		t.Fatal(err)
	}
	// The reading that sees the run end can't open its log yet.
	if err := os.Chmod(logPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(logPath, 0o600) })
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(logPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "build"); err != nil || got.Text != "error TS2322" {
		t.Fatalf("output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputSeesALaterRunWritingTheSameLogThroughAnotherPath(t *testing.T) {
	ctx := context.Background()
	started := time.Now().Add(-time.Minute)
	first := ports.ChatBackgroundTask{ID: "first", Kind: "shell", State: "running", Command: "npm run dev > logs/dev.log 2>&1", StartedAt: &started}
	conv := &outputConversation{backgroundConversation: &backgroundConversation{fakeConversation: newFakeConversation(), tasks: []ports.ChatBackgroundTask{first}}}
	h := newHarnessWithConversation(t, conv)
	if err := os.Mkdir(filepath.Join(h.workspace, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h.workspace, "logs"), filepath.Join(h.workspace, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	logPath := filepath.Join(h.workspace, "logs", "dev.log")
	if err := os.WriteFile(logPath, []byte("first run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	// A later run writes the same file as alias/dev.log, then the first run
	// vanishes before any output was read for it.
	restarted := time.Now()
	second := ports.ChatBackgroundTask{ID: "second", Kind: "shell", State: "running", Command: "npm run dev > alias/dev.log 2>&1", StartedAt: &restarted}
	if err := os.WriteFile(logPath, []byte("second run ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conv.tasks = []ports.ChatBackgroundTask{second}
	h.advance(2 * time.Second)
	if _, err := h.svc.BackgroundTasks(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.BackgroundTaskOutput(ctx, testSession, "first"); err != nil || !got.LogOmitted || got.Text != "" {
		t.Fatalf("first run output=%+v err=%v", got, err)
	}
}
