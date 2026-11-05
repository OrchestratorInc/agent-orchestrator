// Package principal 为 M2 路由派生可信调用方。
// Agent 必须先证明 session capability 与 attempt lease；无效 Agent 凭据不能回退为 Operator。
// 正式 Operator 只接受 OS 人工确认后、绑定具体 RequestURI 和正文的单次审批。
// 回环地址、可读取的 token 和请求正文都不能独立证明人工授权。
package principal

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Kind is who a request acts as.
type Kind string

const (
	// KindOperator is the desktop app / a human at this machine.
	KindOperator Kind = "operator"
	// KindAgent is a worker process AO launched.
	KindAgent Kind = "agent"
)

// HeaderAgentCapability carries the per-session worker capability. A worker process receives
// the raw token in its environment; the daemon stores only the verifier.
const HeaderAgentCapability = "X-AO-Agent-Capability"

// HeaderAttemptLease carries the workflow lease token an agent proves its attempt with. It is
// a header, never a body field, so a body cannot impersonate an attempt.
const HeaderAttemptLease = "X-AO-Attempt-Lease"

// HeaderAttemptID names the attempt an agent claims on a route whose path carries no attempt
// segment (the workflow-scoped message and task-request routes). It is a transport fact, not a
// body field, and it proves nothing on its own: the lease proof below is what makes it identity.
const HeaderAttemptID = "X-AO-Attempt-Id"

// Caller is the trusted identity a handler derived from the request. It is never built from a
// body field.
type Caller struct {
	Kind      Kind
	SessionID domain.SessionID // set for KindAgent
	// AttemptID is set once the caller proved it holds this attempt's lease. It is never the
	// attempt a URL happens to name: only a lease makes an attempt an identity.
	AttemptID domain.WorkflowAttemptID
	// LeaseProven reports whether AttemptID was proven rather than merely named.
	LeaseProven bool
	ProjectID   domain.ProjectID
	// OperatorTokenFingerprint 保留审计字段名；生产审批记录 OS 主体标识，Agent 为空。
	OperatorTokenFingerprint string
}

// Ref is the audit-safe reference for this caller: "operator:<kind>" or "agent:<session>".
// It is always derived, never accepted from a request.
func (c Caller) Ref() string {
	switch c.Kind {
	case KindOperator:
		return "operator:" + string(KindOperator)
	case KindAgent:
		return "agent:" + string(c.SessionID)
	default:
		return ""
	}
}

// IsAgent reports whether the caller is a worker process.
func (c Caller) IsAgent() bool { return c.Kind == KindAgent }

// IsOperator reports whether the caller is the desktop app / local human.
func (c Caller) IsOperator() bool { return c.Kind == KindOperator }

// Errors are the typed refusals. A handler maps them onto 403 COLLAB_NOT_AUTHORIZED; they are
// distinct so the reason is diagnosable without telling a caller which check it failed in a way
// that would help it forge the next one.
var (
	// ErrNotAuthorized refuses a caller that proved nothing.
	ErrNotAuthorized = errors.New("caller is not authorized")
	// ErrCapabilityInvalid refuses an agent capability that does not match the session.
	ErrCapabilityInvalid = errors.New("agent capability is invalid")
	// ErrLeaseUnproven refuses an attempt claim with no lease proof.
	ErrLeaseUnproven = errors.New("workflow attempt lease was not proven")
)

// Verifier checks an agent capability against the session's stored verifier.
type Verifier interface {
	// VerifyAgentCapability reports whether token is the capability issued for sessionID.
	VerifyAgentCapability(ctx context.Context, sessionID domain.SessionID, token string) (bool, error)
}

// SessionReader reads the durable session row, which carries the capability verifier and the
// project the session belongs to.
type SessionReader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// OperatorTrust reports whether transport facts prove the request came from the operator.
type OperatorTrust interface {
	PermitsOperator(req *http.Request) bool
}

// LeaseProver checks a workflow lease token against the attempt it names.
type LeaseProver interface {
	ProveWorkflowAttemptLease(
		ctx context.Context,
		workflowID domain.WorkflowID,
		attemptID domain.WorkflowAttemptID,
		token string,
	) (bool, error)
}

