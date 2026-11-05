// Package capability resolves what AO has actually observed about each agent
// harness, and refuses to let weak evidence authorize a feature.
//
// The package is the third truth layer described in domain/harness_capability.go.
// It composes three inputs, in descending order of authority:
//
//  1. A registered CapabilityProbe (handshake or runtime observation).
//  2. A recorded snapshot from a previous run (CapabilityRecorder).
//  3. A static profile: what source reading suggests, never used to authorize.
//
// Every decision below turns on two questions that are deliberately separate:
// "is the evidence strong enough to gate a feature" (CapabilitySource) and "was the
// feature observed as supported for this exact session generation" (the snapshot).
// A resolver that answered either from a harness name would be a bug; the
// StaticProfile table exists only so a client can show what a provider might do,
// and Gate refuses everything that table says.
package capability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DefaultTTL bounds how long a probe result may be reused. A handshake is cheap
// relative to a user-visible wrong control, so the window stays short.
const DefaultTTL = 2 * time.Minute

// Resolver composes probes, recorded evidence, and the static display catalog.
//
// The zero value is not usable; construct with NewResolver. All injections are
// optional, and a missing probe or recorder degrades to a weaker evidence grade
// rather than to a fabricated negative.
type Resolver struct {
	probes   map[domain.AgentHarness]ports.CapabilityProbe
	recorder ports.CapabilityRecorder
	now      func() time.Time
	ttl      time.Duration
}

// Option configures a Resolver.
type Option func(*Resolver)

// WithClock injects the time source. Tests must use this instead of sleeping.
func WithClock(now func() time.Time) Option {
	return func(r *Resolver) {
		if now != nil {
			r.now = now
		}
	}
}

// WithTTL overrides the evidence lifetime.
func WithTTL(ttl time.Duration) Option {
	return func(r *Resolver) { r.ttl = ttl }
}

// WithRecorder installs durable evidence so a restart can answer without probing.
func WithRecorder(recorder ports.CapabilityRecorder) Option {
	return func(r *Resolver) { r.recorder = recorder }
}

// NewResolver builds a resolver. Probes are registered per harness; registering
// none leaves every harness on recorded or static evidence.
func NewResolver(opts ...Option) *Resolver {
	r := &Resolver{
		probes: make(map[domain.AgentHarness]ports.CapabilityProbe),
		now:    time.Now,
		ttl:    DefaultTTL,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RegisterProbe installs the probe used for one harness.
func (r *Resolver) RegisterProbe(harness domain.AgentHarness, probe ports.CapabilityProbe) {
	if probe == nil {
		delete(r.probes, harness)
		return
	}
	r.probes[harness] = probe
}

// RegisterProbeFunc is the functional form of RegisterProbe.
func (r *Resolver) RegisterProbeFunc(
	harness domain.AgentHarness,
	probe func(context.Context, ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error),
) {
	if probe == nil {
		r.RegisterProbe(harness, nil)
		return
	}
	r.RegisterProbe(harness, ports.CapabilityProbeFunc(probe))
}

// Decision is the answer to "may this feature be enabled".
//
// Allowed is never true for unknown evidence, expired evidence, or evidence whose
// grade cannot authorize a session feature. ReasonCode always explains the outcome,
// so a client can distinguish "the provider cannot do this" from "nobody asked".
type Decision struct {
	Allowed bool `json:"allowed"`
	// State is the resolved three-valued feature state. It is unknown when the
	// evidence expired or the probe failed.
	State domain.CapabilityState `json:"state"`
	// Source grades the evidence behind State.
	Source domain.CapabilitySource `json:"source"`
	// ReasonCode is a stable ports.CapabilityReason* value.
	ReasonCode string `json:"reasonCode"`
}

// Resolve returns the best snapshot AO currently holds for the request.
//
// Order of authority: probe, then recorded snapshot, then static profile. An
// expired snapshot is returned with every feature downgraded to unknown and its
// original ReasonCode preserved, so a caller can still show what was once seen
// while no gate can act on it.
//
// # Cache invalidation
//
// The resolver keeps no per-harness cache: every Resolve calls the registered probe,
// so a provider that drops a capability is observed on the next question rather than
// after a TTL. What IS persisted is evidence, and it is deliberately keyed:
//
//   - a session-scoped observation lives in the session's own row, which carries the
//     session id AND the controller generation it was observed for (loadRecorded
//     refuses it via AppliesTo unless both match);
//   - a harness-scoped observation lives in the harness row and can only answer
//     harness-level questions.
//
// A controller restart rotates the generation, so the previous generation's session
// row stops applying immediately: loadRecorded returns "not found" and the resolver
// falls through to a fresh probe instead of reusing stale negotiation facts. A
// session-scoped answer therefore cannot survive a generation change, and a
// harness-scoped answer never answers a session-scoped question at all.
func (r *Resolver) Resolve(
	ctx context.Context,
	request ports.CapabilityProbeRequest,
) (domain.HarnessCapabilitySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return r.unknown(request, ports.CapabilityReasonProbeFailed), err
	}
	if !request.Harness.IsKnown() && request.Harness != domain.HarnessFake {
		return r.unknown(request, ports.CapabilityReasonNotProbed), domain.ErrInvalidCapabilityHarness
	}

	snapshot, reasonCode, err := r.observe(ctx, request)
	if err != nil {
		return r.unknown(request, reasonCode), err
	}

	if snapshot.Source == "" {
		snapshot.Source = domain.CapabilitySourceStatic
	}
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = r.now().UTC()
	}
	if snapshot.ExpiresAt.IsZero() {
		snapshot.ExpiresAt = snapshot.ObservedAt.Add(r.ttl)
	}
	// A probe may explain its own observation (for example "handshake did not
	// advertise steering"). Only overwrite that when the resolver itself has
	// something to say, so provider-supplied detail is never erased.
	if reasonCode != "" {
		snapshot.ReasonCode = reasonCode
	}
	if snapshot.Source == domain.CapabilitySourceStatic && snapshot.ReasonCode == "" {
		snapshot.ReasonCode = ports.CapabilityReasonStaticOnly
	}

	if err := snapshot.Validate(); err != nil {
		return r.unknown(request, ports.CapabilityReasonProbeFailed), err
	}
	if snapshot.Expired(r.now()) {
		return downgradeToUnknown(snapshot), nil
	}
	return snapshot, nil
}

