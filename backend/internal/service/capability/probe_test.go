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

// Chat driver probe tests.
//
// The rule under test throughout is that a probe may raise evidence and never lower a
// capability into a negative it did not observe: a driver that does not advertise a
// feature leaves it unknown, and only a provider refusal could make it unsupported.

var probeNow = time.Date(2026, 11, 4, 11, 0, 0, 0, time.UTC)

type fakeReadiness struct {
	installation   domain.AgentInstallationState
	authentication domain.AgentAuthenticationState
	err            error
	calls          int
}

func (f *fakeReadiness) EnsureAgentReadiness(
	_ context.Context,
	_ string,
	_ domain.AgentReadinessPurpose,
) (domain.AgentReadinessSnapshot, error) {
	f.calls++
	if f.err != nil {
		return domain.AgentReadinessSnapshot{}, f.err
	}
	return domain.AgentReadinessSnapshot{
		Installation:   domain.AgentInstallationObservation{State: f.installation},
		Authentication: domain.AgentAuthenticationObservation{State: f.authentication},
	}, nil
}

func (f *fakeReadiness) InvalidateAgentInstallation(string)   {}
func (f *fakeReadiness) InvalidateAgentAuthentication(string) {}
func (f *fakeReadiness) RecheckAgent(string)                  {}

type fakeDriver struct {
	harness domain.AgentHarness
	caps    ports.ChatCapabilities
	err     error
	calls   int
}

func (f *fakeDriver) Harness() domain.AgentHarness { return f.harness }

func (f *fakeDriver) Probe(context.Context) (ports.ChatCapabilities, error) {
	f.calls++
	return f.caps, f.err
}

func (f *fakeDriver) Start(
	context.Context,
	ports.ChatStartConfig,
) (ports.ChatConversation, error) {
	return nil, errors.New("not used in this test")
}

func (f *fakeDriver) Resume(
	context.Context,
	ports.ChatResumeConfig,
) (ports.ChatConversation, error) {
	return nil, errors.New("not used in this test")
}

type fakeRegistry struct {
	drivers map[domain.AgentHarness]ports.ChatDriver
}

func (f *fakeRegistry) Driver(harness domain.AgentHarness) (ports.ChatDriver, error) {
	driver, ok := f.drivers[harness]
	if !ok {
		return nil, ports.ErrChatUnsupported
	}
	return driver, nil
}

func (f *fakeRegistry) SupportsChat(harness domain.AgentHarness) bool {
	_, ok := f.drivers[harness]
	return ok
}

type fakeSessionProber struct {
	mu   sync.Mutex
	live bool
	caps ports.ChatCapabilities
	gen  string
	// genOK overrides whether SessionGeneration reports a usable generation. It exists so a test
	// can express "a controller is live but cannot say which generation it is", which is a
	// different fact from "no controller exists".
	genOK *bool
}

func (f *fakeSessionProber) SessionGeneration(domain.SessionID) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.genOK != nil {
		return f.gen, *f.genOK
	}
	return f.gen, f.live
}

func (f *fakeSessionProber) ChatCapabilities(domain.SessionID) (ports.ChatCapabilities, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.caps, f.live
}

func newProbeFixture(
	installation domain.AgentInstallationState,
	authentication domain.AgentAuthenticationState,
	caps ports.ChatCapabilities,
) (*fakeReadiness, *fakeDriver, ports.CapabilityProbe) {
	readiness := &fakeReadiness{installation: installation, authentication: authentication}
	driver := &fakeDriver{harness: domain.HarnessCodex, caps: caps}
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{
		domain.HarnessCodex: driver,
	}}
	probe := NewChatDriverProbe(nil, registry, readiness, func() time.Time { return probeNow }, time.Minute)
	return readiness, driver, probe
}

