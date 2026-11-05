package execution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// LocalGateway is the production ports.ExecutionGateway.
//
// It composes owners that already existed before M2 and adds no session, chat, or
// provider machinery of its own:
//
//   - the Session Manager creates a session, restores a terminated one, and resumes a
//     live one whose agent exited;
//   - the Chat service owns turns, messages, steering, and interrupt;
//   - the SQLite store holds the durable session row this gateway reads.
//
// Everything here is a projection or a request forwarded to one of those owners. The
// two rules from ports/execution.go are enforced structurally rather than by
// convention:
//
//  1. Readiness is projected from durable facts plus one live-controller probe, so a
//     provisioned row never reads as "the provider is working".
//  2. A spawn whose answer is lost re-reads the idempotency key before deciding. A row
//     that exists and is committed is returned as success; a row that exists and is
//     not committed, or no row at all, is ErrSpawnOutcomeUnknown. Neither path ever
//     spawns a second time.
//
// The gateway deliberately has no Steer fallback to Send: a steer that cannot be
// delivered into the named turn reports that, because silently opening a new turn
// would put guidance on work the caller was not looking at.

// SessionSpawner creates the session row and its workspace. It is the Session
// Manager's own Spawn, which returns the durable record rather than the API view.
type SessionSpawner interface {
	Spawn(context.Context, ports.SpawnConfig) (domain.SessionRecord, int, int, error)
}

// SessionReader is the durable session read surface.
type SessionReader interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	GetSessionByClientRequestID(context.Context, string) (domain.SessionRecord, bool, error)
}

// SessionResumer brings an existing session back: RestoreWithMode for a terminated
// session, ResumeAgentWithMode for one whose agent exited inside a live session.
type SessionResumer interface {
	ResumeAgentWithMode(context.Context, domain.SessionID) (sessionmanager.RestoreResult, error)
	RestoreWithMode(context.Context, domain.SessionID) (sessionmanager.RestoreResult, error)
}

// ClientRequestCommitter marks a client request id as replayable. It exists so a
// successful spawn does not depend on the caller remembering to commit.
type ClientRequestCommitter interface {
	CommitClientRequestSession(context.Context, domain.SessionID) error
}

// SessionWorktreeReader reports the per-repository worktrees a session owns. It is
// optional: an unwired reader leaves the observation's worktree list empty rather
// than implying the session has none.
type SessionWorktreeReader interface {
	ListSessionWorktrees(context.Context, domain.SessionID) ([]domain.SessionWorktreeRecord, error)
}

// SessionWorktreeObserver 由工作区所有者验证持久化归属，并返回本次 Git 观察；
// 空列表仅代表已确认的 Legacy 会话，读取或验证失败必须返回错误。
type SessionWorktreeObserver interface {
	ObserveSessionWorktrees(context.Context, domain.SessionID) ([]ports.RepoWorktreeObservation, error)
}

// ChatTurnReader resolves the durable AO turn behind a provider turn.
//
// The gateway needs it because an "applied" intervention receipt requires BOTH ids: the
// provider's turn proves the guidance landed, and AO's own turn row proves which durable
// turn absorbed it. Reporting only the provider id leaves the receipt unable to authorize
// anything, so a successful steer would be unreachable in the shipped stack.
//
// The signatures match *sqlite.Store verbatim rather than adding a bool: the store reports
// absence as an error (ErrConversationNotFound / domain.ErrNoConversationTurn), and a
// second "found" channel would let a caller ignore the error and treat a failure as empty.
type ChatTurnReader interface {
	ConversationForSession(ctx context.Context, session domain.SessionID) (domain.ConversationRecord, error)
	TurnByProviderID(ctx context.Context, conversationID, providerTurnID string) (domain.ConversationTurn, error)
}

// ChatSender is the Chat service surface used for ordinary sends and stops.
//
// It is a narrow local interface on purpose: the gateway needs four methods, and
// binding to the whole service would make every fake in this package carry a
// controller registry it never uses.
//
// The pinned forms are not optional here. A gateway that could only call Send would
// have no way to act on the generation it observed: it would read a generation, then
// hand the message to whichever controller held the session by the time the call
// landed. The fence has to be evaluated by the owner of the controller, under the
// lock that serialises dispatch, which is exactly what the pinned entry points do.
type ChatSender interface {
	Send(ctx context.Context, id domain.SessionID, msg ports.ChatUserMessage) (domain.ConversationTurn, error)
	Interrupt(ctx context.Context, id domain.SessionID) error
	SendPinned(
		ctx context.Context,
		id domain.SessionID,
		expectedGeneration string,
		msg ports.ChatUserMessage,
	) (domain.ConversationTurn, error)
	InterruptPinned(ctx context.Context, id domain.SessionID, expectedGeneration string) error
}

// ChatControllers reports whether a session has a live Chat controller and lets the
// gateway read that controller's generation. The generation is what a steer receipt
// is fenced against, so reading the live value (rather than only the durable
// metadata) is the point.
type ChatControllers interface {
	HasLiveChatController(domain.SessionID) bool
	Controller(domain.SessionID) (*chatsvc.Controller, error)
}

// pinnedSteerer is satisfied by *chatsvc.Service. It exists so the gateway can be
// handed the steer entry point directly when the chat service implements it.
type pinnedSteerer interface {
	SteerPinned(
		ctx context.Context,
		id domain.SessionID,
		expectedGeneration, providerTurnID string,
		msg ports.ChatUserMessage,
	) (chatsvc.SteerResult, error)
}

