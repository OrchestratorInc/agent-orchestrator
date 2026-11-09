// Package execution is the M2 coordinator that turns a Workflow attempt into a real
// AO session without ever being able to fake that the agent started.
//
// It is deliberately thin. Everything it does is a composition of three owners that
// already exist:
//
//   - the capability Resolver says whether the evidence allows this harness/feature;
//   - the binding store records intent and the session fact, with its own CAS;
//   - an ExecutionGateway (the Session Manager and Chat service behind it) performs
//     the actual spawn, send, interrupt, or resume.
//
// Nothing here launches a process, runs `exec`, parses a provider protocol, touches a
// worktree's files, or reports a task result. The two rules the whole package is built
// around:
//
//	"Session created" is not "provider started".
//	A lost answer is not a failure, and it is never a reason to try again.
//
// The offline fake gateway can simulate both, so the tests can prove the coordinator
// freezes rather than duplicating side effects.
package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
)

// Action names one coordinated execution decision. It is part of the spawn key so a
// resume can never collide with the spawn that created the session.
type Action string

const (
	// ActionSpawn is the initial creation of a session for an attempt.
	ActionSpawn Action = "spawn"
	// ActionResume re-attaches to an existing session after a restart.
	ActionResume Action = "resume"
)

// Valid reports whether a is a supported action.
func (a Action) Valid() bool { return a == ActionSpawn || a == ActionResume }

// pendingControllerGenerationPrefix marks a binding that has a session but no controller
// generation, which is exactly the provisioning state. The store requires a non-empty
// generation on a bound row, and a provisioning session genuinely has none, so the column
// records this marker instead of an identity no controller can hold.
const pendingControllerGenerationPrefix = "pending:"

// fencedGeneration reports the generation a binding can actually be fenced against.
//
// A pending marker is not a generation: no controller reports it, so comparing it to an
// observation would refuse every provisioning session with a misleading "stale" answer.
// Returning "" instead lets ValidateFence report the honest reason — an expected
// generation that is absent cannot be checked — while LiveTarget already refuses such a
// binding earlier with generation_missing.
func fencedGeneration(generation string) string {
	if strings.HasPrefix(generation, pendingControllerGenerationPrefix) {
		return ""
	}
	return generation
}

// reanchorableGeneration reports whether an observation's generation may replace the one
// a binding recorded.
//
// It is the pure decision behind the re-anchor branch in bindObservation, and it exists as
// a function so every refusal can be table-tested rather than inferred from a SQL no-op.
// All four conditions must hold:
//
//   - the observation describes the SAME session the binding already names. A different
//     session is a different fact; adopting it through a generation repair would let a
//     re-anchor smuggle in a rebind.
//   - the observed generation is non-empty. An observation with no generation is not
//     evidence about a controller, and recording "" would erase the identity the fence
//     depends on.
//   - the observed generation is not a pending placeholder. The placeholder is a marker
//     this package writes for "a session exists and no controller was observed yet", so it
//     is never an owner: letting one win would replace a real controller with a fiction.
//   - it actually differs from the recorded one. Identical means there is nothing to
//     repair, and a write would only bump the revision.
func reanchorableGeneration(binding domain.WorkflowAttemptBinding, observation ports.SessionObservation) bool {
	if binding.SessionID == nil || *binding.SessionID != observation.SessionID {
		return false
	}
	if strings.TrimSpace(observation.ControllerGeneration) == "" {
		return false
	}
	if strings.HasPrefix(observation.ControllerGeneration, pendingControllerGenerationPrefix) {
		return false
	}
	return observation.ControllerGeneration != binding.ControllerGeneration
}

