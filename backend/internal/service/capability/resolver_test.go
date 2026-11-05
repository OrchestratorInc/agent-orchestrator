package capability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var testNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func fixedClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

// handshakeSnapshot builds a session-scoped handshake observation for one session
// generation. It is the only shape that may authorize a per-session feature.
func handshakeSnapshot(
	harness domain.AgentHarness,
	sessionID domain.SessionID,
	generation string,
	observedAt time.Time,
) domain.HarnessCapabilitySnapshot {
	snapshot := domain.NewHarnessCapabilitySnapshot(
		harness, domain.CapabilitySourceProviderHandshake, observedAt, DefaultTTL,
	)
	snapshot.ObservedSessionID = sessionID
	snapshot.Generation = generation
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	snapshot.Surfaces = domain.CapabilitySurfaceChat
	snapshot.Streaming = domain.CapabilitySupported
	snapshot.Tools = domain.CapabilitySupported
	snapshot.Interrupt = domain.CapabilitySupported
	snapshot.Steer = domain.CapabilitySupported
	snapshot.Queue = domain.CapabilitySupported
	snapshot.Resume = domain.CapabilityResumeWithHistory
	snapshot.Approval = domain.CapabilityApprovalEnforced
	snapshot.ApplyAt = domain.CapabilityApplyAtTurn
	snapshot.Usage = domain.CapabilityUsageExact
	return snapshot
}

// TestGateAllowsOnlyHandshakeOrRuntimeEvidence is the evidence-grade contract:
// static and local_probe may describe a harness, never authorize a feature.
func TestGateAllowsOnlyHandshakeOrRuntimeEvidence(t *testing.T) {
	t.Parallel()
	for _, source := range []domain.CapabilitySource{
		domain.CapabilitySourceStatic,
		domain.CapabilitySourceLocalProbe,
		domain.CapabilitySourceProviderHandshake,
		domain.CapabilitySourceConfirmedRuntime,
	} {
		t.Run(string(source), func(t *testing.T) {
			t.Parallel()
			sessionID, generation := domain.SessionID("p1-1"), "gen-1"
			snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
			snapshot.Source = source

			probe := NewFakeProbe().Script(snapshot)
			resolver := NewResolver(WithClock(fixedClock(testNow)))
			resolver.RegisterProbe(domain.HarnessCodex, probe)

			decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			}, domain.CapabilityFeatureSteer)

			wantAllowed := source.AllowsSessionGating()
			if decision.Allowed != wantAllowed {
				t.Fatalf("source %q Gate allowed = %v, want %v (state=%q reason=%q)",
					source, decision.Allowed, wantAllowed, decision.State, decision.ReasonCode)
			}
			if !wantAllowed {
				if decision.State != domain.CapabilityStateUnknown {
					t.Fatalf("a refused gate must report unknown, got %q", decision.State)
				}
				if decision.ReasonCode != ports.CapabilityReasonStaticOnly {
					t.Fatalf("reason = %q, want %q", decision.ReasonCode, ports.CapabilityReasonStaticOnly)
				}
			}
		})
	}
}

// TestGateStaticProfileNeverAuthorizes proves the display catalog cannot leak into
// a decision even when a harness's static profile says "supported".
func TestGateStaticProfileNeverAuthorizes(t *testing.T) {
	t.Parallel()
	resolver := NewResolver(WithClock(fixedClock(testNow)))

	// Codex's static profile claims steer: supported. Gate must still refuse it.
	for _, harness := range SupportedHarnesses() {
		for _, feature := range []domain.CapabilityFeature{
			domain.CapabilityFeatureSteer,
			domain.CapabilityFeatureInterrupt,
			domain.CapabilityFeatureResume,
			domain.CapabilityFeatureApproval,
			domain.CapabilityFeatureUsage,
			domain.CapabilityFeatureStreaming,
		} {
			decision := resolver.Gate(context.Background(),
				ports.CapabilityProbeRequest{Harness: harness}, feature)
			if decision.Allowed {
				t.Fatalf("%s/%s authorized from static evidence alone (reason=%q)",
					harness, feature, decision.ReasonCode)
			}
			if decision.ReasonCode != ports.CapabilityReasonStaticOnly {
				t.Fatalf("%s/%s reason = %q, want %q",
					harness, feature, decision.ReasonCode, ports.CapabilityReasonStaticOnly)
			}
		}
	}
}