// LocalGatewayDeps are the gateway's injected owners.
//
// Sessions and Records are separate fields because they are separate owners in
// production: the Session Manager spawns and resumes, the store reads rows. A test
// fake may satisfy both with one value.
type LocalGatewayDeps struct {
	// Sessions spawns and resumes sessions. Nil makes Spawn and Resume report
	// ErrExecutionUnsupported; nothing else in the gateway depends on it.
	Sessions SessionSpawner
	// Resumer is the Session Manager's restore/resume surface.
	Resumer SessionResumer
	// Records is the durable session reader.
	Records SessionReader
	// Commits marks a spawn request id replayable. Optional.
	Commits ClientRequestCommitter
	// Worktrees is the optional per-repository workspace reader.
	Worktrees SessionWorktreeReader
	// Turns resolves the durable AO turn behind a provider turn, so a steer receipt can
	// carry both ids. Optional: without it a steer reports the provider turn only and
	// leaves the applied decision to the caller.
	Turns ChatTurnReader

	// Chats delivers messages and stops. Nil makes Send and Interrupt report
	// ErrExecutionUnsupported.
	Chats ChatSender
	// Controllers is the optional live-controller probe. Without it every session
	// reads as provisioning, because nothing can prove a controller is running.
	Controllers ChatControllers

	// SteerPinned is the pinned steer entry point. When nil, Chats is asked whether
	// it implements SteerPinned; when neither is available Steer reports
	// ErrExecutionUnsupported, and WithSteerPinned can install it later.
	SteerPinned SteerPinnedFunc

	// Capabilities records observed capability evidence. Optional.
	Capabilities ports.CapabilityRecorder

	// Clock defaults to time.Now.
	Clock func() time.Time
	// Log defaults to slog.Default.
	Log *slog.Logger
}

// SteerPinnedFunc is the pinned-steer entry point: chat.Service.SteerPinned.
type SteerPinnedFunc func(
	ctx context.Context,
	id domain.SessionID,
	expectedGeneration, providerTurnID string,
	msg ports.ChatUserMessage,
) (chatsvc.SteerResult, error)

// LocalGateway implements ports.ExecutionGateway over AO's own owners.
type LocalGateway struct {
	sessions    SessionSpawner
	resumer     SessionResumer
	records     SessionReader
	commits     ClientRequestCommitter
	worktrees   SessionWorktreeReader
	turns       ChatTurnReader
	chats       ChatSender
	controllers ChatControllers
	// steerMu guards steerPinned because the daemon installs it after construction
	// (both the chat service and the gateway must exist before either can be wired).
	steerMu      sync.RWMutex
	steerPinned  SteerPinnedFunc
	capabilities ports.CapabilityRecorder
	now          func() time.Time
	log          *slog.Logger
}

var _ ports.ExecutionGateway = (*LocalGateway)(nil)

// NewLocalGateway builds the gateway. It accepts missing collaborators on purpose:
// a partially wired daemon must refuse the operations it cannot perform rather than
// panic, and the tests use the same constructor the daemon does.
func NewLocalGateway(deps LocalGatewayDeps) *LocalGateway {
	g := &LocalGateway{
		sessions:     deps.Sessions,
		resumer:      deps.Resumer,
		records:      deps.Records,
		commits:      deps.Commits,
		worktrees:    deps.Worktrees,
		turns:        deps.Turns,
		chats:        deps.Chats,
		controllers:  deps.Controllers,
		steerPinned:  deps.SteerPinned,
		capabilities: deps.Capabilities,
		now:          deps.Clock,
		log:          deps.Log,
	}
	if g.now == nil {
		g.now = time.Now
	}
	if g.log == nil {
		g.log = slog.Default()
	}
	if g.steerPinned == nil {
		if steerer, ok := deps.Chats.(pinnedSteerer); ok {
			g.steerPinned = steerer.SteerPinned
		}
	}
	return g
}

// WithSteerPinned installs the pinned-steer entry point after construction.
//
// It exists because of a boot-order fact: the gateway is built from the Session
// Manager, and the chat service is wired into the daemon in the same step, so a caller
// that wants chat.Service.SteerPinned has to attach it once both exist. A nil steerer is
// not a permissive default — Steer refuses with ErrExecutionUnsupported and never falls
// back to Send, because a send opens a new turn instead of joining the running one.
func (g *LocalGateway) WithSteerPinned(fn SteerPinnedFunc) *LocalGateway {
	g.steerMu.Lock()
	defer g.steerMu.Unlock()
	g.steerPinned = fn
	return g
}

// steerEntryPoint reads the installed steer func under the lock.
func (g *LocalGateway) steerEntryPoint() SteerPinnedFunc {
	g.steerMu.RLock()
	defer g.steerMu.RUnlock()
	return g.steerPinned
}

// ProjectReadiness derives the readiness a session actually has from its durable row
// and one live fact.
//
// The order is the contract: a terminated or failed session can never read as ready,
// a provisioning one masks everything else, and an exited activity state means the
// agent process ended — which is not success. The `live` flag is the caller's
// HasLiveChatController answer; a TUI session has no chat-addressed controller at
// all, so it is never ready here and the caller refuses it.
func ProjectReadiness(rec domain.SessionRecord, live bool) ports.SessionReadiness {
	if rec.IsTerminated {
		return ports.SessionReadinessExited
	}
	if rec.ProvisionState.WithDefault() == domain.SessionProvisionFailed {
		return ports.SessionReadinessFailed
	}
	if rec.ProvisionState.IsProvisioning() {
		return ports.SessionReadinessProvisioning
	}
	if rec.Activity.State == domain.ActivityExited {
		return ports.SessionReadinessExited
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		// A TUI session's provider runs in a terminal runtime, which this gateway
		// cannot address. Reporting "ready" would let a chat-addressed send through
		// and fail far from the caller.
		return ports.SessionReadinessProvisioning
	}
	if live && rec.Metadata.ControllerGeneration != "" {
		return ports.SessionReadinessReady
	}
	return ports.SessionReadinessProvisioning
}

