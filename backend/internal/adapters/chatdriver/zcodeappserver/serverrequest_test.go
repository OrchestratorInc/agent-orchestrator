package zcodeappserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const permissionRequestParams = `{"requestId":"r1","sessionId":"sess-1","turnId":"t-native","toolCallId":"tc1","toolName":"Bash","reason":"run date","riskLevel":"low","input":{"command":"date"},` +
	`"options":[{"optionId":"allow-once","kind":"allow","name":"Allow once","response":{"decision":"allow"}},` +
	`{"optionId":"allow-always","kind":"allow","name":"Always allow","response":{"decision":"allow","permissionUpdates":[{"type":"addRules"}]}},` +
	`{"optionId":"deny","kind":"deny","name":"Deny","response":{"decision":"deny","reason":"no"}}]}`

func pushPermission(srv *scriptedServer, frameID string) {
	srv.push(`{"id":"` + frameID + `","method":"interaction/requestPermission","params":` + permissionRequestParams + `}`)
}

func TestApprovalIsParkedUntilResolvedAndEchoesProviderPayload(t *testing.T) {
	conv, srv := startConversation(t)
	pushPermission(srv, "server-3")

	requested := nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	if requested.RequestID != "r1" || requested.ProviderItemID != "r1" || requested.ActivityKind != domain.ActivityKindCommand {
		t.Fatalf("approval.requested = %+v", requested)
	}
	if requested.Summary != "Bash: run date" {
		t.Fatalf("summary = %q", requested.Summary)
	}
	if len(requested.Decisions) != 3 {
		t.Fatalf("decisions = %+v", requested.Decisions)
	}
	kinds := map[string]ports.ChatDecisionKind{}
	for _, d := range requested.Decisions {
		kinds[d.ID] = d.Kind
	}
	if kinds["allow-once"] != ports.ChatDecisionAllowOnce || kinds["allow-always"] != ports.ChatDecisionAllowAlways || kinds["deny"] != ports.ChatDecisionRejectOnce {
		t.Fatalf("decision kinds = %v", kinds)
	}

	time.Sleep(50 * time.Millisecond)
	if srv.countFrames(func(f frame) bool { return f.Method == "" && f.ID != nil && rawID(*f.ID) == "server-3" }) != 0 {
		t.Fatal("reply written before a decision")
	}

	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "allow-once"}); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}
	reply := srv.awaitReply("server-3")
	if !jsonEqual(reply.Result, json.RawMessage(`{"decision":"allow"}`)) {
		t.Fatalf("reply result = %s, want options[0].response verbatim", reply.Result)
	}
	if reply.Error != nil {
		t.Fatalf("reply carried an error: %+v", reply.Error)
	}
}

func TestReannouncedRequestIsNotASecondApproval(t *testing.T) {
	conv, srv := startConversation(t)
	pushPermission(srv, "server-3")
	nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	pushPermission(srv, "server-4")

	if got := drainEvents(conv.Events(), 100*time.Millisecond); countKind(got, ports.ChatEventApprovalRequested) != 0 {
		t.Fatalf("re-announce produced a second approval: %+v", got)
	}
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "deny"}); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}
	first := srv.awaitReply("server-3")
	second := srv.awaitReply("server-4")
	if string(first.Result) != string(second.Result) {
		t.Fatalf("replies differ: %s vs %s", first.Result, second.Result)
	}
	if !jsonEqual(first.Result, json.RawMessage(`{"decision":"deny","reason":"no"}`)) {
		t.Fatalf("reply = %s", first.Result)
	}

	// A re-announce that crossed the reply on the wire is answered from memory
	// and emits nothing.
	pushPermission(srv, "server-5")
	late := srv.awaitReply("server-5")
	if string(late.Result) != string(first.Result) {
		t.Fatalf("late reply = %s, want %s", late.Result, first.Result)
	}
	if got := drainEvents(conv.Events(), 100*time.Millisecond); countKind(got, ports.ChatEventApprovalRequested) != 0 {
		t.Fatalf("post-resolution re-announce produced an approval: %+v", got)
	}

	// Once the server reports it resolved, a decision for it is no longer
	// pending — but a re-announce the server wrote before it processed the
	// reply can still arrive after that event, and is answered from memory.
	srv.push(`{"method":"session/event","params":{"type":"permission.resolved","payload":{"requestId":"r1","toolCallId":"tc1"}}}`)
	nextEvent(t, conv.Events(), ports.ChatEventApprovalResolved)
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "deny"}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("ResolveRequest after resolution = %v", err)
	}
	pushPermission(srv, "server-6")
	if afterResolved := srv.awaitReply("server-6"); string(afterResolved.Result) != string(first.Result) {
		t.Fatalf("re-announce after permission.resolved = %+v, want the remembered %s", afterResolved, first.Result)
	}
	if got := drainEvents(conv.Events(), 100*time.Millisecond); countKind(got, ports.ChatEventApprovalRequested) != 0 {
		t.Fatalf("re-announce after permission.resolved became a ghost approval: %+v", got)
	}
}

