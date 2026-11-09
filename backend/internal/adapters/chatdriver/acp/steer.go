package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const steeringMethod = "_session/steering"

// defaultSteerDrainWait bounds how long a drain-mode steer holds the caller
// while the agent works toward its next step boundary. The Chat controller
// holds its dispatch lock for the whole call, so new sends wait too; Stop
// withdraws the guidance first (WithdrawPendingSteers). Past this, the
// guidance is withdrawn and the caller is told to try again.
const defaultSteerDrainWait = 5 * time.Second

// steerNudgeTimeout bounds the best-effort request asking a drain agent to
// collect guidance now. The agent answers it at once; a slow answer only means
// the guidance waits for the next ordinary boundary.
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

// SteerPrompt is for an agent that takes mid-turn guidance as another
// session/prompt on the running session. The turn's prompts overlap, and AO
// keeps one turn open across them: the turn settles when the last of them
// returns. Agents differ in which returns first, and both orders are a steer
// that took:
//
//   - Antigravity's `agy acp` answers the earlier prompt with end_turn at once
//     and carries the work on under the new prompt (a handoff).
//   - Hermes answers a `/steer` prompt at once and delivers the guidance to the
//     running turn at its next step (an acknowledgement).
//
// Steer returns when the first of the turn's prompts returns successfully. If
// the new prompt fails while an earlier one still runs, the agent refused it.
type SteerPrompt struct {
	// Supported reports from the handshake whether the agent steers this way.
	Supported func(acpsdk.InitializeResponse) bool
	// Prompt optionally reshapes the guidance into the prompt the agent steers
	// with, such as Hermes' `/steer <text>`. An error means the guidance cannot
	// be sent this way; nothing is sent.
	Prompt func([]acpsdk.ContentBlock) ([]acpsdk.ContentBlock, error)
	// Ack optionally recognises the agent message an agent sends to acknowledge
	// a steer prompt. A recognised message is not part of the reply. A non-nil
	// error means the agent declined the guidance. With Ack set, a steer prompt
	// that returns first with no recognised acknowledgement is a refusal: an
	// agent that does not know the command answered it some other way.
	Ack func(text string) (recognised bool, declined error)
	// Wait bounds how long Steer waits for the first prompt to return. Zero uses
	// five seconds. Past it the guidance is still in flight and the turn stays
	// open for it, so Steer reports it delivered.
	Wait time.Duration
}

// defaultSteerPromptWait bounds how long a prompt-mode steer waits for the
// agent to hand off the earlier prompt. Antigravity answers at once.
const defaultSteerPromptWait = 5 * time.Second

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
	prompt  *SteerPrompt
}

