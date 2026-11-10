package capability

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ChatDriverProbe is the production ports.CapabilityProbe.
//
// It answers from two sources that already exist, in this order:
//
//  1. the daemon's readiness provider, which says whether the binary is present and
//     whether a credential is usable;
//  2. the chat driver's own Probe, which is the provider handshake.
//
// There is deliberately no per-harness table here. A probe that answered from a
// harness name would be the exact hardcoding the capability boundary exists to
// prevent, and it would keep reporting a capability after a provider dropped it.
//
// The mapping rule is one-directional: a capability the driver reports as present is
// supported, and a capability it does not report is unknown — never unsupported. Absence
// of a key in a driver's advertisement is not evidence the provider cannot do it, and
// only evidence that it cannot may hide a control.
type ChatDriverProbe struct {
	sessions  SessionProber
	drivers   ports.ChatDriverRegistry
	readiness ports.AgentReadinessProvider
	now       func() time.Time
	ttl       time.Duration
	profiles  ports.ProviderProfiles
}

// SessionProber reports the live controller state this probe needs for a
// session-scoped request. It is optional: a harness-scoped request never uses it.
type SessionProber interface {
	// SessionGeneration reports the controller generation currently owning a session,
	// and whether a live chat controller exists at all.
	SessionGeneration(sessionID domain.SessionID) (generation string, live bool)
	// ChatCapabilities reports what the live controller actually negotiated, which can
	// be narrower than what the driver advertised.
	ChatCapabilities(sessionID domain.SessionID) (ports.ChatCapabilities, bool)
}

// NewChatDriverProbe builds the probe. A nil readiness provider or driver registry
// makes the probe report unknown rather than guess; both are wired in the daemon.
func NewChatDriverProbe(
	sessions SessionProber,
	drivers ports.ChatDriverRegistry,
	readiness ports.AgentReadinessProvider,
	now func() time.Time,
	ttl time.Duration,
	profiles ...ports.ProviderProfiles,
) ports.CapabilityProbe {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	p := &ChatDriverProbe{
		sessions:  sessions,
		drivers:   drivers,
		readiness: readiness,
		now:       now,
		ttl:       ttl,
	}
	if len(profiles) > 0 {
		p.profiles = profiles[0]
	}
	return p
}