// InspectSession implements ports.ExecutionGateway.
func (g *LocalGateway) InspectSession(
	ctx context.Context,
	sessionID domain.SessionID,
) (ports.SessionObservation, error) {
	rec, found, err := g.readSession(ctx, sessionID)
	if err != nil {
		return ports.SessionObservation{}, err
	}
	if !found {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: %s", ports.ErrExecutionSessionNotFound, sessionID)
	}
	return g.observe(ctx, rec)
}

// FindSpawn implements ports.ExecutionGateway.
//
// It answers from the store's own client-request row and reports what is stored: a
// found but uncommitted request is returned as committed=false so the caller decides,
// because "a row exists" and "the spawn finished" are different facts.
func (g *LocalGateway) FindSpawn(
	ctx context.Context,
	clientRequestID string,
) (ports.SessionObservation, bool, error) {
	if strings.TrimSpace(clientRequestID) == "" {
		return ports.SessionObservation{}, false, nil
	}
	if g.records == nil {
		return ports.SessionObservation{}, false, fmt.Errorf(
			"%w: session records are not wired", ports.ErrExecutionUnsupported)
	}
	rec, found, err := g.records.GetSessionByClientRequestID(ctx, clientRequestID)
	if err != nil {
		return ports.SessionObservation{}, false, err
	}
	if !found {
		return ports.SessionObservation{}, false, nil
	}
	observation, err := g.observe(ctx, rec)
	if err != nil {
		return ports.SessionObservation{}, false, err
	}
	return observation, true, nil
}

// Spawn implements ports.ExecutionGateway.
func (g *LocalGateway) Spawn(
	ctx context.Context,
	request ports.SpawnRequest,
) (ports.SessionObservation, error) {
	if err := request.Validate(); err != nil {
		return ports.SessionObservation{}, err
	}
	// The coordinator passes SpawnKey; deriving one here would hide the fact that a
	// caller lost it, and a derived key is exactly the kind of second source of truth
	// that mints a duplicate session.
	if strings.TrimSpace(request.SpawnKey) == "" {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: spawnKey is required", ports.ErrExecutionRefused)
	}
	if g.sessions == nil || g.records == nil {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: the session manager is not wired", ports.ErrExecutionUnsupported)
	}

	// Idempotency first. This is the whole retry rule: a key that already produced a
	// committed row is answered from that row, and a key whose spawn is still in
	// flight is answered as unknown rather than duplicated.
	if existing, found, err := g.records.GetSessionByClientRequestID(ctx, request.SpawnKey); err != nil {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: read spawn request: %v", ports.ErrSpawnOutcomeUnknown, err)
	} else if found {
		if !existing.ClientRequestCommitted {
			return ports.SessionObservation{}, fmt.Errorf(
				"%w: a spawn for this request id is still in flight", ports.ErrSpawnOutcomeUnknown)
		}
		return g.observe(ctx, existing)
	}

	cfg := ports.SpawnConfig{
		ProjectID:         request.ProjectID,
		Kind:              domain.KindWorker,
		Harness:           request.Harness,
		RequestedMode:     request.Mode,
		Prompt:            request.Prompt,
		ClientRequestID:   request.SpawnKey,
		ClientRequestHash: request.SpawnHash,
		Async:             request.Async,
		// Workflow-originated work is unattended by definition: nobody is watching a
		// workflow attempt the way a person watches the session they just started.
		CreatedBy: domain.SessionCreatedByAutomation,
		// The attempt's repository scope becomes the session's worktree list, and
		// its identity becomes the tool server's identity. An empty Repos keeps the
		// single-project behaviour.
		Repos:         ports.SpawnRepos(request.Repos),
		WorkflowSpawn: request.Workflow,
	}

	rec, _, _, err := g.sessions.Spawn(ctx, cfg)
	if err != nil {
		return g.spawnFailure(ctx, request, err)
	}
	return g.commitAndObserve(ctx, rec)
}

// spawnFailure classifies a spawn error without ever guessing that nothing happened.
//
// The split is deliberate:
//
//   - an error the owner provably raises before any durable state (missing or unknown
//     harness, missing binary, unsupported model, refused chat admission, a client
//     request id that belongs to different content) is a refusal;
//   - everything else — including cancellation, a deadline, and an unclassified
//     failure — may have left a session row behind, so it is an unknown outcome.
//
// Before returning "unknown" it re-reads the idempotency key on a context that
// survives the caller's cancellation: if the row now exists and is committed, the
// spawn did succeed and the caller is told so.
func (g *LocalGateway) spawnFailure(
	ctx context.Context,
	request ports.SpawnRequest,
	cause error,
) (ports.SessionObservation, error) {
	if g.isRefusal(cause) {
		return ports.SessionObservation{}, fmt.Errorf("%w: %v", ports.ErrExecutionRefused, cause)
	}
	lookupCtx := context.WithoutCancel(ctx)
	if observation, found, err := g.lookupCommitted(lookupCtx, request.SpawnKey); err == nil && found {
		g.log.Info("spawn answer was lost but the session exists; reporting it",
			"reason", "spawn_outcome_recovered")
		return observation, nil
	}
	return ports.SessionObservation{}, fmt.Errorf("%w: %v", ports.ErrSpawnOutcomeUnknown, cause)
}

// isRefusal reports whether err proves the spawn produced no durable state.
func (g *LocalGateway) isRefusal(err error) bool {
	for _, refusal := range spawnRefusals {
		if errors.Is(err, refusal) {
			return true
		}
	}
	return false
}

