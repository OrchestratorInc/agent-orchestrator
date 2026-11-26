package persistenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// ACPEventIDMetaKey identifies one replayable provider event. AO assigns the
	// value to every durable event derived from that frame.
	ACPEventIDMetaKey = "ao.persistentEventId"

	// ACPPromptStalledDataKey marks the error data of a prompt the stall
	// watchdog failed. The daemon reads it to tell a silent adapter from a
	// provider that answered with an error.
	ACPPromptStalledDataKey = "ao.promptStalled"

	// promptStallTimeout is how long an in-flight session/prompt may hear
	// nothing from the adapter, while the adapter is not waiting on the client,
	// before the relay settles it synthetically. A live turn streams tool
	// calls, message chunks, or state changes continuously, so a silent gap
	// this long means the adapter's turn state machine lost the completion
	// (upstream claude-agent-acp#1145 class) and no response will ever arrive.
	promptStallTimeout = 15 * time.Minute

	// promptStallCheckInterval is how often the host re-checks for a stalled
	// prompt.
	promptStallCheckInterval = 30 * time.Second

	// promptStallCancelGrace is how long the relay waits, after cancelling a
	// stalled prompt, for the adapter to answer it before giving up on the
	// adapter and settling the prompt itself.
	promptStallCancelGrace = 30 * time.Second
)

type acpClientRequest struct {
	clientID   json.RawMessage
	method     string
	params     json.RawMessage
	generation uint64
}

type acpServerRequest struct {
	requestID   string
	frame       []byte
	promptOwned bool
}

type acpInteractionCommand struct {
	eventID     string
	canonical   string
	params      json.RawMessage
	promptOwned bool
}

type acpReplayKind uint8

const (
	acpReplayFrame acpReplayKind = iota
	acpReplayRequest
	acpReplayCommand
)

type acpReplayEntry struct {
	kind acpReplayKind
	key  string
	span acpJournalSpan
}

type acpClientFrames struct {
	provider []byte
	client   []byte
}

// acpRelay is deliberately only a JSON-RPC correlation layer. The pinned ACP
// SDK and all provider semantics remain in the adapter; the host owns only the
// connection state that cannot be reconstructed after a daemon dies.
type acpRelay struct {
	identity          string
	nextRequestID     int64
	nextEventID       uint64
	nextInteractionID uint64
	pending           map[string]acpClientRequest
	serverRequests    map[string]acpServerRequest
	serverOrder       []string
	commands          map[string]acpInteractionCommand
	commandOrder      []string
	promptReplay      []acpReplayEntry
	state             ACPState
	promptJournal     *acpPromptJournal
	promptResult      []byte
	// availableCommands holds the latest command catalog the adapter announced
	// and availableCommandsSession the session it belongs to; see
	// ACPState.AvailableCommands.
	availableCommands        json.RawMessage
	availableCommandsSession string
	cancelRequested          bool
	// lastActivityAt stamps the last adapter output, prompt registration, or
	// client answer to an adapter request, so the stall watchdog can tell a
	// live turn from a lost one.
	lastActivityAt time.Time
	// stallCancelledAt is when the watchdog cancelled the active prompt for
	// silence. The prompt stays active until the adapter answers it, so output
	// the cancelled turn still emits is not attributed to the next prompt.
	stallCancelledAt time.Time
	// abandonedPrompts holds provider ids of prompts the watchdog failed. The
	// adapter's own late answer to one is dropped, not forwarded.
	abandonedPrompts map[string]struct{}
	// heldResult is the answer that settles the turn, kept back while a steer
	// command (keepsTurn) is still running. See providerFrame.
	heldResult *acpHeldResult
}

// acpHeldResult is a prompt answer the relay settles the turn with later.
type acpHeldResult struct {
	id       json.RawMessage
	request  acpClientRequest
	envelope map[string]json.RawMessage
	frame    []byte
}

func newACPRelay(ctx context.Context, journalPath string) (*acpRelay, error) {
	identity, err := randomToken()
	if err != nil {
		return nil, err
	}
	journal, err := openACPPromptJournal(ctx, journalPath)
	if err != nil {
		return nil, err
	}
	return &acpRelay{
		identity: identity,
		pending:  make(map[string]acpClientRequest), serverRequests: make(map[string]acpServerRequest),
		commands:         make(map[string]acpInteractionCommand),
		abandonedPrompts: make(map[string]struct{}),
		promptJournal:    journal,
	}, nil
}

