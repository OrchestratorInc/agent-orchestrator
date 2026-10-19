package zcodeappserver

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// providerTurnPrefix namespaces the minted id of a turn the provider started on
// its own (goal continuation, `nhc`), so the controller can adopt it without
// ever confusing it with the turn AO prepared.
const providerTurnPrefix = "zcode-turn:"

// turnTracker is the one piece of per-conversation state the normalizer needs
// (DESIGN 4.1). zcode names a turn in three places that arrive in a fixed order
// for one session/send:
//
//  1. state.updated{reason:"prompt_started"} — emitted BEFORE the {accepted}
//     result, no turn id;
//  2. computer-use/operation-event{kind:"turn-started", turnId};
//  3. session/event turn.started{turnId, payload.inputId} — only when subscribed.
//
// AO's controller needs one ProviderTurnID that SendTurn returned before any of
// them could be observed, so the tracker is armed with the minted id by
// prepare() before the session/send frame is written, emits turn.started once on
// the first of the three signals, and fills in the native id as it learns it.
// Completion is likewise emitted once, from session/event turn.completed or
// turn.failed, with the state.updated prompt_* frame (always last on the wire)
// as the fallback; the operation-event completion precedes the session event
// and settles nothing.
//
// It also remembers what the proven wire splits across frames: a tool call's
// input arrives on model.streaming{kind:"tool_call"} while tool.updated
// scheduled says inputOmitted:true, and tool.updated result omits the tool
// name (p5-approval-allow.ndjson); reasoning has no start frame, so the first
// reasoning_delta of an assistant message must open its activity row.
type turnTracker struct {
	mu sync.Mutex

	// pending is the minted id armed by prepare and not yet observed started.
	pending string
	// current is the open turn's AO id; native is its provider id when known.
	current string
	native  string
	open    bool
	// startedOut is set once the pump has put the open turn's turn.started on
	// the event stream, and failAfterStart holds the failed completion a cancel
	// left for the pump to emit right after it (the two must not be reordered).
	startedOut     bool
	failAfterStart *ports.ChatEvent

	// textSource latches which delta kind carried assistant text this turn so
	// model.streaming and part.delta never double the text (DESIGN 4.2).
	textSource string
	// replyItemID is the item id the turn's assistant text is currently
	// streamed under (the latest assistantMessageId), so message.completed
	// settles the row the last deltas built.
	replyItemID string
	// toolTails remembers the last progress tail per tool call for the suffix
	// diff of DESIGN 4.2.
	toolTails map[string]string
	// tools remembers each tool call's name and input for the frames that omit
	// them (result carries neither; scheduled omits the input).
	tools map[string]toolCall
	// reasoningOpen records the reasoning activity rows already started this
	// turn, keyed by their item id, so a delta never precedes its row.
	reasoningOpen map[string]bool

	// Mappings outlive the turn so Interrupt and fork anchors can translate
	// after settlement. Bounded by the number of turns in one process.
	nativeByMinted map[string]string
	mintedByNative map[string]string
}

// toolCall is what the tracker remembers about one tool call.
type toolCall struct {
	name  string
	input json.RawMessage
}

func newTurnTracker() *turnTracker {
	return &turnTracker{
		nativeByMinted: make(map[string]string),
		mintedByNative: make(map[string]string),
		toolTails:      make(map[string]string),
		tools:          make(map[string]toolCall),
		reasoningOpen:  make(map[string]bool),
	}
}

// prepare arms the tracker with the id SendTurn minted. It must run before the
// session/send request is written (DESIGN 3.6).
func (t *turnTracker) prepare(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending = id
}

// disarm drops a prepared id whose session/send was never written.
func (t *turnTracker) disarm(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == id {
		t.pending = ""
	}
}

// cancel disarms a prepared id after the session/send request failed. When the
// turn was already observed started, a failed turn.completed must reach the
// stream so the controller cannot stay Busy — and it must follow the pump's
// turn.started, which the pump emits on its own goroutine after observeStarted
// returns. So the caller emits the failure itself only when the pump reports
// turn.started already out (emitNow); otherwise the failure is parked here and
// the pump emits it right after turn.started (markStartedEmitted).
func (t *turnTracker) cancel(id string, failure ports.ChatEvent) (emitNow bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == id {
		t.pending = ""
	}
	if !t.open || t.current != id {
		return false
	}
	startedOut := t.startedOut
	t.closeLocked()
	if startedOut {
		return true
	}
	t.failAfterStart = &failure
	return false
}

// markStartedEmitted records that the pump put turn.started for id on the
// stream and hands back the failed completion a cancel parked for it, if any.
func (t *turnTracker) markStartedEmitted(id string) *ports.ChatEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open && t.current == id {
		t.startedOut = true
	}
	if t.failAfterStart != nil && t.failAfterStart.ProviderTurnID == id {
		failure := t.failAfterStart
		t.failAfterStart = nil
		return failure
	}
	return nil
}

// adopt opens the turn a previous daemon started (live reconnect): minted is
// the durable id the controller restored, native the app-server's id for it
// when session/read reported one. Nothing is emitted — the controller already
// owns the turn — but every later signal for it now correlates, so a completion
// replayed from the detach window settles it instead of being dropped.
func (t *turnTracker) adopt(minted, native string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending = ""
	t.openLocked(minted, native)
	t.startedOut = true
}

