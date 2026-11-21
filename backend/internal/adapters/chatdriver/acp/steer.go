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

// SteerPrompt is for an agent that takes mid-turn guidance as another
// session/prompt on the running session. The turn's prompts overlap, and AO
// keeps one turn open across them until every one has returned. Agents show
// in two ways that they took the guidance:
//
//   - Antigravity's `agy acp` answers the earlier prompt with end_turn at once
//     and carries the work on under the new prompt (a handoff).
//   - Hermes answers a `/steer` prompt at once with a note, and delivers the
//     guidance to the running turn at its next step (an acknowledgement; see
//     Ack).
//
// Steer reports the guidance delivered only on that evidence, or when the
// steer prompt itself completes inside the turn. If the steer prompt fails
// instead, the agent refused it and the earlier prompt keeps the turn.
type SteerPrompt struct {
	// Supported reports from the handshake whether the agent steers this way.
	Supported func(acpsdk.InitializeResponse) bool
	// Prompt optionally reshapes the guidance into the prompt the agent steers
	// with, such as Hermes' `/steer <text>`. An error means the guidance cannot
	// be sent this way; nothing is sent.
	Prompt func([]acpsdk.ContentBlock) ([]acpsdk.ContentBlock, error)
	// Ack optionally recognises the agent message that acknowledges a steer
	// prompt. A recognised message is not part of the reply, and a non-nil
	// error means the agent declined the guidance. With Ack set, only the
	// acknowledgement says whether a steer took: other prompts' answers say
	// nothing about it, and a steer prompt that answers with none is a refusal,
	// since an agent that does not know the command answered it another way.
	Ack func(text string) (recognised bool, declined error)
	// Wait bounds how long Steer waits for the agent to show it took the
	// guidance. Zero uses five seconds. Past it the guidance may still reach the
	// turn, so Steer reports the delivery uncertain rather than guess.
	Wait time.Duration
}

// defaultSteerPromptWait bounds how long a prompt-mode steer waits for the
// agent to take the guidance. Antigravity hands off at once.
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

// promptSteer is one steer prompt waiting to learn whether it took. result
// receives exactly once, under conversation.mu.
type promptSteer struct {
	turn    string
	seq     int
	result  chan error
	settled bool
	// ackKnown is set once the agent's acknowledgement is recognised (see
	// SteerPrompt.Ack), and ackSeen is closed then. awaitingAck means its
	// prompt has answered and awaitSteerAck is waiting for the note.
	ackKnown    bool
	ackSeen     chan struct{}
	awaitingAck bool
	// sent is set once the prompt is handed to the connection; until then
	// Stop can withdraw it, and withdrawn is set so it is never written.
	sent      bool
	withdrawn bool
}

// steerTookLocked settles a steer prompt as taken and starts the reply's next
// segment in the same critical section, as a drain does. Text the agent
// streams without a message id from here on then renders after the steer,
// even if it arrives before Steer has returned. Callers must hold c.mu.
func (c *conversation) steerTookLocked(w *promptSteer) {
	if w.settled {
		return
	}
	w.settle(nil)
	if c.activeTurn == w.turn {
		c.steerSegment++
	}
}

func (w *promptSteer) settle(err error) {
	if !w.settled {
		w.settled = true
		w.result <- err
	}
}

// promptAnswer is one session/prompt's answer.
type promptAnswer struct {
	resp acpsdk.PromptResponse
	err  error
}

// adoptedPromptSeq stands for the prompt an earlier daemon attachment sent,
// which this conversation adopted on live reconnect.
const adoptedPromptSeq = -1

// errSteerUnconfirmed marks a steer prompt whose fate is not known yet. The
// guidance may still reach the turn, so it is neither delivered nor refused.
var errSteerUnconfirmed = errors.New("the agent has not shown that it took the guidance")