// replayOnAttach makes the scripted host replay frames the moment the daemon
// attaches: they are the first bytes on the wire, ahead of any response, which
// is the order the persistent host produces.
func replayOnAttach(srv *scriptedServer, frames ...string) {
	srv.proc.stdout = io.MultiReader(strings.NewReader(strings.Join(frames, "\n")+"\n"), srv.proc.stdout)
}

func permissionFrame(frameID string) string {
	return `{"id":"` + frameID + `","method":"interaction/requestPermission","params":` + permissionRequestParams + `}`
}

func TestReconnectReplayOfResolvedRequestEmitsNoApproval(t *testing.T) {
	d, srv, _ := newHostTestDriver(t, true, 10)
	replayOnAttach(srv, permissionFrame("server-7"))
	// session/read answers runtime.pendingRequestIds: [] (the default snapshot)
	// right away: the replayed frame was registered from the read loop ahead of
	// that response, so no timing gap is needed for reconciliation to see it.
	conv, err := d.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-1", ProviderConversationID: "sess-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	defer func() { _ = conv.Close() }()
	activateLive(t, conv, "")

	reply := srv.awaitReply("server-7")
	if reply.Error == nil || reply.Error.Code != zcodeproto.CodeMethodNotFound {
		t.Fatalf("replayed request reply = %+v, want -32601", reply)
	}
	if got := drainEvents(conv.Events(), 150*time.Millisecond); countKind(got, ports.ChatEventApprovalRequested) != 0 {
		t.Fatalf("stale replay became an approval: %+v", got)
	}
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "deny"}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("ResolveRequest = %v", err)
	}
	// A re-announce of the refused request is answered from memory, not parked.
	pushPermission(srv, "server-9")
	if late := srv.awaitReply("server-9"); late.Error == nil || late.Error.Code != zcodeproto.CodeMethodNotFound {
		t.Fatalf("re-announce after reconciliation = %+v, want the remembered refusal", late)
	}
	if got := drainEvents(conv.Events(), 100*time.Millisecond); countKind(got, ports.ChatEventApprovalRequested) != 0 {
		t.Fatalf("re-announce after reconciliation became an approval: %+v", got)
	}
}

func TestReconnectReplayOfStillPendingRequestIsEmittedOnce(t *testing.T) {
	d, srv, _ := newHostTestDriver(t, true, 10)
	replayOnAttach(srv, permissionFrame("server-7"), permissionFrame("server-8"))
	srv.respondTo(zcodeproto.MethodSessionRead, strings.Replace(testSnapshot, `"pendingRequestIds":[]`, `"pendingRequestIds":["r1"]`, 1))
	conv, err := d.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-1", ProviderConversationID: "sess-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	defer func() { _ = conv.Close() }()
	activateLive(t, conv, "")
	got := drainEvents(conv.Events(), 200*time.Millisecond)
	if countKind(got, ports.ChatEventApprovalRequested) != 1 {
		t.Fatalf("approval.requested count = %d: %+v", countKind(got, ports.ChatEventApprovalRequested), got)
	}
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "allow-once"}); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}
	srv.awaitReply("server-7")
	srv.awaitReply("server-8")
}

