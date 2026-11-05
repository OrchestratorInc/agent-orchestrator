package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// Gateway tests.
//
// Every collaborator here is a hand-written fake with a call counter. That is deliberate:
// the acceptance point of this workstream is that the gateway forwards to the real owners
// instead of doing their work, so each test asserts the call happened (or provably did not),
// not merely that a struct was returned.

var gatewayTestNow = time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)

func gatewayClock() func() time.Time { return func() time.Time { return gatewayTestNow } }

// fakeSessions is the durable session read surface plus the commit and worktree reads.
type fakeSessions struct {
	mu        sync.Mutex
	byID      map[domain.SessionID]domain.SessionRecord
	byReq     map[string]domain.SessionID
	worktrees []domain.SessionWorktreeRecord
	commits   int
	getErr    error
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{
		byID:  make(map[domain.SessionID]domain.SessionRecord),
		byReq: make(map[string]domain.SessionID),
	}
}

func (f *fakeSessions) put(rec domain.SessionRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[rec.ID] = rec
	if rec.ClientRequestID != "" {
		f.byReq[rec.ClientRequestID] = rec.ID
	}
}

func (f *fakeSessions) GetSession(
	_ context.Context,
	id domain.SessionID,
) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return domain.SessionRecord{}, false, f.getErr
	}
	rec, ok := f.byID[id]
	return rec, ok, nil
}

func (f *fakeSessions) GetSessionByClientRequestID(
	_ context.Context,
	key string,
) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byReq[key]
	if !ok {
		return domain.SessionRecord{}, false, nil
	}
	return f.byID[id], true, nil
}

func (f *fakeSessions) CommitClientRequestSession(_ context.Context, id domain.SessionID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits++
	rec, ok := f.byID[id]
	if !ok {
		return fmt.Errorf("session %s is missing", id)
	}
	rec.ClientRequestCommitted = true
	f.byID[id] = rec
	return nil
}

func (f *fakeSessions) ListSessionWorktrees(
	_ context.Context,
	_ domain.SessionID,
) ([]domain.SessionWorktreeRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.worktrees, nil
}

func (f *fakeSessions) commitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commits
}

// fakeSpawner records every Spawn call and scripts its answer.
type fakeSpawner struct {
	mu      sync.Mutex
	calls   int
	err     error
	onSpawn func(cfg ports.SpawnConfig) (domain.SessionRecord, error)
	lastCfg ports.SpawnConfig
}

func (f *fakeSpawner) Spawn(
	ctx context.Context,
	cfg ports.SpawnConfig,
) (domain.SessionRecord, int, int, error) {
	f.mu.Lock()
	f.calls++
	f.lastCfg = cfg
	onSpawn := f.onSpawn
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return domain.SessionRecord{}, 0, 0, err
	}
	if onSpawn == nil {
		return domain.SessionRecord{}, 0, 0, errors.New("fake spawner has no answer scripted")
	}
	rec, err := onSpawn(cfg)
	_ = ctx
	return rec, 0, 0, err
}

func (f *fakeSpawner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeSpawner) config() ports.SpawnConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastCfg
}

// fakeResumer records which resume path was taken.
type fakeResumer struct {
	mu         sync.Mutex
	restores   int
	resumes    int
	restoreErr error
	resumeErr  error
	restoreRes sessionmanager.RestoreResult
	resumeRes  sessionmanager.RestoreResult
}

func (f *fakeResumer) RestoreWithMode(
	_ context.Context,
	_ domain.SessionID,
) (sessionmanager.RestoreResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restores++
	return f.restoreRes, f.restoreErr
}

func (f *fakeResumer) ResumeAgentWithMode(
	_ context.Context,
	_ domain.SessionID,
) (sessionmanager.RestoreResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumes++
	return f.resumeRes, f.resumeErr
}

func (f *fakeResumer) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restores, f.resumes
}

// fakeChats records sends and interrupts.
type fakeChats struct {
	mu             sync.Mutex
	sendCalls      int
	interruptCalls int
	sendTurn       domain.ConversationTurn
	sendErr        error
	interruptErr   error
	lastMsg        ports.ChatUserMessage
	lastSession    domain.SessionID
	// pinnedSends and pinnedInterrupts count the fenced entry points separately from the
	// unfenced ones, so a test can prove the gateway never dispatches through the
	// unfenced path.
	pinnedSends      int
	pinnedInterrupts int
	lastGeneration   string
	// pinnedErr, when set, is what SendPinned/InterruptPinned answer: it models a
	// controller swapped between the gateway's observation and the dispatch.
	pinnedErr error
}

func (f *fakeChats) Send(
	_ context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
) (domain.ConversationTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCalls++
	f.lastMsg = msg
	f.lastSession = id
	return f.sendTurn, f.sendErr
}

// SendPinned answers the fenced send. A scripted pinnedErr wins, so a test can model a
// generation that changed after the observation.
func (f *fakeChats) SendPinned(
	_ context.Context,
	id domain.SessionID,
	expectedGeneration string,
	msg ports.ChatUserMessage,
) (domain.ConversationTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinnedSends++
	f.lastMsg = msg
	f.lastSession = id
	f.lastGeneration = expectedGeneration
	if f.pinnedErr != nil {
		return domain.ConversationTurn{}, f.pinnedErr
	}
	return f.sendTurn, f.sendErr
}

func (f *fakeChats) Interrupt(_ context.Context, id domain.SessionID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interruptCalls++
	f.lastSession = id
	return f.interruptErr
}

// InterruptPinned answers the fenced stop.
func (f *fakeChats) InterruptPinned(
	_ context.Context,
	id domain.SessionID,
	expectedGeneration string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinnedInterrupts++
	f.lastSession = id
	f.lastGeneration = expectedGeneration
	if f.pinnedErr != nil {
		return f.pinnedErr
	}
	return f.interruptErr
}

func (f *fakeChats) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sendCalls, f.interruptCalls
}

// pinnedCounts reports how many fenced sends and stops reached the chat service.
func (f *fakeChats) pinnedCounts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pinnedSends, f.pinnedInterrupts
}

// generation returns the last generation a fenced call carried.
func (f *fakeChats) generation() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastGeneration
}

// fakeTurns scripts the durable AO turn lookup a steer receipt needs.
type fakeTurns struct {
	mu                sync.Mutex
	conversation      domain.ConversationRecord
	conversationID    string
	turns             map[string]domain.ConversationTurn
	conversationErr   error
	turnErr           error
	conversationCalls int
	turnCalls         int
}

func newFakeTurns() *fakeTurns {
	return &fakeTurns{
		conversation: domain.ConversationRecord{ID: "conv-1"},
		turns:        make(map[string]domain.ConversationTurn),
	}
}

func (f *fakeTurns) ConversationForSession(
	_ context.Context,
	_ domain.SessionID,
) (domain.ConversationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conversationCalls++
	if f.conversationErr != nil {
		return domain.ConversationRecord{}, f.conversationErr
	}
	return f.conversation, nil
}

func (f *fakeTurns) TurnByProviderID(
	_ context.Context,
	_ string,
	providerTurnID string,
) (domain.ConversationTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turnCalls++
	if f.turnErr != nil {
		return domain.ConversationTurn{}, f.turnErr
	}
	turn, ok := f.turns[providerTurnID]
	if !ok {
		return domain.ConversationTurn{}, domain.ErrNoConversationTurn
	}
	return turn, nil
}

func (f *fakeTurns) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conversationCalls, f.turnCalls
}

// fakeControllers scripts the live-controller probe. Controller returns no controller:
// the tests use the durable generation, which is what the fallback reads.
type fakeControllers struct {
	mu   sync.Mutex
	live map[domain.SessionID]bool
}

func newFakeControllers() *fakeControllers {
	return &fakeControllers{live: make(map[domain.SessionID]bool)}
}

func (f *fakeControllers) set(sessionID domain.SessionID, live bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live[sessionID] = live
}

func (f *fakeControllers) HasLiveChatController(sessionID domain.SessionID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live[sessionID]
}

func (f *fakeControllers) Controller(domain.SessionID) (*chatsvc.Controller, error) {
	return nil, chatsvc.ErrNoController
}

// gatewayFixture bundles the fakes so each test changes only what it cares about.
type gatewayFixture struct {
	sessions    *fakeSessions
	spawner     *fakeSpawner
	resumer     *fakeResumer
	chats       *fakeChats
	controllers *fakeControllers
	gateway     *LocalGateway
}

func newGatewayFixture() *gatewayFixture {
	f := &gatewayFixture{
		sessions:    newFakeSessions(),
		spawner:     &fakeSpawner{},
		resumer:     &fakeResumer{},
		chats:       &fakeChats{},
		controllers: newFakeControllers(),
	}
	f.gateway = NewLocalGateway(LocalGatewayDeps{
		Sessions:    f.spawner,
		Resumer:     f.resumer,
		Records:     f.sessions,
		Commits:     f.sessions,
		Worktrees:   f.sessions,
		Chats:       f.chats,
		Controllers: f.controllers,
		Clock:       gatewayClock(),
	})
	return f
}

// readyChatSession seeds a live chat-mode session with a controller generation.
func readyChatSession(
	f *gatewayFixture,
	id domain.SessionID,
	generation string,
) domain.SessionRecord {
	rec := domain.SessionRecord{
		ID:        id,
		ProjectID: "proj-1",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Mode:      domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityActive},
		Metadata:  domain.SessionMetadata{ControllerGeneration: generation},
	}
	f.sessions.put(rec)
	f.controllers.set(id, true)
	return rec
}

