package codexappserver

import (
	"encoding/json"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Codex subagents, in the shape AO's chat already reads for Claude's.
//
// A Codex launch is a collabAgentToolCall item (tool spawnAgent) on the parent's
// thread, or, in the v2 multi-agent protocol, a subAgentActivity item of kind
// "started". The child runs on a thread of its own, and app-server multiplexes that
// thread's notifications over the root connection. Nothing on the child's events
// names the launch: only the spawn's item/completed does, in receiverThreadIds, or
// the v2 item, in agentThreadId.
//
// The renderer groups a launch's steps by detail.parentProviderItemId and takes a
// SubagentHandback step as the child's report (subagent-team.ts). subagentRouter
// writes exactly that, so the timeline needs no Codex-specific code.

// codexSpawnTool is the launch's providerToolName. The renderer lists it among the
// tool names that start a subagent.
const codexSpawnTool = string(codexproto.CollabAgentToolSpawnAgent)

// subagentHandbackTool marks the child's report back to the launch. It is the name
// the renderer already reads for Claude's hand-back.
const subagentHandbackTool = "SubagentHandback"

// maxHeldSubagentEvents caps what the router holds while it waits for a spawn to
// name its child. Spawn completes as soon as the child starts, so the wait is
// short; past the cap, everything held goes out unfiled rather than piling up.
const maxHeldSubagentEvents = 2000

func isSpawnAgent(it threadItem) bool {
	return it.Type == itemCollabAgentToolCall && deref(it.Tool) == codexSpawnTool
}

// isSubagentStart reports a v2 launch: the child exists and has a thread.
func isSubagentStart(it threadItem) bool {
	return it.Type == itemSubAgentActivity && it.Kind != nil &&
		*it.Kind == codexproto.SubAgentActivityKindStarted && deref(it.AgentThreadID) != ""
}

// agentPathSummary labels a v2 launch with the task name in its agent path
// (/root/check_build reads "check build"); v2 does not carry the prompt.
func agentPathSummary(path string) string {
	name := path[strings.LastIndex(path, "/")+1:]
	if name = strings.TrimSpace(strings.ReplaceAll(name, "_", " ")); name != "" {
		return truncateLabel(name)
	}
	return "Started a subagent"
}

// spawnSummary labels a launch with the first line of the task it was given, the
// way Claude's launch is labelled by its description.
func spawnSummary(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncateLabel(line)
		}
	}
	return "Started a subagent"
}

// spawnDetail is the launch's payload. `background` says the launch settles as
// soon as the child starts, so only the child's hand-back says it finished.
func spawnDetail(it threadItem) []byte {
	detail := map[string]any{
		"providerToolName": codexSpawnTool,
		"background":       true,
	}
	if prompt := strings.TrimSpace(deref(it.Prompt)); prompt != "" {
		detail["input"] = map[string]any{"prompt": prompt}
	}
	if model := strings.TrimSpace(deref(it.Model)); model != "" {
		detail["model"] = model
	}
	// A spawn that could not start the child says why in the state it reports for it.
	for _, state := range it.AgentsStates {
		if state.Message == nil || strings.TrimSpace(*state.Message) == "" {
			continue
		}
		if state.Status == codexproto.CollabAgentStatusErrored || state.Status == codexproto.CollabAgentStatusNotFound {
			detail["output"] = strings.TrimSpace(*state.Message)
			break
		}
	}
	return encodeDetail(detail)
}