// ProbeCapabilities implements ports.CapabilityProbe.
func (p *ChatDriverProbe) ProbeCapabilities(
	ctx context.Context,
	request ports.CapabilityProbeRequest,
) (domain.HarnessCapabilitySnapshot, error) {
	observedAt := p.now().UTC()
	// The starting point is local_probe, not static: this probe always inspects the
	// local installation, and the grade is raised only when a handshake succeeds.
	snapshot := domain.NewHarnessCapabilitySnapshot(
		request.Harness, domain.CapabilitySourceLocalProbe, observedAt, p.ttl)
	snapshot.ObservedSessionID = request.SessionID
	snapshot.Profile = request.Profile
	// request.Generation is deliberately NOT copied here. It is the caller's claim about
	// which controller generation it is asking about, and the resolver's gate compares
	// that claim with what was OBSERVED. Echoing it would make the comparison vacuous and
	// let any caller authorize any generation. Generation is set below, and only from the
	// live controller.
	snapshot.ReasonCode = ports.CapabilityReasonProbeFailed

	installation, authentication, ready := p.readReadiness(ctx, request)
	snapshot.AuthState = authentication
	snapshot.Readiness = DeriveReadiness(installation, authentication)

	if !ready {
		// No usable install or credential: every feature stays unknown, and the evidence
		// grade stays local_probe so nothing downstream can gate on it.
		if snapshot.Readiness == domain.CapabilityReady {
			snapshot.Readiness = domain.CapabilityUnknown
		}
		if installation == domain.AgentInstallationNotInstalled {
			snapshot.ReasonCode = ports.CapabilityReasonBinaryMissing
			snapshot.Readiness = domain.CapabilityBlockedBinary
		} else if authentication == domain.AgentAuthenticationConfigured ||
			authentication == domain.AgentAuthenticationUnauthorized {
			snapshot.ReasonCode = ports.CapabilityReasonAuthUnverified
			if authentication == domain.AgentAuthenticationUnauthorized {
				snapshot.ReasonCode = ports.CapabilityReasonAuthRejected
			}
		}
		return snapshot, nil
	}

	if request.Mode == domain.SessionModeTUI {
		// A terminal launch runs the harness's own CLI; it never opens a chat
		// controller, so a handshake would be both irrelevant and a side effect
		// nobody asked for (it starts a provider process). The installation and
		// credential facts above are the whole observation, and every chat feature
		// stays unknown because nothing negotiated one.
		snapshot.ReasonCode = ports.CapabilityReasonNotProbed
		return snapshot, nil
	}

	driver, err := p.driver(request.Harness)
	if err != nil {
		// No driver is not a negative capability claim: the harness has no chat driver
		// in this build, which the readiness evidence cannot turn into "unsupported
		// features".
		// Surface stays unknown: "no chat driver" says nothing about whether the
		// harness has a terminal interface, and this probe cannot see one.
		snapshot.ReasonCode = ports.CapabilityReasonNotProbed
		return snapshot, nil //nolint:nilerr // 无 Driver 表示 unknown/not_probed，不能改为不支持或硬错误。
	}

	advertised, err := driver.Probe(ctx)
	if err != nil {
		// A failed handshake is unknown, never a negative. The reason code preserves
		// the error class so an operator can tell a timeout from a broken install.
		snapshot.ReasonCode = probeReason(err)
		return snapshot, err
	}

	// The handshake succeeded. It is a PROMISE, not a proof: the driver says it can do
	// these things in general, which is exactly what provider_handshake means and exactly
	// why it may never be reported as confirmed_runtime. Only Recorder.RecordConfirmed —
	// called by the chat service after an operation actually landed — may do that.
	snapshot.Source = domain.CapabilitySourceProviderHandshake
	snapshot.ReasonCode = ""
	snapshot.Surfaces = domain.CapabilitySurfaceChat
	applyAdvertised(&snapshot, advertised)

	if request.SessionScoped() {
		// A session-scoped answer is the CONTROLLER's answer, not the harness's. It
		// narrows what the handshake just claimed and can never widen it.
		p.applyLiveController(&snapshot, request)
	}
	return snapshot, nil
}

// readReadiness asks the daemon for install/auth facts. A missing provider leaves both
// unknown, which DeriveReadiness turns into CapabilityUnknown.
func (p *ChatDriverProbe) readReadiness(
	ctx context.Context,
	request ports.CapabilityProbeRequest,
) (domain.AgentInstallationState, domain.AgentAuthenticationState, bool) {
	if request.Profile != "" {
		installation := domain.AgentInstallationUnknown
		// 自定义路由只借用安装事实，默认账号的探测与结果均不能参与。
		if checker, ok := p.readiness.(interface {
			EnsureAgentInstallation(context.Context, string) (domain.AgentInstallationState, error)
		}); ok {
			if state, err := checker.EnsureAgentInstallation(ctx, string(request.Harness)); err == nil {
				installation = state
			}
		}
		if p.profiles == nil || installation != domain.AgentInstallationInstalled {
			return installation, domain.AgentAuthenticationUnknown, false
		}
		auth, err := p.profiles.Authentication(ctx, request.Profile, request.Harness, request.Model, request.Effort)
		if err != nil {
			return installation, domain.AgentAuthenticationUnknown, false
		}
		return installation, auth, auth == domain.AgentAuthenticationAuthorized
	}
	if p.readiness == nil {
		return domain.AgentInstallationUnknown, domain.AgentAuthenticationUnknown, false
	}
	snapshot, err := p.readiness.EnsureAgentReadiness(
		ctx, string(request.Harness), domain.AgentReadinessPurposeLaunch)
	if err != nil {
		return domain.AgentInstallationUnknown, domain.AgentAuthenticationUnknown, false
	}
	installation := snapshot.Installation.State
	authentication := snapshot.Authentication.State
	ready := installation == domain.AgentInstallationInstalled &&
		(authentication == domain.AgentAuthenticationAuthorized ||
			authentication == domain.AgentAuthenticationNotApplicable)
	return installation, authentication, ready
}