// errSteerCancelled marks a steer prompt the agent answered as cancelled: the
// turn was stopped, and whether the agent acted on the guidance first is not
// known.
var errSteerCancelled = fmt.Errorf("%w: the turn was stopped first", errSteerUnconfirmed)

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
	// A turn that is settling, or whose result the host has already given,
	// is over: the agent would take the text as a new turn of its own.
	if c.activeTurn != providerTurnID || c.turnCtx == nil || c.settlingTurn == providerTurnID || c.turnResult != nil {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	}
	if c.stoppingTurn == providerTurnID {
		// Stop withdrew this turn's guidance between Steer's check and now.
		c.mu.Unlock()
		return ports.ChatTurnRef{}, errSteerTurnStopping
	}
	c.promptSeq++
	seq := c.promptSeq
	c.outstanding[seq] = true
	waiter := &promptSteer{turn: providerTurnID, seq: seq, result: make(chan error, 1), ackSeen: make(chan struct{})}
	c.promptSteers = append(c.promptSteers, waiter)
	turnCtx := c.turnCtx
	c.mu.Unlock()

	messageID := strings.TrimSpace(clientMessageID)
	if messageID == "" {
		messageID = uuid.NewString()
	}
	// A persistent host admits it into the running turn by this mark. An
	// acknowledging agent answers the steer as a command while the turn's
	// earlier prompt keeps the work, so that answer is never the turn's result.
	meta := map[string]any{persistenthost.ACPSteerPromptMetaKey: true}
	if binding.Ack != nil {
		meta[persistenthost.ACPSteerKeepsTurnMetaKey] = true
	}
	go func() {
		if c.promptSending != nil {
			c.promptSending(seq)
		}
		c.mu.Lock()
		if waiter.withdrawn || c.activeTurn != providerTurnID || c.stoppingTurn == providerTurnID {
			// Stop began before the prompt was written. Sent now, it would reach
			// the agent after the cancel and run as fresh work.
			waiter.settle(errSteerTurnStopping)
			final, done := c.dropUnsentPromptLocked(providerTurnID, seq)
			c.mu.Unlock()
			if done {
				c.finishPrompt(providerTurnID, final.resp, final.err)
			}
			return
		}
		waiter.sent = true
		c.mu.Unlock()
		resp, err := c.conn.Prompt(turnCtx, acpsdk.PromptRequest{
			SessionId: acpsdk.SessionId(sessionID),
			MessageId: &messageID,
			Prompt:    prompt,
			Meta:      meta,
		})
		if c.promptReturned != nil {
			c.promptReturned(seq)
		}
		c.finishTurnPrompt(providerTurnID, seq, resp, err)
	}()

	wait := binding.Wait
	if wait <= 0 {
		wait = defaultSteerPromptWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	settled := func(err error) (ports.ChatTurnRef, error) {
		if errors.Is(err, ports.ErrChatProviderSessionLost) {
			return ports.ChatTurnRef{}, err // a refusal; see finishTurnPrompt
		}
		if err != nil {
			return ports.ChatTurnRef{}, steerPromptError(err)
		}
		// steerTookLocked has already started the reply's next segment.
		return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
	}
	var gaveUp error
	select {
	case err := <-waiter.result:
		return settled(err)
	case <-timer.C:
		gaveUp = fmt.Errorf("%w within %s; it may still reach this turn", errSteerUnconfirmed, wait)
	case <-ctx.Done():
		gaveUp = fmt.Errorf("%w: %w", errSteerUnconfirmed, ctx.Err())
	}
	select {
	case err := <-waiter.result:
		// The answer came in the same instant the wait gave up.
		return settled(err)
	default:
	}
	c.log.Debug("ACP steer prompt sent; the agent has not taken it yet",
		"turn", providerTurnID, "wait", wait)
	return ports.ChatTurnRef{}, fmt.Errorf("ACP session/prompt while a turn is running: %w", gaveUp)
}

// steerPromptError classifies a steer prompt that did not take. The agent
// answering with an error is a definite refusal, and so is the host turning it
// away because the turn had already ended; anything else is uncertain.
func steerPromptError(err error) error {
	if errors.Is(err, errSteerTurnStopping) {
		return err // never sent
	}
	if steerTurnEnded(err) {
		return fmt.Errorf("%w: the turn ended before the steer prompt reached the agent", ports.ErrChatNoSteerableTurn)
	}
	wrapped := fmt.Errorf("ACP session/prompt while a turn is running: %w", err)
	var requestErr *acpsdk.RequestError
	if errors.As(err, &requestErr) || errors.Is(err, errSteerDeclined) || errors.Is(err, errSteerNotAcknowledged) {
		// The agent answered: a definite refusal. An earlier prompt still owns
		// the turn, so nothing was lost.
		return &providerRefusal{err: wrapped}
	}
	return wrapped
}

