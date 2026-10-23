package chat_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// An asynchronous spawn puts the session on screen before its controller
// exists. Everything the user types in that window has to land somewhere
// durable, in order — refusing it would be a message lost from a session the
// user is already looking at and typing into.

func openProvisioningStore(t *testing.T, state domain.SessionProvisionState) (*sqlite.Store, domain.SessionID) {
	t.Helper()
	dir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: string(testProject), Path: dir, RegisteredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	rec, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		ProvisionState: state,
		CreatedAt:      time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return st, rec.ID
}

func provisioningService(t *testing.T, st *sqlite.Store) *chatsvc.Service {
	t.Helper()
	return provisioningServiceOver(t, st, st)
}

func provisioningServiceOver(t *testing.T, store chatsvc.Store, st *sqlite.Store, loggers ...*slog.Logger) *chatsvc.Service {
	t.Helper()
	svcLog := slog.New(slog.DiscardHandler)
	if len(loggers) > 0 && loggers[0] != nil {
		svcLog = loggers[0]
	}
	next := 0
	return chatsvc.New(chatsvc.Options{
		Store: store, Sessions: st, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
		Log:     svcLog,
		NewID: func() string {
			next++
			return fmt.Sprintf("queued-%d", next)
		},
	})
}

type staleProvisioningReader struct{ record domain.SessionRecord }

func (r staleProvisioningReader) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return r.record, true, nil
}

func TestSendDoesNotQueueAfterConcurrentKill(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	ctx := context.Background()
	stale, ok, err := st.GetSession(ctx, id)
	if err != nil || !ok {
		t.Fatalf("read provisioning session: found=%v err=%v", ok, err)
	}
	terminated := stale
	terminated.IsTerminated = true
	if err := st.UpdateSession(ctx, terminated); err != nil {
		t.Fatal(err)
	}
	next := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: staleProvisioningReader{record: stale}, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
		Log:     slog.New(slog.DiscardHandler), NewID: func() string {
			next++
			return fmt.Sprintf("late-%d", next)
		},
	})
	if _, err := svc.Send(ctx, id, ports.ChatUserMessage{Text: "late message", Origin: domain.MessageOriginHuman}); !errors.Is(err, chatsvc.ErrNotProvisioning) {
		t.Fatalf("send after Kill = %v, want ErrNotProvisioning", err)
	}
}

