package zcodeappserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPermissionModeMapping(t *testing.T) {
	cases := []struct {
		mode     ports.PermissionMode
		readOnly bool
		want     string
		wantErr  bool
	}{
		{ports.PermissionModeDefault, false, "edit", false},
		{"", false, "edit", false},
		{"unknown-mode", false, "edit", false},
		{ports.PermissionModeAcceptEdits, false, "edit", false},
		{ports.PermissionModeAuto, false, "build", false},
		{ports.PermissionModeBypassPermissions, false, "yolo", false},
		{ports.PermissionModeBypassPermissions, true, "", true},
		{ports.PermissionModeDefault, true, "", true},
	}
	for _, tc := range cases {
		got, err := sessionMode(tc.mode, tc.readOnly)
		if tc.wantErr {
			if !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
				t.Errorf("sessionMode(%q, readOnly=%v) err = %v, want ErrChatPermissionModeUnsupported", tc.mode, tc.readOnly, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("sessionMode(%q, %v) = %q, %v; want %q", tc.mode, tc.readOnly, got, err, tc.want)
		}
	}

	// Every mode starts: approvals are PROVEN under mode edit
	// (p5-approval-allow.ndjson), so the Phase 0 bypass-only guard is gone.
	// Read-only is still refused (no provider-enforced sandbox).
	workspace := t.TempDir()
	for _, mode := range []ports.PermissionMode{ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions, ""} {
		if err := validateStart(workspace, mode, false); err != nil {
			t.Errorf("validateStart(%q) = %v", mode, err)
		}
	}
	if err := validateStart(workspace, ports.PermissionModeBypassPermissions, true); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Errorf("validateStart(bypass, readOnly) = %v", err)
	}

	// The create carries the mapped mode.
	d, srv := newTestDriver(t)
	cfg := startConfig(t)
	cfg.Permissions = ports.PermissionModeAcceptEdits
	conv, err := d.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start(accept-edits) = %v", err)
	}
	defer func() { _ = conv.Close() }()
	if got := paramsMap(t, srv.awaitRequest(zcodeproto.MethodSessionCreate))["mode"]; got != "edit" {
		t.Fatalf("session/create mode = %v, want edit", got)
	}

	// Read-only refuses before spawning anything.
	d2, srv2 := newTestDriver(t)
	cfg2 := startConfig(t)
	cfg2.ReadOnly = true
	if _, err := d2.Start(context.Background(), cfg2); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Fatalf("Start(readOnly) = %v", err)
	}
	if len(srv2.capturedEnv()) != 0 {
		t.Fatal("a refused mode spawned the provider")
	}
}

func TestPerTurnApprovalMustMatchLaunchMode(t *testing.T) {
	conv, _ := startConversation(t)
	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "x", Settings: ports.ChatTurnSettings{Approval: ports.PermissionModeAcceptEdits},
	}); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Fatalf("SendTurn with a different mode = %v", err)
	}
	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "x", Settings: ports.ChatTurnSettings{Approval: ports.PermissionModeBypassPermissions},
	}); err != nil {
		t.Fatalf("SendTurn with the launch mode = %v", err)
	}
}

func TestDecisionKind(t *testing.T) {
	cases := []struct {
		resp zcodeproto.PermissionResponse
		want ports.ChatDecisionKind
	}{
		{zcodeproto.PermissionResponse{Decision: "allow"}, ports.ChatDecisionAllowOnce},
		{zcodeproto.PermissionResponse{Decision: "allow", PermissionUpdates: []byte(`[{"type":"addRules"}]`)}, ports.ChatDecisionAllowAlways},
		{zcodeproto.PermissionResponse{Decision: "allow", PermissionUpdates: []byte(`null`)}, ports.ChatDecisionAllowOnce},
		{zcodeproto.PermissionResponse{Decision: "deny"}, ports.ChatDecisionRejectOnce},
		{zcodeproto.PermissionResponse{Decision: "deny", PermissionUpdates: []byte(`[{}]`)}, ports.ChatDecisionRejectAlways},
		{zcodeproto.PermissionResponse{Decision: "modify"}, ports.ChatDecisionAllowOnce},
		{zcodeproto.PermissionResponse{Decision: "escalate"}, ports.ChatDecisionAllowOnce},
		{zcodeproto.PermissionResponse{Decision: "???"}, ""},
	}
	for _, tc := range cases {
		if got := decisionKind(tc.resp); got != tc.want {
			t.Errorf("decisionKind(%+v) = %q, want %q", tc.resp, got, tc.want)
		}
	}
}