func (r *acpRelay) snapshot() *ACPState {
	state := r.state
	state.InitializeResult = append(json.RawMessage(nil), state.InitializeResult...)
	state.SessionResult = append(json.RawMessage(nil), state.SessionResult...)
	if r.availableCommandsSession != "" && r.availableCommandsSession == state.SessionID {
		state.AvailableCommands = append(json.RawMessage(nil), r.availableCommands...)
	}
	return &state
}

// captureAvailableCommands keeps the latest available_commands_update. ACP
// updates replace the whole catalog, so only the last one matters.
func (r *acpRelay) captureAvailableCommands(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var notification struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate     string          `json:"sessionUpdate"`
			AvailableCommands json.RawMessage `json:"availableCommands"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &notification) != nil ||
		notification.Update.SessionUpdate != "available_commands_update" ||
		notification.SessionID == "" {
		return
	}
	r.availableCommands = append(json.RawMessage(nil), notification.Update.AvailableCommands...)
	r.availableCommandsSession = notification.SessionID
}

func (r *acpRelay) replayTo(ctx context.Context, dst io.Writer) error {
	for _, entry := range r.promptReplay {
		switch entry.kind {
		case acpReplayFrame:
			if err := r.promptJournal.replayTo(ctx, dst, entry.span); err != nil {
				return err
			}
		case acpReplayRequest:
			if request, ok := r.serverRequest(entry.key); ok {
				if _, err := dst.Write(request.frame); err != nil {
					return err
				}
			}
		case acpReplayCommand:
			if command, ok := r.commands[entry.key]; ok {
				if err := r.replayInteractionCommand(ctx, dst, entry.key, command); err != nil {
					return err
				}
			}
		}
	}
	for _, providerID := range r.serverOrder {
		request, ok := r.serverRequests[providerID]
		if !ok || request.promptOwned {
			continue
		}
		if _, err := dst.Write(request.frame); err != nil {
			return err
		}
	}
	for _, requestID := range r.commandOrder {
		command, ok := r.commands[requestID]
		if !ok || command.promptOwned {
			continue
		}
		if err := r.replayInteractionCommand(ctx, dst, requestID, command); err != nil {
			return err
		}
	}
	if len(r.promptResult) > 0 {
		_, err := dst.Write(r.promptResult)
		return err
	}
	return nil
}

func (r *acpRelay) replayInteractionCommand(
	ctx context.Context,
	dst io.Writer,
	requestID string,
	command acpInteractionCommand,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(command.params, &params); err != nil {
		return err
	}
	params["eventId"], _ = json.Marshal(command.eventID)
	_, pending := r.serverRequest(requestID)
	params["providerPending"], _ = json.Marshal(pending)
	encoded, _ := json.Marshal(params)
	_, err := dst.Write(marshalFrame(map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`),
		"method":  json.RawMessage(`"` + ACPInteractionCommandMethod + `"`),
		"params":  encoded,
	}, nil))
	return err
}

func (r *acpRelay) close(ctx context.Context) error { return r.promptJournal.close(ctx) }

