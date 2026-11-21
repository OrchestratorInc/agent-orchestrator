package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	spawnStartedParams   = `{"threadId":"thread-1","turnId":"root-turn","item":{"type":"collabAgentToolCall","id":"call-spawn","tool":"spawnAgent","status":"inProgress","senderThreadId":"thread-1","receiverThreadIds":[],"prompt":"Check the build\nThen report back.","model":"gpt-test","agentsStates":{}}}`
	spawnCompletedParams = `{"threadId":"thread-1","turnId":"root-turn","item":{"type":"collabAgentToolCall","id":"call-spawn","tool":"spawnAgent","status":"completed","senderThreadId":"thread-1","receiverThreadIds":["child-1"],"prompt":"Check the build\nThen report back.","agentsStates":{"child-1":{"status":"running"}}}}`
)

type detailFields struct {
	ParentProviderItemID string         `json:"parentProviderItemId"`
	ChildTurn            bool           `json:"childTurn"`
	NestedAgent          bool           `json:"nestedAgent"`
	ProviderToolName     string         `json:"providerToolName"`
	Background           bool           `json:"background"`
	Text                 string         `json:"text"`
	Output               string         `json:"output"`
	Input                map[string]any `json:"input"`
}

func decodeDetail(t *testing.T, ev ports.ChatEvent) detailFields {
	t.Helper()
	var fields detailFields
	if len(ev.Detail) > 0 {
		if err := json.Unmarshal(ev.Detail, &fields); err != nil {
			t.Fatalf("decode detail %s: %v", ev.Detail, err)
		}
	}
	return fields
}

func TestNormalizeSpawnAgentIsALaunch(t *testing.T) {
	started := normalizeOne(t, "item/started", spawnStartedParams)
	if started.ActivityKind != domain.ActivityKindMCPTool || started.Summary != "Check the build" || started.ProviderItemID != "call-spawn" {
		t.Fatalf("spawn started -> %+v", started)
	}
	detail := decodeDetail(t, started)
	if detail.ProviderToolName != "spawnAgent" || !detail.Background || detail.Input["prompt"] != "Check the build\nThen report back." {
		t.Fatalf("spawn detail = %s", started.Detail)
	}

	failed := normalizeOne(t, "item/completed", `{"threadId":"thread-1","turnId":"root-turn","item":{"type":"collabAgentToolCall","id":"call-spawn","tool":"spawnAgent","status":"failed","senderThreadId":"thread-1","receiverThreadIds":[],"agentsStates":{"x":{"status":"errored","message":"agent limit reached"}}}}`)
	if failed.ActivityStatus != domain.ActivityStatusFailed || decodeDetail(t, failed).Output != "agent limit reached" {
		t.Fatalf("failed spawn -> %+v detail %s", failed, failed.Detail)
	}

	// The other multi-agent tools are the parent managing its children, not work.
	normalizeNone(t, "item/completed", `{"threadId":"thread-1","turnId":"root-turn","item":{"type":"collabAgentToolCall","id":"call-wait","tool":"wait","status":"completed","senderThreadId":"thread-1","receiverThreadIds":["child-1"],"agentsStates":{}}}`)
}