// observe picks the strongest available source without inventing one.
func (r *Resolver) observe(
	ctx context.Context,
	request ports.CapabilityProbeRequest,
) (domain.HarnessCapabilitySnapshot, string, error) {
	if probe, ok := r.probes[request.Harness]; ok {
		snapshot, err := probe.ProbeCapabilities(ctx, request)
		if err != nil {
			return domain.HarnessCapabilitySnapshot{}, probeFailureReason(err), err
		}
		// A session-scoped request that came back WITHOUT a session binding (no controller, or a
		// controller that could not report its generation) is a refusal, not an observation.
		// Storing it would overwrite the harness row with cleared features — turning one
		// unanswered session question into lost harness evidence — so it is not recorded.
		if r.recorder != nil && !(request.SessionScoped() && !snapshot.SessionScoped()) {
			// Recording is best effort: losing the memo must not fail a live read,
			// because the evidence in hand is still valid.
			_ = r.recorder.RecordCapabilities(ctx, snapshot)
		}
		return snapshot, "", nil
	}

	if r.recorder != nil {
		if snapshot, found, err := r.loadRecorded(ctx, request); err == nil && found {
			return snapshot, snapshot.ReasonCode, nil
		}
	}

	if profile, ok := StaticProfileAt(request.Harness, r.now(), r.ttl); ok {
		return profile, ports.CapabilityReasonStaticOnly, nil
	}

	return r.unknown(request, ports.CapabilityReasonNotProbed), ports.CapabilityReasonNotProbed, nil
}

// loadRecorded prefers an exact session-generation match, then a harness-scoped
// observation. It never returns a different session's evidence.
func (r *Resolver) loadRecorded(
	ctx context.Context,
	request ports.CapabilityProbeRequest,
) (domain.HarnessCapabilitySnapshot, bool, error) {
	if request.SessionScoped() {
		snapshot, found, err := r.recorder.LoadSessionCapabilities(ctx, request.SessionID)
		if err != nil || !found {
			return domain.HarnessCapabilitySnapshot{}, found, err
		}
		if !snapshot.AppliesTo(request.SessionID, request.Generation) {
			return domain.HarnessCapabilitySnapshot{}, false, nil
		}
		return snapshot, true, nil
	}
	return r.recorder.LoadCapabilities(ctx, request.Harness)
}