// steerTurnEnded reports whether the persistent host refused a steer prompt
// because its turn had already ended.
func steerTurnEnded(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	data, _ := requestErr.Data.(map[string]any)
	ended, _ := data[persistenthost.ACPSteerTurnEndedDataKey].(bool)
	return ended
}

// hostResultEventID returns the persistent host's receipt on a session/prompt
// answer, or "". The host puts it only on the turn's result.
func hostResultEventID(resp acpsdk.PromptResponse, err error) string {
	eventID, _ := resp.Meta[persistenthost.ACPEventIDMetaKey].(string)
	var requestErr *acpsdk.RequestError
	if eventID == "" && errors.As(err, &requestErr) {
		if data, ok := requestErr.Data.(map[string]any); ok {
			eventID, _ = data[persistenthost.ACPEventIDMetaKey].(string)
		}
	}
	return eventID
}

// finishTurnPrompt routes one of the running turn's prompt answers.
//
// A successful answer while a later prompt still runs is a handoff to the next
// of them: the agent took that prompt's guidance and carries the work on under
// it. A steer prompt's own answer settles its steer: an error is a refusal, a
// cancelled answer is uncertain, success means it ran inside the turn.
//
// The turn settles once every prompt this conversation sent has returned.
// Behind a persistent host its result is the answer carrying the host's
// receipt, the last answer the host saw. That need not be the last one to get
// here, because the SDK wakes each Prompt caller on its own, so the receipt
// answer is kept until the others return. An adopted turn also waits for that
// receipt, since its first prompt belongs to the host, not this conversation.
func (c *conversation) finishTurnPrompt(turnID string, seq int, resp acpsdk.PromptResponse, err error) {
	c.mu.Lock()
	if c.activeTurn != turnID || c.settlingTurn == turnID {
		// The turn is over. A steer prompt's late answer never ends a turn: its
		// turn ended without it, and another turn may be running now. The
		// turn's own prompt keeps the path to finishPrompt it had before
		// steering, unless its steered turn has already settled.
		straggler := c.takeStragglerLocked(turnID)
		lateAnswer := seq > 0 || straggler || c.steerSettledTurn == turnID
		c.mu.Unlock()
		if !lateAnswer {
			c.finishPrompt(turnID, resp, err)
		}
		return
	}
	delete(c.outstanding, seq)
	answer := promptAnswer{resp: resp, err: err}
	c.lastAnswer = &answer
	result := hostResultEventID(resp, err) != ""
	if result {
		c.turnResult = &answer
	}
	acknowledged := c.steering.prompt != nil && c.steering.prompt.Ack != nil
	cancelled := err == nil && resp.StopReason == acpsdk.StopReasonCancelled
	// A steer prompt answered "Session not found" was not delivered, and the
	// provider can never serve this conversation again. It is stopped below,
	// whether or not the steer's Steer is still waiting for the answer.
	lostSession := seq > 0 && err != nil && isACPSessionMissing(err)
	if lostSession {
		c.sessionLost = true
	}
	// A handoff passes the work to the next prompt of the turn, the one that
	// arrived after this one. A cancelled answer hands nothing on, and an
	// acknowledging agent says itself whether a steer took.
	handoffTo := noPrompt
	if !acknowledged && err == nil && !cancelled && !result {
		handoffTo = c.nextOutstandingLocked(seq)
	}
	for _, w := range c.promptSteers {
		if w.turn != turnID || w.settled {
			continue
		}
		switch {
		case w.seq == seq && lostSession:
			w.settle(&providerRefusal{err: fmt.Errorf("%w: %w", ports.ErrChatProviderSessionLost, err)})
		case w.seq == seq && err != nil:
			w.settle(err) // refused
		case w.seq == seq && cancelled:
			w.settle(errSteerCancelled) // stopped before the agent acted on it
		case w.seq == seq && !acknowledged:
			c.steerTookLocked(w) // it ran inside this turn
		case w.seq == seq && !w.awaitingAck:
			// The acknowledgement can trail the answer; let it decide.
			w.awaitingAck = true
			go c.awaitSteerAck(w)
		case w.seq == handoffTo:
			c.steerTookLocked(w) // the earlier prompt handed the work on to it
		}
	}
	final, done := c.turnEndLocked(turnID)
	if !done && result && len(c.outstanding) > 0 {
		// The host has ended the turn; its other prompts' answers are on
		// their way. A steer command's answer could still go missing, so
		// the wait for them is bounded.
		wait := c.stragglerWait
		if wait <= 0 {
			wait = promptStragglerWait
		}
		go c.finishAfterStragglers(turnID, c.turnResult, wait)
	}
	c.mu.Unlock()
	if done {
		c.finishPrompt(turnID, final.resp, final.err)
	} else if err != nil && !lostSession {
		c.log.Debug("ACP prompt returned an error while another prompt keeps the turn",
			"turn", turnID, "prompt", seq, "error", err)
	}
	if lostSession {
		// Stop it now rather than when the turn's other prompts fail.
		c.log.Warn("chat: ACP provider no longer holds the session; stopping it so the session can be resumed",
			"turn", turnID, "error", err)
		if stopErr := c.stopLostProvider(); stopErr != nil {
			c.log.Error("chat: failed to stop ACP provider after its session was lost",
				"turn", turnID, "error", stopErr)
		}
	}
}