// A child's work arrives on its own thread, before the spawn names that thread.
// It must land under the launch, on the child's own turn, with its last message as
// the hand-back before that turn ends. The child's turn settles its steps, never
// the parent's.
func TestChildThreadWorkFilesUnderItsLaunch(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"root-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"item/started","params":` + spawnStartedParams + `}`)
	srv.push(`{"method":"turn/started","params":{"threadId":"child-1","turn":{"id":"child-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"item/started","params":{"threadId":"child-1","turnId":"child-turn","item":{"type":"commandExecution","id":"child-cmd","command":"go build ./...","status":"inProgress"}}}`)
	srv.push(`{"method":"item/completed","params":` + spawnCompletedParams + `}`)
	srv.push(`{"method":"item/agentMessage/delta","params":{"threadId":"child-1","turnId":"child-turn","itemId":"child-msg","delta":"Build passes."}}`)
	srv.push(`{"method":"item/completed","params":{"threadId":"child-1","turnId":"child-turn","item":{"type":"agentMessage","id":"child-msg","text":"Build passes."}}}`)
	srv.push(`{"method":"turn/completed","params":{"threadId":"child-1","turn":{"id":"child-turn","status":"completed","items":[]}}}`)

	got := collectUntil(t, conv.Events(), func(ev ports.ChatEvent) bool {
		return ev.Kind == ports.ChatEventTurnCompleted && ev.ProviderConversationID == "child-1"
	})

	launchAt, commandAt, handbackAt := -1, -1, -1
	for i, ev := range got {
		switch {
		case ev.ProviderItemID == "call-spawn" && launchAt < 0:
			launchAt = i
		case ev.ProviderItemID == "child-cmd":
			commandAt = i
			if detail := decodeDetail(t, ev); ev.ProviderTurnID != "child-turn" || detail.ParentProviderItemID != "call-spawn" || !detail.ChildTurn {
				t.Fatalf("child command = %+v detail %s", ev, ev.Detail)
			}
		case strings.HasPrefix(ev.ProviderItemID, "codex-handback:"):
			handbackAt = i
		case ev.ProviderItemID == "child-msg":
			if ev.Kind == ports.ChatEventMessageDelta || ev.Kind == ports.ChatEventMessageCompleted {
				t.Fatalf("child message reached the parent's reply: %+v", ev)
			}
			if ev.Kind == ports.ChatEventActivityStarted && !decodeDetail(t, ev).NestedAgent {
				t.Fatalf("child message step = %s", ev.Detail)
			}
		case ev.Kind == ports.ChatEventTurnCompleted:
			if ev.ProviderTurnID != "child-turn" || ev.ProviderConversationID != "child-1" {
				t.Fatalf("child turn completion now names %q/%q", ev.ProviderConversationID, ev.ProviderTurnID)
			}
		}
	}
	if launchAt < 0 || commandAt < launchAt {
		t.Fatalf("launch at %d, child command at %d: %+v", launchAt, commandAt, got)
	}
	// The report is in before the child's turn ends, so the child never reads as
	// finished without one.
	if handbackAt < 0 || handbackAt != len(got)-2 {
		t.Fatalf("hand-back at %d of %d: %+v", handbackAt, len(got), got)
	}
	handback := got[handbackAt]
	detail := decodeDetail(t, handback)
	if handback.ActivityStatus != domain.ActivityStatusCompleted || handback.ProviderTurnID != "child-turn" ||
		detail.ParentProviderItemID != "call-spawn" || detail.Input["message"] != "Build passes." {
		t.Fatalf("hand-back = %+v detail %s", handback, handback.Detail)
	}
}

// collectUntil reads events until one matches done, which it includes.
func collectUntil(t *testing.T, events <-chan ports.ChatEvent, done func(ports.ChatEvent) bool) []ports.ChatEvent {
	t.Helper()
	var got []ports.ChatEvent
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed; events: %+v", got)
			}
			got = append(got, ev)
			if done(ev) {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out; events: %+v", got)
		}
	}
}

func TestSubagentRouterLeavesUnclaimedThreadsAlone(t *testing.T) {
	r := newSubagentRouter(func(id string) string { return id })
	ev := ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: "other-turn", ProviderItemID: "x"}
	out := r.route("root", "guardian", ev)
	if len(out) != 1 || out[0].ProviderTurnID != "other-turn" || out[0].Detail != nil {
		t.Fatalf("unclaimed thread rewritten: %+v", out)
	}

	// A spawn in flight holds an unknown thread; one that names another child
	// releases it unfiled.
	r.observe(notification{Method: "item/started", Params: json.RawMessage(spawnStartedParams)}, "thread-1")
	if out := r.route("thread-1", "guardian", ev); out != nil {
		t.Fatalf("held event emitted early: %+v", out)
	}
	r.observe(notification{Method: "item/completed", Params: json.RawMessage(spawnCompletedParams)}, "thread-1")
	released := r.release()
	if len(released) != 1 || released[0].Detail != nil || released[0].ProviderTurnID != "other-turn" {
		t.Fatalf("released = %+v", released)
	}
}

