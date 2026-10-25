package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Steering: guidance delivered into a turn that is already running.
//
// Why this is not "interrupt, then send again". An interrupt destroys the turn —
// its reasoning, its half-applied edits, the command it has running — and the
// resend starts from nothing. A steer adds the correction to the SAME turn and
// leaves the work in place; the agent decides what to abandon. Measured against
// codex-cli 0.146.0, the steered turn kept its id and settled `completed`, having
// abandoned the command it was running on its own. So the user's guidance costs
// them nothing they had already paid for, which is the entire reason to prefer it.
//
// Optional and feature-detected, following the Models precedent: a provider that
// cannot steer gets a typed refusal a client renders as an absent control rather
// than as a failed request.

// Typed outcomes for steering. Each distinguishes advice a client can act on:
// "this agent cannot do it", "send it as a new message instead", "wait, then try
// again".
var (
	// ErrSteerUnsupported reports a driver whose provider cannot take guidance into
	// a running turn. Permanent for that harness — a client should stop offering the
	// control rather than retry.
	ErrSteerUnsupported = errors.New("chat driver cannot steer a running turn")
	// ErrTurnNotSteerable reports a turn that is running but cannot absorb guidance.
	// Codex refuses a compaction or review turn this way. Retryable once that turn
	// finishes, which is what separates it from ErrSteerUnsupported.
	ErrTurnNotSteerable = errors.New("the running turn cannot take guidance")
	// ErrSteerTextRequired refuses an empty steer. There is no keystroke concept in
	// Chat mode: an empty body is a caller bug, not a way to nudge the agent.
	ErrSteerTextRequired = errors.New("steer text is required")
	// ErrTurnNotQueued reports a selected source that has already dispatched,
	// settled, or been claimed by another promotion.
	ErrTurnNotQueued = errors.New("turn is not queued")
	// ErrSteerDeliveryUncertain means AO reserved an idempotency handle but cannot
	// prove whether the provider accepted it. Reusing the handle is safe and must
	// never contact the provider again; inventing a fresh handle is not.
	ErrSteerDeliveryUncertain = errors.New("steer delivery is uncertain")
	// ErrSteerIdempotencyConflict refuses reuse of one handle for different
	// guidance. Dispatching either request would make the handle lie about identity.
	ErrSteerIdempotencyConflict = errors.New("steer idempotency handle belongs to different guidance")
	// ErrSteerContentUnsupported means the provider cannot accept every structured
	// block. The whole queued message stays undelivered.
	ErrSteerContentUnsupported = errors.New("steer content is unsupported")
)

// SteerResult is what a steer did, for a client that has to attribute it.
type SteerResult struct {
	// ProviderTurnID is the turn that absorbed the guidance. Reported rather than
	// assumed to be the one asked for: the provider names it in its own response,
	// and a client matches it against the snapshot's turns.
	ProviderTurnID string
	// ActivityID is the timeline row recording the guidance, so a client can
	// reconcile an optimistic bubble with the durable one instead of showing both.
	ActivityID string
}

// SteerOrSendResult identifies whether one atomic request joined an active turn
// or opened a normal turn while the conversation was idle.
type SteerOrSendResult struct {
	Steered   bool
	Duplicate bool
	Steer     SteerResult
	Turn      domain.ConversationTurn
}

// resolveSteerSender enriches CLI-originated steering with the source session's
// current durable identity. A missing source is tolerated so older workers and
// cross-daemon callers keep the raw text prefix as their fallback.
func (s *Service) resolveSteerSender(ctx context.Context, msg ports.ChatUserMessage) ports.ChatUserMessage {
	if msg.SenderSessionID == "" || s.sessions == nil {
		return msg
	}
	record, found, err := s.sessions.GetSession(ctx, domain.SessionID(msg.SenderSessionID))
	if err != nil || !found {
		return msg
	}
	msg.SenderProjectID = string(record.ProjectID)
	msg.SenderDisplayName = strings.TrimSpace(record.DisplayName)
	return msg
}