func TestChatDriverProbeReportsAHandshake(t *testing.T) {
	_, driver, probe := newProbeFixture(
		domain.AgentInstallationInstalled,
		domain.AgentAuthenticationAuthorized,
		ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityTools:     true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilitySteer:     true,
			ports.ChatCapabilityUsage:     true,
			ports.ChatCapabilityResume:    true,
			ports.ChatCapabilityHistory:   true,
		},
	)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if driver.calls != 1 {
		t.Fatalf("driver Probe calls = %d, want 1", driver.calls)
	}
	if snapshot.Source != domain.CapabilitySourceProviderHandshake {
		t.Fatalf("source = %q, want provider_handshake", snapshot.Source)
	}
	if snapshot.Readiness != domain.CapabilityReady {
		t.Fatalf("readiness = %q, want ready", snapshot.Readiness)
	}
	if snapshot.AuthState != domain.AgentAuthenticationAuthorized {
		t.Fatalf("auth = %q, want authorized", snapshot.AuthState)
	}
	for name, state := range map[string]domain.CapabilityState{
		"streaming": snapshot.Streaming,
		"tools":     snapshot.Tools,
		"interrupt": snapshot.Interrupt,
		"steer":     snapshot.Steer,
	} {
		if state != domain.CapabilitySupported {
			t.Fatalf("%s = %q, want supported", name, state)
		}
	}
	if snapshot.Resume != domain.CapabilityResumeWithHistory {
		t.Fatalf("resume = %q, want with-history", snapshot.Resume)
	}
	if snapshot.Usage != domain.CapabilityUsageEstimated {
		t.Fatalf("usage = %q, want estimated", snapshot.Usage)
	}
	if snapshot.ExpiresAt != probeNow.Add(time.Minute) {
		t.Fatalf("expiresAt = %v, want now+ttl", snapshot.ExpiresAt)
	}
}

func TestChatDriverProbeReportsUnadvertisedFeaturesAsUnknown(t *testing.T) {
	// A driver that advertises nothing at all: every feature must stay unknown, because
	// a missing key is not a provider refusal.
	_, _, probe := newProbeFixture(
		domain.AgentInstallationInstalled,
		domain.AgentAuthenticationAuthorized,
		ports.ChatCapabilities{},
	)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	for name, state := range map[string]domain.CapabilityState{
		"streaming": snapshot.Streaming,
		"tools":     snapshot.Tools,
		"interrupt": snapshot.Interrupt,
		"steer":     snapshot.Steer,
		"queue":     snapshot.Queue,
	} {
		if state != domain.CapabilityStateUnknown {
			t.Fatalf("%s = %q, want unknown for an unadvertised feature", name, state)
		}
	}
	if snapshot.Resume != domain.CapabilityResumeUnknown {
		t.Fatalf("resume = %q, want unknown", snapshot.Resume)
	}
	if snapshot.Approval != domain.CapabilityApprovalUnknown {
		t.Fatalf("approval = %q, want unknown", snapshot.Approval)
	}
}

func TestChatDriverProbeNeverClaimsUnsupported(t *testing.T) {
	// No matter what the driver advertises, the probe must not produce an unsupported
	// verdict: it holds no observation that a provider cannot do something.
	for _, caps := range []ports.ChatCapabilities{
		{},
		{ports.ChatCapabilityStreaming: true},
		{ports.ChatCapabilitySteer: false},
		{ports.ChatCapabilityApprovals: true},
	} {
		_, _, probe := newProbeFixture(
			domain.AgentInstallationInstalled,
			domain.AgentAuthenticationAuthorized,
			caps,
		)
		snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
			Harness: domain.HarnessCodex,
		})
		if err != nil {
			t.Fatalf("ProbeCapabilities: %v", err)
		}
		for name, state := range map[string]domain.CapabilityState{
			"streaming": snapshot.Streaming,
			"tools":     snapshot.Tools,
			"interrupt": snapshot.Interrupt,
			"steer":     snapshot.Steer,
			"queue":     snapshot.Queue,
		} {
			if state.DeterminatelyUnsupported() {
				t.Fatalf("%s = unsupported for caps %v; the probe cannot prove a refusal", name, caps)
			}
		}
	}
}

