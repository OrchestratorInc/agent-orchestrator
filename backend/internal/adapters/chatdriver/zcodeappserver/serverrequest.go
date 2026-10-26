package zcodeappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// approvalWait bounds how long a parked server request is left unanswered. No
// server-side timeout exists for permission or user-input requests, so an
// approval nobody resolves would hang the turn indefinitely; on expiry AO
// refuses — to every waiting frame id — rather than deciding for the user.
const approvalWait = 30 * time.Minute

// recentlyResolvedLimit bounds the memory of answered requests kept to answer a
// re-announce that crossed the reply on the wire.
const recentlyResolvedLimit = 64

// errConversationClosed reports a decision arriving after the controller ended.
var errConversationClosed = errors.New("conversation closed")

// resolvedReply is what settled a request, kept so a re-announce that crossed
// the settlement on the wire gets the same answer: AO's decision, or the
// refusal a timeout, a provider-side resolution or reconciliation sent.
type resolvedReply struct {
	reply any
	err   error
	// method is what the request was, so a provider-side resolution of a
	// user-input request can still be reported as input.resolved after the
	// record left the pending map.
	method string
}

// parkedRequest is one server->client request waiting on a person, shared by
// every frame id the server re-announced it under (DESIGN 3.3, 3.5).
//
// The server re-announces an unanswered interaction request with a fresh
// `server-N` id every 1 s, doubling to 10 s. Each re-announce reaches the
// transport as a separate answer goroutine; every one of them joins this
// record and returns the same reply, so conn.answer writes exactly one reply
// per frame id. That matters beyond tidiness: the raw persistent host keys its
// pending table by frame id and replays every id that never got a reply to the
// next daemon, which would park a ghost approval on an idle session.
type parkedRequest struct {
	method     string
	requestID  string
	toolCallID string
	turnID     string
	// offered keeps the provider's own response object per option id so the
	// reply echoes it verbatim: the server zod-parses the result strictly.
	offered map[string]json.RawMessage
	// questions is the user-input request's question list, kept to map AO's
	// form answer back onto the provider's reply shape (U6).
	questions []zcodeproto.Question
	// done closes once reply or replyErr is set. waiters counts the frame ids
	// parked on this record, for diagnostics.
	done     chan struct{}
	reply    any
	replyErr error
	waiters  int
	// deferred is set while a live reconnect has not yet reconciled the
	// host-replayed requests against the server's pending list; the
	// approval.requested event is emitted (or the request refused) by
	// reconcilePending rather than by the handler.
	deferred bool
	emit     ports.ChatEvent
	// prestaged marks a record registered from the read loop, in wire order,
	// before its handler goroutine parsed the frame; the handler fills it in.
	prestaged bool
}

// resolve settles every waiter with one answer. Safe to call once; later calls
// are no-ops so a timeout racing a decision cannot double-close.
func (p *parkedRequest) resolve(reply any, err error) {
	select {
	case <-p.done:
		return
	default:
	}
	p.reply = reply
	p.replyErr = err
	close(p.done)
}

