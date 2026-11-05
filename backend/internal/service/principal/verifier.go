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
//     it proves for the browser tunnel, and no more. Attempt identity additionally comes from
//     that session's durable binding and current server-held lease and scheduler admission.
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

// AttemptIdentityReader 是 Session 绑定身份所需的持久读取闭集，SQLite Store 已实现。
type AttemptIdentityReader interface {
	AttemptReader
	GetWorkflowAttemptBindingBySession(context.Context, domain.SessionID) (domain.WorkflowAttemptBinding, bool, error)
	ListSchedulerAdmissions(context.Context, bool) ([]domain.SchedulerAdmission, error)
	GetSchedulerLeader(context.Context) (domain.SchedulerLeader, error)
}

// StoreSessionAttemptProver 从 Session 唯一绑定读取执行许可；不接收外部 lease 或 Attempt。
// 读取检查不能替代写事务栅栏，因此返回完整快照供副作用所有者再次 CAS。
type StoreSessionAttemptProver struct {
	Store AttemptIdentityReader
	// Clock 用于两种租约期限比较；nil 使用 time.Now。
	Clock func() time.Time
}

// ProveSessionAttempt 只允许已恢复的活跃绑定；不确定、缺事实和读取失败均不能授予 Attempt。
func (p StoreSessionAttemptProver) ProveSessionAttempt(
	ctx context.Context,
	session domain.SessionRecord,
) (domain.ExecutionPermit, bool, error) {
	if p.Store == nil || session.ID == "" || session.IsTerminated {
		return domain.ExecutionPermit{}, false, ErrLeaseUnproven
	}
	binding, found, err := p.Store.GetWorkflowAttemptBindingBySession(ctx, session.ID)
	if err != nil || !found {
		return domain.ExecutionPermit{}, false, err
	}
	if binding.SessionID == nil || *binding.SessionID != session.ID ||
		binding.AttemptID == "" || binding.WorkflowID == "" || binding.TaskID == "" ||
		strings.TrimSpace(binding.ControllerGeneration) == "" || binding.ControllerGeneration != session.Metadata.ControllerGeneration ||
		(binding.State != domain.WorkflowBindingStateBoundReady && binding.State != domain.WorkflowBindingStateRunning) {
		return domain.ExecutionPermit{}, false, ErrLeaseUnproven
	}
	attempt, found, err := p.Store.GetWorkflowAttempt(ctx, binding.AttemptID)
	if err != nil {
		return domain.ExecutionPermit{}, false, err
	}
	if !found || attempt.ID != binding.AttemptID || attempt.WorkflowID != binding.WorkflowID || attempt.TaskID != binding.TaskID ||
		attempt.SessionID == nil || *attempt.SessionID != session.ID || strings.TrimSpace(attempt.WorkerID) == "" ||
		(attempt.State != domain.WorkflowAttemptStateClaimed && attempt.State != domain.WorkflowAttemptStateRunning) ||
		attempt.LeaseToken == "" || attempt.LeaseUntil == nil {
		return domain.ExecutionPermit{}, false, ErrLeaseUnproven
	}
	clock := time.Now
	if p.Clock != nil {
		clock = p.Clock
	}
	admissions, err := p.Store.ListSchedulerAdmissions(ctx, true)
	if err != nil {
		return domain.ExecutionPermit{}, false, err
	}
	var admission domain.SchedulerAdmission
	for _, candidate := range admissions {
		if candidate.AttemptID != attempt.ID {
			continue
		}
		if admission.ID != "" || candidate.ID == "" || candidate.State != domain.AdmissionActive ||
			candidate.WorkflowID != binding.WorkflowID || candidate.TaskID != binding.TaskID || candidate.SessionID != session.ID {
			return domain.ExecutionPermit{}, false, ErrLeaseUnproven
		}
		admission = candidate
	}
	if admission.ID == "" {
		return domain.ExecutionPermit{}, false, ErrLeaseUnproven
	}
	leader, err := p.Store.GetSchedulerLeader(ctx)
	if err != nil {
		return domain.ExecutionPermit{}, false, err
	}
	now := clock().UTC()
	if !attempt.LeaseUntil.After(now) || !leader.Recovered || !leader.LeaseUntil.After(now) ||
		leader.Fence.OwnerID == "" || leader.Fence.Epoch < 1 || leader.Fence != admission.Fence {
		return domain.ExecutionPermit{}, false, ErrLeaseUnproven
	}
	return domain.ExecutionPermit{
		AdmissionID: admission.ID, AdmissionRevision: admission.Revision, Fence: admission.Fence,
		AttemptID: attempt.ID, WorkflowID: attempt.WorkflowID, TaskID: attempt.TaskID, WorkerID: attempt.WorkerID,
		LeaseToken: attempt.LeaseToken, LeaseUntil: *attempt.LeaseUntil, AttemptUpdatedAt: attempt.UpdatedAt, AttemptState: attempt.State,
		SessionID: session.ID, BindingRevision: binding.Revision, Generation: binding.ControllerGeneration,
	}, true, nil
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
	attempts AttemptIdentityReader,
	authority CapabilityAuthority,
) *Resolver {
	return NewResolver(
		sessions,
		CapabilityVerifier{Sessions: sessions, Authority: authority},
		nil,
	).WithSessionAttemptProver(StoreSessionAttemptProver{Store: attempts})
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
	attempts AttemptIdentityReader,
	authority CapabilityAuthority,
	operator OperatorTrust,
) *Resolver {
	return StoreResolver(sessions, attempts, authority).WithOperatorTrust(operator)
}