// spawnRefusals are the sentinels the Session Manager and the Chat admission path
// raise before they create a row. Anything not listed here is treated as unknown, so
// a new error added upstream fails closed instead of being read as "nothing ran".
var spawnRefusals = []error{
	ports.ErrAgentBinaryNotFound,
	ports.ErrChatUnsupported,
	ports.ErrChatDriverUnavailable,
	ports.ErrChatDriverIncompatible,
	ports.ErrChatAuthRequired,
	sessionmanager.ErrMissingHarness,
	sessionmanager.ErrUnknownHarness,
	sessionmanager.ErrProjectNotResolvable,
	sessionmanager.ErrUnsupportedModel,
	sessionmanager.ErrArtifactOutputUnavailable,
	sessionmanager.ErrScratchBranchUnsupported,
	sessionmanager.ErrHarnessInstallActive,
	sessionmanager.ErrClientRequestConflict,
}

// lookupCommitted resolves one spawn request id, reporting found=true only for a row
// that is committed.
func (g *LocalGateway) lookupCommitted(
	ctx context.Context,
	spawnKey string,
) (ports.SessionObservation, bool, error) {
	rec, found, err := g.records.GetSessionByClientRequestID(ctx, spawnKey)
	if err != nil || !found {
		return ports.SessionObservation{}, false, err
	}
	if !rec.ClientRequestCommitted {
		return ports.SessionObservation{}, false, nil
	}
	observation, err := g.observe(ctx, rec)
	if err != nil {
		return ports.SessionObservation{}, false, err
	}
	return observation, true, nil
}

// commitAndObserve commits the client request (when the manager did not) and then
// reads the full observation. A failed commit is not a failed spawn: the session
// exists, so the observation is returned with committed=false and the caller decides.
func (g *LocalGateway) commitAndObserve(
	ctx context.Context,
	rec domain.SessionRecord,
) (ports.SessionObservation, error) {
	if !rec.ClientRequestCommitted && g.commits != nil {
		if err := g.commits.CommitClientRequestSession(ctx, rec.ID); err != nil {
			g.log.Warn("commit spawn client request failed; reporting the session uncommitted",
				"reason", "client_request_commit_failed")
		}
	}
	return g.InspectSession(ctx, rec.ID)
}

// Send implements ports.ExecutionGateway.
//
// The fence is the point of this method, not a preamble to it. A send opens a new
// turn, and that turn belongs to whichever controller owns the session, so the
// gateway must prove the controller it hands the message to is the one the caller
// observed:
//
//  1. request.Validate requires a non-empty ControllerGeneration, so a caller that
//     cannot be fenced never reaches the store;
//  2. the observation from requireReady must itself pass ValidateFence for the
//     caller's session and generation — an observation with no generation is a
//     refusal, because "I found no controller id" is evidence about nothing; and
//  3. the chat service applies the same comparison again inside the controller under
//     sendMu, so a controller swapped between (2) and the dispatch is refused there
//     rather than silently accepted.
//
// Step (3) is why this call goes to SendPinned and never to Send: only the controller
// can make the comparison and the dispatch one atomic step.
func (g *LocalGateway) Send(
	ctx context.Context,
	request ports.MessageRequest,
) (ports.MessageReceipt, error) {
	if err := request.Validate(); err != nil {
		return ports.MessageReceipt{}, err
	}
	if request.Delivery != ports.MessageDeliverySend {
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: delivery=steer must use Steer, not Send", ports.ErrExecutionRefused)
	}
	if g.chats == nil {
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: the chat service is not wired", ports.ErrExecutionUnsupported)
	}

	observation, err := g.requireReady(ctx, request.SessionID)
	if err != nil {
		return ports.MessageReceipt{}, err
	}
	if err := observation.ValidateFence(request.SessionID, request.ControllerGeneration); err != nil {
		return ports.MessageReceipt{}, err
	}

	turn, err := g.chats.SendPinned(ctx, request.SessionID, request.ControllerGeneration,
		ports.ChatUserMessage{
			Text:            request.Text,
			ClientMessageID: request.ClientMessageID,
			Origin:          originOrDefault(request.Origin),
			SenderSessionID: string(request.SenderSessionID),
			// A workflow sends direction to a worker. Attributing it to a human would
			// make the timeline lie about who asked for the work.
			AuthoredByUser: false,
		})
	if err != nil {
		return ports.MessageReceipt{}, mapChatError(err)
	}

	return ports.MessageReceipt{
		SessionID:       request.SessionID,
		ClientMessageID: request.ClientMessageID,
		// An empty turn id is the Chat service's "nothing new happened": the client
		// message id was already delivered, which is a duplicate rather than a send.
		Duplicate:            turn.ID == "",
		Queued:               turn.State == domain.TurnStateQueued,
		TurnID:               turn.ID,
		ProviderTurnID:       turn.ProviderTurnID,
		Steered:              false,
		TargetProviderTurnID: "",
		// The generation that actually produced this receipt is the one the pinned call
		// proved, not a value re-read after the fact. A second read could observe a
		// replacement controller and attribute this turn to it.
		ControllerGeneration: request.ControllerGeneration,
	}, nil
}