// PromoteQueuedTurnResult reports what one queued-turn steer did: which turn it
// cut (when one was running) and the provider turn now running the promoted
// message.
type PromoteQueuedTurnResult struct {
	// SourceTurnID is the durable queued turn the caller addressed.
	SourceTurnID string
	// InterruptedTurnID is the provider turn the steer cancelled, or "" when
	// nothing was running and the request was a plain promotion.
	InterruptedTurnID string
	// ProviderTurnID is the provider turn dispatching the promoted message. Empty
	// only when a concurrent dispatch claimed the agent between the cut and the
	// promotion, leaving the source queued behind it.
	ProviderTurnID string
}

// maxSteerSummaryRunes bounds the one-line label a steer lands on the timeline
// with. The full text is always kept in the activity's detail payload; this is only
// what a collapsed row shows.
const maxSteerSummaryRunes = 120

// Steer sends guidance into this session's running turn.
//
// Dispatch is decided from the persisted session mode like every other Chat
// command, so a TUI session cannot reach this path by calling the URL directly.
func (s *Service) Steer(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
) (SteerResult, error) {
	if strings.TrimSpace(msg.Text) == "" {
		return SteerResult{}, ErrSteerTextRequired
	}
	msg = s.resolveSteerSender(ctx, msg)
	if msg.SenderSessionID != "" {
		msg.Origin = domain.MessageOriginAutomation
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return SteerResult{}, err
	}
	defer release()
	return controller.Steer(ctx, msg)
}

// RecoverSteer reads only the durable receipt. A missing controller or a changed
// interface cannot turn an earlier accepted/uncertain delivery into a rejection.
func (s *Service) RecoverSteer(ctx context.Context, id domain.SessionID, clientMessageID string) (SteerResult, error) {
	if clientMessageID == "" {
		return SteerResult{}, ErrSteerDeliveryUncertain
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: load conversation: %w", ErrSteerDeliveryUncertain, err)
	}
	delivery, found, err := s.store.SteerDelivery(ctx, conversation.ID, clientMessageID)
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: load receipt: %w", ErrSteerDeliveryUncertain, err)
	}
	if !found {
		return SteerResult{}, ErrSteerDeliveryUncertain
	}
	return replaySteerDelivery(delivery, delivery.RequestJSON)
}

// SteerOrSend routes one idempotent request through the session's live Chat
// controller without exposing a state-check race to the caller.
func (s *Service) SteerOrSend(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
	recoverOnly bool,
) (SteerOrSendResult, error) {
	if strings.TrimSpace(msg.Text) == "" && !recoverOnly {
		return SteerOrSendResult{}, ErrSteerTextRequired
	}
	if msg.ClientMessageID == "" {
		return SteerOrSendResult{}, ErrSteerDeliveryUncertain
	}
	msg = s.resolveSteerSender(ctx, msg)
	// A cross-session steer that finds no active turn falls through to the normal
	// send path. Keep that message attributed as automation so idle and busy
	// targets render the same way.
	if msg.SenderSessionID != "" {
		msg.Origin = domain.MessageOriginAutomation
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return SteerOrSendResult{}, err
	}
	defer release()
	return controller.SteerOrSend(ctx, msg, recoverOnly)
}

// PromoteQueuedTurn delivers one already queued turn now: the running turn is
// cancelled and marked interrupted, and the addressed message starts as the next
// turn. The daemon reads the queued content; callers identify it but cannot
// replace it.
//
// Every chat driver gets the same cancel-then-send semantics through the shared
// interrupt seam, so unlike mid-turn Steer this is not capability-gated. When
// nothing is running, the request degrades to a plain promotion.
func (s *Service) PromoteQueuedTurn(
	ctx context.Context,
	id domain.SessionID,
	turnID string,
) (PromoteQueuedTurnResult, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	return controller.PromoteQueuedTurn(ctx, turnID)
}

