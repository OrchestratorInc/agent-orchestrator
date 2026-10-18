package httpd

import "sync"

// Readiness is the daemon's self-reported readiness state. It starts ready
// and can be marked degraded with a reason; /readyz reflects it so a daemon
// whose core dependency failed at startup does not keep reporting ready.
//
// The daemon wires this in at startup: a failed agent-catalog refresh (the
// preflight dependency of ao spawn) marks it degraded instead of being
// swallowed into a WARN that nothing else ever reads.
type Readiness struct {
	mu     sync.Mutex
	status string // "ready" | "degraded"
	reason string
}

// NewReadiness returns a readiness state that starts ready.
func NewReadiness() *Readiness {
	return &Readiness{status: "ready"}
}

// SetDegraded marks the daemon degraded with a human-readable reason. It is
// idempotent: the first reason wins, so a later transient failure does not
// overwrite the original cause.
func (r *Readiness) SetDegraded(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == "degraded" {
		return
	}
	r.status = "degraded"
	r.reason = reason
}

// Snapshot returns the current status and reason (empty when ready).
func (r *Readiness) Snapshot() (status, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, r.reason
}