// Gate answers whether the named feature may be enabled for this request.
//
// The order of the checks is the security contract: session identity, then harness
// identity, then authorization, then readiness, then evidence grade, then freshness,
// then the observation itself. Every check is a refusal, not a warning: a snapshot that
// cannot prove all of them may not authorize work.
func (r *Resolver) Gate(
	ctx context.Context,
	request ports.CapabilityProbeRequest,
	feature domain.CapabilityFeature,
) Decision {
	if !feature.Valid() {
		return Decision{State: domain.CapabilityStateUnknown, ReasonCode: ports.CapabilityReasonNotObserved}
	}
	if request.SessionScoped() && strings.TrimSpace(request.Generation) == "" {
		return Decision{
			State: domain.CapabilityStateUnknown, Source: domain.CapabilitySourceStatic,
			ReasonCode: ports.CapabilityReasonNoSession,
		}
	}

	snapshot, err := r.Resolve(ctx, request)
	if err != nil {
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: snapshot.ReasonCode,
		}
	}

	// The snapshot must describe the harness that was asked about. A resolver whose
	// probe answered for a different harness would otherwise authorize a control on
	// evidence about something else entirely.
	if snapshot.Harness != request.Harness {
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: ports.CapabilityReasonHandshakeIncomplete,
		}
	}

	// A session-scoped observation must describe this exact session generation.
	// A harness-scoped one may answer harness-level questions only.
	if snapshot.SessionScoped() {
		if snapshot.ObservedSessionID != request.SessionID {
			return Decision{
				State: domain.CapabilityStateUnknown, Source: snapshot.Source,
				ReasonCode: ports.CapabilityReasonSessionMismatch,
			}
		}
		if snapshot.Generation != request.Generation {
			return Decision{
				State: domain.CapabilityStateUnknown, Source: snapshot.Source,
				ReasonCode: ports.CapabilityReasonGenerationMismatch,
			}
		}
	} else if request.SessionScoped() {
		// Harness-level evidence cannot authorize a per-session feature. When the observation
		// itself explained why it could not be bound to this session (no controller, or a
		// controller that could not report its generation), that explanation is the useful
		// answer: repeating "session_mismatch" would hide the difference between "nothing is
		// running" and "something is running but unidentifiable".
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: sessionRefusalReason(snapshot.ReasonCode),
		}
	}

	// Evidence grade is checked before anything that a weaker snapshot could still
	// report: static and local_probe may describe but may never authorize, and saying
	// so directly is more useful than reporting a downstream symptom of it.
	if !snapshot.Source.AllowsSessionGating() {
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: ports.CapabilityReasonStaticOnly,
		}
	}

	if snapshot.Expired(r.now()) {
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: ports.CapabilityReasonSnapshotExpired,
		}
	}

	// Authorization is a precondition, never an inference. `configured` means a
	// credential exists locally and nothing proved it works, so it must not authorize
	// a control that will spend the user's account.
	switch snapshot.AuthState {
	case domain.AgentAuthenticationAuthorized, domain.AgentAuthenticationNotApplicable:
	case domain.AgentAuthenticationUnauthorized:
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: ports.CapabilityReasonAuthRejected,
		}
	case domain.AgentAuthenticationConfigured:
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: ports.CapabilityReasonAuthUnverified,
		}
	default:
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: ports.CapabilityReasonAuthMissing,
		}
	}

	// Readiness is a separate fact from authorization: an authorized install can still
	// be unable to start, and reporting that as "the provider cannot do this" would be
	// a different (and wrong) answer.
	switch snapshot.Readiness {
	case domain.CapabilityReady:
	case domain.CapabilityBlockedAuth:
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: ports.CapabilityReasonAuthMissing,
		}
	case domain.CapabilityBlockedBinary:
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: ports.CapabilityReasonBinaryMissing,
		}
	default:
		return Decision{
			State: domain.CapabilityStateUnknown, Source: snapshot.Source,
			ReasonCode: ports.CapabilityReasonProbeFailed,
		}
	}

	state := snapshot.FeatureState(feature)
	return Decision{
		Allowed:    state.Supported(),
		State:      state,
		Source:     snapshot.Source,
		ReasonCode: featureReason(snapshot, state),
	}
}

// sessionRefusalReason maps a snapshot's own explanation of why it could not be bound to a
// session onto the reason Gate reports. Only the two "this could not be observed for a session"
// codes are preserved; anything else (including an empty code) falls back to session_mismatch,
// which is the accurate answer for evidence that simply describes a different session.
func sessionRefusalReason(reasonCode string) string {
	switch reasonCode {
	case ports.CapabilityReasonNoSession, ports.CapabilityReasonSessionGenerationUnknown:
		return reasonCode
	default:
		return ports.CapabilityReasonSessionMismatch
	}
}

