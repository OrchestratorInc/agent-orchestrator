package capability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Recorder evidence.
//
// The promise under test is narrow and load-bearing: evidence recorded because a
// provider ACTUALLY did something must be strong enough to authorize that feature
// on that session generation, and evidence that merely describes a provider must
// never be. Everything else here is the plumbing that keeps those two apart.

// memoryCapabilityStore is the CapabilityStore the recorder needs, in memory, so
// these tests exercise grading rules rather than SQLite.
type memoryCapabilityStore struct {
	mu       sync.Mutex
	harness  map[domain.AgentHarness]domain.HarnessCapabilitySnapshot
	sessions map[domain.SessionID]domain.HarnessCapabilitySnapshot
	err      error
}

func newMemoryCapabilityStore() *memoryCapabilityStore {
	return &memoryCapabilityStore{
		harness:  make(map[domain.AgentHarness]domain.HarnessCapabilitySnapshot),
		sessions: make(map[domain.SessionID]domain.HarnessCapabilitySnapshot),
	}
}

func (s *memoryCapabilityStore) UpsertHarnessCapabilitySnapshot(
	_ context.Context,
	snapshot domain.HarnessCapabilitySnapshot,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.harness[snapshot.Harness] = snapshot
	return nil
}

func (s *memoryCapabilityStore) GetHarnessCapabilitySnapshot(
	_ context.Context,
	harness domain.AgentHarness,
) (domain.HarnessCapabilitySnapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return domain.HarnessCapabilitySnapshot{}, false, s.err
	}
	snapshot, ok := s.harness[harness]
	return snapshot, ok, nil
}

func (s *memoryCapabilityStore) UpsertSessionCapabilitySnapshot(
	_ context.Context,
	snapshot domain.HarnessCapabilitySnapshot,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.sessions[snapshot.ObservedSessionID] = snapshot
	return nil
}

func (s *memoryCapabilityStore) GetSessionCapabilitySnapshot(
	_ context.Context,
	sessionID domain.SessionID,
) (domain.HarnessCapabilitySnapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return domain.HarnessCapabilitySnapshot{}, false, s.err
	}
	snapshot, ok := s.sessions[sessionID]
	return snapshot, ok, nil
}

// readyHarnessSnapshot is the harness-level fact a session confirmation inherits:
// an authorized, ready install. Without it the gate refuses on authorization, which
// is a separate and correct refusal.
func readyHarnessSnapshot(harness domain.AgentHarness, at time.Time) domain.HarnessCapabilitySnapshot {
	snapshot := domain.NewHarnessCapabilitySnapshot(
		harness, domain.CapabilitySourceProviderHandshake, at, DefaultTTL)
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	snapshot.Surfaces = domain.CapabilitySurfaceChat
	return snapshot
}

func TestRecordConfirmedProducesEvidenceTheGateAccepts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const sessionID, generation = domain.SessionID("p1-1"), "gen-1"

	store := newMemoryCapabilityStore()
	if err := store.UpsertHarnessCapabilitySnapshot(
		ctx, readyHarnessSnapshot(domain.HarnessCodex, testNow)); err != nil {
		t.Fatalf("seed harness snapshot: %v", err)
	}
	recorder := NewRecorder(store, WithRecorderClock(fixedClock(testNow)))

	if err := recorder.RecordConfirmed(ctx, domain.HarnessCodex, sessionID, generation,
		domain.CapabilityFeatureSteer); err != nil {
		t.Fatalf("RecordConfirmed: %v", err)
	}

	snapshot, found, err := recorder.LoadSessionCapabilities(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("load recorded session evidence: found=%v err=%v", found, err)
	}
	if snapshot.Source != domain.CapabilitySourceConfirmedRuntime {
		t.Fatalf("source = %q, want confirmed_runtime", snapshot.Source)
	}
	if snapshot.Generation != generation {
		t.Fatalf("generation = %q, want %q", snapshot.Generation, generation)
	}
	if !snapshot.Steer.Supported() {
		t.Fatalf("steer = %q, want supported", snapshot.Steer)
	}
	// Auth and readiness are harness facts. A session confirmation that forgot them
	// would be refused by the gate for a reason having nothing to do with steer.
	if snapshot.AuthState != domain.AgentAuthenticationAuthorized ||
		snapshot.Readiness != domain.CapabilityReady {
		t.Fatalf("inherited auth/readiness = %q/%q, want authorized/ready",
			snapshot.AuthState, snapshot.Readiness)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("recorded snapshot is invalid: %v", err)
	}

	resolver := NewResolver(WithClock(fixedClock(testNow)), WithRecorder(recorder))
	decision := resolver.Gate(ctx, ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
	}, domain.CapabilityFeatureSteer)
	if !decision.Allowed {
		t.Fatalf("Gate refused confirmed runtime evidence: state=%q source=%q reason=%q",
			decision.State, decision.Source, decision.ReasonCode)
	}
	if decision.Source != domain.CapabilitySourceConfirmedRuntime {
		t.Fatalf("decision source = %q, want confirmed_runtime", decision.Source)
	}
}