// steerer returns the provider's steering port only when the conversation
// negotiated it. The ACP driver implements Steer for every agent, so the type
// alone does not say the agent can take guidance mid-turn.
func (c *Controller) steerer() (ports.ChatSteerer, bool) {
	steerer, ok := c.conv.(ports.ChatSteerer)
	return steerer, ok && c.Capabilities().Has(ports.ChatCapabilitySteer)
}

// queuedTurnSettleWait bounds how long a queued-turn steer waits for the turn it
// cancelled to settle. A variable so tests can shorten it.
var queuedTurnSettleWait = 10 * time.Second

// PromoteQueuedTurn cuts the running turn and immediately dispatches the
// addressed queued turn as the next one.
//
// The sequence is cancel, settle, promote. Cutting first is what makes this
// different from mid-turn Steer: the interrupted turn keeps its partial output in
// the timeline and settles `interrupted`, and the queued message becomes its own
// real turn rather than guidance absorbed into work the user just abandoned.
//
// Like Interrupt, the provider boundary is crossed WITHOUT holding the dispatch
// lock: the projection goroutine takes the same lock to commit the interrupted
// completion, and waiting under the lock would deadlock that handoff. The queue
// is never cancelled — only the running turn is cut — so the remaining queued
// messages dispatch behind the promoted one as usual.
//
// When the running turn settles on its own before the cancel lands, the request
// degrades to a plain promotion: the interrupt refusal is the provider saying the
// work already ended, and the drain dispatches the addressed message.
func (c *Controller) PromoteQueuedTurn(
	ctx context.Context,
	turnID string,
) (PromoteQueuedTurnResult, error) {
	c.sendMu.Lock()
	if c.handoffActive() {
		c.sendMu.Unlock()
		return PromoteQueuedTurnResult{}, ErrControllerHandoff
	}
	source, err := c.store.TurnByID(ctx, turnID)
	if err != nil {
		c.sendMu.Unlock()
		return PromoteQueuedTurnResult{}, err
	}
	if source.ConversationID != c.conversation.ID {
		c.sendMu.Unlock()
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s", domain.ErrNoConversationTurn, turnID)
	}
	if source.State != domain.TurnStateQueued {
		c.sendMu.Unlock()
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s", ErrTurnNotQueued, turnID)
	}
	// Only the user's own queued messages are steerable. The queued-message read
	// enforces both queued state and human origin, so an automation item — which
	// follows the queue it was given — is refused here without touching the
	// provider or the queue order.
	if _, err := c.store.QueuedTurnMessage(ctx, c.conversation.ID, turnID); err != nil {
		c.sendMu.Unlock()
		if errors.Is(err, store.ErrQueuedTurnNotAvailable) {
			return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s", ErrTurnNotQueued, turnID)
		}
		return PromoteQueuedTurnResult{}, err
	}

	// Head the queue before releasing the dispatch lock. The settlement drain and
	// any racing natural completion must find this turn first, or a different
	// queued message would go out under this request's answer.
	if err := c.prioritizeQueuedTurn(ctx, turnID); err != nil {
		c.sendMu.Unlock()
		return PromoteQueuedTurnResult{}, err
	}

	interrupted, ok := c.awaitAcknowledgedTurn(ctx)
	cut := ""
	if ok {
		c.sendMu.Unlock()
		err := c.conv.Interrupt(ctx, interrupted)
		if err != nil && !errors.Is(err, ports.ErrChatNoActiveTurn) {
			return PromoteQueuedTurnResult{}, fmt.Errorf(
				"interrupt turn %s for queued steer: %w", interrupted, err)
		}
		if err == nil {
			// The cancel succeeded; only now may the response claim an interruption.
			cut = interrupted
		}
		// Whether the cancel was accepted or answered "nothing active" — a turn
		// that settled on its own in the race window — its completion may still be
		// in flight. Waiting for it is what keeps the promotion behind the settle
		// instead of racing it.
		if !c.awaitTurnSettled(ctx, interrupted) {
			// The provider never settled the turn. Durable state is what the
			// timeline renders, so reconcile the rows the same way Stop does and
			// let its drain dispatch the promoted turn. A zero cutoff keeps the
			// queue: only the cut turn is interrupted.
			c.sendMu.Lock()
			providerTurnIDs, listErr := c.store.ListVisibleRunningTurnProviderIDs(ctx, c.conversation.ID)
			if listErr != nil {
				c.sendMu.Unlock()
				return PromoteQueuedTurnResult{}, fmt.Errorf(
					"check running turns after steer cancel: %w", listErr)
			}
			if err := c.reconcileDurableTurnsLocked(ctx, interrupted, providerTurnIDs, time.Time{}); err != nil {
				return PromoteQueuedTurnResult{}, err
			}
			c.sendMu.Unlock()
		}
	} else {
		c.sendMu.Unlock()
	}
	return c.dispatchPromotedTurn(ctx, turnID, cut)
}