// featureReason names why a feature resolved the way it did, without turning an
// absence of evidence into a negative verdict.
func featureReason(snapshot domain.HarnessCapabilitySnapshot, state domain.CapabilityState) string {
	if reason := strings.TrimSpace(snapshot.ReasonCode); reason != "" {
		return reason
	}
	switch state {
	case domain.CapabilitySupported:
		return ports.CapabilityReasonSupported
	case domain.CapabilityUnsupported:
		return ports.CapabilityReasonUnsupported
	default:
		return ports.CapabilityReasonNotObserved
	}
}

// unknown builds an all-unknown snapshot carrying the reason it is unknown. It is
// the only place the resolver creates evidence out of nothing, and it never claims
// a capability.
func (r *Resolver) unknown(
	request ports.CapabilityProbeRequest,
	reasonCode string,
) domain.HarnessCapabilitySnapshot {
	snapshot := domain.NewHarnessCapabilitySnapshot(
		request.Harness, domain.CapabilitySourceStatic, r.now().UTC(), r.ttl,
	)
	snapshot.ObservedSessionID = request.SessionID
	snapshot.Generation = request.Generation
	snapshot.ReasonCode = reasonCode
	return snapshot
}

// downgradeToUnknown clears every feature observation from an expired snapshot and
// marks readiness unknown, while preserving the original ReasonCode so a client can
// still say what was once observed. A snapshot that never carried a reason gets
// snapshot_expired, so an expired answer is never unattributed.
func downgradeToUnknown(snapshot domain.HarnessCapabilitySnapshot) domain.HarnessCapabilitySnapshot {
	if snapshot.ReasonCode == "" {
		snapshot.ReasonCode = ports.CapabilityReasonSnapshotExpired
	}
	snapshot.Readiness = domain.CapabilityUnknown
	snapshot.Streaming = domain.CapabilityStateUnknown
	snapshot.Tools = domain.CapabilityStateUnknown
	snapshot.Interrupt = domain.CapabilityStateUnknown
	snapshot.Steer = domain.CapabilityStateUnknown
	snapshot.Queue = domain.CapabilityStateUnknown
	snapshot.Resume = domain.CapabilityResumeUnknown
	snapshot.Approval = domain.CapabilityApprovalUnknown
	snapshot.ApplyAt = domain.CapabilityApplyAtUnknown
	snapshot.Usage = domain.CapabilityUsageUnknown
	return snapshot
}

// probeFailureReason maps a probe error to a stable reason code. A deadline is
// reported as a timeout rather than as a generic failure so an operator can tell a
// slow provider from a broken one.
func probeFailureReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ports.CapabilityReasonProbeTimeout
	case errors.Is(err, context.Canceled):
		return ports.CapabilityReasonProbeFailed
	default:
		return ports.CapabilityReasonProbeFailed
	}
}

// DeriveReadiness computes the coarse readiness verdict from separately observed
// installation and authentication facts. It reuses the readiness layer's own
// derivation for the install/auth part and then layers the binary-missing case,
// because a capability snapshot that knows only auth must not answer "ready".
func DeriveReadiness(
	installation domain.AgentInstallationState,
	authentication domain.AgentAuthenticationState,
) domain.CapabilityReadiness {
	switch installation {
	case domain.AgentInstallationNotInstalled:
		return domain.CapabilityBlockedBinary
	case domain.AgentInstallationUnknown:
		return domain.CapabilityUnknown
	}
	switch domain.EffectiveAgentReadiness(installation, authentication) {
	case domain.AgentReadinessReady:
		return domain.CapabilityReady
	case domain.AgentReadinessNotReady:
		if authentication == domain.AgentAuthenticationUnauthorized {
			return domain.CapabilityBlockedAuth
		}
		return domain.CapabilityBlockedBinary
	default:
		// configured and unknown both land here on purpose: an unverified
		// credential must never surface as ready.
		if authentication == domain.AgentAuthenticationConfigured {
			return domain.CapabilityBlockedAuth
		}
		return domain.CapabilityUnknown
	}
}

// Describe renders a snapshot for logs without leaking anything: it prints only
// enum values and the reason code, never Executable or Version, which can carry a
// user's home path.
func Describe(snapshot domain.HarnessCapabilitySnapshot) string {
	return fmt.Sprintf(
		"harness=%s source=%s readiness=%s auth=%s steer=%s resume=%s approval=%s usage=%s reason=%s",
		snapshot.Harness, snapshot.Source, snapshot.Readiness, snapshot.AuthState,
		snapshot.Steer, snapshot.Resume, snapshot.Approval, snapshot.Usage, snapshot.ReasonCode,
	)
}
