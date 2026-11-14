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

const defaultSteerDrainWait = 5 * time.Second

const steerNudgeTimeout = time.Second

type steeringResponse struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

type SteerOutcome int

const (
	SteerInjected SteerOutcome = iota + 1
	SteerNoRunningTurn
)

type SteerRequest struct {
	Supported   func(acpsdk.InitializeResponse) bool
	Method      string
	Params      func(sessionID string, prompt []acpsdk.ContentBlock) (any, error)
	TakesImages func(acpsdk.InitializeResponse) bool
	Outcome     func(json.RawMessage) (SteerOutcome, error)
}

type SteerDrain struct {
	Supported func(acpsdk.InitializeResponse) bool
	Method    string
	Response  func(texts []string) any
	Nudge     string
	Wait      time.Duration
}

type SteerPrompt struct {
	Supported func(acpsdk.InitializeResponse) bool
	Prompt    func([]acpsdk.ContentBlock) ([]acpsdk.ContentBlock, error)
	Ack       func(text string) (recognised bool, declined error)
	Wait      time.Duration
}

const defaultSteerPromptWait = 5 * time.Second

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
			return 0, errors.New("ACP steering started a detached turn")
		case "failed":
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

type steering struct {
	request *SteerRequest
	drain   *SteerDrain
	prompt  *SteerPrompt
}

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

type pendingSteer struct {
	turn      string
	text      string
	done      chan bool
	withdrawn bool
}

type providerRefusal struct{ err error }

func (e *providerRefusal) Error() string     { return e.err.Error() }
func (e *providerRefusal) Unwrap() error     { return e.err }
func (e *providerRefusal) ChatRefusal() bool { return true }

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
			return ports.ChatTurnRef{}, &providerRefusal{err: c.providerSessionLost(err)}
		}
		wrapped := fmt.Errorf("ACP %s: %w", request.Method, err)
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
		c.mu.Unlock()
		return ports.ChatTurnRef{}, errSteerTurnStopping
	}
	c.pendingSteers = append(c.pendingSteers, pending)
	c.mu.Unlock()

	wait := drain.Wait
	if wait <= 0 {
		wait = defaultSteerDrainWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

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
	return settled(<-pending.done)
}

type promptSteer struct {
	turn        string
	seq         int
	result      chan error
	settled     bool
	ackKnown    bool
	ackSeen     chan struct{}
	awaitingAck bool
	sent        bool
	withdrawn   bool
}

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

type promptAnswer struct {
	resp acpsdk.PromptResponse
	err  error
}

const adoptedPromptSeq = -1

var errSteerUnconfirmed = errors.New("the agent has not shown that it took the guidance")

var errSteerCancelled = fmt.Errorf("%w: the turn was stopped first", errSteerUnconfirmed)

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
	if c.activeTurn != providerTurnID || c.turnCtx == nil || c.settlingTurn == providerTurnID || c.turnResult != nil {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	}
	if c.stoppingTurn == providerTurnID {
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
			return ports.ChatTurnRef{}, err
		}
		if err != nil {
			return ports.ChatTurnRef{}, steerPromptError(err)
		}
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
		return settled(err)
	default:
	}
	c.log.Debug("ACP steer prompt sent; the agent has not taken it yet",
		"turn", providerTurnID, "wait", wait)
	return ports.ChatTurnRef{}, fmt.Errorf("ACP session/prompt while a turn is running: %w", gaveUp)
}

func steerPromptError(err error) error {
	if errors.Is(err, errSteerTurnStopping) {
		return err
	}
	if steerTurnEnded(err) {
		return fmt.Errorf("%w: the turn ended before the steer prompt reached the agent", ports.ErrChatNoSteerableTurn)
	}
	wrapped := fmt.Errorf("ACP session/prompt while a turn is running: %w", err)
	var requestErr *acpsdk.RequestError
	if errors.As(err, &requestErr) || errors.Is(err, errSteerDeclined) || errors.Is(err, errSteerNotAcknowledged) {
		return &providerRefusal{err: wrapped}
	}
	return wrapped
}