func (r *acpRelay) clientFrame(ctx context.Context, frame []byte, generation uint64) (acpClientFrames, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(frame, &envelope) != nil {
		return acpClientFrames{provider: frame}, nil //nolint:nilerr // preserve malformed input so the ACP peer owns its protocol error
	}
	var method string
	_ = json.Unmarshal(envelope["method"], &method)
	if method == ACPPromptAckMethod {
		var params struct {
			EventID string `json:"eventId"`
		}
		_ = json.Unmarshal(envelope["params"], &params)
		if params.EventID != "" && params.EventID == r.state.PendingResultEventID {
			if err := r.promptJournal.reset(ctx); err != nil {
				return acpClientFrames{}, err
			}
			r.promptResult = nil
			r.state.PendingResultEventID = ""
			r.commands = make(map[string]acpInteractionCommand)
			r.commandOrder = nil
			r.promptReplay = nil
		}
		return acpClientFrames{}, nil
	}
	if method == ACPInteractionCommandMethod {
		return r.acceptInteractionCommand(ctx, envelope)
	}
	if method == "$/cancel_request" {
		return acpClientFrames{provider: r.clientCancellation(envelope, frame, generation)}, nil
	}
	if method == "session/cancel" && r.state.ActivePrompt {
		if r.cancelRequested {
			return acpClientFrames{}, nil
		}
		r.cancelRequested = true
	}
	id := envelope["id"]
	if method == "" && len(id) > 0 {
		r.removeServerRequest(string(id))
		// The adapter resumes the turn once the client answers it.
		r.lastActivityAt = time.Now()
	}
	if method == "" || len(id) == 0 || string(id) == "null" {
		return acpClientFrames{provider: frame}, nil
	}

	steer := false
	if method == "session/prompt" {
		steer = isSteerPrompt(envelope["params"])
		// A turn being cancelled, by Stop or by the stall watchdog, takes no more
		// steers: the agent would run one as fresh work once the cancel lands.
		live := r.state.ActivePrompt && r.state.PendingResultEventID == "" &&
			!r.cancelRequested && r.stallCancelledAt.IsZero()
		switch {
		case steer && !live:
			// The turn this steer was meant for has ended or is ending. Forwarded,
			// the agent would start a turn of its own that AO has no row for.
			return acpClientFrames{client: steerTurnEndedFrame(id)}, nil
		case !steer && (r.state.ActivePrompt || r.state.PendingResultEventID != ""):
			return acpClientFrames{client: rpcErrorFrame(id, -32602, "previous ACP prompt is active or not acknowledged", nil)}, nil
		}
	}
	r.nextRequestID++
	providerID, _ := json.Marshal(r.nextRequestID)
	r.pending[string(providerID)] = acpClientRequest{
		clientID: append(json.RawMessage(nil), id...), method: method,
		params: append(json.RawMessage(nil), envelope["params"]...), generation: generation,
	}
	envelope["id"] = providerID
	if method == "session/prompt" {
		r.lastActivityAt = time.Now()
	}
	if method == "session/prompt" && !steer {
		r.state.ActivePrompt = true
		r.state.ActiveCompaction = isCompactionPrompt(envelope["params"])
		r.state.PendingResultEventID = ""
		r.cancelRequested = false
		r.heldResult = nil
		r.lastActivityAt = time.Now()
		if err := r.promptJournal.reset(ctx); err != nil {
			return acpClientFrames{}, err
		}
		r.promptResult = nil
		r.commands = make(map[string]acpInteractionCommand)
		r.commandOrder = nil
		r.promptReplay = nil
	}
	return acpClientFrames{provider: marshalFrame(envelope, frame)}, nil
}

func (r *acpRelay) acceptInteractionCommand(
	ctx context.Context,
	envelope map[string]json.RawMessage,
) (acpClientFrames, error) {
	id := envelope["id"]
	var identity struct {
		RequestID string `json:"requestId"`
	}
	params := envelope["params"]
	if len(id) == 0 || string(id) == "null" || json.Unmarshal(params, &identity) != nil || identity.RequestID == "" {
		return acpClientFrames{client: rpcErrorFrame(id, -32602, "invalid persistent interaction command", nil)}, nil //nolint:nilerr // protocol validation errors are returned in-band
	}
	canonical, err := canonicalJSON(params)
	if err != nil {
		return acpClientFrames{client: rpcErrorFrame(id, -32602, "invalid persistent interaction command", nil)}, nil //nolint:nilerr // protocol validation errors are returned in-band
	}
	if existing, ok := r.commands[identity.RequestID]; ok {
		if existing.canonical != canonical {
			return acpClientFrames{client: rpcErrorFrame(id, -32602, "persistent interaction command changed", nil)}, nil
		}
		return acpClientFrames{client: rpcResultFrame(id, map[string]string{"eventId": existing.eventID})}, nil
	}
	request, pending := r.serverRequest(identity.RequestID)
	if !pending {
		return acpClientFrames{client: rpcErrorFrame(id, -32602, "persistent interaction is not pending", nil)}, nil
	}
	if err := ctx.Err(); err != nil {
		return acpClientFrames{}, err
	}
	command := acpInteractionCommand{
		eventID: r.newEventID(), canonical: canonical,
		params:      append(json.RawMessage(nil), params...),
		promptOwned: request.promptOwned,
	}
	r.commands[identity.RequestID] = command
	r.commandOrder = append(r.commandOrder, identity.RequestID)
	if command.promptOwned {
		r.promptReplay = append(r.promptReplay, acpReplayEntry{kind: acpReplayCommand, key: identity.RequestID})
	}
	return acpClientFrames{client: rpcResultFrame(id, map[string]string{"eventId": command.eventID})}, nil
}

