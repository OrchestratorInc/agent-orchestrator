package domain

import (
	"errors"
	"testing"
	"time"
)

func TestCapabilitySourceGating(t *testing.T) {
	t.Parallel()
	tests := []struct {
		source CapabilitySource
		gates  bool
	}{
		{CapabilitySourceStatic, false},
		{CapabilitySourceLocalProbe, false},
		{CapabilitySourceProviderHandshake, true},
		{CapabilitySourceConfirmedRuntime, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			t.Parallel()
			if !tt.source.Valid() {
				t.Fatalf("%q must be a valid source", tt.source)
			}
			if got := tt.source.AllowsSessionGating(); got != tt.gates {
				t.Fatalf("%q AllowsSessionGating() = %v, want %v", tt.source, got, tt.gates)
			}
		})
	}
	if CapabilitySource("guess").Valid() {
		t.Fatal("an unknown source must not be valid")
	}
}

// TestCapabilitySourceLocalProbeNeverGates pins the design rule that proving a
// binary exists says nothing about whether a provider will accept work.
func TestCapabilitySourceLocalProbeNeverGates(t *testing.T) {
	t.Parallel()
	if CapabilitySourceLocalProbe.AllowsSessionGating() {
		t.Fatal("local_probe must never authorize a session feature")
	}
	if CapabilitySourceStatic.AllowsSessionGating() {
		t.Fatal("static evidence must never authorize a session feature")
	}
}

func TestCapabilityStateSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state     CapabilityState
		supported bool
		unsupport bool
	}{
		{CapabilitySupported, true, false},
		{CapabilityUnsupported, false, true},
		{CapabilityStateUnknown, false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			t.Parallel()
			if !tt.state.Valid() {
				t.Fatalf("%q must be valid", tt.state)
			}
			if got := tt.state.Supported(); got != tt.supported {
				t.Fatalf("%q Supported() = %v, want %v", tt.state, got, tt.supported)
			}
			if got := tt.state.DeterminatelyUnsupported(); got != tt.unsupport {
				t.Fatalf("%q DeterminatelyUnsupported() = %v, want %v", tt.state, got, tt.unsupport)
			}
		})
	}
}

// TestCapabilityStateUnknownIsNeverSupported is the single most important
// invariant in this file: not having observed a feature must never read as either
// "supported" or "definitely unsupported".
func TestCapabilityStateUnknownIsNeverSupported(t *testing.T) {
	t.Parallel()
	if CapabilityStateUnknown.Supported() {
		t.Fatal("unknown must never report supported")
	}
	if CapabilityStateUnknown.DeterminatelyUnsupported() {
		t.Fatal("unknown must never report determinately unsupported; a client would hide a working control")
	}
}

func TestCapabilityResumeCollapse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		resume CapabilityResume
		want   CapabilityState
	}{
		{CapabilityResumeWithHistory, CapabilitySupported},
		{CapabilityResumeNoHistory, CapabilitySupported},
		{CapabilityResumeNone, CapabilityUnsupported},
		{CapabilityResumeUnknown, CapabilityStateUnknown},
	}
	for _, tt := range tests {
		t.Run(string(tt.resume), func(t *testing.T) {
			t.Parallel()
			if !tt.resume.Valid() {
				t.Fatalf("%q must be valid", tt.resume)
			}
			if got := tt.resume.State(); got != tt.want {
				t.Fatalf("%q State() = %q, want %q", tt.resume, got, tt.want)
			}
		})
	}
}

func TestCapabilityApprovalCollapse(t *testing.T) {
	t.Parallel()
	// Only enforced reads as supported. advertised_only is a provider accepting an
	// approval flag, which is not proof it acts on one: a CLI that takes the flag and
	// ignores it gives the user no gate, so reporting it as supported would let a
	// policy gate authorize work on the strength of a string. Unknown is the honest
	// answer — the provider may well enforce it, and nothing has shown that.
	tests := []struct {
		approval CapabilityApproval
		want     CapabilityState
	}{
		{CapabilityApprovalEnforced, CapabilitySupported},
		{CapabilityApprovalAdvertisedOnly, CapabilityStateUnknown},
		{CapabilityApprovalUnknown, CapabilityStateUnknown},
	}
	for _, tt := range tests {
		t.Run(string(tt.approval), func(t *testing.T) {
			t.Parallel()
			if !tt.approval.Valid() {
				t.Fatalf("%q must be valid", tt.approval)
			}
			if got := tt.approval.State(); got != tt.want {
				t.Fatalf("%q State() = %q, want %q", tt.approval, got, tt.want)
			}
		})
	}
}

func TestCapabilityUsageCollapse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		usage CapabilityUsage
		want  CapabilityState
	}{
		{CapabilityUsageExact, CapabilitySupported},
		{CapabilityUsageEstimated, CapabilitySupported},
		{CapabilityUsageUnknown, CapabilityStateUnknown},
	}
	for _, tt := range tests {
		t.Run(string(tt.usage), func(t *testing.T) {
			t.Parallel()
			if !tt.usage.Valid() {
				t.Fatalf("%q must be valid", tt.usage)
			}
			if got := tt.usage.State(); got != tt.want {
				t.Fatalf("%q State() = %q, want %q", tt.usage, got, tt.want)
			}
		})
	}
}

