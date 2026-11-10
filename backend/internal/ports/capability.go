package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Capability probing boundary.
//
// This file declares what a capability resolver needs from the outside world and
// nothing about how any provider answers. There is deliberately no provider-specific
// field here: a resolver must not be able to see a harness name and return a canned
// capability set, because that is precisely the hardcoding this boundary exists to
// prevent.
//
// Reason codes are stable strings, not booleans. A probe that could not establish a
// fact reports why, and the resolver preserves that text so a client can explain
// "we could not reach the provider" instead of silently showing an unsupported
// control.

// Capability reason codes. They are wire-visible through the resolver's output.
const (
	// CapabilityReasonNotProbed means no probe has run for this harness yet.
	CapabilityReasonNotProbed = "not_probed"
	// CapabilityReasonStaticOnly means the snapshot came from source reading.
	CapabilityReasonStaticOnly = "static_only"
	// CapabilityReasonProbeFailed means a probe ran and failed; its outcome is
	// unknown rather than negative.
	CapabilityReasonProbeFailed = "probe_failed"
	// CapabilityReasonProbeTimeout means a probe exceeded its deadline.
	CapabilityReasonProbeTimeout = "probe_timeout"
	// CapabilityReasonSnapshotExpired means the evidence is past its TTL.
	CapabilityReasonSnapshotExpired = "snapshot_expired"
	// CapabilityReasonBinaryMissing means the executable could not be located.
	CapabilityReasonBinaryMissing = "probe_binary_missing"
	// CapabilityReasonAuthMissing means the provider reported no usable credential.
	CapabilityReasonAuthMissing = "probe_auth_missing"
	// CapabilityReasonAuthRejected means the provider rejected the credential.
	CapabilityReasonAuthRejected = "probe_auth_rejected"
	// CapabilityReasonAuthUnverified means a credential exists but was never
	// validated. It must not be reported as authorized.
	CapabilityReasonAuthUnverified = "probe_auth_unverified"
	// CapabilityReasonHandshakeIncomplete means the provider answered but did not
	// advertise the capability in question.
	CapabilityReasonHandshakeIncomplete = "handshake_incomplete"
	// CapabilityReasonNoSession means the caller asked for a session-scoped fact
	// but supplied no session.
	CapabilityReasonNoSession = "no_session"
	// CapabilityReasonSessionGenerationUnknown means a session-scoped observation
	// could not establish the controller generation that owns the session, so it
	// cannot be bound to a generation and may not authorize anything. It is
	// distinct from no_session: a controller may be live and still be unable to
	// report which generation it is.
	CapabilityReasonSessionGenerationUnknown = "session_generation_unknown"
	// CapabilityReasonSessionMismatch means the only available evidence describes a
	// different session or a superseded controller generation.
	CapabilityReasonSessionMismatch = "session_mismatch"
	// CapabilityReasonGenerationMismatch means the session matches but its
	// controller generation has rotated.
	CapabilityReasonGenerationMismatch = "generation_mismatch"
	// CapabilityReasonNotObserved means the probe succeeded but this particular
	// feature was not exercised.
	CapabilityReasonNotObserved = "not_observed"
	// CapabilityReasonSupported means the evidence affirmatively reports the
	// feature.
	CapabilityReasonSupported = "supported"
	// CapabilityReasonUnsupported means the evidence affirmatively reports the
	// provider cannot do this.
	CapabilityReasonUnsupported = "unsupported"
	// CapabilityReasonNotAdvertised means the provider's own advertisement did not
	// include the feature. It is weaker than unsupported: a provider may omit an
	// optional extension it still accepts.
	CapabilityReasonNotAdvertised = "not_advertised"
)

// CapabilityProbeRequest identifies what to observe. A request with an empty
// SessionID asks for a harness-scoped observation; a request with a SessionID must
// also carry that session's controller generation.
//
// Generation is the CALLER's claim about which controller generation it is asking
// about. It is used for comparison and never copied into the answer: a snapshot's
// Generation must be observed (from the live controller) or absent, because an echoed
// claim would let a caller manufacture the generation match the gate checks.
type CapabilityProbeRequest struct {
	Harness    domain.AgentHarness
	SessionID  domain.SessionID
	Generation string
	Profile    string
	Model      string
	Effort     string
	// Mode names the launch being authorized, so a probe can observe the facts
	// that launch actually depends on. An empty Mode asks the general
	// (Chat-shaped) question. A TUI launch must not be gated on a provider
	// handshake it will never use, and its observation must not overwrite the
	// handshake evidence a later Chat launch relies on.
	Mode domain.SessionMode
}

// SessionScoped reports whether the request asks about one specific session.
func (r CapabilityProbeRequest) SessionScoped() bool {
	return r.SessionID != ""
}

// CapabilityProbe observes one harness. Implementations must return either a
// snapshot with the evidence they actually obtained or an error; they must not
// invent a negative observation to represent "I could not tell".
//
// A probe is expected to bound its own work with the caller's context deadline.
// Returning context.DeadlineExceeded is the correct way to report a timeout and
// the resolver maps it to CapabilityReasonProbeTimeout.
type CapabilityProbe interface {
	ProbeCapabilities(ctx context.Context, request CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error)
}

// CapabilityProbeFunc adapts a function to CapabilityProbe. It exists for fakes
// and tests; production adapters should implement the interface directly.
type CapabilityProbeFunc func(ctx context.Context, request CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error)

// ProbeCapabilities implements CapabilityProbe.
func (f CapabilityProbeFunc) ProbeCapabilities(
	ctx context.Context,
	request CapabilityProbeRequest,
) (domain.HarnessCapabilitySnapshot, error) {
	return f(ctx, request)
}

// CapabilityRecorder stores observed snapshots so a restart can answer from
// evidence it already collected instead of probing again blindly. An empty
// SessionID in Record addresses a harness-scoped snapshot.
type CapabilityRecorder interface {
	RecordCapabilities(ctx context.Context, snapshot domain.HarnessCapabilitySnapshot) error
	LoadCapabilities(ctx context.Context, harness domain.AgentHarness) (domain.HarnessCapabilitySnapshot, bool, error)
	LoadSessionCapabilities(
		ctx context.Context,
		sessionID domain.SessionID,
	) (domain.HarnessCapabilitySnapshot, bool, error)
}
