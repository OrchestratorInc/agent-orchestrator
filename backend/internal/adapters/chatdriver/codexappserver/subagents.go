package codexappserver

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

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
// writes exactly that. The steps stay on the child's own turn, so that turn's
// start and end settle them, and each says so (detail.childTurn) so the timeline
// shows the turn on the subagent rather than as a turn of the parent's.

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

// After a live reattach, a thread no launch names may be a child whose launch went
// out before the detach. Its launch chain is read back from the provider, at most
// maxSubagentDepth launches up, for at most maxSubagentRecoveries threads, each
// within subagentRecoveryWait. Recovery is best effort: a thread it cannot place
// goes out unfiled, as any thread no launch names does.
const (
	maxSubagentDepth      = 8
	maxSubagentRecoveries = 64
	subagentRecoveryWait  = 10 * time.Second
)

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
	// recovering holds threads whose launch chain is being read back after a
	// reattach. Their events wait for the answer.
	recovering map[string]bool
	// origin is the thread each spawn call was made on. A nested launch's steps wait
	// until its chain of launches reaches the root, so the launch they hang from has
	// gone out before them.
	origin map[string]string
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

func newSubagentRouter(scope func(string) string) *subagentRouter {
	return &subagentRouter{
		scope:      scope,
		inflight:   map[string]struct{}{},
		launchOf:   map[string]string{},
		expected:   map[string]bool{},
		recovering: map[string]bool{},
		origin:     map[string]string{},
		held:       map[string][]ports.ChatEvent{},
		streaming:  map[string]bool{},
		lastSaid:   map[string]string{},
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
		Item     threadItem `json:"item"`
	}
	r.root = root
	if json.Unmarshal(n.Params, &p) != nil {
		return
	}
	launch := p.Item.itemID()
	if n.Method == codexproto.MethodItemStarted && isSpawnAgent(p.Item) && launch != "" {
		if _, known := r.origin[launch]; !known {
			r.origin[launch] = p.ThreadID
		}
		r.inflight[launch] = struct{}{}
		return
	}
	if n.Method == codexproto.MethodItemCompleted && isSpawnAgent(p.Item) {
		delete(r.inflight, launch)
	}
	r.place(p.ThreadID, p.Item, root)
}

// namedChildren is the child threads a launch item names: a completed spawn in
// receiverThreadIds, a v2 launch in agentThreadId as soon as the child exists.
func namedChildren(it threadItem) []string {
	switch {
	case it.itemID() == "":
		return nil
	case isSubagentStart(it):
		return []string{deref(it.AgentThreadID)}
	case isSpawnAgent(it):
		return it.ReceiverThreadIds
	}
	return nil
}

// place records the child threads a launch item on thread names.
func (r *subagentRouter) place(thread string, it threadItem, root string) {
	for _, child := range namedChildren(it) {
		if child != "" && child != root && child != thread {
			r.placeChild(child, it.itemID(), thread)
		}
	}
}

func (r *subagentRouter) placeChild(child, launch, thread string) {
	if _, known := r.origin[launch]; !known {
		r.origin[launch] = thread
	}
	r.launchOf[child] = launch
}

func (r *subagentRouter) placed(thread string) bool {
	_, ok := r.launchOf[thread]
	return ok
}

// placedThreads is a copy of the threads a launch names, for a reader off the pump.
func (r *subagentRouter) placedThreads() map[string]bool {
	placed := make(map[string]bool, len(r.launchOf))
	for thread := range r.launchOf {
		placed[thread] = true
	}
	return placed
}

// subagentRecovery is what reading back one thread's launch chain found: for each
// thread on the chain, the launch that named it and the thread it was made on.
type subagentRecovery struct {
	thread     string
	placements []subagentPlacement
}

type subagentPlacement struct{ child, launch, parent string }

// recover holds thread's events while its launch chain is read back.
func (r *subagentRouter) recover(thread string) { r.recovering[thread] = true }

// recovered places what a read-back found. A thread it could not place stops
// waiting, and release() lets its events out as they were.
func (r *subagentRouter) recovered(found subagentRecovery) {
	for _, p := range found.placements {
		if !r.placed(p.child) {
			r.placeChild(p.child, p.launch, p.parent)
		}
	}
	delete(r.recovering, found.thread)
}

// recoverLaunchChain reads back the launches that lead to thread, which spoke
// after a live reattach with no launch naming it. A reconnect gets only what the
// host buffered while AO was away, so a launch delivered before the detach never
// comes again. It walks up the thread's ancestry to the root or to a thread already
// placed, then down, taking from each parent the newest launch that named the
// thread below. It runs off the pump, which holds the thread's events meanwhile,
// and always answers, with whatever it found.
func (c *conversation) recoverLaunchChain(thread string, placed map[string]bool, out chan<- subagentRecovery) {
	found := subagentRecovery{thread: thread}
	defer func() {
		select {
		case out <- found:
		case <-c.pumpDone:
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), subagentRecoveryWait)
	defer cancel()

	chain := []string{thread}
	for {
		parent, err := c.parentThread(ctx, chain[len(chain)-1])
		if err != nil {
			c.log.Debug("codex subagent launch not recovered", "thread", thread, "error", err)
			return
		}
		if parent == "" || len(chain) > maxSubagentDepth {
			// Not a subagent, such as a provider-internal thread, or too deep to follow.
			return
		}
		chain = append(chain, parent)
		if parent == c.threadID || placed[parent] {
			break
		}
	}
	for i := len(chain) - 1; i > 0; i-- {
		parent, child := chain[i], chain[i-1]
		launch, err := c.launchNaming(ctx, parent, child)
		if err != nil || launch == "" {
			c.log.Debug("codex subagent launch not recovered", "thread", child, "parent", parent, "error", err)
			return
		}
		found.placements = append(found.placements, subagentPlacement{child: child, launch: launch, parent: parent})
	}
}

