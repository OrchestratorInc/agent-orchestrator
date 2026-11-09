package domain

import (
	"errors"
	"strings"
	"time"
)

// Harness capability evidence: a third, deliberately separate truth layer.
//
// AO already has two capability-ish sources and this file must not be mistaken
// for either of them:
//
//   - AgentReadinessSnapshot (agent_readiness.go) answers "is the binary here and
//     is a credential present/valid". It is installation/authentication truth.
//   - ChatCapabilities (ports/chat.go) is what a chat driver reported from its own
//     Probe, cached per harness with no TTL and no session identity.
//
// A HarnessCapabilitySnapshot answers a narrower and stricter question: "what did
// we actually observe about this harness (and, for session-bound features, this
// exact session generation), when, and how strong was the evidence". That is why
// the auth vocabulary here reuses AgentAuthenticationState instead of inventing a
// parallel enum: there is one definition of "configured is not authorized" in this
// codebase, and it lives in AgentAuthenticationState's doc comment.
//
// Two invariants drive every type below:
//
//  1. Absence of evidence is never encoded as a negative. A capability that was
//     not observed is unknown, not unsupported. Several fields are therefore
//     three-valued instead of bool.
//  2. Evidence grade bounds what the observation may enable. static and
//     local_probe are display-only; only provider_handshake and confirmed_runtime
//     can gate a per-session feature.

// CapabilitySource grades where an observation came from. The order is
// load-bearing: a later value is strictly stronger evidence than an earlier one.
type CapabilitySource string

const (
	// CapabilitySourceStatic is source-code reasoning only. It can show a client
	// what a provider might do; it must never enable an action.
	CapabilitySourceStatic CapabilitySource = "static"
	// CapabilitySourceLocalProbe proves a binary or config file exists locally,
	// with a bounded timeout. It says nothing about whether the provider will
	// accept work.
	CapabilitySourceLocalProbe CapabilitySource = "local_probe"
	// CapabilitySourceProviderHandshake came from the provider answering a protocol
	// handshake (initialize/session-new/capability advertisement).
	CapabilitySourceProviderHandshake CapabilitySource = "provider_handshake"
	// CapabilitySourceConfirmedRuntime came from observing the provider actually
	// perform the operation in this session.
	CapabilitySourceConfirmedRuntime CapabilitySource = "confirmed_runtime"
)

// Valid reports whether s is a supported evidence grade.
func (s CapabilitySource) Valid() bool {
	switch s {
	case CapabilitySourceStatic, CapabilitySourceLocalProbe,
		CapabilitySourceProviderHandshake, CapabilitySourceConfirmedRuntime:
		return true
	default:
		return false
	}
}

// AllowsSessionGating reports whether evidence at this grade may enable a
// per-session capability such as steer or interrupt. Static source reading and a
// local binary probe may describe, but never authorize.
func (s CapabilitySource) AllowsSessionGating() bool {
	return s == CapabilitySourceProviderHandshake || s == CapabilitySourceConfirmedRuntime
}

// CapabilityReadiness is the coarse verdict for one harness snapshot. It is
// derived, never stored by a provider.
type CapabilityReadiness string

const (
	// CapabilityReady means the strongest available evidence says the harness can
	// be used.
	CapabilityReady CapabilityReadiness = "ready"
	// CapabilityBlockedAuth means a harness is present but authentication is
	// missing, rejected, or merely unverified where that distinction matters.
	CapabilityBlockedAuth CapabilityReadiness = "blocked_auth"
	// CapabilityBlockedBinary means the executable could not be located.
	CapabilityBlockedBinary CapabilityReadiness = "blocked_binary"
	// CapabilityUnknown means readiness cannot be determined from the evidence we
	// hold. It is not a failure verdict.
	CapabilityUnknown CapabilityReadiness = "unknown"
)

// Valid reports whether r is a supported readiness verdict.
func (r CapabilityReadiness) Valid() bool {
	switch r {
	case CapabilityReady, CapabilityBlockedAuth, CapabilityBlockedBinary, CapabilityUnknown:
		return true
	default:
		return false
	}
}

// CapabilityState is the three-valued answer for a feature that is otherwise a
// boolean. Unsupported requires evidence that the provider does not offer it;
// unknown means nobody asked or the answer did not arrive.
type CapabilityState string