func sampleGatewaySpawnRequest() ports.SpawnRequest {
	return ports.SpawnRequest{
		WorkflowID: "wf-1",
		TaskID:     "task-1",
		AttemptID:  "attempt-1",
		SpawnKey:   "spawn-attempt-1",
		SpawnHash:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ProjectID:  "proj-1",
		Harness:    domain.HarnessCodex,
		Mode:       domain.SessionModeChat,
		Prompt:     "do the task",
	}
}

// --- Spawn ------------------------------------------------------------------

func TestLocalGatewaySpawnBindsTheReturnedRecord(t *testing.T) {
	f := newGatewayFixture()
	request := sampleGatewaySpawnRequest()
	f.spawner.onSpawn = func(cfg ports.SpawnConfig) (domain.SessionRecord, error) {
		rec := domain.SessionRecord{
			ID:                     "proj-1-7",
			ProjectID:              cfg.ProjectID,
			Kind:                   cfg.Kind,
			Harness:                cfg.Harness,
			Mode:                   cfg.RequestedMode,
			ClientRequestID:        cfg.ClientRequestID,
			ClientRequestCommitted: true,
			Activity:               domain.Activity{State: domain.ActivityActive},
			Metadata:               domain.SessionMetadata{ControllerGeneration: "gen-1"},
		}
		f.sessions.put(rec)
		f.controllers.set(rec.ID, true)
		return rec, nil
	}

	observation, err := f.gateway.Spawn(context.Background(), request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if observation.SessionID != "proj-1-7" {
		t.Fatalf("session id = %q, want the record the spawner returned", observation.SessionID)
	}
	if observation.Readiness != ports.SessionReadinessReady {
		t.Fatalf("readiness = %q, want ready", observation.Readiness)
	}
	if !observation.ClientRequestCommitted {
		t.Fatal("observation reports the request uncommitted after a committed spawn")
	}

	// The gateway must forward the request verbatim into the manager's own config.
	cfg := f.spawner.config()
	if cfg.ProjectID != request.ProjectID || cfg.Harness != request.Harness ||
		cfg.Prompt != request.Prompt || cfg.RequestedMode != request.Mode {
		t.Fatalf("spawn config was not forwarded: %+v", cfg)
	}
	if cfg.ClientRequestID != request.SpawnKey {
		t.Fatalf("clientRequestId = %q, want the spawn key %q", cfg.ClientRequestID, request.SpawnKey)
	}
	if cfg.ClientRequestHash != request.SpawnHash {
		t.Fatalf("clientRequestHash = %q, want the spawn hash", cfg.ClientRequestHash)
	}
	if cfg.Kind != domain.KindWorker {
		t.Fatalf("kind = %q, want worker", cfg.Kind)
	}
	if cfg.CreatedBy != domain.SessionCreatedByAutomation {
		t.Fatalf("createdBy = %q, want automation", cfg.CreatedBy)
	}
	if cfg.ParentSessionID != "" {
		t.Fatalf("parentSessionId = %q, want empty", cfg.ParentSessionID)
	}
}

func TestLocalGatewaySpawnIsIdempotentPerClientRequestID(t *testing.T) {
	f := newGatewayFixture()
	request := sampleGatewaySpawnRequest()
	existing := domain.SessionRecord{
		ID:                     "proj-1-1",
		ProjectID:              "proj-1",
		Kind:                   domain.KindWorker,
		Harness:                domain.HarnessCodex,
		Mode:                   domain.SessionModeChat,
		ClientRequestID:        request.SpawnKey,
		ClientRequestCommitted: true,
		Activity:               domain.Activity{State: domain.ActivityActive},
		Metadata:               domain.SessionMetadata{ControllerGeneration: "gen-1"},
	}
	f.sessions.put(existing)
	f.controllers.set(existing.ID, true)

	observation, err := f.gateway.Spawn(context.Background(), request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if observation.SessionID != existing.ID {
		t.Fatalf("session id = %q, want the already-committed session", observation.SessionID)
	}
	if calls := f.spawner.callCount(); calls != 0 {
		t.Fatalf("a replayed spawn called the session spawner %d times, want 0", calls)
	}
}

func TestLocalGatewaySpawnWithAnInFlightRequestIsUnknown(t *testing.T) {
	f := newGatewayFixture()
	request := sampleGatewaySpawnRequest()
	// The row exists but was never committed: a spawn for this key is in flight.
	f.sessions.put(domain.SessionRecord{
		ID:              "proj-1-2",
		ProjectID:       "proj-1",
		Kind:            domain.KindWorker,
		ClientRequestID: request.SpawnKey,
	})

	_, err := f.gateway.Spawn(context.Background(), request)
	if !errors.Is(err, ports.ErrSpawnOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrSpawnOutcomeUnknown", err)
	}
	if calls := f.spawner.callCount(); calls != 0 {
		t.Fatalf("an in-flight request spawned again: calls = %d", calls)
	}
}

func TestLocalGatewaySpawnRefusesAMissingSpawnKey(t *testing.T) {
	f := newGatewayFixture()
	request := sampleGatewaySpawnRequest()
	request.SpawnKey = ""

	_, err := f.gateway.Spawn(context.Background(), request)
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
	if calls := f.spawner.callCount(); calls != 0 {
		t.Fatalf("a refused spawn reached the spawner: calls = %d", calls)
	}
}

func TestLocalGatewaySpawnRefusesAnUnknownHarness(t *testing.T) {
	f := newGatewayFixture()
	request := sampleGatewaySpawnRequest()
	request.Harness = "not-a-harness"

	_, err := f.gateway.Spawn(context.Background(), request)
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
}

func TestLocalGatewaySpawnRefusalClassBecomesExecutionRefused(t *testing.T) {
	for _, cause := range []error{
		sessionmanager.ErrMissingHarness,
		sessionmanager.ErrUnknownHarness,
		ports.ErrAgentBinaryNotFound,
		sessionmanager.ErrClientRequestConflict,
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			f := newGatewayFixture()
			f.spawner.err = cause

			_, err := f.gateway.Spawn(context.Background(), sampleGatewaySpawnRequest())
			if !errors.Is(err, ports.ErrExecutionRefused) {
				t.Fatalf("err = %v, want ErrExecutionRefused", err)
			}
			if errors.Is(err, ports.ErrSpawnOutcomeUnknown) {
				t.Fatalf("a provable refusal was reported as unknown: %v", err)
			}
		})
	}
}

func TestLocalGatewaySpawnLostAnswerIsUnknown(t *testing.T) {
	f := newGatewayFixture()
	// The spawner fails without leaving a row: the answer is lost and nothing exists.
	f.spawner.err = errors.New("connection reset by peer")

	_, err := f.gateway.Spawn(context.Background(), sampleGatewaySpawnRequest())
	if !errors.Is(err, ports.ErrSpawnOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrSpawnOutcomeUnknown", err)
	}
}