// Steer implements ports.ExecutionGateway.
//
// It refuses to report success for anything but the exact turn the caller named. A
// provider that absorbed the guidance into a different turn did something the caller
// will not be looking at, so that answer is ErrExecutionTargetMismatch rather than a
// receipt the caller has to double-check.
func (g *LocalGateway) Steer(
	ctx context.Context,
	request ports.MessageRequest,
) (ports.MessageReceipt, error) {
	if err := request.Validate(); err != nil {
		return ports.MessageReceipt{}, err
	}
	if request.Delivery != ports.MessageDeliverySteer {
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: delivery=send must use Send, not Steer", ports.ErrExecutionRefused)
	}
	steerPinned := g.steerEntryPoint()
	if steerPinned == nil {
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: steering is not available in this build", ports.ErrExecutionUnsupported)
	}

	observation, err := g.requireReady(ctx, request.SessionID)
	if err != nil {
		return ports.MessageReceipt{}, err
	}
	// The fence comes before the call. Acting on a generation the caller observed
	// after the controller was replaced would steer turns this controller does not
	// own.
	if observation.ControllerGeneration != request.ControllerGeneration {
		return ports.MessageReceipt{}, fmt.Errorf(
			"%w: observed generation does not match the request", ports.ErrExecutionStaleGeneration)
	}

	result, err := steerPinned(
		ctx, request.SessionID, request.ControllerGeneration, request.TargetProviderTurnID,
		ports.ChatUserMessage{
			Text:            request.Text,
			ClientMessageID: request.ClientMessageID,
			Origin:          originOrDefault(request.Origin),
			SenderSessionID: string(request.SenderSessionID),
			AuthoredByUser:  false,
		},
	)
	if err != nil {
		return ports.MessageReceipt{}, mapSteerError(err)
	}
	if result.ProviderTurnID != request.TargetProviderTurnID {
		// The receipt is returned WITH the error on purpose: the provider answered, and
		// what it said is evidence the caller needs (which turn actually absorbed the
		// guidance). Dropping it would force a caller to re-observe the provider to learn
		// something AO already knows, and the error alone is not actionable.
		return ports.MessageReceipt{
				SessionID:       request.SessionID,
				ClientMessageID: request.ClientMessageID,
				// The turn that absorbed it is a real row, so it is resolved here too: a
				// caller investigating the mismatch needs it. Steered stays false below, so
				// this can never be read as a successful steer.
				TurnID:         g.durableTurnID(ctx, request.SessionID, result.ProviderTurnID),
				ProviderTurnID: result.ProviderTurnID,
				// Steered stays false: this receipt is evidence of what the provider did,
				// not a claim that the caller's request succeeded. Setting it true would
				// make MatchesSteer's contract depend on the caller ignoring the error.
				Steered:              false,
				TargetProviderTurnID: result.ProviderTurnID,
				ControllerGeneration: request.ControllerGeneration,
			}, fmt.Errorf(
				"%w: the provider absorbed the guidance into %q, not %q",
				ports.ErrExecutionTargetMismatch, result.ProviderTurnID, request.TargetProviderTurnID)
	}
	return ports.MessageReceipt{
		SessionID:            request.SessionID,
		ClientMessageID:      request.ClientMessageID,
		TurnID:               g.durableTurnID(ctx, request.SessionID, result.ProviderTurnID),
		ProviderTurnID:       result.ProviderTurnID,
		Queued:               false,
		Duplicate:            false,
		Steered:              true,
		TargetProviderTurnID: result.ProviderTurnID,
		ControllerGeneration: request.ControllerGeneration,
	}, nil
}

// Interrupt implements ports.ExecutionGateway.
//
// A stop is reported as one of three proven facts and never as an error: confirmed,
// nothing-to-stop, or unknown. An uncertain stop must not be retried (it would
// interrupt a second turn), so the coordinator records stop_unknown from the receipt.
//
// Every receipt carries the generation it describes, and the fence comes first:
//
//  1. the request must name a generation at all (InterruptRequest.Validate);
//  2. the live controller generation — read from the controller itself, falling back
//     to the durable column — must equal it, so a stop aimed at a replaced controller
//     is refused before anything is interrupted;
//  3. the chat service repeats the comparison inside the controller through
//     InterruptPinned. That is authoritative: (2) is a cheap early refusal made with
//     evidence the gateway already had, but only the controller can promise that the
//     generation it stops is the one it was asked about.
//
// A refusal at (2) or (3) is ErrExecutionStaleGeneration and produces no receipt: an
// outcome receipt would claim a fact about a controller the caller did not name.
func (g *LocalGateway) Interrupt(
	ctx context.Context,
	request ports.InterruptRequest,
) (ports.InterruptReceipt, error) {
	if err := request.Validate(); err != nil {
		return ports.InterruptReceipt{}, err
	}
	rec, found, err := g.readSession(ctx, request.SessionID)
	if err != nil {
		return ports.InterruptReceipt{}, err
	}
	if !found {
		return ports.InterruptReceipt{}, fmt.Errorf(
			"%w: %s", ports.ErrExecutionSessionNotFound, request.SessionID)
	}
	if rec.IsTerminated {
		// A terminated session has nothing in flight, and calling the chat layer to
		// learn that would only observe a controller that no longer exists.
		return g.interruptNothingToStop(rec, request)
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return g.interruptNothingToStop(rec, request)
	}
	if g.chats == nil {
		return ports.InterruptReceipt{}, fmt.Errorf(
			"%w: the chat service is not wired", ports.ErrExecutionUnsupported)
	}
	if !g.hasLiveController(request.SessionID) {
		return g.interruptNothingToStop(rec, request)
	}

	// The early fence. The live value is what the caller must have observed, so a
	// session whose controller rotated refuses here without any provider call.
	live := g.controllerGeneration(rec, true)
	if live == "" {
		return ports.InterruptReceipt{}, fmt.Errorf(
			"%w: no live controller generation is known for %s",
			ports.ErrExecutionStaleGeneration, request.SessionID)
	}
	if live != request.ControllerGeneration {
		return ports.InterruptReceipt{}, fmt.Errorf(
			"%w: the live controller is %q, not %q",
			ports.ErrExecutionStaleGeneration, live, request.ControllerGeneration)
	}
	// Stop 仍以当前 generation 为栅栏，但失去工作区证据时不得操作该 Controller。
	if _, err := g.observeWorktrees(ctx, rec); err != nil {
		return ports.InterruptReceipt{}, fmt.Errorf("%w: workspace verification: %v", ports.ErrExecutionRefused, err)
	}

	generation := request.ControllerGeneration
	switch err := g.chats.InterruptPinned(ctx, request.SessionID, generation); {
	case err == nil:
		return ports.InterruptReceipt{
			SessionID: request.SessionID, ControllerGeneration: generation, Confirmed: true,
		}, nil
	case errors.Is(err, chatsvc.ErrNoActiveTurn), errors.Is(err, chatsvc.ErrNoController):
		return ports.InterruptReceipt{
			SessionID: request.SessionID, ControllerGeneration: generation, NothingToStop: true,
		}, nil
	case errors.Is(err, chatsvc.ErrStaleControllerGeneration):
		// The authoritative fence refused: the controller changed since the read above.
		return ports.InterruptReceipt{}, mapChatError(err)
	case errors.Is(err, ports.ErrChatUnsupported), errors.Is(err, chatsvc.ErrNotChatMode):
		// The session exists but this build cannot stop it through Chat. That is a
		// permanent condition for the caller, so it is an error rather than a receipt.
		return ports.InterruptReceipt{}, mapChatError(err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, chatsvc.ErrControllerHandoff), errors.Is(err, chatsvc.ErrSteerDeliveryUncertain):
		return ports.InterruptReceipt{
			SessionID: request.SessionID, ControllerGeneration: generation, Unknown: true,
		}, nil
	default:
		// An unclassified failure may still have reached the provider. Reporting
		// "stopped" would be a fabricated receipt and reporting an error would invite
		// a retry, so the honest answer is unknown.
		return ports.InterruptReceipt{
			SessionID: request.SessionID, ControllerGeneration: generation, Unknown: true,
		}, nil
	}
}

