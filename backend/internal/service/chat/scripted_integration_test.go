package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/scripted"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Scripted integration.
//
// Everything here runs against the REAL Chat service: the real SQLite store (fully
// migrated through the repository's own migration path), the real controller
// admission and dispatch code, the real durable steer-delivery ledger, and the real
// capability resolver. The only substitute is the driver, which is a scripted
// adapter that speaks AO's ports and nothing else — no process, no network, no
// credential.
//
// That distinction matters: a test that hand-assembles fakes above the service proves
// the test's own arithmetic, while a test that runs the service proves the service.
// Each assertion below names the production path it covers for exactly that reason.

// scriptedSession is one harness plus the session row and driver behind it.
type scriptedSession struct {
	harness domain.AgentHarness
	id      domain.SessionID
	driver  *scripted.Driver
}

// scriptedEnv is one real Chat service over two harnesses with different
// capabilities.
type scriptedEnv struct {
	svc      *chatsvc.Service
	store    *sqlite.Store
	recorder *capability.Recorder
	resolver *capability.Resolver
	// steering supports steer; queueing does not. That difference is the point.
	steering scriptedSession
	queueing scriptedSession
}

// openScriptedStore opens a fully migrated store through the same public path the
// daemon's own tests use, so the migrations this test depends on are the ones a real
// install applies.
func openScriptedStore(t *testing.T) *sqlite.Store {
	t.Helper()
	return sqlitetest.MustOpen(t)
}

// startScriptedEnv builds the service, the two sessions, and their controllers.
func startScriptedEnv(t *testing.T) *scriptedEnv {
	t.Helper()
	ctx := context.Background()
	st := openScriptedStore(t)
	projectDir := t.TempDir()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: "m2e2e", Path: projectDir,
		RegisteredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	steeringDriver := scripted.NewDriver(scripted.Config{
		Harness: domain.HarnessCodex,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
			ports.ChatCapabilitySteer:     true,
		},
		Steerable: true,
	})
	// The second harness advertises no steer and implements no steerer, so the
	// service's own "this provider cannot steer" path is what runs.
	queueingDriver := scripted.NewDriver(scripted.Config{Harness: domain.HarnessClaudeCode})

	// The store assigns the session id from the project's sequence, so the id is
	// read back rather than assumed: a test that guessed it would be asserting about
	// a row that does not exist.
	createSession := func(harness domain.AgentHarness) domain.SessionID {
		t.Helper()
		now := time.Now().UTC().Truncate(time.Second)
		created, err := st.CreateSession(ctx, domain.SessionRecord{
			ProjectID:        "m2e2e",
			Kind:             domain.KindOrchestrator,
			Harness:          harness,
			Mode:             domain.SessionModeChat,
			Metadata:         domain.SessionMetadata{WorkspacePath: projectDir},
			Activity:         domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
			AutoInjectReview: true,
			AutoInjectCI:     true,
			CreatedAt:        now,
			UpdatedAt:        now,
		})
		if err != nil {
			t.Fatalf("create %s session: %v", harness, err)
		}
		return created.ID
	}
	codexSessionID := createSession(domain.HarnessCodex)
	claudeSessionID := createSession(domain.HarnessClaudeCode)

	recorder := capability.NewRecorder(st)
	svc := chatsvc.New(chatsvc.Options{
		Store:      st,
		Reader:     fullSnapshotReader(st),
		PageReader: boundedSnapshotReader(st),
		Sessions:   st,
		Drivers:    scripted.Registry(steeringDriver, queueingDriver),
		Activity:   &recordingActivity{},
		Log:        slog.New(slog.DiscardHandler),
		// The capability hook under test. It is optional by contract, and this is
		// the one place it is wired so confirmed evidence is produced by a real
		// operation rather than by a fixture.
		CapabilityEvidence: recorder,
		NewID:              scriptedIDFactory(),
		Now:                func() time.Time { return time.Now().UTC() },
	})
	resolver := capability.NewResolver(capability.WithRecorder(recorder))

	for _, session := range []struct {
		id      domain.SessionID
		harness domain.AgentHarness
	}{
		{codexSessionID, domain.HarnessCodex},
		{claudeSessionID, domain.HarnessClaudeCode},
	} {
		if _, err := svc.Start(ctx, chatsvc.StartConfig{
			SessionID:     session.id,
			ProjectID:     "m2e2e",
			Harness:       session.harness,
			WorkspacePath: projectDir,
		}); err != nil {
			t.Fatalf("start %s controller: %v", session.harness, err)
		}
	}
	t.Cleanup(func() {
		_ = svc.Stop(context.Background(), codexSessionID)
		_ = svc.Stop(context.Background(), claudeSessionID)
	})

	return &scriptedEnv{
		svc:      svc,
		store:    st,
		recorder: recorder,
		resolver: resolver,
		steering: scriptedSession{harness: domain.HarnessCodex, id: codexSessionID, driver: steeringDriver},
		queueing: scriptedSession{harness: domain.HarnessClaudeCode, id: claudeSessionID, driver: queueingDriver},
	}
}