// steeringFor returns the binding's protocol. A drain takes precedence, then a
// prompt; a binding that names none uses ACP's _session/steering extension.
func steeringFor(cfg Config) steering {
	if cfg.SteerDrain != nil {
		return steering{drain: cfg.SteerDrain}
	}
	if cfg.SteerPrompt != nil {
		return steering{prompt: cfg.SteerPrompt}
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
	case s.prompt != nil:
		return s.prompt.Supported != nil && s.prompt.Supported(init)
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
	if c.steering.drain != nil {
		return c.steerByDrain(ctx, providerTurnID, sessionID, prompt)
	}
	if c.steering.prompt != nil {
		return c.steerByPrompt(ctx, providerTurnID, sessionID, msg.ClientMessageID, prompt)
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
	params, err := request.Params(sessionID, prompt)
	if err != nil {
		return ports.ChatTurnRef{}, fmt.Errorf("%w: %w", ports.ErrChatSteerContentUnsupported, err)
	}
	raw, err := c.conn.CallExtension(ctx, request.Method, params)
	if err != nil {
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
	c.pendingSteers = append(c.pendingSteers, pending)
	c.mu.Unlock()

	// The wait starts before the nudge, so the nudge counts toward Wait.
	wait := drain.Wait
	if wait <= 0 {
		wait = defaultSteerDrainWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	if drain.Nudge != "" {
		nudgeCtx, cancel := context.WithTimeout(ctx, min(steerNudgeTimeout, wait))
		if err := c.legacyWire.call(nudgeCtx, drain.Nudge, map[string]string{"sessionId": sessionID}); err != nil {
			c.log.Debug("ACP steer nudge failed; guidance waits for the next boundary",
				"method", drain.Nudge, "error", err)
		}
		cancel()
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

// steerAckWait bounds how long a steer prompt that returned first waits for
// the agent's acknowledgement message, which can trail the response.
const steerAckWait = 300 * time.Millisecond

// promptSteer is one steer prompt waiting to learn whether it took. result
// receives exactly once, under conversation.mu.
type promptSteer struct {
	turn    string
	seq     int
	result  chan error
	settled bool
	// ack is the agent's acknowledgement outcome, once recognised.
	ack      chan error
	ackKnown bool
}

func (w *promptSteer) settle(err error) {
	if !w.settled {
		w.settled = true
		w.result <- err
	}
}

// steerByPrompt sends the guidance as another session/prompt under the running
// turn's context, so Stop cancels it with the rest of the turn.
func (c *conversation) steerByPrompt(
	ctx context.Context,
	providerTurnID string,
	sessionID string,
	clientMessageID string,
	prompt []acpsdk.ContentBlock,
) (ports.ChatTurnRef, error) {
	binding := c.steering.prompt
	if binding.Prompt != nil {
		shaped, err := binding.Prompt(prompt)
		if err != nil {
			return ports.ChatTurnRef{}, fmt.Errorf("%w: %w", ports.ErrChatSteerContentUnsupported, err)
		}
		prompt = shaped
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, errConversationClosed
	}
	// A turn whose last prompt has already returned is settling: the agent
	// would take the text as a new turn of its own.
	if c.activeTurn != providerTurnID || c.turnCtx == nil || c.settlingTurn == providerTurnID {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	}
	c.promptSeq++
	seq := c.promptSeq
	c.outstanding[seq] = true
	waiter := &promptSteer{turn: providerTurnID, seq: seq, result: make(chan error, 1), ack: make(chan error, 1)}
	c.promptSteers = append(c.promptSteers, waiter)
	turnCtx := c.turnCtx
	c.mu.Unlock()

	messageID := strings.TrimSpace(clientMessageID)
	if messageID == "" {
		messageID = uuid.NewString()
	}
	go func() {
		resp, err := c.conn.Prompt(turnCtx, acpsdk.PromptRequest{
			SessionId: acpsdk.SessionId(sessionID),
			MessageId: &messageID,
			Prompt:    prompt,
			// A persistent host admits it into the running turn by this mark.
			Meta: map[string]any{persistenthost.ACPSteerPromptMetaKey: true},
		})
		c.finishTurnPrompt(providerTurnID, seq, resp, err)
	}()

	wait := binding.Wait
	if wait <= 0 {
		wait = defaultSteerPromptWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-waiter.result:
		if err != nil {
			wrapped := fmt.Errorf("ACP session/prompt while a turn is running: %w", err)
			var requestErr *acpsdk.RequestError
			if errors.As(err, &requestErr) || errors.Is(err, errSteerDeclined) || errors.Is(err, errSteerNotAcknowledged) {
				// The agent answered: a definite refusal. An earlier prompt still
				// owns the turn, so nothing was lost.
				return ports.ChatTurnRef{}, &providerRefusal{err: wrapped}
			}
			return ports.ChatTurnRef{}, wrapped
		}
	case <-timer.C:
		c.log.Debug("ACP steer prompt sent; no prompt of the turn has returned yet",
			"turn", providerTurnID, "wait", wait)
	case <-ctx.Done():
	}
	// Delivered, or still in flight: the turn stays open until every one of its
	// prompts returns, so the text is answered in this turn either way.
	c.mu.Lock()
	if c.activeTurn == providerTurnID {
		c.steerSegment++
	}
	c.mu.Unlock()
	return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
}

// errSteerDeclined marks guidance the agent acknowledged but did not take.
var errSteerDeclined = errors.New("the agent declined the guidance")

// steerAcknowledgement reports whether an agent message is a steer prompt's
// acknowledgement, which is not part of the reply, and records its outcome
// for the oldest steer still waiting to hear it.
func (c *conversation) steerAcknowledgement(turnID, text string) bool {
	if c.steering.prompt == nil || c.steering.prompt.Ack == nil || text == "" {
		return false
	}
	recognised, declined := c.steering.prompt.Ack(text)
	if !recognised {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeTurn != turnID || c.promptSeq == 0 {
		// Only a turn that sent a steer prompt hides the acknowledgement.
		return false
	}
	for _, w := range c.promptSteers {
		if w.turn == turnID && !w.ackKnown {
			w.ackKnown = true
			if declined != nil {
				w.ack <- fmt.Errorf("%w: %w", errSteerDeclined, declined)
			} else {
				w.ack <- nil
			}
			break
		}
	}
	return true
}

// finishTurnPrompt routes one prompt's result. The turn settles when the last
// of its prompts returns, with that prompt's result. Any successful return
// before that tells waiting steers that their guidance took; a steer prompt
// that fails while another prompt still runs was refused.
func (c *conversation) finishTurnPrompt(turnID string, seq int, resp acpsdk.PromptResponse, err error) {
	c.mu.Lock()
	if c.activeTurn != turnID {
		settledBySteer := c.steerSettledTurn == turnID
		c.mu.Unlock()
		if !settledBySteer {
			c.finishPrompt(turnID, resp, err)
		}
		// Otherwise an earlier prompt is answering after the turn settled.
		return
	}
	delete(c.outstanding, seq)
	last := len(c.outstanding) == 0
	var own *promptSteer
	for _, w := range c.promptSteers {
		if w.turn != turnID || w.settled {
			continue
		}
		if w.seq == seq {
			own = w
			continue
		}
		if err == nil {
			w.settle(nil) // another prompt handed the work on
		}
	}
	if own != nil {
		switch {
		case err != nil && !last:
			own.settle(err) // refused while an earlier prompt still runs
		case err == nil && !last && c.steering.prompt.Ack != nil && !own.ackKnown:
			// The acknowledgement can trail the response; let it decide.
			go c.awaitSteerAck(own)
		case err == nil && !last && own.ackKnown:
			own.settled = true
			own.result <- <-own.ack
		default:
			own.settle(nil) // it ran inside this turn
		}
	}
	if !last {
		c.mu.Unlock()
		if err != nil {
			c.log.Debug("ACP prompt returned an error while another prompt keeps the turn",
				"turn", turnID, "prompt", seq, "error", err)
		}
		return
	}
	if seq > 0 {
		c.steerSettledTurn = turnID
	}
	c.mu.Unlock()
	c.finishPrompt(turnID, resp, err)
}

// errSteerNotAcknowledged marks a steer prompt the agent answered without
// acknowledging it as a steer.
var errSteerNotAcknowledged = errors.New("the agent answered without acknowledging the guidance")

// awaitSteerAck settles a steer prompt that returned first from the agent's
// acknowledgement. No acknowledgement means the agent did not take it.
func (c *conversation) awaitSteerAck(w *promptSteer) {
	timer := time.NewTimer(steerAckWait)
	defer timer.Stop()
	var outcome error
	select {
	case outcome = <-w.ack:
	case <-timer.C:
		outcome = errSteerNotAcknowledged
	}
	c.mu.Lock()
	w.settle(outcome)
	c.mu.Unlock()
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

// WithdrawPendingSteers settles every steer still waiting for the agent as not
// collected, so a Steer blocked on a drain agent returns at once. Stop calls it
// before taking the Chat controller's dispatch lock.
func (c *conversation) WithdrawPendingSteers() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pending := range c.pendingSteers {
		pending.withdrawn = true
	}
	c.releasePendingSteersLocked()
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