func TestSubagentHandbackCarriesAFailure(t *testing.T) {
	r := newSubagentRouter(func(id string) string { return "scoped:" + id })
	r.launchOf["child-1"] = "call-spawn"
	r.origin["call-spawn"] = "thread-1"
	out := r.route("thread-1", "child-1", ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "child-turn", ProviderConversationID: "child-1",
		TurnState: domain.TurnStateFailed, Err: errors.New("context window exceeded"),
	})
	if len(out) != 2 || out[1].Kind != ports.ChatEventTurnCompleted {
		t.Fatalf("out = %+v", out)
	}
	handback := out[0]
	detail := decodeDetail(t, handback)
	if handback.ActivityStatus != domain.ActivityStatusFailed || handback.ProviderTurnID != "child-turn" ||
		detail.ParentProviderItemID != "scoped:call-spawn" || detail.Input["message"] != "context window exceeded" {
		t.Fatalf("hand-back = %+v detail %s", handback, handback.Detail)
	}
}

// A retry on a child thread starts while the router still holds that thread. Its
// completion must come out after the start and under the launch, or the row stays
// running. Both stay on the child's turn, which settles the row if the child fails.
func TestChildRetryCompletionFollowsItsHeldStart(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"root-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"item/started","params":` + spawnStartedParams + `}`)
	srv.push(`{"method":"error","params":{"threadId":"child-1","turnId":"child-turn","willRetry":true,"error":{"message":"stream disconnected","codexErrorInfo":null,"additionalDetails":null}}}`)
	srv.push(`{"method":"item/started","params":{"threadId":"child-1","turnId":"child-turn","item":{"type":"commandExecution","id":"child-cmd","command":"ls","status":"inProgress"}}}`)
	srv.push(`{"method":"item/completed","params":` + spawnCompletedParams + `}`)

	var retries []ports.ChatEvent
	deadline := time.After(3 * time.Second)
	for len(retries) < 2 {
		select {
		case ev := <-conv.Events():
			if strings.HasPrefix(ev.ProviderItemID, "codex-retry:child-1") {
				retries = append(retries, ev)
			}
		case <-deadline:
			t.Fatalf("retry events = %+v", retries)
		}
	}
	if retries[0].Kind != ports.ChatEventActivityStarted || retries[1].Kind != ports.ChatEventActivityCompleted {
		t.Fatalf("retry order = %s then %s", retries[0].Kind, retries[1].Kind)
	}
	for _, ev := range retries {
		if ev.ProviderTurnID != "child-turn" || decodeDetail(t, ev).ParentProviderItemID != "call-spawn" {
			t.Fatalf("retry event not under its launch on the child's turn: %+v detail %s", ev, ev.Detail)
		}
	}
}

// A nested spawn can start and finish on a child thread before the outer spawn
// names that thread. The grandchild's steps wait until the inner launch they hang
// from can go out, then keep their own turn.
func TestNestedLaunchStepsWaitForTheirChainToReachTheRoot(t *testing.T) {
	r := newSubagentRouter(func(id string) string { return id })
	observe := func(method, params string) {
		r.observe(notification{Method: method, Params: json.RawMessage(params)}, "thread-1")
	}
	observe("item/started", spawnStartedParams)
	observe("item/started", `{"threadId":"child-1","turnId":"child-turn","item":{"type":"collabAgentToolCall","id":"call-inner","tool":"spawnAgent","status":"inProgress","receiverThreadIds":[]}}`)
	observe("item/completed", `{"threadId":"child-1","turnId":"child-turn","item":{"type":"collabAgentToolCall","id":"call-inner","tool":"spawnAgent","status":"completed","receiverThreadIds":["grandchild-1"]}}`)

	// The grandchild is named, but its chain does not reach the root yet: the step
	// waits rather than go out ahead of the launch it hangs from.
	step := ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: "grandchild-turn", ProviderItemID: "gc-cmd"}
	if out := r.route("thread-1", "grandchild-1", step); out != nil {
		t.Fatalf("grandchild step went out before its chain reached the root: %+v", out)
	}
	observe("item/completed", spawnCompletedParams)
	out := r.release()
	if len(out) != 1 || out[0].ProviderTurnID != "grandchild-turn" || decodeDetail(t, out[0]).ParentProviderItemID != "call-inner" {
		t.Fatalf("released grandchild step = %+v", out)
	}

	// Once the chain is complete, a later step goes straight out.
	out = r.route("thread-1", "grandchild-1", ports.ChatEvent{
		Kind: ports.ChatEventActivityStarted, ProviderTurnID: "grandchild-turn", ProviderItemID: "gc-cmd-2",
	})
	if len(out) != 1 || out[0].ProviderTurnID != "grandchild-turn" {
		t.Fatalf("grandchild step after placement = %+v", out)
	}
}