func TestDecisionOptionsFromTheCapturedRequest(t *testing.T) {
	request := fixtureFrame(t, "p5-approval-allow.ndjson", func(line replayLine) bool {
		return line.Dir == "in" && line.Frame.Method == zcodeproto.MethodInteractionRequestPermission
	})
	var params zcodeproto.RequestPermissionParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	options := decisionOptions(params.Options)
	if len(options) != 3 {
		t.Fatalf("options = %+v", options)
	}
	want := map[string]ports.ChatDecisionKind{"allow_once": ports.ChatDecisionAllowOnce, "allow_project": ports.ChatDecisionAllowAlways, "deny": ports.ChatDecisionRejectOnce}
	for _, option := range options {
		if want[option.ID] != option.Kind {
			t.Errorf("option %s kind = %q, want %q", option.ID, option.Kind, want[option.ID])
		}
	}
	// The captured reply is options[0].response verbatim.
	reply := fixtureFrame(t, "p5-approval-allow.ndjson", func(line replayLine) bool {
		return line.Dir == "out" && line.Frame.Method == "" && line.Frame.ID != nil && rawID(*line.Frame.ID) == "server-95"
	})
	if !jsonEqual(reply.Result, options[0].Raw) {
		t.Fatalf("captured reply %s != options[0].response %s", reply.Result, options[0].Raw)
	}
}

func TestInputRequestFromTheCapturedQuestions(t *testing.T) {
	request := fixtureFrame(t, "p6-user-input.ndjson", func(line replayLine) bool {
		return line.Dir == "in" && line.Frame.Method == zcodeproto.MethodInteractionRequestUserInput
	})
	var params zcodeproto.RequestUserInputParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	req := inputRequestFrom(params)
	if req.Message != "Tool AskUserQuestion requires user interaction" || req.ElicitationID != params.RequestID {
		t.Fatalf("request = %+v", req)
	}
	proceed := req.Schema["properties"].(map[string]any)["Proceed"].(map[string]any)
	if proceed["description"] != "Do you want to proceed?" || len(proceed["enum"].([]any)) != 2 {
		t.Fatalf("schema = %+v", req.Schema)
	}
	// The captured accept reply is what AO's form answer maps onto.
	reply := fixtureFrame(t, "p6-user-input.ndjson", func(line replayLine) bool {
		return line.Dir == "out" && line.Frame.Method == "" && line.Frame.ID != nil && rawID(*line.Frame.ID) == "server-95"
	})
	got, _ := json.Marshal(zcodeproto.UserInputResponse{Action: "accept", Content: userInputReplyContent(params.Questions, map[string]any{"Proceed": "yes"})})
	if !jsonEqual(got, reply.Result) {
		t.Fatalf("reply = %s, capture has %s", got, reply.Result)
	}
}

func TestInputRequestFromQuestions(t *testing.T) {
	req := inputRequestFrom(zcodeproto.RequestUserInputParams{
		RequestID: "q1",
		Questions: []zcodeproto.Question{
			{Question: "Which?", Options: []zcodeproto.QuestionOption{{Value: "a"}, {Value: "b"}}},
			{Question: "Why?", Header: "why"},
		},
	})
	if req.Mode != ports.ChatInputModeForm || req.Message != "Which?" || req.ElicitationID != "q1" {
		t.Fatalf("request = %+v", req)
	}
	properties := req.Schema["properties"].(map[string]any)
	if _, ok := properties["Which?"]; !ok {
		t.Fatalf("question keyed by text missing: %+v", properties)
	}
	if _, ok := properties["why"]; !ok {
		t.Fatalf("question keyed by header missing: %+v", properties)
	}
	if enum := properties["Which?"].(map[string]any)["enum"].([]any); len(enum) != 2 {
		t.Fatalf("enum = %+v", enum)
	}
}