// TestGateUnknownObservationIsNotUnsupported proves that a handshake which simply
// did not report steer yields unknown, not "unsupported". A client must keep the
// control visible-but-unproven rather than hiding a working feature.
func TestGateUnknownObservationIsNotUnsupported(t *testing.T) {
	t.Parallel()
	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	snapshot := handshakeSnapshot(domain.HarnessClaudeCode, sessionID, generation, testNow)
	snapshot.Steer = domain.CapabilityStateUnknown

	probe := NewFakeProbe().Script(snapshot)
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	resolver.RegisterProbe(domain.HarnessClaudeCode, probe)

	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessClaudeCode, SessionID: sessionID, Generation: generation,
	}, domain.CapabilityFeatureSteer)

	if decision.Allowed {
		t.Fatal("unknown observation must not authorize steer")
	}
	if decision.State != domain.CapabilityStateUnknown {
		t.Fatalf("State = %q, want unknown", decision.State)
	}
	if decision.State.DeterminatelyUnsupported() {
		t.Fatal("a provider that never reported steer must not be reported as determinately unsupported")
	}
}

// TestGateFailedProbeYieldsUnknownWithReason proves a probe failure is reported as
// unknown and the failure reason survives.
func TestGateFailedProbeYieldsUnknownWithReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		reason string
	}{
		{"generic failure", errors.New("socket closed"), ports.CapabilityReasonProbeFailed},
		{"deadline", context.DeadlineExceeded, ports.CapabilityReasonProbeTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			probe := NewFakeProbe().ScriptError(domain.HarnessOMP, tt.err)
			resolver := NewResolver(WithClock(fixedClock(testNow)))
			resolver.RegisterProbe(domain.HarnessOMP, probe)

			sessionID, generation := domain.SessionID("p1-1"), "gen-1"
			snapshot, err := resolver.Resolve(context.Background(), ports.CapabilityProbeRequest{
				Harness: domain.HarnessOMP, SessionID: sessionID, Generation: generation,
			})
			if err == nil {
				t.Fatal("Resolve must surface the probe failure")
			}
			if snapshot.Readiness != domain.CapabilityUnknown {
				t.Fatalf("Readiness = %q, want unknown after a failed probe", snapshot.Readiness)
			}
			if snapshot.Steer != domain.CapabilityStateUnknown {
				t.Fatalf("Steer = %q, want unknown after a failed probe", snapshot.Steer)
			}
			if snapshot.ReasonCode != tt.reason {
				t.Fatalf("ReasonCode = %q, want %q", snapshot.ReasonCode, tt.reason)
			}

			decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
				Harness: domain.HarnessOMP, SessionID: sessionID, Generation: generation,
			}, domain.CapabilityFeatureSteer)
			if decision.Allowed {
				t.Fatal("a failed probe must never authorize a feature")
			}
			if decision.ReasonCode == "" {
				t.Fatal("a refused decision must carry a reason code")
			}
		})
	}
}