func TestChatDriverProbeMissingBinaryBlocksAndStaysLocal(t *testing.T) {
	_, driver, probe := newProbeFixture(
		domain.AgentInstallationNotInstalled,
		domain.AgentAuthenticationUnknown,
		ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
	)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if driver.calls != 0 {
		t.Fatalf("a missing binary still probed the driver %d times", driver.calls)
	}
	if snapshot.Readiness != domain.CapabilityBlockedBinary {
		t.Fatalf("readiness = %q, want blocked_binary", snapshot.Readiness)
	}
	if snapshot.Source != domain.CapabilitySourceLocalProbe {
		t.Fatalf("source = %q, want local_probe", snapshot.Source)
	}
	if snapshot.ReasonCode != ports.CapabilityReasonBinaryMissing {
		t.Fatalf("reason = %q, want binary missing", snapshot.ReasonCode)
	}
	if snapshot.Streaming != domain.CapabilityStateUnknown {
		t.Fatalf("streaming = %q, want unknown when the binary is absent", snapshot.Streaming)
	}
}

func TestChatDriverProbeUnauthorizedCredentialIsNotReady(t *testing.T) {
	_, driver, probe := newProbeFixture(
		domain.AgentInstallationInstalled,
		domain.AgentAuthenticationUnauthorized,
		ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
	)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if driver.calls != 0 {
		t.Fatalf("an unauthorized credential still probed the driver %d times", driver.calls)
	}
	if snapshot.Readiness == domain.CapabilityReady {
		t.Fatal("an unauthorized credential reported ready")
	}
	if snapshot.Source.AllowsSessionGating() {
		t.Fatalf("source = %q, want a non-gating grade", snapshot.Source)
	}
}

func TestChatDriverProbeConfiguredCredentialIsNotReady(t *testing.T) {
	// "configured" means a credential exists and nothing proved it works.
	_, _, probe := newProbeFixture(
		domain.AgentInstallationInstalled,
		domain.AgentAuthenticationConfigured,
		ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
	)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if snapshot.Readiness == domain.CapabilityReady {
		t.Fatal("an unverified credential reported ready")
	}
	if snapshot.ReasonCode != ports.CapabilityReasonAuthUnverified {
		t.Fatalf("reason = %q, want auth unverified", snapshot.ReasonCode)
	}
}

func TestChatDriverProbeFailedHandshakeIsUnknownNotNegative(t *testing.T) {
	failure := errors.New("handshake refused")
	readiness := &fakeReadiness{
		installation:   domain.AgentInstallationInstalled,
		authentication: domain.AgentAuthenticationAuthorized,
	}
	driver := &fakeDriver{harness: domain.HarnessCodex, err: failure}
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{
		domain.HarnessCodex: driver,
	}}
	probe := NewChatDriverProbe(nil, registry, readiness, func() time.Time { return probeNow }, time.Minute)

	_, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	})
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v, want the driver's own failure", err)
	}
}

func TestChatDriverProbeWithoutADriverStaysLocal(t *testing.T) {
	readiness := &fakeReadiness{
		installation:   domain.AgentInstallationInstalled,
		authentication: domain.AgentAuthenticationAuthorized,
	}
	// No driver registered for the harness at all.
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{}}
	probe := NewChatDriverProbe(nil, registry, readiness, func() time.Time { return probeNow }, time.Minute)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if snapshot.Source != domain.CapabilitySourceLocalProbe {
		t.Fatalf("source = %q, want local_probe without a handshake", snapshot.Source)
	}
	if snapshot.Steer != domain.CapabilityStateUnknown {
		t.Fatalf("steer = %q, want unknown", snapshot.Steer)
	}
}