func TestLocalGatewaySpawnCancelledButRowExistsReturnsTheObservation(t *testing.T) {
	f := newGatewayFixture()
	request := sampleGatewaySpawnRequest()
	// The spawner did create the row and then the caller's answer was lost to
	// cancellation. Re-reading the idempotency key must find it.
	f.spawner.err = context.Canceled
	f.sessions.put(domain.SessionRecord{
		ID:                     "proj-1-9",
		ProjectID:              "proj-1",
		Kind:                   domain.KindWorker,
		Harness:                domain.HarnessCodex,
		Mode:                   domain.SessionModeChat,
		ClientRequestID:        request.SpawnKey,
		ClientRequestCommitted: true,
		Activity:               domain.Activity{State: domain.ActivityActive},
		Metadata:               domain.SessionMetadata{ControllerGeneration: "gen-9"},
	})
	f.controllers.set("proj-1-9", true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	observation, err := f.gateway.Spawn(ctx, request)
	if err != nil {
		t.Fatalf("Spawn: %v (a row that exists must be reported, not called unknown)", err)
	}
	if observation.SessionID != "proj-1-9" {
		t.Fatalf("session id = %q, want the row the spawn actually created", observation.SessionID)
	}
}

func TestLocalGatewaySpawnCommitsAnUncommittedRow(t *testing.T) {
	f := newGatewayFixture()
	request := sampleGatewaySpawnRequest()
	f.spawner.onSpawn = func(cfg ports.SpawnConfig) (domain.SessionRecord, error) {
		rec := domain.SessionRecord{
			ID:              "proj-1-11",
			ProjectID:       cfg.ProjectID,
			Kind:            domain.KindWorker,
			Harness:         cfg.Harness,
			Mode:            cfg.RequestedMode,
			ClientRequestID: cfg.ClientRequestID,
			// Not committed: the gateway owns the commit.
			Activity: domain.Activity{State: domain.ActivityActive},
			Metadata: domain.SessionMetadata{ControllerGeneration: "gen-2"},
		}
		f.sessions.put(rec)
		f.controllers.set(rec.ID, true)
		return rec, nil
	}

	observation, err := f.gateway.Spawn(context.Background(), request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if f.sessions.commitCount() != 1 {
		t.Fatalf("commit calls = %d, want 1", f.sessions.commitCount())
	}
	if !observation.ClientRequestCommitted {
		t.Fatal("observation still reports the request uncommitted after the gateway committed it")
	}
}

// --- InspectSession / ProjectReadiness --------------------------------------

func TestProjectReadinessBranches(t *testing.T) {
	chatReady := domain.SessionRecord{
		Mode:     domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{ControllerGeneration: "gen-1"},
	}
	tui := chatReady
	tui.Mode = domain.SessionModeTUI

	for _, tc := range []struct {
		name string
		rec  domain.SessionRecord
		live bool
		want ports.SessionReadiness
	}{
		{
			name: "terminated",
			rec:  withTerminated(chatReady),
			live: true,
			want: ports.SessionReadinessExited,
		},
		{
			name: "provision failed",
			rec:  withProvision(chatReady, domain.SessionProvisionFailed),
			live: true,
			want: ports.SessionReadinessFailed,
		},
		{
			name: "provisioning",
			rec:  withProvision(chatReady, domain.SessionProvisionProvisioning),
			live: true,
			want: ports.SessionReadinessProvisioning,
		},
		{
			name: "activity exited",
			rec:  withActivity(chatReady, domain.ActivityExited),
			live: true,
			want: ports.SessionReadinessExited,
		},
		{
			name: "ready",
			rec:  chatReady,
			live: true,
			want: ports.SessionReadinessReady,
		},
		{
			name: "chat session with no live controller is provisioning",
			rec:  chatReady,
			live: false,
			want: ports.SessionReadinessProvisioning,
		},
		{
			name: "no generation is provisioning",
			rec:  withGeneration(chatReady, ""),
			live: true,
			want: ports.SessionReadinessProvisioning,
		},
		{
			name: "tui is never ready for chat-addressed work",
			rec:  tui,
			live: true,
			want: ports.SessionReadinessProvisioning,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProjectReadiness(tc.rec, tc.live); got != tc.want {
				t.Fatalf("readiness = %q, want %q", got, tc.want)
			}
		})
	}
}

func withTerminated(rec domain.SessionRecord) domain.SessionRecord {
	rec.IsTerminated = true
	return rec
}

func withProvision(rec domain.SessionRecord, state domain.SessionProvisionState) domain.SessionRecord {
	rec.ProvisionState = state
	return rec
}

func withActivity(rec domain.SessionRecord, state domain.ActivityState) domain.SessionRecord {
	rec.Activity = domain.Activity{State: state}
	return rec
}

func withGeneration(rec domain.SessionRecord, generation string) domain.SessionRecord {
	rec.Metadata.ControllerGeneration = generation
	return rec
}

func TestLocalGatewayInspectSessionProjectsTheRecord(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-3", "gen-42")
	rec.ClientRequestID = "spawn-x"
	rec.ClientRequestCommitted = true
	rec.Metadata.ProviderConversationID = "conv-abc"
	f.sessions.put(rec)
	f.sessions.worktrees = []domain.SessionWorktreeRecord{{
		SessionID:    rec.ID,
		RepoName:     "backend",
		WorktreePath: "/tmp/wt/backend",
		BaseSHA:      "deadbeef",
	}}

	observation, err := f.gateway.InspectSession(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("InspectSession: %v", err)
	}
	if observation.SessionID != rec.ID || observation.ProjectID != "proj-1" ||
		observation.Kind != domain.KindWorker || observation.Harness != domain.HarnessCodex ||
		observation.Mode != domain.SessionModeChat {
		t.Fatalf("identity was not projected: %+v", observation)
	}
	if observation.ControllerGeneration != "gen-42" {
		t.Fatalf("generation = %q, want gen-42", observation.ControllerGeneration)
	}
	if observation.ProviderConversationID != "conv-abc" {
		t.Fatalf("provider conversation = %q, want conv-abc", observation.ProviderConversationID)
	}
	if observation.ClientRequestID != "spawn-x" || !observation.ClientRequestCommitted {
		t.Fatalf("client request facts = %q/%v", observation.ClientRequestID, observation.ClientRequestCommitted)
	}
	if !observation.ObservedAt.Equal(gatewayTestNow) {
		t.Fatalf("observedAt = %v, want the injected clock", observation.ObservedAt)
	}
	if len(observation.Worktrees) != 1 {
		t.Fatalf("worktrees = %d, want 1", len(observation.Worktrees))
	}
	wt := observation.Worktrees[0]
	if wt.RepoKey != "backend" || wt.WorktreeRef != "/tmp/wt/backend" || wt.BaseSHA != "deadbeef" {
		t.Fatalf("worktree was not projected: %+v", wt)
	}
}

func TestLocalGatewayInspectSessionMissingIsNotFound(t *testing.T) {
	f := newGatewayFixture()
	_, err := f.gateway.InspectSession(context.Background(), "no-such-session")
	if !errors.Is(err, ports.ErrExecutionSessionNotFound) {
		t.Fatalf("err = %v, want ErrExecutionSessionNotFound", err)
	}
}

// --- FindSpawn --------------------------------------------------------------

func TestLocalGatewayFindSpawnReportsStoredFacts(t *testing.T) {
	f := newGatewayFixture()

	found, ok, err := f.gateway.FindSpawn(context.Background(), "missing-key")
	if err != nil || ok {
		t.Fatalf("missing request: ok=%v err=%v, want not found", ok, err)
	}
	if found.SessionID != "" {
		t.Fatalf("a missing request returned an observation: %+v", found)
	}

	readyChatSession(f, "proj-1-4", "gen-1")
	f.sessions.put(domain.SessionRecord{
		ID:                     "proj-1-4",
		ProjectID:              "proj-1",
		Kind:                   domain.KindWorker,
		Harness:                domain.HarnessCodex,
		Mode:                   domain.SessionModeChat,
		ClientRequestID:        "key-committed",
		ClientRequestCommitted: true,
		Activity:               domain.Activity{State: domain.ActivityActive},
		Metadata:               domain.SessionMetadata{ControllerGeneration: "gen-1"},
	})
	f.sessions.put(domain.SessionRecord{
		ID:              "proj-1-5",
		ProjectID:       "proj-1",
		Kind:            domain.KindWorker,
		Harness:         domain.HarnessCodex,
		Mode:            domain.SessionModeChat,
		ClientRequestID: "key-uncommitted",
	})

	committed, ok, err := f.gateway.FindSpawn(context.Background(), "key-committed")
	if err != nil || !ok {
		t.Fatalf("committed request: ok=%v err=%v", ok, err)
	}
	if !committed.ClientRequestCommitted {
		t.Fatal("FindSpawn reported a committed request as uncommitted")
	}

	uncommitted, ok, err := f.gateway.FindSpawn(context.Background(), "key-uncommitted")
	if err != nil || !ok {
		t.Fatalf("uncommitted request: ok=%v err=%v", ok, err)
	}
	if uncommitted.ClientRequestCommitted {
		t.Fatal("FindSpawn synthesized a commitment the store does not hold")
	}
	if uncommitted.SessionID != "proj-1-5" {
		t.Fatalf("session id = %q, want proj-1-5", uncommitted.SessionID)
	}
}

// --- Send -------------------------------------------------------------------

func TestLocalGatewaySendRequiresTheSendDelivery(t *testing.T) {
	f := newGatewayFixture()
	readyChatSession(f, "proj-1-6", "gen-1")

	_, err := f.gateway.Send(context.Background(), ports.MessageRequest{
		SessionID:            "proj-1-6",
		ClientMessageID:      "m-1",
		Text:                 "hello",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: "turn-1",
		ControllerGeneration: "gen-1",
	})
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused (a steer must use Steer)", err)
	}
	if sends, _ := f.chats.counts(); sends != 0 {
		t.Fatalf("a refused delivery reached the chat service: sends = %d", sends)
	}
}

func TestLocalGatewaySendRefusesATerminatedSession(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-7", "gen-1")
	rec.IsTerminated = true
	f.sessions.put(rec)

	_, err := f.gateway.Send(context.Background(), ports.MessageRequest{
		SessionID:            rec.ID,
		ClientMessageID:      "m-1",
		Text:                 "hello",
		Delivery:             ports.MessageDeliverySend,
		ControllerGeneration: "gen-1",
	})
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
	if sends, _ := f.chats.counts(); sends != 0 {
		t.Fatalf("a terminated session reached the chat service: sends = %d", sends)
	}
}

func TestLocalGatewaySendRefusesAProvisioningSession(t *testing.T) {
	f := newGatewayFixture()
	rec := domain.SessionRecord{
		ID:             "proj-1-8",
		ProjectID:      "proj-1",
		Kind:           domain.KindWorker,
		Harness:        domain.HarnessCodex,
		Mode:           domain.SessionModeChat,
		ProvisionState: domain.SessionProvisionProvisioning,
	}
	f.sessions.put(rec)

	_, err := f.gateway.Send(context.Background(), ports.MessageRequest{
		SessionID:            rec.ID,
		ClientMessageID:      "m-1",
		Text:                 "hello",
		Delivery:             ports.MessageDeliverySend,
		ControllerGeneration: "gen-1",
	})
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
	if sends, _ := f.chats.counts(); sends != 0 {
		t.Fatalf("a provisioning session reached the chat service: sends = %d", sends)
	}
}