func TestSendWhileProvisioningQueuesInOrder(t *testing.T) {
	st, provisioningSession := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	svc := provisioningService(t, st)
	ctx := context.Background()

	first, err := svc.Send(ctx, provisioningSession, ports.ChatUserMessage{
		Text: "first", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("send while provisioning: %v", err)
	}
	if first.State != domain.TurnStateQueued {
		t.Fatalf("turn state = %q, want queued", first.State)
	}
	if _, err := svc.Send(ctx, provisioningSession, ports.ChatUserMessage{
		Text: "second", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("second send while provisioning: %v", err)
	}

	snapshot, err := svc.Snapshot(ctx, provisioningSession)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("messages = %d, want both queued messages", len(snapshot.Messages))
	}
	if snapshot.Messages[0].Text != "first" || snapshot.Messages[1].Text != "second" {
		t.Fatalf("messages out of order: %q then %q",
			snapshot.Messages[0].Text, snapshot.Messages[1].Text)
	}
	// A session that is still starting is connecting, not stopped: a client that
	// reads "stopped" hides the composer the user is meant to keep typing into.
	if snapshot.Controller != ports.ChatControllerConnecting {
		t.Fatalf("controller state = %q, want connecting", snapshot.Controller)
	}
}

func TestProvisioningSendPiggybacksReportsOnlyOnRecordedTurn(t *testing.T) {
	st, workerID := openProvisioningStore(t, domain.SessionProvisionReady)
	ctx := context.Background()
	now := time.Now().UTC()
	orchestrator, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		ProvisionState: domain.SessionProvisionProvisioning,
		CreatedAt:      now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	createReport := func(id string) {
		t.Helper()
		if _, err := st.CreateReport(ctx, domain.ReportRecord{
			ID: id, SessionID: workerID, ProjectID: testProject,
			State: domain.ReportCheckpoint, Note: id,
			CreatedAt: now, AvailableAt: now.Add(time.Hour),
			DeliveryState: domain.ReportPending, RepeatCount: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	createReport("first-report")
	var logs bytes.Buffer
	svc := provisioningServiceOver(t, st, st, slog.New(slog.NewTextHandler(&logs, nil)))
	svc.SetReportCoordinator(reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{
		Store: st, Now: func() time.Time { return now }, NewToken: func() string { return "report-claim" },
	}))
	message := ports.ChatUserMessage{Text: "start", Origin: domain.MessageOriginHuman, ClientMessageID: "client-1"}
	turn, err := svc.Send(ctx, orchestrator.ID, message)
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("queued send = %+v, err = %v", turn, err)
	}
	snapshot, err := svc.Snapshot(ctx, orchestrator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || !strings.Contains(snapshot.Messages[0].Text, "<ao-worker-reports>") ||
		!strings.Contains(snapshot.Messages[0].Text, "first-report") {
		t.Fatalf("queued message missing piggyback: %+v", snapshot.Messages)
	}
	if !strings.Contains(logs.String(), "worker reports piggybacked into user message") {
		t.Fatalf("piggyback evidence log missing: %q", logs.String())
	}
	first, found, err := st.GetReport(ctx, "first-report")
	if err != nil || !found || first.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("first report = %+v, found = %v, err = %v", first, found, err)
	}

	createReport("second-report")
	duplicate, err := svc.Send(ctx, orchestrator.ID, message)
	if err != nil || duplicate.ID != "" {
		t.Fatalf("duplicate send = %+v, err = %v", duplicate, err)
	}
	changed := message
	changed.Text = "different request"
	if _, err := svc.Send(ctx, orchestrator.ID, changed); !errors.Is(err, domain.ErrClientMessageConflict) {
		t.Fatalf("changed client message = %v, want conflict", err)
	}
	second, found, err := st.GetReport(ctx, "second-report")
	if err != nil || !found || second.DeliveryState != domain.ReportPending {
		t.Fatalf("duplicate acknowledged an unrecorded report: %+v, found = %v, err = %v", second, found, err)
	}
}

// A batch shown moments ago rides along silently: the user message stays clean,
// the suppression is logged, and the accepted turn still settles the batch.
func TestProvisioningSendSuppressesPiggybackInsideEmitTTL(t *testing.T) {
	st, workerID := openProvisioningStore(t, domain.SessionProvisionReady)
	ctx := context.Background()
	now := time.Now().UTC()
	orchestrator, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		ProvisionState: domain.SessionProvisionProvisioning,
		CreatedAt:      now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateReport(ctx, domain.ReportRecord{
		ID: "shown-report", SessionID: workerID, ProjectID: testProject,
		State: domain.ReportCheckpoint, Note: "shown-report",
		CreatedAt: now, AvailableAt: now.Add(time.Hour),
		DeliveryState: domain.ReportPending, RepeatCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	reportCoord := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{
		Store: st, Now: func() time.Time { return now }, NewToken: func() string { return "report-claim" },
	})
	// Seed the "shown once" state honestly: claim, stamp the visible-emission
	// marker, then release the unaccepted batch back to pending.
	shown, err := reportCoord.PreparePiggyback(ctx, orchestrator.ID)
	if err != nil || len(shown.Reports) != 1 {
		t.Fatalf("prepare shown batch = %#v, err = %v", shown, err)
	}
	if visible, err := reportCoord.EmitVisible(ctx, shown); err != nil || !visible {
		t.Fatalf("first show visible = %v, err = %v", visible, err)
	}
	// ReleasePiggyback returns the cause by contract; the seeded refusal is not
	// a test failure.
	_ = reportCoord.ReleasePiggyback(ctx, shown, errors.New("turn refused after the pane write"))
	var logs bytes.Buffer
	svc := provisioningServiceOver(t, st, st, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	svc.SetReportCoordinator(reportCoord)
	if _, err := svc.Send(ctx, orchestrator.ID, ports.ChatUserMessage{Text: "start", Origin: domain.MessageOriginHuman, ClientMessageID: "client-silent"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.Snapshot(ctx, orchestrator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || strings.Contains(snapshot.Messages[0].Text, "<ao-worker-reports>") {
		t.Fatalf("suppressed piggyback leaked into the message: %+v", snapshot.Messages)
	}
	if !strings.Contains(logs.String(), "worker reports piggyback suppressed within emit ttl") {
		t.Fatalf("suppression evidence log missing: %q", logs.String())
	}
	settled, found, err := st.GetReport(ctx, "shown-report")
	if err != nil || !found || settled.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("silent ride lost the batch: %+v, found = %v, err = %v", settled, found, err)
	}
}

// The queue is for sessions that are starting. A session with no controller for
// any other reason has a real reason, and accepting a message it will never
// dispatch would be worse than refusing it.
func TestSendWithoutControllerStillRefusedWhenNotProvisioning(t *testing.T) {
	st, provisioningSession := openProvisioningStore(t, domain.SessionProvisionReady)
	svc := provisioningService(t, st)

	_, err := svc.Send(context.Background(), provisioningSession, ports.ChatUserMessage{
		Text: "hello", Origin: domain.MessageOriginHuman,
	})
	if !errors.Is(err, chatsvc.ErrNoController) {
		t.Fatalf("err = %v, want ErrNoController", err)
	}
}

// The queue advances the conversation sequence without a single provider event.
// Start must still treat this as a conversation's first controller: reserving a
// fresh provider boundary here would open a brand-new session as if it were a
// resumed one whose provider thread could not be reached.
func TestFirstControllerAfterQueuedPromptIsNotFencedAsResume(t *testing.T) {
	st, provisioningSession := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	svc := provisioningService(t, st)
	ctx := context.Background()
	t.Cleanup(func() { _ = svc.Stop(context.Background(), provisioningSession) })

	if _, err := svc.Send(ctx, provisioningSession, ports.ChatUserMessage{
		Text: "the opening brief", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("queue opening prompt: %v", err)
	}

	boundaryReserved := false
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: provisioningSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			boundaryReserved = result.ProviderBoundary != nil
			return chatsvc.ControllerCommit{}, nil
		},
	}); err != nil {
		t.Fatalf("start first controller: %v", err)
	}
	if boundaryReserved {
		t.Fatal("queued turns were fenced off as provider history")
	}

	// The turn has to still be there to be drained. Start settles work a dead
	// controller left behind, and a queue written before the first controller
	// existed looks exactly like that unless it is excluded.
	snapshot, err := svc.Snapshot(ctx, provisioningSession)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Turns) != 1 {
		t.Fatalf("turns = %d, want the queued opening prompt", len(snapshot.Turns))
	}
	if state := snapshot.Turns[0].State; state == domain.TurnStateFailed {
		t.Fatalf("the opening prompt was settled as orphaned work: %q", snapshot.Turns[0].ErrorMessage)
	}
}

func TestDrainWithoutControllerKeepsOpeningPromptForLaterStart(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	ctx := context.Background()
	svc := provisioningService(t, st)
	if _, err := svc.Send(ctx, id, ports.ChatUserMessage{Text: "opening brief", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DrainQueued(ctx, id); !errors.Is(err, chatsvc.ErrNoController) {
		t.Fatalf("drain before controller = %v, want ErrNoController", err)
	}
	snapshot, err := svc.Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateQueued {
		t.Fatalf("opening brief after failed drain = %+v, want queued", snapshot.Turns)
	}
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			return chatsvc.ControllerCommit{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), id) })
	if err := svc.DrainQueued(ctx, id); err != nil {
		t.Fatalf("drain after controller start: %v", err)
	}
	snapshot, err = svc.Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateRunning {
		t.Fatalf("opening brief after retry = %+v, want running", snapshot.Turns)
	}
}

func provisioningOrchestrator(t *testing.T, st *sqlite.Store, id domain.SessionID) {
	t.Helper()
	record, found, err := st.GetSession(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("read session: found=%v err=%v", found, err)
	}
	record.Kind = domain.KindOrchestrator
	if err := st.UpdateSession(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}

func startOrchestratorController(t *testing.T, svc *chatsvc.Service, id domain.SessionID) {
	t.Helper()
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			return chatsvc.ControllerCommit{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), id) })
}