// TestChatDriverProbeSessionScopedReportsANegotiation is the R-05 regression for the evidence
// grade: a live controller reporting what it negotiated is a handshake, never a confirmed
// runtime observation. confirmed_runtime is reachable only through Recorder.RecordConfirmed,
// which is written by the chat service after an operation actually landed.
func TestChatDriverProbeSessionScopedReportsANegotiation(t *testing.T) {
	readiness := &fakeReadiness{
		installation:   domain.AgentInstallationInstalled,
		authentication: domain.AgentAuthenticationAuthorized,
	}
	driver := &fakeDriver{harness: domain.HarnessCodex, caps: ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
	}}
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{
		domain.HarnessCodex: driver,
	}}
	prober := &fakeSessionProber{
		live: true,
		gen:  "gen-live",
		caps: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilitySteer:     true,
		},
	}
	probe := NewChatDriverProbe(prober, registry, readiness, func() time.Time { return probeNow }, time.Minute)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness:    domain.HarnessCodex,
		SessionID:  "sess-1",
		Generation: "gen-asked",
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if snapshot.Source != domain.CapabilitySourceProviderHandshake {
		t.Fatalf("source = %q, want provider_handshake for a reported negotiation", snapshot.Source)
	}
	if snapshot.Source == domain.CapabilitySourceConfirmedRuntime {
		t.Fatal("a negotiated capability set was graded as confirmed_runtime")
	}
	if snapshot.ObservedSessionID != "sess-1" {
		t.Fatalf("session = %q, want sess-1", snapshot.ObservedSessionID)
	}
	if snapshot.Generation != "gen-live" {
		t.Fatalf("generation = %q, want the live controller's own generation", snapshot.Generation)
	}
	if snapshot.Generation == "gen-asked" {
		t.Fatal("the probe echoed the caller's generation claim")
	}
	if snapshot.Steer != domain.CapabilitySupported {
		t.Fatalf("steer = %q, want supported from the live controller", snapshot.Steer)
	}
}

// TestChatDriverProbeSessionNeverWidensAnAbsentNegotiation is the fail-closed half of R-05: a
// harness that advertises steer must not let a session that did NOT negotiate steer inherit it.
// The absence is cleared to unknown — never to unsupported, which would be a fabricated negative.
func TestChatDriverProbeSessionNeverWidensAnAbsentNegotiation(t *testing.T) {
	readiness := &fakeReadiness{
		installation:   domain.AgentInstallationInstalled,
		authentication: domain.AgentAuthenticationAuthorized,
	}
	driver := &fakeDriver{harness: domain.HarnessCodex, caps: ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilitySteer:     true,
		ports.ChatCapabilityInterrupt: true,
	}}
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{
		domain.HarnessCodex: driver,
	}}
	// The live controller negotiated streaming only.
	prober := &fakeSessionProber{
		live: true, gen: "gen-live",
		caps: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
	}
	probe := NewChatDriverProbe(prober, registry, readiness, func() time.Time { return probeNow }, time.Minute)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "sess-1", Generation: "gen-live",
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if snapshot.Steer != domain.CapabilityStateUnknown {
		t.Fatalf("steer = %q, want unknown for a session that did not negotiate it", snapshot.Steer)
	}
	if snapshot.Steer == domain.CapabilityUnsupported {
		t.Fatal("an absent negotiation was recorded as a fabricated negative")
	}
	if snapshot.Interrupt != domain.CapabilityStateUnknown {
		t.Fatalf("interrupt = %q, want unknown for a session that did not negotiate it", snapshot.Interrupt)
	}
	if snapshot.Streaming != domain.CapabilitySupported {
		t.Fatalf("streaming = %q, want the feature the controller DID negotiate", snapshot.Streaming)
	}

	// And the gate agrees: a harness advertisement alone may not authorize the session's steer.
	resolver := NewResolver(WithClock(func() time.Time { return probeNow }))
	resolver.RegisterProbe(domain.HarnessCodex, probe)
	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "sess-1", Generation: "gen-live",
	}, domain.CapabilityFeatureSteer)
	if decision.Allowed {
		t.Fatal("a session that did not negotiate steer was authorized to steer")
	}
	if decision.ReasonCode != ports.CapabilityReasonNotObserved {
		t.Fatalf("reason = %q, want not_observed", decision.ReasonCode)
	}
}

