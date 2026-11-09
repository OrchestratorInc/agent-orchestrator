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
// session/prompt on the running session, as Antigravity's `agy acp` does. The
// agent answers the earlier prompt with end_turn once it has the guidance and
// carries on the same work under the new prompt, so AO keeps one turn open
// across them and settles it only when the latest prompt returns.
//
// Steer returns once the agent has handed off the earlier prompt. If the new
// prompt fails before that, the agent refused it and the earlier prompt keeps
// the turn.
type SteerPrompt struct {
	// Supported reports from the handshake whether the agent steers this way.
	Supported func(acpsdk.InitializeResponse) bool
	// Wait bounds how long Steer waits for the handoff. Zero uses five seconds.
	// Past it the guidance is still in flight and the turn stays open for it,
	// so Steer reports it delivered.
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

// steerByPrompt sends the guidance as another session/prompt under the running
// turn's context, so Stop cancels it with the rest of the turn. The new prompt
// becomes the one that settles the turn; Steer reports injected when the agent
// hands off the earlier prompt.
func (c *conversation) steerByPrompt(
	ctx context.Context,
	providerTurnID string,
	sessionID string,
	clientMessageID string,
	prompt []acpsdk.ContentBlock,
) (ports.ChatTurnRef, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, errConversationClosed
	}
	// A turn whose latest prompt has already returned is settling: the agent
	// would take the text as a new turn of its own.
	if c.activeTurn != providerTurnID || c.turnCtx == nil || c.settlingTurn == providerTurnID {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	}
	seq := c.promptSeq + 1
	c.promptSeq = seq
	handedOff := make(chan struct{})
	c.handedOff[seq-1] = handedOff
	turnCtx := c.turnCtx
	c.mu.Unlock()

	messageID := strings.TrimSpace(clientMessageID)
	if messageID == "" {
		messageID = uuid.NewString()
	}
	refused := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		resp, err := c.conn.Prompt(turnCtx, acpsdk.PromptRequest{
			SessionId: acpsdk.SessionId(sessionID),
			MessageId: &messageID,
			Prompt:    prompt,
			// A persistent host admits it into the running turn by this mark.
			Meta: map[string]any{persistenthost.ACPSteerPromptMetaKey: true},
		})
		// Exactly one of refused and finished fires, so Steer never reads a
		// refusal as a delivery.
		if c.refusedBeforeHandoff(providerTurnID, seq, err) {
			refused <- err
			return
		}
		c.finishTurnPrompt(providerTurnID, seq, resp, err)
		close(finished)
	}()

	wait := c.steering.prompt.Wait
	if wait <= 0 {
		wait = defaultSteerPromptWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	injected := func() (ports.ChatTurnRef, error) {
		c.mu.Lock()
		if c.activeTurn == providerTurnID {
			c.steerSegment++
		}
		c.mu.Unlock()
		return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
	}
	select {
	case <-handedOff:
		return injected()
	case err := <-refused:
		wrapped := fmt.Errorf("ACP session/prompt while a turn is running: %w", err)
		var requestErr *acpsdk.RequestError
		if errors.As(err, &requestErr) {
			// The agent answered: a definite refusal. The earlier prompt still
			// owns the turn, so nothing was lost.
			return ports.ChatTurnRef{}, &providerRefusal{err: wrapped}
		}
		return ports.ChatTurnRef{}, wrapped
	case <-finished:
		// The steer prompt itself settled the turn: the agent ran it to the end
		// inside this turn before the earlier prompt returned.
		return injected()
	case <-timer.C:
	case <-ctx.Done():
	}
	// The agent holds the guidance but has not handed off yet. The turn stays
	// open until the new prompt returns, so the text is answered in this turn.
	c.log.Debug("ACP steer prompt sent; the agent has not handed off the earlier prompt yet",
		"turn", providerTurnID, "wait", wait)
	return injected()
}

// refusedBeforeHandoff reports whether a steer prompt failed while the prompt
// before it still owned the turn, meaning the agent refused the guidance. It
// then gives the turn back to that earlier prompt.
func (c *conversation) refusedBeforeHandoff(turnID string, seq int, err error) bool {
	if err == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeTurn != turnID || c.promptSeq != seq {
		return false
	}
	if _, waiting := c.handedOff[seq-1]; !waiting {
		return false
	}
	delete(c.handedOff, seq-1)
	c.promptSeq = seq - 1
	return true
}

// finishTurnPrompt routes one prompt's result. Only the turn's latest prompt
// settles it. An earlier one returning means the agent handed the work to the
// steer prompt after it; its usage is left to the prompt that settles.
func (c *conversation) finishTurnPrompt(turnID string, seq int, resp acpsdk.PromptResponse, err error) {
	c.mu.Lock()
	switch {
	case c.activeTurn == turnID && seq < c.promptSeq:
		if done, ok := c.handedOff[seq]; ok {
			close(done)
			delete(c.handedOff, seq)
		}
		c.mu.Unlock()
		if err != nil {
			c.log.Debug("ACP prompt handed off with an error; the steer prompt keeps the turn",
				"turn", turnID, "prompt", seq, "error", err)
		}
		return
	case c.activeTurn == turnID && seq > 0:
		c.steerSettledTurn = turnID
	case c.activeTurn != turnID && c.steerSettledTurn == turnID:
		// An earlier prompt answering after the steer prompt settled the turn.
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.finishPrompt(turnID, resp, err)
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