// turnEndLocked reports whether every prompt of the turn has returned, and if
// so the answer the turn ends with: the host's result when it gave one,
// otherwise the last answer. Callers must hold c.mu and finish the turn with
// that answer once they release it.
func (c *conversation) turnEndLocked(turnID string) (promptAnswer, bool) {
	if len(c.outstanding) > 0 || (c.adoptedPrompt && c.turnResult == nil) || c.lastAnswer == nil {
		return promptAnswer{}, false
	}
	final := *c.lastAnswer
	if c.turnResult != nil {
		final = *c.turnResult
	}
	if c.promptSeq > 0 || c.adoptedPrompt {
		c.steerSettledTurn = turnID
	}
	return final, true
}

// dropUnsentPromptLocked takes back a steer prompt Stop overtook before it was
// written. If the turn's other prompts have all returned meanwhile, the turn
// ends now, since no answer for this one will come. Callers must hold c.mu.
func (c *conversation) dropUnsentPromptLocked(turnID string, seq int) (promptAnswer, bool) {
	if c.activeTurn != turnID || c.settlingTurn == turnID {
		return promptAnswer{}, false
	}
	delete(c.outstanding, seq)
	return c.turnEndLocked(turnID)
}

// takeStragglerLocked reports whether turnID ended without the answers of some
// of its prompts, and counts this answer as one of them. Callers must hold c.mu.
func (c *conversation) takeStragglerLocked(turnID string) bool {
	left, ok := c.stragglers[turnID]
	if !ok {
		return false
	}
	if left <= 1 {
		delete(c.stragglers, turnID)
	} else {
		c.stragglers[turnID] = left - 1
	}
	return true
}

// promptStragglerWait bounds how long a turn whose host result has arrived
// waits for its other prompts' answers, which normally follow at once.
const promptStragglerWait = 10 * time.Second

// finishAfterStragglers ends a turn with the host's result if its other
// prompts have still not answered after wait. Their late answers are then
// dropped.
func (c *conversation) finishAfterStragglers(turnID string, result *promptAnswer, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	<-timer.C
	c.mu.Lock()
	if c.activeTurn != turnID || c.turnResult != result || len(c.outstanding) == 0 || c.settlingTurn == turnID {
		c.mu.Unlock()
		return
	}
	c.log.Warn("ACP turn ended without every prompt answering; finishing it with the host's result",
		"turn", turnID, "unanswered", len(c.outstanding))
	// Their answers can still come after later turns have settled, so this
	// turn is remembered until each has arrived.
	if c.stragglers == nil {
		c.stragglers = map[string]int{}
	}
	c.stragglers[turnID] = len(c.outstanding)
	c.steerSettledTurn = turnID
	c.settlingTurn = turnID // the stragglers' answers are late from here on
	c.mu.Unlock()
	c.finishPrompt(turnID, result.resp, result.err)
}