// subagentRouter files each child thread's events under the launch that started
// it. It only reads and rewrites events; it never drops a child's work, so a
// thread it cannot place goes out exactly as it did before.
type subagentRouter struct {
	// scope turns a native item id into the projected one. The parent id inside a
	// detail payload is not touched by the conversation's own event scoping.
	scope func(string) string

	// inflight holds spawn calls that started but have not named their child.
	inflight map[string]struct{}
	// launchOf maps a child thread to the spawn call that started it.
	launchOf map[string]string
	// expected holds child threads Codex announced (thread/started with a parent)
	// that no launch has named yet. A v2 child can start before its launch item, so
	// its events wait for it until the root turn ends.
	expected map[string]bool
	// origin is where each spawn call was made: its thread and native turn. The
	// root turn a launch belongs to is read from it when routing (rootTurnOf), so a
	// nested launch still joins the root turn when its own thread is placed only
	// after the nested spawn completed.
	origin map[string]launchOrigin
	// root is the conversation's own thread, as last seen.
	root string

	// held keeps a child thread's events, in order, until its spawn names it.
	held      map[string][]ports.ChatEvent
	heldOrder []string
	heldCount int

	// streaming marks a child's message the router already opened as a step.
	streaming map[string]bool
	// lastSaid is the child's latest message, which becomes its report.
	lastSaid map[string]string
}

type launchOrigin struct{ thread, turn string }

func newSubagentRouter(scope func(string) string) *subagentRouter {
	return &subagentRouter{
		scope:     scope,
		inflight:  map[string]struct{}{},
		launchOf:  map[string]string{},
		expected:  map[string]bool{},
		origin:    map[string]launchOrigin{},
		held:      map[string][]ports.ChatEvent{},
		streaming: map[string]bool{},
		lastSaid:  map[string]string{},
	}
}

// observe records spawn calls. It runs before the notification's events are
// routed, so a launch is in flight before anything waits on it.
func (r *subagentRouter) observe(n notification, root string) {
	if n.Method == codexproto.MethodThreadStarted {
		var started codexproto.ThreadStartedNotification
		if json.Unmarshal(n.Params, &started) == nil && deref(started.Thread.ParentThreadID) != "" &&
			started.Thread.ID != root {
			if _, placed := r.launchOf[started.Thread.ID]; !placed {
				r.expected[started.Thread.ID] = true
			}
		}
		return
	}
	if n.Method != codexproto.MethodItemStarted && n.Method != codexproto.MethodItemCompleted {
		return
	}
	var p struct {
		ThreadID string     `json:"threadId"`
		TurnID   string     `json:"turnId"`
		Item     threadItem `json:"item"`
	}
	r.root = root
	if json.Unmarshal(n.Params, &p) != nil {
		return
	}
	launch := p.Item.itemID()
	v2 := isSubagentStart(p.Item)
	if launch == "" || (!v2 && !isSpawnAgent(p.Item)) {
		return
	}
	if _, known := r.origin[launch]; !known {
		r.origin[launch] = launchOrigin{thread: p.ThreadID, turn: p.TurnID}
	}
	if v2 {
		// The v2 item names the child's thread as soon as it exists.
		if child := deref(p.Item.AgentThreadID); child != root {
			r.launchOf[child] = launch
		}
		return
	}
	if n.Method == codexproto.MethodItemStarted {
		r.inflight[launch] = struct{}{}
		return
	}
	delete(r.inflight, launch)
	for _, child := range p.Item.ReceiverThreadIds {
		if child != "" && child != root {
			r.launchOf[child] = launch
		}
	}
}

// rootTurnOf is the root turn a launch belongs to. A grandchild's launch is on a
// child thread, whose own turn AO never opened, so it takes its ancestor's.
func (r *subagentRouter) rootTurnOf(launch string) string {
	seen := map[string]bool{}
	for !seen[launch] {
		seen[launch] = true
		from := r.origin[launch]
		parent, nested := r.launchOf[from.thread]
		if !nested || from.thread == r.root {
			return from.turn
		}
		launch = parent
	}
	// A malformed chain that loops back: keep the turn the launch named itself.
	return r.origin[launch].turn
}