// driver resolves the chat driver for one harness.
func (p *ChatDriverProbe) driver(harness domain.AgentHarness) (ports.ChatDriver, error) {
	if p.drivers == nil {
		return nil, ports.ErrChatUnsupported
	}
	return p.drivers.Driver(harness)
}

// applyLiveController narrows a session-scoped snapshot to what the live controller
// actually negotiated.
//
// # Why this NARROWS rather than merges
//
// The handshake above describes the harness in general. A session is one running controller,
// and what it negotiated is its own, narrower fact. So for a session-scoped request the
// controller's map is authoritative and REPLACES the harness advertisement:
//
//   - a feature the controller reports is supported;
//   - a feature the controller does not report is reset to UNKNOWN.
//
// Absence in a negotiation is evidence of "not negotiated for this session", which is a
// statement about this session only, not about the provider. It is reset to unknown — never
// to unsupported — because "this controller did not negotiate it" is not proof the provider
// cannot do it, and inventing that negative would hide a control the provider does support.
// Unknown is the fail-closed choice: the gate refuses it, and the snapshot stops claiming a
// capability nobody observed for this generation.
//
// # Generation is observed, never echoed
//
// The snapshot's Generation may only come from the live controller. When the controller
// cannot report one, Generation stays empty and the reason code says why, so the resolver's
// generation comparison refuses instead of matching a caller-supplied claim.
func (p *ChatDriverProbe) applyLiveController(
	snapshot *domain.HarnessCapabilitySnapshot,
	request ports.CapabilityProbeRequest,
) {
	if p.sessions == nil {
		// No session prober at all: nothing session-scoped was observed. Clearing the
		// harness advertisement is the only honest answer, because it describes the harness,
		// not this session.
		unbindSession(snapshot, ports.CapabilityReasonNoSession)
		return
	}
	liveCapabilities, live := p.sessions.ChatCapabilities(request.SessionID)
	if !live {
		// No controller means no session-scoped evidence at all. Leaving the harness
		// advertisement in place would let it authorize work for a session that is not
		// running, so the negotiated facts are cleared and the resolver's own session
		// checks refuse the request.
		unbindSession(snapshot, ports.CapabilityReasonNoSession)
		return
	}
	generation, ok := p.sessions.SessionGeneration(request.SessionID)
	if !ok || strings.TrimSpace(generation) == "" {
		// A live controller that cannot say which generation it is cannot bind an
		// observation to one, and an unbound observation may not authorize anything. The
		// generation is left empty — it is never invented — and the snapshot stops being a
		// session observation, which is what makes Gate refuse.
		unbindSession(snapshot, ports.CapabilityReasonSessionGenerationUnknown)
		return
	}
	clearNegotiated(snapshot)
	applyNegotiated(snapshot, liveCapabilities)
	// The evidence grade stays provider_handshake: a controller reporting what it negotiated
	// is still a handshake. confirmed_runtime is reserved for an operation that was actually
	// performed and recorded through Recorder.RecordConfirmed.
	snapshot.Source = domain.CapabilitySourceProviderHandshake
	snapshot.Generation = generation
}

// unbindSession turns a snapshot that cannot be bound to a controller generation into an
// explicitly harness-scoped one that claims nothing.
//
// Two facts are deliberately separated here. The session id is cleared because a
// session-scoped snapshot MUST carry the generation it describes (domain.Validate enforces it),
// and a snapshot naming a session without one would either be invalid or — worse — tempt a later
// reader into treating the caller's claimed generation as the observation. The features are
// cleared to unknown because the harness advertisement described the harness, not this session.
//
// The reason code is what a client reads to learn WHY the session could not be described, and
// Gate surfaces it rather than flattening every such refusal into a generic mismatch.
func unbindSession(snapshot *domain.HarnessCapabilitySnapshot, reasonCode string) {
	clearNegotiated(snapshot)
	snapshot.ObservedSessionID = ""
	snapshot.Generation = ""
	snapshot.ReasonCode = reasonCode
}