// TestGateExpiredEvidenceYieldsUnknown preserves the reason code while refusing to
// act on stale evidence.
func TestGateExpiredEvidenceYieldsUnknown(t *testing.T) {
	t.Parallel()
	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	observedAt := testNow.Add(-10 * time.Minute)
	snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, observedAt)
	snapshot.ExpiresAt = observedAt.Add(time.Minute)

	probe := NewFakeProbe().Script(snapshot)
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	resolver.RegisterProbe(domain.HarnessCodex, probe)

	resolved, err := resolver.Resolve(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Steer != domain.CapabilityStateUnknown {
		t.Fatalf("expired evidence Steer = %q, want unknown", resolved.Steer)
	}
	if resolved.Usage != domain.CapabilityUsageUnknown {
		t.Fatalf("expired evidence Usage = %q, want unknown", resolved.Usage)
	}
	if resolved.Readiness != domain.CapabilityUnknown {
		t.Fatalf("expired evidence Readiness = %q, want unknown", resolved.Readiness)
	}
	if resolved.ReasonCode == "" {
		t.Fatal("expired evidence must preserve a reason code")
	}

	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
	}, domain.CapabilityFeatureSteer)
	if decision.Allowed {
		t.Fatal("expired evidence must never authorize steer")
	}
	if decision.ReasonCode != ports.CapabilityReasonSnapshotExpired {
		t.Fatalf("reason = %q, want %q", decision.ReasonCode, ports.CapabilityReasonSnapshotExpired)
	}
}

// TestGateRejectsDifferentSession is the cross-session fencing test: evidence
// observed for session A must never gate session B, even when the harness matches
// and the generation string happens to be identical.
func TestGateRejectsDifferentSession(t *testing.T) {
	t.Parallel()
	snapshot := handshakeSnapshot(domain.HarnessCodex, "p1-1", "gen-1", testNow)
	probe := NewFakeProbe().Script(snapshot)
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	resolver.RegisterProbe(domain.HarnessCodex, probe)

	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "p1-2", Generation: "gen-1",
	}, domain.CapabilityFeatureSteer)

	if decision.Allowed {
		t.Fatal("session A's evidence must never authorize session B")
	}
	if decision.State != domain.CapabilityStateUnknown {
		t.Fatalf("State = %q, want unknown", decision.State)
	}
	if decision.ReasonCode == "" {
		t.Fatal("a cross-session refusal must explain itself")
	}
}

// TestGateRejectsRotatedGeneration is the generation-fencing test: a controller
// restart rotates the generation, so an earlier steering observation is void.
func TestGateRejectsRotatedGeneration(t *testing.T) {
	t.Parallel()
	snapshot := handshakeSnapshot(domain.HarnessCodex, "p1-1", "gen-1", testNow)
	probe := NewFakeProbe().Script(snapshot)
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	resolver.RegisterProbe(domain.HarnessCodex, probe)

	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "p1-1", Generation: "gen-2",
	}, domain.CapabilityFeatureSteer)

	if decision.Allowed {
		t.Fatal("evidence from a superseded controller generation must not authorize the new one")
	}
	if decision.State != domain.CapabilityStateUnknown {
		t.Fatalf("State = %q, want unknown", decision.State)
	}
	if decision.ReasonCode == "" {
		t.Fatal("a generation refusal must explain itself")
	}
}

// TestGateRequiresGenerationForSessionRequest refuses a session-scoped question with
// no generation, because there is nothing to fence against.
func TestGateRequiresGenerationForSessionRequest(t *testing.T) {
	t.Parallel()
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "p1-1",
	}, domain.CapabilityFeatureSteer)

	if decision.Allowed {
		t.Fatal("a session request without a generation must not be authorized")
	}
	if decision.ReasonCode != ports.CapabilityReasonNoSession {
		t.Fatalf("reason = %q, want %q", decision.ReasonCode, ports.CapabilityReasonNoSession)
	}
}

// TestGateHarnessScopedEvidenceCannotAuthorizeSession is the complementary fence:
// harness-level knowledge answers harness-level questions only.
func TestGateHarnessScopedEvidenceCannotAuthorizeSession(t *testing.T) {
	t.Parallel()
	snapshot := handshakeSnapshot(domain.HarnessCodex, "", "", testNow)
	probe := NewFakeProbe().Script(snapshot)
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	resolver.RegisterProbe(domain.HarnessCodex, probe)

	sessionDecision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "p1-1", Generation: "gen-1",
	}, domain.CapabilityFeatureSteer)
	if sessionDecision.Allowed {
		t.Fatal("harness-scoped evidence must not authorize a per-session feature")
	}

	harnessDecision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex,
	}, domain.CapabilityFeatureStreaming)
	if !harnessDecision.Allowed {
		t.Fatalf("harness-scoped handshake should authorize a harness-level feature (reason=%q)",
			harnessDecision.ReasonCode)
	}
}