func scriptedIDFactory() chatsvc.IDFactory {
	var (
		mu      sync.Mutex
		counter int
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		counter++
		return fmt.Sprintf("scripted-id-%04d", counter)
	}
}

// awaitDispatchedTurn waits until AO reports an in-flight provider turn.
//
// It deliberately waits only for dispatch, not for acknowledgement: the provider's
// turn.started arrives asynchronously, and riding out that gap is the steer path's
// own job (awaitTurnAcknowledged). A test that reached into the controller to force
// acknowledgement would be testing its own scaffolding instead of that wait.
func awaitDispatchedTurn(t *testing.T, svc *chatsvc.Service, id domain.SessionID) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if turn, _, ok := svc.RunningProviderTurn(context.Background(), id); ok {
			return turn
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no provider turn became visible for %s", id)
	return ""
}

// TestScriptedSendIsDurableAndIdempotent covers the real dispatch path: a message
// becomes a durable turn row bound to a provider id, and replaying the same
// ClientMessageID returns the same turn without a second provider call.
func TestScriptedSendIsDurableAndIdempotent(t *testing.T) {
	env := startScriptedEnv(t)
	ctx := context.Background()
	msg := ports.ChatUserMessage{
		Text:            "do the long thing",
		ClientMessageID: "scripted-send-1",
		Origin:          domain.MessageOriginHuman,
	}

	turn, err := env.svc.Send(ctx, env.steering.id, msg)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turn.ID == "" {
		t.Fatal("Send returned no durable turn id")
	}
	if turn.ProviderTurnID == "" {
		t.Fatal("Send returned no provider turn id: the dispatch never bound")
	}
	if turn.State != domain.TurnStateRunning {
		t.Fatalf("turn state = %q, want running", turn.State)
	}
	if got := env.steering.driver.SendCalls(); got != 1 {
		t.Fatalf("provider saw %d sends, want 1", got)
	}

	// The durable row is the record a client replays against, so it must agree.
	conversation, err := env.store.ConversationForSession(ctx, env.steering.id)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	snapshot, err := env.store.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	var found bool
	for _, stored := range snapshot.Turns {
		if stored.ID != turn.ID {
			continue
		}
		found = true
		if stored.ProviderTurnID != turn.ProviderTurnID {
			t.Errorf("stored provider turn = %q, want %q",
				stored.ProviderTurnID, turn.ProviderTurnID)
		}
		if stored.State != domain.TurnStateRunning {
			t.Errorf("stored state = %q, want running", stored.State)
		}
	}
	if !found {
		t.Fatalf("no durable row for turn %s", turn.ID)
	}

	// The same client handle is the same message, and must never reach the provider
	// twice. This is the controller's AppendUserMessage idempotency, not a check in
	// this test.
	duplicate, err := env.svc.Send(ctx, env.steering.id, msg)
	if err != nil {
		t.Fatalf("duplicate Send: %v", err)
	}
	if duplicate.ID != "" {
		t.Fatalf("duplicate Send opened turn %q, want the no-op empty turn", duplicate.ID)
	}
	if got := env.steering.driver.SendCalls(); got != 1 {
		t.Fatalf("provider saw %d sends after replay, want 1", got)
	}
}