func (r *acpRelay) clientCancellation(
	envelope map[string]json.RawMessage,
	fallback []byte,
	generation uint64,
) []byte {
	var params map[string]json.RawMessage
	if json.Unmarshal(envelope["params"], &params) != nil {
		return fallback
	}
	target := params["requestId"]
	for providerID, request := range r.pending {
		if request.generation != generation || string(request.clientID) != string(target) {
			continue
		}
		params["requestId"] = json.RawMessage(providerID)
		encoded, _ := json.Marshal(params)
		envelope["params"] = encoded
		return marshalFrame(envelope, fallback)
	}
	return fallback
}

// providerFrame returns the daemon-facing frame and whether the ACP profile owns
// its replay. A retained frame must not also enter the generic detached buffer or
// it would be replayed twice.
func (r *acpRelay) providerFrame(
	ctx context.Context,
	frame []byte,
	generation uint64,
	attached bool,
) ([]byte, bool, error) {
	r.lastActivityAt = time.Now()
	var envelope map[string]json.RawMessage
	if json.Unmarshal(frame, &envelope) != nil {
		return frame, false, nil //nolint:nilerr // preserve malformed output for the SDK's protocol error
	}
	var method string
	_ = json.Unmarshal(envelope["method"], &method)
	id := envelope["id"]

	if method != "" {
		if len(id) > 0 && string(id) != "null" {
			rewritten, key, created := r.providerRequest(envelope, frame)
			if created && r.state.ActivePrompt {
				r.promptReplay = append(r.promptReplay, acpReplayEntry{kind: acpReplayRequest, key: key})
			}
			return rewritten, true, nil
		}
		r.captureAvailableCommands(method, envelope["params"])
		if r.state.ActivePrompt {
			eventID := r.newEventID()
			injectMeta(envelope, ACPEventIDMetaKey, eventID)
			rewritten := marshalFrame(envelope, frame)
			span, err := r.promptJournal.append(ctx, rewritten)
			if err != nil {
				return nil, false, err
			}
			r.promptReplay = append(r.promptReplay, acpReplayEntry{kind: acpReplayFrame, span: span})
			if !attached {
				return nil, true, nil
			}
			return rewritten, true, nil
		}
		return frame, false, nil
	}
	if len(id) == 0 {
		return frame, false, nil
	}
	if _, ok := r.abandonedPrompts[string(id)]; ok {
		delete(r.abandonedPrompts, string(id))
		return nil, false, nil
	}

	request, ok := r.pending[string(id)]
	if !ok {
		return frame, false, nil
	}
	delete(r.pending, string(id))
	r.captureResponse(request, envelope)

	if request.method == "session/prompt" && keepsTurn(request.params) {
		// A steer command's answer is never the turn's result; that comes from
		// the turn's own prompts.
		var answer []byte
		if attached && request.generation == generation {
			answer = rewriteResponseID(envelope, request.clientID, frame)
		}
		held := r.heldResult
		if held == nil || r.pendingSteerCommand() {
			return answer, false, nil
		}
		// The turn's own prompt answered while this command still ran, so its
		// answer waited for this one and settles the turn now.
		r.heldResult = nil
		result, retained, err := r.settleTurn(held.id, held.request, held.envelope, held.frame, generation, attached)
		return append(answer, result...), retained, err
	}
	if request.method == "session/prompt" && r.newestPendingPrompt() != "" {
		// Another prompt of the turn still runs, so this answer is a handoff or a
		// refused steer prompt: the turn goes on, and its result is the answer of
		// the last prompt to return.
		if !attached {
			return nil, false, nil
		}
		if request.generation != generation {
			// A prompt the previous attachment sent. The reattached controller
			// adopted its turn and learns of the handoff this way.
			return promptAnswerNotification(ACPPromptHandoffMethod, envelope, ""), false, nil
		}
		return rewriteResponseID(envelope, request.clientID, frame), false, nil
	}
	if request.method == "session/prompt" && r.pendingSteerCommand() {
		// A steer command is still running. Hermes runs the guidance inside its
		// `/steer` request when the turn ended just before the command arrived
		// ("No active turn"), so the turn stays open until the command answers:
		// its output belongs to this turn, and the agent is not idle yet.
		r.heldResult = &acpHeldResult{id: id, request: request, envelope: envelope, frame: frame}
		return nil, false, nil
	}
	if request.method == "session/prompt" {
		return r.settleTurn(id, request, envelope, frame, generation, attached)
	}
	if request.generation != generation || !attached {
		return nil, false, nil
	}
	return rewriteResponseID(envelope, request.clientID, frame), false, nil
}

