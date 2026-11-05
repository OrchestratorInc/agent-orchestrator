package capability

import (
	"context"
	"errors"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// FakeProbe is a scripted CapabilityProbe. It exists so the resolver's evidence
// rules can be exercised without a provider, a binary, or a token, and so M2's
// offline tests have a deterministic handshake stand-in.
//
// It is not a production adapter and must never be registered on a live daemon:
// registering it would make every capability claim fabricated. Nothing in this
// repository wires it outside tests.
type FakeProbe struct {
	mu sync.Mutex
	// snapshots is the scripted answer per harness. A harness with no entry
	// returns ErrProbeNotScripted, which the resolver records as a failed probe
	// rather than as "unsupported".
	snapshots map[domain.AgentHarness]domain.HarnessCapabilitySnapshot
	errs      map[domain.AgentHarness]error
	calls     []ports.CapabilityProbeRequest
}

// ErrProbeNotScripted reports that a fake probe was asked about a harness it has no
// scripted answer for. It is an error, not a negative observation.
var ErrProbeNotScripted = errors.New("fake probe has no scripted answer for this harness")

// NewFakeProbe builds an empty fake probe. Use Script to add answers.
func NewFakeProbe() *FakeProbe {
	return &FakeProbe{
		snapshots: make(map[domain.AgentHarness]domain.HarnessCapabilitySnapshot),
		errs:      make(map[domain.AgentHarness]error),
	}
}

// Script installs the snapshot returned for a harness.
func (p *FakeProbe) Script(snapshot domain.HarnessCapabilitySnapshot) *FakeProbe {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshots[snapshot.Harness] = snapshot
	delete(p.errs, snapshot.Harness)
	return p
}

// ScriptError installs a failure for a harness, so a test can prove that a failed
// probe yields unknown rather than a fabricated negative.
func (p *FakeProbe) ScriptError(harness domain.AgentHarness, err error) *FakeProbe {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs[harness] = err
	delete(p.snapshots, harness)
	return p
}

// Calls returns the requests the probe received, in order.
func (p *FakeProbe) Calls() []ports.CapabilityProbeRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ports.CapabilityProbeRequest, len(p.calls))
	copy(out, p.calls)
	return out
}

// ProbeCapabilities implements ports.CapabilityProbe.
func (p *FakeProbe) ProbeCapabilities(
	ctx context.Context,
	request ports.CapabilityProbeRequest,
) (domain.HarnessCapabilitySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.HarnessCapabilitySnapshot{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, request)
	if err, ok := p.errs[request.Harness]; ok {
		return domain.HarnessCapabilitySnapshot{}, err
	}
	snapshot, ok := p.snapshots[request.Harness]
	if !ok {
		return domain.HarnessCapabilitySnapshot{}, ErrProbeNotScripted
	}
	// A session-scoped request must receive a session-scoped answer, and the fake
	// refuses to pretend otherwise: handing back a harness-scoped snapshot for a
	// session request would let a test pass on evidence the resolver is supposed
	// to reject.
	if request.SessionScoped() {
		if !snapshot.SessionScoped() {
			return domain.HarnessCapabilitySnapshot{}, ErrProbeNotScripted
		}
	} else if snapshot.SessionScoped() {
		return domain.HarnessCapabilitySnapshot{}, ErrProbeNotScripted
	}
	return snapshot, nil
}

// MemoryRecorder is an in-memory ports.CapabilityRecorder for tests and for a
// daemon that has not yet been given durable storage.
type MemoryRecorder struct {
	mu       sync.Mutex
	harness  map[domain.AgentHarness]domain.HarnessCapabilitySnapshot
	sessions map[domain.SessionID]domain.HarnessCapabilitySnapshot
}

// NewMemoryRecorder builds an empty recorder.
func NewMemoryRecorder() *MemoryRecorder {
	return &MemoryRecorder{
		harness:  make(map[domain.AgentHarness]domain.HarnessCapabilitySnapshot),
		sessions: make(map[domain.SessionID]domain.HarnessCapabilitySnapshot),
	}
}

// RecordCapabilities implements ports.CapabilityRecorder.
func (r *MemoryRecorder) RecordCapabilities(
	_ context.Context,
	snapshot domain.HarnessCapabilitySnapshot,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if snapshot.SessionScoped() {
		r.sessions[snapshot.ObservedSessionID] = snapshot
		return nil
	}
	r.harness[snapshot.Harness] = snapshot
	return nil
}

// LoadCapabilities implements ports.CapabilityRecorder.
func (r *MemoryRecorder) LoadCapabilities(
	_ context.Context,
	harness domain.AgentHarness,
) (domain.HarnessCapabilitySnapshot, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot, ok := r.harness[harness]
	return snapshot, ok, nil
}

// LoadSessionCapabilities implements ports.CapabilityRecorder.
func (r *MemoryRecorder) LoadSessionCapabilities(
	_ context.Context,
	sessionID domain.SessionID,
) (domain.HarnessCapabilitySnapshot, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot, ok := r.sessions[sessionID]
	return snapshot, ok, nil
}