func TestLocalGatewaySendReceiptShapes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		turn          domain.ConversationTurn
		wantDuplicate bool
		wantQueued    bool
	}{
		{
			name: "dispatched",
			turn: domain.ConversationTurn{
				ID: "turn-1", State: domain.TurnStateRunning, ProviderTurnID: "prov-1",
			},
		},
		{
			name:       "queued",
			turn:       domain.ConversationTurn{ID: "turn-2", State: domain.TurnStateQueued},
			wantQueued: true,
		},
		{
			name:          "duplicate",
			turn:          domain.ConversationTurn{},
			wantDuplicate: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGatewayFixture()
			readyChatSession(f, "proj-1-9", "gen-7")
			f.chats.sendTurn = tc.turn

			receipt, err := f.gateway.Send(context.Background(), ports.MessageRequest{
				SessionID:            "proj-1-9",
				ClientMessageID:      "m-1",
				Text:                 "hello",
				Delivery:             ports.MessageDeliverySend,
				ControllerGeneration: "gen-7",
				Origin:               domain.MessageOriginAutomation,
				SenderSessionID:      "orch-1",
			})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if receipt.Duplicate != tc.wantDuplicate {
				t.Fatalf("duplicate = %v, want %v", receipt.Duplicate, tc.wantDuplicate)
			}
			if receipt.Queued != tc.wantQueued {
				t.Fatalf("queued = %v, want %v", receipt.Queued, tc.wantQueued)
			}
			if receipt.Steered {
				t.Fatal("a send receipt reported steered=true")
			}
			if receipt.TargetProviderTurnID != "" {
				t.Fatalf("a send receipt carries a target turn %q", receipt.TargetProviderTurnID)
			}
			if receipt.TurnID != tc.turn.ID || receipt.ProviderTurnID != tc.turn.ProviderTurnID {
				t.Fatalf("turn ids were not forwarded: %+v", receipt)
			}
			if receipt.ControllerGeneration != "gen-7" {
				t.Fatalf("generation = %q, want gen-7", receipt.ControllerGeneration)
			}
			// The gateway must not claim human authorship for workflow direction.
			if f.chats.lastMsg.AuthoredByUser {
				t.Fatal("a workflow send was attributed to a human author")
			}
			if f.chats.lastMsg.Origin != domain.MessageOriginAutomation {
				t.Fatalf("origin = %q, want automation", f.chats.lastMsg.Origin)
			}
			if f.chats.lastMsg.SenderSessionID != "orch-1" {
				t.Fatalf("sender = %q, want orch-1", f.chats.lastMsg.SenderSessionID)
			}
		})
	}
}

func TestLocalGatewaySendMapsChatErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"unsupported", ports.ErrChatUnsupported, ports.ErrExecutionUnsupported},
		{"not chat mode", chatsvc.ErrNotChatMode, ports.ErrExecutionUnsupported},
		{"no controller", chatsvc.ErrNoController, ports.ErrExecutionRefused},
		{"stale controller generation", chatsvc.ErrStaleControllerGeneration, ports.ErrExecutionStaleGeneration},
		{"payload conflict", domain.ErrClientMessageConflict, ports.ErrExecutionRefused},
		{"missing session", ports.ErrSessionNotFound, ports.ErrExecutionSessionNotFound},
		{"uncertain steer", chatsvc.ErrSteerDeliveryUncertain, ports.ErrExecutionOutcomeUnknown},
		{"handoff", chatsvc.ErrControllerHandoff, ports.ErrExecutionOutcomeUnknown},
		{"unclassified", errors.New("boom"), ports.ErrExecutionOutcomeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGatewayFixture()
			readyChatSession(f, "proj-1-10", "gen-1")
			f.chats.sendErr = tc.err

			_, err := f.gateway.Send(context.Background(), ports.MessageRequest{
				SessionID:            "proj-1-10",
				ClientMessageID:      "m-1",
				Text:                 "hello",
				Delivery:             ports.MessageDeliverySend,
				ControllerGeneration: "gen-1",
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// --- Steer ------------------------------------------------------------------

func steerFixture(t *testing.T, generation string) (*gatewayFixture, ports.MessageRequest) {
	t.Helper()
	f := newGatewayFixture()
	readyChatSession(f, "proj-1-20", generation)
	return f, ports.MessageRequest{
		SessionID:            "proj-1-20",
		ClientMessageID:      "steer-1",
		Text:                 "actually, do it this way",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: "turn-live",
		ControllerGeneration: generation,
		Origin:               domain.MessageOriginAutomation,
	}
}

func TestLocalGatewaySteerSucceedsOnlyForTheNamedTurn(t *testing.T) {
	f, request := steerFixture(t, "gen-1")
	calls := 0
	f.gateway = NewLocalGateway(LocalGatewayDeps{
		Records:     f.sessions,
		Commits:     f.sessions,
		Worktrees:   f.sessions,
		Chats:       f.chats,
		Controllers: f.controllers,
		Clock:       gatewayClock(),
		SteerPinned: func(
			_ context.Context,
			_ domain.SessionID,
			_, providerTurnID string,
			_ ports.ChatUserMessage,
		) (chatsvc.SteerResult, error) {
			calls++
			return chatsvc.SteerResult{ProviderTurnID: providerTurnID}, nil
		},
	})

	receipt, err := f.gateway.Steer(context.Background(), request)
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if calls != 1 {
		t.Fatalf("steer entry point called %d times, want 1", calls)
	}
	if !receipt.Steered {
		t.Fatal("a successful steer receipt is not marked steered")
	}
	if receipt.TargetProviderTurnID != request.TargetProviderTurnID {
		t.Fatalf("target turn = %q, want %q", receipt.TargetProviderTurnID, request.TargetProviderTurnID)
	}
	if receipt.ControllerGeneration != request.ControllerGeneration {
		t.Fatalf("generation = %q, want %q", receipt.ControllerGeneration, request.ControllerGeneration)
	}
	if receipt.TurnID != "" {
		t.Fatalf("a steer receipt carries a turn id %q; it joins a turn, it does not open one", receipt.TurnID)
	}
	if !receipt.MatchesSteer(request) {
		t.Fatalf("receipt does not match the request it answered: %+v", receipt)
	}
}

func TestLocalGatewaySteerRefusesWhenTheProviderSteeredAnotherTurn(t *testing.T) {
	f, request := steerFixture(t, "gen-1")
	f.gateway = NewLocalGateway(LocalGatewayDeps{
		Records:     f.sessions,
		Worktrees:   f.sessions,
		Chats:       f.chats,
		Controllers: f.controllers,
		Clock:       gatewayClock(),
		SteerPinned: func(
			_ context.Context,
			_ domain.SessionID,
			_, _ string,
			_ ports.ChatUserMessage,
		) (chatsvc.SteerResult, error) {
			// The provider absorbed the guidance into a different turn.
			return chatsvc.SteerResult{ProviderTurnID: "turn-somewhere-else"}, nil
		},
	})

	receipt, err := f.gateway.Steer(context.Background(), request)
	if !errors.Is(err, ports.ErrExecutionTargetMismatch) {
		t.Fatalf("err = %v, want ErrExecutionTargetMismatch", err)
	}
	// The provider's actual answer travels with the error: a caller must be able to see
	// which turn absorbed the guidance without re-observing the provider.
	if receipt.ProviderTurnID != "turn-somewhere-else" {
		t.Fatalf("receipt turn = %q, want the turn the provider actually steered",
			receipt.ProviderTurnID)
	}
	if receipt.Steered {
		t.Fatal("a mismatched steer receipt reported Steered=true")
	}
}

func TestLocalGatewaySteerRefusesAStaleGeneration(t *testing.T) {
	f, request := steerFixture(t, "gen-new")
	request.ControllerGeneration = "gen-old"
	called := false
	f.gateway = NewLocalGateway(LocalGatewayDeps{
		Records:     f.sessions,
		Worktrees:   f.sessions,
		Chats:       f.chats,
		Controllers: f.controllers,
		Clock:       gatewayClock(),
		SteerPinned: func(
			_ context.Context,
			_ domain.SessionID,
			_, _ string,
			_ ports.ChatUserMessage,
		) (chatsvc.SteerResult, error) {
			called = true
			return chatsvc.SteerResult{}, nil
		},
	})

	_, err := f.gateway.Steer(context.Background(), request)
	if !errors.Is(err, ports.ErrExecutionStaleGeneration) {
		t.Fatalf("err = %v, want ErrExecutionStaleGeneration", err)
	}
	if called {
		t.Fatal("a stale generation reached the steer entry point")
	}
}

func TestLocalGatewaySteerWithoutAnEntryPointIsUnsupported(t *testing.T) {
	f, request := steerFixture(t, "gen-1")
	// No SteerPinned func, and the chat fake does not implement SteerPinned.
	_, err := f.gateway.Steer(context.Background(), request)
	if !errors.Is(err, ports.ErrExecutionUnsupported) {
		t.Fatalf("err = %v, want ErrExecutionUnsupported", err)
	}
}

func TestLocalGatewaySteerMapsChatErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"unsupported steer", chatsvc.ErrSteerUnsupported, ports.ErrExecutionUnsupported},
		{"no active turn", chatsvc.ErrNoActiveTurn, ports.ErrExecutionTargetMismatch},
		{"turn not steerable", chatsvc.ErrTurnNotSteerable, ports.ErrExecutionTargetMismatch},
		{"stale generation", chatsvc.ErrStaleControllerGeneration, ports.ErrExecutionTargetMismatch},
		{"uncertain", chatsvc.ErrSteerDeliveryUncertain, ports.ErrExecutionOutcomeUnknown},
		{"payload conflict", domain.ErrClientMessageConflict, ports.ErrExecutionRefused},
		{"no controller", chatsvc.ErrNoController, ports.ErrExecutionRefused},
		{"unclassified", errors.New("boom"), ports.ErrExecutionOutcomeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, request := steerFixture(t, "gen-1")
			f.gateway = NewLocalGateway(LocalGatewayDeps{
				Records:     f.sessions,
				Worktrees:   f.sessions,
				Chats:       f.chats,
				Controllers: f.controllers,
				Clock:       gatewayClock(),
				SteerPinned: func(
					_ context.Context,
					_ domain.SessionID,
					_, _ string,
					_ ports.ChatUserMessage,
				) (chatsvc.SteerResult, error) {
					return chatsvc.SteerResult{}, tc.err
				},
			})

			_, err := f.gateway.Steer(context.Background(), request)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLocalGatewaySteerRequiresTheSteerDelivery(t *testing.T) {
	f, request := steerFixture(t, "gen-1")
	request.Delivery = ports.MessageDeliverySend
	request.TargetProviderTurnID = ""

	_, err := f.gateway.Steer(context.Background(), request)
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused (a send must use Send)", err)
	}
}

// --- Interrupt --------------------------------------------------------------

func TestLocalGatewayInterruptOnATerminatedSessionNeverTouchesChat(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-30", "gen-1")
	rec.IsTerminated = true
	f.sessions.put(rec)

	receipt, err := f.gateway.Interrupt(context.Background(), ports.InterruptRequest{
		SessionID:            rec.ID,
		ControllerGeneration: "gen-1",
	})
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if !receipt.NothingToStop {
		t.Fatal("a terminated session reported something to stop")
	}
	if receipt.Confirmed || receipt.Unknown {
		t.Fatalf("a terminated session reported a contradictory receipt: %+v", receipt)
	}
	if _, interrupts := f.chats.counts(); interrupts != 0 {
		t.Fatalf("a terminated session reached the chat service: interrupts = %d", interrupts)
	}
}

func TestLocalGatewayInterruptWithNoLiveControllerNeverTouchesChat(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-31", "gen-1")
	f.controllers.set(rec.ID, false)

	receipt, err := f.gateway.Interrupt(context.Background(), ports.InterruptRequest{
		SessionID:            rec.ID,
		ControllerGeneration: "gen-1",
	})
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if !receipt.NothingToStop {
		t.Fatal("a session with no controller reported something to stop")
	}
	if _, interrupts := f.chats.counts(); interrupts != 0 {
		t.Fatalf("a session with no controller reached the chat service: interrupts = %d", interrupts)
	}
}

func TestLocalGatewayInterruptOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		confirmed bool
		unknown   bool
		nothing   bool
	}{
		{name: "confirmed", err: nil, confirmed: true},
		{name: "no active turn", err: chatsvc.ErrNoActiveTurn, nothing: true},
		{name: "no controller", err: chatsvc.ErrNoController, nothing: true},
		{name: "uncertain", err: chatsvc.ErrControllerHandoff, unknown: true},
		{name: "unclassified", err: errors.New("socket closed"), unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGatewayFixture()
			rec := readyChatSession(f, "proj-1-32", "gen-1")
			f.chats.interruptErr = tc.err

			receipt, err := f.gateway.Interrupt(context.Background(), ports.InterruptRequest{
				SessionID:            rec.ID,
				ControllerGeneration: "gen-1",
			})
			if err != nil {
				t.Fatalf("Interrupt: %v (an uncertain stop must never be an error)", err)
			}
			if receipt.Confirmed != tc.confirmed {
				t.Fatalf("confirmed = %v, want %v", receipt.Confirmed, tc.confirmed)
			}
			if receipt.Unknown != tc.unknown {
				t.Fatalf("unknown = %v, want %v", receipt.Unknown, tc.unknown)
			}
			if receipt.NothingToStop != tc.nothing {
				t.Fatalf("nothingToStop = %v, want %v", receipt.NothingToStop, tc.nothing)
			}
			if _, interrupts := f.chats.pinnedCounts(); interrupts != 1 {
				t.Fatalf("interrupt calls = %d, want 1", interrupts)
			}
			if receipt.ControllerGeneration != "gen-1" {
				t.Fatalf("receipt generation = %q, want gen-1", receipt.ControllerGeneration)
			}
		})
	}
}