// route returns what to emit for one event from thread.
func (r *subagentRouter) route(root, thread string, ev ports.ChatEvent) []ports.ChatEvent {
	r.root = root
	if thread == "" || thread == root {
		if ev.Kind == ports.ChatEventTurnCompleted {
			// A child no launch named by the end of the root turn is not coming;
			// release() lets its events out as they were.
			r.expected = map[string]bool{}
		}
		return []ports.ChatEvent{ev}
	}
	_, waiting := r.held[thread]
	if r.settled(thread) {
		if waiting {
			// Keep the child's order: what it did first goes out first.
			return append(r.release(), r.adopt(thread, ev)...)
		}
		return r.adopt(thread, ev)
	}
	if _, placed := r.launchOf[thread]; !placed && len(r.inflight) == 0 && !r.expected[thread] && !waiting {
		// No spawn could claim this thread, such as a provider-internal one.
		return []ports.ChatEvent{ev}
	}
	if r.heldCount >= maxHeldSubagentEvents {
		return append(r.flush(), ev)
	}
	if !waiting {
		r.heldOrder = append(r.heldOrder, thread)
	}
	r.held[thread] = append(r.held[thread], ev)
	r.heldCount++
	return nil
}

// settled reports whether a thread's events can be filed now: a spawn named the
// thread, and its launch's chain reaches the root, so its steps get the root turn.
// A nested launch whose own thread is not placed yet waits for its ancestor, while
// any spawn that could place it is still in flight.
func (r *subagentRouter) settled(thread string) bool {
	launch, placed := r.launchOf[thread]
	if !placed {
		return false
	}
	if len(r.inflight) == 0 {
		return true
	}
	seen := map[string]bool{}
	for !seen[launch] {
		seen[launch] = true
		from := r.origin[launch]
		if from.thread == r.root {
			return true
		}
		parent, nested := r.launchOf[from.thread]
		if !nested {
			return false
		}
		launch = parent
	}
	return false
}

// release returns held events whose launch is now settled, filed under it. Once
// no spawn is in flight, nothing can claim the rest, so they go out as they were.
func (r *subagentRouter) release() []ports.ChatEvent {
	var out []ports.ChatEvent
	kept := r.heldOrder[:0]
	for _, thread := range r.heldOrder {
		events := r.held[thread]
		switch {
		case r.settled(thread):
			for _, ev := range events {
				out = append(out, r.adopt(thread, ev)...)
			}
		case len(r.inflight) == 0 && !r.expected[thread]:
			out = append(out, events...)
		default:
			kept = append(kept, thread)
			continue
		}
		r.heldCount -= len(events)
		delete(r.held, thread)
	}
	r.heldOrder = kept
	return out
}

// flush returns everything held, unfiled. Used when the stream ends or the cap is hit.
func (r *subagentRouter) flush() []ports.ChatEvent {
	var out []ports.ChatEvent
	for _, thread := range r.heldOrder {
		out = append(out, r.held[thread]...)
	}
	r.held = map[string][]ports.ChatEvent{}
	r.heldOrder = nil
	r.heldCount = 0
	return out
}

