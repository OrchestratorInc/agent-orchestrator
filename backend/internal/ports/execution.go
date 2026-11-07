package ports

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type executionPermitContextKey struct{}

type sessionReattachContextKey struct{}

// sessionReattachContext 只在内部传递观察许可和动态时钟；慢连接提交时必须重新判断任期是否到期。
type sessionReattachContext struct {
	permit domain.SessionReattachPermit
	clock  func() time.Time
}

// WithSessionReattachPermit 与业务 ExecutionPermit 分开，不能转换为 Agent 的执行授权。
func WithSessionReattachPermit(ctx context.Context, permit domain.SessionReattachPermit, clock func() time.Time) context.Context {
	return context.WithValue(ctx, sessionReattachContextKey{}, sessionReattachContext{permit: permit, clock: clock})
}

// SessionReattachPermitFromContext 返回现存 Provider 的重附着观察快照。
func SessionReattachPermitFromContext(ctx context.Context) (domain.SessionReattachPermit, bool) {
	v, ok := ctx.Value(sessionReattachContextKey{}).(sessionReattachContext)
	return v.permit, ok
}

// SessionReattachNow 在实际写事务中读取时间，不能把开始连接时的时间冻结为后续授权。
func SessionReattachNow(ctx context.Context) time.Time {
	if v, ok := ctx.Value(sessionReattachContextKey{}).(sessionReattachContext); ok && v.clock != nil {
		return v.clock().UTC()
	}
	return time.Now().UTC()
}

// WithExecutionPermit 只在 daemon 内部传递可信快照，不进入启动配置、环境或 HTTP。
func WithExecutionPermit(ctx context.Context, permit domain.ExecutionPermit) context.Context {
	return context.WithValue(ctx, executionPermitContextKey{}, permit)
}

// ExecutionPermitFromContext 读取由协调器或身份校验器持有的许可。
func ExecutionPermitFromContext(ctx context.Context) (domain.ExecutionPermit, bool) {
	p, ok := ctx.Value(executionPermitContextKey{}).(domain.ExecutionPermit)
	return p, ok
}

// ExecutionGateway is the thin boundary between the Workflow coordinator and AO's
// existing Session Manager and Chat service.
//
// It exists so the coordinator never launches a CLI, never parses a provider
// protocol, and never guesses whether an agent started. Every method here is an
// observation or a request to an owner that already existed before M2: the Session
// Manager owns session rows and workspaces, the Chat service owns turns and steering.
//
// Two rules shape the whole interface:
//
//  1. A returned observation describes facts AO actually holds. Readiness is a
//     three-valued fact, not a boolean: "provisioning" is not "ready", and "no
//     answer" is not "failed".
//  2. No struct in this file can carry a lease token or a credential. The workflow
//     lease stays in the Workflow domain, and the session record's own secrets never
//     cross this boundary.
//
// Implementations must never infer success from a process exit code, and must never
// treat a lost answer as a failure: a lost answer is ErrSpawnOutcomeUnknown.

// Execution errors. These are outcomes, not failures: a caller branching on them is
// deciding what is safe, not reporting a bug.
var (
	// ErrSpawnOutcomeUnknown means a spawn was issued and AO cannot establish
	// whether its side effect happened. Retrying under a new request id is exactly
	// the mistake this error exists to prevent.
	ErrSpawnOutcomeUnknown = errors.New("execution spawn outcome unknown")
	// ErrExecutionSessionNotFound means no session exists for the requested id.
	ErrExecutionSessionNotFound = errors.New("execution session not found")
	// ErrExecutionUnsupported means this gateway cannot perform the operation at
	// all. It is permanent for the implementation, not a transient failure.
	ErrExecutionUnsupported = errors.New("execution operation unsupported")
	// ErrExecutionRefused means the owner refused the request for a reason the
	// caller should surface (unknown project, harness unavailable, and similar).
	ErrExecutionRefused = errors.New("execution request refused")
	// ErrExecutionOutcomeUnknown means a message was issued and AO cannot establish
	// whether the provider received it. A caller must freeze rather than resend: a
	// resent steer lands twice, and a resent stop interrupts a second turn.
	ErrExecutionOutcomeUnknown = errors.New("execution message outcome unknown")
	// ErrExecutionStaleGeneration means the request names a controller generation
	// that no longer owns the session. It is a fence, not a failure: a controller
	// that was replaced may not keep acting on work it no longer owns.
	ErrExecutionStaleGeneration = errors.New("execution controller generation is stale")
	// ErrExecutionTerminated means the session is terminated. A terminated session
	// is durable history, never a live target.
	ErrExecutionTerminated = errors.New("execution session is terminated")
	// ErrExecutionTargetMismatch means the steer named a provider turn that is not
	// the turn currently in flight. Steering the wrong turn would put guidance on
	// work the caller was not looking at.
	ErrExecutionTargetMismatch = errors.New("execution target provider turn is not the running turn")
	// ErrExecutionSessionMismatch means the observation in hand describes a different
	// session than the one the caller asked about, so it cannot be used as evidence.
	ErrExecutionSessionMismatch = errors.New("execution observation describes a different session")
)