// adoptedPromptHandoff hears that the prompt an earlier attachment sent
// answered while a steer prompt of this attachment's still runs: the agent
// took the guidance and carries the turn on under the steer.
func (c *conversation) adoptedPromptHandoff(answer promptAnswer) {
	if answer.err != nil {
		c.log.Debug("adopted ACP prompt returned an error while a steer prompt keeps the turn", "error", answer.err)
		return
	}
	if answer.resp.StopReason == acpsdk.StopReasonCancelled {
		return // a cancelled answer hands nothing on, as in finishTurnPrompt
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeTurn == "" || !c.adoptedPrompt || c.steering.prompt == nil || c.steering.prompt.Ack != nil {
		// An acknowledging agent says itself whether a steer took.
		return
	}
	// The adopted prompt came before every prompt this attachment sent.
	next := c.nextOutstandingLocked(adoptedPromptSeq)
	for _, w := range c.promptSteers {
		if w.turn == c.activeTurn && w.seq == next {
			c.steerTookLocked(w)
		}
	}
}

// errSteerDeclined marks guidance the agent acknowledged but did not take.
var errSteerDeclined = errors.New("the agent declined the guidance")

// errSteerNotAcknowledged marks a steer prompt the agent answered without
// acknowledging it as a steer.
var errSteerNotAcknowledged = errors.New("the agent answered without acknowledging the guidance")

// steerAckWait bounds how long a steer prompt that has answered waits for the
// agent's acknowledgement message, which can trail the answer.
const steerAckWait = 300 * time.Millisecond

// steerAcknowledgement reports whether an agent message is a steer prompt's
// acknowledgement, which is not part of the reply. It settles the oldest steer
// still waiting to hear one: the agent took the guidance or declined it.
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
		if w.turn != turnID || w.ackKnown || w.settled {
			continue // a steer the agent refused outright sends no note
		}
		w.ackKnown = true
		close(w.ackSeen)
		if declined != nil {
			w.settle(fmt.Errorf("%w: %w", errSteerDeclined, declined))
		} else {
			c.steerTookLocked(w)
		}
		break
	}
	return true
}

// awaitSteerAck gives a steer prompt that answered before its acknowledgement
// a moment for the note to arrive. No note means the agent did not take it.
func (c *conversation) awaitSteerAck(w *promptSteer) {
	timer := time.NewTimer(steerAckWait)
	defer timer.Stop()
	select {
	case <-w.ackSeen:
		return // steerAcknowledgement settled it
	case <-timer.C:
	}
	c.mu.Lock()
	w.settle(errSteerNotAcknowledged)
	c.mu.Unlock()
}

// noPrompt is no prompt of the turn.
const noPrompt = -2

// nextOutstandingLocked returns the earliest prompt sent after seq that has
// not returned yet, or noPrompt. A steer prompt registered but not yet written
// has not reached the agent, so the work cannot have gone to it. Callers must
// hold c.mu.
func (c *conversation) nextOutstandingLocked(seq int) int {
	unsent := map[int]bool{}
	for _, w := range c.promptSteers {
		if w.turn == c.activeTurn && !w.sent {
			unsent[w.seq] = true
		}
	}
	next := noPrompt
	for other := range c.outstanding {
		if other > seq && !unsent[other] && (next == noPrompt || other < next) {
			next = other
		}
	}
	return next
}

// resetPromptSteeringLocked clears the running turn's prompt bookkeeping. A
// steer still waiting when its turn ends without hearing back is uncertain.
// Callers must hold c.mu.
func (c *conversation) resetPromptSteeringLocked() {
	for _, w := range c.promptSteers {
		if !w.awaitingAck {
			w.settle(errSteerUnconfirmed)
		}
	}
	c.promptSteers = nil
	c.promptSeq = 0
	c.outstanding = nil
	c.adoptedPrompt = false
	c.turnResult = nil
	c.lastAnswer = nil
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
	// A steer prompt not written yet never will be. One already with the agent
	// cannot be taken back: its Steer returns now, so Stop does not wait out
	// the steer's wait, and whether the agent took it is not known.
	for _, w := range c.promptSteers {
		if w.turn != turn {
			continue
		}
		if !w.sent {
			w.withdrawn = true
			w.settle(errSteerTurnStopping)
			continue
		}
		w.settle(errSteerStopping)
	}
	if turn == "" {
		return nil
	}
	return sync.OnceFunc(func() { c.restoreSteering(turn) })
}

// errSteerStopping settles a steer prompt still waiting when Stop begins.
var errSteerStopping = fmt.Errorf("%w: the turn is being stopped", errSteerUnconfirmed)

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
