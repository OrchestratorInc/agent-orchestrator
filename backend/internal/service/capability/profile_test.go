package capability

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestStaticProfilesAreValidAndDisplayOnly proves the catalog is well formed and,
// critically, that every entry carries the static grade so no caller can mistake it
// for evidence.
func TestStaticProfilesAreValidAndDisplayOnly(t *testing.T) {
	t.Parallel()
	harnesses := SupportedHarnesses()
	if len(harnesses) != 4 {
		t.Fatalf("static profiles = %d, want the four M2 launch harnesses", len(harnesses))
	}
	want := map[domain.AgentHarness]bool{
		domain.HarnessCodex: true, domain.HarnessClaudeCode: true,
		domain.HarnessOMP: true, domain.HarnessDeepSeek: true,
	}
	for _, harness := range harnesses {
		if !want[harness] {
			t.Fatalf("unexpected harness %q in the static catalog", harness)
		}
		snapshot, ok := StaticProfileAt(harness, testNow, DefaultTTL)
		if !ok {
			t.Fatalf("StaticProfileAt(%q) reported no profile", harness)
		}
		if err := snapshot.Validate(); err != nil {
			t.Fatalf("%s static profile is invalid: %v", harness, err)
		}
		if snapshot.Source != domain.CapabilitySourceStatic {
			t.Fatalf("%s static profile source = %q, want static", harness, snapshot.Source)
		}
		if snapshot.Source.AllowsSessionGating() {
			t.Fatalf("%s static profile claims gating authority", harness)
		}
		if snapshot.Readiness != domain.CapabilityUnknown {
			t.Fatalf("%s static profile claims readiness %q; source reading cannot prove readiness",
				harness, snapshot.Readiness)
		}
		if snapshot.AuthState != domain.AgentAuthenticationUnknown {
			t.Fatalf("%s static profile claims auth %q; it never observed a credential",
				harness, snapshot.AuthState)
		}
		// The profile must never advertise a session id it did not observe.
		if snapshot.SessionScoped() {
			t.Fatalf("%s static profile claims a session it never saw", harness)
		}
	}
}

// TestStaticProfilesNeverGate is the anti-hardcoding proof required by M2-02: a
// harness whose static profile says "supported" must still be refused, so no caller
// can read a capability out of a harness constant.
func TestStaticProfilesNeverGate(t *testing.T) {
	t.Parallel()
	resolver := NewResolver(WithClock(fixedClock(testNow)))
	features := []domain.CapabilityFeature{
		domain.CapabilityFeatureStreaming, domain.CapabilityFeatureTools,
		domain.CapabilityFeatureInterrupt, domain.CapabilityFeatureSteer,
		domain.CapabilityFeatureQueue, domain.CapabilityFeatureResume,
		domain.CapabilityFeatureApproval, domain.CapabilityFeatureUsage,
	}
	for _, harness := range SupportedHarnesses() {
		profile, _ := StaticProfileAt(harness, testNow, DefaultTTL)
		claimed := 0
		for _, feature := range features {
			if profile.FeatureState(feature).Supported() {
				claimed++
			}
		}
		if claimed == 0 {
			t.Fatalf("%s static profile claims nothing; the anti-hardcoding test would be vacuous", harness)
		}
		for _, feature := range features {
			if decision := resolver.Gate(context.Background(),
				ports.CapabilityProbeRequest{Harness: harness}, feature); decision.Allowed {
				t.Fatalf("%s/%s was authorized by a harness constant; %d static claims exist for this harness",
					harness, feature, claimed)
			}
		}
	}
}