// SessionReadiness is how far a session has actually got. It exists because
// "a row exists" and "the provider is working" are different facts, and M2 must never
// report the first as the second.
type SessionReadiness string

const (
	// SessionReadinessUnknown means AO holds no readiness evidence. It is the zero
	// value on purpose: an observation that says nothing must not read as ready.
	SessionReadinessUnknown SessionReadiness = "unknown"
	// SessionReadinessProvisioning means the session row exists but its workspace or
	// controller has not finished. The provider may not have started.
	SessionReadinessProvisioning SessionReadiness = "provisioning"
	// SessionReadinessReady means the controller is live and accepting work.
	SessionReadinessReady SessionReadiness = "ready"
	// SessionReadinessExited means the agent process ended. This is NOT success: the
	// exit code says nothing about whether the task was completed.
	SessionReadinessExited SessionReadiness = "exited"
	// SessionReadinessFailed means the session could not be brought up.
	SessionReadinessFailed SessionReadiness = "failed"
)

// Valid reports whether r is a supported readiness value.
func (r SessionReadiness) Valid() bool {
	switch r {
	case SessionReadinessUnknown, SessionReadinessProvisioning, SessionReadinessReady,
		SessionReadinessExited, SessionReadinessFailed:
		return true
	default:
		return false
	}
}

// HoldsProvider reports whether a provider turn can be addressed on this session.
// Only a live controller has a controller generation to fence against.
func (r SessionReadiness) HoldsProvider() bool { return r == SessionReadinessReady }

// RepoWorktreeObservation is one repository's workspace fact for a session. It is a
// reference, never a copy of Git state.
type RepoWorktreeObservation struct {
	RepoKey     domain.WorkflowRepoKey `json:"repoKey"`
	ProjectID   domain.ProjectID       `json:"projectId,omitempty"`
	WorktreeRef string                 `json:"worktreeRef,omitempty"`
	Branch      string                 `json:"branch,omitempty"`
	// HeadSHA 是本次通过归属、分支和创建历史核验的真实 Git HEAD；
	// 无法验证时留空，绝不使用持久化 CreationSHA 替代。
	HeadSHA string `json:"headSha,omitempty"`
	BaseSHA string `json:"baseSha,omitempty"`
	// Dirty reports an uncommitted worktree. M2 records it and never cleans it.
	Dirty bool `json:"dirty"`
}

// SessionObservation is what AO durably knows about one session. It deliberately has
// no lease token, no credential, and no provider-protocol detail.
type SessionObservation struct {
	SessionID domain.SessionID    `json:"sessionId"`
	ProjectID domain.ProjectID    `json:"projectId,omitempty"`
	Kind      domain.SessionKind  `json:"kind,omitempty"`
	Harness   domain.AgentHarness `json:"harness,omitempty"`
	Mode      domain.SessionMode  `json:"mode,omitempty"`

	Readiness SessionReadiness `json:"readiness"`
	// ControllerGeneration identifies the controller instance that owns this session.
	// It rotates on every restart, so an observation from an older generation must
	// not gate work in the newer one.
	ControllerGeneration string `json:"controllerGeneration,omitempty"`
	// ProviderConversationID is the provider's own conversation handle, when one is
	// known. It is not a credential.
	ProviderConversationID string `json:"providerConversationId,omitempty"`

	Worktrees []RepoWorktreeObservation `json:"worktrees,omitempty"`

	// ClientRequestID and ClientRequestCommitted are the idempotency facts a restart
	// needs: a committed request id proves the original spawn completed.
	ClientRequestID        string `json:"clientRequestId,omitempty"`
	ClientRequestCommitted bool   `json:"clientRequestCommitted"`

	Terminated bool      `json:"terminated"`
	ObservedAt time.Time `json:"observedAt"`
	// SessionUpdatedAt 为无 Controller 的普通终端 Session 提供终止事实版本，不作为 API 字段。
	SessionUpdatedAt time.Time `json:"-"`
}