// settleTurn ends the running turn with the answer to one of its own prompts:
// that answer becomes the turn's result and carries the host's receipt. The
// caller has removed the prompt from pending.
func (r *acpRelay) settleTurn(
	id json.RawMessage,
	request acpClientRequest,
	envelope map[string]json.RawMessage,
	frame []byte,
	generation uint64,
	attached bool,
) ([]byte, bool, error) {
	if !r.stallCancelledAt.IsZero() {
		// The adapter has answered the last prompt of the turn the watchdog
		// cancelled. Whatever it says, the turn ended because the adapter went
		// silent. An earlier prompt's answer passed through providerFrame
		// unchanged: the marker says the adapter is quiet, which holds only now.
		r.stallCancelledAt = time.Time{}
		failure := map[string]json.RawMessage{}
		_ = json.Unmarshal(stalledPromptFailure(id), &failure)
		envelope = failure
	}
	r.state.ActivePrompt = false
	r.state.ActiveCompaction = false
	r.cancelRequested = false
	eventID := r.newEventID()
	injectResultMeta(envelope, ACPEventIDMetaKey, eventID)
	rewritten := rewriteResponseID(envelope, request.clientID, frame)
	r.promptResult = promptResultNotification(envelope, eventID)
	r.state.PendingResultEventID = eventID
	if !attached {
		return nil, true, nil
	}
	if request.generation != generation {
		return append([]byte(nil), r.promptResult...), true, nil
	}
	return rewritten, true, nil
}

// pendingSteerCommand reports whether a steer command (keepsTurn) is still
// waiting for its answer.
func (r *acpRelay) pendingSteerCommand() bool {
	for _, request := range r.pending {
		if request.method == "session/prompt" && keepsTurn(request.params) {
			return true
		}
	}
	return false
}