// LoopbackOperatorTrust permits a request whose peer address is a loopback IP — nothing more.
//
// # It is NOT the operator, and it is NOT the production default
//
// The name says exactly what it checks and no more: the peer address is loopback. Every AO worker
// process, every shell inside a session, and every other local program shares that address, so
// this proves "some process on this machine", never "the human" and never "the desktop app".
// Treating it as the operator would let a worker approve its own task request, resolve its own
// attempt, or bind its own session.
//
// 生产使用 ApprovalOperatorTrust；该类型只保留给需要显式交通边界的旧 fixture。
type LoopbackOperatorTrust struct{}

// PermitsOperator reports whether req's peer address is a loopback IP.
func (LoopbackOperatorTrust) PermitsOperator(req *http.Request) bool {
	return LoopbackPeer(req)
}

// Resolver derives the caller from transport facts.
type Resolver struct {
	sessions  SessionReader
	verifier  Verifier
	lease     LeaseProver
	operators OperatorTrust
}

// NewResolver builds a resolver. verifier and lease may be nil, in which case an agent request
// is always refused: a missing verifier is not permission.
func NewResolver(sessions SessionReader, verifier Verifier, trust OperatorTrust) *Resolver {
	return &Resolver{sessions: sessions, verifier: verifier, operators: trust}
}

// WithLeaseProver attaches the lease proof required for an agent identity. Without it an agent
// request is refused.
func (r *Resolver) WithLeaseProver(prover LeaseProver) *Resolver {
	r.lease = prover
	return r
}

// RequestFacts are the transport facts a caller derivation reads. None of them is a body field.
type RequestFacts struct {
	Request        *http.Request
	AgentSessionID domain.SessionID
	WorkflowID     domain.WorkflowID
	// AttemptID is the attempt the caller CLAIMS to be. It is never routing: an attempt id in
	// the URL is RouteAttemptID below, because naming the resource a request is about must not
	// confer the identity of that resource's agent.
	AttemptID domain.WorkflowAttemptID
	// RouteAttemptID is the attempt the request is scoped to, from the path. The resolver never
	// treats it as identity; it is here so a caller can express both facts at once.
	RouteAttemptID domain.WorkflowAttemptID
	// LeaseToken is the workflow lease capability, from HeaderAttemptLease only.
	LeaseToken string
}

// FromRequest derives the caller from transport facts only.
//
//   - A request carrying HeaderAgentCapability plus a session id resolves as KindAgent ONLY
//     when the stored verifier accepts the token; otherwise it is an authorization error, never
//     a silent downgrade to operator.
//   - A request with no capability header resolves as KindOperator ONLY when
//     trust.PermitsOperator(r) holds (loopback peer, or a request the desktop app
//     authenticated). Otherwise it is refused.
func (r *Resolver) FromRequest(
	req *http.Request,
	agentSessionID domain.SessionID,
) (Caller, error) {
	return r.Resolve(context.Background(), RequestFacts{Request: req, AgentSessionID: agentSessionID})
}

// Resolve derives the caller, including the attempt lease when one is supplied.
//
// The order is deliberate: the cheap transport check decides whether the caller is an agent at
// all, and only then are the durable checks (capability, then lease) performed. An attempt id
// supplied without any proof is refused rather than ignored — silently dropping it would let a
// caller believe it spoke as an agent.
func (r *Resolver) Resolve(ctx context.Context, facts RequestFacts) (Caller, error) {
	req := facts.Request
	if req == nil {
		return Caller{}, ErrNotAuthorized
	}
	if facts.AgentSessionID == "" {
		// No session id in the URL: the caller cannot be a worker, so a capability header here
		// is a claim with nothing to check it against.
		if strings.TrimSpace(req.Header.Get(HeaderAgentCapability)) != "" {
			return Caller{}, ErrNotAuthorized
		}
		caller, err := r.operator(req)
		if err != nil {
			return Caller{}, err
		}
		if facts.AttemptID != "" || strings.TrimSpace(facts.LeaseToken) != "" {
			// An operator may act on an attempt through the established M1 surfaces, but this
			// resolver does not mint an attempt identity from a request that claimed one.
			return Caller{}, ErrLeaseUnproven
		}
		return caller, nil
	}

	token := strings.TrimSpace(req.Header.Get(HeaderAgentCapability))
	if token == "" {
		// No capability: the operator path. An agent session id in the URL does not turn an
		// operator into an agent, and an attempt claim without proof is still refused below.
		caller, err := r.operator(req)
		if err != nil {
			return Caller{}, err
		}
		if facts.AttemptID != "" || strings.TrimSpace(facts.LeaseToken) != "" {
			return Caller{}, ErrLeaseUnproven
		}
		return caller, nil
	}

	caller, err := r.agent(ctx, facts, token)
	if err != nil {
		return Caller{}, err
	}
	return caller, nil
}