// CapabilitySupported 要求肯定证据；未观察与明确不支持必须区分。
const (
	CapabilitySupported    CapabilityState = "supported"
	CapabilityUnsupported  CapabilityState = "unsupported"
	CapabilityStateUnknown CapabilityState = "unknown"
)

// Valid reports whether s is a supported capability state.
func (s CapabilityState) Valid() bool {
	switch s {
	case CapabilitySupported, CapabilityUnsupported, CapabilityStateUnknown:
		return true
	default:
		return false
	}
}

// Supported reports whether the state is affirmative. Unknown is never true.
func (s CapabilityState) Supported() bool { return s == CapabilitySupported }

// DeterminatelyUnsupported reports whether evidence says the provider cannot do
// this. Callers use it to hide a control; unknown callers must not, because the
// provider may well support it.
func (s CapabilityState) DeterminatelyUnsupported() bool { return s == CapabilityUnsupported }

// CapabilitySurface is the interaction surface a harness was observed to offer.
type CapabilitySurface string

// CapabilitySurfaceChat 表示已观察到的 Chat 交互面。
const (
	CapabilitySurfaceChat    CapabilitySurface = "chat"
	CapabilitySurfaceTUI     CapabilitySurface = "tui"
	CapabilitySurfaceBoth    CapabilitySurface = "both"
	CapabilitySurfaceUnknown CapabilitySurface = "unknown"
)

// Valid reports whether s is a supported surface value.
func (s CapabilitySurface) Valid() bool {
	switch s {
	case CapabilitySurfaceChat, CapabilitySurfaceTUI, CapabilitySurfaceBoth, CapabilitySurfaceUnknown:
		return true
	default:
		return false
	}
}

// CapabilityResume is how a provider can bring a previous conversation back.
// "has history" and "has context" are different guarantees, so the middle value
// is not a lesser spelling of the first.
type CapabilityResume string

const (
	// CapabilityResumeWithHistory reloads typed transcript history as well.
	CapabilityResumeWithHistory CapabilityResume = "load_with_history"
	// CapabilityResumeNoHistory restores provider context only; AO has no typed
	// transcript to render and must not claim a lossless replay.
	CapabilityResumeNoHistory CapabilityResume = "resume_no_history"
	// CapabilityResumeNone means the provider cannot resume at all.
	CapabilityResumeNone CapabilityResume = "none"
	// CapabilityResumeUnknown means resume support was not established.
	CapabilityResumeUnknown CapabilityResume = "unknown"
)

// Valid reports whether r is a supported resume value.
func (r CapabilityResume) Valid() bool {
	switch r {
	case CapabilityResumeWithHistory, CapabilityResumeNoHistory, CapabilityResumeNone, CapabilityResumeUnknown:
		return true
	default:
		return false
	}
}

// State collapses resume support to the shared three-valued feature state.
func (r CapabilityResume) State() CapabilityState {
	switch r {
	case CapabilityResumeWithHistory, CapabilityResumeNoHistory:
		return CapabilitySupported
	case CapabilityResumeNone:
		return CapabilityUnsupported
	default:
		return CapabilityStateUnknown
	}
}

// CapabilityApproval describes how strongly a provider enforces an approval
// policy. "Advertised" is not "enforced": a mode string a CLI accepts but never
// acts on gives a user no gate.
type CapabilityApproval string

// CapabilityApprovalEnforced 表示已验证执行审批策略，不能由广告能力推定。
const (
	CapabilityApprovalEnforced       CapabilityApproval = "enforced"
	CapabilityApprovalAdvertisedOnly CapabilityApproval = "advertised_only"
	CapabilityApprovalUnknown        CapabilityApproval = "unknown"
)

// Valid reports whether a is a supported approval strength.
func (a CapabilityApproval) Valid() bool {
	switch a {
	case CapabilityApprovalEnforced, CapabilityApprovalAdvertisedOnly, CapabilityApprovalUnknown:
		return true
	default:
		return false
	}
}

// State collapses approval strength to the shared three-valued feature state.
//
// advertised_only is deliberately NOT supported. A CLI that accepts an approval flag
// but never enforces it gives the user no gate at all, so reporting it as "supported"
// would let a policy gate authorize work on the strength of a string AO cannot rely
// on. It stays unknown: the provider may enforce it, and nothing has proven that.
// CapabilityApprovalEnforced is the only value that reads as supported.
func (a CapabilityApproval) State() CapabilityState {
	switch a {
	case CapabilityApprovalEnforced:
		return CapabilitySupported
	default:
		return CapabilityStateUnknown
	}
}

