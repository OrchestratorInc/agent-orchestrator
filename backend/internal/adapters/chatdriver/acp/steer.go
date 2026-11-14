package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const steeringMethod = "_session/steering"

// defaultSteerDrainWait bounds how long a drain-mode steer holds the caller
// while the agent works toward its next step boundary. The Chat controller
// holds its dispatch lock for the whole call, so new sends wait too; Stop
// withdraws the guidance first (WithdrawPendingSteers). Past this, the
// guidance is withdrawn and the caller is told to try again.
const defaultSteerDrainWait = 5 * time.Second

// steerNudgeTimeout bounds the wait for an answer to the best-effort request
// asking a drain agent to collect guidance now. The agent answers it at once; a
// slow answer only means the guidance waits for the next ordinary boundary.
const steerNudgeTimeout = time.Second

type steeringResponse struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// SteerOutcome is a provider's answer to one steering request.
type SteerOutcome int

const (
	// SteerInjected means the running turn took the guidance.
	SteerInjected SteerOutcome = iota + 1
	// SteerNoRunningTurn means nothing was running to take it. The agent did not
	// consume the guidance and started nothing, so AO may send it as a new turn.
	SteerNoRunningTurn
)

// SteerRequest delivers mid-turn guidance with one client-to-agent request.
// The default is ACP's pre-standard _session/steering extension. A binding
// whose agent ships its own vendor method supplies one instead.
type SteerRequest struct {
	// Supported reports from the handshake whether the agent takes the request.
	Supported func(acpsdk.InitializeResponse) bool
	// Method is the underscore extension method AO calls.
	Method string
	// Params builds the request for the session's running turn. An error means
	// the method cannot carry this prompt; nothing is sent.
	Params func(sessionID string, prompt []acpsdk.ContentBlock) (any, error)
	// TakesImages optionally reports from the handshake whether this agent
	// reads images sent with the request. When it does not, a steer with an
	// image is refused before anything is sent, rather than accepted with the
	// image silently dropped. Nil means the request carries what Params takes.
	TakesImages func(acpsdk.InitializeResponse) bool
	// Outcome reads the agent's response.
	Outcome func(json.RawMessage) (SteerOutcome, error)
}

// SteerDrain lets an agent collect mid-turn guidance itself. AO holds the
// guidance and the agent asks for it with an agent-to-client request at its own
// step boundaries: after a batch of tool calls, and before it ends a response.
// Qwen Code's craft/drainMidTurnQueue works this way.
//
// Steer returns once the agent has collected the guidance, so AO never reports
// a steer the agent did not see. If the turn ends first, or the agent does not
// collect it within Wait, AO withdraws it and the caller keeps the message.
type SteerDrain struct {
	// Supported reports from the handshake whether the agent collects guidance.
	Supported func(acpsdk.InitializeResponse) bool
	// Method is the agent-to-client request the agent sends to collect
	// guidance. A legacy name without ACP's underscore prefix is aliased.
	Method string
	// Response encodes the guidance handed over by one request, oldest first.
	// texts is empty, never nil, when nothing is waiting.
	Response func(texts []string) any
	// Nudge optionally names a client-to-agent request, sent with the session
	// id, that asks the agent to collect guidance at its next opportunity
	// instead of its next ordinary boundary. Failures are ignored.
	Nudge string
	// Wait bounds how long Steer waits for collection. Zero uses five seconds.
	Wait time.Duration
}

// sessionSteeringRequest is ACP's _session/steering extension, as implemented
// by claude-agent-acp, codex-acp, and OMP. promptRequired is load-bearing: if
// the turn ends before the request arrives, the agent hands the text back to
// AO instead of silently starting a detached turn with no durable AO turn row.
var sessionSteeringRequest = SteerRequest{
	Supported: func(init acpsdk.InitializeResponse) bool {
		return extensionSupported(init.Meta, "steering")
	},
	Method: steeringMethod,
	Params: func(sessionID string, prompt []acpsdk.ContentBlock) (any, error) {
		return map[string]any{
			"sessionId": sessionID,
			"prompt":    prompt,
			"_meta": map[string]any{
				"steering": map[string]any{"idleBehavior": "promptRequired"},
			},
		}, nil
	},
	Outcome: func(raw json.RawMessage) (SteerOutcome, error) {
		var response steeringResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			return 0, fmt.Errorf("decode ACP steering response: %w", err)
		}
		switch response.Outcome {
		case "injected":
			return SteerInjected, nil
		case "promptRequired":
			return SteerNoRunningTurn, nil
		case "startedNewTurn":
			// AO explicitly requested promptRequired, so this means the extension
			// contract was not honored. Claiming this joined the active turn would
			// misattribute the provider's detached work in durable history.
			return 0, errors.New("ACP steering started a detached turn")
		case "failed":
			// OMP answers failed when it could not queue the guidance. The agent
			// answered, so this is a definite refusal rather than an unknown.
			reason := strings.TrimSpace(response.Reason)
			if reason == "" {
				reason = "no reason given"
			}
			return 0, &providerRefusal{err: fmt.Errorf("ACP steering failed: %s", reason)}
		default:
			return 0, fmt.Errorf("ACP steering returned unknown outcome %q", response.Outcome)
		}
	},
}