// TestConfiguredIsNotAuthorized is the auth-discipline test the M2 design calls out
// explicitly: a local credential that was never validated must not read as ready.
func TestConfiguredIsNotAuthorized(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		auth   domain.AgentAuthenticationState
		want   domain.CapabilityReadiness
		reason string
	}{
		{"authorized", domain.AgentAuthenticationAuthorized, domain.CapabilityReady, ""},
		{"configured only", domain.AgentAuthenticationConfigured, domain.CapabilityBlockedAuth, "configured must not be ready"},
		{"unauthorized", domain.AgentAuthenticationUnauthorized, domain.CapabilityBlockedAuth, ""},
		{"unknown", domain.AgentAuthenticationUnknown, domain.CapabilityUnknown, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DeriveReadiness(domain.AgentInstallationInstalled, tt.auth)
			if got != tt.want {
				t.Fatalf("DeriveReadiness(installed, %q) = %q, want %q", tt.auth, got, tt.want)
			}
			if tt.auth == domain.AgentAuthenticationConfigured && got == domain.CapabilityReady {
				t.Fatal("a credential present on disk was reported ready without validation")
			}
		})
	}
}

// TestConfiguredSnapshotCannotGateSteer ties the auth rule to the feature gate: even
// with a handshake that reports steer supported, a merely-configured credential
// leaves readiness blocked_auth and AO must not treat the session as usable.
func TestConfiguredSnapshotCannotGateSteer(t *testing.T) {
	t.Parallel()
	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	snapshot := handshakeSnapshot(domain.HarnessClaudeCode, sessionID, generation, testNow)
	snapshot.AuthState = domain.AgentAuthenticationConfigured
	snapshot.Readiness = DeriveReadiness(domain.AgentInstallationInstalled, domain.AgentAuthenticationConfigured)
	if snapshot.Readiness != domain.CapabilityBlockedAuth {
		t.Fatalf("fixture readiness = %q, want blocked_auth", snapshot.Readiness)
	}

	probe := NewFakeProbe().Script(snapshot)
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	resolver.RegisterProbe(domain.HarnessClaudeCode, probe)

	resolved, err := resolver.Resolve(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessClaudeCode, SessionID: sessionID, Generation: generation,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Readiness == domain.CapabilityReady {
		t.Fatal("a configured-but-unverified credential must never resolve as ready")
	}
	if resolved.AuthState != domain.AgentAuthenticationConfigured {
		t.Fatalf("AuthState = %q, want configured preserved", resolved.AuthState)
	}
}

func TestDeriveReadinessBlockedBinary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		installation domain.AgentInstallationState
		auth         domain.AgentAuthenticationState
		want         domain.CapabilityReadiness
	}{
		{"not installed", domain.AgentInstallationNotInstalled, domain.AgentAuthenticationAuthorized, domain.CapabilityBlockedBinary},
		{"installation unknown", domain.AgentInstallationUnknown, domain.AgentAuthenticationAuthorized, domain.CapabilityUnknown},
		{"not applicable auth", domain.AgentInstallationInstalled, domain.AgentAuthenticationNotApplicable, domain.CapabilityReady},
		{"bogus installation", domain.AgentInstallationState("halfway"), domain.AgentAuthenticationAuthorized, domain.CapabilityUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := DeriveReadiness(tt.installation, tt.auth); got != tt.want {
				t.Fatalf("DeriveReadiness(%q, %q) = %q, want %q", tt.installation, tt.auth, got, tt.want)
			}
		})
	}
}