func TestApprovalWaitExpiryRefusesEveryWaitingID(t *testing.T) {
	conv, srv := startConversation(t)
	conv.approvalWait = 100 * time.Millisecond
	pushPermission(srv, "server-3")
	nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	pushPermission(srv, "server-4")

	for _, id := range []string{"server-3", "server-4"} {
		reply := srv.awaitReply(id)
		if reply.Error == nil || reply.Error.Code != zcodeproto.CodeMethodNotFound {
			t.Fatalf("reply to %s = %+v, want a -32601 refusal", id, reply)
		}
	}
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "deny"}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("ResolveRequest after expiry = %v", err)
	}
	// A re-announce after expiry gets the same refusal, not a new card.
	pushPermission(srv, "server-5")
	if late := srv.awaitReply("server-5"); late.Error == nil || late.Error.Code != zcodeproto.CodeMethodNotFound {
		t.Fatalf("re-announce after expiry = %+v, want the remembered refusal", late)
	}
	if got := drainEvents(conv.Events(), 100*time.Millisecond); countKind(got, ports.ChatEventApprovalRequested) != 0 {
		t.Fatalf("re-announce after expiry became an approval: %+v", got)
	}
}

func TestParkedRequestBeforeAnyStartSignalUsesTheArmedTurn(t *testing.T) {
	conv, srv := startConversation(t)
	// session/send is acknowledged but no start signal has been pumped: the
	// request's turnId is unknown to the tracker and nothing is open yet.
	id := sendTurn(t, conv, "hello")
	pushPermission(srv, "server-3")
	requested := nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	if requested.ProviderTurnID != id {
		t.Fatalf("approval turn = %q, want the armed %q (DESIGN 3.5: the current pending turn)", requested.ProviderTurnID, id)
	}
}

func TestResolveWithCancelledContextLeavesTheRequestPending(t *testing.T) {
	conv, srv := startConversation(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	pushPermission(srv, "server-3")
	nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	if err := conv.ResolveRequest(cancelled, "r1", ports.ChatDecision{ID: "allow-once"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveRequest with a cancelled context = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if srv.countFrames(func(f frame) bool { return f.Method == "" && f.ID != nil && rawID(*f.ID) == "server-3" }) != 0 {
		t.Fatal("a failed ResolveRequest delivered the decision anyway")
	}
	// The real answer lands and reports success: delivered means delivered.
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "allow-once"}); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}
	srv.awaitReply("server-3")

	srv.push(`{"id":"server-6","method":"interaction/requestUserInput","params":` + userInputParams + `}`)
	nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
	if err := conv.ResolveInput(cancelled, "q1", ports.ChatInputResponse{Action: ports.ChatInputActionAccept}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveInput with a cancelled context = %v", err)
	}
	if err := conv.ResolveInput(context.Background(), "q1", ports.ChatInputResponse{Action: ports.ChatInputActionAccept}); err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	srv.awaitReply("server-6")
}

func TestStructuredInputOnAPermissionRequestIsRefusedAndLeftPending(t *testing.T) {
	conv, srv := startConversation(t)
	pushPermission(srv, "server-3")
	nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	err := conv.ResolveInput(context.Background(), "r1", ports.ChatInputResponse{Action: ports.ChatInputActionAccept})
	if !errors.Is(err, ports.ErrChatDecisionNotOffered) || errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("ResolveInput on a permission request = %v, want ErrChatDecisionNotOffered (it is still pending)", err)
	}
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "allow-once"}); err != nil {
		t.Fatalf("the real answer was refused: %v", err)
	}
	srv.awaitReply("server-3")
}