func (p *parkedRequest) offeredIDs() []string {
	ids := make([]string, 0, len(p.offered))
	for id := range p.offered {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// handleServerRequest is the router of DESIGN 3.5, one case per method. The
// provider blocks on the reply for interaction requests, so those park rather
// than answer. Everything AO does not model is refused with -32601, never with
// a fabricated decision.
func (c *conversation) handleServerRequest(ctx context.Context, req serverRequest) (any, error) {
	switch req.Method {
	case zcodeproto.MethodSessionRequestRuntimePreferences:
		// Never refused: this is raised inside session/create and a refusal other
		// than -32601/-32020 fails the create. Both keys are always sent; the
		// second one off keeps the server from self-answering AskUserQuestion.
		c.observeRuntimePreferences(req.Params)
		return zcodeproto.RequestRuntimePreferencesResult{
			NativeSearchEnhancementsEnabled:      true,
			AskUserQuestionAutoResolutionEnabled: false,
		}, nil

	case zcodeproto.MethodInteractionRequestPermission:
		return c.parkPermission(ctx, req)

	case zcodeproto.MethodInteractionRequestUserInput:
		return c.parkUserInput(ctx, req)

	case zcodeproto.MethodInteractionRequestProviderRuntimeHeaders:
		var params zcodeproto.RequestProviderRuntimeHeadersParams
		_ = json.Unmarshal(req.Params, &params)
		resolver := c.runtimeHeaders
		if resolver == nil {
			resolver = declineResolver{}
		}
		result, err := resolver.Resolve(ctx, params)
		if err != nil {
			// Logged without the params: the request names the account and the
			// reply would carry credentials. Neither belongs in a log line.
			c.log.Warn("declined provider runtime headers request", "reason", params.Reason)
			return nil, err
		}
		return result, nil

	case zcodeproto.MethodInteractionRequestOfficialMcpAuthHeaders:
		// AO holds no MCP auth for zcode's official servers; the server's
		// resolveHeaders catch-all turns any error into official_auth_unavailable.
		return nil, &rpcError{Code: zcodeproto.CodeClientDeclined, Message: "declined by agent-orchestrator"}

	case zcodeproto.MethodSubagentsReadRuntimeConfig:
		// Only sent when the client enabled subagent runtime config, which AO
		// does not; answered with the built-in fallback if it ever arrives.
		return zcodeproto.SubagentRuntimeConfigResult{Kind: "built-in-fallback"}, nil
	}

	c.log.Warn("refusing unmodelled app-server request", "method", req.Method)
	return nil, &rpcError{Code: zcodeproto.CodeMethodNotFound, Message: fmt.Sprintf("unsupported request %s", req.Method)}
}

// noteServerRequest runs on the read loop, in wire order, before a server
// request's handler goroutine exists. While a live reconnect is still
// reconciling, it registers the frame's requestId as a deferred record so that
// deferral is decided by the frame's position on the wire, not by whether the
// handler goroutine reached park before the Resume goroutine finished
// reconciling: the host replays every unanswered frame before the session/read
// response, which this same loop delivers afterwards.
func (c *conversation) noteServerRequest(req serverRequest) {
	if req.Method != zcodeproto.MethodInteractionRequestPermission &&
		req.Method != zcodeproto.MethodInteractionRequestUserInput {
		return
	}
	var params struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.RequestID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.reconciling || c.closed {
		return
	}
	if _, pending := c.pending[params.RequestID]; pending {
		return
	}
	if _, settled := c.recentlyResolved[params.RequestID]; settled {
		return
	}
	c.pending[params.RequestID] = &parkedRequest{
		method: req.Method, requestID: params.RequestID,
		done: make(chan struct{}), deferred: true, prestaged: true,
	}
}

// observeRuntimePreferences records the session id the server named inside
// session/create. That request's params.sessionId is the PROVEN id source.
func (c *conversation) observeRuntimePreferences(params json.RawMessage) {
	var p zcodeproto.RequestRuntimePreferencesParams
	if err := json.Unmarshal(params, &p); err != nil || p.SessionID == "" {
		return
	}
	c.mu.Lock()
	if c.preferencesSessionID == "" {
		c.preferencesSessionID = p.SessionID
	}
	c.mu.Unlock()
}

// parkPermission parks interaction/requestPermission until ResolveRequest.
func (c *conversation) parkPermission(ctx context.Context, req serverRequest) (any, error) {
	var params zcodeproto.RequestPermissionParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.RequestID == "" {
		return nil, &rpcError{Code: zcodeproto.CodeInvalidParams, Message: "permission request carried no requestId"}
	}
	decisions := decisionOptions(params.Options)
	offered := make(map[string]json.RawMessage, len(decisions))
	for _, option := range decisions {
		offered[option.ID] = option.Raw
	}
	summary := params.ToolName
	if params.Reason != "" {
		if summary != "" {
			summary += ": "
		}
		summary += params.Reason
	}
	if summary == "" {
		summary = "Approval required"
	}
	detail, _ := json.Marshal(map[string]any{
		"toolName": params.ToolName, "riskLevel": params.RiskLevel,
		"input": validOr(params.Input, []byte("null")), "toolCallId": params.ToolCallID,
	})
	ev := ports.ChatEvent{
		Kind:           ports.ChatEventApprovalRequested,
		ProviderTurnID: c.turnForRequest(params.TurnID),
		ProviderItemID: params.RequestID,
		RequestID:      params.RequestID,
		ActivityKind:   approvalKind(params.ToolName),
		ActivityStatus: domain.ActivityStatusPending,
		Summary:        summary,
		Detail:         detail,
		Decisions:      decisions,
	}
	return c.park(ctx, &parkedRequest{
		method: req.Method, requestID: params.RequestID, toolCallID: params.ToolCallID,
		turnID: params.TurnID, offered: offered,
	}, ev)
}

// parkUserInput parks interaction/requestUserInput until ResolveInput.
func (c *conversation) parkUserInput(ctx context.Context, req serverRequest) (any, error) {
	var params zcodeproto.RequestUserInputParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.RequestID == "" {
		return nil, &rpcError{Code: zcodeproto.CodeInvalidParams, Message: "user input request carried no requestId"}
	}
	ev := ports.ChatEvent{
		Kind:           ports.ChatEventInputRequested,
		ProviderTurnID: c.turnForRequest(params.TurnID),
		ProviderItemID: params.RequestID,
		RequestID:      params.RequestID,
		ActivityKind:   domain.ActivityKindUserInput,
		ActivityStatus: domain.ActivityStatusPending,
		Summary:        inputRequestFrom(params).Message,
		Input:          inputRequestFrom(params),
	}
	// toolCallId is indexed for user input too: the server resolves an
	// answered AskUserQuestion through permission.resolved (PROVEN,
	// p6-user-input.ndjson), which `Uhr` keys by toolCallId.
	return c.park(ctx, &parkedRequest{
		method: req.Method, requestID: params.RequestID, toolCallID: params.ToolCallID,
		turnID: params.TurnID, questions: params.Questions,
	}, ev)
}

// park registers (or joins) the parked record for a requestId and waits for its
// answer. Exactly one approval.requested/input.requested is emitted per
// requestId while every frame id is answered. fresh carries what the handler
// parsed; it becomes the record, or fills in the one noteServerRequest
// prestaged for this requestId from the read loop.
func (c *conversation) park(ctx context.Context, fresh *parkedRequest, ev ports.ChatEvent) (any, error) {
	requestID := fresh.requestID
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, &rpcError{Code: zcodeproto.CodeMethodNotFound, Message: errConversationClosed.Error()}
	}
	if settled, ok := c.recentlyResolved[requestID]; ok {
		// A re-announce that crossed the settlement on the wire: answer it the
		// same way instead of parking a second approval for a request already
		// decided, refused or resolved by the provider.
		c.mu.Unlock()
		return settled.reply, settled.err
	}
	parked, joined := c.pending[requestID]
	if joined && !parked.prestaged {
		parked.waiters++
		c.mu.Unlock()
		return c.awaitParked(ctx, parked)
	}
	if joined {
		parked.prestaged = false
		parked.toolCallID = fresh.toolCallID
		parked.turnID = fresh.turnID
		parked.offered = fresh.offered
		parked.questions = fresh.questions
	} else {
		parked = fresh
		parked.done = make(chan struct{})
		parked.deferred = c.reconciling
		c.pending[requestID] = parked
	}
	parked.waiters = 1
	if parked.toolCallID != "" {
		c.byToolCallID[parked.toolCallID] = requestID
	}
	deferred := parked.deferred
	if deferred {
		// reconcilePending emits it, or refuses it, once session/read answers.
		parked.emit = ev
	}
	c.mu.Unlock()

	if !deferred {
		c.emit(ev)
	}

	timer := time.NewTimer(c.approvalWait)
	defer timer.Stop()
	select {
	case <-parked.done:
		return parked.reply, parked.replyErr
	case <-timer.C:
		// Expiry refuses every waiting id rather than deciding; the record is
		// dropped so a later decision gets ErrChatRequestNotPending. Every path
		// that removes a record from pending resolves it, so done closes here
		// even when a provider-side resolution or Close won the race.
		c.refusePending(requestID, &rpcError{Code: zcodeproto.CodeMethodNotFound, Message: "approval timed out without a decision"})
		<-parked.done
		return parked.reply, parked.replyErr
	case <-ctx.Done():
		c.refusePending(requestID, ctx.Err())
		return nil, ctx.Err()
	}
}

// awaitParked is the path a re-announced frame id takes: wait for the shared
// answer and return it so its own reply frame is written.
func (c *conversation) awaitParked(ctx context.Context, parked *parkedRequest) (any, error) {
	select {
	case <-parked.done:
		return parked.reply, parked.replyErr
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// refusePending answers every waiter of a request with an error and forgets
// it, remembering the refusal for any re-announce still on the wire.
func (c *conversation) refusePending(requestID string, err error) {
	c.mu.Lock()
	parked, ok := c.pending[requestID]
	if ok {
		c.forgetPendingLocked(parked)
		c.rememberResolvedLocked(requestID, resolvedReply{err: err, method: parked.method})
	}
	c.mu.Unlock()
	if ok {
		parked.resolve(nil, err)
	}
}

// forgetPendingLocked removes a record from the pending maps. Caller holds
// c.mu and must resolve the record afterwards: every removal resolves.
func (c *conversation) forgetPendingLocked(parked *parkedRequest) {
	delete(c.pending, parked.requestID)
	if parked.toolCallID != "" {
		delete(c.byToolCallID, parked.toolCallID)
	}
}

// ResolveRequest answers a parked permission request with one of the decisions
// the provider offered for it. The reply is the option's response object
// verbatim, written to every frame id the request was announced under.
func (c *conversation) ResolveRequest(ctx context.Context, requestID string, decision ports.ChatDecision) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errConversationClosed
	}
	parked, ok := c.pending[requestID]
	if !ok {
		c.mu.Unlock()
		// Already resolved, superseded, or from a previous controller. A stale
		// card must never resolve a newer request.
		return fmt.Errorf("%w: %q", ports.ErrChatRequestNotPending, requestID)
	}
	if parked.method != zcodeproto.MethodInteractionRequestPermission {
		c.mu.Unlock()
		return fmt.Errorf("%w: %q expects structured input, not a decision", ports.ErrChatDecisionNotOffered, requestID)
	}
	raw, offered := parked.offered[decision.ID]
	if !offered {
		c.mu.Unlock()
		// Refused and left pending: consent AO invented is not consent, and
		// consuming the request would leave the real answer nothing to answer.
		return fmt.Errorf("%w: %q (offered: %s)",
			ports.ErrChatDecisionNotOffered, decision.ID, strings.Join(parked.offeredIDs(), ", "))
	}
	if err := ctx.Err(); err != nil {
		// Checked before delivery, like codex: once settled, the provider has
		// the consent, so reporting a failure would leave the card pending for
		// an answer that was in fact given.
		c.mu.Unlock()
		return err
	}
	c.settleLocked(parked, raw)
	c.mu.Unlock()
	return nil
}

// ResolveInput answers a parked interaction/requestUserInput with the user's
// structured response (ports.ChatInputResponder).
func (c *conversation) ResolveInput(ctx context.Context, requestID string, response ports.ChatInputResponse) error {
	if !response.Action.Valid() {
		return fmt.Errorf("invalid input action %q", response.Action)
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errConversationClosed
	}
	parked, ok := c.pending[requestID]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("%w: %q", ports.ErrChatRequestNotPending, requestID)
	}
	if parked.method != zcodeproto.MethodInteractionRequestUserInput {
		c.mu.Unlock()
		// Refused and left pending: the request is still waiting, for a decision.
		return fmt.Errorf("%w: %q expects a decision, not structured input", ports.ErrChatDecisionNotOffered, requestID)
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return err
	}
	reply := zcodeproto.UserInputResponse{Action: string(response.Action)}
	if response.Action == ports.ChatInputActionAccept && response.Content != nil {
		reply.Content = userInputReplyContent(parked.questions, response.Content)
	}
	c.settleLocked(parked, reply)
	c.mu.Unlock()
	return nil
}