// BindingStore is the narrow persistence surface the coordinator needs. It is an
// interface so the coordinator can be tested against the real store or a fake without
// the coordinator knowing which.
type BindingStore interface {
	CreateWorkflowAttemptBinding(
		ctx context.Context,
		binding domain.WorkflowAttemptBinding,
		repositories []domain.WorkflowAttemptRepoBinding,
		now time.Time,
	) (domain.WorkflowAttemptBinding, bool, error)
	GetWorkflowAttemptBinding(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (domain.WorkflowAttemptBinding, bool, error)
	GetWorkflowAttemptBindingBySpawnRequest(
		ctx context.Context,
		spawnRequestID string,
	) (domain.WorkflowAttemptBinding, bool, error)
	BindWorkflowAttemptSession(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
		sessionID domain.SessionID,
		controllerGeneration string,
		target domain.WorkflowBindingState,
		expectedRevision int64,
		now time.Time,
	) (domain.WorkflowAttemptBinding, bool, error)
	TransitionWorkflowAttemptBinding(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
		next domain.WorkflowBindingState,
		failureCode string,
		expectedRevision int64,
		now time.Time,
	) (domain.WorkflowAttemptBinding, bool, error)
	// ReanchorWorkflowAttemptGeneration repairs the recorded controller generation when
	// the session's controller was REPLACED and its generation rotated. It never
	// changes the session, and the fence still refuses the old generation.
	ReanchorWorkflowAttemptGeneration(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
		sessionID domain.SessionID,
		generation string,
		expectedRevision int64,
		expectedState domain.WorkflowBindingState,
		now time.Time,
	) (domain.WorkflowAttemptBinding, bool, error)
	// GetWorkflowAttempt reads the attempt row, which is the only durable holder of
	// the attempt's lease token. BindAttempt reads it so the spawned session's tool
	// server can authenticate as the attempt; the token is never taken from a
	// request body and never logged.
	GetWorkflowAttempt(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (domain.WorkflowAttempt, bool, error)
}

// RepoBindingStore is the optional per-repository persistence surface.
//
// When a coordinator's store implements it, a bind verifies that EVERY repository
// the attempt expects actually has a worktree before it may land on bound_ready,
// and records each repository's observed head afterwards. A store without it keeps
// the single-repository behaviour: there is nothing to compare against.
type RepoBindingStore interface {
	ListWorkflowAttemptRepoBindings(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) ([]domain.WorkflowAttemptRepoBinding, error)
	RecordWorkflowAttemptRepoBinding(
		ctx context.Context,
		binding domain.WorkflowAttemptRepoBinding,
		expectedRevision int64,
		now time.Time,
	) (domain.WorkflowAttemptRepoBinding, bool, error)
}

// RepoWorktreeMissingCode is the stable failure code recorded when an attempt is
// not bound because one of its expected repositories has no worktree in the
// session. It is a code, not a message, so a client can branch on it.
const RepoWorktreeMissingCode = "repo_worktree_missing"

// RepoWorktreeMismatchCode is the stable failure code recorded when an expected
// repository HAS a worktree that cannot be proven to be the one the attempt asked
// for: no recorded head, or a head that is not the expected base.
const RepoWorktreeMismatchCode = "repo_worktree_mismatch"

// Coordinator composes capability gating, binding persistence, and the gateway.
//
// It is safe for concurrent use as long as the store and gateway are; it holds no
// mutable state of its own apart from injected functions.
type Coordinator struct {
	store    BindingStore
	gateway  ports.ExecutionGateway
	resolver *capability.Resolver
	now      func() time.Time
	newID    func() string
	// allowAction lets a deployment refuse an action entirely (for example resume
	// before M2-05 wires stop/resume receipts). Nil allows both.
	allowAction    func(Action) error
	executionFence func(context.Context) (domain.SchedulerFence, error)
}

// Deps are the coordinator's injected collaborators.
type Deps struct {
	Store    BindingStore
	Gateway  ports.ExecutionGateway
	Resolver *capability.Resolver
	// Clock defaults to time.Now. Tests must inject it.
	Clock func() time.Time
	// NewID defaults to a hash-derived id; it exists so a test can make binding
	// identity deterministic.
	NewID func() string
	// AllowAction is optional. Returning an error refuses the action before any
	// durable state is written.
	AllowAction func(Action) error
	// ExecutionFence 只返回本 daemon 当前持有的任期；不能读取 DB 后冒用其他实例的 owner。
	ExecutionFence func(context.Context) (domain.SchedulerFence, error)
}

// New builds a coordinator.
func New(deps Deps) *Coordinator {
	c := &Coordinator{
		store:          deps.Store,
		gateway:        deps.Gateway,
		resolver:       deps.Resolver,
		now:            deps.Clock,
		newID:          deps.NewID,
		allowAction:    deps.AllowAction,
		executionFence: deps.ExecutionFence,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.resolver == nil {
		c.resolver = capability.NewResolver(capability.WithClock(c.now))
	}
	if c.newID == nil {
		c.newID = func() string { return "" }
	}
	return c
}

// SpawnKey derives the stable idempotency key for one attempt and action.
//
// The rule is the whole point of this function: the key is a pure function of
// (attempt, action), so a retry computes the same key and the owner replays instead of
// spawning again. Nothing here consults the clock, a counter, or randomness — any of
// those would mint a new id on retry, which is precisely how a duplicate session is
// created.
func SpawnKey(attemptID domain.WorkflowAttemptID, action Action) string {
	digest := sha256.Sum256([]byte("ao.workflow." + string(action) + ":" + string(attemptID)))
	return string(action) + "-" + hex.EncodeToString(digest[:16])
}

// SpawnHash fingerprints a spawn request so the owner can reject a reused key whose
// content changed. It excludes the prompt: the same attempt asked to spawn twice with
// different prompt text is still the same spawn, and hashing free-form text would make
// an innocuous edit look like a conflict.
func SpawnHash(request ports.SpawnRequest) (string, error) {
	var builder strings.Builder
	if request.Profile != "" {
		if len(request.Profile) > 129 || strings.ContainsAny(request.Profile, "\r\n\x00") {
			return "", ports.ErrExecutionRefused
		}
		builder.WriteString("profile=" + request.Profile + "\n")
	}
	if request.Model != "" || request.Effort != "" {
		for _, selection := range []string{request.Model, request.Effort} {
			if strings.ContainsAny(selection, "\r\n\x00") {
				return "", ports.ErrExecutionRefused
			}
			builder.WriteString(selection)
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("workflow=")
	builder.WriteString(string(request.WorkflowID))
	builder.WriteString("\ntask=")
	builder.WriteString(string(request.TaskID))
	builder.WriteString("\nattempt=")
	builder.WriteString(string(request.AttemptID))
	builder.WriteString("\nspawnKey=")
	builder.WriteString(request.SpawnKey)
	builder.WriteString("\nproject=")
	builder.WriteString(string(request.ProjectID))
	builder.WriteString("\nharness=")
	builder.WriteString(string(request.Harness))
	builder.WriteString("\nmode=")
	builder.WriteString(string(request.Mode))
	builder.WriteString("\nasync=")
	if request.Async {
		builder.WriteString("true")
	} else {
		builder.WriteString("false")
	}
	for _, repo := range request.Repos {
		builder.WriteString("\nrepo=")
		builder.WriteString(string(repo.RepoKey))
		builder.WriteString(":")
		builder.WriteString(string(repo.ProjectID))
		builder.WriteString(":")
		builder.WriteString(repo.BaseSHA)
	}
	digest := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(digest[:]), nil
}

// BindOutcome is what one BindAttempt call decided.
type BindOutcome struct {
	Binding domain.WorkflowAttemptBinding
	// Spawned reports whether this call issued the gateway spawn. A replay or a
	// frozen unknown never sets it.
	Spawned bool
	// Frozen means the binding is in an uncertain state and no further external
	// action may be taken until reconciliation.
	Frozen bool
	// ReasonCode explains the outcome for a client.
	ReasonCode string
}

// BindAttempt performs the guarded spawn-and-bind sequence for one attempt.
//
// The order is the safety contract:
//
//  1. Capability gate. Insufficient evidence refuses before any durable write, so a
//     refused request costs nothing.
//  2. Look up an existing binding. An uncertain state freezes; it never spawns again.
//  3. Persist the intent (idempotent by spawn key).
//  4. Ask the gateway for the session.
//  5. Bind from the answer, or record spawn_unknown if the answer was lost.
//
// Step 5 is where "session created" and "provider started" are kept apart: a
// provisioning observation binds at bound_ready and nothing in this package can move
// it to running.
func (c *Coordinator) BindAttempt(
	ctx context.Context,
	request ports.SpawnRequest,
	repositories []domain.WorkflowAttemptRepoBinding,
) (BindOutcome, error) {
	if c.store == nil || c.gateway == nil {
		return BindOutcome{}, apierrUnavailable()
	}
	if err := request.Validate(); err != nil {
		return BindOutcome{}, err
	}
	var permitErr error
	ctx, permitErr = c.executionContext(ctx, request.AttemptID)
	if permitErr != nil {
		return BindOutcome{}, permitErr
	}
	now := c.now().UTC()

	// 1. Capability gate. A session may only be requested when the evidence allows it,
	// and the gate is asked about the harness, not about a session that does not exist
	// yet.
	if decision := c.resolver.Gate(ctx, ports.CapabilityProbeRequest{
		Harness: request.Harness,
		Profile: request.Profile, Model: request.Model, Effort: request.Effort,
	}, domain.CapabilityFeatureStreaming); !decision.Allowed {
		return BindOutcome{
			ReasonCode: decision.ReasonCode,
		}, fmt.Errorf("%w: harness %s: %s", ports.ErrExecutionRefused, request.Harness, decision.ReasonCode)
	}

	if request.SpawnKey == "" {
		request.SpawnKey = SpawnKey(request.AttemptID, ActionSpawn)
	}
	hash, err := SpawnHash(request)
	if err != nil {
		return BindOutcome{}, err
	}

	// 2. An existing binding decides everything. Reading before writing is what makes
	// the unknown case safe: the freeze happens here, with no gateway call at all.
	existing, found, err := c.store.GetWorkflowAttemptBinding(ctx, request.AttemptID)
	if err != nil {
		return BindOutcome{}, err
	}
	if found {
		outcome, handled, err := c.resumeExisting(ctx, existing, now)
		if handled {
			return outcome, err
		}
	}

	// 3. Persist the intent. This is idempotent by spawn request id, so a concurrent
	// second caller gets the same row back with created=false.
	intent := domain.WorkflowAttemptBinding{
		AttemptID:      request.AttemptID,
		WorkflowID:     request.WorkflowID,
		TaskID:         request.TaskID,
		SpawnRequestID: request.SpawnKey,
		SpawnHash:      hash,
	}
	binding, created, err := c.store.CreateWorkflowAttemptBinding(ctx, intent, repositories, now)
	if err != nil {
		return BindOutcome{}, err
	}
	if !created {
		outcome, handled := terminalOutcome(binding)
		if handled {
			return outcome, nil
		}
	}
	ctx = bindingPermitContext(ctx, binding)

	// 4. Claim the right to spawn. This CAS is the concurrency fence: exactly one caller
	// may move intent_persisted to spawn_requested, so two callers racing on one attempt
	// cannot both reach the gateway. Without it, both would insert (one sees created=false
	// with the row still in intent_persisted) and both would spawn, creating two sessions
	// for one attempt — the duplicate this whole design exists to prevent.
	claimed, applied, err := c.store.TransitionWorkflowAttemptBinding(
		ctx, request.AttemptID, domain.WorkflowBindingStateSpawnRequested, "", binding.Revision, now,
	)
	if err != nil {
		if errors.Is(err, domain.ErrWorkflowBindingRevisionConflict) ||
			errors.Is(err, domain.ErrInvalidWorkflowBindingTransition) {
			// Another caller owns the spawn. Report that plainly and do not spawn.
			current, found, getErr := c.store.GetWorkflowAttemptBinding(ctx, request.AttemptID)
			if getErr != nil {
				return BindOutcome{}, getErr
			}
			if !found {
				current = binding
			}
			return BindOutcome{Binding: current, ReasonCode: "spawn_in_progress"}, nil
		}
		return BindOutcome{}, err
	}
	if !applied {
		current, found, getErr := c.store.GetWorkflowAttemptBinding(ctx, request.AttemptID)
		if getErr != nil {
			return BindOutcome{}, getErr
		}
		if !found {
			current = binding
		}
		return BindOutcome{Binding: current, ReasonCode: "spawn_in_progress"}, nil
	}
	binding = claimed
	ctx = bindingPermitContext(ctx, binding)

	// 5. Ask the gateway. This is the only external side effect in the package, and only
	// the caller that won the claim above reaches it.
	//
	// 从持久 Attempt 确认启动身份，跨边界只传非秘密标识。
	// MCP 通过 Session capability 认证，内部租约不会交给 Gateway 或 Agent。
	attempt, found, err := c.store.GetWorkflowAttempt(ctx, request.AttemptID)
	if err != nil {
		return BindOutcome{}, err
	}
	if !found {
		// Spawning a session for an attempt that does not exist would produce an agent
		// that can never collaborate. Refuse before the side effect.
		return BindOutcome{}, fmt.Errorf("%w: %s", ErrAttemptNotFound, request.AttemptID)
	}
	if attempt.LeaseToken != "" {
		request.Workflow = &ports.WorkflowSpawnContext{
			WorkflowID: request.WorkflowID,
			TaskID:     request.TaskID,
			AttemptID:  request.AttemptID,
		}
	}
	// The hash computed in step 1 must travel with the request. The gateway forwards it
	// as the session's client-request hash, and the Session Manager refuses a client
	// request id with no hash — so a request that carried the key but not the hash could
	// never spawn at all. The offline fake spawner ignored the field, which is exactly
	// how this gap survived until the real Session Manager was wired underneath.
	request.SpawnHash = hash
	observation, spawnErr := c.gateway.Spawn(ctx, request)
	ctx, err = c.refreshExecutionContext(ctx, request.AttemptID)
	if err != nil {
		return BindOutcome{Binding: binding, Frozen: true, Spawned: true, ReasonCode: "execution_fenced"}, err
	}
	if spawnErr != nil {
		return c.recordSpawnFailure(ctx, binding, spawnErr, now)
	}

	// 6. Bind from the answer. A provisioning session is not a started provider, so the
	// binding lands at bound_ready and stays there.
	outcome, err := c.bindObservation(ctx, binding, observation, now)
	// 即使后续仓库门禁拒绝绑定，也必须如实报告已经发生的创建调用。
	outcome.Spawned = true
	return outcome, err
}

// resumeExisting decides what an already-recorded binding means for this call. It
// returns handled=false only when the binding is safe to continue from.
func (c *Coordinator) resumeExisting(
	ctx context.Context,
	existing domain.WorkflowAttemptBinding,
	now time.Time,
) (BindOutcome, bool, error) {
	_ = ctx
	switch existing.State {
	case domain.WorkflowBindingStateSpawnUnknown,
		domain.WorkflowBindingStateReconcileRequired,
		domain.WorkflowBindingStateRecoveryUncertain,
		domain.WorkflowBindingStateStopUnknown:
		// The dangerous states. No external action may be taken, and in particular no
		// second spawn: the first one may have succeeded.
		return BindOutcome{
			Binding:    existing,
			Frozen:     true,
			ReasonCode: string(existing.State),
		}, true, nil
	case domain.WorkflowBindingStateBoundReady, domain.WorkflowBindingStateRunning:
		// 幂等重放复用 Session，但仍重新核验工作区，不能永久信任历史 READY。
		outcome, err := c.reconcileKnownSession(ctx, existing, now)
		if err == nil && !outcome.Frozen {
			outcome.ReasonCode = "binding_replayed"
		}
		return outcome, true, err
	case domain.WorkflowBindingStateStopRequested, domain.WorkflowBindingStateStopConfirmed:
		return BindOutcome{
			Binding:    existing,
			Frozen:     true,
			ReasonCode: string(existing.State),
		}, true, nil
	case domain.WorkflowBindingStateFailedConfirmed:
		return BindOutcome{Binding: existing, Frozen: true, ReasonCode: "binding_failed_confirmed"}, true, nil
	default:
		// intent_persisted or spawn_requested: a previous call never got an answer
		// recorded. Continue the sequence under the same binding.
		_ = now
		return BindOutcome{}, false, nil
	}
}

// recordSpawnFailure turns a gateway error into a durable outcome.
//
// A lost answer becomes spawn_unknown and freezes the attempt. Any other error is
// still not proof that nothing happened, so it freezes too; only an explicit refusal
// that the gateway can prove happened before any side effect is recorded as
// failed_confirmed.
func (c *Coordinator) recordSpawnFailure(
	ctx context.Context,
	binding domain.WorkflowAttemptBinding,
	spawnErr error,
	now time.Time,
) (BindOutcome, error) {
	ctx = bindingPermitContext(ctx, binding)
	if errors.Is(spawnErr, ports.ErrExecutionRefused) {
		// The gateway proved it did not create anything.
		updated, _, err := c.store.TransitionWorkflowAttemptBinding(
			ctx, binding.AttemptID, domain.WorkflowBindingStateFailedConfirmed,
			"spawn_refused", binding.Revision, now,
		)
		if err != nil {
			return BindOutcome{}, err
		}
		return BindOutcome{
			Binding: updated, Frozen: true, ReasonCode: "spawn_refused",
		}, nil
	}

	unknown := errors.Is(spawnErr, ports.ErrSpawnOutcomeUnknown)
	reasonCode := "spawn_unknown"
	if !unknown {
		// Even a non-classified error may have reached the owner. Treat it as unknown
		// rather than guessing; this is the fail-closed direction.
		reasonCode = "spawn_unclassified"
	}
	updated, _, err := c.store.TransitionWorkflowAttemptBinding(
		ctx, binding.AttemptID, domain.WorkflowBindingStateSpawnUnknown,
		reasonCode, binding.Revision, now,
	)
	if err != nil {
		return BindOutcome{}, err
	}
	return BindOutcome{Binding: updated, Frozen: true, ReasonCode: reasonCode}, nil
}

// readinessPlan is what one readiness observation permits, expressed as data so a test can
// enumerate every value.
type readinessPlan struct {
	// bind reports whether the session should be recorded on the binding at all.
	bind bool
	// target is the state the bind lands on when bind is true.
	target domain.WorkflowBindingState
	// transition is the state to move to when bind is false.
	transition domain.WorkflowBindingState
	// failureCode is the stable code recorded with the outcome.
	failureCode string
	// live reports whether a provider turn can be addressed on this session.
	live bool
}

// planForReadiness maps one readiness value to what it permits.
//
// Every known value is listed explicitly and the finish returns the fail-closed plan, so
// adding a SessionReadiness value later cannot silently inherit success: a new value falls
// through to reconcile_required until someone decides what it means. That is the whole
// point of this function — the previous shape used "if provisioning then X else bound_ready",
// which made exited and unknown look bound.
func planForReadiness(readiness ports.SessionReadiness) readinessPlan {
	switch readiness {
	case ports.SessionReadinessReady:
		// A live controller: the session may be bound and a provider turn addressed.
		return readinessPlan{
			bind: true, target: domain.WorkflowBindingStateBoundReady, live: true,
		}
	case ports.SessionReadinessProvisioning:
		// The row exists, the workspace/controller does not. Bound, but not live.
		return readinessPlan{
			bind: true, target: domain.WorkflowBindingStateSessionProvisioning,
			failureCode: "session_provisioning",
		}
	case ports.SessionReadinessExited:
		// The agent process ended. That is a proven non-success, and it is emphatically
		// not "bound": the exit code says nothing about whether the task was completed.
		return readinessPlan{
			transition:  domain.WorkflowBindingStateFailedConfirmed,
			failureCode: "session_exited",
		}
	case ports.SessionReadinessFailed:
		return readinessPlan{
			transition:  domain.WorkflowBindingStateFailedConfirmed,
			failureCode: "spawn_failed",
		}
	case ports.SessionReadinessUnknown:
		return readinessPlan{
			transition:  domain.WorkflowBindingStateReconcileRequired,
			failureCode: "session_readiness_unknown",
		}
	default:
		// Fail closed for any value this switch does not know. A future readiness value
		// must not inherit success by omission.
		return readinessPlan{
			transition:  domain.WorkflowBindingStateReconcileRequired,
			failureCode: "session_readiness_unrecognized",
		}
	}
}

// bindObservation records the session the gateway reported.
//
// The mapping is delegated to planForReadiness so every readiness value has an explicit,
// testable consequence. Only a Ready observation is allowed to look like a live session,
// and even then this package never advances the binding to running — that requires a
// confirmed_runtime observation, which the offline path cannot produce.
func (c *Coordinator) bindObservation(
	ctx context.Context,
	binding domain.WorkflowAttemptBinding,
	observation ports.SessionObservation,
	now time.Time,
) (BindOutcome, error) {
	ctx = bindingPermitContext(ctx, binding)
	if observation.SessionID == "" {
		// The gateway answered without a session: that is an unknown outcome, not a
		// successful spawn.
		updated, _, err := c.store.TransitionWorkflowAttemptBinding(
			ctx, binding.AttemptID, domain.WorkflowBindingStateSpawnUnknown,
			"spawn_no_session", binding.Revision, now,
		)
		if err != nil {
			return BindOutcome{}, err
		}
		return BindOutcome{Binding: updated, Frozen: true, ReasonCode: "spawn_no_session"}, nil
	}
	plan := planForReadiness(observation.Readiness)
	if !plan.bind {
		// Exited, failed, unknown, or an unrecognized value: record the loss instead of
		// vouching for a session we cannot use. These states forbid a session id, which is
		// correct — the binding must not claim a session it is not bound to.
		updated, _, err := c.store.TransitionWorkflowAttemptBinding(
			ctx, binding.AttemptID, plan.transition, plan.failureCode, binding.Revision, now,
		)
		if err != nil {
			return BindOutcome{}, err
		}
		return BindOutcome{Binding: updated, Frozen: true, ReasonCode: plan.failureCode}, nil
	}

	// A binding about to record a live session must be able to point at the
	// repositories the attempt expects, and at the commits it expected — but only
	// when the session actually reports repository evidence. See repoGate for why an
	// empty worktree list is not evidence of a missing worktree on a single-repo
	// attempt, and why a multi-repo attempt is always checked strictly.
	if plan.target == domain.WorkflowBindingStateBoundReady {
		states, gate, err := c.repoGate(ctx, binding.AttemptID, observation)
		if err != nil {
			return BindOutcome{}, err
		}
		if code := blockedRepoCode(states, gate); code != "" {
			updated, _, err := c.store.TransitionWorkflowAttemptBinding(
				ctx, binding.AttemptID, domain.WorkflowBindingStateReconcileRequired,
				code, binding.Revision, now,
			)
			if err != nil {
				return BindOutcome{}, err
			}
			return BindOutcome{
				Binding:    updated,
				Frozen:     true,
				ReasonCode: code,
			}, nil
		}
	}

	// A bound row must carry a generation (the store refuses an empty one), and a
	// provisioning session genuinely has none yet. The placeholder is deliberately not a
	// generation any controller can hold, and every fence in this package reads it as "no
	// controller" (see fencedGeneration) rather than as an identity: it exists so the
	// column can be NOT NULL without claiming an owner that was never observed.
	generation := observation.ControllerGeneration
	if generation == "" {
		generation = pendingControllerGenerationPrefix + string(observation.Readiness)
	}
	// The landing state records how far the session actually got, per planForReadiness.
	// Neither target is running: that needs runtime evidence this path cannot have.
	targetState := plan.target
	if binding.SessionID != nil && binding.State.CanTransitionTo(targetState) {
		// 恢复已有会话只更新状态和 generation；首次绑定的唯一性检查不能再次获取自己。
		if *binding.SessionID != observation.SessionID {
			return BindOutcome{}, ports.ErrExecutionSessionMismatch
		}
		if reanchorableGeneration(binding, observation) {
			updated, applied, err := c.store.ReanchorWorkflowAttemptGeneration(
				ctx, binding.AttemptID, observation.SessionID, observation.ControllerGeneration,
				binding.Revision, binding.State, now,
			)
			if err != nil {
				return BindOutcome{}, err
			}
			if !applied {
				return BindOutcome{Binding: binding, Frozen: true, ReasonCode: "generation_changed"}, nil
			}
			binding = updated
			ctx, err = c.refreshExecutionContext(bindingPermitContext(ctx, binding), binding.AttemptID)
			if err != nil {
				return BindOutcome{}, err
			}
		}
		updated, applied, err := c.store.TransitionWorkflowAttemptBinding(
			ctx, binding.AttemptID, targetState, "", binding.Revision, now,
		)
		if err != nil {
			return BindOutcome{}, err
		}
		if !applied {
			return BindOutcome{Binding: binding, Frozen: true, ReasonCode: "binding_changed"}, nil
		}
		err = c.recordRepoBindings(ctx, updated, observation, now)
		return BindOutcome{Binding: updated, ReasonCode: string(observation.Readiness)}, err
	}
	if !binding.State.CanTransitionTo(targetState) {
		// Nothing to record. This is the resume path: a binding that is already bound and
		// whose session answers ready has no forward edge to bound_ready, and an
		// observation must never DEMOTE a live binding either. Report the observed
		// readiness with the binding unchanged rather than inventing a write.
		//
		// The one thing that may still change is the recorded CONTROLLER. When the same
		// session's controller was replaced, the generation rotated and the binding kept
		// the dead one — and because there is no forward edge here, the bind above never
		// runs, which is how the live controller became permanently unfenceable. Repairing
		// the recorded identity is a re-anchor, not a transition: the state, the session,
		// and the fence semantics are all untouched, so the OLD generation is still
		// refused by every fence.
		if reanchorableGeneration(binding, observation) {
			// A failed CAS (another writer moved the row) is not an error: this path's
			// job is to report what is durable, so fall back to the unchanged binding
			// exactly as the no-write case does.
			reanchored, applied, reanchorErr := c.store.ReanchorWorkflowAttemptGeneration(
				ctx, binding.AttemptID, observation.SessionID, observation.ControllerGeneration,
				binding.Revision, binding.State, now,
			)
			if reanchorErr == nil && applied {
				return BindOutcome{
					Binding:    reanchored,
					ReasonCode: "generation_reanchored",
				}, nil
			}
		}
		return BindOutcome{
			Binding:    binding,
			ReasonCode: string(observation.Readiness),
		}, nil
	}
	updated, applied, err := c.store.BindWorkflowAttemptSession(
		ctx, binding.AttemptID, observation.SessionID, generation, targetState,
		binding.Revision, now,
	)
	if err != nil {
		return BindOutcome{}, err
	}
	if !applied {
		current, found, err := c.store.GetWorkflowAttemptBinding(ctx, binding.AttemptID)
		if err != nil || !found {
			return BindOutcome{Binding: updated}, err
		}
		return BindOutcome{Binding: current, ReasonCode: "binding_replayed"}, nil
	}
	// The session is recorded. Each repository's observed head is recorded beside
	// it, so a later reader can tell a verified worktree from a merely expected
	// one. This is a projection of the observation, never a repair: a mismatch is
	// recorded as a mismatch and the worktree is left exactly as it is.
	if err := c.recordRepoBindings(ctx, binding, observation, now); err != nil {
		return BindOutcome{Binding: updated, Spawned: true, ReasonCode: string(observation.Readiness)}, err
	}
	return BindOutcome{
		Binding:    updated,
		Spawned:    true,
		ReasonCode: string(observation.Readiness),
	}, nil
}

// repoStore returns the optional per-repository surface, or nil.
func (c *Coordinator) repoStore() RepoBindingStore {
	if store, ok := c.store.(RepoBindingStore); ok {
		return store
	}
	return nil
}

// repoGate 将本次实时观察与持久化 Repo 期望逐项比对。
// 只有无显式 Repo 绑定时不适用；单仓缺记录与多仓缺记录采用同一闭锁规则。
func (c *Coordinator) repoGate(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	observation ports.SessionObservation,
) (map[domain.WorkflowRepoKey]domain.WorkflowRepoBindingState, bool, error) {
	store := c.repoStore()
	if store == nil {
		return nil, false, nil
	}
	bindings, err := store.ListWorkflowAttemptRepoBindings(ctx, attemptID)
	if err != nil {
		return nil, false, err
	}
	states, gate := repoGateDecision(bindings, observation.Worktrees)
	return states, gate, nil
}

// repoGateDecision is the pure form of repoGate.
func repoGateDecision(
	bindings []domain.WorkflowAttemptRepoBinding,
	worktrees []ports.RepoWorktreeObservation,
) (map[domain.WorkflowRepoKey]domain.WorkflowRepoBindingState, bool) {
	if len(bindings) == 0 {
		return nil, false
	}
	// 持久化显式 Repo 绑定就是 M2 门禁依据，单仓缺行不能降级 Legacy。
	return classifyRepoBindings(bindings, worktrees), true
}

// classifyRepoBindings is the pure form of repoBindingStates.
func classifyRepoBindings(
	bindings []domain.WorkflowAttemptRepoBinding,
	worktrees []ports.RepoWorktreeObservation,
) map[domain.WorkflowRepoKey]domain.WorkflowRepoBindingState {
	byKey := make(map[domain.WorkflowRepoKey]ports.RepoWorktreeObservation, len(worktrees))
	for _, worktree := range worktrees {
		byKey[worktree.RepoKey] = worktree
	}
	states := make(map[domain.WorkflowRepoKey]domain.WorkflowRepoBindingState, len(bindings))
	for _, binding := range bindings {
		worktree, observed := byKey[binding.RepoKey]
		switch {
		case !observed || strings.TrimSpace(worktree.WorktreeRef) == "":
			states[binding.RepoKey] = domain.WorkflowRepoBindingStateMissing
		case binding.ProjectID != "" && worktree.ProjectID != binding.ProjectID:
			states[binding.RepoKey] = domain.WorkflowRepoBindingStateMismatch
		case strings.TrimSpace(worktree.HeadSHA) == "":
			states[binding.RepoKey] = domain.WorkflowRepoBindingStateUnknown
		case binding.ExpectedBaseSHA != "" && !strings.EqualFold(worktree.BaseSHA, binding.ExpectedBaseSHA):
			states[binding.RepoKey] = domain.WorkflowRepoBindingStateMismatch
		default:
			states[binding.RepoKey] = domain.WorkflowRepoBindingStateVerified
		}
	}
	return states
}

// blockedRepoCode reports why a bind may not land on bound_ready, or "" when every
// expected repository is verified or when the gate does not apply.
//
// Only two codes exist on purpose: an outright absent worktree is a different
// operator situation from a worktree that exists but cannot be proven to be the one
// the attempt asked for. Missing wins over mismatch, because "there is no worktree"
// is the more actionable of the two.
//
// gate=false 仅代表没有显式 Repo 期望，保留 Legacy 非 Workflow 会话兼容性。
func blockedRepoCode(
	states map[domain.WorkflowRepoKey]domain.WorkflowRepoBindingState,
	gate bool,
) string {
	if !gate {
		return ""
	}
	blocked := ""
	for _, state := range states {
		switch state {
		case domain.WorkflowRepoBindingStateVerified:
			continue
		case domain.WorkflowRepoBindingStateMissing:
			return RepoWorktreeMissingCode
		default:
			blocked = RepoWorktreeMismatchCode
		}
	}
	return blocked
}

// recordRepoBindings writes each expected repository's observed head and state.
//
// ObservedHeadSHA 来自本次已验证的 Git HEAD，不能用历史 CreationSHA 代替。
// 验证失败只更新证据状态，不重置、清理或切换用户工作区。
func (c *Coordinator) recordRepoBindings(
	ctx context.Context,
	binding domain.WorkflowAttemptBinding,
	observation ports.SessionObservation,
	now time.Time,
) error {
	var permitErr error
	ctx, permitErr = c.refreshExecutionContext(ctx, binding.AttemptID)
	if permitErr != nil {
		return permitErr
	}
	store := c.repoStore()
	if store == nil {
		return nil
	}
	bindings, err := store.ListWorkflowAttemptRepoBindings(ctx, binding.AttemptID)
	if err != nil || len(bindings) == 0 {
		return err
	}
	states, gate := repoGateDecision(bindings, observation.Worktrees)
	if !gate {
		// No evidence to project. Writing `missing` here would claim a fact the
		// session never reported, so the rows keep their recorded state (pending =
		// expected but unobserved).
		return nil
	}
	byKey := make(map[domain.WorkflowRepoKey]ports.RepoWorktreeObservation, len(observation.Worktrees))
	for _, worktree := range observation.Worktrees {
		byKey[worktree.RepoKey] = worktree
	}
	for _, repo := range bindings {
		worktree := byKey[repo.RepoKey]
		next := repo
		next.State = states[repo.RepoKey]
		switch next.State {
		case domain.WorkflowRepoBindingStateVerified, domain.WorkflowRepoBindingStateMismatch,
			domain.WorkflowRepoBindingStateUnknown:
			next.SessionWorktreeRef = worktree.WorktreeRef
			next.ObservedHeadSHA = worktree.HeadSHA
		default:
			next.SessionWorktreeRef = ""
			next.ObservedHeadSHA = ""
		}
		if _, _, err := store.RecordWorkflowAttemptRepoBinding(ctx, next, repo.Revision, now); err != nil {
			return err
		}
	}
	return nil
}

// ObserveSession reads current facts for a bound session and reports whether anything
// may be promoted.
//
// It never promotes. Promotion to running needs a confirmed_runtime observation, and
// this method's job is to say so explicitly rather than to infer it from "the session
// exists".
//
// When the binding names a session and a generation, the observation is fenced against
// both. A mismatch is reported as an error wrapping the matching sentinel, and the state
// returned with it is never promotable: evidence about a different controller must not be
// read as evidence about this one.
func (c *Coordinator) ObserveSession(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) (ports.SessionObservation, domain.WorkflowBindingState, error) {
	binding, found, err := c.store.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil {
		return ports.SessionObservation{}, "", err
	}
	if !found {
		return ports.SessionObservation{}, "", domain.ErrWorkflowBindingNotFound
	}
	if binding.SessionID == nil {
		return ports.SessionObservation{}, binding.State, nil
	}
	observation, err := c.gateway.InspectSession(ctx, *binding.SessionID)
	if err != nil {
		return ports.SessionObservation{}, binding.State, err
	}
	if err := observation.ValidateFence(*binding.SessionID, fencedGeneration(binding.ControllerGeneration)); err != nil {
		// Return the evidence AND the fence error: the caller can see what was observed
		// while the state stays the recorded, non-promotable one.
		return observation, binding.State, fmt.Errorf("observe session %s: %w", attemptID, err)
	}
	return observation, binding.State, nil
}

// Reconcile resolves an unknown spawn by asking the owner what actually exists, and
// never by spawning.
//
// The lookup is keyed on the stable spawn request id, so a session the gateway created
// before losing its answer is found and adopted. If nothing is found, the binding stays
// frozen for a human decision: absence of evidence after a lost answer is not proof
// that nothing happened.
func (c *Coordinator) Reconcile(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) (BindOutcome, error) {
	var permitErr error
	ctx, permitErr = c.executionContext(ctx, attemptID)
	if permitErr != nil {
		return BindOutcome{}, permitErr
	}
	binding, found, err := c.store.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil {
		return BindOutcome{}, err
	}
	if !found {
		return BindOutcome{}, domain.ErrWorkflowBindingNotFound
	}
	// spawn_requested is included because a crash between the claim CAS and the gateway
	// call leaves a row nothing else advances. Reconciling it is safe for the same reason
	// reconciling an unknown is: it looks for an existing session first and cannot create
	// one.
	switch binding.State {
	case domain.WorkflowBindingStateBoundReady,
		domain.WorkflowBindingStateRunning,
		domain.WorkflowBindingStateRecoveryUncertain,
		domain.WorkflowBindingStateSessionProvisioning:
		return c.reconcileKnownSession(ctx, binding, c.now().UTC())
	case domain.WorkflowBindingStateSpawnUnknown,
		domain.WorkflowBindingStateReconcileRequired,
		domain.WorkflowBindingStateSpawnRequested:
	default:
		return BindOutcome{Binding: binding, ReasonCode: "not_unknown"}, nil
	}
	now := c.now().UTC()

	observation, found, err := c.gateway.FindSpawn(ctx, binding.SpawnRequestID)
	if err != nil {
		// A failed lookup cannot clear an unknown; it deepens it.
		updated, _, transitionErr := c.store.TransitionWorkflowAttemptBinding(
			ctx, attemptID, domain.WorkflowBindingStateRecoveryUncertain,
			"reconcile_lookup_failed", binding.Revision, now,
		)
		if transitionErr != nil {
			return BindOutcome{}, transitionErr
		}
		return BindOutcome{Binding: updated, Frozen: true, ReasonCode: "reconcile_lookup_failed"}, nil
	}
	if !found {
		// Nothing to adopt and nothing to spawn. Freeze and let a human decide.
		updated, _, transitionErr := c.store.TransitionWorkflowAttemptBinding(
			ctx, attemptID, domain.WorkflowBindingStateReconcileRequired,
			"reconcile_no_session", binding.Revision, now,
		)
		if transitionErr != nil {
			return BindOutcome{}, transitionErr
		}
		return BindOutcome{Binding: updated, Frozen: true, ReasonCode: "reconcile_no_session"}, nil
	}
	return c.bindObservation(ctx, binding, observation, now)
}

// reconcileKnownSession 重新核验已经绑定的会话；曾经 ready 不是当前可恢复的证据。
// 失去证据时保留 SessionID 与工作区，冻结而不重建；成功时复用原 generation 重锚逻辑。
func (c *Coordinator) reconcileKnownSession(ctx context.Context, binding domain.WorkflowAttemptBinding, now time.Time) (BindOutcome, error) {
	ctx = bindingPermitContext(ctx, binding)
	if binding.SessionID == nil {
		return BindOutcome{Binding: binding, Frozen: true, ReasonCode: "reconcile_no_session"}, nil
	}
	observation, err := c.gateway.InspectSession(ctx, *binding.SessionID)
	reason := "reconcile_lookup_failed"
	if err == nil {
		reason = "session_readiness_unknown"
		if observation.SessionID == *binding.SessionID && !observation.Terminated && observation.Readiness.HoldsProvider() {
			states, gate, gateErr := c.repoGate(ctx, binding.AttemptID, observation)
			if gateErr != nil {
				return BindOutcome{}, gateErr
			}
			reason = blockedRepoCode(states, gate)
			if reason == "" {
				return c.bindObservation(ctx, binding, observation, now)
			}
		}
		if err := c.recordRepoBindings(ctx, binding, observation, now); err != nil {
			return BindOutcome{}, err
		}
	}
	if binding.State == domain.WorkflowBindingStateRecoveryUncertain {
		return BindOutcome{Binding: binding, Frozen: true, ReasonCode: reason}, nil
	}
	target := domain.WorkflowBindingStateRecoveryUncertain
	if binding.State == domain.WorkflowBindingStateSessionProvisioning {
		target = domain.WorkflowBindingStateReconcileRequired
	}
	updated, _, err := c.store.TransitionWorkflowAttemptBinding(ctx, binding.AttemptID, target, reason, binding.Revision, now)
	return BindOutcome{Binding: updated, Frozen: true, ReasonCode: reason}, err
}

// SendMessage delivers a message through the gateway. It requires a bound session and
// passes the caller's client message id straight through, so the Chat service's own
// idempotency does the deduplication.
//
// The request is validated and fenced against the binding before anything external
// happens: a send that cannot name its session, or that asked for steering, is refused
// rather than degraded into a different operation.
func (c *Coordinator) SendMessage(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	request ports.MessageRequest,
) (ports.MessageReceipt, error) {
	var permitErr error
	ctx, permitErr = c.actionContext(ctx, attemptID)
	if permitErr != nil {
		return ports.MessageReceipt{}, permitErr
	}
	binding, _, live, reason, err := c.LiveTarget(ctx, attemptID)
	if err != nil {
		return ports.MessageReceipt{}, err
	}
	// The live check comes FIRST: LiveTarget legitimately returns a binding with no session
	// (a deleted one), and dereferencing it before this check would panic.
	if !live {
		return ports.MessageReceipt{}, notLiveError(reason)
	}
	if request.Delivery != ports.MessageDeliverySend {
		// A steer routed through Send would silently open a new turn instead of joining the
		// one the caller named. Refuse the wrong path instead of changing what it asked for.
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: SendMessage delivers a send, not %q", ports.ErrExecutionRefused, request.Delivery)
	}
	if binding.SessionID == nil || *binding.SessionID != request.SessionID {
		// A message aimed at a different session is a cross-binding write.
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: the message session is not the bound session", ports.ErrExecutionRefused)
	}
	// The binding owns the generation; a caller cannot pick a different one. It is
	// stamped before Validate because the gateway refuses a send that names no
	// generation at all, and this request's generation is the binding's, not the
	// caller's.
	request.ControllerGeneration = binding.ControllerGeneration
	if err := request.Validate(); err != nil {
		return ports.MessageReceipt{}, err
	}
	return c.gateway.Send(ctx, request)
}

// SteerMessage delivers guidance into the turn the request names. It refuses a request
// whose delivery mode is not steer and never degrades to a send.
//
// The receipt is verified rather than trusted: a gateway that answers with a different
// provider turn (or a different generation) steered work the caller was not looking at,
// so that answer is ErrExecutionTargetMismatch, not success.
func (c *Coordinator) SteerMessage(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	request ports.MessageRequest,
) (ports.MessageReceipt, error) {
	var permitErr error
	ctx, permitErr = c.actionContext(ctx, attemptID)
	if permitErr != nil {
		return ports.MessageReceipt{}, permitErr
	}
	if request.Delivery != ports.MessageDeliverySteer {
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: SteerMessage requires delivery=steer, not %q", ports.ErrExecutionRefused, request.Delivery)
	}
	binding, _, live, reason, err := c.LiveTarget(ctx, attemptID)
	if err != nil {
		return ports.MessageReceipt{}, err
	}
	if !live {
		return ports.MessageReceipt{}, notLiveError(reason)
	}
	if binding.SessionID == nil || *binding.SessionID != request.SessionID {
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: the steer session is not the bound session", ports.ErrExecutionRefused)
	}
	if strings.TrimSpace(request.TargetProviderTurnID) == "" {
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: a steer must name the provider turn it joins", ports.ErrExecutionRefused)
	}
	if request.ControllerGeneration != "" && request.ControllerGeneration != binding.ControllerGeneration {
		// The caller observed a different controller. Steering under this binding's
		// generation would fence work it no longer owns.
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: the steer names generation %q, the binding owns %q",
			ports.ErrExecutionStaleGeneration, request.ControllerGeneration, binding.ControllerGeneration)
	}
	request.ControllerGeneration = binding.ControllerGeneration
	if err := request.Validate(); err != nil {
		return ports.MessageReceipt{}, err
	}

	receipt, err := c.gateway.Steer(ctx, request)
	if err != nil {
		return receipt, err
	}
	if !receipt.MatchesSteer(request) {
		// Not success: the guidance did not provably join the turn the caller named.
		return receipt, fmt.Errorf("%w: receipt did not match the requested turn",
			ports.ErrExecutionTargetMismatch)
	}
	return receipt, nil
}

// LiveTarget resolves the binding and reports whether a provider turn can be addressed on
// it right now.
//
// It is the single gated observation both M2-04's send/interrupt and M2-05's interventions
// consume, so no caller re-reads the raw gateway and reaches a different conclusion. A
// "live" answer requires ALL of:
//
//   - a binding that names a session and a controller generation, so there is something to
//     fence against;
//   - a binding state that may hold a session at or beyond bound_ready;
//   - a current gateway observation that passes SessionObservation.ValidateFence for
//     exactly that session and generation, and that is neither terminated nor exited; and
//   - readiness that actually holds a provider.
//
// A fence mismatch is NOT an error: "this is not my session any more" is an answer, and
// the caller branches on it. It is reported through reason instead.
//
// reason is a stable non-secret code explaining a false live answer:
// binding_not_found, no_session, generation_missing, binding_state_<state>,
// observation_failed, session_mismatch, generation_mismatch, session_terminated,
// readiness_<value>. It is empty when live is true.
func (c *Coordinator) LiveTarget(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) (domain.WorkflowAttemptBinding, ports.SessionObservation, bool, string, error) {
	binding, found, err := c.store.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil {
		return domain.WorkflowAttemptBinding{}, ports.SessionObservation{}, false, "", err
	}
	if !found {
		return domain.WorkflowAttemptBinding{}, ports.SessionObservation{}, false,
			"binding_not_found", domain.ErrWorkflowBindingNotFound
	}
	if binding.SessionID == nil {
		// Includes the post-deletion reconcile_required state: no session, no target.
		return binding, ports.SessionObservation{}, false, "no_session", nil
	}
	if fencedGeneration(binding.ControllerGeneration) == "" {
		// Without a generation there is no fence, so no observation can be attributed to
		// the controller this binding claims to own.
		return binding, ports.SessionObservation{}, false, "generation_missing", nil
	}
	if binding.State != domain.WorkflowBindingStateBoundReady &&
		binding.State != domain.WorkflowBindingStateRunning &&
		binding.State != domain.WorkflowBindingStateStopRequested &&
		binding.State != domain.WorkflowBindingStateRecoveryUncertain {
		return binding, ports.SessionObservation{}, false,
			"binding_state_" + string(binding.State), nil
	}
	observation, err := c.gateway.InspectSession(ctx, *binding.SessionID)
	if err != nil {
		// 观察失败只禁止本次操作，不证明进程死亡；保留绑定并通过 reason 返回闭锁原因。
		return binding, ports.SessionObservation{}, false, "observation_failed", nil //nolint:nilerr // LiveTarget 的拒绝结果由 live/reason 表示，不能把未知观察改成终止事实。
	}
	if err := observation.ValidateFence(*binding.SessionID, fencedGeneration(binding.ControllerGeneration)); err != nil {
		// The evidence describes a different session, a replaced controller, or an
		// already-terminated session. Acting on it would fence the wrong owner.
		return binding, observation, false, fenceReason(err), nil
	}
	if !observation.Readiness.HoldsProvider() {
		return binding, observation, false, "readiness_" + string(observation.Readiness), nil
	}
	// 发送/干预同样核对本 Attempt 的授权仓库集合，不以 Controller 存活替代归属。
	states, gate, err := c.repoGate(ctx, attemptID, observation)
	if err != nil {
		return binding, observation, false, "repo_observation_failed", err
	}
	if reason := blockedRepoCode(states, gate); reason != "" {
		return binding, observation, false, reason, nil
	}
	return binding, observation, true, "", nil
}

// fenceReason maps a ValidateFence failure onto its stable reason code. Anything
// unrecognized still refuses: an unknown fence failure is never live.
func fenceReason(err error) string {
	switch {
	case errors.Is(err, ports.ErrExecutionSessionMismatch):
		return "session_mismatch"
	case errors.Is(err, ports.ErrExecutionStaleGeneration):
		return "generation_mismatch"
	case errors.Is(err, ports.ErrExecutionTerminated):
		return "session_terminated"
	default:
		return "session_mismatch"
	}
}

// notLiveError reports a refused action against a binding that is not live, naming the
// stable reason without leaking identifiers.
func notLiveError(reason string) error {
	if reason == "" {
		reason = "not_live"
	}
	return fmt.Errorf("%w: %s", ports.ErrExecutionRefused, reason)
}

// InterruptTurn asks the live controller to stop, and reports an uncertain stop as
// uncertain rather than retrying it.
//
// clientMessageID is stamped onto the request so a retried stop is idempotent at the
// gateway boundary too, instead of interrupting a second turn.
func (c *Coordinator) InterruptTurn(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	clientMessageID string,
) (ports.InterruptReceipt, error) {
	var permitErr error
	ctx, permitErr = c.actionContext(ctx, attemptID)
	if permitErr != nil {
		return ports.InterruptReceipt{}, permitErr
	}
	binding, _, live, reason, err := c.LiveTarget(ctx, attemptID)
	if err != nil {
		return ports.InterruptReceipt{}, err
	}
	if !live {
		// Asking a dead or unverified session to stop would mint a false receipt.
		return ports.InterruptReceipt{}, notLiveError(reason)
	}
	return c.gateway.Interrupt(ctx, ports.InterruptRequest{
		SessionID:            *binding.SessionID,
		ControllerGeneration: binding.ControllerGeneration,
		ClientMessageID:      clientMessageID,
	})
}

// resumeEligible reports whether a binding state may be brought back. Only states that
// already hold a session are resumable: resume names an existing session and must never be
// the way a session is created.
func resumeEligible(state domain.WorkflowBindingState) bool {
	switch state {
	case domain.WorkflowBindingStateSessionProvisioning, domain.WorkflowBindingStateBoundReady,
		domain.WorkflowBindingStateRunning, domain.WorkflowBindingStateRecoveryUncertain:
		return true
	default:
		return false
	}
}

// Resume brings a bound session back and reports what the gateway observed. It never
// spawns: ResumeRequest only names an existing session.
func (c *Coordinator) Resume(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) (BindOutcome, error) {
	var permitErr error
	ctx, permitErr = c.actionContext(ctx, attemptID)
	if permitErr != nil {
		return BindOutcome{}, permitErr
	}
	if c.store == nil || c.gateway == nil {
		return BindOutcome{}, apierrUnavailable()
	}
	binding, found, err := c.store.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil {
		return BindOutcome{}, err
	}
	if !found {
		return BindOutcome{}, domain.ErrWorkflowBindingNotFound
	}
	if binding.SessionID == nil || strings.TrimSpace(string(*binding.SessionID)) == "" {
		return BindOutcome{Binding: binding, ReasonCode: "no_session"}, nil
	}
	if !resumeEligible(binding.State) {
		return BindOutcome{Binding: binding, ReasonCode: "not_resumable"}, nil
	}
	if c.allowAction != nil {
		if err := c.allowAction(ActionResume); err != nil {
			return BindOutcome{Binding: binding, Frozen: true, ReasonCode: "resume_refused"}, err
		}
	}
	now := c.now().UTC()

	observation, resumeErr := c.gateway.Resume(ctx, ports.ResumeRequest{
		SessionID: *binding.SessionID, SpawnKey: binding.SpawnRequestID,
	})
	// Manager 接受恢复时会先推进 Admission 代际；只跟随本次同任期执行的快照，
	// 才能记录成功重锚或失败观测，不能拿启动前 revision 回写。
	ctx, permitErr = c.refreshExecutionContext(ctx, attemptID)
	if permitErr != nil {
		return BindOutcome{}, permitErr
	}
	if resumeErr != nil {
		// A failed resume is not proof the previous session is gone, and it is not proof
		// it came back. Freeze instead of guessing, but only when the transition is legal:
		// a terminal binding is never moved backwards. The recorded outcome is the answer,
		// so this mirrors recordSpawnFailure and reports no error.
		if binding.State.CanTransitionTo(domain.WorkflowBindingStateRecoveryUncertain) {
			updated, applied, transitionErr := c.store.TransitionWorkflowAttemptBinding(
				ctx, attemptID, domain.WorkflowBindingStateRecoveryUncertain,
				"resume_failed", binding.Revision, now,
			)
			if transitionErr == nil && applied {
				return BindOutcome{
					Binding: updated, Frozen: true, ReasonCode: "resume_failed",
				}, nil
			}
		}
		return BindOutcome{Binding: binding, Frozen: true, ReasonCode: "resume_failed"}, nil
	}
	return c.bindObservation(ctx, binding, observation, now)
}

// MarkStopOutcome advances the binding so it agrees with a stop that already happened.
//
// It never moves a terminal binding backwards: a confirmed stop stays confirmed, and a
// failed attempt stays failed. When the request asks for a transition the graph forbids,
// the current binding is returned unchanged with reason stop_transition_refused rather than
// writing an illegal edge.
func (c *Coordinator) MarkStopOutcome(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	confirmed bool,
	unknown bool,
) (domain.WorkflowAttemptBinding, error) {
	var permitErr error
	ctx, permitErr = c.executionContext(ctx, attemptID)
	if permitErr != nil {
		return domain.WorkflowAttemptBinding{}, permitErr
	}
	if c.store == nil {
		return domain.WorkflowAttemptBinding{}, apierrUnavailable()
	}
	binding, found, err := c.store.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil {
		return domain.WorkflowAttemptBinding{}, err
	}
	if !found {
		return domain.WorkflowAttemptBinding{}, domain.ErrWorkflowBindingNotFound
	}

	var next domain.WorkflowBindingState
	var reasonCode string
	switch {
	case unknown:
		next, reasonCode = domain.WorkflowBindingStateStopUnknown, "stop_unknown"
	case confirmed:
		next, reasonCode = domain.WorkflowBindingStateStopConfirmed, "stop_confirmed"
	default:
		// Nothing was in flight: the stop is trivially satisfied. Recording that is not
		// the same as claiming an interrupt happened, which is why the reason differs.
		next, reasonCode = domain.WorkflowBindingStateStopConfirmed, "stop_nothing_to_stop"
	}
	if binding.State == next {
		// Already recorded. This is a replay, not a refusal.
		return binding, nil
	}
	if binding.State.Terminal() {
		return binding, nil
	}
	// A live binding reaches a REAL stop outcome through stop_requested, because the graph
	// has no direct bound_ready/running → stop_confirmed edge: that edge would let a caller
	// claim a stop was confirmed without ever recording that one was requested. When the
	// receipt has already arrived, the two steps are one durable fact and this method
	// records both, in order, inside the caller's request.
	//
	// The nothing-to-stop case is deliberately excluded. Nothing was in flight, so nothing
	// stopped: leaving a live binding at bound_ready is the honest record, and moving it to
	// stop_confirmed would claim a session ended that is still running.
	actualStop := confirmed || unknown
	if !binding.State.CanTransitionTo(next) {
		if !actualStop ||
			!binding.State.CanTransitionTo(domain.WorkflowBindingStateStopRequested) ||
			!domain.WorkflowBindingStateStopRequested.CanTransitionTo(next) {
			return binding, nil
		}
		pending, applied, err := c.store.TransitionWorkflowAttemptBinding(
			ctx, attemptID, domain.WorkflowBindingStateStopRequested,
			"stop_requested", binding.Revision, c.now().UTC(),
		)
		if err != nil || !applied {
			return binding, err
		}
		binding = pending
		ctx = bindingPermitContext(ctx, binding)
	}

	updated, applied, err := c.store.TransitionWorkflowAttemptBinding(
		ctx, attemptID, next, reasonCode, binding.Revision, c.now().UTC(),
	)
	if err != nil {
		return binding, err
	}
	if !applied {
		// Another writer won the CAS. Report what is durable now rather than the stale row.
		current, found, getErr := c.store.GetWorkflowAttemptBinding(ctx, attemptID)
		if getErr != nil {
			return binding, getErr
		}
		if found {
			return current, nil
		}
		return binding, nil
	}
	return updated, nil
}

// apierrUnavailable reports a coordinator built without its collaborators. It is a
// programming error, not a user-facing condition, so it is a plain error.
func apierrUnavailable() error {
	return errors.New("execution coordinator requires a binding store and a gateway")
}

// terminalOutcome reports what a binding that already existed means for this call,
// independent of how it got there. handled=false means the caller may continue the
// spawn sequence from it.
func terminalOutcome(binding domain.WorkflowAttemptBinding) (BindOutcome, bool) {
	switch binding.State {
	case domain.WorkflowBindingStateFailedConfirmed:
		return BindOutcome{Binding: binding, Frozen: true, ReasonCode: "binding_failed_confirmed"}, true
	case domain.WorkflowBindingStateStopRequested, domain.WorkflowBindingStateStopConfirmed,
		domain.WorkflowBindingStateStopUnknown:
		return BindOutcome{Binding: binding, Frozen: true, ReasonCode: string(binding.State)}, true
	}
	if binding.State.HasSession() {
		return BindOutcome{Binding: binding, ReasonCode: "binding_replayed"}, true
	}
	return BindOutcome{}, false
}
