package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	spawnStartedParams   = `{"threadId":"thread-1","turnId":"root-turn","item":{"type":"collabAgentToolCall","id":"call-spawn","tool":"spawnAgent","status":"inProgress","senderThreadId":"thread-1","receiverThreadIds":[],"prompt":"Check the build\nThen report back.","model":"gpt-test","agentsStates":{}}}`
	spawnCompletedParams = `{"threadId":"thread-1","turnId":"root-turn","item":{"type":"collabAgentToolCall","id":"call-spawn","tool":"spawnAgent","status":"completed","senderThreadId":"thread-1","receiverThreadIds":["child-1"],"prompt":"Check the build\nThen report back.","agentsStates":{"child-1":{"status":"running"}}}}`
)

type detailFields struct {
	ParentProviderItemID string         `json:"parentProviderItemId"`
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
// It must land under the launch, in the parent's turn, with its last message as
// the hand-back, and the child's turn must never settle the parent's.
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

	var got []ports.ChatEvent
	deadline := time.After(3 * time.Second)
	for !hasHandback(got) {
		select {
		case ev := <-conv.Events():
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("no hand-back; events: %+v", got)
		}
	}

	launchAt, commandAt := -1, -1
	for i, ev := range got {
		switch {
		case ev.ProviderItemID == "call-spawn" && launchAt < 0:
			launchAt = i
		case ev.ProviderItemID == "child-cmd":
			commandAt = i
			if ev.ProviderTurnID != "root-turn" || decodeDetail(t, ev).ParentProviderItemID != "call-spawn" {
				t.Fatalf("child command = %+v detail %s", ev, ev.Detail)
			}
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
	handback := got[len(got)-1]
	detail := decodeDetail(t, handback)
	if handback.ActivityStatus != domain.ActivityStatusCompleted || handback.ProviderTurnID != "root-turn" ||
		detail.ParentProviderItemID != "call-spawn" || detail.Input["message"] != "Build passes." {
		t.Fatalf("hand-back = %+v detail %s", handback, handback.Detail)
	}
}

func hasHandback(events []ports.ChatEvent) bool {
	for _, ev := range events {
		if strings.HasPrefix(ev.ProviderItemID, "codex-handback:") {
			return true
		}
	}
	return false
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
	r.origin["call-spawn"] = launchOrigin{thread: "thread-1", turn: "root-turn"}
	out := r.route("thread-1", "child-1", ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "child-turn", ProviderConversationID: "child-1",
		TurnState: domain.TurnStateFailed, Err: errors.New("context window exceeded"),
	})
	if len(out) != 2 {
		t.Fatalf("out = %+v", out)
	}
	handback := out[1]
	detail := decodeDetail(t, handback)
	if handback.ActivityStatus != domain.ActivityStatusFailed || detail.ParentProviderItemID != "scoped:call-spawn" ||
		detail.Input["message"] != "context window exceeded" {
		t.Fatalf("hand-back = %+v detail %s", handback, handback.Detail)
	}
}

// A retry on a child thread starts while the router still holds that thread. Its
// completion must come out after the start and under the launch, not ahead of it
// on the child's own turn, or the row stays running.
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
		if ev.ProviderTurnID != "root-turn" || decodeDetail(t, ev).ParentProviderItemID != "call-spawn" {
			t.Fatalf("retry event not under its launch: %+v detail %s", ev, ev.Detail)
		}
	}
}

// A nested spawn can start and finish on a child thread before the outer spawn
// names that thread. The grandchild's steps still join the root turn.
func TestNestedLaunchJoinsRootTurnWhenItsThreadIsPlacedLater(t *testing.T) {
	r := newSubagentRouter(func(id string) string { return id })
	observe := func(method, params string) {
		r.observe(notification{Method: method, Params: json.RawMessage(params)}, "thread-1")
	}
	observe("item/started", spawnStartedParams)
	observe("item/started", `{"threadId":"child-1","turnId":"child-turn","item":{"type":"collabAgentToolCall","id":"call-inner","tool":"spawnAgent","status":"inProgress","receiverThreadIds":[]}}`)
	observe("item/completed", `{"threadId":"child-1","turnId":"child-turn","item":{"type":"collabAgentToolCall","id":"call-inner","tool":"spawnAgent","status":"completed","receiverThreadIds":["grandchild-1"]}}`)

	// The grandchild is named, but its chain does not reach the root yet: the step
	// waits rather than go out on the child's own turn.
	step := ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: "grandchild-turn", ProviderItemID: "gc-cmd"}
	if out := r.route("thread-1", "grandchild-1", step); out != nil {
		t.Fatalf("grandchild step went out before its chain reached the root: %+v", out)
	}
	observe("item/completed", spawnCompletedParams)
	out := r.release()
	if len(out) != 1 || out[0].ProviderTurnID != "root-turn" || decodeDetail(t, out[0]).ParentProviderItemID != "call-inner" {
		t.Fatalf("released grandchild step = %+v", out)
	}

	// Once the chain is complete, a later step goes straight out.
	out = r.route("thread-1", "grandchild-1", ports.ChatEvent{
		Kind: ports.ChatEventActivityStarted, ProviderTurnID: "grandchild-turn", ProviderItemID: "gc-cmd-2",
	})
	if len(out) != 1 || out[0].ProviderTurnID != "root-turn" {
		t.Fatalf("grandchild step after placement = %+v", out)
	}
}