// TestScriptedSteerPinnedUsesTheRealSteerPath covers SteerPinned end to end: the pin
// is honoured, the provider is called exactly once, and the durable activity lands
// on the turn it steered.
func TestScriptedSteerPinnedUsesTheRealSteerPath(t *testing.T) {
	env := startScriptedEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Send(ctx, env.steering.id, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "scripted-steer-send-1",
		Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provTurn := awaitDispatchedTurn(t, env.svc, env.steering.id)

	controller, err := env.svc.Controller(env.steering.id)
	if err != nil {
		t.Fatalf("Controller: %v", err)
	}
	result, err := env.svc.SteerPinned(ctx, env.steering.id, controller.Generation(), provTurn,
		ports.ChatUserMessage{
			Text:            "narrow it to the first file",
			ClientMessageID: "scripted-steer-1",
			Origin:          domain.MessageOriginAutomation,
		})
	if err != nil {
		t.Fatalf("SteerPinned: %v", err)
	}
	if result.ProviderTurnID != provTurn {
		t.Fatalf("steered turn = %q, want %q", result.ProviderTurnID, provTurn)
	}
	if result.ActivityID == "" {
		t.Error("SteerPinned reported no activity id; the steer is invisible on the timeline")
	}
	if got := env.steering.driver.SteerCalls(); got != 1 {
		t.Fatalf("provider saw %d steers, want 1", got)
	}

	// A wrong pin must reach the provider zero times and leave the count alone.
	if _, err := env.svc.SteerPinned(ctx, env.steering.id, controller.Generation(),
		"a-turn-that-never-existed", ports.ChatUserMessage{
			Text:            "this must not be delivered",
			ClientMessageID: "scripted-steer-wrong",
			Origin:          domain.MessageOriginAutomation,
		}); !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("wrong-pin error = %v, want ErrNoActiveTurn", err)
	}
	if got := env.steering.driver.SteerCalls(); got != 1 {
		t.Fatalf("provider saw %d steers after a wrong pin, want 1 (unchanged)", got)
	}

	// Replaying the accepted handle must come from the durable receipt, not from a
	// second provider call.
	replayed, err := env.svc.SteerPinned(ctx, env.steering.id, controller.Generation(), provTurn,
		ports.ChatUserMessage{
			Text:            "narrow it to the first file",
			ClientMessageID: "scripted-steer-1",
			Origin:          domain.MessageOriginAutomation,
		})
	if err != nil {
		t.Fatalf("replayed SteerPinned: %v", err)
	}
	if !replayed.Replayed {
		t.Fatal("replayed steer did not report itself as a replay")
	}
	if got := env.steering.driver.SteerCalls(); got != 1 {
		t.Fatalf("provider saw %d steers after replay, want 1", got)
	}
}