// userInputReplyContent shapes AO's form answer into the provider's accept
// content. inputRequestFrom publishes a schema keyed by each question's header
// (or text), so a generic AO client answers {<header>: value}; the provider
// wants the answers keyed by question text under "answers" (PROVEN,
// p6-user-input.ndjson: {action:"accept", content:{answers:{"<question>":
// "yes"}}} was accepted and the turn continued with the answer). Content that
// already carries an "answers" key is forwarded verbatim.
func userInputReplyContent(questions []zcodeproto.Question, content map[string]any) map[string]any {
	if _, shaped := content["answers"]; shaped {
		return content
	}
	answers := make(map[string]any, len(content))
	consumed := make(map[string]struct{}, len(questions))
	for i, question := range questions {
		key := question.Header
		if key == "" {
			key = question.Question
		}
		if key == "" {
			key = fmt.Sprintf("answer_%d", i)
		}
		value, ok := content[key]
		if !ok {
			continue
		}
		consumed[key] = struct{}{}
		text := question.Question
		if text == "" {
			text = key
		}
		answers[text] = value
	}
	for key, value := range content {
		if _, ok := consumed[key]; !ok {
			answers[key] = value
		}
	}
	return map[string]any{"answers": answers}
}

// settleLocked answers a parked request and remembers the reply for any
// re-announce still in flight. Caller holds c.mu.
func (c *conversation) settleLocked(parked *parkedRequest, reply any) {
	c.forgetPendingLocked(parked)
	c.rememberResolvedLocked(parked.requestID, resolvedReply{reply: reply, method: parked.method})
	parked.resolve(reply, nil)
}

