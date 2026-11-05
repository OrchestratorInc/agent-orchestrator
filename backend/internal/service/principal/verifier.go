package principal

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// CapabilityAuthority is the digest comparison the daemon already trusts for a per-session
// worker capability. It is the exact shape service/browser.Authority provides.
type CapabilityAuthority interface {
	Valid(sessionID domain.SessionID, token, verifier string) bool
}

// CapabilityVerifier checks a per-session agent capability against the verifier AO persisted.
//
// # Which token this checks, and why there is exactly one
//
// AO already mints one unguessable capability per worker launch, injects the raw token into the
// worker process, and persists only a one-way verifier bound to the session id
// (domain.SessionMetadata.BrowserCapabilityVerifier). The verifier column holds ONE digest, so a
// second, independently-minted "agent capability" could not be verified without a new column and a
// migration — and a token nobody could check would be a fiction.
//
// So the agent capability IS that capability. A worker presents the same token it was given at
// launch in HeaderAgentCapability; the daemon compares it against the stored verifier with the
// same authority implementation the browser tunnel uses. That means:
//
//   - one credential per session, rotated with the worker, persisted only as a digest;
//   - the digest is bound to the session id, so a verifier copied between rows cannot authorize
//     the copied-to session;
//   - holding the capability proves "a process inside this session", which is exactly as much as
//     it proves for the browser tunnel, and no more. Attempt identity additionally requires the
//     workflow lease (StoreLeaseProver), so a leaked capability alone cannot act as another task.
//
// Authority must not be nil: a verifier that cannot compare is not permission.
type CapabilityVerifier struct {
	// Sessions reads the durable verifier.
	Sessions SessionReader
	// Authority performs the constant-time comparison.
	Authority CapabilityAuthority
}

// VerifyAgentCapability reports whether token is the capability issued for sessionID.
func (v CapabilityVerifier) VerifyAgentCapability(
	ctx context.Context,
	sessionID domain.SessionID,
	token string,
) (bool, error) {
	if v.Sessions == nil || v.Authority == nil || sessionID == "" {
		return false, nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	session, found, err := v.Sessions.GetSession(ctx, sessionID)
	if err != nil {
		return false, err
	}
	// A terminated session's capability is dead even though its digest is still on the row.
	if !found || session.IsTerminated {
		return false, nil
	}
	stored := strings.TrimSpace(session.Metadata.BrowserCapabilityVerifier)
	if stored == "" {
		return false, nil
	}
	return v.Authority.Valid(sessionID, token, stored), nil
}

// AttemptReader reads one durable workflow attempt, including its lease token.
type AttemptReader interface {
	GetWorkflowAttempt(
		ctx context.Context,
		id domain.WorkflowAttemptID,
	) (domain.WorkflowAttempt, bool, error)
}

// StoreLeaseProver proves a lease against the durable attempt row.
//
// Every failure is a "not proven" answer rather than an error, because the caller cannot act on
// the difference: an unknown attempt, an attempt in another workflow, a cleared lease (a settled
// attempt) and an expired lease all mean the same thing to it — this caller does not hold the
// attempt. Only a read failure is an error.
type StoreLeaseProver struct {
	Attempts AttemptReader
	// Clock is the time source for the lease expiry comparison. Nil means time.Now.
	Clock func() time.Time
}

// ProveWorkflowAttemptLease reports whether token is the live lease of exactly that attempt.
func (p StoreLeaseProver) ProveWorkflowAttemptLease(
	ctx context.Context,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	token string,
) (bool, error) {
	if p.Attempts == nil || attemptID == "" || strings.TrimSpace(token) == "" {
		return false, nil
	}
	attempt, found, err := p.Attempts.GetWorkflowAttempt(ctx, attemptID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	// The workflow check is what makes a lease non-transferable across workflows: the same token
	// presented on another workflow's route proves nothing.
	if workflowID != "" && attempt.WorkflowID != workflowID {
		return false, nil
	}
	if attempt.LeaseToken == "" || attempt.LeaseUntil == nil {
		return false, nil
	}
	now := time.Now
	if p.Clock != nil {
		now = p.Clock
	}
	if !attempt.LeaseUntil.After(now().UTC()) {
		return false, nil
	}
	// Constant-time comparison is not the point here (both sides are server-held), so a plain
	// comparison is used deliberately: the lease is a random 32-byte hex string and the timing
	// channel would leak nothing an attacker could use without already holding it.
	return attempt.LeaseToken == token, nil
}

// StoreResolver builds the production resolver over the durable store.
//
// It is the wiring this package exists for: one call, every fact from the database. The daemon
// passes it to httpd.APIDeps.WorkflowPrincipals.
//
// The operator trust is NOT defaulted to a loopback check: callers must supply one with
// WithOperatorTrust. A resolver built without one refuses every operator request, which is the
// correct failure for an unwired identity check (see WithOperatorTrust and TokenOperatorTrust).
func StoreResolver(
	sessions SessionReader,
	attempts AttemptReader,
	authority CapabilityAuthority,
) *Resolver {
	return NewResolver(
		sessions,
		CapabilityVerifier{Sessions: sessions, Authority: authority},
		nil,
	).WithLeaseProver(StoreLeaseProver{Attempts: attempts})
}

// WithOperatorTrust installs the operator proof. Without one, every operator request is refused:
// a missing trust is not permission.
func (r *Resolver) WithOperatorTrust(trust OperatorTrust) *Resolver {
	r.operators = trust
	return r
}

// StoreResolverWithOperator is StoreResolver plus the operator proof, in one call.
//
// It exists so the daemon's wiring is a single expression that cannot accidentally omit the
// operator credential: the trust is a required argument rather than an optional setter.
func StoreResolverWithOperator(
	sessions SessionReader,
	attempts AttemptReader,
	authority CapabilityAuthority,
	operator OperatorTrust,
) *Resolver {
	return StoreResolver(sessions, attempts, authority).WithOperatorTrust(operator)
}