// operator 保留自定义 trust 的测试兼容入口；生产 ApprovalOperatorTrust 的布尔路径永远拒绝。
// 正式授权必须消费审批，并将 OS 确认主体作为审计标识；拒绝原因原样返回给 HTTP 层。
func (r *Resolver) operator(req *http.Request) (Caller, error) {
	if r.operators == nil {
		return Caller{}, ErrNotAuthorized
	}
	if r.operators.PermitsOperator(req) {
		caller := Caller{Kind: KindOperator}
		// The fingerprint is derived from whatever token the request presented, and only after
		// the trust accepted the request as an operator. An empty fingerprint means the trust
		// did not use a token (a test double), not that a credential was skipped.
		if token := strings.TrimSpace(req.Header.Get(HeaderOperatorToken)); token != "" {
			caller.OperatorTokenFingerprint = OperatorTokenFingerprint(token)
		}
		return caller, nil
	}
	prover, ok := r.operators.(ApprovalProver)
	if !ok {
		return Caller{}, ErrNotAuthorized
	}
	// A request with no approval header is "no proof at all" — the same refusal as before this
	// proof existed. Only a present approval is evaluated (and spent), so the refusal a caller
	// sees for missing-everything stays COLLAB_NOT_AUTHORIZED while a present-but-unusable
	// approval gets the specific OPERATOR_APPROVAL_* code.
	if strings.TrimSpace(req.Header.Get(HeaderOperatorApproval)) == "" {
		return Caller{}, ErrNotAuthorized
	}
	fingerprint, err := prover.ProveApproval(req)
	if err != nil {
		return Caller{}, err
	}
	// The fingerprint recorded is the CONFIRMING credential's: an approval-authorised decision
	// is audited against the human gesture that released it, not against the requesting
	// process, which proved nothing.
	return Caller{Kind: KindOperator, OperatorTokenFingerprint: fingerprint}, nil
}

// agent proves the capability and then the attempt lease.
func (r *Resolver) agent(ctx context.Context, facts RequestFacts, token string) (Caller, error) {
	if r.sessions == nil || r.verifier == nil {
		return Caller{}, ErrCapabilityInvalid
	}
	session, found, err := r.sessions.GetSession(ctx, facts.AgentSessionID)
	if err != nil {
		return Caller{}, err
	}
	if !found || session.IsTerminated || session.Metadata.BrowserCapabilityVerifier == "" {
		return Caller{}, ErrCapabilityInvalid
	}
	valid, err := r.verifier.VerifyAgentCapability(ctx, facts.AgentSessionID, token)
	if err != nil {
		return Caller{}, err
	}
	if !valid {
		return Caller{}, ErrCapabilityInvalid
	}
	caller := Caller{
		Kind:      KindAgent,
		SessionID: facts.AgentSessionID,
		ProjectID: session.ProjectID,
	}
	if facts.AttemptID == "" {
		// A capability with no attempt claim is a legitimate session-scoped agent (a worker
		// asking to read its own binding), just not an attempt identity.
		return caller, nil
	}
	if strings.TrimSpace(facts.LeaseToken) == "" || r.lease == nil {
		return Caller{}, ErrLeaseUnproven
	}
	proven, err := r.lease.ProveWorkflowAttemptLease(
		ctx, facts.WorkflowID, facts.AttemptID, facts.LeaseToken,
	)
	if err != nil {
		return Caller{}, err
	}
	if !proven {
		return Caller{}, ErrLeaseUnproven
	}
	caller.AttemptID = facts.AttemptID
	caller.LeaseProven = true
	return caller, nil
}
