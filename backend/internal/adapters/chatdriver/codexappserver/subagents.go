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
// thread. The child runs on a thread of its own, and app-server multiplexes that
// thread's notifications over the root connection. Nothing on the child's events
// names the launch: only the spawn's item/completed does, in receiverThreadIds.
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
	// turnOf maps a spawn call to the root provider turn it belongs to, so a
	// child's steps join the parent's turn instead of a turn AO never opened.
	turnOf map[string]string

	// held keeps a child thread's events, in order, until its spawn names it.
	held      map[string][]ports.ChatEvent
	heldOrder []string
	heldCount int

	// streaming marks a child's message the router already opened as a step.
	streaming map[string]bool
	// lastSaid is the child's latest message, which becomes its report.
	lastSaid map[string]string
}

func newSubagentRouter(scope func(string) string) *subagentRouter {
	return &subagentRouter{
		scope:     scope,
		inflight:  map[string]struct{}{},
		launchOf:  map[string]string{},
		turnOf:    map[string]string{},
		held:      map[string][]ports.ChatEvent{},
		streaming: map[string]bool{},
		lastSaid:  map[string]string{},
	}
}

// observe records spawn calls. It runs before the notification's events are
// routed, so a launch is in flight before anything waits on it.
func (r *subagentRouter) observe(n notification, root string) {
	if n.Method != codexproto.MethodItemStarted && n.Method != codexproto.MethodItemCompleted {
		return
	}
	var p struct {
		ThreadID string     `json:"threadId"`
		TurnID   string     `json:"turnId"`
		Item     threadItem `json:"item"`
	}
	if json.Unmarshal(n.Params, &p) != nil || !isSpawnAgent(p.Item) {
		return
	}
	launch := p.Item.itemID()
	if launch == "" {
		return
	}
	// Read again on completion: a grandchild's launch sits on a child thread that
	// may only have been placed in between.
	r.turnOf[launch] = r.rootTurn(root, p.ThreadID, p.TurnID)
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

// rootTurn is the root turn an event on thread belongs to. A grandchild's launch
// is on a child thread, whose own turn AO never opened.
func (r *subagentRouter) rootTurn(root, thread, turn string) string {
	if launch, ok := r.launchOf[thread]; ok && thread != root {
		return r.turnOf[launch]
	}
	return turn
}

// route returns what to emit for one event from thread.
func (r *subagentRouter) route(root, thread string, ev ports.ChatEvent) []ports.ChatEvent {
	if thread == "" || thread == root {
		return []ports.ChatEvent{ev}
	}
	_, waiting := r.held[thread]
	if _, ok := r.launchOf[thread]; ok {
		if waiting {
			// Keep the child's order: what it did first goes out first.
			return append(r.release(), r.adopt(thread, ev)...)
		}
		return r.adopt(thread, ev)
	}
	if len(r.inflight) == 0 && !waiting {
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

// release returns held events whose spawn has now named their thread, filed
// under it. Once no spawn is in flight, nothing can claim the rest, so they go
// out as they were.
func (r *subagentRouter) release() []ports.ChatEvent {
	var out []ports.ChatEvent
	kept := r.heldOrder[:0]
	for _, thread := range r.heldOrder {
		events := r.held[thread]
		_, placed := r.launchOf[thread]
		switch {
		case placed:
			for _, ev := range events {
				out = append(out, r.adopt(thread, ev)...)
			}
		case len(r.inflight) == 0:
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
	turn := r.turnOf[launch]
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