// The negative half: a static snapshot is display-only, so the same gate must
// refuse it however affirmative its feature columns look.
func TestStaticEvidenceNeverPassesTheGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	recorder := NewRecorder(newMemoryCapabilityStore(), WithRecorderClock(fixedClock(testNow)))

	static := handshakeSnapshot(domain.HarnessCodex, domain.SessionID("p1-1"), "gen-1", testNow)
	static.Source = domain.CapabilitySourceStatic
	if err := recorder.RecordCapabilities(ctx, static); err != nil {
		t.Fatalf("record static snapshot: %v", err)
	}

	resolver := NewResolver(WithClock(fixedClock(testNow)), WithRecorder(recorder))
	decision := resolver.Gate(ctx, ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: static.ObservedSessionID, Generation: static.Generation,
	}, domain.CapabilityFeatureSteer)
	if decision.Allowed {
		t.Fatalf("static evidence authorized steer (source=%q)", decision.Source)
	}
	if decision.ReasonCode != ports.CapabilityReasonStaticOnly {
		t.Fatalf("reason = %q, want %q", decision.ReasonCode, ports.CapabilityReasonStaticOnly)
	}
}

// A second confirmation must not erase the first: each call adds one feature.
func TestRecordConfirmedAccumulatesAcrossCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const sessionID, generation = domain.SessionID("p1-1"), "gen-1"
	store := newMemoryCapabilityStore()
	recorder := NewRecorder(store, WithRecorderClock(fixedClock(testNow)))

	if err := recorder.RecordConfirmed(ctx, domain.HarnessCodex, sessionID, generation,
		domain.CapabilityFeatureSteer); err != nil {
		t.Fatalf("record steer: %v", err)
	}
	if err := recorder.RecordConfirmed(ctx, domain.HarnessCodex, sessionID, generation,
		domain.CapabilityFeatureInterrupt); err != nil {
		t.Fatalf("record interrupt: %v", err)
	}

	snapshot, found, err := recorder.LoadSessionCapabilities(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if !snapshot.Steer.Supported() || !snapshot.Interrupt.Supported() {
		t.Fatalf("steer/interrupt = %q/%q, want both supported",
			snapshot.Steer, snapshot.Interrupt)
	}
	// Features never confirmed stay unknown. Absence of evidence is not a negative.
	if snapshot.Tools != domain.CapabilityStateUnknown {
		t.Fatalf("tools = %q, want unknown", snapshot.Tools)
	}
}

// A confirmation must name the generation it was observed in; without one the
// evidence could outlive the controller it describes.
func TestRecordConfirmedRequiresSessionAndGeneration(t *testing.T) {
	t.Parallel()
	recorder := NewRecorder(newMemoryCapabilityStore())
	if err := recorder.RecordConfirmed(context.Background(), domain.HarnessCodex, "", "gen-1",
		domain.CapabilityFeatureSteer); err == nil {
		t.Fatal("RecordConfirmed accepted a confirmation with no session")
	}
	if err := recorder.RecordConfirmed(context.Background(), domain.HarnessCodex, "p1-1", "",
		domain.CapabilityFeatureSteer); !errors.Is(err, domain.ErrInvalidCapabilityGeneration) {
		t.Fatalf("error = %v, want %v", err, domain.ErrInvalidCapabilityGeneration)
	}
}

// A recorder with no store must say so rather than silently succeeding: a caller
// that believed evidence was stored would later gate on nothing.
func TestRecorderWithoutStoreRefuses(t *testing.T) {
	t.Parallel()
	recorder := NewRecorder(nil)
	err := recorder.RecordConfirmed(context.Background(), domain.HarnessCodex, "p1-1", "gen-1",
		domain.CapabilityFeatureSteer)
	if !errors.Is(err, ErrRecorderUnavailable) {
		t.Fatalf("error = %v, want ErrRecorderUnavailable", err)
	}
	if _, _, err := recorder.LoadCapabilities(context.Background(), domain.HarnessCodex); !errors.Is(err, ErrRecorderUnavailable) {
		t.Fatalf("load error = %v, want ErrRecorderUnavailable", err)
	}
}
