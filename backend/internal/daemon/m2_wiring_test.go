package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/scripted"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestM2WiringLogRedaction is the M2-08 log-redaction check for the one log statement M2 adds
// to the production startup path.
//
// The M2 services deliberately keep no logger of their own: execution, intervention, and
// collaboration log nothing, so there is no formatter to get wrong. The single statement that
// exists records the WIRING SHAPE at startup. This test pins that it stays a shape: it names
// the gateway mode and the boolean presence of each coordinator, and it must never carry a
// prompt, a message body, a session id, a spawn hash, a lease token, or a fingerprint.
//
// The "%+v" half is covered statically — grep over the M2 production files returns nothing —
// but this test proves the runtime output too, which a future `log.Debug("req", "req", req)`
// would break.
func TestM2WiringLogRedaction(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	store := openDaemonTestStore(t)
	services := newM2Services(store, log)
	logM2Wiring(log, services)

	out := logs.String()
	if strings.TrimSpace(out) == "" {
		t.Fatal("logM2Wiring wrote nothing, so this test would prove nothing")
	}

	// The statement must say what it is, and that the execution path is the real gateway.
	for _, want := range []string{"workflow execution wiring", "gateway=local"} {
		if !strings.Contains(out, want) {
			t.Fatalf("wiring log is missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "fake-gateway") {
		t.Fatalf("wiring log still reports the offline fake gateway: %s", out)
	}

	// And it must leak nothing. Each needle is a shape that would be a real disclosure.
	for _, banned := range []string{
		"prompt=", "text=", "message=", "content=", "spawnHash=", "hash=",
		"leaseToken=", "fingerprint=", "sessionId=", "attemptId=", "workflowId=",
		"clientRequestId=", "clientMessageId=", "path=", "token=",
	} {
		if strings.Contains(out, banned) {
			t.Fatalf("the wiring log carries %q: %s", banned, out)
		}
	}

	// No 64-character hex value may appear: that is the shape of a lease token and of every
	// operation fingerprint, so any occurrence is a disclosure regardless of what wrote it.
	for _, field := range strings.Fields(out) {
		value := field
		if idx := strings.Index(value, "="); idx >= 0 {
			value = value[idx+1:]
		}
		value = strings.Trim(value, `"`)
		if len(value) == 64 && isLowerHex(value) {
			t.Fatalf("the wiring log carries a 64-hex value %q: %s", value, out)
		}
	}

	// The services it reports must actually be non-nil, so the booleans are a real check and
	// not a row of falses that would pass regardless.
	if services.Binder == nil || services.Interventions == nil || services.Collaboration == nil {
		t.Fatal("the M2 wiring produced a nil coordinator, so the logged shape is a lie")
	}
	if services.Gateway == nil {
		t.Fatal("the M2 wiring produced no execution gateway")
	}
	if !strings.Contains(out, "bind=true") ||
		!strings.Contains(out, "interventions=true") ||
		!strings.Contains(out, "collaboration=true") {
		t.Fatalf("the wiring log does not report the live coordinators: %s", out)
	}

	// The probe count is reported as a number, and it covers every harness the daemon
	// would launch.
	if !strings.Contains(out, "capabilityProbes=") {
		t.Fatalf("the wiring log does not report the capability probe count: %s", out)
	}
	if services.probeCount != len(domain.AllHarnesses) {
		t.Fatalf("probeCount = %d, want one probe per harness (%d)",
			services.probeCount, len(domain.AllHarnesses))
	}
}

// TestM2WiringUsesTheProductionGateway proves the daemon's execution path is the real
// gateway, not the offline fake.
//
// This is a behavioural assertion, not a compile-time one: ports.ExecutionGateway has no
// unexported seal, so any package could name any implementation. What this test pins is
// that THIS wiring names the production adapter, and that the adapter it names composes the
// real owners rather than a scripted double.
func TestM2WiringUsesTheProductionGateway(t *testing.T) {
	store := openDaemonTestStore(t)
	services := newM2Services(store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	if _, ok := services.Gateway.(*execution.LocalGateway); !ok {
		t.Fatalf("the daemon wired %T as its execution gateway, want *execution.LocalGateway",
			services.Gateway)
	}
}

// TestM2WiringBindsTheRealSteerEntryPoint proves the daemon's gateway found the chat
// service's pinned steer entry point.
//
// A gateway with no steer path refuses every steer with ErrExecutionUnsupported, which is
// the correct failure but a silent regression if it happens by accident: the chat service
// implements SteerPinned, so the gateway must have discovered it at construction.
func TestM2WiringBindsTheRealSteerEntryPoint(t *testing.T) {
	store := openDaemonTestStore(t)
	// A chat service is enough to satisfy the pinnedSteerer assertion: the gateway only
	// needs the method set, and Steer itself refuses a session that is not ready.
	chatSvc := chatsvc.New(chatsvc.Options{Sessions: store})
	services := newM2ServicesWith(m2GatewayDeps{Store: store, Chats: chatSvc})

	local, ok := services.Gateway.(*execution.LocalGateway)
	if !ok {
		t.Fatalf("gateway is %T, want *execution.LocalGateway", services.Gateway)
	}
	// The refusal must be "not ready", never "steering is not available": the latter would
	// mean the entry point was never bound.
	_, err := local.Steer(context.Background(), ports.MessageRequest{
		SessionID:            "no-such-session",
		ClientMessageID:      "m-1",
		Text:                 "steer",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: "turn-1",
		ControllerGeneration: "gen-1",
	})
	if errors.Is(err, ports.ErrExecutionUnsupported) {
		t.Fatalf("the gateway has no steer entry point bound: %v", err)
	}
}

// TestM2WiringCapabilityResolverHasProbes proves the resolver was given runtime evidence
// instead of falling back to static-only profiles.
//
// With no probes registered, every capability answer is static-only, whose grade cannot
// authorize a control. This test asks a real question through the resolver and checks that
// the answer describes a probe, not a static table.
func TestM2WiringCapabilityResolverHasProbes(t *testing.T) {
	store := openDaemonTestStore(t)
	services := newM2Services(store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if services.Capabilities == nil {
		t.Fatal("the M2 wiring produced no capability resolver")
	}

	// The probe is registered for every harness; resolving a harness-scoped request must
	// therefore come from the probe. With no readiness provider and no driver registry in
	// this test wiring, the probe honestly reports it could not establish a handshake — but
	// it ran, which is what proves registration.
	snapshot, err := services.Capabilities.Resolve(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if snapshot.Source == domain.CapabilitySourceStatic {
		t.Fatalf("source = %s, want a probe-backed source (registration missing?)", snapshot.Source)
	}
	if snapshot.Harness != domain.HarnessCodex {
		t.Fatalf("snapshot describes %s, not the requested harness", snapshot.Harness)
	}
}

// isLowerHex reports whether s is entirely lowercase hexadecimal.
func isLowerHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return len(s) > 0
}

// openDaemonTestStore builds a real, migrated store in a temp directory, so the wiring is
// exercised against the actual schema rather than a mock.
func openDaemonTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

// --- daemon-level wiring assertions -----------------------------------------
//
// Everything below tests the WIRING FUNCTION rather than the types it composes. The
// distinction matters: the coordinator and the gateway can each be correct while the daemon
// hands the coordinator a different object than the one it built, and only a wiring test
// sees that.

// TestM2WiringForwardsTheInjectedGateway proves the bundle uses the gateway it was handed
// rather than constructing its own.
//
// The assertion is a call counter on an injected fake: if the coordinator held a different
// gateway, the fake's Spawn would never run, and the test would be asserting about a
// coordinator nobody wired.
func TestM2WiringForwardsTheInjectedGateway(t *testing.T) {
	store := openDaemonTestStore(t)
	fake := execution.NewFakeGateway().
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-injected")
	materializeSession(t, store, fake)

	attempt := seedWiringAttempt(t, store, "wiring-gw")
	services := newM2ServicesWith(m2GatewayDeps{Store: store, Gateway: fake, ExecutionFence: daemonExecutionFence(t, store), Log: slog.Default()})

	// The capability gate runs first and would refuse without evidence, so register a probe
	// that reports a real handshake. This keeps the assertion about the gateway rather than
	// about the gate.
	services.Capabilities.RegisterProbe(domain.HarnessCodex, wiringProbe{
		grade:    domain.CapabilitySourceProviderHandshake,
		features: []domain.CapabilityFeature{domain.CapabilityFeatureStreaming},
	})

	outcome, err := services.Binder.BindAttempt(context.Background(), ports.SpawnRequest{
		WorkflowID: "wiring-workflow", TaskID: "wiring-task", AttemptID: attempt,
		ProjectID: "wiring-proj", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
	}, nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if fake.SpawnCalls() != 1 {
		t.Fatalf("the injected gateway's Spawn ran %d times, want 1: the coordinator holds a "+
			"different gateway than the bundle built", fake.SpawnCalls())
	}
	if outcome.Binding.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("binding state = %q, want bound_ready", outcome.Binding.State)
	}
}

// TestM2WiringInterventionsAdvanceTheBindingOnAConfirmedStop is the daemon-level proof that
// the P1 defect "Stop confirmed but the binding stays bound_ready" is fixed.
//
// It drives the REAL intervention coordinator through the bundle, so the assertion covers
// the wiring (`BindingWriter` is the coordinator) as well as the coordinator's own behavior.
func TestM2WiringInterventionsAdvanceTheBindingOnAConfirmedStop(t *testing.T) {
	store := openDaemonTestStore(t)
	ctx := context.Background()
	fake := execution.NewFakeGateway().
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-stop").
		InterruptResult(ports.InterruptReceipt{Confirmed: true})
	materializeSession(t, store, fake)

	attemptID := seedWiringAttempt(t, store, "wiring-stop")
	services := newM2ServicesWith(m2GatewayDeps{Store: store, Gateway: fake, ExecutionFence: daemonExecutionFence(t, store), Log: slog.Default()})
	services.Capabilities.RegisterProbe(domain.HarnessCodex, wiringProbe{
		grade: domain.CapabilitySourceConfirmedRuntime,
		features: []domain.CapabilityFeature{
			domain.CapabilityFeatureStreaming, domain.CapabilityFeatureInterrupt,
		},
	})

	outcome, err := services.Binder.BindAttempt(ctx, ports.SpawnRequest{
		WorkflowID: "wiring-workflow", TaskID: "wiring-task", AttemptID: attemptID,
		ProjectID: "wiring-proj", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
	}, nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("precondition: state = %q, want bound_ready", outcome.Binding.State)
	}

	stop, _, err := services.Interventions.Stop(ctx, intervention.StopRequest{
		WorkflowID:      "wiring-workflow",
		TaskID:          "wiring-task",
		AttemptID:       attemptID,
		ClientMessageID: "wiring-stop-1",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !stop.Performed {
		t.Fatalf("Stop did not perform the interrupt: %s", stop.ReasonCode)
	}
	if fake.InterruptCount() != 1 {
		t.Fatalf("gateway interrupt calls = %d, want 1", fake.InterruptCount())
	}

	// The binding must agree that the session is stopping. Leaving it at bound_ready is the
	// exact divergence the BindingWriter wiring exists to prevent.
	binding, found, err := store.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil || !found {
		t.Fatalf("read binding: found=%v err=%v", found, err)
	}
	if binding.State != domain.WorkflowBindingStateStopConfirmed {
		t.Fatalf("binding state = %q, want stop_confirmed (BindingWriter not wired?)", binding.State)
	}
}

// TestM2WiringRegistersAProbePerHarness proves the wiring function installs probes rather
// than leaving the resolver on static evidence.
//
// The resolver is asked directly: with a probe registered, a harness-scoped request must
// come back at handshake grade, which no static profile can produce.
func TestM2WiringRegistersAProbePerHarness(t *testing.T) {
	store := openDaemonTestStore(t)
	services := newM2ServicesWith(m2GatewayDeps{Store: store, Log: slog.Default()})

	for _, harness := range []domain.AgentHarness{
		domain.HarnessClaudeCode, domain.HarnessCodex,
		domain.HarnessOpenCode, domain.HarnessOpenCodeV2,
	} {
		t.Run(string(harness), func(t *testing.T) {
			// The probe this bundle installed reads readiness and the driver registry. Both
			// are absent in this wiring, so the honest answer is local_probe — but the
			// probe RAN, which is what proves registration (an unregistered harness would
			// resolve from the static profile instead).
			snapshot, err := services.Capabilities.Resolve(
				context.Background(), ports.CapabilityProbeRequest{Harness: harness})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if snapshot.Source == domain.CapabilitySourceStatic {
				t.Fatalf("source = %s for %s: no probe is registered for this harness",
					snapshot.Source, harness)
			}
			if snapshot.Harness != harness {
				t.Fatalf("snapshot describes %s, not %s", snapshot.Harness, harness)
			}
		})
	}
}

// TestM2ProductionWiringNeverNamesTheFakeGateway is a deliberately SOURCE-TEXT assertion.
//
// The only other proof would be a compile-time seal, and ports.ExecutionGateway cannot
// carry one: a seal works only through an interface declared in the implementing package,
// and the port lives in package ports. So the guarantee is behavioural — no production file
// in internal/daemon names the fake — and this test reads the source to keep it that way. A
// future edit that swaps the gateway back fails here with the offending line.
func TestM2ProductionWiringNeverNamesTheFakeGateway(t *testing.T) {
	for _, path := range []string{"execution_wiring.go", "daemon.go", "execution_recovery.go"} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Contains(source, []byte("NewFakeGateway")) {
			t.Fatalf("%s names NewFakeGateway: the daemon's execution path must be the "+
				"production gateway", path)
		}
		if bytes.Contains(source, []byte("execution.FakeGateway")) {
			t.Fatalf("%s names execution.FakeGateway: the daemon's execution path must be "+
				"the production gateway", path)
		}
	}
}

// materializeSession makes the fake gateway create a real session row for the id it
// reports. The binding has a foreign key onto sessions, so a fake that answered with an
// invented id could never be bound — and a test that skipped this would be asserting about
// a bind the store rejected.
func materializeSession(t *testing.T, store *sqlite.Store, fake *execution.FakeGateway) {
	t.Helper()
	fake.WithSessionMaterializer(func(
		ctx context.Context,
		_ domain.SessionID,
		request ports.SpawnRequest,
	) (domain.SessionID, error) {
		return materializeAdmittedDaemonSession(ctx, store, domain.SessionRecord{
			ProjectID: request.ProjectID, Kind: domain.KindWorker, Harness: request.Harness,
			Mode:     domain.SessionModeChat,
			Activity: domain.Activity{State: domain.ActivityActive},
			Metadata: domain.SessionMetadata{ControllerGeneration: "gen-wiring"},
		}, request, time.Now().UTC())
	})
}

// wiringProbe is a CapabilityProbe that reports a fixed handshake grade, so a wiring test
// can reach the coordinator past its capability gate without a provider.
type wiringProbe struct {
	grade    domain.CapabilitySource
	features []domain.CapabilityFeature
}

func (p wiringProbe) ProbeCapabilities(
	_ context.Context,
	request ports.CapabilityProbeRequest,
) (domain.HarnessCapabilitySnapshot, error) {
	snapshot := domain.NewHarnessCapabilitySnapshot(
		request.Harness, p.grade, time.Now().UTC(), time.Minute)
	// A session-scoped request must be answered by a session-scoped snapshot: the resolver
	// refuses a harness-level answer for a per-session gate, so the probe echoes the
	// identity it was asked about.
	snapshot.ObservedSessionID = request.SessionID
	snapshot.Generation = request.Generation
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	for _, feature := range p.features {
		switch feature {
		case domain.CapabilityFeatureStreaming:
			snapshot.Streaming = domain.CapabilitySupported
		case domain.CapabilityFeatureInterrupt:
			snapshot.Interrupt = domain.CapabilitySupported
		}
	}
	return snapshot, nil
}

// seedWiringAttempt creates the workflow, task, and attempt a bind needs, and returns the
// attempt id.
func seedWiringAttempt(t *testing.T, store *sqlite.Store, idem string) domain.WorkflowAttemptID {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	admission := sqlitetest.NewAdmissionFixture(t, store, func() time.Time { return now })

	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "wiring-proj", Path: t.TempDir(), RegisteredAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	workflow := domain.Workflow{
		ID: "wiring-workflow", Title: "Wiring", Goal: "wire M2",
		State: domain.WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	task := domain.WorkflowTask{
		ID: "wiring-task", WorkflowID: workflow.ID, Kind: domain.WorkflowTaskKindDevelopment,
		Title: "Task", State: domain.WorkflowTaskStatePending, MaxAttempts: 1, Revision: 1,
		AgentHarness: domain.HarnessCodex, CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := store.CreateWorkflow(
		ctx, workflow,
		[]domain.WorkflowRepository{{
			WorkflowID: workflow.ID, RepoKey: "backend", ProjectID: "wiring-proj",
		}},
		[]domain.WorkflowTask{task}, nil, nil,
		"wiring-create-"+idem, strings.Repeat("d", 64),
	); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if _, _, err := store.ActivateWorkflow(
		ctx, workflow.ID, 1, "wiring-activate-"+idem, strings.Repeat("e", 64), now,
	); err != nil {
		t.Fatalf("activate workflow: %v", err)
	}

	leaseUntil := now.Add(time.Hour)
	attempt := domain.WorkflowAttempt{
		ID:         domain.WorkflowAttemptID("wiring-attempt-" + idem),
		WorkflowID: workflow.ID, TaskID: task.ID, AttemptNo: 0,
		WorkerID: "worker-" + idem, LeaseToken: "lease-" + idem, LeaseUntil: &leaseUntil,
		State: domain.WorkflowAttemptStateClaimed, CreatedAt: now, UpdatedAt: now,
	}
	claimed, _, err := admission.Claim(
		ctx, attempt, 2, "wiring-claim-"+idem, strings.Repeat("f", 64),
	)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	return claimed.ID
}

// daemonIDFactory mints deterministic ids for the chat service under test.
func daemonIDFactory() chatsvc.IDFactory {
	var (
		mu      sync.Mutex
		counter int
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		counter++
		return fmt.Sprintf("daemon-test-id-%d", counter)
	}
}

// daemonSnapshotReader is the bounded snapshot read the real chat service needs. It calls
// the store's own loader rather than a fake, so the controller sees real rows.
func daemonSnapshotReader(st *sqlite.Store) chatsvc.SnapshotReader {
	return chatsvc.SnapshotReaderFunc(func(
		ctx context.Context,
		conversationID string,
	) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshot(ctx, conversationID)
		if err != nil {
			return chatsvc.ConversationRows{}, err
		}
		return chatsvc.ConversationRows{
			Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns,
			Messages: rows.Messages, Activities: rows.Activities,
			BranchPoints:               rows.BranchPoints,
			BranchedFromEarlierMessage: rows.BranchedFromEarlierMessage,
		}, nil
	})
}

// daemonSnapshotPageReader is the paginated form of the same read.
func daemonSnapshotPageReader(st *sqlite.Store) chatsvc.SnapshotPageReader {
	return chatsvc.SnapshotPageReaderFunc(func(
		ctx context.Context,
		id string,
		before, limit int64,
	) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshotPage(ctx, id, before, limit)
		if err != nil {
			return chatsvc.ConversationRows{}, err
		}
		return chatsvc.ConversationRows{
			Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns,
			Messages: rows.Messages, Activities: rows.Activities,
			BranchPoints:               rows.BranchPoints,
			BranchedFromEarlierMessage: rows.BranchedFromEarlierMessage,
		}, nil
	})
}

// daemonActivitySink accepts the controller's activity signals. The test asserts the
// capability evidence, not the activity stream, so this records nothing.
type daemonActivitySink struct{}

func (daemonActivitySink) ApplyActivitySignal(
	context.Context,
	domain.SessionID,
	ports.ActivitySignal,
) error {
	return nil
}

// TestM2CapabilityEvidenceReachesTheResolver proves the whole evidence chain runs in the
// REAL boot order: the recorder is built before the chat service, handed to it at
// construction, and read back by the resolver after a scripted turn confirms streaming.
//
// The point of the test is the ordering. A recorder constructed inside newM2Services would
// be created after the chat service and could never be given to it, so every confirmed
// runtime observation would be lost. This drives a real chat.Service over a real store with
// a scripted driver (the repository's own offline adapter: no process, no network, no
// credential) and asserts the resolver's session-scoped gate opens afterwards.
func TestM2CapabilityEvidenceReachesTheResolver(t *testing.T) {
	ctx := context.Background()
	store := openDaemonTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "evidence-proj", Path: t.TempDir(), RegisteredAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	// Step 1: the recorder exists BEFORE the chat service, exactly as daemon.go builds it.
	recorder := capability.NewRecorder(store)

	driver := scripted.NewDriver(scripted.Config{
		Harness: domain.HarnessCodex,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
		},
	})
	svc := chatsvc.New(chatsvc.Options{
		Store:              store,
		Reader:             daemonSnapshotReader(store),
		PageReader:         daemonSnapshotPageReader(store),
		Sessions:           store,
		Drivers:            scripted.Registry(driver),
		CapabilityEvidence: recorder,
		Activity:           daemonActivitySink{},
		DataDir:            t.TempDir(),
		Log:                slog.New(slog.DiscardHandler),
		Now:                func() time.Time { return time.Now().UTC() },
		// The service defaults Now but not NewID; without it the controller cannot mint a
		// conversation id. A deterministic counter keeps the test's rows reproducible.
		NewID: daemonIDFactory(),
	})

	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "evidence-proj", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityActive},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	workspace := t.TempDir()
	controller, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: session.ID, ProjectID: "evidence-proj", Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("start controller: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), session.ID) })
	generation := controller.Generation()
	if generation == "" {
		t.Fatal("the controller reported no generation, so the evidence would not be fenced")
	}

	// Before the turn: no confirmed runtime evidence exists for this generation, so a
	// session-scoped gate must refuse.
	resolver := capability.NewResolver(
		capability.WithRecorder(recorder), capability.WithClock(func() time.Time { return time.Now().UTC() }))
	before := resolver.Gate(ctx, ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: session.ID, Generation: generation,
	}, domain.CapabilityFeatureStreaming)
	if before.Allowed {
		t.Fatalf("streaming was already authorized before any turn ran: %+v", before)
	}

	// A confirmed runtime observation carries feature evidence, not a credential: the
	// recorder builds it on whatever session-scoped snapshot already exists, and a bare
	// one inherits the zero auth state, which the gate rightly treats as "not proven". The
	// boot path seeds that base from a real handshake, so the test does the same rather
	// than teaching the recorder to invent an authorization it never observed.
	if err := recorder.RecordCapabilities(ctx, domain.HarnessCapabilitySnapshot{
		Harness:           domain.HarnessCodex,
		Source:            domain.CapabilitySourceProviderHandshake,
		ObservedAt:        now,
		ExpiresAt:         now.Add(time.Hour),
		AuthState:         domain.AgentAuthenticationAuthorized,
		Readiness:         domain.CapabilityReady,
		Surfaces:          domain.CapabilitySurfaceChat,
		Streaming:         domain.CapabilityStateUnknown,
		Tools:             domain.CapabilityStateUnknown,
		Interrupt:         domain.CapabilityStateUnknown,
		Steer:             domain.CapabilityStateUnknown,
		Queue:             domain.CapabilityStateUnknown,
		Resume:            domain.CapabilityResumeUnknown,
		Approval:          domain.CapabilityApprovalUnknown,
		ApplyAt:           domain.CapabilityApplyAtUnknown,
		Usage:             domain.CapabilityUsageUnknown,
		ObservedSessionID: session.ID,
		Generation:        generation,
	}); err != nil {
		t.Fatalf("seed session capability base: %v", err)
	}

	// Step 2: one real dispatch through the real controller.
	if _, err := svc.Send(ctx, session.ID, ports.ChatUserMessage{
		Text: "hello", ClientMessageID: "evidence-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Step 3: the resolver now sees the confirmed observation the send produced.
	after := resolver.Gate(ctx, ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: session.ID, Generation: generation,
	}, domain.CapabilityFeatureStreaming)
	if !after.Allowed {
		t.Fatalf("streaming was not authorized after a confirmed dispatch: %+v", after)
	}
	if after.Source != domain.CapabilitySourceConfirmedRuntime {
		t.Fatalf("gate source = %q, want confirmed_runtime", after.Source)
	}
}

// TestM2SteerReceiptCarriesBothTurnIDsAgainstTheRealStore is the end-to-end proof that the
// applied-steer path is reachable in the shipped stack.
//
// It runs a REAL store, a REAL chat service with a scripted driver, and the REAL
// LocalGateway. A steer is only recordable as "applied" when the receipt carries both the
// provider turn and AO's own durable turn row, so the assertion is exactly that pair: the
// gateway must resolve the row the guidance joined rather than reporting the provider id
// alone.
func TestM2SteerReceiptCarriesBothTurnIDsAgainstTheRealStore(t *testing.T) {
	ctx := context.Background()
	store := openDaemonTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "steer-proj", Path: t.TempDir(), RegisteredAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	driver := scripted.NewDriver(scripted.Config{
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
	svc := chatsvc.New(chatsvc.Options{
		Store:      store,
		Reader:     daemonSnapshotReader(store),
		PageReader: daemonSnapshotPageReader(store),
		Sessions:   store,
		Drivers:    scripted.Registry(driver),
		Activity:   daemonActivitySink{},
		DataDir:    t.TempDir(),
		Log:        slog.New(slog.DiscardHandler),
		Now:        func() time.Time { return time.Now().UTC() },
		NewID:      daemonIDFactory(),
	})

	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "steer-proj", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityActive},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	controller, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: session.ID, ProjectID: "steer-proj", Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("start controller: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), session.ID) })

	// One real turn, so there is a running turn for the steer to join. Its ids are what the
	// receipt must reproduce.
	turn, err := svc.Send(ctx, session.ID, ports.ChatUserMessage{
		Text: "first", ClientMessageID: "steer-e2e-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turn.ID == "" || turn.ProviderTurnID == "" {
		t.Fatalf("the dispatched turn carries no ids: %+v", turn)
	}

	// The gateway the daemon builds, over the real store and the real chat service.
	gateway := execution.NewLocalGateway(execution.LocalGatewayDeps{
		Records:     store,
		Commits:     store,
		Worktrees:   store,
		Turns:       store,
		Chats:       svc,
		Controllers: svc,
		Clock:       func() time.Time { return time.Now().UTC() },
		Log:         slog.New(slog.DiscardHandler),
	})

	receipt, err := gateway.Steer(ctx, ports.MessageRequest{
		SessionID:            session.ID,
		ClientMessageID:      "steer-e2e-2",
		Text:                 "correct course",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: turn.ProviderTurnID,
		ControllerGeneration: controller.Generation(),
		Origin:               domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}

	if receipt.ProviderTurnID != turn.ProviderTurnID {
		t.Fatalf("providerTurnId = %q, want %q", receipt.ProviderTurnID, turn.ProviderTurnID)
	}
	if receipt.TurnID == "" {
		t.Fatal("turnId is empty: the applied path is unreachable because the receipt " +
			"carries only the provider turn")
	}
	if receipt.TurnID != turn.ID {
		t.Fatalf("turnId = %q, want AO's own turn row %q", receipt.TurnID, turn.ID)
	}

	// The exact predicate the intervention layer applies before recording "applied".
	fact := domain.InterventionChatFact{
		ChatTurnID:     receipt.TurnID,
		ProviderTurnID: receipt.ProviderTurnID,
	}
	if !fact.Licenses(domain.WorkflowInterventionStateApplied) {
		t.Fatalf("the receipt does not license an applied intervention: %+v", fact)
	}
}

// TestM2WiringGatewayResolvesDurableTurns proves the BUNDLE's gateway — not a hand-built
// one — resolves AO's turn row for a steer receipt.
//
// The e2e test above builds a LocalGateway by hand, so it would keep passing if the daemon
// forgot to inject the turn reader. This one asks the bundle's own gateway and asserts the
// applied predicate holds, which is the wiring link that made the shipped stack unable to
// record a successful steer.
func TestM2WiringGatewayResolvesDurableTurns(t *testing.T) {
	ctx := context.Background()
	store := openDaemonTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "bundle-steer-proj", Path: t.TempDir(), RegisteredAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	driver := scripted.NewDriver(scripted.Config{
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
	svc := chatsvc.New(chatsvc.Options{
		Store:      store,
		Reader:     daemonSnapshotReader(store),
		PageReader: daemonSnapshotPageReader(store),
		Sessions:   store,
		Drivers:    scripted.Registry(driver),
		Activity:   daemonActivitySink{},
		DataDir:    t.TempDir(),
		Log:        slog.New(slog.DiscardHandler),
		Now:        func() time.Time { return time.Now().UTC() },
		NewID:      daemonIDFactory(),
	})

	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "bundle-steer-proj", Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityActive},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	controller, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: session.ID, ProjectID: "bundle-steer-proj", Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("start controller: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), session.ID) })

	turn, err := svc.Send(ctx, session.ID, ports.ChatUserMessage{
		Text: "first", ClientMessageID: "bundle-steer-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The bundle the daemon actually builds, with the chat service as both chat owner and
	// controller probe.
	services := newM2ServicesWith(m2GatewayDeps{
		Store: store, Chats: svc, Log: slog.New(slog.DiscardHandler),
	})

	receipt, err := services.Gateway.Steer(ctx, ports.MessageRequest{
		SessionID:            session.ID,
		ClientMessageID:      "bundle-steer-2",
		Text:                 "correct course",
		Delivery:             ports.MessageDeliverySteer,
		TargetProviderTurnID: turn.ProviderTurnID,
		ControllerGeneration: controller.Generation(),
		Origin:               domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatalf("Steer through the bundle's gateway: %v", err)
	}
	if receipt.TurnID == "" {
		t.Fatal("the bundle's gateway reported no durable turn id: the daemon is not " +
			"injecting the turn reader, so no steer can be recorded as applied")
	}
	fact := domain.InterventionChatFact{
		ChatTurnID:     receipt.TurnID,
		ProviderTurnID: receipt.ProviderTurnID,
	}
	if !fact.Licenses(domain.WorkflowInterventionStateApplied) {
		t.Fatalf("the bundle's steer receipt does not license an applied intervention: %+v", receipt)
	}
}