// TestUnknownObservationsAreNotReportedSupported proves the "unknown is not
// supported" rule end to end through the resolver, for a harness whose static
// profile happens to claim a feature.
func TestUnknownObservationsAreNotReportedSupported(t *testing.T) {
	t.Parallel()
	sessionID, generation := domain.SessionID("p1-1"), "gen-1"
	snapshot := handshakeSnapshot(domain.HarnessCodex, sessionID, generation, testNow)
	// Codex's static profile claims steer supported; this handshake did not observe it.
	snapshot.Steer = domain.CapabilityStateUnknown
	snapshot.Tools = domain.CapabilityStateUnknown

	resolver := NewResolver(WithClock(fixedClock(testNow)))
	resolver.RegisterProbe(domain.HarnessCodex, NewFakeProbe().Script(snapshot))

	for _, feature := range []domain.CapabilityFeature{
		domain.CapabilityFeatureSteer, domain.CapabilityFeatureTools,
	} {
		decision := resolver.Gate(context.Background(), ports.CapabilityProbeRequest{
			Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
		}, feature)
		if decision.Allowed {
			t.Fatalf("%s reported supported from an unknown observation", feature)
		}
		if decision.State != domain.CapabilityStateUnknown {
			t.Fatalf("%s state = %q, want unknown", feature, decision.State)
		}
	}
}

// TestStaticProfileAbsentHarness proves an unprofiled harness is distinguishable
// from an unprofiled capability: ok=false, not an empty snapshot.
func TestStaticProfileAbsentHarness(t *testing.T) {
	t.Parallel()
	for _, harness := range []domain.AgentHarness{"gpt-imaginary", domain.HarnessAider} {
		if _, ok := StaticProfileAt(harness, testNow, DefaultTTL); ok {
			t.Fatalf("%q must have no static profile", harness)
		}
	}
}

// TestStaticProfileDeterministic proves the injected clock fully controls the
// timestamps, so tests never depend on wall time.
func TestStaticProfileDeterministic(t *testing.T) {
	t.Parallel()
	first, _ := StaticProfileAt(domain.HarnessCodex, testNow, time.Minute)
	second, _ := StaticProfileAt(domain.HarnessCodex, testNow, time.Minute)
	if !first.ObservedAt.Equal(second.ObservedAt) || !first.ExpiresAt.Equal(second.ExpiresAt) {
		t.Fatal("StaticProfileAt must be deterministic for a fixed clock")
	}
	if first.ObservedAt != testNow {
		t.Fatalf("ObservedAt = %v, want %v", first.ObservedAt, testNow)
	}
	if !first.ExpiresAt.Equal(testNow.Add(time.Minute)) {
		t.Fatalf("ExpiresAt = %v, want %v", first.ExpiresAt, testNow.Add(time.Minute))
	}
}

// TestStaticProfileSteerClaimsMatchSourceReading documents exactly which cells the
// catalog asserts, so a future edit that silently widens a claim fails here.
func TestStaticProfileSteerClaimsMatchSourceReading(t *testing.T) {
	t.Parallel()
	tests := []struct {
		harness domain.AgentHarness
		steer   domain.CapabilityState
		why     string
	}{
		// Codex advertises steer unconditionally from its driver (driver.go:160),
		// so a source read can say supported. Gate still refuses it.
		{domain.HarnessCodex, domain.CapabilitySupported, "native turn/steer with expectedTurnId"},
		// ACP steering depends on the initialize handshake advertising the
		// steering extension (acp/driver.go:944); no source read can decide it.
		{domain.HarnessClaudeCode, domain.CapabilityStateUnknown, "ACP extension gated at handshake"},
		{domain.HarnessOMP, domain.CapabilityStateUnknown, "ACP extension gated at handshake"},
		{domain.HarnessDeepSeek, domain.CapabilityStateUnknown, "ACP extension gated at handshake"},
	}
	for _, tt := range tests {
		t.Run(string(tt.harness), func(t *testing.T) {
			t.Parallel()
			profile, ok := StaticProfileAt(tt.harness, testNow, DefaultTTL)
			if !ok {
				t.Fatalf("%s has no profile", tt.harness)
			}
			if profile.Steer != tt.steer {
				t.Fatalf("%s steer = %q, want %q (%s)", tt.harness, profile.Steer, tt.steer, tt.why)
			}
		})
	}
}