// An orchestrator's controller runs on the project conversation, but what the
// user types while it provisions is queued before that controller exists
// (#6523). The opening prompt must drain in the project conversation and stay
// visible after the context boundary a fresh controller records there, both for
// a new project and for a replacement orchestrator inheriting earlier history.
func TestOrchestratorOpeningPromptDrainsInProjectConversation(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		name := "new_project"
		if inherited {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
			ctx := context.Background()
			provisioningOrchestrator(t, st, id)
			if inherited {
				previous, err := st.CreateSession(ctx, domain.SessionRecord{
					ProjectID: testProject, Kind: domain.KindOrchestrator,
					Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
					CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
				})
				if err != nil {
					t.Fatalf("seed earlier orchestrator: %v", err)
				}
				project, err := st.CreateConversation(ctx, "project-narrative",
					domain.ConversationScopeProject, testProject, previous.ID, time.Now().UTC())
				if err != nil {
					t.Fatalf("seed project conversation: %v", err)
				}
				if err := st.UpsertActivity(ctx, project.ID, "", domain.ConversationActivity{
					ID: "earlier-work", Kind: domain.ActivityKindSystem,
					Status: domain.ActivityStatusCompleted, Summary: "earlier orchestrator work",
				}, time.Now().UTC()); err != nil {
					t.Fatalf("seed project history: %v", err)
				}
			}
			svc := provisioningService(t, st)
			if _, err := svc.Send(ctx, id, ports.ChatUserMessage{Text: "opening brief", Origin: domain.MessageOriginHuman}); err != nil {
				t.Fatal(err)
			}

			startOrchestratorController(t, svc, id)
			if err := svc.DrainQueued(ctx, id); err != nil {
				t.Fatalf("drain after controller start: %v", err)
			}

			snapshot, err := svc.Snapshot(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Conversation.Scope != domain.ConversationScopeProject {
				t.Fatalf("orchestrator conversation scope = %q, want project", snapshot.Conversation.Scope)
			}
			if inherited && snapshot.Conversation.ID != "project-narrative" {
				t.Fatalf("orchestrator conversation = %s, want inherited project-narrative", snapshot.Conversation.ID)
			}
			if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateRunning {
				t.Fatalf("opening brief after start = %+v, want one running turn", snapshot.Turns)
			}
			page, err := st.LoadConversationSnapshotPage(ctx, snapshot.Conversation.ID, 0, 50)
			if err != nil {
				t.Fatal(err)
			}
			visible := false
			for _, message := range page.Messages {
				visible = visible || message.Text == "opening brief"
			}
			if !visible {
				t.Fatalf("opening brief hidden from the project timeline: %+v", page.Messages)
			}
		})
	}
}