func steerTurnEnded(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	data, _ := requestErr.Data.(map[string]any)
	ended, _ := data[persistenthost.ACPSteerTurnEndedDataKey].(bool)
	return ended
}

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

func (c *conversation) finishTurnPrompt(turnID string, seq int, resp acpsdk.PromptResponse, err error) {
	c.mu.Lock()
	if c.activeTurn != turnID || c.settlingTurn == turnID {
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
	lostSession := seq > 0 && err != nil && isACPSessionMissing(err)
	if lostSession {
		c.sessionLost = true
	}
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
			w.settle(err)
		case w.seq == seq && cancelled:
			w.settle(errSteerCancelled)
		case w.seq == seq && !acknowledged:
			c.steerTookLocked(w)
		case w.seq == seq && !w.awaitingAck:
			w.awaitingAck = true
			go c.awaitSteerAck(w)
		case w.seq == handoffTo:
			c.steerTookLocked(w)
		}
	}
	final, done := c.turnEndLocked(turnID)
	if !done && result && len(c.outstanding) > 0 {
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
		c.log.Warn("chat: ACP provider no longer holds the session; stopping it so the session can be resumed",
			"turn", turnID, "error", err)
		if stopErr := c.stopLostProvider(); stopErr != nil {
			c.log.Error("chat: failed to stop ACP provider after its session was lost",
				"turn", turnID, "error", stopErr)
		}
	}
}

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

func (c *conversation) dropUnsentPromptLocked(turnID string, seq int) (promptAnswer, bool) {
	if c.activeTurn != turnID || c.settlingTurn == turnID {
		return promptAnswer{}, false
	}
	delete(c.outstanding, seq)
	return c.turnEndLocked(turnID)
}

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

const promptStragglerWait = 10 * time.Second

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
	if c.stragglers == nil {
		c.stragglers = map[string]int{}
	}
	c.stragglers[turnID] = len(c.outstanding)
	c.steerSettledTurn = turnID
	c.settlingTurn = turnID
	c.mu.Unlock()
	c.finishPrompt(turnID, result.resp, result.err)
}

func (c *conversation) adoptedPromptHandoff(answer promptAnswer) {
	if answer.err != nil {
		c.log.Debug("adopted ACP prompt returned an error while a steer prompt keeps the turn", "error", answer.err)
		return
	}
	if answer.resp.StopReason == acpsdk.StopReasonCancelled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeTurn == "" || !c.adoptedPrompt || c.steering.prompt == nil || c.steering.prompt.Ack != nil {
		return
	}
	next := c.nextOutstandingLocked(adoptedPromptSeq)
	for _, w := range c.promptSteers {
		if w.turn == c.activeTurn && w.seq == next {
			c.steerTookLocked(w)
		}
	}
}

var errSteerDeclined = errors.New("the agent declined the guidance")

var errSteerNotAcknowledged = errors.New("the agent answered without acknowledging the guidance")

const steerAckWait = 300 * time.Millisecond

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
		return false
	}
	for _, w := range c.promptSteers {
		if w.turn != turnID || w.ackKnown || w.settled {
			continue
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

func (c *conversation) awaitSteerAck(w *promptSteer) {
	timer := time.NewTimer(steerAckWait)
	defer timer.Stop()
	select {
	case <-w.ackSeen:
		return
	case <-timer.C:
	}
	c.mu.Lock()
	w.settle(errSteerNotAcknowledged)
	c.mu.Unlock()
}

const noPrompt = -2

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

var errSteerTurnStopping = fmt.Errorf("%w: the turn is being stopped", ports.ErrChatTurnNotSteerable)

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

var errSteerStopping = fmt.Errorf("%w: the turn is being stopped", errSteerUnconfirmed)

func (c *conversation) restoreSteering(turn string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stoppingTurn != turn {
		return
	}
	if c.stopAttempts > 0 {
		c.stopAttempts--
	}
	if c.stopAttempts == 0 {
		c.stoppingTurn = ""
	}
}

func (c *conversation) releasePendingSteersLocked() {
	for _, pending := range c.pendingSteers {
		pending.done <- false
	}
	c.pendingSteers = nil
}

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