// parentThread is the thread that started thread, or "" for one no launch started.
func (c *conversation) parentThread(ctx context.Context, thread string) (string, error) {
	var resp codexproto.ThreadReadResponse
	if err := c.conn.request(ctx, codexproto.MethodThreadRead, codexproto.ThreadReadParams{ThreadID: thread}, &resp); err != nil {
		return "", err
	}
	return deref(resp.Thread.ParentThreadID), nil
}

// launchNaming is the newest launch on parent that names child.
func (c *conversation) launchNaming(ctx context.Context, parent, child string) (string, error) {
	includeTurns := true
	var resp struct {
		Thread struct {
			Turns []struct {
				Items []threadItem `json:"items"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err := c.conn.request(ctx, codexproto.MethodThreadRead,
		codexproto.ThreadReadParams{ThreadID: parent, IncludeTurns: &includeTurns}, &resp); err != nil {
		return "", err
	}
	turns := resp.Thread.Turns
	for t := len(turns) - 1; t >= 0; t-- {
		for i := len(turns[t].Items) - 1; i >= 0; i-- {
			it := turns[t].Items[i]
			if slices.Contains(namedChildren(it), child) {
				return it.itemID(), nil
			}
		}
	}
	return "", nil
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
	if _, placed := r.launchOf[thread]; !placed && !r.claimable(thread) && !waiting {
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

// replyChunk reports a piece of a child's reply as it streams. Its message's
// completion restates the whole reply, so a chunk may be dropped under load like
// an assistant delta, unlike tool output, which nothing restates.
func (r *subagentRouter) replyChunk(ev ports.ChatEvent) bool {
	return ev.Kind == ports.ChatEventActivityText && r.streaming[ev.ProviderItemID]
}

// claimable reports whether a launch may still name a thread that none has yet:
// a spawn is in flight, Codex announced the thread as a child, or its launch is
// being read back.
func (r *subagentRouter) claimable(thread string) bool {
	return len(r.inflight) > 0 || r.expected[thread] || r.recovering[thread]
}

// settled reports whether a thread's events can be filed now: a launch named the
// thread, and that launch's chain reaches the root, so the launch it hangs from has
// gone out first. A nested launch whose own thread is not placed yet waits for its
// ancestor while anything could still place that thread.
func (r *subagentRouter) settled(thread string) bool {
	launch, placed := r.launchOf[thread]
	if !placed {
		return false
	}
	seen := map[string]bool{}
	for !seen[launch] {
		seen[launch] = true
		from := r.origin[launch]
		if from == r.root {
			return true
		}
		parent, nested := r.launchOf[from]
		if !nested {
			return !r.claimable(from)
		}
		launch = parent
	}
	// A malformed chain that loops back cannot resolve: file it under its own launch.
	return true
}

// release returns held events whose launch is now settled, filed under it. A
// thread nothing can claim any more goes out as it was.
func (r *subagentRouter) release() []ports.ChatEvent {
	var out []ports.ChatEvent
	kept := r.heldOrder[:0]
	for _, thread := range r.heldOrder {
		events := r.held[thread]
		_, placed := r.launchOf[thread]
		switch {
		case r.settled(thread):
			for _, ev := range events {
				out = append(out, r.adopt(thread, ev)...)
			}
		case !placed && !r.claimable(thread):
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
//
// Every event keeps the child's own turn. The child's turn start and end are what
// settle its steps, so a parent that finishes first leaves a running child command
// running, and a child that fails settles its own rows. The renderer files the steps
// under the launch by their parent link, whichever turn they are in.
func (r *subagentRouter) adopt(thread string, ev ports.ChatEvent) []ports.ChatEvent {
	parent := r.scope(r.launchOf[thread])
	switch ev.Kind {
	case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted, ports.ChatEventPlanUpdated, ports.ChatEventError:
		// A plan or a fatal error is a step too: on the child's turn it can never
		// replace the parent's plan or fail the parent's turn, and its row reads as
		// the child's.
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
		// The report goes out before the turn ends, so the child never reads as
		// finished without one.
		return []ports.ChatEvent{r.handback(thread, parent, ev), ev}
	}
	return []ports.ChatEvent{ev}
}

// handback is the child's report to its launch: its last message when its turn
// ends, or why the turn did not finish.
func (r *subagentRouter) handback(thread, parent string, ev ports.ChatEvent) ports.ChatEvent {
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
	return ports.ChatEvent{
		Kind: ports.ChatEventActivityCompleted, ProviderTurnID: ev.ProviderTurnID,
		ProviderConversationID: ev.ProviderConversationID,
		ProviderItemID:         "codex-handback:" + thread + ":" + ev.ProviderTurnID,
		ActivityKind:           domain.ActivityKindMCPTool, ActivityStatus: status,
		Summary: subagentHandbackTool, Detail: detail,
	}
}

// withParent stamps a detail payload with the launch it belongs to. childTurn says
// the step sits on the child's own turn, which the renderer then leaves out of the
// timeline: that turn's progress and outcome show on the subagent instead.
func withParent(detail []byte, parent string, message bool) []byte {
	detail = withField(detail, "parentProviderItemId", parent)
	detail = withField(detail, "childTurn", true)
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