// CapabilityApplyAt is when an approval policy takes effect. A launch-scoped
// policy cannot be switched live without restarting the provider.
type CapabilityApplyAt string

// CapabilityApplyAtLaunch 表示审批策略仅在启动时生效。
const (
	CapabilityApplyAtLaunch  CapabilityApplyAt = "launch"
	CapabilityApplyAtTurn    CapabilityApplyAt = "turn"
	CapabilityApplyAtRequest CapabilityApplyAt = "request"
	CapabilityApplyAtUnknown CapabilityApplyAt = "unknown"
)

// Valid reports whether a is a supported application point.
func (a CapabilityApplyAt) Valid() bool {
	switch a {
	case CapabilityApplyAtLaunch, CapabilityApplyAtTurn, CapabilityApplyAtRequest, CapabilityApplyAtUnknown:
		return true
	default:
		return false
	}
}

// CapabilityUsage is how accurate a provider's token accounting is. No harness
// reports money, so cost is never exact even when usage is.
type CapabilityUsage string

// CapabilityUsageExact 表示用量计量精确，不代表费用精确。
const (
	CapabilityUsageExact     CapabilityUsage = "exact"
	CapabilityUsageEstimated CapabilityUsage = "estimated"
	CapabilityUsageUnknown   CapabilityUsage = "unknown"
)

// Valid reports whether u is a supported usage accuracy.
func (u CapabilityUsage) Valid() bool {
	switch u {
	case CapabilityUsageExact, CapabilityUsageEstimated, CapabilityUsageUnknown:
		return true
	default:
		return false
	}
}

// State collapses usage accuracy to the shared three-valued feature state.
// Estimated still counts as supported: AO reports a number and labels it.
func (u CapabilityUsage) State() CapabilityState {
	switch u {
	case CapabilityUsageExact, CapabilityUsageEstimated:
		return CapabilitySupported
	default:
		return CapabilityStateUnknown
	}
}

// CapabilityFeature names one gateable capability. It exists so callers gate on a
// feature, never on a harness name.
type CapabilityFeature string

// CapabilityFeatureStreaming 标识可以独立获取证据的功能。
const (
	CapabilityFeatureStreaming CapabilityFeature = "streaming"
	CapabilityFeatureTools     CapabilityFeature = "tools"
	CapabilityFeatureInterrupt CapabilityFeature = "interrupt"
	CapabilityFeatureSteer     CapabilityFeature = "steer"
	CapabilityFeatureQueue     CapabilityFeature = "queue"
	CapabilityFeatureResume    CapabilityFeature = "resume"
	CapabilityFeatureApproval  CapabilityFeature = "approval"
	CapabilityFeatureUsage     CapabilityFeature = "usage"
)

// Valid reports whether f is a supported feature name.
func (f CapabilityFeature) Valid() bool {
	switch f {
	case CapabilityFeatureStreaming, CapabilityFeatureTools, CapabilityFeatureInterrupt,
		CapabilityFeatureSteer, CapabilityFeatureQueue, CapabilityFeatureResume,
		CapabilityFeatureApproval, CapabilityFeatureUsage:
		return true
	default:
		return false
	}
}

// CapabilityModelChoice is one advertised model and the efforts it accepts. Only
// values a provider advertised are recorded; an empty Effort list means the
// provider offered no effort axis, not that effort is unsupported.
type CapabilityModelChoice struct {
	Model   string   `json:"model"`
	Effort  []string `json:"effort,omitempty"`
	Default string   `json:"defaultEffort,omitempty"`
}

// Validate rejects an empty model and duplicate efforts.
func (c CapabilityModelChoice) Validate() error {
	if strings.TrimSpace(c.Model) == "" {
		return ErrInvalidCapabilityModelChoice
	}
	seen := make(map[string]struct{}, len(c.Effort))
	for _, effort := range c.Effort {
		trimmed := strings.TrimSpace(effort)
		if trimmed == "" {
			return ErrInvalidCapabilityModelChoice
		}
		if _, dup := seen[trimmed]; dup {
			return ErrInvalidCapabilityModelChoice
		}
		seen[trimmed] = struct{}{}
	}
	return nil
}