// TestResolveFallsBackToRecordedEvidence proves the resolver prefers recorded
// evidence over the static catalog and records what a probe learned.
func TestResolveFallsBackToRecordedEvidence(t *testing.T) {
	t.Parallel()
	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	recorder := NewMemoryRecorder()
	recorded := handshakeSnapshot(domain.HarnessOMP, sessionID, generation, testNow)
	if err := recorder.RecordCapabilities(context.Background(), recorded); err != nil {
		t.Fatalf("RecordCapabilities() error = %v", err)
	}

	resolver := NewResolver(WithClock(fixedClock(testNow)), WithRecorder(recorder))
	snapshot, err := resolver.Resolve(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessOMP, SessionID: sessionID, Generation: generation,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if snapshot.Source != domain.CapabilitySourceProviderHandshake {
		t.Fatalf("Source = %q, want the recorded handshake", snapshot.Source)
	}
	if !snapshot.Steer.Supported() {
		t.Fatalf("Steer = %q, want the recorded observation", snapshot.Steer)
	}
}

// TestResolveDoesNotReuseAnotherSessionsRecordedEvidence is the recorded-evidence
// half of cross-session fencing.
func TestResolveDoesNotReuseAnotherSessionsRecordedEvidence(t *testing.T) {
	t.Parallel()
	recorder := NewMemoryRecorder()
	recorded := handshakeSnapshot(domain.HarnessOMP, "p1-1", "gen-1", testNow)
	if err := recorder.RecordCapabilities(context.Background(), recorded); err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(WithClock(fixedClock(testNow)), WithRecorder(recorder))
	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessOMP, SessionID: "p1-9", Generation: "gen-1",
	}, domain.CapabilityFeatureSteer)

	if decision.Allowed {
		t.Fatal("recorded evidence for session p1-1 must not authorize p1-9")
	}
}

// TestResolveRecordsProbeOutput proves the resolver persists what it observed, so a
// restart can answer without probing.
func TestResolveRecordsProbeOutput(t *testing.T) {
	t.Parallel()
	recorder := NewMemoryRecorder()
	probe := NewFakeProbe().Script(handshakeSnapshot(domain.HarnessCodex, "p1-1", "gen-1", testNow))
	resolver := NewResolver(WithClock(fixedClock(testNow)), WithRecorder(recorder))
	resolver.RegisterProbe(domain.HarnessCodex, probe)

	if _, err := resolver.Resolve(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: "p1-1", Generation: "gen-1",
	}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	stored, found, err := recorder.LoadSessionCapabilities(context.Background(), "p1-1")
	if err != nil || !found {
		t.Fatalf("probe output was not recorded: found=%v err=%v", found, err)
	}
	if stored.Generation != "gen-1" {
		t.Fatalf("recorded generation = %q, want gen-1", stored.Generation)
	}
}

// TestResolveUnknownHarnessIsRejected refuses a harness AO has no profile for,
// rather than returning an empty-but-plausible matrix.
func TestResolveUnknownHarnessIsRejected(t *testing.T) {
	t.Parallel()
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	_, err := resolver.Resolve(context.Background(), ports.CapabilityProbeRequest{
		Harness: "gpt-imaginary",
	})
	if !errors.Is(err, domain.ErrInvalidCapabilityHarness) {
		t.Fatalf("Resolve() error = %v, want ErrInvalidCapabilityHarness", err)
	}
}

// TestResolveHonoursContextCancellation proves the probe boundary is respected.
func TestResolveHonoursContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolver := NewResolver(WithClock(fixedClock(testNow)))
	snapshot, err := resolver.Resolve(ctx, ports.CapabilityProbeRequest{Harness: domain.HarnessCodex})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve() error = %v, want context.Canceled", err)
	}
	if snapshot.Readiness != domain.CapabilityUnknown {
		t.Fatalf("cancelled resolve Readiness = %q, want unknown", snapshot.Readiness)
	}
}

