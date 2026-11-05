// Package principal 从 Session capability 及服务端执行事实派生可信调用方。
// Agent 无权提交内部租约；无效 Agent 凭据不能回退为 Operator。
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

// HeaderAttemptLease 仅用于识别并拒绝升级前的凭据协议。
const HeaderAttemptLease = "X-AO-Attempt-Lease"

// HeaderSessionID 选择待验证 capability 的 Session，本身不授予权限。
const HeaderSessionID = "X-AO-Session-Id"

// HeaderAttemptID 仅约束服务端已推导的身份，不能选择或冒领其他 Attempt。
const HeaderAttemptID = "X-AO-Attempt-Id"

// CodeSecurityUpgradeRequired 告知旧 MCP 需要安全重附着，不返回任何凭据。
const CodeSecurityUpgradeRequired = "WORKFLOW_SECURITY_UPGRADE_REQUIRED"

// Caller is the trusted identity a handler derived from the request. It is never built from a
// body field.
type Caller struct {
	Kind      Kind
	SessionID domain.SessionID // set for KindAgent
	// 身份来自该 Session 的唯一 binding，而非外部 Attempt 标识。
	AttemptID  domain.WorkflowAttemptID
	WorkflowID domain.WorkflowID
	TaskID     domain.WorkflowTaskID
	// LeaseProven 表示服务端已核验活跃租约、绑定与当前调度任期。
	LeaseProven bool
	ProjectID   domain.ProjectID
	// ExecutionPermit 仅在 daemon 内交给写事务做 CAS，不能序列化到响应。
	ExecutionPermit *domain.ExecutionPermit `json:"-"`
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
	// ErrLeaseUnproven 拒绝缺失或过期的服务端执行身份。
	ErrLeaseUnproven = errors.New("workflow attempt lease was not proven")
	// ErrSecurityUpgradeRequired 拒绝旧协议及新旧凭据混用。
	ErrSecurityUpgradeRequired = errors.New("旧 Workflow lease 凭据已停用；请通过安全重附着更新 MCP")
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

// SessionAttemptProver 只从已验证的 Session 读取内部执行身份；false 表示没有绑定。
type SessionAttemptProver interface {
	ProveSessionAttempt(ctx context.Context, session domain.SessionRecord) (domain.ExecutionPermit, bool, error)
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
	attempts  SessionAttemptProver
	operators OperatorTrust
}

// NewResolver 组装身份边界；缺失 capability 或执行事实验证器时拒绝 Agent。
func NewResolver(sessions SessionReader, verifier Verifier, trust OperatorTrust) *Resolver {
	return &Resolver{sessions: sessions, verifier: verifier, operators: trust}
}

// WithSessionAttemptProver 注入从持久绑定派生执行身份的验证器。
func (r *Resolver) WithSessionAttemptProver(prover SessionAttemptProver) *Resolver {
	r.attempts = prover
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
	// LeaseToken 仅保留供旧调用方得到明确升级拒绝；不得再作为凭据使用。
	LeaseToken string
}

// FromRequest 为已有 transport 调用方保留入口，权限规则与 Resolve 完全一致。
func (r *Resolver) FromRequest(
	req *http.Request,
	agentSessionID domain.SessionID,
) (Caller, error) {
	return r.Resolve(context.Background(), RequestFacts{Request: req, AgentSessionID: agentSessionID})
}

// Resolve 在消费任何审批前拒绝旧凭据；capability 验证成功后才读取 Session 的绑定。
func (r *Resolver) Resolve(ctx context.Context, facts RequestFacts) (Caller, error) {
	req := facts.Request
	if req == nil {
		return Caller{}, ErrNotAuthorized
	}
	if facts.LeaseToken != "" || hasHeader(req, HeaderAttemptLease) || hasHeader(req, "X-AO-Workflow-Lease") {
		return Caller{}, ErrSecurityUpgradeRequired
	}
	if id := domain.SessionID(strings.TrimSpace(req.Header.Get(HeaderSessionID))); id != "" {
		if facts.AgentSessionID != "" && facts.AgentSessionID != id {
			return Caller{}, ErrCapabilityInvalid
		}
		facts.AgentSessionID = id
	}
	if id := domain.WorkflowAttemptID(strings.TrimSpace(req.Header.Get(HeaderAttemptID))); id != "" {
		if facts.AttemptID != "" && facts.AttemptID != id {
			return Caller{}, ErrLeaseUnproven
		}
		facts.AttemptID = id
	}
	if hasHeader(req, HeaderAgentCapability) {
		if facts.AgentSessionID == "" {
			return Caller{}, ErrNotAuthorized
		}
		return r.agent(ctx, facts, strings.TrimSpace(req.Header.Get(HeaderAgentCapability)))
	}
	if hasHeader(req, HeaderSessionID) {
		return Caller{}, ErrCapabilityInvalid
	}
	if facts.AttemptID != "" {
		return Caller{}, ErrLeaseUnproven
	}
	return r.operator(req)
}

// hasHeader 连空值和非标准大小写也视为协议声明，避免无效 Agent 降级为 Operator。
func hasHeader(req *http.Request, name string) bool {
	for key := range req.Header {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
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

// agent 先证明 Session，再核验内部租约与任期；已有坏绑定不能降级为 Session 权限。
func (r *Resolver) agent(ctx context.Context, facts RequestFacts, token string) (Caller, error) {
	if r.sessions == nil || r.verifier == nil {
		return Caller{}, ErrCapabilityInvalid
	}
	session, found, err := r.sessions.GetSession(ctx, facts.AgentSessionID)
	if err != nil {
		return Caller{}, err
	}
	if !found || session.ID != facts.AgentSessionID || session.IsTerminated || session.Metadata.BrowserCapabilityVerifier == "" || token == "" {
		return Caller{}, ErrCapabilityInvalid
	}
	valid, err := r.verifier.VerifyAgentCapability(ctx, facts.AgentSessionID, token)
	if err != nil {
		return Caller{}, err
	}
	if !valid {
		return Caller{}, ErrCapabilityInvalid
	}
	current, found, err := r.sessions.GetSession(ctx, facts.AgentSessionID)
	if err != nil {
		return Caller{}, err
	}
	// capability 验证期间可能轮换 Controller，不能把新凭据和旧 generation 拼成身份。
	if !found || current.ID != session.ID || current.IsTerminated || current.ProjectID != session.ProjectID ||
		current.Metadata.BrowserCapabilityVerifier != session.Metadata.BrowserCapabilityVerifier ||
		current.Metadata.ControllerGeneration != session.Metadata.ControllerGeneration {
		return Caller{}, ErrCapabilityInvalid
	}
	caller := Caller{
		Kind:      KindAgent,
		SessionID: facts.AgentSessionID,
		ProjectID: session.ProjectID,
	}
	if r.attempts == nil {
		return Caller{}, ErrLeaseUnproven
	}
	permit, bound, err := r.attempts.ProveSessionAttempt(ctx, session)
	if err != nil {
		return Caller{}, err
	}
	if !bound {
		if facts.WorkflowID != "" || facts.AttemptID != "" || facts.RouteAttemptID != "" {
			return Caller{}, ErrLeaseUnproven
		}
		return caller, nil
	}
	if (facts.WorkflowID != "" && facts.WorkflowID != permit.WorkflowID) ||
		(facts.AttemptID != "" && facts.AttemptID != permit.AttemptID) {
		return Caller{}, ErrLeaseUnproven
	}
	caller.AttemptID = permit.AttemptID
	caller.WorkflowID = permit.WorkflowID
	caller.TaskID = permit.TaskID
	caller.LeaseProven = true
	caller.ExecutionPermit = &permit
	return caller, nil
}