// steering is the steering protocol a conversation negotiated.
type steering struct {
	request *SteerRequest
	drain   *SteerDrain
}

// steeringFor returns the binding's protocol. A drain takes precedence; a
// binding that names neither uses ACP's _session/steering extension.
func steeringFor(cfg Config) steering {
	if cfg.SteerDrain != nil {
		return steering{drain: cfg.SteerDrain}
	}
	if cfg.SteerRequest != nil {
		return steering{request: cfg.SteerRequest}
	}
	return steering{request: &sessionSteeringRequest}
}

func (s steering) supported(init acpsdk.InitializeResponse) bool {
	switch {
	case s.drain != nil:
		return s.drain.Supported != nil && s.drain.Supported(init)
	case s.request != nil:
		return s.request.Supported != nil && s.request.Supported(init)
	default:
		return false
	}
}

// pendingSteer is guidance waiting for a drain agent to collect it. done
// receives exactly once, under conversation.mu: true when the agent collected
// it, false when the turn it was meant for ended first or Stop withdrew it.
type pendingSteer struct {
	turn string
	text string
	done chan bool
	// withdrawn means Stop settled it, not the end of the turn. Set under
	// conversation.mu before done is sent.
	withdrawn bool
}

// providerRefusal marks a request the ACP agent rejected on its own terms, as
// opposed to one that never got through. The Chat service classifies errors that
// implement ChatRefusal() as definitive, so a reserved steer delivery handle can
// settle as rejected instead of remaining uncertain forever.
type providerRefusal struct{ err error }

func (e *providerRefusal) Error() string     { return e.err.Error() }
func (e *providerRefusal) Unwrap() error     { return e.err }
func (e *providerRefusal) ChatRefusal() bool { return true }

// Steer maps AO's mid-turn guidance contract onto the steering protocol the
// agent negotiated.
func (c *conversation) Steer(
	ctx context.Context,
	providerTurnID string,
	msg ports.ChatUserMessage,
) (ports.ChatTurnRef, error) {
	if strings.TrimSpace(msg.Text) == "" {
		return ports.ChatTurnRef{}, errors.New("steer message text is empty")
	}
	prompt, err := c.promptContent(msg)
	if err != nil {
		return ports.ChatTurnRef{}, fmt.Errorf("%w: %w", ports.ErrChatSteerContentUnsupported, err)
	}

	c.mu.Lock()
	activeTurn := c.activeTurn
	sessionID := c.sessionID
	closed := c.closed
	stopping := activeTurn != "" && c.stoppingTurn == activeTurn
	c.mu.Unlock()
	if closed {
		return ports.ChatTurnRef{}, errConversationClosed
	}
	if providerTurnID == "" {
		providerTurnID = activeTurn
	}
	if providerTurnID == "" || providerTurnID != activeTurn {
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	}
	if stopping {
		return ports.ChatTurnRef{}, errSteerTurnStopping
	}
	if c.steering.drain != nil {
		return c.steerByDrain(ctx, providerTurnID, sessionID, prompt)
	}
	request := c.steering.request
	if request == nil {
		request = &sessionSteeringRequest
	}
	return c.steerByRequest(ctx, request, providerTurnID, sessionID, prompt)
}