// The v2 protocol names a child's thread as soon as it exists, so a grandchild's
// launch can arrive before the launch of the child that made it. No spawn is in
// flight, but the child was announced: the grandchild's steps wait for the
// child's launch and go out after the inner launch they hang from.
func TestNestedV2StepsWaitForAnAnnouncedAncestor(t *testing.T) {
	r := newSubagentRouter(func(id string) string { return id })
	observe := func(method, params string) {
		r.observe(notification{Method: method, Params: json.RawMessage(params)}, "thread-1")
	}
	const innerLaunch = `{"threadId":"child-v2","turnId":"child-turn","item":{"type":"subAgentActivity","id":"act-inner","kind":"started","agentThreadId":"grandchild-v2","agentPath":"/root/alpha/beta"}}`
	observe("thread/started", `{"thread":{"id":"child-v2","parentThreadId":"thread-1"}}`)
	observe("thread/started", `{"thread":{"id":"grandchild-v2","parentThreadId":"child-v2"}}`)
	observe("item/completed", innerLaunch)

	launch := ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "child-turn", ProviderItemID: "act-inner"}
	if out := r.route("thread-1", "child-v2", launch); out != nil {
		t.Fatalf("inner launch went out before its child was placed: %+v", out)
	}
	step := ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: "grandchild-turn", ProviderItemID: "gc-cmd"}
	if out := r.route("thread-1", "grandchild-v2", step); out != nil {
		t.Fatalf("grandchild step went out while its parent's launch could still arrive: %+v", out)
	}

	observe("item/completed", `{"threadId":"thread-1","turnId":"root-turn","item":{"type":"subAgentActivity","id":"act-alpha","kind":"started","agentThreadId":"child-v2","agentPath":"/root/alpha"}}`)
	out := r.release()
	if len(out) != 2 || out[0].ProviderItemID != "act-inner" || out[1].ProviderItemID != "gc-cmd" {
		t.Fatalf("released = %+v", out)
	}
	if decodeDetail(t, out[0]).ParentProviderItemID != "act-alpha" || decodeDetail(t, out[1]).ParentProviderItemID != "act-inner" {
		t.Fatalf("released chain = %s, %s", out[0].Detail, out[1].Detail)
	}
	if out[1].ProviderTurnID != "grandchild-turn" {
		t.Fatalf("grandchild step turn = %q", out[1].ProviderTurnID)
	}
}