// prioritizeQueuedTurn moves turnID to the head of the durable queue, keeping the
// relative order of everything else. Callers hold sendMu, so the queue cannot
// change between the queued-state validation and the rewrite.
func (c *Controller) prioritizeQueuedTurn(ctx context.Context, turnID string) error {
	order, err := c.store.QueuedTurnOrder(ctx, c.conversation.ID)
	if err != nil {
		return fmt.Errorf("read queued turn order: %w", err)
	}
	if len(order) == 0 || order[0] == turnID {
		return nil
	}
	reordered := make([]string, 0, len(order))
	reordered = append(reordered, turnID)
	for _, id := range order {
		if id != turnID {
			reordered = append(reordered, id)
		}
	}
	if len(reordered) != len(order) {
		// The turn left the queue between validation and the reorder; the queue
		// moved underneath a request that no longer describes it.
		return fmt.Errorf("%w: %s", ErrTurnNotQueued, turnID)
	}
	if err := c.store.ReorderQueuedTurns(ctx, c.conversation.ID, reordered); err != nil {
		return fmt.Errorf("prioritize queued turn %s: %w", turnID, err)
	}
	return nil
}

// awaitTurnSettled waits for the named turn to leave the controller's ownership,
// which the interrupted completion commits. It gives up on the deadline, the
// request context, or the controller stopping — whichever comes first — so a
// provider that accepted the cancel but never settled cannot hang the request.
func (c *Controller) awaitTurnSettled(ctx context.Context, turn string) bool {
	deadline := time.Now().Add(queuedTurnSettleWait)
	for {
		c.mu.Lock()
		pending := c.pendingTurnID
		c.mu.Unlock()
		if pending != turn {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-c.stopped:
			return false
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// dispatchPromotedTurn drains the queue — which now heads with the addressed
// turn — and reports where the promotion landed. The drain is a no-op when a
// racing dispatch already claimed the agent, in which case the source turn is
// still queued behind whatever went out first. `cut` is the provider turn the
// cancel stopped, or "" when nothing was interrupted.
func (c *Controller) dispatchPromotedTurn(
	ctx context.Context,
	turnID, cut string,
) (PromoteQueuedTurnResult, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.drainLocked(ctx, true); err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	promoted, err := c.store.TurnByID(ctx, turnID)
	if err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	return PromoteQueuedTurnResult{
		SourceTurnID: turnID, InterruptedTurnID: cut, ProviderTurnID: promoted.ProviderTurnID,
	}, nil
}

type durableSteerRefusal struct {
	kind           domain.ConversationSteerRejectionKind
	cause          error
	reserveContext string
	persistContext string
}

func (c *Controller) rejectSteerBeforeDispatch(
	ctx context.Context,
	clientMessageID, requestJSON string,
	refusal durableSteerRefusal,
) (SteerResult, error) {
	if clientMessageID == "" {
		return SteerResult{}, refusal.cause
	}
	delivery, created, err := c.store.ReserveSteerDelivery(
		ctx, c.conversation.ID, clientMessageID, requestJSON, c.now())
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: %s: %w",
			ErrSteerDeliveryUncertain, refusal.reserveContext, err)
	}
	if !created {
		return replaySteerDelivery(delivery, requestJSON)
	}
	if err := c.store.RejectSteerDelivery(
		context.WithoutCancel(ctx), c.conversation.ID, clientMessageID,
		refusal.kind, refusal.cause.Error(), c.now()); err != nil {
		return SteerResult{}, fmt.Errorf("%w: %s: %w",
			ErrSteerDeliveryUncertain, refusal.persistContext, err)
	}
	return SteerResult{}, refusal.cause
}

// Steer hands guidance to the provider for the turn currently in flight, then
// records it on that turn.
//
// The wait for acknowledgement is load-bearing, not defensive. Codex refuses a
// steer for a turn it has accepted but not yet announced — probed directly: a steer
// sent immediately after turn/start returned, carrying the id turn/start itself had
// just handed back, came back "no active turn to steer". Steering is most useful in
// exactly that window, the second after someone realizes they sent the wrong thing,
// so the same helper Interrupt uses to ride out that gap is used here.
//
// AO durably reserves a caller-supplied client handle before provider I/O. A
// definitive refusal settles that handle as rejected, success records the visible
// activity and accepted result atomically, and any gap between those facts remains
// uncertain forever. That fail-closed state is what makes a retry unable to deliver
// the same guidance twice even when the provider ignores the client handle.
func (c *Controller) Steer(ctx context.Context, msg ports.ChatUserMessage) (SteerResult, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.steerLocked(ctx, msg)
}

func (c *Controller) steerLocked(ctx context.Context, msg ports.ChatUserMessage) (SteerResult, error) {

	requestJSON, err := encodeSteerDeliveryRequest(msg)
	if err != nil {
		return SteerResult{}, err
	}
	if msg.ClientMessageID != "" {
		delivery, found, loadErr := c.store.SteerDelivery(
			ctx, c.conversation.ID, msg.ClientMessageID)
		if loadErr != nil {
			return SteerResult{}, fmt.Errorf("%w: load prior result: %w",
				ErrSteerDeliveryUncertain, loadErr)
		}
		if found {
			return replaySteerDelivery(delivery, requestJSON)
		}
	}
	if c.handoffActive() {
		return c.rejectSteerBeforeDispatch(ctx, msg.ClientMessageID, requestJSON,
			durableSteerRefusal{
				kind:           domain.ConversationSteerRejectedInterfaceTransition,
				cause:          ErrControllerHandoff,
				reserveContext: "reserve interface-transition refusal",
				persistContext: "persist interface-transition refusal",
			})
	}
	steerer, ok := c.steerer()
	if !ok {
		return c.rejectSteerBeforeDispatch(ctx, msg.ClientMessageID, requestJSON,
			durableSteerRefusal{
				kind:           domain.ConversationSteerRejectedUnsupported,
				cause:          ErrSteerUnsupported,
				reserveContext: "reserve unsupported result",
				persistContext: "persist unsupported result",
			})
	}

	turn, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		// Nothing is in flight. Reusing the interrupt sentinel keeps one code for
		// "there is no turn" across every command that needs one; the endpoint says
		// what to do about it.
		return c.rejectSteerBeforeDispatch(ctx, msg.ClientMessageID, requestJSON,
			durableSteerRefusal{
				kind:           domain.ConversationSteerRejectedNoActiveTurn,
				cause:          ErrNoActiveTurn,
				reserveContext: "reserve refusal",
				persistContext: "persist no-active-turn refusal",
			})
	}
	if msg.ClientMessageID != "" {
		delivery, created, reserveErr := c.store.ReserveSteerDelivery(
			ctx, c.conversation.ID, msg.ClientMessageID, requestJSON, c.now())
		if reserveErr != nil {
			return SteerResult{}, fmt.Errorf("%w: reserve delivery: %w",
				ErrSteerDeliveryUncertain, reserveErr)
		}
		if !created {
			return replaySteerDelivery(delivery, requestJSON)
		}
	}

	ref, err := steerer.Steer(ctx, turn, msg)
	if err != nil {
		kind, definitive, refused := classifySteerRejection(err)
		if definitive {
			if msg.ClientMessageID != "" {
				if rejectErr := c.store.RejectSteerDelivery(
					context.WithoutCancel(ctx), c.conversation.ID, msg.ClientMessageID,
					kind, refused.Error(), c.now()); rejectErr != nil {
					return SteerResult{}, fmt.Errorf("%w: persist provider refusal: %w",
						ErrSteerDeliveryUncertain, rejectErr)
				}
			}
			return SteerResult{}, refused
		}
		wrapped := classify(fmt.Errorf("steer turn %s: %w", turn, err))
		if msg.ClientMessageID != "" {
			return SteerResult{}, fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, wrapped)
		}
		return SteerResult{}, wrapped
	}

	landed := ref.ProviderTurnID
	if landed == "" {
		landed = turn
	}
	if msg.ClientMessageID == "" {
		activityID, recordErr := c.recordSteer(ctx, landed, msg)
		if recordErr != nil {
			// The guidance IS with the agent; only AO's record of it failed. Reporting the
			// error rather than swallowing it, because a steer the timeline never mentions
			// is a conversation whose next answer has no visible cause.
			return SteerResult{ProviderTurnID: landed}, recordErr
		}
		return SteerResult{ProviderTurnID: landed, ActivityID: activityID}, nil
	}
	activityID := c.newID()
	activity, err := makeSteerActivity(activityID, msg)
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, err)
	}
	if err := c.store.CompleteSteerDelivery(
		context.WithoutCancel(ctx), c.conversation.ID, msg.ClientMessageID,
		landed, activity, c.now()); err != nil {
		return SteerResult{}, fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, err)
	}
	return SteerResult{ProviderTurnID: landed, ActivityID: activityID}, nil
}