// HarnessCapabilitySnapshot is one graded observation of one harness. Identity
// fields (executable, version) are metadata only: no credential, token, or
// account path may ever be stored here.
type HarnessCapabilitySnapshot struct {
	Harness AgentHarness `json:"harness"`
	// Profile 绑定宿主路由摘要；凭据身份仅保存在探测器内存中。
	Profile string `json:"profile,omitempty"`

	// Executable identifies the resolved binary for display and support triage.
	// It must be a path or name only, never an argument containing a secret.
	Executable string `json:"executable,omitempty"`
	// Version is the provider-reported version string, verbatim.
	Version string `json:"version,omitempty"`

	ObservedAt time.Time        `json:"observedAt"`
	ExpiresAt  time.Time        `json:"expiresAt"`
	Source     CapabilitySource `json:"source"`

	AuthState AgentAuthenticationState `json:"authState"`
	Readiness CapabilityReadiness      `json:"readiness"`
	Surfaces  CapabilitySurface        `json:"surfaces"`

	Streaming CapabilityState `json:"streaming"`
	Tools     CapabilityState `json:"tools"`
	Interrupt CapabilityState `json:"interrupt"`
	Steer     CapabilityState `json:"steer"`
	Queue     CapabilityState `json:"queue"`

	Resume   CapabilityResume   `json:"resume"`
	Approval CapabilityApproval `json:"approval"`
	ApplyAt  CapabilityApplyAt  `json:"applyAt"`
	Usage    CapabilityUsage    `json:"usage"`

	Models []CapabilityModelChoice `json:"models,omitempty"`

	// ObservedSessionID and Generation bind a session-scoped observation to the
	// exact session and controller generation it describes. Empty SessionID means
	// the observation is harness-scoped and may describe no particular session.
	ObservedSessionID SessionID `json:"observedSessionId,omitempty"`
	Generation        string    `json:"generation,omitempty"`

	// ReasonCode preserves why the snapshot says what it says, including the
	// failure that produced an unknown value.
	ReasonCode string `json:"reasonCode,omitempty"`
}

// NewHarnessCapabilitySnapshot builds a snapshot with an explicit TTL. A
// non-positive ttl yields an already-expired snapshot rather than one that never
// expires, so a caller that forgot the TTL cannot gate anything.
func NewHarnessCapabilitySnapshot(
	harness AgentHarness,
	source CapabilitySource,
	observedAt time.Time,
	ttl time.Duration,
) HarnessCapabilitySnapshot {
	observedAt = observedAt.UTC()
	return HarnessCapabilitySnapshot{
		Harness:    harness,
		Source:     source,
		ObservedAt: observedAt,
		ExpiresAt:  observedAt.Add(ttl),
		AuthState:  AgentAuthenticationUnknown,
		Readiness:  CapabilityUnknown,
		Surfaces:   CapabilitySurfaceUnknown,
		Streaming:  CapabilityStateUnknown,
		Tools:      CapabilityStateUnknown,
		Interrupt:  CapabilityStateUnknown,
		Steer:      CapabilityStateUnknown,
		Queue:      CapabilityStateUnknown,
		Resume:     CapabilityResumeUnknown,
		Approval:   CapabilityApprovalUnknown,
		ApplyAt:    CapabilityApplyAtUnknown,
		Usage:      CapabilityUsageUnknown,
	}
}

// Expired reports whether the snapshot's evidence is past its TTL at now. A zero
// ExpiresAt is treated as expired: an observation with no stated lifetime must
// not authorize work.
func (s HarnessCapabilitySnapshot) Expired(now time.Time) bool {
	if s.ExpiresAt.IsZero() || s.ObservedAt.IsZero() {
		return true
	}
	return !now.UTC().Before(s.ExpiresAt.UTC())
}

// SessionScoped reports whether the observation describes one specific session
// generation rather than the harness in general.
func (s HarnessCapabilitySnapshot) SessionScoped() bool {
	return strings.TrimSpace(string(s.ObservedSessionID)) != ""
}

// AppliesTo reports whether this observation may describe the given session and
// generation. A harness-scoped snapshot never claims a specific session; a
// session-scoped one must match both identifiers exactly, because a controller
// restart rotates the generation and invalidates earlier steering observations.
func (s HarnessCapabilitySnapshot) AppliesTo(sessionID SessionID, generation string) bool {
	if !s.SessionScoped() {
		return strings.TrimSpace(string(sessionID)) == ""
	}
	return s.ObservedSessionID == sessionID && s.Generation == generation
}