func (c *conversation) steerByRequest(
	ctx context.Context,
	request *SteerRequest,
	providerTurnID string,
	sessionID string,
	prompt []acpsdk.ContentBlock,
) (ports.ChatTurnRef, error) {
	if request.TakesImages != nil && hasImage(prompt) {
		c.mu.Lock()
		handshake := c.handshake
		c.mu.Unlock()
		if !request.TakesImages(handshake) {
			return ports.ChatTurnRef{}, fmt.Errorf(
				"%w: this agent version drops images sent while a turn is running", ports.ErrChatSteerContentUnsupported)
		}
	}
	params, err := request.Params(sessionID, prompt)
	if err != nil {
		return ports.ChatTurnRef{}, fmt.Errorf("%w: %w", ports.ErrChatSteerContentUnsupported, err)
	}
	raw, err := c.conn.CallExtension(ctx, request.Method, params)
	if err != nil {
		if isACPSessionMissing(err) {
			// The agent answered: it has no session to steer, so the guidance was
			// definitively not delivered. Settle the delivery handle as rejected
			// rather than uncertain, and stop the dead provider.
			return ports.ChatTurnRef{}, &providerRefusal{err: c.providerSessionLost(err)}
		}
		wrapped := fmt.Errorf("ACP %s: %w", request.Method, err)
		// -32601 means the agent answered and does not implement this optional
		// extension; -32602 means it validated the request and refused it before
		// queueing anything. Both are definite refusals, not a lost transport
		// reply that might have delivered the guidance.
		if isACPMethodNotFound(err) || isACPInvalidParams(err) {
			return ports.ChatTurnRef{}, &providerRefusal{err: wrapped}
		}
		return ports.ChatTurnRef{}, wrapped
	}
	outcome, err := request.Outcome(raw)
	if err != nil {
		return ports.ChatTurnRef{}, err
	}
	switch outcome {
	case SteerInjected:
		c.mu.Lock()
		if c.activeTurn == providerTurnID {
			c.steerSegment++
		}
		c.mu.Unlock()
		return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
	case SteerNoRunningTurn:
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	default:
		return ports.ChatTurnRef{}, fmt.Errorf("ACP %s returned unknown outcome %d", request.Method, outcome)
	}
}

// steerByDrain parks the guidance for the agent to collect and waits until it
// does, the turn ends, or the wait runs out. It reports injected only after
// the agent has taken the text.
func (c *conversation) steerByDrain(
	ctx context.Context,
	providerTurnID string,
	sessionID string,
	prompt []acpsdk.ContentBlock,
) (ports.ChatTurnRef, error) {
	drain := c.steering.drain
	text, err := drainText(prompt)
	if err != nil {
		return ports.ChatTurnRef{}, fmt.Errorf("%w: %w", ports.ErrChatSteerContentUnsupported, err)
	}
	pending := &pendingSteer{turn: providerTurnID, text: text, done: make(chan bool, 1)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, errConversationClosed
	}
	if c.activeTurn != providerTurnID {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	}
	if c.stoppingTurn == providerTurnID {
		// Stop withdrew this turn's guidance between Steer's check and now.
		c.mu.Unlock()
		return ports.ChatTurnRef{}, errSteerTurnStopping
	}
	c.pendingSteers = append(c.pendingSteers, pending)
	c.mu.Unlock()

	// The wait starts before the nudge, so the nudge counts toward Wait.
	wait := drain.Wait
	if wait <= 0 {
		wait = defaultSteerDrainWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	// Off Steer's path: a write can stall before the call sees its context,
	// and Steer must still return within its wait. The guidance is already
	// parked, so the nudge only hurries the agent along. One nudge at a time,
	// so steers that time out behind a stalled write do not pile up more.
	if drain.Nudge != "" && c.nudging.CompareAndSwap(false, true) {
		nudgeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), min(steerNudgeTimeout, wait))
		go func() {
			defer c.nudging.Store(false)
			defer cancel()
			if err := c.legacyWire.call(nudgeCtx, drain.Nudge, map[string]string{"sessionId": sessionID}); err != nil {
				c.log.Debug("ACP steer nudge failed; guidance waits for the next boundary",
					"method", drain.Nudge, "error", err)
			}
		}()
	}

	settled := func(collected bool) (ports.ChatTurnRef, error) {
		if collected {
			return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
		}
		// withdrawn is written before done is sent, so reading it here is safe.
		if pending.withdrawn {
			return ports.ChatTurnRef{}, fmt.Errorf(
				"%w: the turn is being stopped, so the agent never collected the guidance",
				ports.ErrChatTurnNotSteerable)
		}
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	}
	select {
	case collected := <-pending.done:
		return settled(collected)
	case <-timer.C:
		if c.withdrawSteer(pending) {
			return ports.ChatTurnRef{}, fmt.Errorf(
				"%w: the agent did not collect the guidance within %s; it collects between tool calls",
				ports.ErrChatTurnNotSteerable, wait)
		}
	case <-ctx.Done():
		if c.withdrawSteer(pending) {
			return ports.ChatTurnRef{}, fmt.Errorf("%w: %w", ports.ErrChatTurnNotSteerable, ctx.Err())
		}
	}
	// The agent collected it, or the turn ended, in the same instant the wait
	// gave up. The settlement was sent under the lock withdrawSteer took.
	return settled(<-pending.done)
}