// SteerOrSend selects and persists one delivery outcome while holding the same
// lock used by ordinary sends and steering.
func (c *Controller) SteerOrSend(
	ctx context.Context,
	msg ports.ChatUserMessage,
	recoverOnly bool,
) (SteerOrSendResult, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if existing, found, err := c.store.ConversationMessageByClientID(
		ctx, c.conversation.ID, msg.ClientMessageID,
	); err != nil {
		return SteerOrSendResult{}, fmt.Errorf("recover sent message: %w", err)
	} else if found {
		if !recoverOnly {
			hash, err := clientPayloadHash(msg)
			if err != nil {
				return SteerOrSendResult{}, err
			}
			if existing.ClientPayloadHash != "" {
				if existing.ClientPayloadHash != hash {
					return SteerOrSendResult{}, domain.ErrClientMessageConflict
				}
			} else if !legacyMessageMatches(existing, msg) {
				return SteerOrSendResult{}, domain.ErrClientMessageConflict
			}
		}
		turn, err := c.store.TurnByID(ctx, existing.TurnID)
		if err != nil {
			return SteerOrSendResult{}, fmt.Errorf("recover sent turn: %w", err)
		}
		return SteerOrSendResult{Duplicate: true, Turn: turn}, nil
	}

	requestJSON, err := encodeSteerDeliveryRequest(msg)
	if err != nil {
		return SteerOrSendResult{}, err
	}
	if delivery, found, err := c.store.SteerDelivery(
		ctx, c.conversation.ID, msg.ClientMessageID,
	); err != nil {
		return SteerOrSendResult{}, fmt.Errorf("%w: load prior result: %w", ErrSteerDeliveryUncertain, err)
	} else if found {
		replayRequest := requestJSON
		if recoverOnly {
			replayRequest = delivery.RequestJSON
		}
		steered, replayErr := replaySteerDelivery(delivery, replayRequest)
		if replayErr == nil {
			return SteerOrSendResult{Steered: true, Duplicate: true, Steer: steered}, nil
		}
		if !errors.Is(replayErr, ErrNoActiveTurn) || recoverOnly {
			return SteerOrSendResult{}, replayErr
		}
	}
	if recoverOnly {
		return SteerOrSendResult{}, ErrSteerDeliveryUncertain
	}

	if _, active := c.awaitAcknowledgedTurn(ctx); active {
		steered, err := c.steerLocked(ctx, msg)
		if err == nil {
			return SteerOrSendResult{Steered: true, Steer: steered}, nil
		}
		if !errors.Is(err, ErrNoActiveTurn) {
			return SteerOrSendResult{}, err
		}
	}

	turn, err := c.sendLocked(ctx, msg, false)
	if err != nil {
		return SteerOrSendResult{}, err
	}
	return SteerOrSendResult{Turn: turn}, nil
}