// Older builds left an orchestrator bound to both a provisioning row holding the
// stranded opening prompt and the project row it actually ran on (#6523). That
// prompt is stale once the session has run, so a restart shows the project
// transcript and must not replay it.
func TestStrandedPromptFromEarlierRunIsNotReplayed(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	ctx := context.Background()
	provisioningOrchestrator(t, st, id)
	svc := provisioningService(t, st)
	if _, err := svc.Send(ctx, id, ports.ChatUserMessage{Text: "stale brief", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateConversation(ctx, "live-project",
		domain.ConversationScopeProject, testProject, id, time.Now().UTC())
	if err != nil {
		t.Fatalf("seed split project conversation: %v", err)
	}
	record, found, err := st.GetSession(ctx, id)
	if err != nil || !found {
		t.Fatalf("read session: found=%v err=%v", found, err)
	}
	record.ProvisionState = domain.SessionProvisionReady
	record.Metadata.ControllerGeneration = "earlier-generation"
	if err := st.UpdateSession(ctx, record); err != nil {
		t.Fatal(err)
	}

	startOrchestratorController(t, svc, id)
	if err := svc.DrainQueued(ctx, id); err != nil {
		t.Fatalf("drain after restart: %v", err)
	}

	snapshot, err := svc.Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Conversation.ID != project.ID {
		t.Fatalf("orchestrator conversation = %s, want project row %s", snapshot.Conversation.ID, project.ID)
	}
	if len(snapshot.Turns) != 0 {
		t.Fatalf("stale provisioning prompt replayed into project conversation: %+v", snapshot.Turns)
	}
}

// staleLookupStore returns, once, the conversation a Send read before the
// orchestrator's first controller moved the queue out of it.
type staleLookupStore struct {
	*sqlite.Store
	stale *domain.ConversationRecord
}

func (s *staleLookupStore) ConversationForSession(ctx context.Context, id domain.SessionID) (domain.ConversationRecord, error) {
	if s.stale != nil {
		stale := *s.stale
		s.stale = nil
		return stale, nil
	}
	return s.Store.ConversationForSession(ctx, id)
}

// A Send can read the provisioning row just before the orchestrator's first
// controller moves the queue to the project conversation (#6523). The prompt
// must still be delivered: it lands in the project conversation after the
// prompts that moved, not in the row nothing reads any more.
func TestQueuedSendRacingFirstStartLandsInProjectConversation(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	ctx := context.Background()
	provisioningOrchestrator(t, st, id)
	racing := &staleLookupStore{Store: st}
	svc := provisioningServiceOver(t, racing, st)
	if _, err := svc.Send(ctx, id, ports.ChatUserMessage{Text: "opening brief", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	provisioningRow, err := st.ConversationForSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	startOrchestratorController(t, svc, id)
	if err := svc.DrainQueued(ctx, id); err != nil {
		t.Fatalf("drain after controller start: %v", err)
	}
	racing.stale = &provisioningRow
	turn, err := svc.QueueUserMessage(ctx, id, ports.ChatUserMessage{Text: "sent during start", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatalf("queue against the moved-out row: %v", err)
	}

	project, err := st.ConversationForSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if project.Scope != domain.ConversationScopeProject || turn.ConversationID != project.ID {
		t.Fatalf("racing prompt queued in %s, want project conversation %s", turn.ConversationID, project.ID)
	}
	snapshot, err := svc.Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, message := range snapshot.Messages {
		texts = append(texts, message.Text)
	}
	if strings.Join(texts, "|") != "opening brief|sent during start" {
		t.Fatalf("project timeline = %q, want the moved prompt before the racing one", texts)
	}
}

func TestRetryAfterControllerStartedButBeforeDrainKeepsOpeningPrompt(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	ctx := context.Background()
	first := provisioningService(t, st)
	if _, err := first.Send(ctx, id, ports.ChatUserMessage{Text: "opening brief", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	start := chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			return chatsvc.ControllerCommit{}, nil
		},
	}
	if _, err := first.Start(ctx, start); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSessionProvisionState(ctx, id, domain.SessionProvisionFailed, "drain interrupted", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSessionProvisionState(ctx, id, domain.SessionProvisionProvisioning, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Model daemon replacement: the old controller did not dispatch or clean up.
	next := provisioningService(t, st)
	start.ProviderConversationID = "thread-1"
	if _, err := next.Start(ctx, start); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Stop(context.Background(), id) })
	snapshot, err := next.Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateQueued {
		t.Fatalf("opening prompt after retry = %+v, want queued for drain", snapshot.Turns)
	}
}

type failingProvisionSessionReader struct{ *sqlite.Store }

func (f failingProvisionSessionReader) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, errors.New("temporary session read failure")
}

func TestFirstControllerSessionReadFailureDoesNotFailQueuedBrief(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	ctx := context.Background()
	svc := provisioningService(t, st)
	if _, err := svc.Send(ctx, id, ports.ChatUserMessage{Text: "opening brief", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	failing := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: failingProvisionSessionReader{st}, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
		Log:     slog.New(slog.DiscardHandler), NewID: func() string { return "read-failure" },
	})
	if _, err := failing.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	}); err == nil || !strings.Contains(err.Error(), "temporary session read failure") {
		t.Fatalf("start with unavailable session facts = %v, want read error", err)
	}
	snapshot, err := svc.Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateQueued {
		t.Fatalf("opening prompt after read failure = %+v, want queued", snapshot.Turns)
	}
}

func TestProvisioningRetryStillReservesBoundaryForPriorProviderHistory(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	ctx := context.Background()
	now := time.Now().UTC()
	rec, found, err := st.GetSession(ctx, id)
	if err != nil || !found {
		t.Fatalf("session: found=%v err=%v", found, err)
	}
	rec.Metadata.ControllerGeneration = "old-generation"
	rec.Metadata.ProviderConversationID = "unavailable-provider-thread"
	if err := st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation(ctx, "prior-provider-history", domain.ConversationScopeSession, testProject, id, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertActivity(ctx, conv.ID, "", domain.ConversationActivity{
		ID: "old-provider-event", Kind: domain.ActivityKindSystem,
		Status: domain.ActivityStatusCompleted, ProviderItemID: "old-provider-item",
	}, now); err != nil {
		t.Fatal(err)
	}
	svc := provisioningService(t, st)
	boundaryReserved := false
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			boundaryReserved = result.ProviderBoundary != nil
			if result.ProviderBoundary == nil {
				return chatsvc.ControllerCommit{}, nil
			}
			if err := st.CreateAndActivateConversationBranch(ctx, id, *result.ProviderBoundary, result.ControllerGeneration, now); err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			committed := result.Conversation
			committed.ActiveBranchID = result.ProviderBoundary.ID
			return chatsvc.ControllerCommit{Conversation: committed}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), id) })
	if !boundaryReserved {
		t.Fatal("fresh provider was attached to prior provider history without a new boundary")
	}
}

func TestControllerPublishedWhileProvisioningCannotOvertakeQueuedPrompt(t *testing.T) {
	st, provisioningSession := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	conv := newFakeConversation()
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("queued-%d", time.Now().UnixNano()) },
	})
	ctx := context.Background()
	t.Cleanup(func() { _ = svc.Stop(context.Background(), provisioningSession) })

	if _, err := svc.Send(ctx, provisioningSession, ports.ChatUserMessage{
		Text: "opening prompt", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: provisioningSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			return chatsvc.ControllerCommit{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Send(ctx, provisioningSession, ports.ChatUserMessage{
		Text: "typed during handoff", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	if got := conv.sentTexts(); len(got) != 1 || got[0] != "opening prompt" {
		t.Fatalf("provider received %v, want only the opening prompt first", got)
	}
}
