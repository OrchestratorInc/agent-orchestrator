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

// launchFixture is an admissible Chat observation for Codex, and each test below
// removes exactly the one fact it is about.
func launchFixture(now time.Time) HarnessCapabilitySnapshot {
	snapshot := NewHarnessCapabilitySnapshot(HarnessCodex, CapabilitySourceProviderHandshake, now, time.Minute)
	snapshot.AuthState = AgentAuthenticationAuthorized
	snapshot.Readiness = CapabilityReady
	snapshot.Surfaces = CapabilitySurfaceChat
	return snapshot
}

// TestAuthorizeLaunchChatNeedsAHandshake covers the Chat rule: a provider
// handshake that observed the chat surface, plus a usable credential.
func TestAuthorizeLaunchChatNeedsAHandshake(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*HarnessCapabilitySnapshot)
		want   string
	}{
		{"admissible", func(*HarnessCapabilitySnapshot) {}, ""},
		{"confirmed runtime", func(s *HarnessCapabilitySnapshot) {
			s.Source = CapabilitySourceConfirmedRuntime
		}, ""},
		{"static reading", func(s *HarnessCapabilitySnapshot) { s.Source = CapabilitySourceStatic }, LaunchRefusalStaticEvidence},
		{"local probe only", func(s *HarnessCapabilitySnapshot) {
			s.Source = CapabilitySourceLocalProbe
		}, LaunchRefusalStaticEvidence},
		{"no observed surface", func(s *HarnessCapabilitySnapshot) {
			s.Surfaces = CapabilitySurfaceUnknown
		}, LaunchRefusalSurfaceNotObserved},
		{"terminal surface only", func(s *HarnessCapabilitySnapshot) {
			s.Surfaces = CapabilitySurfaceTUI
		}, LaunchRefusalSurfaceNotObserved},
		{"blocked binary", func(s *HarnessCapabilitySnapshot) {
			s.Readiness = CapabilityBlockedBinary
		}, LaunchRefusalNotReady},
		{"credential unverified", func(s *HarnessCapabilitySnapshot) {
			s.AuthState = AgentAuthenticationConfigured
		}, LaunchRefusalAuthUnverified},
		{"credential rejected", func(s *HarnessCapabilitySnapshot) {
			s.AuthState = AgentAuthenticationUnauthorized
		}, LaunchRefusalAuthUnverified},
		{"expired", func(s *HarnessCapabilitySnapshot) {
			s.ObservedAt = now.Add(-2 * time.Hour)
			s.ExpiresAt = now.Add(-time.Hour)
		}, LaunchRefusalEvidenceExpired},
		{"observed in the future", func(s *HarnessCapabilitySnapshot) {
			s.ObservedAt = now.Add(time.Minute)
			s.ExpiresAt = now.Add(time.Hour)
		}, LaunchRefusalEvidenceFromFuture},
		{"another harness", func(s *HarnessCapabilitySnapshot) { s.Harness = HarnessClaudeCode }, LaunchRefusalHarnessMismatch},
		{"session scoped", func(s *HarnessCapabilitySnapshot) {
			s.ObservedSessionID = "p1-1"
			s.Generation = "gen-1"
		}, LaunchRefusalSessionScoped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			snapshot := launchFixture(now)
			tt.mutate(&snapshot)
			err := AuthorizeLaunch(snapshot, HarnessCodex, SessionModeChat, now)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("AuthorizeLaunch() = %v, want nil", err)
				}
				return
			}
			var refusal *CapabilityRefusal
			if !errors.As(err, &refusal) || refusal.Code != tt.want {
				t.Fatalf("AuthorizeLaunch() = %v, want refusal %q", err, tt.want)
			}
			if !errors.Is(err, ErrCapabilityEvidenceInsufficient) {
				t.Fatalf("refusal %q must still match the sentinel", refusal.Code)
			}
		})
	}
}

// TestAuthorizeLaunchTUIReadsInstallationNotAHandshake pins the defect this rule
// fixes: no probe reports a terminal surface, so requiring one refused every TUI
// launch. A local probe that observed a working install is the whole of what a
// terminal launch needs.
func TestAuthorizeLaunchTUIReadsInstallationNotAHandshake(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	local := NewHarnessCapabilitySnapshot(HarnessClaudeCode, CapabilitySourceLocalProbe, now, time.Minute)
	local.AuthState = AgentAuthenticationAuthorized
	local.Readiness = CapabilityReady
	local.Surfaces = CapabilitySurfaceUnknown
	if err := AuthorizeLaunch(local, HarnessClaudeCode, SessionModeTUI, now); err != nil {
		t.Fatalf("a locally probed, authorized install must authorize a TUI launch: %v", err)
	}
	// The same evidence cannot authorize a Chat launch: nothing negotiated one.
	if err := AuthorizeLaunch(local, HarnessClaudeCode, SessionModeChat, now); err == nil {
		t.Fatal("local probe evidence must not authorize a Chat launch")
	}
	// Static reading still authorizes nothing.
	static := local
	static.Source = CapabilitySourceStatic
	if err := AuthorizeLaunch(static, HarnessClaudeCode, SessionModeTUI, now); err == nil {
		t.Fatal("static reading must not authorize a TUI launch")
	}
	// A terminal launch still needs a credential it could actually use.
	unverified := local
	unverified.AuthState = AgentAuthenticationConfigured
	if err := AuthorizeLaunch(unverified, HarnessClaudeCode, SessionModeTUI, now); err == nil {
		t.Fatal("an unverified credential must not authorize a terminal launch")
	}
}

// TestAuthorizeLaunchForProfileRefusesAnUnobservedRoute covers the profile rule:
// a profile carries its own credential, so an observation of another route cannot
// authorize the launch.
func TestAuthorizeLaunchForProfileRefusesAnUnobservedRoute(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	snapshot := launchFixture(now)
	snapshot.Profile = "profile/default"
	if err := AuthorizeLaunchForProfile(snapshot, HarnessCodex, "profile/default", SessionModeChat, now); err != nil {
		t.Fatalf("matching profile: %v", err)
	}
	var refusal *CapabilityRefusal
	if err := AuthorizeLaunchForProfile(snapshot, HarnessCodex, "profile/other", SessionModeChat, now); !errors.As(err, &refusal) || refusal.Code != LaunchRefusalProfileMismatch {
		t.Fatalf("mismatched profile = %v, want %q", err, LaunchRefusalProfileMismatch)
	}
}

// TestAuthorizeLaunchRejectsAnUnknownMode keeps a zero mode from silently passing
// the mode switch, which would authorize a launch nobody described.
func TestAuthorizeLaunchRejectsAnUnknownMode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	var refusal *CapabilityRefusal
	if err := AuthorizeLaunch(launchFixture(now), HarnessCodex, SessionMode(""), now); !errors.As(err, &refusal) || refusal.Code != LaunchRefusalInvalidEvidence {
		t.Fatalf("empty mode = %v, want %q", err, LaunchRefusalInvalidEvidence)
	}
}
