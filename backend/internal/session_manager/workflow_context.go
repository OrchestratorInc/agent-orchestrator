package sessionmanager

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// workflowAttemptBindingStore is the narrow durable surface a RESUMED session needs
// to recover which workflow attempt it belongs to.
//
// It is a local optional interface rather than a widening of Store: only a store that
// actually owns workflow bindings can answer it, and a deployment without M2 wiring
// keeps working with no binding at all.
type workflowAttemptBindingStore interface {
	GetWorkflowAttemptBindingBySession(
		ctx context.Context,
		sessionID domain.SessionID,
	) (domain.WorkflowAttemptBinding, bool, error)
}

// workflowAttemptLeaseStore is the narrow durable surface that holds the attempt's
// lease token.
//
// It is separate from the binding store on purpose: the binding deliberately never
// carries the lease (its type has no such field), so recovering one is a second,
// explicitly named read.
type workflowAttemptLeaseStore interface {
	GetWorkflowAttempt(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (domain.WorkflowAttempt, bool, error)
}

// workflowSpawnContext reconstructs the workflow identity of an EXISTING session from
// durable state, for a resume after a daemon restart.
//
// It returns nil whenever the identity cannot be proven, which is the ordinary case
// for every non-workflow session: nothing about those sessions changes.
//
// The rules, in order:
//
//  1. The session must have a bound attempt (binding state that must name a session)
//     with a non-empty workflow and task id. A binding in a pre-session state names
//     no attempt-session pair and yields nil.
//  2. The attempt's lease is included ONLY while it is still valid, using the same
//     predicate the daemon's verifier applies (a present token, a present expiry, and
//     an expiry in the future). An EXPIRED lease yields an empty LeaseToken: the tool
//     server then presents no dead credential and the daemon answers its own clean
//     403 instead of rejecting a stale token.
//  3. Nothing here is logged. The lease never enters a log line, an error, or a
//     prompt.
//
// Errors are returned rather than swallowed so a caller can decide; a resume that
// cannot read its own durable facts should say so instead of silently downgrading to
// "not a workflow attempt".
func (m *Manager) workflowSpawnContext(
	ctx context.Context,
	rec domain.SessionRecord,
) (*ports.WorkflowSpawnContext, error) {
	bindings, ok := m.store.(workflowAttemptBindingStore)
	if !ok {
		return nil, nil
	}
	binding, found, err := bindings.GetWorkflowAttemptBindingBySession(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	if !found || !binding.State.HasSession() {
		return nil, nil
	}
	if strings.TrimSpace(string(binding.WorkflowID)) == "" ||
		strings.TrimSpace(string(binding.TaskID)) == "" ||
		strings.TrimSpace(string(binding.AttemptID)) == "" {
		return nil, nil
	}
	workflow := &ports.WorkflowSpawnContext{
		WorkflowID: binding.WorkflowID,
		TaskID:     binding.TaskID,
		AttemptID:  binding.AttemptID,
	}
	leases, ok := m.store.(workflowAttemptLeaseStore)
	if !ok {
		return workflow, nil
	}
	attempt, found, err := leases.GetWorkflowAttempt(ctx, binding.AttemptID)
	if err != nil {
		return nil, err
	}
	if !found {
		// The binding names an attempt that no longer exists. The identity is still
		// reportable (the ids are durable on the binding) but no lease can be proven.
		return workflow, nil
	}
	if workflowLeaseValid(attempt, m.clock()) {
		workflow.LeaseToken = attempt.LeaseToken
	}
	return workflow, nil
}

// workflowLeaseValid reports whether an attempt's lease may still be presented.
//
// It mirrors principal's verifier exactly: an empty token, a nil expiry, or an expiry
// that is not in the future is not a usable lease. Keeping the same predicate here is
// what stops this path from handing the tool server a credential the daemon will
// reject.
func workflowLeaseValid(attempt domain.WorkflowAttempt, now time.Time) bool {
	if strings.TrimSpace(attempt.LeaseToken) == "" || attempt.LeaseUntil == nil {
		return false
	}
	return attempt.LeaseUntil.After(now.UTC())
}