// TestScriptedHarnessWithoutSteeringDegradesToTheQueue covers the capability
// difference between the two harnesses: a provider with no steerer cannot be steered,
// and the service says so with a typed refusal instead of pretending.
func TestScriptedHarnessWithoutSteeringDegradesToTheQueue(t *testing.T) {
	env := startScriptedEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Send(ctx, env.queueing.id, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "scripted-queue-send-1",
		Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provTurn := awaitDispatchedTurn(t, env.svc, env.queueing.id)

	controller, err := env.svc.Controller(env.queueing.id)
	if err != nil {
		t.Fatalf("Controller: %v", err)
	}
	_, err = env.svc.SteerPinned(ctx, env.queueing.id, controller.Generation(), provTurn,
		ports.ChatUserMessage{
			Text:            "a correction this provider cannot take",
			ClientMessageID: "scripted-queue-steer-1",
			Origin:          domain.MessageOriginAutomation,
		})
	if !errors.Is(err, chatsvc.ErrSteerUnsupported) {
		t.Fatalf("error = %v, want ErrSteerUnsupported", err)
	}
	if got := env.queueing.driver.SteerCalls(); got != 0 {
		t.Fatalf("provider saw %d steers, want 0", got)
	}
}

// TestScriptedSteerProducesGateReadyEvidence is the capability half of the same
// walk: a real steer through the real service is what upgrades a session generation
// from "unproven" to "confirmed", and the resolver's gate is what reads it.
//
// Before the steer, no evidence exists for this session generation, so the gate must
// refuse. A static profile that says "codex can steer" must not be enough — that is
// the whole reason confirmed_runtime exists.
func TestScriptedSteerProducesGateReadyEvidence(t *testing.T) {
	env := startScriptedEnv(t)
	ctx := context.Background()

	controller, err := env.svc.Controller(env.steering.id)
	if err != nil {
		t.Fatalf("Controller: %v", err)
	}
	request := ports.CapabilityProbeRequest{
		Harness:    domain.HarnessCodex,
		SessionID:  env.steering.id,
		Generation: controller.Generation(),
	}

	before := env.resolver.Gate(ctx, request, domain.CapabilityFeatureSteer)
	if before.Allowed {
		t.Fatalf("steer was authorized before any provider performed it: source=%q reason=%q",
			before.Source, before.ReasonCode)
	}
	if before.Source == domain.CapabilitySourceConfirmedRuntime {
		t.Fatal("confirmed_runtime evidence existed before the operation")
	}

	// Auth and readiness are harness facts, so a handshake is what establishes them.
	// It is recorded BEFORE the operation because that is the real order — a session
	// only runs at all once its harness probed as usable — and because a confirmation
	// copies them at the moment it is written.
	if err := env.recorder.RecordCapabilities(ctx, domain.HarnessCapabilitySnapshot{
		Harness:    domain.HarnessCodex,
		Source:     domain.CapabilitySourceProviderHandshake,
		AuthState:  domain.AgentAuthenticationAuthorized,
		Readiness:  domain.CapabilityReady,
		Surfaces:   domain.CapabilitySurfaceChat,
		Streaming:  domain.CapabilitySupported,
		Tools:      domain.CapabilitySupported,
		Interrupt:  domain.CapabilitySupported,
		Steer:      domain.CapabilityStateUnknown,
		Queue:      domain.CapabilityStateUnknown,
		Resume:     domain.CapabilityResumeUnknown,
		Approval:   domain.CapabilityApprovalUnknown,
		ApplyAt:    domain.CapabilityApplyAtUnknown,
		Usage:      domain.CapabilityUsageUnknown,
		ObservedAt: time.Now().UTC(),
		ExpiresAt:  time.Now().UTC().Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("record harness handshake: %v", err)
	}

	// A handshake alone still may not authorize a per-session feature, so the gate
	// must refuse even now. This is the row that keeps the test honest: if it passed
	// here, the confirmation below would be proving nothing.
	handshakeOnly := env.resolver.Gate(ctx, request, domain.CapabilityFeatureSteer)
	if handshakeOnly.Allowed {
		t.Fatalf("a harness handshake authorized a session feature: reason=%q",
			handshakeOnly.ReasonCode)
	}

	if _, err := env.svc.Send(ctx, env.steering.id, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "scripted-cap-send-1",
		Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provTurn := awaitDispatchedTurn(t, env.svc, env.steering.id)

	if _, err := env.svc.SteerPinned(ctx, env.steering.id, controller.Generation(), provTurn,
		ports.ChatUserMessage{
			Text:            "narrow it",
			ClientMessageID: "scripted-cap-steer-1",
			Origin:          domain.MessageOriginAutomation,
		}); err != nil {
		t.Fatalf("SteerPinned: %v", err)
	}

	after := env.resolver.Gate(ctx, request, domain.CapabilityFeatureSteer)
	if !after.Allowed {
		t.Fatalf("steer was not authorized after the provider performed it: source=%q state=%q reason=%q",
			after.Source, after.State, after.ReasonCode)
	}
	if after.Source != domain.CapabilitySourceConfirmedRuntime {
		t.Fatalf("gate source = %q, want confirmed_runtime", after.Source)
	}
}

// TestScriptedRunningProviderTurnReportsWhatTheProviderAnnounced covers the accessor
// a production gateway pins a steer with: the turn and generation it reports must be
// the ones the real controller is tracking, not a guess.
func TestScriptedRunningProviderTurnReportsWhatTheProviderAnnounced(t *testing.T) {
	env := startScriptedEnv(t)

	if turn, _, ok := env.svc.RunningProviderTurn(context.Background(), env.steering.id); ok || turn != "" {
		t.Fatalf("idle session reported turn %q", turn)
	}

	if _, err := env.svc.Send(context.Background(), env.steering.id, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "scripted-turn-send-1",
		Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provTurn := awaitDispatchedTurn(t, env.svc, env.steering.id)

	controller, err := env.svc.Controller(env.steering.id)
	if err != nil {
		t.Fatalf("Controller: %v", err)
	}
	turn, generation, ok := env.svc.RunningProviderTurn(context.Background(), env.steering.id)
	if !ok {
		t.Fatal("running turn not reported after a dispatch")
	}
	if turn != provTurn {
		t.Fatalf("running turn = %q, want %q", turn, provTurn)
	}
	if generation != controller.Generation() {
		t.Fatalf("generation = %q, want %q", generation, controller.Generation())
	}
}