type steerDeliveryRequest struct {
	Text            string                  `json:"text"`
	Content         []ports.ChatContent     `json:"content,omitempty"`
	Origin          domain.MessageOrigin    `json:"origin"`
	SenderSessionID string                  `json:"senderSessionId,omitempty"`
	Settings        deliveryRequestSettings `json:"settings"`
}

func encodeSteerDeliveryRequest(msg ports.ChatUserMessage) (string, error) {
	encoded, err := json.Marshal(steerDeliveryRequest{
		Text: msg.Text, Content: msg.Content, Origin: normalizeOrigin(msg.Origin),
		SenderSessionID: msg.SenderSessionID,
		Settings: deliveryRequestSettings{
			Model: msg.Settings.Model, Effort: msg.Settings.Effort, Approval: msg.Settings.Approval,
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode steer delivery request: %w", err)
	}
	return string(encoded), nil
}

func replaySteerDelivery(
	delivery domain.ConversationSteerDelivery,
	requestJSON string,
) (SteerResult, error) {
	if delivery.RequestJSON != requestJSON {
		return SteerResult{}, ErrSteerIdempotencyConflict
	}
	switch delivery.State {
	case domain.ConversationSteerAccepted:
		return SteerResult{
			ProviderTurnID: delivery.ProviderTurnID,
			ActivityID:     delivery.ActivityID,
		}, nil
	case domain.ConversationSteerRejected:
		return SteerResult{}, replaySteerRejection(delivery)
	case domain.ConversationSteerReserved:
		return SteerResult{}, ErrSteerDeliveryUncertain
	default:
		return SteerResult{}, fmt.Errorf("%w: invalid durable state %q",
			ErrSteerDeliveryUncertain, delivery.State)
	}
}

type storedSteerRejection struct {
	message string
	cause   error
}

func (e storedSteerRejection) Error() string { return e.message }
func (e storedSteerRejection) Unwrap() error { return e.cause }

func replaySteerRejection(delivery domain.ConversationSteerDelivery) error {
	var cause error
	switch delivery.RejectionKind {
	case domain.ConversationSteerRejectedNoActiveTurn:
		cause = ErrNoActiveTurn
	case domain.ConversationSteerRejectedUnsupported:
		cause = ErrSteerUnsupported
	case domain.ConversationSteerRejectedTurnNotSteerable:
		cause = ErrTurnNotSteerable
	case domain.ConversationSteerRejectedContentUnsupported:
		cause = ErrSteerContentUnsupported
	case domain.ConversationSteerRejectedByProvider:
		cause = ErrProviderRefused
	case domain.ConversationSteerRejectedInterfaceTransition:
		cause = ErrControllerHandoff
	default:
		return fmt.Errorf("%w: invalid durable rejection %q",
			ErrSteerDeliveryUncertain, delivery.RejectionKind)
	}
	return storedSteerRejection{message: delivery.RejectionMessage, cause: cause}
}

func classifySteerRejection(
	err error,
) (domain.ConversationSteerRejectionKind, bool, error) {
	switch {
	case errors.Is(err, ports.ErrChatNoSteerableTurn):
		// The turn ended, or was replaced, between AO's check and the provider's.
		// The provider is the authority on that, and losing the race is ordinary.
		return domain.ConversationSteerRejectedNoActiveTurn, true, ErrNoActiveTurn
	case errors.Is(err, ports.ErrChatTurnNotSteerable):
		return domain.ConversationSteerRejectedTurnNotSteerable, true,
			fmt.Errorf("%w: %w", ErrTurnNotSteerable, err)
	case errors.Is(err, ports.ErrChatSteerContentUnsupported):
		return domain.ConversationSteerRejectedContentUnsupported, true,
			fmt.Errorf("%w: %w", ErrSteerContentUnsupported, err)
	}
	classified := classify(err)
	if errors.Is(classified, ErrProviderRefused) {
		return domain.ConversationSteerRejectedByProvider, true, classified
	}
	return "", false, classified
}

// recordSteer writes the guidance onto the turn that took it.
//
// It lands as an activity, not as a message, and the reason is structural rather
// than semantic: a steer joins a turn that is already running, and AO's only durable
// write that can attach to a turn in flight is the activity row. AppendUserMessage
// opens a NEW turn — using it here would mint a second turn row that the drain loop
// would later dispatch as its own turn, sending the user's correction twice. The row
// is tagged `event: "steer"` in its detail so a client can render it as the user's
// own words rather than as a system notice. Cross-session CLI steers additionally
// carry sender metadata so the client can attribute them as automation without
// changing the activity storage model.
//
// The provider's own echo is not the record. It replays the guidance as a
// `userMessage` item on the turn, but the driver drops those: AO records what the
// user said when it accepts the request, and re-emitting the echo would show it
// twice.
func (c *Controller) recordSteer(
	ctx context.Context,
	providerTurnID string,
	msg ports.ChatUserMessage,
) (string, error) {
	id := c.newID()
	activity, err := makeSteerActivity(id, msg)
	if err != nil {
		return "", err
	}
	if err := c.store.UpsertActivity(ctx, c.conversation.ID, providerTurnID,
		activity, c.now()); err != nil {
		return "", fmt.Errorf("record steer on turn %s: %w", providerTurnID, err)
	}
	return id, nil
}

func makeSteerActivity(
	id string,
	msg ports.ChatUserMessage,
) (domain.ConversationActivity, error) {
	detail := map[string]any{
		"event":  "steer",
		"text":   msg.Text,
		"origin": string(normalizeOrigin(msg.Origin)),
	}
	if msg.SenderSessionID != "" {
		detail["senderSessionId"] = msg.SenderSessionID
		if msg.SenderProjectID != "" {
			detail["senderProjectId"] = msg.SenderProjectID
		}
		if msg.SenderDisplayName != "" {
			detail["senderDisplayName"] = msg.SenderDisplayName
		}
	}
	if msg.ClientMessageID != "" {
		detail["clientMessageId"] = msg.ClientMessageID
	}
	if len(msg.Content) > 0 {
		detail["content"] = msg.Content
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return domain.ConversationActivity{}, fmt.Errorf("encode steer detail: %w", err)
	}
	return domain.ConversationActivity{
		ID:     id,
		Kind:   domain.ActivityKindSystem,
		Status: domain.ActivityStatusCompleted,
		// Delivered, not pending: the provider accepted it before this ran.
		Summary: steerSummary(msg.Text),
		Detail:  encoded,
		// A synthetic item key, so a client retrying with the same idempotency
		// handle updates this row instead of adding a second one. Prefixed because
		// this id is AO's, not the provider's, and the two share a column.
		ProviderItemID: steerItemID(msg.ClientMessageID),
	}, nil
}

// steerItemID is the dedupe key for a recorded steer, or empty when the caller
// supplied no idempotency handle and every request is a distinct piece of guidance.
func steerItemID(clientMessageID string) string {
	if clientMessageID == "" {
		return ""
	}
	return "steer:" + clientMessageID
}

// steerSummary reduces guidance to a one-line label. The full text stays in the
// detail payload, so this only decides what a collapsed row reads like.
func steerSummary(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(collapsed) <= maxSteerSummaryRunes {
		return collapsed
	}
	runes := []rune(collapsed)
	return strings.TrimRight(string(runes[:maxSteerSummaryRunes]), " ") + "…"
}