// failStalledPrompt fails an in-flight session/prompt that has heard nothing
// from the adapter for promptStallTimeout. It gives the turn a terminal state
// instead of leaving the session's Working indicator hanging until the next
// controller restart. A turn waiting on the client (a permission prompt or an
// elicitation) is not silent; the daemon owns that wait.
//
// It works in two steps. The first sends the adapter a session/cancel and keeps
// the prompt active: the adapter still owns the turn, and anything it emits
// until it answers belongs to that turn, not to the prompt the controller sends
// next. When the adapter answers the turn's last prompt, providerFrame turns
// that answer into the stall failure, which marks the adapter quiet and lets
// the controller release its queue. If it has not answered every prompt after
// promptStallCancelGrace it is wedged, and the second step settles each prompt
// still waiting without it and without the marker, so the queue stays held: the
// adapter may still emit from the old turn. Its late answers are dropped.
//
// The prompt may belong to an earlier controller generation: providerFrame then
// turns the failure into the private prompt result, which the reattached
// controller finishes against the turn it adopted. The returned client frame is
// nil while detached; replay delivers it on attach. Callers must hold the host
// mutex.
func (r *acpRelay) failStalledPrompt(
	ctx context.Context,
	generation uint64,
	attached bool,
	now time.Time,
) (acpClientFrames, error) {
	if !r.state.ActivePrompt {
		return acpClientFrames{}, nil
	}
	cancelling := !r.stallCancelledAt.IsZero()
	if !cancelling && (len(r.serverRequests) > 0 || now.Sub(r.lastActivityAt) < promptStallTimeout) {
		return acpClientFrames{}, nil
	}
	if cancelling && now.Sub(r.stallCancelledAt) < promptStallCancelGrace {
		return acpClientFrames{}, nil
	}
	stalled, sessionParams := r.pendingPrompts()
	if len(stalled) == 0 {
		return acpClientFrames{}, nil
	}
	if !cancelling {
		r.stallCancelledAt = now
		cancel := marshalFrame(map[string]json.RawMessage{
			"jsonrpc": json.RawMessage(`"2.0"`), "method": json.RawMessage(`"session/cancel"`), "params": sessionParams,
		}, nil)
		return acpClientFrames{provider: cancel}, nil
	}
	// The adapter ignored the cancel. Settle without the stall marker: the
	// adapter may still emit from the old turn, so the controller must hold its
	// queue like any other failure rather than start a prompt on it. A steered
	// turn has several prompts in flight; every one still waiting fails, oldest
	// first, so the newest failure is the turn's result and no prompt the
	// controller is waiting on is left unanswered.
	r.stallCancelledAt = time.Time{}
	if held := r.heldResult; held != nil {
		// The turn's own prompt answered, but a steer command ignored the
		// cancel. A held success would release the queue into a wedged adapter,
		// so the turn fails like the prompts that are still waiting.
		failure := map[string]json.RawMessage{}
		_ = json.Unmarshal(rpcErrorFrame(held.id, -32603, fmt.Sprintf(
			"provider stopped responding: no ACP output for %s and no answer to the cancel; failing the stalled prompt",
			promptStallTimeout), nil), &failure)
		held.envelope = failure
	}
	var client []byte
	for _, providerID := range stalled {
		failure := rpcErrorFrame(json.RawMessage(providerID), -32603, fmt.Sprintf(
			"provider stopped responding: no ACP output for %s and no answer to the cancel; failing the stalled prompt",
			promptStallTimeout), nil)
		completion, _, err := r.providerFrame(ctx, failure, generation, attached)
		if err != nil {
			return acpClientFrames{}, err
		}
		r.abandonedPrompts[providerID] = struct{}{}
		client = append(client, completion...)
	}
	return acpClientFrames{client: client}, nil
}

// pendingPrompts returns the provider ids of the session/prompt requests still
// waiting for an answer, oldest first, and the session params a session/cancel
// for them takes.
func (r *acpRelay) pendingPrompts() ([]string, json.RawMessage) {
	var ids []int64
	var sessionParams json.RawMessage
	for providerID, request := range r.pending {
		if request.method != "session/prompt" {
			continue
		}
		if n, err := strconv.ParseInt(providerID, 10, 64); err == nil {
			ids = append(ids, n)
		}
		if sessionParams == nil {
			var prompt struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(request.params, &prompt)
			sessionParams, _ = json.Marshal(prompt)
		}
	}
	slices.Sort(ids)
	stalled := make([]string, 0, len(ids))
	for _, n := range ids {
		stalled = append(stalled, strconv.FormatInt(n, 10))
	}
	return stalled, sessionParams
}

// stalledPromptFailure is the error a prompt ends with when the watchdog
// cancelled it for silence. The data marker lets the daemon tell it from a
// provider that answered with an error.
func stalledPromptFailure(id json.RawMessage) []byte {
	return rpcErrorFrame(id, -32603, fmt.Sprintf(
		"provider stopped responding: no ACP output for %s; failing the stalled prompt", promptStallTimeout),
		map[string]any{ACPPromptStalledDataKey: true})
}