// TestFeatureReasonsAreStable pins the reason vocabulary a client may branch on.
func TestFeatureReasonsAreStable(t *testing.T) {
	t.Parallel()
	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	tests := []struct {
		name    string
		mutate  func(*domain.HarnessCapabilitySnapshot)
		feature domain.CapabilityFeature
		want    string
		allowed bool
	}{
		{"supported", func(s *domain.HarnessCapabilitySnapshot) {
			s.Steer = domain.CapabilitySupported
		}, domain.CapabilityFeatureSteer, ports.CapabilityReasonSupported, true},
		{"unsupported", func(s *domain.HarnessCapabilitySnapshot) {
			s.Steer = domain.CapabilityUnsupported
		}, domain.CapabilityFeatureSteer, ports.CapabilityReasonUnsupported, false},
		{"not observed", func(s *domain.HarnessCapabilitySnapshot) {
			s.Steer = domain.CapabilityStateUnknown
		}, domain.CapabilityFeatureSteer, ports.CapabilityReasonNotObserved, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
			tt.mutate(&snapshot)

			resolver := NewResolver(WithClock(fixedClock(testNow)))
			resolver.RegisterProbe(domain.HarnessCodex, NewFakeProbe().Script(snapshot))

			decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			}, tt.feature)
			if decision.Allowed != tt.allowed {
				t.Fatalf("Allowed = %v, want %v", decision.Allowed, tt.allowed)
			}
			if decision.ReasonCode != tt.want {
				t.Fatalf("ReasonCode = %q, want %q", decision.ReasonCode, tt.want)
			}
		})
	}
}

// TestGateRejectsUnknownFeature keeps a typo from authorizing anything.
func TestGateRejectsUnknownFeature(t *testing.T) {
	t.Parallel()
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	resolver.RegisterProbe(domain.HarnessCodex,
		NewFakeProbe().Script(handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)))

	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
	}, domain.CapabilityFeature("telepathy"))
	if decision.Allowed {
		t.Fatal("an unknown feature name must never be authorized")
	}
}

// TestDescribeNeverLeaksIdentity proves the log renderer omits executable paths and
// versions, which can carry a user's home directory.
func TestDescribeNeverLeaksIdentity(t *testing.T) {
	t.Parallel()
	snapshot := handshakeSnapshot(domain.HarnessCodex, "p1-1", "gen-1", testNow)
	snapshot.Executable = `C:\Users\someone\.local\bin\codex.exe`
	snapshot.Version = "0.154.0"

	got := Describe(snapshot)
	for _, secret := range []string{"someone", ".local", "codex.exe", "0.154.0"} {
		if strings.Contains(got, secret) {
			t.Fatalf("Describe() leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "harness=codex") {
		t.Fatalf("Describe() dropped the harness: %s", got)
	}
}

// TestResolvePreservesProbeSuppliedReason proves a probe's own explanation is not
// erased by the resolver: "the handshake did not advertise steering" is more useful
// to a client than a generic code.
func TestResolvePreservesProbeSuppliedReason(t *testing.T) {
	t.Parallel()
	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	snapshot := handshakeSnapshot(domain.HarnessOMP, sessionID, generation, testNow)
	snapshot.Steer = domain.CapabilityStateUnknown
	snapshot.ReasonCode = ports.CapabilityReasonNotAdvertised

	resolver := NewResolver(WithClock(fixedClock(testNow)))
	resolver.RegisterProbe(domain.HarnessOMP, NewFakeProbe().Script(snapshot))

	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessOMP, SessionID: sessionID, Generation: generation,
	}, domain.CapabilityFeatureSteer)
	if decision.Allowed {
		t.Fatal("an unadvertised steer must not be authorized")
	}
	if decision.ReasonCode != ports.CapabilityReasonNotAdvertised {
		t.Fatalf("ReasonCode = %q, want the probe's own %q",
			decision.ReasonCode, ports.CapabilityReasonNotAdvertised)
	}
}