// FeatureState returns the recorded state for one feature.
func (s HarnessCapabilitySnapshot) FeatureState(feature CapabilityFeature) CapabilityState {
	switch feature {
	case CapabilityFeatureStreaming:
		return s.Streaming
	case CapabilityFeatureTools:
		return s.Tools
	case CapabilityFeatureInterrupt:
		return s.Interrupt
	case CapabilityFeatureSteer:
		return s.Steer
	case CapabilityFeatureQueue:
		return s.Queue
	case CapabilityFeatureResume:
		return s.Resume.State()
	case CapabilityFeatureApproval:
		return s.Approval.State()
	case CapabilityFeatureUsage:
		return s.Usage.State()
	default:
		return CapabilityStateUnknown
	}
}

// Gates reports whether this snapshot's evidence may enable the feature at all,
// ignoring whether the feature was observed as supported. It is the evidence-grade
// half of a gate decision; see Service-level resolution for the session half.
func (s HarnessCapabilitySnapshot) Gates(feature CapabilityFeature) bool {
	return s.Source.AllowsSessionGating() && feature.Valid()
}

// validCapabilityAuthState reuses the readiness layer's closed set rather than
// declaring a parallel one. "configured" is a member precisely so it can be
// rejected as a ready signal by callers while remaining a valid observation.
func validCapabilityAuthState(state AgentAuthenticationState) bool {
	switch state {
	case AgentAuthenticationAuthorized, AgentAuthenticationUnauthorized,
		AgentAuthenticationUnknown, AgentAuthenticationNotApplicable,
		AgentAuthenticationConfigured:
		return true
	default:
		return false
	}
}

// Validate checks enum membership and identity. It deliberately does not require
// any feature to be supported: an all-unknown snapshot is valid and useful.
func (s HarnessCapabilitySnapshot) Validate() error {
	switch {
	case !s.Harness.IsKnown() && s.Harness != HarnessFake:
		return ErrInvalidCapabilityHarness
	case !s.Source.Valid():
		return ErrInvalidCapabilitySource
	case !s.Readiness.Valid():
		return ErrInvalidCapabilityReadiness
	case !s.Surfaces.Valid():
		return ErrInvalidCapabilitySurface
	case !validCapabilityAuthState(s.AuthState):
		return ErrInvalidCapabilityAuthState
	case !s.Streaming.Valid(), !s.Tools.Valid(), !s.Interrupt.Valid(), !s.Steer.Valid(), !s.Queue.Valid():
		return ErrInvalidCapabilityState
	case !s.Resume.Valid():
		return ErrInvalidCapabilityResume
	case !s.Approval.Valid():
		return ErrInvalidCapabilityApproval
	case !s.ApplyAt.Valid():
		return ErrInvalidCapabilityApplyAt
	case !s.Usage.Valid():
		return ErrInvalidCapabilityUsage
	case s.ObservedAt.IsZero():
		return ErrInvalidCapabilityObservedAt
	case s.ExpiresAt.Before(s.ObservedAt):
		return ErrInvalidCapabilityExpiry
	case s.SessionScoped() && strings.TrimSpace(s.Generation) == "":
		return ErrInvalidCapabilityGeneration
	}
	for _, choice := range s.Models {
		if err := choice.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Capability domain validation errors.
var (
	ErrInvalidCapabilityHarness       = errors.New("invalid capability harness")
	ErrInvalidCapabilitySource        = errors.New("invalid capability source")
	ErrInvalidCapabilityReadiness     = errors.New("invalid capability readiness")
	ErrInvalidCapabilitySurface       = errors.New("invalid capability surface")
	ErrInvalidCapabilityAuthState     = errors.New("invalid capability auth state")
	ErrInvalidCapabilityState         = errors.New("invalid capability state")
	ErrInvalidCapabilityResume        = errors.New("invalid capability resume value")
	ErrInvalidCapabilityApproval      = errors.New("invalid capability approval value")
	ErrInvalidCapabilityApplyAt       = errors.New("invalid capability apply-at value")
	ErrInvalidCapabilityUsage         = errors.New("invalid capability usage value")
	ErrInvalidCapabilityObservedAt    = errors.New("capability observation time is required")
	ErrInvalidCapabilityExpiry        = errors.New("capability expiry precedes its observation")
	ErrInvalidCapabilityGeneration    = errors.New("session-scoped capability snapshot requires a generation")
	ErrInvalidCapabilityModelChoice   = errors.New("invalid capability model choice")
	ErrCapabilityEvidenceInsufficient = errors.New("capability evidence is insufficient to authorize this feature")
)