// interruptNothingToStop builds the "no turn in flight" receipt.
//
// It still names a generation, because "nothing to stop" is a claim about one
// controller: the caller must be able to tell whether the nothing-to-stop answer
// applied to the generation it was worried about. The requested generation is used
// when no live controller can be read, which is the terminated/TUI case; a live
// session reports the controller's own value so a rotated controller is visible.
func (g *LocalGateway) interruptNothingToStop(
	rec domain.SessionRecord,
	request ports.InterruptRequest,
) (ports.InterruptReceipt, error) {
	generation := request.ControllerGeneration
	if live := g.controllerGeneration(rec, g.hasLiveController(rec.ID)); live != "" {
		generation = live
	}
	return ports.InterruptReceipt{
		SessionID: request.SessionID, ControllerGeneration: generation, NothingToStop: true,
	}, nil
}

// Resume implements ports.ExecutionGateway.
func (g *LocalGateway) Resume(
	ctx context.Context,
	request ports.ResumeRequest,
) (ports.SessionObservation, error) {
	if strings.TrimSpace(string(request.SessionID)) == "" {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: a resume must name a session", ports.ErrExecutionRefused)
	}
	if strings.TrimSpace(request.SpawnKey) == "" {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: a resume must carry the spawn key that created the session",
			ports.ErrExecutionRefused)
	}
	if g.resumer == nil {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: the session manager is not wired", ports.ErrExecutionUnsupported)
	}

	rec, found, err := g.readSession(ctx, request.SessionID)
	if err != nil {
		return ports.SessionObservation{}, err
	}
	if !found {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: %s", ports.ErrExecutionSessionNotFound, request.SessionID)
	}
	// A resume may not act on a session another request created: the spawn key is the
	// only thing that ties this call to the work it believes it owns.
	//
	// The match is exact, including the empty case. An earlier form accepted a session
	// whose stored ClientRequestID was empty, which meant "no sponsor recorded" was
	// read as "sponsor matches": any caller holding a session id could resume a row
	// nobody could attribute, and a resume starts (or restarts) provider work. A row
	// with no sponsor therefore cannot be resumed through this gateway at all, and the
	// refusal says so.
	if rec.ClientRequestID != request.SpawnKey {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: session belongs to a different spawn request", ports.ErrExecutionRefused)
	}
	// 恢复会启动控制器，必须在副作用前核验工作区；不能先启动再报告 unknown。
	if _, err := g.observeWorktrees(ctx, rec); err != nil {
		return ports.SessionObservation{}, fmt.Errorf("%w: workspace verification: %v", ports.ErrExecutionRefused, err)
	}

	readiness := ProjectReadiness(rec, g.hasLiveController(rec.ID))
	switch {
	case rec.IsTerminated:
		if _, err := g.resumer.RestoreWithMode(ctx, rec.ID); err != nil {
			return ports.SessionObservation{}, g.resumeFailure(err)
		}
	case readiness == ports.SessionReadinessExited || !g.hasLiveController(rec.ID):
		if _, err := g.resumer.ResumeAgentWithMode(ctx, rec.ID); err != nil {
			return ports.SessionObservation{}, g.resumeFailure(err)
		}
	default:
		// Already live. Resuming a working session would launch a second controller
		// over the same provider conversation, so a no-op is the correct answer.
		return g.InspectSession(ctx, rec.ID)
	}
	return g.InspectSession(ctx, rec.ID)
}