// TestGateRefusesUnprovenEvidence walks every refusal Gate can return, one row per
// path, because each one is a separate claim about what evidence may authorize work.
//
// The order of Gate's checks is the security contract, not an implementation detail:
// a row that expects `session_mismatch` would pass for the wrong reason if the
// session check actually landed after the evidence-grade one, so the exact
// ReasonCode is asserted rather than merely "not allowed".
func TestGateRefusesUnprovenEvidence(t *testing.T) {
	t.Parallel()

	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	feature := domain.CapabilityFeatureSteer

	tests := []struct {
		name string
		// snapshot is what the probe answers with. nil means the probe reports a
		// failure instead.
		snapshot func() domain.HarnessCapabilitySnapshot
		probeErr error
		request  ports.CapabilityProbeRequest
		want     string
		allowed  bool
	}{
		{
			name: "harness mismatch",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				// A probe registered for codex answering about a different harness.
				return handshakeSnapshot(domain.HarnessOMP, sessionID, generation, testNow)
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonHandshakeIncomplete,
		},
		{
			name: "session mismatch",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				return handshakeSnapshot(domain.HarnessCodex, "another-session", generation, testNow)
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonSessionMismatch,
		},
		{
			name: "generation mismatch",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				return handshakeSnapshot(domain.HarnessCodex, sessionID, "gen-0", testNow)
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonGenerationMismatch,
		},
		{
			name: "harness-scoped evidence cannot answer a session question",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
				snapshot.ObservedSessionID = ""
				snapshot.Generation = ""
				return snapshot
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonSessionMismatch,
		},
		{
			name: "static evidence",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
				snapshot.Source = domain.CapabilitySourceStatic
				return snapshot
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonStaticOnly,
		},
		{
			name: "local probe evidence",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
				snapshot.Source = domain.CapabilitySourceLocalProbe
				return snapshot
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonStaticOnly,
		},
		{
			name: "expired evidence",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				// Observed and expired before the resolver's clock reads it.
				return handshakeSnapshot(domain.HarnessCodex, sessionID, generation,
					testNow.Add(-2*DefaultTTL))
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonSnapshotExpired,
		},
		{
			name: "credential rejected",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
				snapshot.AuthState = domain.AgentAuthenticationUnauthorized
				return snapshot
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonAuthRejected,
		},
		{
			name: "credential present but never validated",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
				snapshot.AuthState = domain.AgentAuthenticationConfigured
				return snapshot
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonAuthUnverified,
		},
		{
			name: "no credential at all",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
				snapshot.AuthState = domain.AgentAuthenticationUnknown
				return snapshot
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonAuthMissing,
		},
		{
			name: "readiness blocked on auth",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
				snapshot.Readiness = domain.CapabilityBlockedAuth
				return snapshot
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonAuthMissing,
		},
		{
			name: "readiness blocked on the binary",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
				snapshot.Readiness = domain.CapabilityBlockedBinary
				return snapshot
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonBinaryMissing,
		},
		{
			name: "readiness unknown",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
				snapshot.Readiness = domain.CapabilityUnknown
				return snapshot
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonProbeFailed,
		},
		{
			name: "session request without a generation",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				return handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID,
			},
			want: ports.CapabilityReasonNoSession,
		},
		{
			name:     "failed probe",
			probeErr: errors.New("handshake refused"),
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want: ports.CapabilityReasonProbeFailed,
		},
		{
			// The positive row. Without it every refusal above could pass on a Gate
			// that simply refuses everything.
			name: "fully proven evidence",
			snapshot: func() domain.HarnessCapabilitySnapshot {
				return handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
			},
			request: ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			},
			want:    ports.CapabilityReasonSupported,
			allowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolver := NewResolver(WithClock(fixedClock(testNow)))
			returned := domain.HarnessCapabilitySnapshot{}
			if tt.snapshot != nil {
				returned = tt.snapshot()
			}
			resolver.RegisterProbeFunc(domain.HarnessCodex,
				func(context.Context, ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
					if tt.probeErr != nil {
						return domain.HarnessCapabilitySnapshot{}, tt.probeErr
					}
					return returned, nil
				})

			decision := resolver.Gate(context.Background(), tt.request, feature)
			if decision.Allowed != tt.allowed {
				t.Fatalf("Allowed = %v, want %v (state=%q reason=%q)",
					decision.Allowed, tt.allowed, decision.State, decision.ReasonCode)
			}
			if decision.ReasonCode != tt.want {
				t.Fatalf("ReasonCode = %q, want %q", decision.ReasonCode, tt.want)
			}
			if !tt.allowed && decision.State != domain.CapabilityStateUnknown {
				t.Fatalf("a refused gate must report unknown, got %q", decision.State)
			}
		})
	}
}