// TestChatDriverProbeUnknownGenerationNeverAuthorizes covers the observed-generation rule: when
// the live controller cannot report a generation, the snapshot must not carry one, and the gate
// must refuse rather than comparing a caller-supplied claim with itself.
func TestChatDriverProbeUnknownGenerationNeverAuthorizes(t *testing.T) {
	readiness := &fakeReadiness{
		installation:   domain.AgentInstallationInstalled,
		authentication: domain.AgentAuthenticationAuthorized,
	}
	driver := &fakeDriver{harness: domain.HarnessCodex, caps: ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilitySteer:     true,
	}}
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{
		domain.HarnessCodex: driver,
	}}
	// Live, but it cannot say which generation it is: ChatCapabilities succeeds while
	// SessionGeneration reports no usable generation.
	noGeneration := false
	prober := &fakeSessionProber{
		live: true, gen: "", genOK: &noGeneration,
		caps: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilitySteer:     true,
		},
	}
	probe := NewChatDriverProbe(prober, registry, readiness, func() time.Time { return probeNow }, time.Minute)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "sess-1", Generation: "gen-claimed",
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if snapshot.Generation != "" {
		t.Fatalf("generation = %q, want empty when the controller cannot report one", snapshot.Generation)
	}
	if snapshot.ReasonCode != ports.CapabilityReasonNoSession &&
		snapshot.ReasonCode != ports.CapabilityReasonSessionGenerationUnknown {
		t.Fatalf("reason = %q, want a no-session/session-generation-unknown code", snapshot.ReasonCode)
	}
	if snapshot.Source.AllowsSessionGating() && snapshot.Steer == domain.CapabilitySupported {
		t.Fatal("an unbound session observation kept a supported steer")
	}

	// The gate refuses even though the caller asked about "gen-claimed".
	resolver := NewResolver(WithClock(func() time.Time { return probeNow }))
	resolver.RegisterProbe(domain.HarnessCodex, probe)
	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "sess-1", Generation: "gen-claimed",
	}, domain.CapabilityFeatureSteer)
	if decision.Allowed {
		t.Fatal("a session whose generation could not be observed authorized a steer")
	}
}

// TestChatDriverProbeGenerationMismatchIsRefused proves the comparison is real: an observation
// of a live controller carries THAT controller's generation, so a caller asking about a
// different generation is refused as a generation mismatch rather than silently matched.
func TestChatDriverProbeGenerationMismatchIsRefused(t *testing.T) {
	readiness := &fakeReadiness{
		installation:   domain.AgentInstallationInstalled,
		authentication: domain.AgentAuthenticationAuthorized,
	}
	driver := &fakeDriver{harness: domain.HarnessCodex, caps: ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
	}}
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{
		domain.HarnessCodex: driver,
	}}
	prober := &fakeSessionProber{
		live: true, gen: "gen-rotated",
		caps: ports.ChatCapabilities{ports.ChatCapabilitySteer: true},
	}
	probe := NewChatDriverProbe(prober, registry, readiness, func() time.Time { return probeNow }, time.Minute)

	resolver := NewResolver(WithClock(func() time.Time { return probeNow }))
	resolver.RegisterProbe(domain.HarnessCodex, probe)
	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "sess-1", Generation: "gen-stale",
	}, domain.CapabilityFeatureSteer)
	if decision.Allowed {
		t.Fatal("a rotated generation authorized a steer")
	}
	if decision.ReasonCode != ports.CapabilityReasonGenerationMismatch {
		t.Fatalf("reason = %q, want generation_mismatch", decision.ReasonCode)
	}
}