func TestHeaderKeyedInputAnswersAreWrappedAsAnswers(t *testing.T) {
	conv, srv := startConversation(t)
	srv.push(`{"id":"server-6","method":"interaction/requestUserInput","params":` + userInputParams + `}`)
	nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
	// A generic AO client answers the published schema, keyed by header.
	if err := conv.ResolveInput(context.Background(), "q1", ports.ChatInputResponse{
		Action: ports.ChatInputActionAccept, Content: map[string]any{"proceed": "yes"},
	}); err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	reply := srv.awaitReply("server-6")
	if !jsonEqual(reply.Result, json.RawMessage(`{"action":"accept","content":{"answers":{"Proceed?":"yes"}}}`)) {
		t.Fatalf("reply = %s, want the U6 static shape keyed by question text", reply.Result)
	}
}

func TestUserInputReplyContent(t *testing.T) {
	questions := []zcodeproto.Question{
		{Question: "Which?", Header: "which"},
		{Question: "Why?"},
		{Header: "", Question: ""},
	}
	cases := []struct {
		name    string
		content map[string]any
		want    map[string]any
	}{
		{"already shaped", map[string]any{"answers": map[string]any{"Which?": "a"}}, map[string]any{"answers": map[string]any{"Which?": "a"}}},
		{"header and text keys", map[string]any{"which": "a", "Why?": "b", "answer_2": "c"},
			map[string]any{"answers": map[string]any{"Which?": "a", "Why?": "b", "answer_2": "c"}}},
		{"unknown keys pass through", map[string]any{"extra": 1}, map[string]any{"answers": map[string]any{"extra": 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := userInputReplyContent(questions, tc.content); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("userInputReplyContent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDecisionNotOfferedIsRefusedAndLeavesTheRequestPending(t *testing.T) {
	conv, srv := startConversation(t)
	pushPermission(srv, "server-3")
	nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "accept"})
	if !errors.Is(err, ports.ErrChatDecisionNotOffered) {
		t.Fatalf("err = %v, want ErrChatDecisionNotOffered", err)
	}
	time.Sleep(50 * time.Millisecond)
	if srv.countFrames(func(f frame) bool { return f.Method == "" && f.ID != nil && rawID(*f.ID) == "server-3" }) != 0 {
		t.Fatal("a refused decision consumed the request")
	}
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "allow-once"}); err != nil {
		t.Fatalf("the real answer was refused: %v", err)
	}
	srv.awaitReply("server-3")
}

func TestRequestNotPending(t *testing.T) {
	conv, _ := startConversation(t)
	if err := conv.ResolveRequest(context.Background(), "nope", ports.ChatDecision{ID: "allow-once"}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("err = %v, want ErrChatRequestNotPending", err)
	}
	if err := conv.ResolveInput(context.Background(), "nope", ports.ChatInputResponse{Action: ports.ChatInputActionAccept}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("ResolveInput err = %v, want ErrChatRequestNotPending", err)
	}
}

const userInputParams = `{"requestId":"q1","sessionId":"sess-1","kind":"askUserQuestion","questions":[{"question":"Proceed?","header":"proceed","options":[{"value":"yes","label":"Yes"},{"value":"no","label":"No"}]}]}`

func TestUserInputIsParkedAndAnsweredWithActionAndContent(t *testing.T) {
	conv, srv := startConversation(t)
	srv.push(`{"id":"server-6","method":"interaction/requestUserInput","params":` + userInputParams + `}`)
	requested := nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
	if requested.RequestID != "q1" || requested.Input == nil || requested.Input.Mode != ports.ChatInputModeForm {
		t.Fatalf("input.requested = %+v", requested)
	}
	if requested.Input.Message != "Proceed?" || requested.Input.ElicitationID != "q1" {
		t.Fatalf("input request = %+v", requested.Input)
	}
	properties := requested.Input.Schema["properties"].(map[string]any)
	proceed := properties["proceed"].(map[string]any)
	if enum, _ := proceed["enum"].([]any); len(enum) != 2 || enum[0] != "yes" {
		t.Fatalf("schema = %+v", requested.Input.Schema)
	}
	if err := conv.ResolveRequest(context.Background(), "q1", ports.ChatDecision{ID: "yes"}); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
		t.Fatalf("a decision answered a user-input request: %v", err)
	}
	if err := conv.ResolveInput(context.Background(), "q1", ports.ChatInputResponse{
		Action: ports.ChatInputActionAccept, Content: map[string]any{"answers": map[string]any{"Proceed?": "yes"}},
	}); err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	reply := srv.awaitReply("server-6")
	if !jsonEqual(reply.Result, json.RawMessage(`{"action":"accept","content":{"answers":{"Proceed?":"yes"}}}`)) {
		t.Fatalf("reply = %s", reply.Result)
	}
	srv.push(`{"method":"session/event","params":{"type":"userInput.resolved","payload":{"requestId":"q1","action":"accept"}}}`)
	resolved := nextEvent(t, conv.Events(), ports.ChatEventInputResolved)
	if resolved.RequestID != "q1" {
		t.Fatalf("input.resolved = %+v", resolved)
	}
}