// withdrawSteer removes guidance the agent has not collected. It reports false
// when the guidance was already settled, in which case done holds the result.
func (c *conversation) withdrawSteer(pending *pendingSteer) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, queued := range c.pendingSteers {
		if queued == pending {
			c.pendingSteers = append(c.pendingSteers[:i], c.pendingSteers[i+1:]...)
			return true
		}
	}
	return false
}

// collectSteers answers a drain agent's request for waiting guidance. Only the
// session's running turn can collect; guidance meant for a turn that has ended
// is settled as not collected.
func (c *conversation) collectSteers(params json.RawMessage) any {
	var request struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(params, &request)
	texts := []string{}
	c.mu.Lock()
	drain := c.steering.drain
	ownSession := request.SessionID == "" ||
		request.SessionID == c.sessionID || request.SessionID == c.reportedSessionID
	if ownSession {
		for _, pending := range c.pendingSteers {
			collected := c.activeTurn != "" && pending.turn == c.activeTurn
			if collected {
				texts = append(texts, pending.text)
			}
			pending.done <- collected
		}
		c.pendingSteers = nil
		if len(texts) > 0 {
			c.steerSegment++
		}
	}
	c.mu.Unlock()
	return drain.Response(texts)
}

// errSteerTurnStopping refuses guidance for a turn Stop is stopping.
var errSteerTurnStopping = fmt.Errorf("%w: the turn is being stopped", ports.ErrChatTurnNotSteerable)

// WithdrawPendingSteers settles every steer still waiting for the agent as not
// collected, so a Steer blocked on a drain agent returns at once, and refuses
// any later steer for the running turn. Stop calls it before taking the Chat
// controller's dispatch lock, so a steer that already holds the lock but has
// not reached the agent yet is turned away too. A later turn steers as usual.
//
// The returned restore lets that turn take steers again after a Stop that did
// not stop it. Stops can overlap: while another of them is still stopping the
// turn, its steers stay refused. A restore that comes after the turn has
// ended does nothing, so it cannot lift the next turn's Stop.
func (c *conversation) WithdrawPendingSteers() func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	turn := c.activeTurn
	if turn != "" {
		if c.stoppingTurn != turn {
			c.stoppingTurn, c.stopAttempts = turn, 0
		}
		c.stopAttempts++
	}
	for _, pending := range c.pendingSteers {
		pending.withdrawn = true
	}
	c.releasePendingSteersLocked()
	if turn == "" {
		return nil
	}
	return sync.OnceFunc(func() { c.restoreSteering(turn) })
}

// restoreSteering gives back one withdrawal of turn's steering. See
// WithdrawPendingSteers.
func (c *conversation) restoreSteering(turn string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stoppingTurn != turn {
		return // that turn is over, and a later Stop may own the guard now
	}
	if c.stopAttempts > 0 {
		c.stopAttempts--
	}
	if c.stopAttempts == 0 {
		c.stoppingTurn = ""
	}
}

// releasePendingSteersLocked settles every waiting steer as not collected. It
// runs when the turn they were meant for ends or the conversation closes.
// Callers must hold c.mu.
func (c *conversation) releasePendingSteersLocked() {
	for _, pending := range c.pendingSteers {
		pending.done <- false
	}
	c.pendingSteers = nil
}

// drainText flattens a prompt for an agent that collects plain text. Any other
// block is refused rather than dropped.
func drainText(prompt []acpsdk.ContentBlock) (string, error) {
	parts := make([]string, 0, len(prompt))
	for _, block := range prompt {
		if block.Text == nil {
			return "", errors.New("this agent takes only text while a turn is running")
		}
		parts = append(parts, block.Text.Text)
	}
	return strings.Join(parts, "\n\n"), nil
}

func hasImage(prompt []acpsdk.ContentBlock) bool {
	for _, block := range prompt {
		if block.Image != nil {
			return true
		}
	}
	return false
}