// TestGateRequiresAuthorizedAndReadySnapshot separates the two preconditions that a
// handshake alone cannot establish. A provider that answers a protocol handshake has
// proven it speaks the protocol, not that this install is usable: an unverified
// credential, or a harness that cannot start, must each refuse on their own.
func TestGateRequiresAuthorizedAndReadySnapshot(t *testing.T) {
	t.Parallel()

	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	request := ports.CapabilityProbeRequest{
		Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
	}
	tests := []struct {
		name      string
		mutate    func(*domain.HarnessCapabilitySnapshot)
		want      string
		wantState domain.CapabilityState
	}{
		{
			name: "configured credential",
			mutate: func(s *domain.HarnessCapabilitySnapshot) {
				s.AuthState = domain.AgentAuthenticationConfigured
				s.Readiness = domain.CapabilityReady
			},
			want: ports.CapabilityReasonAuthUnverified,
		},
		{
			name: "ready claim contradicted by blocked auth readiness",
			mutate: func(s *domain.HarnessCapabilitySnapshot) {
				s.AuthState = domain.AgentAuthenticationAuthorized
				s.Readiness = domain.CapabilityBlockedAuth
			},
			want: ports.CapabilityReasonAuthMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
			tt.mutate(&snapshot)
			resolver := NewResolver(WithClock(fixedClock(testNow)))
			resolver.RegisterProbe(domain.HarnessCodex, NewFakeProbe().Script(snapshot))

			decision := resolver.Gate(context.Background(), request, domain.CapabilityFeatureSteer)
			if decision.Allowed {
				t.Fatalf("Allowed = true for %s; the handshake proved the protocol, not usability",
					tt.name)
			}
			if decision.ReasonCode != tt.want {
				t.Fatalf("ReasonCode = %q, want %q", decision.ReasonCode, tt.want)
			}
			if decision.State != domain.CapabilityStateUnknown {
				t.Fatalf("State = %q, want unknown", decision.State)
			}
		})
	}
}

// TestGateRefusesAdvertisedOnlyApproval is the approval-specific half of the same
// rule: a provider that merely accepts an approval flag has not proven it enforces
// one, so the feature must not read as supported.
//
// The expected state is unknown, not unsupported, and that distinction is the point.
// Nothing observed says the provider cannot enforce a policy — only that nobody
// proved it does. Reporting unsupported would hide a control that may work, which is
// a different (and unfounded) claim.
func TestGateRefusesAdvertisedOnlyApproval(t *testing.T) {
	t.Parallel()

	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	snapshot := handshakeSnapshot(domain.HarnessClaudeCode, sessionID, generation, testNow)
	snapshot.Approval = domain.CapabilityApprovalAdvertisedOnly
	snapshot.ApplyAt = domain.CapabilityApplyAtLaunch

	resolver := NewResolver(WithClock(fixedClock(testNow)))
	resolver.RegisterProbe(domain.HarnessClaudeCode, NewFakeProbe().Script(snapshot))

	decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
		Harness: domain.HarnessClaudeCode, SessionID: sessionID, Generation: generation,
	}, domain.CapabilityFeatureApproval)

	if decision.Allowed {
		t.Fatal("advertised_only authorized the approval gate")
	}
	// featureReason preserves the snapshot's own reason when it has one; this
	// snapshot carries none, so the state-derived code is what surfaces.
	if decision.ReasonCode != ports.CapabilityReasonNotObserved {
		t.Fatalf("ReasonCode = %q, want %q", decision.ReasonCode, ports.CapabilityReasonNotObserved)
	}
	if decision.State != domain.CapabilityStateUnknown {
		t.Fatalf("State = %q, want unknown: nothing proved the provider cannot enforce a policy",
			decision.State)
	}
}