// Codex 0.169's default model speaks the v2 multi-agent protocol: the launch is a
// subAgentActivity item of kind "started" that names the child's thread. The
// child's command and report land under it; the parent keeps its own reply.
func TestV2SubagentLaunchFilesChildUnderIt(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"root-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"item/completed","params":{"threadId":"thread-1","turnId":"root-turn","item":{"type":"subAgentActivity","id":"act-alpha","kind":"started","agentThreadId":"child-v2","agentPath":"/root/alpha_task"}}}`)
	srv.push(`{"method":"turn/started","params":{"threadId":"child-v2","turn":{"id":"child-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"item/started","params":{"threadId":"child-v2","turnId":"child-turn","item":{"type":"commandExecution","id":"child-cmd","command":"printf AO_ALPHA_COMMAND","status":"inProgress"}}}`)
	srv.push(`{"method":"item/completed","params":{"threadId":"child-v2","turnId":"child-turn","item":{"type":"agentMessage","id":"child-msg","text":"AO_ALPHA_COMMAND"}}}`)
	srv.push(`{"method":"turn/completed","params":{"threadId":"child-v2","turn":{"id":"child-turn","status":"completed","items":[]}}}`)
	srv.push(`{"method":"item/completed","params":{"threadId":"thread-1","turnId":"root-turn","item":{"type":"agentMessage","id":"parent-msg","text":"Alpha reported back."}}}`)

	var launch, command, handback, parentReply *ports.ChatEvent
	deadline := time.After(3 * time.Second)
	for parentReply == nil {
		select {
		case ev := <-conv.Events():
			switch {
			case ev.ProviderItemID == "act-alpha":
				launch = &ev
			case ev.ProviderItemID == "child-cmd":
				command = &ev
			case strings.HasPrefix(ev.ProviderItemID, "codex-handback:"):
				handback = &ev
			case ev.ProviderItemID == "parent-msg":
				parentReply = &ev
			}
		case <-deadline:
			t.Fatal("parent reply never arrived")
		}
	}
	if launch == nil || launch.Summary != "alpha task" || decodeDetail(t, *launch).ProviderToolName != "spawnAgent" {
		t.Fatalf("launch = %+v", launch)
	}
	if command == nil || command.ProviderTurnID != "child-turn" || decodeDetail(t, *command).ParentProviderItemID != "act-alpha" {
		t.Fatalf("child command = %+v", command)
	}
	if handback == nil || decodeDetail(t, *handback).Input["message"] != "AO_ALPHA_COMMAND" ||
		decodeDetail(t, *handback).ParentProviderItemID != "act-alpha" {
		t.Fatalf("hand-back = %+v", handback)
	}
	if parentReply.Kind != ports.ChatEventMessageCompleted || parentReply.Text != "Alpha reported back." {
		t.Fatalf("parent reply = %+v", parentReply)
	}

	// The other v2 kinds report on a child that already has its launch.
	normalizeNone(t, "item/completed", `{"threadId":"thread-1","turnId":"root-turn","item":{"type":"subAgentActivity","id":"act-2","kind":"interacted","agentThreadId":"child-v2","agentPath":"/root/alpha_task"}}`)
}

// A v2 child can start working before its launch item arrives. Codex announces
// the child's thread with its parent, so its early steps wait and land under the
// launch instead of going out as loose rows.
func TestV2ChildStepsBeforeTheLaunchItemWaitForIt(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"root-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"thread/started","params":{"thread":{"id":"child-v2","parentThreadId":"thread-1"}}}`)
	srv.push(`{"method":"item/started","params":{"threadId":"child-v2","turnId":"child-turn","item":{"type":"commandExecution","id":"early-cmd","command":"printf AO_ALPHA_COMMAND","status":"inProgress"}}}`)
	srv.push(`{"method":"item/completed","params":{"threadId":"thread-1","turnId":"root-turn","item":{"type":"subAgentActivity","id":"act-alpha","kind":"started","agentThreadId":"child-v2","agentPath":"/root/alpha_task"}}}`)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-conv.Events():
			if ev.ProviderItemID != "early-cmd" {
				continue
			}
			if ev.ProviderTurnID != "child-turn" || decodeDetail(t, ev).ParentProviderItemID != "act-alpha" {
				t.Fatalf("early child step not under its launch: %+v detail %s", ev, ev.Detail)
			}
			return
		case <-deadline:
			t.Fatal("early child step never arrived")
		}
	}
}

// An announced child that no launch names by the end of the root turn goes out
// as it was, so nothing waits forever.
func TestAnnouncedChildWithoutALaunchIsReleasedAtTurnEnd(t *testing.T) {
	r := newSubagentRouter(func(id string) string { return id })
	r.observe(notification{Method: "thread/started", Params: json.RawMessage(`{"thread":{"id":"guardian-1","parentThreadId":"thread-1"}}`)}, "thread-1")
	step := ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: "g-turn", ProviderItemID: "g-step"}
	if out := r.route("thread-1", "guardian-1", step); out != nil {
		t.Fatalf("announced child's step went out before its launch could name it: %+v", out)
	}
	r.route("thread-1", "thread-1", ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "root-turn"})
	out := r.release()
	if len(out) != 1 || out[0].ProviderTurnID != "g-turn" || out[0].Detail != nil {
		t.Fatalf("released = %+v", out)
	}
}