// rememberResolvedLocked keeps what settled a request for the re-announce
// window. Entries are never dropped on permission.resolved: a re-announce the
// server wrote before it processed the reply can still be on its way to park
// when that event is pumped. The bound keeps the memory small instead.
func (c *conversation) rememberResolvedLocked(requestID string, settled resolvedReply) {
	if len(c.recentlyResolved) >= recentlyResolvedLimit {
		// Evict arbitrarily: the map only needs to cover the re-announce window.
		for key := range c.recentlyResolved {
			delete(c.recentlyResolved, key)
			break
		}
	}
	c.recentlyResolved[requestID] = settled
}

// discardPending forgets a request the provider resolved on its own.
func (c *conversation) discardPending(requestID string) {
	refusal := &rpcError{Code: zcodeproto.CodeMethodNotFound, Message: "request was resolved by the provider"}
	c.mu.Lock()
	parked, ok := c.pending[requestID]
	if ok {
		c.forgetPendingLocked(parked)
		c.rememberResolvedLocked(requestID, resolvedReply{err: refusal, method: parked.method})
	}
	c.mu.Unlock()
	if ok {
		// The server already settled it elsewhere; answering with a refusal
		// clears the frame ids without pretending to decide.
		parked.resolve(nil, refusal)
	}
}