// clearNegotiated resets every feature that a session negotiation can speak to.
//
// It writes UNKNOWN and never UNSUPPORTED: the points of this function are that a harness
// advertisement must not survive into a session-scoped answer, and that the reset is not a
// fabricated negative. Harness-level facts (auth state, readiness, surfaces, models) are left
// alone — they were observed about the install, which genuinely applies to this session.
func clearNegotiated(snapshot *domain.HarnessCapabilitySnapshot) {
	snapshot.Streaming = domain.CapabilityStateUnknown
	snapshot.Tools = domain.CapabilityStateUnknown
	snapshot.Interrupt = domain.CapabilityStateUnknown
	snapshot.Steer = domain.CapabilityStateUnknown
	snapshot.Queue = domain.CapabilityStateUnknown
	snapshot.Resume = domain.CapabilityResumeUnknown
	snapshot.Approval = domain.CapabilityApprovalUnknown
	snapshot.Usage = domain.CapabilityUsageUnknown
}

// applyNegotiated maps a live controller's negotiated capability set onto a snapshot that has
// already been cleared. present → supported, absent → stays unknown.
func applyNegotiated(snapshot *domain.HarnessCapabilitySnapshot, negotiated ports.ChatCapabilities) {
	applyAdvertised(snapshot, negotiated)
}

// applyAdvertised maps one driver capability map onto the snapshot.
//
// present → supported, absent → left unknown. There is no branch that writes
// CapabilityUnsupported, because a driver's map carries no "cannot" signal: only a
// provider refusal observed at runtime could, and this probe never observes one.
func applyAdvertised(snapshot *domain.HarnessCapabilitySnapshot, advertised ports.ChatCapabilities) {
	snapshot.Streaming = advertisedState(advertised, ports.ChatCapabilityStreaming, snapshot.Streaming)
	snapshot.Tools = advertisedState(advertised, ports.ChatCapabilityTools, snapshot.Tools)
	snapshot.Interrupt = advertisedState(advertised, ports.ChatCapabilityInterrupt, snapshot.Interrupt)
	snapshot.Steer = advertisedState(advertised, ports.ChatCapabilitySteer, snapshot.Steer)
	// Queue has no driver capability of its own: it is AO's durable queue behind a
	// busy controller, so it follows tool/streaming support rather than inventing one.
	if advertised.Has(ports.ChatCapabilityStreaming) {
		snapshot.Queue = domain.CapabilitySupported
	}
	if advertised.Has(ports.ChatCapabilityUsage) {
		snapshot.Usage = domain.CapabilityUsageEstimated
	}
	if advertised.Has(ports.ChatCapabilityApprovals) {
		// Advertised is not enforced: a provider that accepts a policy flag but does not
		// act on it gives the user no gate. The honest value is advertised_only, whose
		// State() is unknown, so it describes without authorizing.
		snapshot.Approval = domain.CapabilityApprovalAdvertisedOnly
	}
	if advertised.Has(ports.ChatCapabilityResume) {
		snapshot.Resume = domain.CapabilityResumeNoHistory
		if advertised.Has(ports.ChatCapabilityHistory) {
			snapshot.Resume = domain.CapabilityResumeWithHistory
		}
	}
}

// advertisedState reports supported when the driver advertises the capability and
// otherwise leaves the existing value untouched.
func advertisedState(
	advertised ports.ChatCapabilities,
	capability ports.ChatCapability,
	current domain.CapabilityState,
) domain.CapabilityState {
	if advertised.Has(capability) {
		return domain.CapabilitySupported
	}
	return current
}

// probeReason maps a driver probe failure to a stable reason code.
func probeReason(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "timeout") {
		return ports.CapabilityReasonProbeTimeout
	}
	return ports.CapabilityReasonProbeFailed
}