func TestNewHarnessCapabilitySnapshotDefaultsToUnknown(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	snapshot := NewHarnessCapabilitySnapshot(HarnessCodex, CapabilitySourceLocalProbe, observedAt, time.Minute)

	if err := snapshot.Validate(); err != nil {
		t.Fatalf("a freshly constructed snapshot must validate: %v", err)
	}
	for name, state := range map[string]CapabilityState{
		"streaming": snapshot.Streaming,
		"tools":     snapshot.Tools,
		"interrupt": snapshot.Interrupt,
		"steer":     snapshot.Steer,
		"queue":     snapshot.Queue,
	} {
		if state != CapabilityStateUnknown {
			t.Fatalf("%s = %q, want unknown before anything is observed", name, state)
		}
	}
	if snapshot.Resume != CapabilityResumeUnknown ||
		snapshot.Approval != CapabilityApprovalUnknown ||
		snapshot.ApplyAt != CapabilityApplyAtUnknown ||
		snapshot.Usage != CapabilityUsageUnknown {
		t.Fatalf("skeleton snapshot claims a capability it never observed: %+v", snapshot)
	}
	if snapshot.AuthState != AgentAuthenticationUnknown || snapshot.Readiness != CapabilityUnknown {
		t.Fatalf("skeleton snapshot must not claim readiness: auth=%q readiness=%q",
			snapshot.AuthState, snapshot.Readiness)
	}
	if !snapshot.ExpiresAt.Equal(observedAt.Add(time.Minute)) {
		t.Fatalf("ExpiresAt = %v, want %v", snapshot.ExpiresAt, observedAt.Add(time.Minute))
	}
}

// TestNewHarnessCapabilitySnapshotNonPositiveTTLExpiresImmediately covers the
// operator error of forgetting the TTL: the result must be unusable, not
// permanently valid.
func TestNewHarnessCapabilitySnapshotNonPositiveTTLExpiresImmediately(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	for _, ttl := range []time.Duration{0, -time.Second} {
		snapshot := NewHarnessCapabilitySnapshot(HarnessCodex, CapabilitySourceProviderHandshake, observedAt, ttl)
		if !snapshot.Expired(observedAt) {
			t.Fatalf("ttl %v must produce an already-expired snapshot", ttl)
		}
	}
}

