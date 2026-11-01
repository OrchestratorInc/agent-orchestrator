package codexappserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const inputParams = `{"threadId":"thread-1","turnId":"turn-1","itemId":"tool-1","questions":[{"id":"first_value","header":"Direction","question":"Which direction should we take?","options":[{"label":"Native","description":"Use the native protocol"},{"label":"Bridge","description":"Keep the bridge"}]}]}`

func TestUserInputProtocolRoundTrip(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	srv.push(`{"id":0,"method":"item/tool/requestUserInput","params":` + inputParams + `}`)
	var ev ports.ChatEvent
	select {
	case ev = <-conv.Events():
	case <-time.After(5 * time.Second):
		t.Fatal("Codex did not surface the question")
	}
	if ev.Kind != ports.ChatEventInputRequested {
		t.Fatalf("question was not structured input: %#v", ev)
	}
	responder, ok := conv.(ports.ChatInputResponder)
	if !ok || !conv.Capabilities().Has(ports.ChatCapabilityElicitation) {
		t.Fatal("Codex does not support structured input")
	}
	if ev.RequestID != "0" || ev.ProviderTurnID != "turn-1" || ev.ActivityKind != domain.ActivityKindUserInput || ev.Input == nil {
		t.Fatalf("input event = %#v", ev)
	}
	if ev.Summary != "Which direction should we take?" || ev.Input.Message != ev.Summary || len(ev.Decisions) != 0 {
		t.Fatalf("input event = %#v", ev)
	}
	properties := ev.Input.Schema["properties"].(map[string]any)
	property := properties["first_value"].(map[string]any)
	if property["title"] != "Direction" || property["description"] != ev.Summary {
		t.Fatalf("question metadata = %#v", property)
	}
	options := property["oneOf"].([]any)
	if !reflect.DeepEqual(options[0], map[string]any{"const": "Native", "title": "Native", "description": "Use the native protocol"}) {
		t.Fatalf("options = %#v", options)
	}
	fixtureBytes, err := os.ReadFile("testdata/user_input.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Params json.RawMessage `json:"params"`
		Detail struct {
			Mode    ports.ChatInputMode `json:"inputMode"`
			Message string              `json:"message"`
			Schema  map[string]any      `json:"schema"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatal(err)
	}
	actual, _ := json.Marshal(ev.Input.Schema)
	wantSchema, _ := json.Marshal(fixture.Detail.Schema)
	var actualParams, fixtureParams any
	_ = json.Unmarshal([]byte(inputParams), &actualParams)
	_ = json.Unmarshal(fixture.Params, &fixtureParams)
	if string(actual) != string(wantSchema) || ev.Input.Message != fixture.Detail.Message || ev.Input.Mode != fixture.Detail.Mode || !reflect.DeepEqual(actualParams, fixtureParams) {
		t.Fatal("Codex input no longer matches the real frontend regression fixture")
	}
	srv.mu.Lock()
	for _, f := range srv.seen {
		if f.ID != nil && string(*f.ID) == "0" {
			t.Error("input was answered without the user")
		}
	}
	srv.mu.Unlock()
	if err := conv.ResolveRequest(context.Background(), ev.RequestID, ports.ChatDecision{ID: "accept"}); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
		t.Fatalf("approval answered a question: %v", err)
	}
	for _, content := range []map[string]any{nil, {"first_value": "invented"}, {"first_value": true}, {"first_value": "Native", "unknown": "value"}} {
		if err := responder.ResolveInput(context.Background(), ev.RequestID, ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: content}); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
			t.Fatalf("invalid content accepted: %v", err)
		}
	}
	if err := responder.ResolveInput(context.Background(), ev.RequestID, ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"first_value": "Bridge"}}); err != nil {
		t.Fatal(err)
	}
	reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == "0" && f.Method == "" })
	var payload map[string]any
	if err := json.Unmarshal(reply.Result, &payload); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"answers": map[string]any{"first_value": map[string]any{"answers": []any{"Bridge"}}}}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("native answer = %s", reply.Result)
	}
	if err := responder.ResolveInput(context.Background(), ev.RequestID, ports.ChatInputResponse{Action: ports.ChatInputActionCancel}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("stale question resolved: %v", err)
	}
}

func TestUserInputOtherAndFreeText(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	params := strings.Replace(inputParams, `"header":"Direction"`, `"isOther":true,"header":"Direction"`, 1)
	params = strings.TrimSuffix(params, `]}`) + `,{"id":"details","header":"Details","question":"Anything else?"},{"id":"codex_other_0","header":"Collision","question":"Keep the id?"}]}`
	srv.push(`{"id":"other","method":"item/tool/requestUserInput","params":` + params + `}`)
	ev := nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
	properties := ev.Input.Schema["properties"].(map[string]any)
	if properties["codex_other_0_"] == nil || properties["codex_other_0"] == nil {
		t.Fatalf("custom field collides with native question id: %#v", properties)
	}
	responder := conv.(ports.ChatInputResponder)
	if err := responder.ResolveInput(context.Background(), ev.RequestID, ports.ChatInputResponse{
		Action: ports.ChatInputActionAccept, Content: map[string]any{"codex_other_0_": "SDK wrapper", "details": "Include snapshots", "codex_other_0": "yes"},
	}); err != nil {
		t.Fatal(err)
	}
	reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == `"other"` && f.Method == "" })
	var payload userInputReply
	if err := json.Unmarshal(reply.Result, &payload); err != nil {
		t.Fatal(err)
	}
	want := map[string]userInputAnswer{
		"first_value":   {Answers: []string{"user_note: SDK wrapper"}},
		"details":       {Answers: []string{"user_note: Include snapshots"}},
		"codex_other_0": {Answers: []string{"user_note: yes"}},
	}
	if !reflect.DeepEqual(payload.Answers, want) {
		t.Fatalf("native answers = %s", reply.Result)
	}
}

func TestUserInputHeaderFallback(t *testing.T) {
	for _, header := range []struct {
		name, field, title string
	}{
		{"missing", "", "Which direction should we take?"},
		{"empty", `"header":"",`, "Which direction should we take?"},
		{"whitespace", `"header":" \t ",`, "Which direction should we take?"},
		{"null", `"header":null,`, "Which direction should we take?"},
		{"nonblank preserved", `"header":"  Direction  ",`, "  Direction  "},
	} {
		for name, other := range map[string]bool{"choice": false, "other": true} {
			t.Run(header.name+"/"+name, func(t *testing.T) {
				d, srv := newTestDriver(t)
				conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = conv.Close() }()
				params := strings.Replace(inputParams, `"header":"Direction",`, header.field, 1)
				if other {
					params = strings.Replace(params, `"options":`, `"isOther":true,"options":`, 1)
				}
				srv.push(`{"id":"header","method":"item/tool/requestUserInput","params":` + params + `}`)
				ev := nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
				if ev.Input == nil || ev.Input.Mode != ports.ChatInputModeForm || ev.Input.Message != "Which direction should we take?" || len(ev.Decisions) != 0 {
					t.Fatalf("question was not a real form: %#v", ev)
				}
				properties := ev.Input.Schema["properties"].(map[string]any)
				property := properties["first_value"].(map[string]any)
				if property["title"] != header.title || property["description"] != ev.Input.Message {
					t.Fatalf("question metadata = %#v", property)
				}
				content := map[string]any{"first_value": "Bridge"}
				answers := []string{"Bridge"}
				if other {
					custom := properties["codex_other_0"].(map[string]any)
					if custom["title"] != header.title+" — other answer or additional details" || custom["description"] != ev.Input.Message {
						t.Fatalf("other metadata = %#v", custom)
					}
					content["codex_other_0"] = "More context"
					answers = append(answers, "user_note: More context")
				}
				if err := conv.(ports.ChatInputResponder).ResolveInput(context.Background(), ev.RequestID, ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: content}); err != nil {
					t.Fatal(err)
				}
				reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == `"header"` && f.Method == "" })
				var payload userInputReply
				if err := json.Unmarshal(reply.Result, &payload); err != nil {
					t.Fatal(err)
				}
				if reply.Error != nil || !reflect.DeepEqual(payload.Answers, map[string]userInputAnswer{"first_value": {Answers: answers}}) {
					t.Fatalf("native answer = %s, error = %#v", reply.Result, reply.Error)
				}
			})
		}
	}
}

func TestUserInputRefusesMalformedAndSecretRequests(t *testing.T) {
	for name, params := range map[string]string{
		"secret":            strings.Replace(inputParams, `"header":"Direction"`, `"isSecret":true,"header":"Direction"`, 1),
		"old prompt field":  strings.Replace(inputParams, `"question":`, `"prompt":`, 1),
		"boolean header":    strings.Replace(inputParams, `"header":"Direction"`, `"header":false`, 1),
		"numeric header":    strings.Replace(inputParams, `"header":"Direction"`, `"header":42`, 1),
		"object header":     strings.Replace(inputParams, `"header":"Direction"`, `"header":{}`, 1),
		"array header":      strings.Replace(inputParams, `"header":"Direction"`, `"header":[]`, 1),
		"blank question id": strings.Replace(inputParams, `"id":"first_value"`, `"id":" "`, 1),
		"missing thread":    strings.Replace(inputParams, `"threadId":"thread-1",`, "", 1),
		"missing turn":      strings.Replace(inputParams, `"turnId":"turn-1",`, "", 1),
		"missing item":      strings.Replace(inputParams, `"itemId":"tool-1",`, "", 1),
		"missing question":  strings.Replace(inputParams, "Which direction should we take?", "", 1),
		"empty questions":   `{"threadId":"thread-1","turnId":"turn-1","itemId":"tool-1","questions":[]}`,
		"duplicate id":      strings.TrimSuffix(inputParams, `]}`) + `,{"id":"first_value","header":"Again","question":"Again?"}]}`,
		"duplicate label":   strings.Replace(inputParams, `"label":"Bridge"`, `"label":"Native"`, 1),
		"invalid options":   strings.Replace(inputParams, `"options":[`, `"options":{},"ignored":[`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			d, srv := newTestDriver(t)
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			srv.push(`{"id":15,"method":"item/tool/requestUserInput","params":` + params + `}`)
			reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == "15" && f.Method == "" })
			if reply.Error == nil || !strings.Contains(reply.Error.Message, "request_user_input") {
				t.Fatalf("unsafe payload was not clearly refused: %#v", reply)
			}
			for len(conv.Events()) > 0 {
				ev := <-conv.Events()
				if ev.Kind == ports.ChatEventInputRequested || ev.Kind == ports.ChatEventApprovalRequested {
					t.Fatalf("unsupported input was exposed to history/UI: %#v", ev)
				}
			}
		})
	}
}

func TestUserInputAcceptsAdditiveFields(t *testing.T) {
	_, expected, err := parseUserInput(json.RawMessage(inputParams))
	if err != nil {
		t.Fatal(err)
	}
	for name, params := range map[string]string{
		"top level":  strings.Replace(inputParams, `"threadId":`, `"futureContext":{"nested":[true,null,"extra"]},"threadId":`, 1),
		"question":   strings.Replace(inputParams, `"header":"Direction"`, `"multiSelect":true,"header":"Direction"`, 1),
		"option":     strings.Replace(inputParams, `"label":"Native"`, `"id":"option-id","label":"Native"`, 1),
		"all levels": `{"threadId":"thread-1","turnId":"turn-1","itemId":"tool-1","futureContext":{},"questions":[{"id":"first_value","header":"Direction","question":"Which direction should we take?","futureQuestion":[],"options":[{"label":"Native","description":"Use the native protocol","futureOption":null},{"label":"Bridge","description":"Keep the bridge"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, input, err := parseUserInput(json.RawMessage(params))
			if err != nil {
				t.Fatalf("additive fields rejected: %v", err)
			}
			if !reflect.DeepEqual(input, expected) {
				t.Fatalf("additive fields changed the known form: %#v", input)
			}
			d, srv := newTestDriver(t)
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			srv.push(`{"id":"additive","method":"item/tool/requestUserInput","params":` + params + `}`)
			ev := nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
			if ev.Input == nil || !reflect.DeepEqual(*ev.Input, expected) || ev.ActivityKind != domain.ActivityKindUserInput {
				t.Fatalf("question was not the known structured form: %#v", ev)
			}
			if err := conv.(ports.ChatInputResponder).ResolveInput(context.Background(), ev.RequestID, ports.ChatInputResponse{
				Action: ports.ChatInputActionAccept, Content: map[string]any{"first_value": "Native"},
			}); err != nil {
				t.Fatal(err)
			}
			reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == `"additive"` && f.Method == "" })
			if reply.Error != nil || string(reply.Result) != `{"answers":{"first_value":{"answers":["Native"]}}}` {
				t.Fatalf("native answer = %s, error = %#v", reply.Result, reply.Error)
			}
		})
	}
}

func TestUserInputRefusalDiagnosticsDoNotLeakPayload(t *testing.T) {
	const canary = "private-payload-canary"
	private := strings.ReplaceAll(inputParams, "Direction", canary+"-header")
	private = strings.ReplaceAll(private, "Which direction should we take?", canary+"-question")
	private = strings.ReplaceAll(private, "Native", canary+"-option")
	for name, params := range map[string]string{
		"secret":           strings.Replace(private, `"header":`, `"isSecret":true,"header":`, 1),
		"missing question": strings.Replace(private, `"question":`, `"`+canary+`-unknown":`, 1),
		"invalid header":   strings.Replace(private, `"header":`, `"header":false,"ignoredHeader":`, 1),
		"invalid options":  strings.Replace(private, `"options":[`, `"options":false,"ignored":[`, 1),
		"malformed JSON":   strings.TrimSuffix(private, "}"),
		"trailing data":    private + ` {"answer":"` + canary + `-answer"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			c := &conversation{log: slog.New(slog.NewJSONHandler(&logs, nil)), events: make(chan ports.ChatEvent, eventBuffer)}
			_, err := c.requestUserInput(context.Background(), serverRequest{
				ID: json.RawMessage(`"diagnostic-request"`), Method: codexproto.MethodItemToolRequestUserInput, Params: json.RawMessage(params),
			})
			if err == nil {
				t.Fatal("invalid request accepted")
			}
			if len(c.pendingInputs) != 0 || len(c.events) != 0 {
				t.Fatal("invalid input was parked or exposed to history")
			}
			if strings.Contains(logs.String(), canary) || strings.Contains(err.Error(), canary) {
				t.Fatal("diagnostic leaked request contents")
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("missing refusal diagnostic: %v", err)
			}
			if entry["level"] != "WARN" || entry["msg"] != "refusing invalid request_user_input payload" || entry["request_id"] != "diagnostic-request" {
				t.Fatalf("unexpected refusal diagnostic: %#v", entry)
			}
			if entry["error"] != err.Error() || strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("refusal diagnostic omitted its sanitized reason: %#v", entry)
			}
			if len(entry) != 5 {
				t.Fatalf("refusal diagnostic contains unexpected fields: %#v", entry)
			}
		})
	}
}

func TestUserInputLifecycle(t *testing.T) {
	for _, mode := range []string{"cancel", "decline", "provider", "timeout", "close", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			d, srv := newTestDriver(t)
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			params := inputParams
			if mode == "timeout" {
				params = strings.Replace(params, `"itemId":`, `"autoResolutionMs":20,"itemId":`, 1)
			}
			srv.push(`{"id":"lifecycle","method":"item/tool/requestUserInput","params":` + params + `}`)
			ev := nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
			responder := conv.(ports.ChatInputResponder)
			switch mode {
			case "cancel", "decline":
				err = responder.ResolveInput(context.Background(), ev.RequestID, ports.ChatInputResponse{Action: ports.ChatInputAction(mode)})
			case "provider":
				srv.push(`{"method":"serverRequest/resolved","params":{"requestId":"lifecycle"}}`)
				nextEvent(t, conv.Events(), ports.ChatEventInputResolved)
			case "timeout":
				nextEvent(t, conv.Events(), ports.ChatEventInputResolved)
			case "close":
				_ = conv.Close()
			case "concurrent":
				var wg sync.WaitGroup
				results := make(chan error, 2)
				for range 2 {
					wg.Go(func() {
						results <- responder.ResolveInput(context.Background(), ev.RequestID, ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"first_value": "Native"}})
					})
				}
				wg.Wait()
				first, second := <-results, <-results
				if (first == nil) == (second == nil) || (first != nil && !errors.Is(first, ports.ErrChatRequestNotPending)) || (second != nil && !errors.Is(second, ports.ErrChatRequestNotPending)) {
					t.Fatalf("concurrent answers = %v / %v", first, second)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode != "close" {
				reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == `"lifecycle"` && f.Method == "" })
				if mode == "provider" || mode == "timeout" {
					if reply.Error == nil {
						t.Fatalf("expired request was answered: %s", reply.Result)
					}
				} else if mode != "concurrent" && string(reply.Result) != `{"answers":{}}` {
					t.Fatalf("cancel/decline sent an answer: %s", reply.Result)
				}
			}
			err = responder.ResolveInput(context.Background(), ev.RequestID, ports.ChatInputResponse{Action: ports.ChatInputActionCancel})
			if !errors.Is(err, ports.ErrChatRequestNotPending) && !errors.Is(err, errConversationClosed) {
				t.Fatalf("stale input resolved: %v", err)
			}
		})
	}
}

func TestUserInputCancelledContextKeepsRequestPending(t *testing.T) {
	parked, _, err := parseUserInput(json.RawMessage(inputParams))
	if err != nil {
		t.Fatal(err)
	}
	c := &conversation{providerScopeID: "scope", pendingInputs: map[string]*parkedInput{"1": parked}, events: make(chan ports.ChatEvent, eventBuffer)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"first_value": "Native"}}
	if err := c.ResolveInput(ctx, c.scopedID("1"), response); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.ResolveInput(context.Background(), "codex:5:other1", response); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("foreign scope answered input: %v", err)
	}
	if err := c.ResolveInput(context.Background(), c.scopedID("1"), response); err != nil {
		t.Fatal(err)
	}
}

func TestUserInputRequestContextCancellation(t *testing.T) {
	c := &conversation{events: make(chan ports.ChatEvent, eventBuffer)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.handleServerRequest(ctx, serverRequest{ID: json.RawMessage(`1`), Method: codexproto.MethodItemToolRequestUserInput, Params: json.RawMessage(inputParams)})
		done <- err
	}()
	nextEvent(t, c.events, ports.ChatEventInputRequested)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("input handler did not unblock on cancellation")
	}
	nextEvent(t, c.events, ports.ChatEventInputResolved)
}

func TestUserInputConcurrentRequestsRemainIndependent(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	for _, id := range []string{"first", "second"} {
		srv.push(`{"id":"` + id + `","method":"item/tool/requestUserInput","params":` + inputParams + `}`)
		nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
	}
	responder := conv.(ports.ChatInputResponder)
	for id, value := range map[string]string{"second": "Bridge", "first": "Native"} {
		if err := responder.ResolveInput(context.Background(), id, ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"first_value": value}}); err != nil {
			t.Fatal(err)
		}
		reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == `"`+id+`"` && f.Method == "" })
		if !strings.Contains(string(reply.Result), `"answers":["`+value+`"]`) {
			t.Fatalf("answer crossed requests: %s", reply.Result)
		}
	}
}

func TestUserInputCloseUnblocksWithoutAnswering(t *testing.T) {
	c := &conversation{events: make(chan ports.ChatEvent, eventBuffer)}
	done := make(chan error, 1)
	go func() {
		_, err := c.handleServerRequest(context.Background(), serverRequest{ID: json.RawMessage(`1`), Method: codexproto.MethodItemToolRequestUserInput, Params: json.RawMessage(inputParams)})
		done <- err
	}()
	nextEvent(t, c.events, ports.ChatEventInputRequested)
	c.failPendingApprovals()
	select {
	case err := <-done:
		if !errors.Is(err, errConversationClosed) {
			t.Fatalf("close sent an answer: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock input handler")
	}
}

func TestUserInputOtherPreservesOptionAndNote(t *testing.T) {
	params := strings.Replace(inputParams, `"header":"Direction"`, `"isOther":true,"header":"Direction"`, 1)
	parked, _, err := parseUserInput(json.RawMessage(params))
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []map[string]any{nil, {"codex_other_0": "  "}, {"codex_other_0": true}, {"first_value": "Native", "codex_other_0": []string{"note"}}} {
		if _, err := parked.reply(ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: content}); err == nil {
			t.Fatalf("invalid other answer was accepted: %#v", content)
		}
	}
	reply, err := parked.reply(ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"first_value": "Native", "codex_other_0": "Include snapshots"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reply.Answers["first_value"].Answers, []string{"Native", "user_note: Include snapshots"}) {
		t.Fatalf("option or additional text lost: %#v", reply)
	}
}