// SpawnRequest asks the gateway to create a session for one attempt.
type SpawnRequest struct {
	WorkflowID domain.WorkflowID        `json:"workflowId"`
	TaskID     domain.WorkflowTaskID    `json:"taskId"`
	AttemptID  domain.WorkflowAttemptID `json:"attemptId"`

	// SpawnKey is stable for the attempt and action. A retry reuses it; inventing a
	// new one is how a duplicate session gets created.
	SpawnKey string `json:"spawnKey"`
	// SpawnHash fingerprints the request so the owner can detect a reused key with
	// different content, exactly like the workflow idempotency ledger.
	SpawnHash string `json:"-"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Profile   string `json:"profile,omitempty"`

	ProjectID domain.ProjectID    `json:"projectId"`
	Harness   domain.AgentHarness `json:"harness,omitempty"`
	Mode      domain.SessionMode  `json:"mode,omitempty"`
	// Prompt is read by the authorized caller and handed over verbatim. This
	// boundary never logs it.
	Prompt string `json:"-"`

	Repos []WorkflowRepoKeyRef `json:"repos,omitempty"`
	// Async asks only for the session row. The resulting observation will be
	// provisioning, not ready.
	Async bool `json:"async"`

	// Workflow 只传递协调器确认的非秘密 Attempt 标识；MCP 复用 Session
	// capability 认证，内部租约不能跨越这个启动边界。
	Workflow *WorkflowSpawnContext `json:"-"`
}

// WorkflowSpawnContext 仅描述启动 Session 所属的 Workflow/Task/Attempt。
// 身份标识本身不授予权限，服务端必须通过 Session capability 与持久绑定验证。
type WorkflowSpawnContext struct {
	WorkflowID domain.WorkflowID
	TaskID     domain.WorkflowTaskID
	AttemptID  domain.WorkflowAttemptID
}

// SpawnRepos translates a spawn request's repository references into the
// session manager's per-repository spawn list. It exists so the gateway is the
// only place the two vocabulary sets meet.
func SpawnRepos(refs []WorkflowRepoKeyRef) []SpawnRepo {
	if len(refs) == 0 {
		return nil
	}
	out := make([]SpawnRepo, 0, len(refs))
	for _, ref := range refs {
		out = append(out, SpawnRepo{
			RepoKey:   string(ref.RepoKey),
			ProjectID: ref.ProjectID,
			BaseSHA:   ref.BaseSHA,
		})
	}
	return out
}

// WorkflowRepoKeyRef names one repository the attempt expects a worktree for.
type WorkflowRepoKeyRef struct {
	RepoKey   domain.WorkflowRepoKey `json:"repoKey"`
	ProjectID domain.ProjectID       `json:"projectId"`
	BaseSHA   string                 `json:"baseSha,omitempty"`
}

// MessageDelivery is which chat path a message must take.
//
// It is explicit rather than inferred because the two paths have different effects: a
// steer joins a running turn, a send opens a new one. A gateway that "tries steer and
// falls back" would silently change what the caller asked for.
type MessageDelivery string

const (
	// MessageDeliverySend opens a normal turn (or queues when the session is busy).
	MessageDeliverySend MessageDelivery = "send"
	// MessageDeliverySteer delivers guidance INTO the turn named by
	// MessageRequest.TargetProviderTurnID. It must never degrade to a send.
	MessageDeliverySteer MessageDelivery = "steer"
)

// Valid reports whether d is a supported delivery mode.
func (d MessageDelivery) Valid() bool {
	return d == MessageDeliverySend || d == MessageDeliverySteer
}

// MessageRequest delivers a message to a session.
type MessageRequest struct {
	SessionID       domain.SessionID `json:"sessionId"`
	ClientMessageID string           `json:"clientMessageId"`
	Text            string           `json:"-"`
	// Delivery selects the chat path. The zero value is not valid: a caller must say
	// whether it is steering an existing turn or opening a new one.
	Delivery MessageDelivery `json:"delivery"`
	// TargetProviderTurnID is the turn a steer joins. It is required for
	// MessageDeliverySteer and forbidden for MessageDeliverySend, because a send has
	// no target turn to be wrong about.
	TargetProviderTurnID string `json:"targetProviderTurnId,omitempty"`
	// ControllerGeneration fences the request to the controller that owned the
	// session when the caller observed it. It is required for BOTH deliveries: an
	// empty value is a caller that cannot be fenced, and a send that cannot be fenced
	// could open a turn on a controller that replaced the one the caller observed.
	ControllerGeneration string `json:"controllerGeneration,omitempty"`
	// Origin preserves who authored the message. A workflow-originated message must
	// not present itself as human.
	Origin domain.MessageOrigin `json:"origin,omitempty"`
	// SenderSessionID is the AO session that authored it, when there is one.
	SenderSessionID domain.SessionID `json:"senderSessionId,omitempty"`
}

// Validate rejects a request whose delivery mode and target turn disagree.
func (r MessageRequest) Validate() error {
	if strings.TrimSpace(string(r.SessionID)) == "" {
		return fmt.Errorf("%w: a message must name a session", ErrExecutionRefused)
	}
	if strings.TrimSpace(r.ClientMessageID) == "" {
		return fmt.Errorf("%w: clientMessageId is required", ErrExecutionRefused)
	}
	if !r.Delivery.Valid() {
		return fmt.Errorf("%w: delivery must be send or steer", ErrExecutionRefused)
	}
	if strings.TrimSpace(r.Text) == "" {
		return fmt.Errorf("%w: message text is empty", ErrExecutionRefused)
	}
	switch r.Delivery {
	case MessageDeliverySteer:
		if strings.TrimSpace(r.TargetProviderTurnID) == "" {
			return fmt.Errorf("%w: a steer must name the provider turn it joins", ErrExecutionRefused)
		}
		if strings.TrimSpace(r.ControllerGeneration) == "" {
			return fmt.Errorf("%w: a steer must name the controller generation", ErrExecutionRefused)
		}
	case MessageDeliverySend:
		if strings.TrimSpace(r.TargetProviderTurnID) != "" {
			return fmt.Errorf("%w: a send must not name a target turn; use delivery=steer",
				ErrExecutionRefused)
		}
		if strings.TrimSpace(r.ControllerGeneration) == "" {
			// A send opens a new turn, and that turn belongs to whichever controller
			// owns the session. Without the caller's generation there is nothing to
			// fence against, so the message could be recorded on a controller that
			// replaced the one the caller observed.
			return fmt.Errorf("%w: a send must name the controller generation", ErrExecutionRefused)
		}
	}
	return nil
}

// MessageReceipt is what the Chat service durably recorded. Queued is not Delivered:
// a queued message has a durable row and no provider action yet.
type MessageReceipt struct {
	SessionID       domain.SessionID `json:"sessionId"`
	ClientMessageID string           `json:"clientMessageId"`
	TurnID          string           `json:"turnId,omitempty"`
	ProviderTurnID  string           `json:"providerTurnId,omitempty"`
	Queued          bool             `json:"queued"`
	Duplicate       bool             `json:"duplicate"`
	// Steered reports that the guidance joined a running turn rather than opening
	// one. It is set only by a steer receipt, so a caller can tell the two paths
	// apart without re-deriving it from TargetProviderTurnID.
	Steered bool `json:"steered"`
	// TargetProviderTurnID echoes the turn the caller named. When Steered is true this
	// equals the turn the provider reported absorbing the guidance, so a receipt that
	// landed elsewhere is detectable rather than merely unlikely.
	TargetProviderTurnID string `json:"targetProviderTurnId,omitempty"`
	// ControllerGeneration is the generation that produced this receipt, so the
	// caller can fence it against the one it observed.
	ControllerGeneration string `json:"controllerGeneration,omitempty"`
}

// MatchesSteer reports whether this receipt proves the guidance joined exactly the
// turn and generation the caller named.
//
// It is deliberately strict and deliberately not "close enough": a provider that
// answered with a different turn id steered work the caller was not looking at, and
// reporting that as success would be the exact failure this check exists to prevent.
func (r MessageReceipt) MatchesSteer(request MessageRequest) bool {
	return r.Steered &&
		r.SessionID == request.SessionID &&
		r.ClientMessageID == request.ClientMessageID &&
		r.ControllerGeneration == request.ControllerGeneration &&
		r.TargetProviderTurnID != "" &&
		r.TargetProviderTurnID == request.TargetProviderTurnID
}

// InterruptRequest asks a session to stop its current turn.
type InterruptRequest struct {
	SessionID domain.SessionID `json:"sessionId"`
	// ControllerGeneration names the controller the caller believes is running the
	// turn. It is mandatory: a stop that cannot be fenced may interrupt a turn on a
	// controller that replaced the one the caller stopped for, and the resulting
	// receipt would prove a stop of work nobody asked to stop.
	ControllerGeneration string `json:"controllerGeneration"`
	// ClientMessageID makes a retried stop idempotent at the gateway boundary too.
	ClientMessageID string `json:"clientMessageId,omitempty"`
}

// Validate rejects a stop request that cannot be fenced.
func (r InterruptRequest) Validate() error {
	if strings.TrimSpace(string(r.SessionID)) == "" {
		return fmt.Errorf("%w: an interrupt must name a session", ErrExecutionRefused)
	}
	if strings.TrimSpace(r.ControllerGeneration) == "" {
		return fmt.Errorf("%w: an interrupt must name the controller generation",
			ErrExecutionRefused)
	}
	return nil
}

// MatchesSend reports whether this receipt proves the message the caller asked to
// send is the one the Chat service recorded, on the controller the caller observed.
//
// It is the send-side counterpart of MatchesSteer, and it is strict for the same
// reason: a send whose receipt names a different generation opened a turn on a
// controller that replaced the one the caller observed, and a receipt that names no
// turn and is not explicitly queued is an acknowledgement of nothing. A duplicate
// (an earlier delivery of the same client message id) has an empty turn id on
// purpose, so it is accepted here only as a duplicate rather than as a new send.
func (r MessageReceipt) MatchesSend(request MessageRequest) bool {
	if r.SessionID != request.SessionID ||
		r.ClientMessageID != request.ClientMessageID ||
		r.ControllerGeneration != request.ControllerGeneration {
		return false
	}
	if r.Steered {
		// A send receipt that claims it steered is evidence of the other path.
		return false
	}
	return r.TurnID != "" || r.Queued || r.Duplicate
}

// SessionObservation.ValidateFence checks that an observation describes exactly the
// session and controller generation the caller named.
//
// A mismatch is not a transient failure: it means the evidence in hand describes a
// different controller than the one the caller is about to act on, so acting on it
// would fence the wrong owner. Implementations therefore treat a mismatch as a
// refusal rather than degrading to "unknown".
//
// An expected generation with no observed generation is a refusal too. The earlier
// "compare only when both sides are non-empty" rule let exactly the case the fence
// exists for through: a caller that named a controller was told an observation with
// no generation at all matched it, so a session whose durable generation column was
// empty (a rotated controller, a partially written row) authorized work the caller
// had no evidence for. Only the both-empty case is silent, because a caller with no
// expectation is asking for no fence.
func (o SessionObservation) ValidateFence(
	sessionID domain.SessionID,
	generation string,
) error {
	if o.SessionID != sessionID {
		return fmt.Errorf("%w: observation describes session %s, not %s",
			ErrExecutionSessionMismatch, o.SessionID, sessionID)
	}
	switch {
	case generation == "":
		// The caller named no generation, so there is nothing to fence against.
	case o.ControllerGeneration == "":
		return fmt.Errorf("%w: observation carries no generation for session %s",
			ErrExecutionStaleGeneration, sessionID)
	case o.ControllerGeneration != generation:
		return fmt.Errorf("%w: observation describes generation %s, not %s",
			ErrExecutionStaleGeneration, o.ControllerGeneration, generation)
	}
	if o.Terminated {
		return fmt.Errorf("%w: session %s is terminated", ErrExecutionTerminated, sessionID)
	}
	return nil
}

// InterruptReceipt records what was proven about the stop. Confirmed false with
// Unknown false means nothing was in flight, which is a clean outcome.
type InterruptReceipt struct {
	SessionID domain.SessionID `json:"sessionId"`
	// ControllerGeneration is the generation the stop was issued against. It is
	// returned on every receipt kind, because "nothing to stop" and "unknown" are
	// also facts about one specific controller: a caller that cannot tell which
	// generation a receipt describes cannot tell whether the stop applied to the
	// work it was worried about.
	ControllerGeneration string `json:"controllerGeneration"`
	// Confirmed means the controller acknowledged the stop.
	Confirmed bool `json:"confirmed"`
	// Unknown means the request was issued and no answer arrived. The caller must
	// freeze rather than retry.
	Unknown bool `json:"unknown"`
	// NothingToStop means there was no turn in flight. It is not an error.
	NothingToStop bool `json:"nothingToStop"`
}

// MatchesInterrupt reports whether this receipt proves the stop was issued against
// exactly the generation the caller named.
//
// The check exists so an uncertain or confirmed answer about a different controller
// cannot settle a stop for this one: a receipt from a replaced generation proves
// something, but not the thing the caller asked about.
func (r InterruptReceipt) MatchesInterrupt(request InterruptRequest) bool {
	return r.SessionID == request.SessionID &&
		r.ControllerGeneration != "" &&
		r.ControllerGeneration == request.ControllerGeneration
}

// ResumeRequest asks for an existing session to be brought back. Resume is not
// guaranteed to replay history; the observation reports what actually happened.
type ResumeRequest struct {
	SessionID domain.SessionID `json:"sessionId"`
	// SpawnKey follows the same stability rule as SpawnRequest.SpawnKey.
	SpawnKey string `json:"spawnKey"`
}

// ExecutionGateway is implemented by the local adapter that composes AO's Session
// Manager and Chat service, and by the fake used in offline tests.
//
// Every method takes the caller's context first and must honour cancellation. None of
// them may start a provider process itself.
type ExecutionGateway interface {
	// InspectSession reads what AO holds for one session.
	InspectSession(ctx context.Context, sessionID domain.SessionID) (SessionObservation, error)
	// FindSpawn answers "was this request already spawned". It is the only
	// sanctioned way to learn that, because guessing from process lists or
	// directories is exactly what produced duplicate sessions before.
	FindSpawn(ctx context.Context, clientRequestID string) (SessionObservation, bool, error)
	// Spawn creates a session. A lost answer must return ErrSpawnOutcomeUnknown
	// rather than an error a caller could mistake for "nothing happened".
	Spawn(ctx context.Context, request SpawnRequest) (SessionObservation, error)
	// Send delivers a message through the Chat service.
	Send(ctx context.Context, request MessageRequest) (MessageReceipt, error)
	// Steer delivers guidance INTO the provider turn named by the request.
	//
	// It is a separate method on purpose. A gateway that could only Send would be
	// forced to express steering as a normal message, which has a different effect:
	// a send opens a new turn, so a "steer" would discard nothing but also would not
	// correct the running turn. Implementations must return ErrExecutionUnsupported
	// when the provider cannot steer, so the caller can offer the queue path instead.
	Steer(ctx context.Context, request MessageRequest) (MessageReceipt, error)
	// Interrupt asks a live controller to stop its current turn.
	Interrupt(ctx context.Context, request InterruptRequest) (InterruptReceipt, error)
	// Resume brings an existing session back after a restart or disconnect.
	Resume(ctx context.Context, request ResumeRequest) (SessionObservation, error)
}

// Validate rejects a spawn request that cannot be safely attributed to one attempt.
//
// A missing attempt, workflow, task, or project id would produce a session nobody can
// trace back, and a missing spawn key would make the retry rule unenforceable. All four
// are refused before any side effect.
func (r SpawnRequest) Validate() error {
	if strings.TrimSpace(string(r.WorkflowID)) == "" ||
		strings.TrimSpace(string(r.TaskID)) == "" ||
		strings.TrimSpace(string(r.AttemptID)) == "" {
		return fmt.Errorf("%w: spawn request must name a workflow, task, and attempt", ErrExecutionRefused)
	}
	if strings.TrimSpace(string(r.ProjectID)) == "" {
		return fmt.Errorf("%w: spawn request must name a project", ErrExecutionRefused)
	}
	if !r.Harness.IsKnown() && r.Harness != domain.HarnessFake {
		return fmt.Errorf("%w: unknown harness %q", ErrExecutionRefused, r.Harness)
	}
	if r.Mode != "" && !r.Mode.Valid() {
		return fmt.Errorf("%w: unknown session mode %q", ErrExecutionRefused, r.Mode)
	}
	return nil
}