// requestMethod reports which server request a requestId belongs to: parked,
// or settled within the re-announce window. "" when unknown.
func (c *conversation) requestMethod(requestID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if parked, ok := c.pending[requestID]; ok {
		return parked.method
	}
	if settled, ok := c.recentlyResolved[requestID]; ok {
		return settled.method
	}
	return ""
}

// requestIDForToolCall translates a toolCallId to the parked requestId, for a
// permission.resolved that omits requestId.
func (c *conversation) requestIDForToolCall(toolCallID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byToolCallID[toolCallID]
}

// reconcilePending finishes a live reconnect (DESIGN 3.5/3.8): host-replayed
// server requests whose requestId the server still lists as pending are
// emitted once; the rest are refused with -32601 so a request the previous
// daemon already resolved never becomes a second approval.
func (c *conversation) reconcilePending(pendingRequestIDs []string, known bool) {
	still := make(map[string]struct{}, len(pendingRequestIDs))
	for _, id := range pendingRequestIDs {
		still[id] = struct{}{}
	}
	refusal := &rpcError{Code: zcodeproto.CodeMethodNotFound, Message: "request is no longer pending on the server"}
	c.mu.Lock()
	c.reconciling = false
	var emit []ports.ChatEvent
	var refuse []*parkedRequest
	for id, parked := range c.pending {
		if !parked.deferred {
			continue
		}
		parked.deferred = false
		if _, live := still[id]; live || !known {
			// Without a server answer, keeping the request is the conservative
			// choice: a stale card is recoverable, a lost real approval is not. A
			// record still prestaged has no event yet; its handler, finding the
			// record no longer deferred, emits on arrival.
			if !parked.prestaged {
				emit = append(emit, parked.emit)
			}
			continue
		}
		c.forgetPendingLocked(parked)
		c.rememberResolvedLocked(id, resolvedReply{err: refusal, method: parked.method})
		refuse = append(refuse, parked)
	}
	c.mu.Unlock()
	for _, parked := range refuse {
		parked.resolve(nil, refusal)
	}
	for _, ev := range emit {
		c.emit(ev)
	}
}

// failPendingApprovals unblocks every parked handler when the controller ends.
// User-input requests are cancelled rather than refused (DESIGN 3.5).
func (c *conversation) failPendingApprovals() {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]*parkedRequest{}
	c.byToolCallID = map[string]string{}
	c.closed = true
	c.mu.Unlock()
	for _, parked := range pending {
		if parked.method == zcodeproto.MethodInteractionRequestUserInput {
			parked.resolve(zcodeproto.UserInputResponse{Action: string(ports.ChatInputActionCancel)}, nil)
			continue
		}
		parked.resolve(nil, &rpcError{Code: zcodeproto.CodeMethodNotFound, Message: errConversationClosed.Error()})
	}
}

// turnForRequest maps a request's native turn id onto AO's, falling back to the
// turn in flight.
func (c *conversation) turnForRequest(native string) string {
	return turnFor(c.turns, native)
}

// approvalKind classifies a permission request by the tool it guards.
func approvalKind(toolName string) domain.ActivityKind {
	if toolName == "" {
		return domain.ActivityKindApproval
	}
	switch kindForTool(toolName) {
	case domain.ActivityKindFileChange:
		return domain.ActivityKindFileChange
	case domain.ActivityKindMCPTool:
		return domain.ActivityKindApproval
	default:
		return domain.ActivityKindCommand
	}
}

func validOr(raw, fallback json.RawMessage) json.RawMessage {
	if len(raw) > 0 && json.Valid(raw) {
		return raw
	}
	return fallback
}
