package capability

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Static profiles: what source reading says a provider might do.
//
// These values are display-only by construction. They carry
// CapabilitySourceStatic, and Resolver.Gate refuses everything at that grade, so
// no entry here can enable a control. They exist so a client can render a
// meaningful "this harness may offer X, unverified" state instead of an empty
// matrix, and so an operator can see what AO believes before any probe has run.
//
// Every value below is anchored to a source location, and every anchor was read
// rather than inferred from a design document. Where source reading cannot decide
// the answer, the field stays unknown on purpose:
//
//   - ACP steering depends on the agent's initialize handshake declaring the
//     steering extension (chatdriver/acp/driver.go:944). No amount of reading tells
//     us what claude-agent-acp, omp, or dsh will advertise, so steer is unknown for
//     all three ACP harnesses.
//   - Resume is unknown for providers whose session/load versus session/resume
//     choice is decided at handshake time.
//
// Nothing in this file may be keyed on anything except the harness constant, and
// nothing here may be reachable from a decision path. TestStaticProfilesNeverGate
// enforces the second half.

// staticProfiles is the display catalog. Callers receive copies; the map is not
// exported so no package can bypass Resolver.
var staticProfiles = map[domain.AgentHarness]func() domain.HarnessCapabilitySnapshot{
	// Codex: native app-server JSON-RPC. Guidance into a running turn is a
	// first-class protocol method (chatdriver/codexappserver/steer.go:62), and the
	// driver advertises it unconditionally (driver.go:160) rather than from a
	// handshake, which is why steer is the one place a static read can say
	// "supported" — and even that is still refused at Gate because the source is
	// static, not because the read is doubtful.
	domain.HarnessCodex: func() domain.HarnessCapabilitySnapshot {
		return domain.HarnessCapabilitySnapshot{
			Harness:   domain.HarnessCodex,
			Surfaces:  domain.CapabilitySurfaceChat,
			Streaming: domain.CapabilitySupported,
			Tools:     domain.CapabilitySupported,
			Interrupt: domain.CapabilitySupported,
			Steer:     domain.CapabilitySupported,
			// Queue is AO's own durable queue (service/chat/controller.go:1512),
			// not a provider feature, but a queued message still needs a target
			// session to land in.
			Queue:    domain.CapabilityStateUnknown,
			Resume:   domain.CapabilityResumeWithHistory,
			Approval: domain.CapabilityApprovalEnforced,
			// Codex approval and sandbox are turn-scoped request fields.
			ApplyAt: domain.CapabilityApplyAtTurn,
			Usage:   domain.CapabilityUsageExact,
		}
	},
	// Claude Code: ACP over a packaged node runtime. Every capability that ACP
	// negotiates stays unknown until the handshake answers.
	domain.HarnessClaudeCode: func() domain.HarnessCapabilitySnapshot {
		return domain.HarnessCapabilitySnapshot{
			Harness:   domain.HarnessClaudeCode,
			Surfaces:  domain.CapabilitySurfaceChat,
			Streaming: domain.CapabilitySupported,
			Tools:     domain.CapabilitySupported,
			Interrupt: domain.CapabilitySupported,
			Steer:     domain.CapabilityStateUnknown,
			Queue:     domain.CapabilityStateUnknown,
			Resume:    domain.CapabilityResumeUnknown,
			// Only a permission-mode string, with no CLI-level filesystem fence
			// (pkg/agentruntime/command.go:170): advertised, not enforced.
			Approval: domain.CapabilityApprovalAdvertisedOnly,
			ApplyAt:  domain.CapabilityApplyAtLaunch,
			Usage:    domain.CapabilityUsageExact,
		}
	},
	// OMP: ACP. Approvals are fixed by the launch flag and refuse a live change at
	// turn time (chatdriver/ompacp/driver.go:64-69), so ApplyAt is launch.
	domain.HarnessOMP: func() domain.HarnessCapabilitySnapshot {
		return domain.HarnessCapabilitySnapshot{
			Harness:   domain.HarnessOMP,
			Surfaces:  domain.CapabilitySurfaceChat,
			Streaming: domain.CapabilitySupported,
			Tools:     domain.CapabilitySupported,
			Interrupt: domain.CapabilitySupported,
			Steer:     domain.CapabilityStateUnknown,
			Queue:     domain.CapabilityStateUnknown,
			Resume:    domain.CapabilityResumeUnknown,
			Approval:  domain.CapabilityApprovalEnforced,
			ApplyAt:   domain.CapabilityApplyAtLaunch,
			// OMP declares usage, but whether a given turn fills it depends on the
			// agent, so a static read cannot promise exact accounting.
			Usage: domain.CapabilityUsageUnknown,
		}
	},
	// DeepSeekRuntime: ACP via `dsh --profile acp`. It declares neither usage nor
	// steering, and permissions are decided per request.
	domain.HarnessDeepSeek: func() domain.HarnessCapabilitySnapshot {
		return domain.HarnessCapabilitySnapshot{
			Harness:   domain.HarnessDeepSeek,
			Surfaces:  domain.CapabilitySurfaceChat,
			Streaming: domain.CapabilitySupported,
			Tools:     domain.CapabilitySupported,
			Interrupt: domain.CapabilitySupported,
			Steer:     domain.CapabilityStateUnknown,
			Queue:     domain.CapabilityStateUnknown,
			Resume:    domain.CapabilityResumeUnknown,
			Approval:  domain.CapabilityApprovalEnforced,
			ApplyAt:   domain.CapabilityApplyAtRequest,
			Usage:     domain.CapabilityUsageUnknown,
		}
	},
}

// StaticProfile returns the display-only profile for a harness, stamped at the
// current wall clock. ok is false for a harness AO has no source reading for, so a
// caller cannot mistake "no profile" for "no capabilities".
func StaticProfile(harness domain.AgentHarness) (domain.HarnessCapabilitySnapshot, bool) {
	return StaticProfileAt(harness, time.Now(), DefaultTTL)
}

// StaticProfileAt is StaticProfile with an injected clock, so the resolver and its
// tests stay deterministic. The returned snapshot is still static and therefore
// cannot gate.
func StaticProfileAt(
	harness domain.AgentHarness,
	now time.Time,
	ttl time.Duration,
) (domain.HarnessCapabilitySnapshot, bool) {
	build, ok := staticProfiles[harness]
	if !ok {
		return domain.HarnessCapabilitySnapshot{}, false
	}
	snapshot := build()
	snapshot.Source = domain.CapabilitySourceStatic
	snapshot.ObservedAt = now.UTC()
	snapshot.ExpiresAt = snapshot.ObservedAt.Add(ttl)
	snapshot.AuthState = domain.AgentAuthenticationUnknown
	snapshot.Readiness = domain.CapabilityUnknown
	snapshot.ReasonCode = ports.CapabilityReasonStaticOnly
	return snapshot, true
}

// SupportedHarnesses lists every harness with a static profile, in the canonical
// AllHarnesses order so a client renders a stable table.
func SupportedHarnesses() []domain.AgentHarness {
	harnesses := make([]domain.AgentHarness, 0, len(staticProfiles))
	for _, harness := range domain.AllHarnesses {
		if _, ok := staticProfiles[harness]; ok {
			harnesses = append(harnesses, harness)
		}
	}
	return harnesses
}