// The captured user-input exchange (p6-user-input.ndjson): the request is
// re-announced once, AO's accept reply matches the capture byte for byte, and
// the server reports the resolution as permission.resolved decision:"modify"
// (there is no userInput.resolved on the wire), which must clear the parked
// request and read as input.resolved for the row AO shows.
func TestUserInputFixtureResolvesThroughPermissionResolved(t *testing.T) {
	conv, srv := startConversation(t)
	lines := loadReplay(t, "p6-user-input.ndjson")
	var requests []replayLine
	var captured, resolved frame
	for _, line := range lines {
		switch {
		case line.Dir == "in" && line.Frame.Method == zcodeproto.MethodInteractionRequestUserInput:
			requests = append(requests, line)
		case line.Dir == "out" && line.Frame.Method == "" && line.Frame.ID != nil && rawID(*line.Frame.ID) == "server-95":
			captured = line.Frame
		case line.Dir == "in" && line.Frame.Method == zcodeproto.MethodSessionEvent && strings.Contains(string(line.Frame.Params), `"permission.resolved"`):
			resolved = line.Frame
		}
	}
	if len(requests) != 2 || captured.Result == nil || resolved.Params == nil {
		t.Fatalf("fixture lacks the exchange: %d requests", len(requests))
	}
	for _, line := range requests {
		srv.push(marshalFrame(t, line.Frame))
	}
	requested := nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
	if requested.RequestID != "perm_89bfeb2e-6979-4b36-8e19-3de57f0696d2" || requested.Input == nil {
		t.Fatalf("input.requested = %+v", requested)
	}
	if got := drainEvents(conv.Events(), 100*time.Millisecond); countKind(got, ports.ChatEventInputRequested) != 0 {
		t.Fatalf("the re-announce produced a second input request: %+v", got)
	}
	if err := conv.ResolveInput(context.Background(), requested.RequestID, ports.ChatInputResponse{
		Action: ports.ChatInputActionAccept, Content: map[string]any{"Proceed": "yes"},
	}); err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	for _, id := range []string{"server-95", "server-96"} {
		if reply := srv.awaitReply(id); !jsonEqual(reply.Result, captured.Result) {
			t.Fatalf("reply to %s = %s, capture has %s", id, reply.Result, captured.Result)
		}
	}
	srv.push(marshalFrame(t, resolved))
	got := nextEvent(t, conv.Events(), ports.ChatEventInputResolved)
	if got.RequestID != requested.RequestID {
		t.Fatalf("input.resolved = %+v", got)
	}
	if err := conv.ResolveInput(context.Background(), requested.RequestID, ports.ChatInputResponse{Action: ports.ChatInputActionAccept}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("ResolveInput after the resolution = %v", err)
	}
}