func TestLocalGatewayInterruptRefusesATuiSession(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-33", "gen-1")
	rec.Mode = domain.SessionModeTUI
	f.sessions.put(rec)

	receipt, err := f.gateway.Interrupt(context.Background(), ports.InterruptRequest{
		SessionID:            rec.ID,
		ControllerGeneration: "gen-1",
	})
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if !receipt.NothingToStop {
		t.Fatal("a TUI session reported something to stop through the chat layer")
	}
	if _, interrupts := f.chats.counts(); interrupts != 0 {
		t.Fatalf("a TUI session reached the chat service: interrupts = %d", interrupts)
	}
}

// --- Resume -----------------------------------------------------------------

func TestLocalGatewayResumeRefusesAForeignSpawnKey(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-40", "gen-1")
	rec.ClientRequestID = "spawn-attempt-1"
	f.sessions.put(rec)

	_, err := f.gateway.Resume(context.Background(), ports.ResumeRequest{
		SessionID: rec.ID,
		SpawnKey:  "spawn-someone-else",
	})
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
	if restores, resumes := f.resumer.counts(); restores+resumes != 0 {
		t.Fatalf("a refused resume reached the session manager: restore=%d resume=%d", restores, resumes)
	}
}

func TestLocalGatewayResumeTerminatedRestores(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-41", "gen-1")
	rec.ClientRequestID = "spawn-41"
	rec.IsTerminated = true
	f.sessions.put(rec)

	if _, err := f.gateway.Resume(context.Background(), ports.ResumeRequest{
		SessionID: rec.ID, SpawnKey: "spawn-41",
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	restores, resumes := f.resumer.counts()
	if restores != 1 {
		t.Fatalf("RestoreWithMode calls = %d, want 1 for a terminated session", restores)
	}
	if resumes != 0 {
		t.Fatalf("ResumeAgentWithMode calls = %d, want 0", resumes)
	}
}

func TestLocalGatewayResumeExitedAgentResumes(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-42", "gen-1")
	rec.ClientRequestID = "spawn-42"
	rec.Activity = domain.Activity{State: domain.ActivityExited}
	f.sessions.put(rec)

	if _, err := f.gateway.Resume(context.Background(), ports.ResumeRequest{
		SessionID: rec.ID, SpawnKey: "spawn-42",
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	restores, resumes := f.resumer.counts()
	if resumes != 1 {
		t.Fatalf("ResumeAgentWithMode calls = %d, want 1 for an exited agent", resumes)
	}
	if restores != 0 {
		t.Fatalf("RestoreWithMode calls = %d, want 0", restores)
	}
}

func TestLocalGatewayResumeOnALiveSessionIsANoOp(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-43", "gen-1")
	rec.ClientRequestID = "spawn-43"
	f.sessions.put(rec)

	observation, err := f.gateway.Resume(context.Background(), ports.ResumeRequest{
		SessionID: rec.ID, SpawnKey: "spawn-43",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if observation.Readiness != ports.SessionReadinessReady {
		t.Fatalf("readiness = %q, want ready", observation.Readiness)
	}
	if restores, resumes := f.resumer.counts(); restores+resumes != 0 {
		t.Fatalf("a live session was relaunched: restore=%d resume=%d", restores, resumes)
	}
}

func TestLocalGatewayResumeRefusesAMissingSpawnKey(t *testing.T) {
	f := newGatewayFixture()
	readyChatSession(f, "proj-1-44", "gen-1")

	_, err := f.gateway.Resume(context.Background(), ports.ResumeRequest{
		SessionID: "proj-1-44",
	})
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
}

// --- The acceptance assertion ----------------------------------------------

// TestLocalGatewayForwardsToTheRealOwners is the acceptance point of this workstream: the
// gateway must compose AO's existing owners rather than simulate them. Every counter below
// would stay at zero for an implementation that fabricated its answers.
func TestLocalGatewayForwardsToTheRealOwners(t *testing.T) {
	f := newGatewayFixture()
	request := sampleGatewaySpawnRequest()
	f.spawner.onSpawn = func(cfg ports.SpawnConfig) (domain.SessionRecord, error) {
		rec := domain.SessionRecord{
			ID:                     "proj-1-50",
			ProjectID:              cfg.ProjectID,
			Kind:                   cfg.Kind,
			Harness:                cfg.Harness,
			Mode:                   cfg.RequestedMode,
			ClientRequestID:        cfg.ClientRequestID,
			ClientRequestCommitted: true,
			Activity:               domain.Activity{State: domain.ActivityActive},
			Metadata:               domain.SessionMetadata{ControllerGeneration: "gen-50"},
		}
		f.sessions.put(rec)
		f.controllers.set(rec.ID, true)
		return rec, nil
	}

	observation, err := f.gateway.Spawn(context.Background(), request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if calls := f.spawner.callCount(); calls != 1 {
		t.Fatalf("session spawner calls = %d, want 1: the gateway did not spawn", calls)
	}

	f.chats.sendTurn = domain.ConversationTurn{
		ID: "turn-1", State: domain.TurnStateRunning, ProviderTurnID: "prov-1",
	}
	if _, err := f.gateway.Send(context.Background(), ports.MessageRequest{
		SessionID:            observation.SessionID,
		ClientMessageID:      "m-1",
		Text:                 "go",
		Delivery:             ports.MessageDeliverySend,
		ControllerGeneration: observation.ControllerGeneration,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sends, _ := f.chats.pinnedCounts(); sends != 1 {
		t.Fatalf("chat send calls = %d, want 1: the gateway did not send", sends)
	}

	if _, err := f.gateway.Interrupt(context.Background(), ports.InterruptRequest{
		SessionID:            observation.SessionID,
		ControllerGeneration: observation.ControllerGeneration,
	}); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if _, interrupts := f.chats.pinnedCounts(); interrupts != 1 {
		t.Fatalf("chat interrupt calls = %d, want 1", interrupts)
	}

	// A second Spawn under the same key must not reach the spawner again.
	if _, err := f.gateway.Spawn(context.Background(), request); err != nil {
		t.Fatalf("replayed Spawn: %v", err)
	}
	if calls := f.spawner.callCount(); calls != 1 {
		t.Fatalf("session spawner calls = %d after a replay, want 1", calls)
	}
}

// --- production-path proof --------------------------------------------------
//
// The two tests below are the workstream's acceptance point: they assert the EXACT
// payload the production gateway hands to AO's existing owners. A gateway that fabricated
// an observation would pass a shape check; these fail unless the real call happened with
// the real arguments.

// TestLocalGatewaySpawnForwardsTheExactSpawnConfig proves Spawn reaches the Session
// Manager's own Spawn with the config a workflow attempt must carry.
func TestLocalGatewaySpawnForwardsTheExactSpawnConfig(t *testing.T) {
	f := newGatewayFixture()
	request := sampleGatewaySpawnRequest()
	// A second spawn under the same key must not reach the owner at all, so the assertion
	// below is about one real call rather than the last of several.
	f.spawner.onSpawn = func(cfg ports.SpawnConfig) (domain.SessionRecord, error) {
		rec := domain.SessionRecord{
			ID: "proj-1-70", ProjectID: cfg.ProjectID, Kind: cfg.Kind,
			Harness: cfg.Harness, Mode: cfg.RequestedMode,
			ClientRequestID: cfg.ClientRequestID, ClientRequestCommitted: true,
			Activity: domain.Activity{State: domain.ActivityActive},
			Metadata: domain.SessionMetadata{ControllerGeneration: "gen-70"},
		}
		f.sessions.put(rec)
		f.controllers.set(rec.ID, true)
		return rec, nil
	}

	if _, err := f.gateway.Spawn(context.Background(), request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	cfg := f.spawner.config()

	// The idempotency pair is what makes a retry replay instead of duplicating.
	if cfg.ClientRequestID != request.SpawnKey {
		t.Fatalf("clientRequestId = %q, want the spawn key %q", cfg.ClientRequestID, request.SpawnKey)
	}
	if cfg.ClientRequestHash != request.SpawnHash {
		t.Fatalf("clientRequestHash = %q, want the spawn hash %q",
			cfg.ClientRequestHash, request.SpawnHash)
	}
	// Unattended attribution: nobody is watching a workflow attempt.
	if cfg.CreatedBy != domain.SessionCreatedByAutomation {
		t.Fatalf("createdBy = %q, want automation", cfg.CreatedBy)
	}
	// A worker for a project, never a child of another session.
	if cfg.Kind != domain.KindWorker {
		t.Fatalf("kind = %q, want worker", cfg.Kind)
	}
	if cfg.ParentSessionID != "" {
		t.Fatalf("parentSessionId = %q, want empty: a workflow attempt is not a child spawn",
			cfg.ParentSessionID)
	}
	if cfg.ProjectID != request.ProjectID || cfg.Harness != request.Harness ||
		cfg.RequestedMode != request.Mode || cfg.Prompt != request.Prompt {
		t.Fatalf("the spawn config does not match the request: %+v", cfg)
	}
	if f.spawner.callCount() != 1 {
		t.Fatalf("session spawner calls = %d, want 1", f.spawner.callCount())
	}
}

// TestLocalGatewaySendForwardsTheExactChatMessage proves Send reaches the Chat service
// with workflow attribution rather than human attribution.
func TestLocalGatewaySendForwardsTheExactChatMessage(t *testing.T) {
	f := newGatewayFixture()
	readyChatSession(f, "proj-1-71", "gen-71")
	f.chats.sendTurn = domain.ConversationTurn{
		ID: "turn-71", State: domain.TurnStateRunning, ProviderTurnID: "prov-71",
	}

	receipt, err := f.gateway.Send(context.Background(), ports.MessageRequest{
		SessionID:            "proj-1-71",
		ClientMessageID:      "client-71",
		Text:                 "please redo the migration",
		Delivery:             ports.MessageDeliverySend,
		ControllerGeneration: "gen-71",
		Origin:               domain.MessageOriginAutomation,
		SenderSessionID:      "orchestrator-71",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if sends, _ := f.chats.pinnedCounts(); sends != 1 {
		t.Fatalf("chat send calls = %d, want 1", sends)
	}
	if got := f.chats.generation(); got != "gen-71" {
		t.Fatalf("pinned generation = %q, want gen-71: the fence was not forwarded", got)
	}
	if f.chats.lastSession != "proj-1-71" {
		t.Fatalf("send target = %q, want proj-1-71", f.chats.lastSession)
	}
	msg := f.chats.lastMsg
	if msg.Text != "please redo the migration" {
		t.Fatalf("text = %q, want the request text verbatim", msg.Text)
	}
	if msg.ClientMessageID != "client-71" {
		t.Fatalf("clientMessageId = %q, want client-71", msg.ClientMessageID)
	}
	// Attribution is the security-relevant part: a workflow message must never present
	// itself as human-authored.
	if msg.Origin != domain.MessageOriginAutomation {
		t.Fatalf("origin = %q, want automation", msg.Origin)
	}
	if msg.AuthoredByUser {
		t.Fatal("a workflow send was attributed to a human author")
	}
	if msg.SenderSessionID != "orchestrator-71" {
		t.Fatalf("sender = %q, want orchestrator-71", msg.SenderSessionID)
	}
	if receipt.TurnID != "turn-71" || receipt.ProviderTurnID != "prov-71" {
		t.Fatalf("receipt does not report the durable turn: %+v", receipt)
	}
}

// TestLocalGatewaySteerCallsThePinnedEntryPointExactlyOnce proves the production steer path
// invokes the injected entry point with the turn and generation the caller pinned.
func TestLocalGatewaySteerCallsThePinnedEntryPointExactlyOnce(t *testing.T) {
	f := newGatewayFixture()
	readyChatSession(f, "proj-1-72", "gen-72")

	type pin struct {
		generation string
		turn       string
		text       string
		clientID   string
	}
	var got []pin
	f.gateway = NewLocalGateway(LocalGatewayDeps{
		Records:     f.sessions,
		Worktrees:   f.sessions,
		Chats:       f.chats,
		Controllers: f.controllers,
		Clock:       gatewayClock(),
		SteerPinned: func(
			_ context.Context,
			_ domain.SessionID,
			generation, providerTurnID string,
			msg ports.ChatUserMessage,
		) (chatsvc.SteerResult, error) {
			got = append(got, pin{
				generation: generation, turn: providerTurnID,
				text: msg.Text, clientID: msg.ClientMessageID,
			})
			return chatsvc.SteerResult{ProviderTurnID: providerTurnID}, nil
		},
	})

	request := ports.MessageRequest{
		SessionID:            "proj-1-72",
		ClientMessageID:      "steer-72",
		Text:                 "use the other table",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: "turn-72",
		ControllerGeneration: "gen-72",
		Origin:               domain.MessageOriginAutomation,
	}
	if _, err := f.gateway.Steer(context.Background(), request); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("pinned steer calls = %d, want exactly 1", len(got))
	}
	if got[0].generation != "gen-72" {
		t.Fatalf("generation = %q, want the caller's fenced generation", got[0].generation)
	}
	if got[0].turn != "turn-72" {
		t.Fatalf("turn = %q, want the pinned provider turn", got[0].turn)
	}
	if got[0].text != "use the other table" || got[0].clientID != "steer-72" {
		t.Fatalf("payload was not forwarded: %+v", got[0])
	}
}

// TestLocalGatewaySteerNeverFallsBackToSend is the refusal guarantee: a steer that cannot
// be delivered must not become a normal message, because a send opens a new turn instead of
// correcting the running one.
func TestLocalGatewaySteerNeverFallsBackToSend(t *testing.T) {
	f := newGatewayFixture()
	readyChatSession(f, "proj-1-73", "gen-73")
	f.gateway = NewLocalGateway(LocalGatewayDeps{
		Records:     f.sessions,
		Worktrees:   f.sessions,
		Chats:       f.chats,
		Controllers: f.controllers,
		Clock:       gatewayClock(),
		SteerPinned: func(
			_ context.Context,
			_ domain.SessionID,
			_, _ string,
			_ ports.ChatUserMessage,
		) (chatsvc.SteerResult, error) {
			return chatsvc.SteerResult{}, chatsvc.ErrSteerUnsupported
		},
	})

	_, err := f.gateway.Steer(context.Background(), ports.MessageRequest{
		SessionID:            "proj-1-73",
		ClientMessageID:      "steer-73",
		Text:                 "actually",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: "turn-73",
		ControllerGeneration: "gen-73",
	})
	if !errors.Is(err, ports.ErrExecutionUnsupported) {
		t.Fatalf("err = %v, want ErrExecutionUnsupported", err)
	}
	if sends, _ := f.chats.counts(); sends != 0 {
		t.Fatalf("an unsupported steer reached the chat SEND path %d times", sends)
	}
}

// TestLocalGatewaySteerReportsTheDurableTurnID is the acceptance test for the applied-steer
// path.
//
// An "applied" intervention receipt requires BOTH ids: the provider turn proves the
// guidance landed, and AO's own turn row proves which durable turn absorbed it. A receipt
// with only the provider id cannot authorize anything, so a steer that genuinely succeeded
// would be unreachable in the shipped stack.
func TestLocalGatewaySteerReportsTheDurableTurnID(t *testing.T) {
	f := newGatewayFixture()
	readyChatSession(f, "proj-1-80", "gen-80")

	turns := newFakeTurns()
	turns.turns["prov-80"] = domain.ConversationTurn{
		ID: "ao-turn-80", ConversationID: "conv-1", ProviderTurnID: "prov-80",
		State: domain.TurnStateRunning,
	}

	f.gateway = NewLocalGateway(LocalGatewayDeps{
		Records:     f.sessions,
		Worktrees:   f.sessions,
		Chats:       f.chats,
		Controllers: f.controllers,
		Turns:       turns,
		Clock:       gatewayClock(),
		SteerPinned: func(
			_ context.Context,
			_ domain.SessionID,
			_, providerTurnID string,
			_ ports.ChatUserMessage,
		) (chatsvc.SteerResult, error) {
			return chatsvc.SteerResult{ProviderTurnID: providerTurnID}, nil
		},
	})

	request := ports.MessageRequest{
		SessionID:            "proj-1-80",
		ClientMessageID:      "steer-80",
		Text:                 "use the other table",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: "prov-80",
		ControllerGeneration: "gen-80",
	}
	receipt, err := f.gateway.Steer(context.Background(), request)
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}

	// Both ids are present: that is the whole point of the lookup.
	if receipt.ProviderTurnID != "prov-80" {
		t.Fatalf("providerTurnId = %q, want prov-80", receipt.ProviderTurnID)
	}
	if receipt.TurnID != "ao-turn-80" {
		t.Fatalf("turnId = %q, want the durable AO turn id (applied path unreachable)",
			receipt.TurnID)
	}
	if !receipt.Steered {
		t.Fatal("a successful steer receipt is not marked steered")
	}
	if !receipt.MatchesSteer(request) {
		t.Fatalf("receipt does not prove the steer it answered: %+v", receipt)
	}
	if convCalls, turnCalls := turns.counts(); convCalls != 1 || turnCalls != 1 {
		t.Fatalf("turn lookup calls = (%d, %d), want one conversation and one turn read",
			convCalls, turnCalls)
	}
}

// TestLocalGatewaySteerWithoutATurnRowStillReportsNoTurnID proves a missing row is reported
// honestly: the steer still succeeded, and the gateway does not invent an id to make the
// receipt look complete.
func TestLocalGatewaySteerWithoutATurnRowStillReportsNoTurnID(t *testing.T) {
	f := newGatewayFixture()
	readyChatSession(f, "proj-1-81", "gen-81")

	// The conversation exists but no turn row matches the provider turn yet: AO's record of
	// the steer has not landed.
	turns := newFakeTurns()

	f.gateway = NewLocalGateway(LocalGatewayDeps{
		Records:     f.sessions,
		Worktrees:   f.sessions,
		Chats:       f.chats,
		Controllers: f.controllers,
		Turns:       turns,
		Clock:       gatewayClock(),
		SteerPinned: func(
			_ context.Context,
			_ domain.SessionID,
			_, providerTurnID string,
			_ ports.ChatUserMessage,
		) (chatsvc.SteerResult, error) {
			return chatsvc.SteerResult{ProviderTurnID: providerTurnID}, nil
		},
	})

	receipt, err := f.gateway.Steer(context.Background(), ports.MessageRequest{
		SessionID:            "proj-1-81",
		ClientMessageID:      "steer-81",
		Text:                 "use the other table",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: "prov-81",
		ControllerGeneration: "gen-81",
	})
	if err != nil {
		t.Fatalf("Steer: %v (a missing turn row must not fail the steer itself)", err)
	}
	if receipt.TurnID != "" {
		t.Fatalf("turnId = %q, want empty: no row exists, so none may be reported",
			receipt.TurnID)
	}
	// The provider's answer still travels: the caller keeps the evidence it does have.
	if receipt.ProviderTurnID != "prov-81" {
		t.Fatalf("providerTurnId = %q, want prov-81", receipt.ProviderTurnID)
	}
	if !receipt.Steered {
		t.Fatal("the steer itself succeeded, so the receipt must say so")
	}
	// MatchesSteer still holds: it proves the guidance joined the named turn, which it
	// did. It deliberately does not require the durable row — that is the intervention
	// layer's evidence check, and it is why an empty TurnID downgrades the receipt instead
	// of invalidating the steer.
	if !receipt.MatchesSteer(ports.MessageRequest{
		SessionID:            "proj-1-81",
		ClientMessageID:      "steer-81",
		TargetProviderTurnID: "prov-81",
		ControllerGeneration: "gen-81",
	}) {
		t.Fatal("the steer genuinely joined the named turn, so MatchesSteer must hold")
	}
}

// TestLocalGatewaySteerWithoutATurnReaderStillSucceeds proves an unwired reader degrades to
// the provider-only receipt rather than failing the steer.
func TestLocalGatewaySteerWithoutATurnReaderStillSucceeds(t *testing.T) {
	f := newGatewayFixture()
	readyChatSession(f, "proj-1-82", "gen-82")
	f.gateway = NewLocalGateway(LocalGatewayDeps{
		Records:     f.sessions,
		Worktrees:   f.sessions,
		Chats:       f.chats,
		Controllers: f.controllers,
		Clock:       gatewayClock(),
		SteerPinned: func(
			_ context.Context,
			_ domain.SessionID,
			_, providerTurnID string,
			_ ports.ChatUserMessage,
		) (chatsvc.SteerResult, error) {
			return chatsvc.SteerResult{ProviderTurnID: providerTurnID}, nil
		},
	})

	receipt, err := f.gateway.Steer(context.Background(), ports.MessageRequest{
		SessionID:            "proj-1-82",
		ClientMessageID:      "steer-82",
		Text:                 "go",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: "prov-82",
		ControllerGeneration: "gen-82",
	})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if receipt.TurnID != "" {
		t.Fatalf("turnId = %q, want empty without a reader", receipt.TurnID)
	}
	if receipt.ProviderTurnID != "prov-82" {
		t.Fatalf("providerTurnId = %q, want prov-82", receipt.ProviderTurnID)
	}
}

// --- Generation fencing on the side-effect site ------------------------------

// sendRequest builds a send for one session at one generation.
func sendRequest(sessionID domain.SessionID, generation string) ports.MessageRequest {
	return ports.MessageRequest{
		SessionID:            sessionID,
		ClientMessageID:      "m-fence",
		Text:                 "do the next thing",
		Delivery:             ports.MessageDeliverySend,
		ControllerGeneration: generation,
		Origin:               domain.MessageOriginAutomation,
	}
}

// TestLocalGatewaySendIsFencedByGeneration is the send-side fence table:
//
//   - the current generation dispatches and the receipt names that generation;
//   - an old generation, an empty generation, a foreign session, and an observation with
//     no generation are all refused, and none of them reaches the chat service.
//
// The counter assertion is the decisive one. A gateway that refused AFTER calling the
// owner would satisfy the error check while still having dispatched the message.
func TestLocalGatewaySendIsFencedByGeneration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation string
		prepare    func(*gatewayFixture) domain.SessionID
		wantErr    error
	}{
		{
			name:       "current generation dispatches",
			generation: "gen-1",
			prepare: func(f *gatewayFixture) domain.SessionID {
				rec := readyChatSession(f, "proj-1-90", "gen-1")
				f.chats.sendTurn = domain.ConversationTurn{
					ID: "turn-90", State: domain.TurnStateRunning, ProviderTurnID: "prov-90",
				}
				return rec.ID
			},
		},
		{
			name:       "old generation",
			generation: "gen-old",
			prepare: func(f *gatewayFixture) domain.SessionID {
				return readyChatSession(f, "proj-1-91", "gen-1").ID
			},
			wantErr: ports.ErrExecutionStaleGeneration,
		},
		{
			name:       "empty generation",
			generation: "",
			prepare: func(f *gatewayFixture) domain.SessionID {
				return readyChatSession(f, "proj-1-92", "gen-1").ID
			},
			wantErr: ports.ErrExecutionRefused,
		},
		{
			name:       "observed generation is empty",
			generation: "gen-1",
			prepare: func(f *gatewayFixture) domain.SessionID {
				// A live controller with no recorded generation: structurally a session but
				// not something a caller can have observed a generation for.
				rec := readyChatSession(f, "proj-1-93", "")
				return rec.ID
			},
			wantErr: ports.ErrExecutionRefused,
		},
		{
			name:       "foreign session in the observation",
			generation: "gen-1",
			prepare: func(f *gatewayFixture) domain.SessionID {
				// The row answers with a different session id than the one asked for, which
				// is the only shape a keyed store cannot produce by accident.
				rec := readyChatSession(f, "proj-1-94", "gen-1")
				f.sessions.mu.Lock()
				stored := f.sessions.byID[rec.ID]
				stored.ID = "someone-elses-session"
				f.sessions.byID[rec.ID] = stored
				f.sessions.mu.Unlock()
				// The projection probes liveness by the id IT read, so the live controller
				// must answer for that id too; otherwise readiness masks the mismatch.
				f.controllers.set("someone-elses-session", true)
				return rec.ID
			},
			wantErr: ports.ErrExecutionSessionMismatch,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGatewayFixture()
			sessionID := tc.prepare(f)

			receipt, err := f.gateway.Send(context.Background(), sendRequest(sessionID, tc.generation))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if sends, _ := f.chats.pinnedCounts(); sends != 0 {
					t.Fatalf("a refused send reached the chat service: sends = %d", sends)
				}
				if unfenced, _ := f.chats.counts(); unfenced != 0 {
					t.Fatalf("a refused send used the unfenced path: sends = %d", unfenced)
				}
				return
			}
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if receipt.ControllerGeneration != tc.generation {
				t.Fatalf("receipt generation = %q, want %q",
					receipt.ControllerGeneration, tc.generation)
			}
			if sends, _ := f.chats.pinnedCounts(); sends != 1 {
				t.Fatalf("pinned sends = %d, want 1", sends)
			}
			if got := f.chats.generation(); got != tc.generation {
				t.Fatalf("forwarded generation = %q, want %q", got, tc.generation)
			}
			// The receipt must match the request it answers, or the intervention layer
			// records unknown for a delivery that really happened.
			if !receipt.MatchesSend(sendRequest(sessionID, tc.generation)) {
				t.Fatalf("receipt does not match its request: %+v", receipt)
			}
		})
	}
}

// TestLocalGatewaySendSurfacesAControllerSwap models the race the fence exists for: the
// gateway observes the current generation, and the controller is replaced before the
// dispatch reaches it.
//
// The honest answer is a stale-generation refusal. It must not be an unknown outcome
// (which would invite a resend) and it must never be reported as applied.
func TestLocalGatewaySendSurfacesAControllerSwap(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-95", "gen-1")
	f.chats.pinnedErr = chatsvc.ErrStaleControllerGeneration

	_, err := f.gateway.Send(context.Background(), sendRequest(rec.ID, "gen-1"))
	if !errors.Is(err, ports.ErrExecutionStaleGeneration) {
		t.Fatalf("err = %v, want ErrExecutionStaleGeneration", err)
	}
	if errors.Is(err, ports.ErrExecutionOutcomeUnknown) {
		t.Fatal("a refused dispatch was reported as an unknown outcome")
	}
	if sends, _ := f.chats.pinnedCounts(); sends != 1 {
		t.Fatalf("pinned sends = %d, want 1: the fence is proved by the owner refusing", sends)
	}
}

// TestLocalGatewaySendIsIdempotentPerClientMessageID proves a retried client message id
// never reaches the provider twice.
//
// The chat service answers a repeated id with an empty turn ("nothing new happened"), and
// the gateway must report that as a duplicate rather than as a delivery. The fake's counter
// is asserted to be exactly one so a second dispatch is caught even if the receipt shape
// looks right.
func TestLocalGatewaySendIsIdempotentPerClientMessageID(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-96", "gen-1")
	f.chats.sendTurn = domain.ConversationTurn{
		ID: "turn-96", State: domain.TurnStateRunning, ProviderTurnID: "prov-96",
	}

	first, err := f.gateway.Send(context.Background(), sendRequest(rec.ID, "gen-1"))
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if first.Duplicate || first.TurnID != "turn-96" {
		t.Fatalf("first receipt = %+v, want the dispatched turn", first)
	}

	// The retry: the Chat service's own idempotency answers with an empty turn.
	f.chats.sendTurn = domain.ConversationTurn{}
	retry, err := f.gateway.Send(context.Background(), sendRequest(rec.ID, "gen-1"))
	if err != nil {
		t.Fatalf("retried Send: %v", err)
	}
	if !retry.Duplicate {
		t.Fatalf("retried receipt = %+v, want duplicate", retry)
	}
	if retry.TurnID != "" {
		t.Fatalf("duplicate receipt names turn %q, want none", retry.TurnID)
	}
	if sends, _ := f.chats.pinnedCounts(); sends != 2 {
		// Two gateway calls, but only the first can have dispatched: the second is the
		// owner's idempotency path. The counter proves the gateway did not itself
		// short-circuit a call the owner needs to see.
		t.Fatalf("pinned sends = %d, want 2 (one dispatch plus one idempotent replay)", sends)
	}
}

// interruptRequest builds a stop for one session at one generation.
func interruptRequest(sessionID domain.SessionID, generation string) ports.InterruptRequest {
	return ports.InterruptRequest{
		SessionID:            sessionID,
		ControllerGeneration: generation,
		ClientMessageID:      "stop-fence",
	}
}

// TestLocalGatewayInterruptIsFencedByGeneration is the stop-side fence table.
func TestLocalGatewayInterruptIsFencedByGeneration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation string
		prepare    func(*gatewayFixture) domain.SessionID
		wantErr    error
		wantConf   bool
	}{
		{
			name:       "current generation confirms",
			generation: "gen-1",
			prepare: func(f *gatewayFixture) domain.SessionID {
				return readyChatSession(f, "proj-1-97", "gen-1").ID
			},
			wantConf: true,
		},
		{
			name:       "old generation",
			generation: "gen-old",
			prepare: func(f *gatewayFixture) domain.SessionID {
				return readyChatSession(f, "proj-1-98", "gen-1").ID
			},
			wantErr: ports.ErrExecutionStaleGeneration,
		},
		{
			name:       "empty generation",
			generation: "",
			prepare: func(f *gatewayFixture) domain.SessionID {
				return readyChatSession(f, "proj-1-99", "gen-1").ID
			},
			wantErr: ports.ErrExecutionRefused,
		},
		{
			name:       "live controller has no generation",
			generation: "gen-1",
			prepare: func(f *gatewayFixture) domain.SessionID {
				return readyChatSession(f, "proj-1-100", "").ID
			},
			wantErr: ports.ErrExecutionStaleGeneration,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGatewayFixture()
			sessionID := tc.prepare(f)

			receipt, err := f.gateway.Interrupt(context.Background(), interruptRequest(sessionID, tc.generation))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if _, interrupts := f.chats.pinnedCounts(); interrupts != 0 {
					t.Fatalf("a refused stop reached the chat service: interrupts = %d", interrupts)
				}
				return
			}
			if err != nil {
				t.Fatalf("Interrupt: %v", err)
			}
			if receipt.Confirmed != tc.wantConf {
				t.Fatalf("confirmed = %v, want %v", receipt.Confirmed, tc.wantConf)
			}
			if receipt.ControllerGeneration != tc.generation {
				t.Fatalf("receipt generation = %q, want %q",
					receipt.ControllerGeneration, tc.generation)
			}
			if !receipt.MatchesInterrupt(interruptRequest(sessionID, tc.generation)) {
				t.Fatalf("receipt does not match its request: %+v", receipt)
			}
		})
	}
}

// TestLocalGatewayInterruptSurfacesAControllerSwap proves the authoritative fence: the
// gateway's own read says the generation is current, and the controller refuses anyway.
func TestLocalGatewayInterruptSurfacesAControllerSwap(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-101", "gen-1")
	f.chats.pinnedErr = chatsvc.ErrStaleControllerGeneration

	_, err := f.gateway.Interrupt(context.Background(), interruptRequest(rec.ID, "gen-1"))
	if !errors.Is(err, ports.ErrExecutionStaleGeneration) {
		t.Fatalf("err = %v, want ErrExecutionStaleGeneration", err)
	}
}

// --- Resume -----------------------------------------------------------------

// TestLocalGatewayResumeRequiresTheExactSpawnKey is R-06: the earlier form accepted a
// session whose stored ClientRequestID was empty, so "no sponsor recorded" read as "sponsor
// matches".
func TestLocalGatewayResumeRequiresTheExactSpawnKey(t *testing.T) {
	for _, tc := range []struct {
		name         string
		clientReqID  string
		spawnKey     string
		wantRefused  bool
		wantRestores int
	}{
		{
			name:        "empty stored client request id is refused",
			clientReqID: "",
			spawnKey:    "spawn-x",
			wantRefused: true,
		},
		{
			name:        "different key is refused",
			clientReqID: "spawn-owner",
			spawnKey:    "spawn-x",
			wantRefused: true,
		},
		{
			name:         "equal key proceeds",
			clientReqID:  "spawn-x",
			spawnKey:     "spawn-x",
			wantRestores: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGatewayFixture()
			rec := readyChatSession(f, "proj-1-110", "gen-1")
			rec.ClientRequestID = tc.clientReqID
			rec.IsTerminated = true
			f.sessions.put(rec)

			_, err := f.gateway.Resume(context.Background(), ports.ResumeRequest{
				SessionID: rec.ID,
				SpawnKey:  tc.spawnKey,
			})
			if tc.wantRefused {
				if !errors.Is(err, ports.ErrExecutionRefused) {
					t.Fatalf("err = %v, want ErrExecutionRefused", err)
				}
				if restores, resumes := f.resumer.counts(); restores+resumes != 0 {
					t.Fatalf("a refused resume reached the session manager: restore=%d resume=%d",
						restores, resumes)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if restores, _ := f.resumer.counts(); restores != tc.wantRestores {
				t.Fatalf("restores = %d, want %d", restores, tc.wantRestores)
			}
		})
	}
}

// TestLocalGatewayResumeOfALiveSessionIsIdempotent proves a repeated resume of an
// already-restored live session relaunches nothing and answers with the current
// observation both times.
func TestLocalGatewayResumeOfALiveSessionIsIdempotent(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "proj-1-111", "gen-1")
	rec.ClientRequestID = "spawn-live"
	f.sessions.put(rec)

	for attempt := 0; attempt < 3; attempt++ {
		observation, err := f.gateway.Resume(context.Background(), ports.ResumeRequest{
			SessionID: rec.ID,
			SpawnKey:  "spawn-live",
		})
		if err != nil {
			t.Fatalf("Resume %d: %v", attempt, err)
		}
		if observation.Readiness != ports.SessionReadinessReady {
			t.Fatalf("Resume %d readiness = %q, want ready", attempt, observation.Readiness)
		}
		if observation.ControllerGeneration != "gen-1" {
			t.Fatalf("Resume %d generation = %q, want gen-1", attempt, observation.ControllerGeneration)
		}
	}
	if restores, resumes := f.resumer.counts(); restores+resumes != 0 {
		t.Fatalf("a live session was relaunched: restore=%d resume=%d", restores, resumes)
	}
}