// TestChatDriverProbeEmptyRequestGenerationAuthorizesNothing is the "a caller cannot supply the
// generation" rule stated as a property: with no caller generation AND no observed generation, a
// session-scoped answer never authorizes anything.
func TestChatDriverProbeEmptyRequestGenerationAuthorizesNothing(t *testing.T) {
	readiness := &fakeReadiness{
		installation:   domain.AgentInstallationInstalled,
		authentication: domain.AgentAuthenticationAuthorized,
	}
	driver := &fakeDriver{harness: domain.HarnessCodex, caps: ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true, ports.ChatCapabilitySteer: true,
	}}
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{
		domain.HarnessCodex: driver,
	}}
	probe := NewChatDriverProbe(&fakeSessionProber{live: false}, registry, readiness,
		func() time.Time { return probeNow }, time.Minute)

	resolver := NewResolver(WithClock(func() time.Time { return probeNow }))
	resolver.RegisterProbe(domain.HarnessCodex, probe)
	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "sess-1",
	}, domain.CapabilityFeatureSteer)
	if decision.Allowed {
		t.Fatal("a session-scoped request with no generation authorized a steer")
	}
	if decision.ReasonCode != ports.CapabilityReasonNoSession {
		t.Fatalf("reason = %q, want no_session", decision.ReasonCode)
	}
}

func TestChatDriverProbeSessionScopedWithoutControllerReportsNoSession(t *testing.T) {
	readiness := &fakeReadiness{
		installation:   domain.AgentInstallationInstalled,
		authentication: domain.AgentAuthenticationAuthorized,
	}
	driver := &fakeDriver{harness: domain.HarnessCodex, caps: ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
	}}
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{
		domain.HarnessCodex: driver,
	}}
	probe := NewChatDriverProbe(
		&fakeSessionProber{live: false}, registry, readiness,
		func() time.Time { return probeNow }, time.Minute)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness:    domain.HarnessCodex,
		SessionID:  "sess-1",
		Generation: "gen-1",
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if snapshot.ReasonCode != ports.CapabilityReasonNoSession {
		t.Fatalf("reason = %q, want no_session", snapshot.ReasonCode)
	}
	if snapshot.Source == domain.CapabilitySourceConfirmedRuntime {
		t.Fatal("a session with no controller reported confirmed runtime evidence")
	}
}

func TestChatDriverProbeWithoutReadinessStaysUnknown(t *testing.T) {
	driver := &fakeDriver{harness: domain.HarnessCodex, caps: ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
	}}
	registry := &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{
		domain.HarnessCodex: driver,
	}}
	probe := NewChatDriverProbe(nil, registry, nil, func() time.Time { return probeNow }, time.Minute)

	snapshot, err := probe.ProbeCapabilities(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if snapshot.Source.AllowsSessionGating() {
		t.Fatalf("source = %q, want a non-gating grade without readiness evidence", snapshot.Source)
	}
	if driver.calls != 0 {
		t.Fatalf("driver Probe calls = %d, want 0 without readiness evidence", driver.calls)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("the probe produced an invalid snapshot: %v", err)
	}
}

func TestResolverAcceptsTheProbeAndGatesOnIt(t *testing.T) {
	// The end-to-end proof that the probe is usable as a resolver input: a real handshake
	// authorizes a feature, and the resolver's own checks still apply.
	_, _, probe := newProbeFixture(
		domain.AgentInstallationInstalled,
		domain.AgentAuthenticationAuthorized,
		ports.ChatCapabilities{ports.ChatCapabilitySteer: true},
	)
	resolver := NewResolver(WithClock(func() time.Time { return probeNow }), WithTTL(time.Minute))
	resolver.RegisterProbe(domain.HarnessCodex, probe)

	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	}, domain.CapabilityFeatureSteer)
	if !decision.Allowed {
		t.Fatalf("steer was refused after a successful handshake: %+v", decision)
	}

	// A feature the driver did not advertise is not authorized.
	unknown := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	}, domain.CapabilityFeatureTools)
	if unknown.Allowed {
		t.Fatalf("an unadvertised feature was authorized: %+v", unknown)
	}
}