// adopt rewrites one child event into a step under its launch.
func (r *subagentRouter) adopt(thread string, ev ports.ChatEvent) []ports.ChatEvent {
	launch := r.launchOf[thread]
	parent := r.scope(launch)
	turn := r.rootTurnOf(launch)
	childTurn := ev.ProviderTurnID
	if turn != "" && isChildStep(ev.Kind) {
		// A step joins the parent's turn. Everything else keeps the child's native
		// turn, so the child's lifecycle, diff or plan can never settle or replace
		// the parent's.
		ev.ProviderTurnID = turn
	}
	switch ev.Kind {
	case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted:
		ev.Detail = withParent(ev.Detail, parent, false)
		return []ports.ChatEvent{ev}

	case ports.ChatEventMessageDelta:
		// What the child says is a step of its own, not the parent's reply.
		var out []ports.ChatEvent
		if !r.streaming[ev.ProviderItemID] {
			r.streaming[ev.ProviderItemID] = true
			out = append(out, ports.ChatEvent{
				Kind: ports.ChatEventActivityStarted, ProviderTurnID: ev.ProviderTurnID,
				ProviderConversationID: ev.ProviderConversationID, ProviderItemID: ev.ProviderItemID,
				ActivityKind: domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusRunning,
				Summary: "Subagent response", Detail: withParent(nil, parent, true),
			})
		}
		return append(out, ports.ChatEvent{
			Kind: ports.ChatEventActivityText, ProviderTurnID: ev.ProviderTurnID,
			ProviderConversationID: ev.ProviderConversationID, ProviderItemID: ev.ProviderItemID, Delta: ev.Delta,
		})

	case ports.ChatEventMessageCompleted:
		delete(r.streaming, ev.ProviderItemID)
		if text := strings.TrimSpace(ev.Text); text != "" {
			r.lastSaid[thread] = text
		}
		detail := withParent(nil, parent, true)
		if text := strings.TrimSpace(ev.Text); text != "" {
			detail = withField(detail, "text", text)
		}
		return []ports.ChatEvent{{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: ev.ProviderTurnID,
			ProviderConversationID: ev.ProviderConversationID, ProviderItemID: ev.ProviderItemID,
			ActivityKind: domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusCompleted,
			Summary: "Subagent response", Text: ev.Text, Detail: detail,
		}}

	case ports.ChatEventTurnCompleted:
		return []ports.ChatEvent{ev, r.handback(thread, parent, turn, childTurn, ev)}
	}
	return []ports.ChatEvent{ev}
}

// isChildStep reports the events that make or extend one of the child's steps.
func isChildStep(kind ports.ChatEventKind) bool {
	switch kind {
	case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted, ports.ChatEventActivityText,
		ports.ChatEventCommandOutputDelta, ports.ChatEventCommandInput, ports.ChatEventReasoningDelta,
		ports.ChatEventMessageDelta, ports.ChatEventMessageCompleted:
		return true
	}
	return false
}

// handback is the child's report to its launch: its last message when its turn
// ends, or why the turn did not finish.
func (r *subagentRouter) handback(thread, parent, turn, childTurn string, ev ports.ChatEvent) ports.ChatEvent {
	message := r.lastSaid[thread]
	delete(r.lastSaid, thread)
	status := domain.ActivityStatusCompleted
	switch {
	case ev.TurnState == domain.TurnStateFailed || ev.Err != nil:
		status = domain.ActivityStatusFailed
		if ev.Err != nil {
			message = ev.Err.Error()
		}
	case ev.TurnState == domain.TurnStateInterrupted || ev.TurnState == domain.TurnStateCancelled:
		status = domain.ActivityStatusCancelled
	}
	detail := withParent(nil, parent, false)
	detail = withField(detail, "providerToolName", subagentHandbackTool)
	if message != "" {
		detail = withField(detail, "input", map[string]any{"message": message})
	}
	if turn == "" {
		turn = childTurn
	}
	return ports.ChatEvent{
		Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turn,
		ProviderConversationID: ev.ProviderConversationID,
		ProviderItemID:         "codex-handback:" + thread + ":" + childTurn,
		ActivityKind:           domain.ActivityKindMCPTool, ActivityStatus: status,
		Summary: subagentHandbackTool, Detail: detail,
	}
}

// withParent stamps a detail payload with the launch it belongs to.
func withParent(detail []byte, parent string, message bool) []byte {
	detail = withField(detail, "parentProviderItemId", parent)
	if message {
		detail = withField(detail, "nestedAgent", true)
	}
	return detail
}

func withField(detail []byte, key string, value any) []byte {
	fields := map[string]any{}
	if len(detail) > 0 && json.Unmarshal(detail, &fields) != nil {
		// Not an object: keep the payload rather than lose it to a stamp.
		return detail
	}
	fields[key] = value
	return encodeDetail(fields)
}