// resumeFailure classifies a restore/resume error: an error raised before any launch
// is a refusal, anything else may have started something and is unknown.
func (g *LocalGateway) resumeFailure(cause error) error {
	switch {
	case errors.Is(cause, sessionmanager.ErrTerminated),
		errors.Is(cause, sessionmanager.ErrNotFound),
		errors.Is(cause, sessionmanager.ErrNotRestorable),
		errors.Is(cause, sessionmanager.ErrNotResumable),
		errors.Is(cause, sessionmanager.ErrAgentNotExited),
		errors.Is(cause, sessionmanager.ErrIncompleteHandle),
		errors.Is(cause, sessionmanager.ErrHarnessInstallActive),
		errors.Is(cause, sessionmanager.ErrTargetAgentUnauthorized),
		errors.Is(cause, sessionmanager.ErrInterfaceHandoffUnsupported),
		errors.Is(cause, sessionmanager.ErrNativeConversationMissing),
		errors.Is(cause, sessionmanager.ErrNativeConversationUnverified),
		errors.Is(cause, sessionmanager.ErrInterfaceWorkspaceUnavailable),
		errors.Is(cause, sessionmanager.ErrInterfaceAlreadySelected),
		errors.Is(cause, sessionmanager.ErrInterfaceTransitionInProgress):
		return fmt.Errorf("%w: %v", ports.ErrExecutionRefused, cause)
	default:
		return fmt.Errorf("%w: %v", ports.ErrSpawnOutcomeUnknown, cause)
	}
}

// requireReady reads the session and refuses anything a chat-addressed request
// cannot act on.
func (g *LocalGateway) requireReady(
	ctx context.Context,
	sessionID domain.SessionID,
) (ports.SessionObservation, error) {
	observation, err := g.InspectSession(ctx, sessionID)
	if err != nil {
		return ports.SessionObservation{}, err
	}
	if observation.Terminated {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: %w: %s", ports.ErrExecutionRefused, ports.ErrExecutionTerminated, sessionID)
	}
	if observation.Readiness != ports.SessionReadinessReady {
		return ports.SessionObservation{}, fmt.Errorf(
			"%w: session %s is %s, not ready", ports.ErrExecutionRefused, sessionID,
			observation.Readiness)
	}
	return observation, nil
}

// readSession reads the durable row.
func (g *LocalGateway) readSession(
	ctx context.Context,
	sessionID domain.SessionID,
) (domain.SessionRecord, bool, error) {
	if g.records == nil {
		return domain.SessionRecord{}, false, fmt.Errorf(
			"%w: session records are not wired", ports.ErrExecutionUnsupported)
	}
	return g.records.GetSession(ctx, sessionID)
}

// observe projects one durable record into an observation.
func (g *LocalGateway) observe(
	ctx context.Context,
	rec domain.SessionRecord,
) (ports.SessionObservation, error) {
	live := g.hasLiveController(rec.ID)
	observation := ports.SessionObservation{
		SessionID:              rec.ID,
		ProjectID:              rec.ProjectID,
		Kind:                   rec.Kind,
		Harness:                rec.Harness,
		Mode:                   rec.Mode,
		Readiness:              ProjectReadiness(rec, live),
		ControllerGeneration:   g.controllerGeneration(rec, live),
		ProviderConversationID: rec.Metadata.ProviderConversationID,
		ClientRequestID:        rec.ClientRequestID,
		ClientRequestCommitted: rec.ClientRequestCommitted,
		Terminated:             rec.IsTerminated,
		ObservedAt:             g.now().UTC(),
		SessionUpdatedAt:       rec.UpdatedAt,
	}
	worktrees, err := g.observeWorktrees(ctx, rec)
	observation.Worktrees = worktrees
	if err != nil {
		observation.Readiness = ports.SessionReadinessUnknown
		// 验证器返回部分结果也不能作为证据，禁止保留任何未经整体核验的 HEAD。
		for i := range observation.Worktrees {
			observation.Worktrees[i].HeadSHA = ""
		}
	}
	return observation, nil
}

// projectWorktrees 只投影历史引用；CreationSHA 不能冒充实时 HEAD。
func projectWorktrees(records []domain.SessionWorktreeRecord) []ports.RepoWorktreeObservation {
	if len(records) == 0 {
		return nil
	}
	out := make([]ports.RepoWorktreeObservation, 0, len(records))
	for _, record := range records {
		out = append(out, ports.RepoWorktreeObservation{
			RepoKey:     domain.WorkflowRepoKey(record.RepoName),
			ProjectID:   record.ProjectID,
			WorktreeRef: record.WorktreePath,
			Branch:      record.Branch,
			BaseSHA:     record.BaseSHA,
		})
	}
	return out
}

// hasLiveController reports whether a live chat controller owns the session.
func (g *LocalGateway) hasLiveController(sessionID domain.SessionID) bool {
	if g.controllers == nil {
		return false
	}
	return g.controllers.HasLiveChatController(sessionID)
}

// controllerGeneration prefers the live controller's generation and falls back to
// the durable one. The live value is what a steer is fenced against, so a stale
// durable column must not be the answer while a newer controller is running.
func (g *LocalGateway) controllerGeneration(rec domain.SessionRecord, live bool) string {
	if live && g.controllers != nil {
		if controller, err := g.controllers.Controller(rec.ID); err == nil && controller != nil {
			if generation := controller.Generation(); generation != "" {
				return generation
			}
		}
	}
	return domain.SessionExecutionGeneration(rec)
}

// originOrDefault attributes a workflow message to automation. A workflow message is
// never human-authored, and the Chat service treats an unknown origin as human.
func originOrDefault(origin domain.MessageOrigin) domain.MessageOrigin {
	switch origin {
	case domain.MessageOriginHuman, domain.MessageOriginAutomation,
		domain.MessageOriginDaemon, domain.MessageOriginProvider:
		return origin
	default:
		return domain.MessageOriginAutomation
	}
}