func (r *acpRelay) providerRequest(
	envelope map[string]json.RawMessage,
	fallback []byte,
) (frame []byte, key string, created bool) {
	id := envelope["id"]
	key = string(id)
	request := r.serverRequests[key]
	if request.requestID == "" {
		r.nextInteractionID++
		request.requestID = fmt.Sprintf("acp-request:%s:%d", r.identity, r.nextInteractionID)
		request.promptOwned = r.state.ActivePrompt
		r.serverOrder = append(r.serverOrder, key)
		created = true
	}
	injectMeta(envelope, ACPRequestIDMetaKey, request.requestID)
	request.frame = marshalFrame(envelope, fallback)
	r.serverRequests[key] = request
	return request.frame, request.requestID, created
}

func (r *acpRelay) serverRequest(requestID string) (acpServerRequest, bool) {
	for _, request := range r.serverRequests {
		if request.requestID == requestID {
			return request, true
		}
	}
	return acpServerRequest{}, false
}

func (r *acpRelay) removeServerRequest(providerID string) {
	if _, ok := r.serverRequests[providerID]; !ok {
		return
	}
	delete(r.serverRequests, providerID)
	for i, id := range r.serverOrder {
		if id == providerID {
			r.serverOrder = append(r.serverOrder[:i], r.serverOrder[i+1:]...)
			return
		}
	}
}

func canonicalJSON(raw json.RawMessage) (string, error) {
	if !json.Valid(raw) {
		return "", errors.New("invalid JSON")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func rpcResultFrame(id json.RawMessage, result any) []byte {
	encoded, _ := json.Marshal(result)
	return marshalFrame(map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`), "id": id, "result": encoded,
	}, nil)
}

func rpcErrorFrame(id json.RawMessage, code int, message string, data map[string]any) []byte {
	body := map[string]any{"code": code, "message": message}
	if data != nil {
		body["data"] = data
	}
	encoded, _ := json.Marshal(body)
	return marshalFrame(map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`), "id": id, "error": encoded,
	}, nil)
}

func (r *acpRelay) captureResponse(request acpClientRequest, envelope map[string]json.RawMessage) {
	if len(envelope["error"]) > 0 && string(envelope["error"]) != "null" {
		return
	}
	result := envelope["result"]
	switch request.method {
	case "initialize":
		var params struct {
			Meta map[string]string `json:"_meta"`
		}
		_ = json.Unmarshal(request.params, &params)
		r.state.InitialPermissions = params.Meta[ACPInitialPermissionsMetaKey]
		r.state.InitializeResult = append(json.RawMessage(nil), result...)
	case "session/new", "session/load", "session/resume", "session/fork":
		var response struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(result, &response)
		if request.method == "session/fork" {
			var params struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(request.params, &params)
			if response.SessionID == "" || response.SessionID == params.SessionID {
				return
			}
		}
		r.state.SessionResult = append(json.RawMessage(nil), result...)
		if response.SessionID != "" {
			r.state.SessionID = response.SessionID
		} else if request.method != "session/fork" {
			var params struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(request.params, &params)
			if params.SessionID != "" {
				r.state.SessionID = params.SessionID
			}
		}
	}
}

// newestPendingPrompt returns the provider id of the newest session/prompt
// still waiting for an answer whose answer can be the turn's result, or "".
func (r *acpRelay) newestPendingPrompt() string {
	newest, newestID := "", int64(-1)
	for providerID, request := range r.pending {
		if request.method != "session/prompt" || keepsTurn(request.params) {
			continue
		}
		if n, err := strconv.ParseInt(providerID, 10, 64); err == nil && n > newestID {
			newest, newestID = providerID, n
		}
	}
	return newest
}

// keepsTurn reports whether session/prompt params mark a steer command whose
// answer is never the turn's result (ACPSteerKeepsTurnMetaKey).
func keepsTurn(params json.RawMessage) bool {
	var prompt struct {
		Meta map[string]any `json:"_meta"`
	}
	if json.Unmarshal(params, &prompt) != nil {
		return false
	}
	keeps, _ := prompt.Meta[ACPSteerKeepsTurnMetaKey].(bool)
	return keeps
}