// Every re-announced id of one request gets the identical deny reply and the
// server reports exactly one resolution (p5-approval-deny-every-id.ndjson).
func TestApprovalDenyFixtureAnswersEveryAnnouncedID(t *testing.T) {
	conv, srv := startConversation(t)
	var ids []string
	var denies []frame
	for _, line := range loadReplay(t, "p5-approval-deny-every-id.ndjson") {
		switch {
		case line.Dir == "in" && line.Frame.Method == zcodeproto.MethodInteractionRequestPermission:
			ids = append(ids, rawID(*line.Frame.ID))
			srv.push(marshalFrame(t, line.Frame))
		case line.Dir == "out" && line.Frame.Method == "" && line.Frame.ID != nil:
			denies = append(denies, line.Frame)
		}
	}
	if len(ids) != 3 || len(denies) != 3 {
		t.Fatalf("fixture: %d announcements, %d replies", len(ids), len(denies))
	}
	requested := nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	if got := drainEvents(conv.Events(), 100*time.Millisecond); countKind(got, ports.ChatEventApprovalRequested) != 0 {
		t.Fatalf("re-announces produced extra approvals: %+v", got)
	}
	if err := conv.ResolveRequest(context.Background(), requested.RequestID, ports.ChatDecision{ID: "deny"}); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}
	for i, id := range ids {
		reply := srv.awaitReply(id)
		if reply.Error != nil || !jsonEqual(reply.Result, denies[i].Result) {
			t.Fatalf("reply to %s = %+v, capture has %s", id, reply, denies[i].Result)
		}
	}
}

func TestCloseCancelsParkedUserInput(t *testing.T) {
	conv, srv := startConversation(t)
	srv.push(`{"id":"server-6","method":"interaction/requestUserInput","params":` + userInputParams + `}`)
	nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
	// Close on the direct path fails parked requests before stopping the process.
	closed := make(chan struct{})
	go func() { _ = conv.Close(); close(closed) }()
	reply := srv.awaitReply("server-6")
	if !jsonEqual(reply.Result, json.RawMessage(`{"action":"cancel"}`)) {
		t.Fatalf("reply = %+v, want {action:cancel}", reply)
	}
	<-closed
}

func TestRuntimePreferencesIsNeverRefused(t *testing.T) {
	conv, srv := startConversation(t)
	srv.push(`{"id":"server-2","method":"session/requestRuntimePreferences","params":{"sessionId":"sess-1"}}`)
	reply := srv.awaitReply("server-2")
	if reply.Error != nil {
		t.Fatalf("runtime preferences refused: %+v", reply.Error)
	}
	if want := `{"nativeSearchEnhancementsEnabled":true,"askUserQuestionAutoResolutionEnabled":false}`; string(reply.Result) != want {
		t.Fatalf("reply = %s, want %s", reply.Result, want)
	}
	// Malformed params still get the same answer: the server fails create on
	// anything but -32601/-32020.
	srv.push(`{"id":"server-3","method":"session/requestRuntimePreferences","params":"not an object"}`)
	if reply := srv.awaitReply("server-3"); reply.Error != nil {
		t.Fatalf("malformed preferences request refused: %+v", reply.Error)
	}
	_ = conv
}

func TestOfficialMcpAuthIsDeclinedWith32001(t *testing.T) {
	_, srv := startConversation(t)
	srv.push(`{"id":"server-8","method":"interaction/requestOfficialMcpAuthHeaders","params":{"sessionId":"sess-1","serverId":"official"}}`)
	reply := srv.awaitReply("server-8")
	if reply.Error == nil || reply.Error.Code != zcodeproto.CodeClientDeclined {
		t.Fatalf("reply = %+v, want -32001", reply)
	}
	if reply.Error.Message != "declined by agent-orchestrator" {
		t.Fatalf("message = %q", reply.Error.Message)
	}
}

func TestUnmodelledServerRequestIsRefusedWith32601(t *testing.T) {
	_, srv := startConversation(t)
	srv.push(`{"id":"server-11","method":"v4/desktop/thing","params":{}}`)
	reply := srv.awaitReply("server-11")
	if reply.Error == nil || reply.Error.Code != zcodeproto.CodeMethodNotFound {
		t.Fatalf("reply = %+v, want -32601", reply)
	}
	srv.push(`{"id":"server-12","method":"subagents/readRuntimeConfig","params":{}}`)
	if reply := srv.awaitReply("server-12"); !jsonEqual(reply.Result, json.RawMessage(`{"kind":"built-in-fallback"}`)) {
		t.Fatalf("subagents reply = %+v", reply)
	}
}

type fakeHeadersResolver struct {
	seen zcodeproto.RequestProviderRuntimeHeadersParams
}