// TestStaticProfileResumeClaimsMatchSourceReading pins the resume column. Where a
// provider picks session/load versus session/resume at handshake time, resume stays
// unknown rather than assuming the richer guarantee.
func TestStaticProfileResumeClaimsMatchSourceReading(t *testing.T) {
	t.Parallel()
	tests := []struct {
		harness domain.AgentHarness
		resume  domain.CapabilityResume
	}{
		{domain.HarnessCodex, domain.CapabilityResumeWithHistory},
		{domain.HarnessClaudeCode, domain.CapabilityResumeUnknown},
		{domain.HarnessOMP, domain.CapabilityResumeUnknown},
		{domain.HarnessDeepSeek, domain.CapabilityResumeUnknown},
	}
	for _, tt := range tests {
		t.Run(string(tt.harness), func(t *testing.T) {
			t.Parallel()
			profile, _ := StaticProfileAt(tt.harness, testNow, DefaultTTL)
			if profile.Resume != tt.resume {
				t.Fatalf("%s resume = %q, want %q", tt.harness, profile.Resume, tt.resume)
			}
		})
	}
}

// TestStaticProfileApprovalApplyAtMatchSourceReading pins the approval column,
// including the distinction that matters to a user: OMP's mode is fixed at launch
// and cannot be changed live.
func TestStaticProfileApprovalApplyAtMatchSourceReading(t *testing.T) {
	t.Parallel()
	tests := []struct {
		harness  domain.AgentHarness
		approval domain.CapabilityApproval
		applyAt  domain.CapabilityApplyAt
	}{
		{domain.HarnessCodex, domain.CapabilityApprovalEnforced, domain.CapabilityApplyAtTurn},
		{domain.HarnessClaudeCode, domain.CapabilityApprovalAdvertisedOnly, domain.CapabilityApplyAtLaunch},
		{domain.HarnessOMP, domain.CapabilityApprovalEnforced, domain.CapabilityApplyAtLaunch},
		{domain.HarnessDeepSeek, domain.CapabilityApprovalEnforced, domain.CapabilityApplyAtRequest},
	}
	for _, tt := range tests {
		t.Run(string(tt.harness), func(t *testing.T) {
			t.Parallel()
			profile, _ := StaticProfileAt(tt.harness, testNow, DefaultTTL)
			if profile.Approval != tt.approval {
				t.Fatalf("%s approval = %q, want %q", tt.harness, profile.Approval, tt.approval)
			}
			if profile.ApplyAt != tt.applyAt {
				t.Fatalf("%s applyAt = %q, want %q", tt.harness, profile.ApplyAt, tt.applyAt)
			}
		})
	}
}

// TestStaticProfileUsageClaimsMatchSourceReading pins the usage column: only where a
// provider's accounting is unconditional may a source read say exact.
func TestStaticProfileUsageClaimsMatchSourceReading(t *testing.T) {
	t.Parallel()
	tests := []struct {
		harness domain.AgentHarness
		usage   domain.CapabilityUsage
	}{
		{domain.HarnessCodex, domain.CapabilityUsageExact},
		{domain.HarnessClaudeCode, domain.CapabilityUsageExact},
		// OMP declares usage, but a given turn may not fill it.
		{domain.HarnessOMP, domain.CapabilityUsageUnknown},
		// DeepSeekRuntime declares no usage capability at all.
		{domain.HarnessDeepSeek, domain.CapabilityUsageUnknown},
	}
	for _, tt := range tests {
		t.Run(string(tt.harness), func(t *testing.T) {
			t.Parallel()
			profile, _ := StaticProfileAt(tt.harness, testNow, DefaultTTL)
			if profile.Usage != tt.usage {
				t.Fatalf("%s usage = %q, want %q", tt.harness, profile.Usage, tt.usage)
			}
		})
	}
}