// isSteerPrompt reports whether session/prompt params carry ACPSteerPromptMetaKey.
func isSteerPrompt(params json.RawMessage) bool {
	var prompt struct {
		Meta map[string]any `json:"_meta"`
	}
	if json.Unmarshal(params, &prompt) != nil {
		return false
	}
	steer, _ := prompt.Meta[ACPSteerPromptMetaKey].(bool)
	return steer
}

func (r *acpRelay) newEventID() string {
	r.nextEventID++
	return fmt.Sprintf("acp-host:%s:%d", r.identity, r.nextEventID)
}

func rewriteResponseID(envelope map[string]json.RawMessage, id json.RawMessage, fallback []byte) []byte {
	envelope["id"] = append(json.RawMessage(nil), id...)
	return marshalFrame(envelope, fallback)
}

func promptResultNotification(response map[string]json.RawMessage, eventID string) []byte {
	return promptAnswerNotification(ACPPromptResultMethod, response, eventID)
}

// promptAnswerNotification carries a session/prompt answer to a controller
// that has no request of its own to match it to. eventID is "" for an answer
// that is not the turn's result.
func promptAnswerNotification(method string, response map[string]json.RawMessage, eventID string) []byte {
	params := map[string]json.RawMessage{}
	if result := response["result"]; len(result) > 0 {
		params["result"] = result
	}
	if rpcErr := response["error"]; len(rpcErr) > 0 {
		params["error"] = rpcErr
	}
	if eventID != "" {
		params["eventId"], _ = json.Marshal(eventID)
	}
	encodedParams, _ := json.Marshal(params)
	encodedMethod, _ := json.Marshal(method)
	return marshalFrame(map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`), "method": encodedMethod, "params": encodedParams,
	}, nil)
}

// steerTurnEndedFrame refuses a steer prompt that arrived after its turn ended.
func steerTurnEndedFrame(id json.RawMessage) []byte {
	encoded, _ := json.Marshal(map[string]any{
		"code": -32602, "message": "the turn ended before the steer prompt arrived",
		"data": map[string]any{ACPSteerTurnEndedDataKey: true},
	})
	return marshalFrame(map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`), "id": id, "error": encoded,
	}, nil)
}

func injectResultMeta(envelope map[string]json.RawMessage, key, value string) {
	var rpcErr map[string]json.RawMessage
	if json.Unmarshal(envelope["error"], &rpcErr) == nil && rpcErr != nil {
		// Error data can be any JSON value. Wrap it rather than overwrite it.
		data := map[string]json.RawMessage{"providerData": rpcErr["data"]}
		data[key], _ = json.Marshal(value)
		if len(data["providerData"]) == 0 {
			delete(data, "providerData")
		}
		rpcErr["data"], _ = json.Marshal(data)
		envelope["error"], _ = json.Marshal(rpcErr)
		return
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(envelope["result"], &result) != nil {
		return
	}
	if result == nil {
		result = make(map[string]json.RawMessage)
	}
	injectRawMeta(result, key, value)
	encoded, _ := json.Marshal(result)
	envelope["result"] = encoded
}

func injectMeta(envelope map[string]json.RawMessage, key, value string) {
	var params map[string]json.RawMessage
	if json.Unmarshal(envelope["params"], &params) != nil || params == nil {
		params = make(map[string]json.RawMessage)
	}
	injectRawMeta(params, key, value)
	encoded, _ := json.Marshal(params)
	envelope["params"] = encoded
}

func injectRawMeta(object map[string]json.RawMessage, key, value string) {
	var meta map[string]json.RawMessage
	if json.Unmarshal(object["_meta"], &meta) != nil || meta == nil {
		meta = make(map[string]json.RawMessage)
	}
	encoded, _ := json.Marshal(value)
	meta[key] = encoded
	encodedMeta, _ := json.Marshal(meta)
	object["_meta"] = encodedMeta
}

func marshalFrame(envelope map[string]json.RawMessage, fallback []byte) []byte {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return fallback
	}
	return append(encoded, '\n')
}

func isCompactionPrompt(params json.RawMessage) bool {
	if len(params) == 0 {
		return false
	}
	var req struct {
		Prompt []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return false
	}
	for _, block := range req.Prompt {
		if block.Type == "text" && strings.TrimSpace(block.Text) == "/compact" {
			return true
		}
	}
	return false
}