func (r *fakeHeadersResolver) Resolve(_ context.Context, params zcodeproto.RequestProviderRuntimeHeadersParams) (zcodeproto.RequestProviderRuntimeHeadersResult, error) {
	r.seen = params
	return zcodeproto.RequestProviderRuntimeHeadersResult{HeadersApplied: true, RequestAuth: json.RawMessage(`{"apiKey":"test-not-a-secret"}`)}, nil
}

func TestProviderRuntimeHeadersUsesResolver(t *testing.T) {
	request := `{"id":"server-20","method":"interaction/requestProviderRuntimeHeaders","params":{"sessionId":"sess-1","providerId":"account:zai-individual-coding-plan","reason":"model_request"}}`

	t.Run("decline by default", func(t *testing.T) {
		_, srv := startConversation(t)
		srv.push(request)
		reply := srv.awaitReply("server-20")
		if reply.Error == nil || reply.Error.Code != zcodeproto.CodeMethodNotFound {
			t.Fatalf("reply = %+v, want -32601", reply)
		}
	})

	t.Run("resolver result forwarded verbatim", func(t *testing.T) {
		d, srv := newTestDriver(t)
		resolver := &fakeHeadersResolver{}
		d.runtimeHeaders = resolver
		conv, err := d.Start(context.Background(), startConfig(t))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = conv.Close() }()
		srv.push(request)
		reply := srv.awaitReply("server-20")
		if !jsonEqual(reply.Result, json.RawMessage(`{"headersApplied":true,"requestAuth":{"apiKey":"test-not-a-secret"}}`)) {
			t.Fatalf("reply = %s", reply.Result)
		}
		if resolver.seen.ProviderID != "account:zai-individual-coding-plan" || resolver.seen.Reason != "model_request" {
			t.Fatalf("resolver saw %+v", resolver.seen)
		}
	})
}

func TestPermissionResolvedWithoutRequestIDResolvesByToolCallID(t *testing.T) {
	conv, srv := startConversation(t)
	pushPermission(srv, "server-3")
	nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	srv.push(`{"method":"session/event","params":{"type":"permission.resolved","payload":{"toolCallId":"tc1","toolName":"Bash","decision":"allow"}}}`)
	resolved := nextEvent(t, conv.Events(), ports.ChatEventApprovalResolved)
	if resolved.RequestID != "r1" {
		t.Fatalf("approval.resolved RequestID = %q, want r1 via the toolCallId index", resolved.RequestID)
	}
	reply := srv.awaitReply("server-3")
	if reply.Error == nil {
		t.Fatalf("parked frame answered with a decision AO never made: %+v", reply)
	}
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "deny"}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("ResolveRequest = %v, want not pending", err)
	}
}

func TestParkedRequestsAreScopedToTheTurn(t *testing.T) {
	conv, srv := startConversation(t)
	id := sendTurn(t, conv, "hello")
	srv.push(`{"method":"computer-use/operation-event","params":{"kind":"turn-started","turnId":"t-native","sessionId":"sess-1"}}`)
	nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	pushPermission(srv, "server-3")
	requested := nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	if requested.ProviderTurnID != id {
		t.Fatalf("approval turn = %q, want %q (native t-native mapped)", requested.ProviderTurnID, id)
	}
}

func TestServerRequestLoggingNeverIncludesParams(t *testing.T) {
	// The runtime-headers decline logs only the reason: the params name the
	// account and the reply carries credentials.
	var buf strings.Builder
	d, srv := newTestDriver(t)
	d.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	conv, err := d.Start(context.Background(), startConfig(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()
	srv.push(`{"id":"server-20","method":"interaction/requestProviderRuntimeHeaders","params":{"providerId":"acct:secret-plan","reason":"model_request","accountAccess":{"token":"SHOULD-NOT-LOG"}}}`)
	srv.awaitReply("server-20")
	if strings.Contains(buf.String(), "SHOULD-NOT-LOG") || strings.Contains(buf.String(), "secret-plan") {
		t.Fatalf("log leaked request params: %s", buf.String())
	}
}