// durableTurnID resolves AO's own turn row behind a provider turn.
//
// It exists because an "applied" intervention receipt requires both ids. When the lookup
// cannot produce one, this returns the empty string rather than inventing an id: the caller
// (the intervention layer) then records the weaker but honest outcome. A fabricated id
// would let the store's evidence check pass on a turn that does not exist.
//
// A miss is not an error for the steer itself — the guidance genuinely landed — so it is
// logged at debug with no ids at all: a turn id and a session id are both durable identity.
func (g *LocalGateway) durableTurnID(
	ctx context.Context,
	sessionID domain.SessionID,
	providerTurnID string,
) string {
	if g.turns == nil || strings.TrimSpace(providerTurnID) == "" {
		return ""
	}
	conversation, err := g.turns.ConversationForSession(ctx, sessionID)
	if err != nil {
		g.log.Debug("steer receipt has no durable turn: conversation lookup failed",
			"reason", "conversation_lookup_failed")
		return ""
	}
	turn, err := g.turns.TurnByProviderID(ctx, conversation.ID, providerTurnID)
	if err != nil || strings.TrimSpace(turn.ID) == "" {
		g.log.Debug("steer receipt has no durable turn: turn lookup found nothing",
			"reason", "turn_lookup_missed")
		return ""
	}
	return turn.ID
}

// mapChatError translates the Chat service's outcomes into execution outcomes.
//
// Every branch is a decision about what the caller may safely do next:
//
//   - ErrChatUnsupported / ErrNotChatMode → ErrExecutionUnsupported: this session
//     cannot take chat-addressed work at all, permanently for this implementation.
//   - ErrNoController / ErrClientMessageConflict → ErrExecutionRefused: nothing was
//     dispatched. A conflict means the same client message id carries different
//     content, which is a caller bug rather than a transient failure.
//   - ErrStaleControllerGeneration → ErrExecutionStaleGeneration: the controller was
//     replaced between the gateway's observation and the dispatch. Nothing reached the
//     provider, and the caller's evidence is simply out of date.
//   - cancellation, handoff, uncertain steer → ErrExecutionOutcomeUnknown: AO cannot
//     prove whether the provider received the message.
//   - a missing session → ErrExecutionSessionNotFound.
//   - anything else → ErrExecutionOutcomeUnknown, because a send that failed for an
//     unclassified reason may still have dispatched.
func mapChatError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrChatUnsupported):
		return fmt.Errorf("%w: %v", ports.ErrExecutionUnsupported, err)
	case errors.Is(err, chatsvc.ErrNotChatMode):
		return fmt.Errorf("%w: %v", ports.ErrExecutionUnsupported, err)
	case errors.Is(err, chatsvc.ErrNoController):
		return fmt.Errorf("%w: %v", ports.ErrExecutionRefused, err)
	case errors.Is(err, chatsvc.ErrWorkspaceInvalid):
		// 已在 Provider 调用前拒绝，不能把明确未发送报告为不确定副作用。
		return fmt.Errorf("%w: %v", ports.ErrExecutionRefused, err)
	case errors.Is(err, chatsvc.ErrStaleControllerGeneration):
		// A pinned send or stop refused because the controller is not the one the
		// caller observed. Nothing was dispatched, so this is a fence, not an
		// uncertain outcome the caller should freeze on.
		return fmt.Errorf("%w: %v", ports.ErrExecutionStaleGeneration, err)
	case errors.Is(err, domain.ErrClientMessageConflict):
		return fmt.Errorf("%w: %v", ports.ErrExecutionRefused, err)
	case errors.Is(err, ports.ErrSessionNotFound):
		return fmt.Errorf("%w: %v", ports.ErrExecutionSessionNotFound, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, chatsvc.ErrSteerDeliveryUncertain),
		errors.Is(err, chatsvc.ErrControllerHandoff):
		return fmt.Errorf("%w: %v", ports.ErrExecutionOutcomeUnknown, err)
	default:
		return fmt.Errorf("%w: %v", ports.ErrExecutionOutcomeUnknown, err)
	}
}

// mapSteerError translates the Chat service's steer outcomes.
//
//   - ErrSteerUnsupported → ErrExecutionUnsupported: the provider cannot steer, so a
//     client should offer the queue path instead.
//   - ErrNoActiveTurn / ErrTurnNotSteerable / ErrSteerTargetRequired /
//     ErrStaleControllerGeneration → ErrExecutionTargetMismatch: the turn the caller
//     named is not the one that can absorb guidance.
//   - ErrNoController / ErrClientMessageConflict → ErrExecutionRefused.
//   - ErrSteerDeliveryUncertain, cancellation, handoff → ErrExecutionOutcomeUnknown.
//   - anything else → ErrExecutionOutcomeUnknown.
func mapSteerError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, chatsvc.ErrWorkspaceInvalid):
		return fmt.Errorf("%w: %v", ports.ErrExecutionRefused, err)
	case errors.Is(err, chatsvc.ErrSteerUnsupported):
		return fmt.Errorf("%w: %v", ports.ErrExecutionUnsupported, err)
	case errors.Is(err, chatsvc.ErrNoActiveTurn),
		errors.Is(err, chatsvc.ErrTurnNotSteerable),
		errors.Is(err, chatsvc.ErrSteerTargetRequired),
		errors.Is(err, chatsvc.ErrStaleControllerGeneration):
		return fmt.Errorf("%w: %v", ports.ErrExecutionTargetMismatch, err)
	case errors.Is(err, chatsvc.ErrNoController),
		errors.Is(err, domain.ErrClientMessageConflict):
		return fmt.Errorf("%w: %v", ports.ErrExecutionRefused, err)
	case errors.Is(err, chatsvc.ErrSteerDeliveryUncertain),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, chatsvc.ErrControllerHandoff):
		return fmt.Errorf("%w: %v", ports.ErrExecutionOutcomeUnknown, err)
	default:
		return fmt.Errorf("%w: %v", ports.ErrExecutionOutcomeUnknown, err)
	}
}