func TestHarnessCapabilityExpiry(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	snapshot := NewHarnessCapabilitySnapshot(HarnessCodex, CapabilitySourceProviderHandshake, observedAt, time.Minute)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before expiry", observedAt.Add(59 * time.Second), false},
		{"at expiry", observedAt.Add(time.Minute), true},
		{"after expiry", observedAt.Add(2 * time.Minute), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := snapshot.Expired(tt.now); got != tt.want {
				t.Fatalf("Expired(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

// TestHarnessCapabilityZeroExpiryIsExpired rejects a snapshot with no stated
// lifetime: "never expires" is not an acceptable default for evidence.
func TestHarnessCapabilityZeroExpiryIsExpired(t *testing.T) {
	t.Parallel()
	var snapshot HarnessCapabilitySnapshot
	if !snapshot.Expired(time.Now()) {
		t.Fatal("a snapshot with no observed/expiry time must be treated as expired")
	}
}

func TestHarnessCapabilityAppliesTo(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	harnessScoped := NewHarnessCapabilitySnapshot(HarnessCodex, CapabilitySourceStatic, observedAt, time.Minute)
	sessionScoped := NewHarnessCapabilitySnapshot(HarnessCodex, CapabilitySourceProviderHandshake, observedAt, time.Minute)
	sessionScoped.ObservedSessionID = "p1-1"
	sessionScoped.Generation = "gen-1"

	tests := []struct {
		name       string
		snapshot   HarnessCapabilitySnapshot
		sessionID  SessionID
		generation string
		want       bool
	}{
		{"harness-scoped answers a harness question", harnessScoped, "", "", true},
		{"harness-scoped never claims a session", harnessScoped, "p1-1", "gen-1", false},
		{"session-scoped matches exactly", sessionScoped, "p1-1", "gen-1", true},
		{"session-scoped rejects another session", sessionScoped, "p1-2", "gen-1", false},
		{"session-scoped rejects an older generation", sessionScoped, "p1-1", "gen-0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.snapshot.AppliesTo(tt.sessionID, tt.generation); got != tt.want {
				t.Fatalf("AppliesTo(%q, %q) = %v, want %v", tt.sessionID, tt.generation, got, tt.want)
			}
		})
	}
}

func TestHarnessCapabilityFeatureState(t *testing.T) {
	t.Parallel()
	snapshot := NewHarnessCapabilitySnapshot(HarnessCodex, CapabilitySourceProviderHandshake,
		time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC), time.Minute)
	snapshot.Streaming = CapabilitySupported
	snapshot.Interrupt = CapabilitySupported
	snapshot.Steer = CapabilityStateUnknown
	snapshot.Resume = CapabilityResumeNoHistory
	// advertised_only, deliberately: a provider that merely accepts an approval flag
	// has not proven it enforces one, so the feature must read unknown rather than
	// supported. Only an enforced observation may authorize the approval gate.
	snapshot.Approval = CapabilityApprovalAdvertisedOnly
	snapshot.Usage = CapabilityUsageEstimated

	tests := []struct {
		feature CapabilityFeature
		want    CapabilityState
	}{
		{CapabilityFeatureStreaming, CapabilitySupported},
		{CapabilityFeatureInterrupt, CapabilitySupported},
		{CapabilityFeatureSteer, CapabilityStateUnknown},
		{CapabilityFeatureQueue, CapabilityStateUnknown},
		{CapabilityFeatureResume, CapabilitySupported},
		{CapabilityFeatureApproval, CapabilityStateUnknown},
		{CapabilityFeatureUsage, CapabilitySupported},
		{CapabilityFeature("telepathy"), CapabilityStateUnknown},
	}
	for _, tt := range tests {
		t.Run(string(tt.feature), func(t *testing.T) {
			t.Parallel()
			if got := snapshot.FeatureState(tt.feature); got != tt.want {
				t.Fatalf("FeatureState(%q) = %q, want %q", tt.feature, got, tt.want)
			}
		})
	}
}

func TestHarnessCapabilityValidate(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	valid := NewHarnessCapabilitySnapshot(HarnessCodex, CapabilitySourceProviderHandshake, observedAt, time.Minute)

	tests := []struct {
		name   string
		mutate func(*HarnessCapabilitySnapshot)
		want   error
	}{
		{"valid skeleton", func(*HarnessCapabilitySnapshot) {}, nil},
		{"fake harness is allowed for fixtures", func(s *HarnessCapabilitySnapshot) { s.Harness = HarnessFake }, nil},
		{"unknown harness", func(s *HarnessCapabilitySnapshot) { s.Harness = "gpt-imaginary" }, ErrInvalidCapabilityHarness},
		{"bad source", func(s *HarnessCapabilitySnapshot) { s.Source = "vibes" }, ErrInvalidCapabilitySource},
		{"bad readiness", func(s *HarnessCapabilitySnapshot) { s.Readiness = "probably" }, ErrInvalidCapabilityReadiness},
		{"bad surface", func(s *HarnessCapabilitySnapshot) { s.Surfaces = "telepathy" }, ErrInvalidCapabilitySurface},
		{"bad auth state", func(s *HarnessCapabilitySnapshot) { s.AuthState = "logged-in" }, ErrInvalidCapabilityAuthState},
		{"bad feature state", func(s *HarnessCapabilitySnapshot) { s.Steer = "maybe" }, ErrInvalidCapabilityState},
		{"bad resume", func(s *HarnessCapabilitySnapshot) { s.Resume = "sometimes" }, ErrInvalidCapabilityResume},
		{"bad approval", func(s *HarnessCapabilitySnapshot) { s.Approval = "trust-me" }, ErrInvalidCapabilityApproval},
		{"bad apply-at", func(s *HarnessCapabilitySnapshot) { s.ApplyAt = "whenever" }, ErrInvalidCapabilityApplyAt},
		{"bad usage", func(s *HarnessCapabilitySnapshot) { s.Usage = "roughly" }, ErrInvalidCapabilityUsage},
		{"missing observed-at", func(s *HarnessCapabilitySnapshot) { s.ObservedAt = time.Time{} }, ErrInvalidCapabilityObservedAt},
		{"expiry before observation", func(s *HarnessCapabilitySnapshot) {
			s.ExpiresAt = s.ObservedAt.Add(-time.Second)
		}, ErrInvalidCapabilityExpiry},
		{"session-scoped without generation", func(s *HarnessCapabilitySnapshot) {
			s.ObservedSessionID = "p1-1"
		}, ErrInvalidCapabilityGeneration},
		{"empty model", func(s *HarnessCapabilitySnapshot) {
			s.Models = []CapabilityModelChoice{{Model: ""}}
		}, ErrInvalidCapabilityModelChoice},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			candidate.Models = nil
			tt.mutate(&candidate)
			err := candidate.Validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCapabilityModelChoiceValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		choice CapabilityModelChoice
		want   bool
	}{
		{"model only", CapabilityModelChoice{Model: "gpt-6-astra"}, true},
		{"model with efforts", CapabilityModelChoice{Model: "gpt-6-astra", Effort: []string{"low", "high"}}, true},
		{"empty model", CapabilityModelChoice{}, false},
		{"blank model", CapabilityModelChoice{Model: "   "}, false},
		{"duplicate effort", CapabilityModelChoice{Model: "m", Effort: []string{"low", "low"}}, false},
		{"blank effort", CapabilityModelChoice{Model: "m", Effort: []string{""}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.choice.Validate()
			if (err == nil) != tt.want {
				t.Fatalf("Validate() = %v, want valid=%v", err, tt.want)
			}
		})
	}
}