// A child's plan stays on the child's own turn, so it cannot replace the parent's,
// and it carries its launch, so its row is one of the child's steps.
func TestChildPlanStaysOnItsTurnUnderItsLaunch(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"root-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"turn/plan/updated","params":{"threadId":"thread-1","turnId":"root-turn","plan":[{"step":"Spawn a checker","status":"inProgress"}]}}`)
	srv.push(`{"method":"item/started","params":` + spawnStartedParams + `}`)
	srv.push(`{"method":"item/completed","params":` + spawnCompletedParams + `}`)
	srv.push(`{"method":"turn/started","params":{"threadId":"child-1","turn":{"id":"child-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"turn/plan/updated","params":{"threadId":"child-1","turnId":"child-turn","plan":[{"step":"Run the build","status":"inProgress"}]}}`)

	var plans []ports.ChatEvent
	collectUntil(t, conv.Events(), func(ev ports.ChatEvent) bool {
		if ev.Kind == ports.ChatEventPlanUpdated {
			plans = append(plans, ev)
		}
		return len(plans) == 2
	})
	root, child := plans[0], plans[1]
	if root.ProviderTurnID != "root-turn" || decodeDetail(t, root).ParentProviderItemID != "" {
		t.Fatalf("parent plan = %+v detail %s", root, root.Detail)
	}
	if detail := decodeDetail(t, child); child.ProviderTurnID != "child-turn" ||
		detail.ParentProviderItemID != "call-spawn" || !detail.ChildTurn {
		t.Fatalf("child plan = %+v detail %s", child, child.Detail)
	}
}

// A launch that reached AO before the daemon detached is not sent again when it
// reattaches to the surviving provider. The driver reads it back, so the child's
// reply and report after the reattach still land under it.
func TestReconnectFilesASurvivingChildUnderItsEarlierLaunch(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replyError("initialize", -32600, "Already initialized")
	proc, err := d.spawn(context.Background(), "codex", "/tmp/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	d.persistent = true
	d.connectHost = func(_ context.Context, _ persistenthost.Config) (*persistenthost.Transport, error) {
		return &persistenthost.Transport{Stdin: proc.stdin, Stdout: proc.stdout, Reconnected: true, NextRequestID: 41}, nil
	}
	spawned := `{"type":"collabAgentToolCall","id":"call-spawn","tool":"spawnAgent","status":"completed","senderThreadId":"thread-1","receiverThreadIds":["child-1"],"prompt":"Check the build","agentsStates":{}}`
	srv.respondSequence("thread/read",
		`{"thread":{"id":"thread-1","turns":[{"id":"root-turn","status":"inProgress","items":[`+spawned+`]}]}}`,
		`{"thread":{"id":"child-1","turns":[{"id":"child-turn","status":"inProgress","items":[]}]}}`)

	conv, err := d.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "subagent-reconnect", ProviderConversationID: "thread-1", DataDir: t.TempDir(), WorkspacePath: "/tmp/ws",
	})
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"item/completed","params":{"threadId":"child-1","turnId":"child-turn","item":{"type":"agentMessage","id":"child-msg","text":"Build passes."}}}`)
	srv.push(`{"method":"turn/completed","params":{"threadId":"child-1","turn":{"id":"child-turn","status":"completed","items":[]}}}`)

	got := collectUntil(t, conv.Events(), func(ev ports.ChatEvent) bool {
		return ev.Kind == ports.ChatEventTurnCompleted && ev.ProviderConversationID == "child-1"
	})
	var reply, handback *ports.ChatEvent
	for i, ev := range got {
		switch {
		case ev.ProviderItemID == "child-msg":
			reply = &got[i]
		case strings.HasPrefix(ev.ProviderItemID, "codex-handback:"):
			handback = &got[i]
		}
	}
	if reply == nil || reply.Kind != ports.ChatEventActivityCompleted || decodeDetail(t, *reply).ParentProviderItemID != "call-spawn" {
		t.Fatalf("child reply after reattach = %+v", reply)
	}
	if handback == nil || decodeDetail(t, *handback).ParentProviderItemID != "call-spawn" ||
		decodeDetail(t, *handback).Input["message"] != "Build passes." {
		t.Fatalf("hand-back after reattach = %+v", handback)
	}
}