// openLocked opens a turn under the lock, resetting the per-turn latches.
func (t *turnTracker) openLocked(id, native string) {
	t.open = true
	t.current = id
	t.native = native
	t.textSource = ""
	t.replyItemID = ""
	t.startedOut = false
	t.toolTails = make(map[string]string)
	t.tools = make(map[string]toolCall)
	t.reasoningOpen = make(map[string]bool)
	if native != "" {
		t.nativeByMinted[id] = native
		t.mintedByNative[native] = id
	}
}

// closeLocked settles the open turn under the lock.
func (t *turnTracker) closeLocked() {
	t.open = false
	t.current = ""
	t.native = ""
	t.textSource = ""
	t.replyItemID = ""
	t.startedOut = false
}

// observeStarted folds one of the three start signals. It returns the
// turn.started event exactly once per turn and nil for every later signal, which
// only fills in the native id. inputID is the session/send inputId echoed on
// session/event turn.started (PROVEN, U3); when nothing is armed it names the
// minted id directly, which is how a replayed start after a reconnect still
// correlates with the turn AO prepared.
func (t *turnTracker) observeStarted(native, inputID string) *ports.ChatEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open {
		if t.native == "" && native != "" {
			t.native = native
			t.nativeByMinted[t.current] = native
			t.mintedByNative[native] = t.current
		}
		return nil
	}
	id := t.pending
	switch {
	case id != "":
		t.pending = ""
	case inputID != "" && !strings.HasPrefix(inputID, providerTurnPrefix):
		id = inputID
	case native != "":
		id = providerTurnPrefix + native
	default:
		// A start signal with neither a prepared turn nor a native id names
		// nothing AO can correlate; dropping it is safer than inventing a turn.
		return nil
	}
	t.openLocked(id, native)
	return &ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: id, NativeTurnID: native}
}

// observeCompleted settles the open turn. It returns the turn.completed event
// once and nil when nothing is open, so the prompt_* fallback cannot fire after
// session/event already settled the turn.
func (t *turnTracker) observeCompleted(state domain.TurnState, err error) *ports.ChatEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.open {
		return nil
	}
	ev := &ports.ChatEvent{
		Kind:           ports.ChatEventTurnCompleted,
		ProviderTurnID: t.current,
		NativeTurnID:   t.native,
		TurnState:      state,
		Err:            err,
	}
	t.closeLocked()
	return ev
}

// replyItem returns the item id assistant text is accumulated under: the
// provider's assistantMessageId whenever a delta names one, so a reply split
// around a tool call keeps one row per assistant message (PROVEN,
// p5-two-messages.ndjson: text on msg A, a tool exchange, then text on msg B),
// and otherwise a stable synthetic id for the turn. The latest adopted id is
// the one turn.completed settles, because its response repeats only the last
// message's text.
func (t *turnTracker) replyItem(candidate, turn string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case candidate != "":
		t.replyItemID = candidate
	case t.replyItemID == "":
		t.replyItemID = "zcode-reply:" + turn
	}
	return t.replyItemID
}

// progressDelta returns the part of a tool stream's new output tail that
// extends the previous one, and remembers the new tail. The key names the tool
// call and the stream (stdout and stderr are remembered separately). A tail
// that does not extend the last one yields "" (DESIGN 4.2: cumulative vs
// incremental is unknown).
func (t *turnTracker) progressDelta(key, tail string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous := t.toolTails[key]
	t.toolTails[key] = tail
	if !strings.HasPrefix(tail, previous) {
		return ""
	}
	return tail[len(previous):]
}

// active is the turn in flight as AO sees it: the open turn, or the prepared
// one whose session/send is written but whose prompt_started has not been
// pumped yet. An interrupt without a name targets it, and so does every
// notification or server request that arrives before the start signal is
// pumped (DESIGN 3.5: "the current pending turn").
func (t *turnTracker) active() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open {
		return t.current
	}
	return t.pending
}

// latchTextSource records which delta kind first carried text this turn and
// reports whether the given kind is the one to honour.
func (t *turnTracker) latchTextSource(kind string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.textSource == "" {
		t.textSource = kind
	}
	return t.textSource == kind
}

// rememberTool records a tool call's name and input as the streaming frames
// reveal them; later frames that omit either read them back.
func (t *turnTracker) rememberTool(toolCallID, name string, input json.RawMessage) {
	if toolCallID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	call := t.tools[toolCallID]
	if name != "" {
		call.name = name
	}
	if len(input) > 0 {
		call.input = input
	}
	t.tools[toolCallID] = call
}

// tool returns what is known about a tool call so far.
func (t *turnTracker) tool(toolCallID string) toolCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tools[toolCallID]
}

// openReasoning reports whether the reasoning row for an item still needs its
// activity.started, marking it open. The first reasoning_delta of a message
// opens the row because the wire has no reasoning_start frame.
func (t *turnTracker) openReasoning(itemID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reasoningOpen[itemID] {
		return false
	}
	t.reasoningOpen[itemID] = true
	return true
}

// closeReasoning forgets an open reasoning row and reports whether one was
// open, so the row can be completed when the message moves on to text or a
// tool call.
func (t *turnTracker) closeReasoning(itemID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.reasoningOpen[itemID] {
		return false
	}
	delete(t.reasoningOpen, itemID)
	return true
}

// nativeFor translates a minted id to the provider's turn id, or "".
func (t *turnTracker) nativeFor(minted string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.nativeByMinted[minted]
}

// mintedFor translates a provider turn id to the AO id, or "".
func (t *turnTracker) mintedFor(native string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mintedByNative[native]
}

// currentTurn is the open turn's AO id, or "".
func (t *turnTracker) currentTurn() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open {
		return t.current
	}
	return ""
}
