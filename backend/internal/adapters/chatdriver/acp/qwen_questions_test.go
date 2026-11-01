package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestIsQwenUserQuestion(t *testing.T) {
	tests := []struct {
		name string
		tool acpsdk.ToolCallUpdate
		want bool
	}{
		{
			name: "meta qwenInteractionKind",
			tool: acpsdk.ToolCallUpdate{Meta: map[string]any{"qwenInteractionKind": "user_question"}},
			want: true,
		},
		{
			name: "meta toolName ask_user_question",
			tool: acpsdk.ToolCallUpdate{Meta: map[string]any{"toolName": "ask_user_question"}},
			want: true,
		},
		{
			name: "kind ask_user_question",
			tool: acpsdk.ToolCallUpdate{Kind: acpsdk.Ptr(acpsdk.ToolKind("ask_user_question"))},
			want: true,
		},
		{
			name: "rawInput questions with question title",
			tool: acpsdk.ToolCallUpdate{
				Title:    acpsdk.Ptr("Please answer question(s)"),
				RawInput: map[string]any{"questions": []any{map[string]any{"question": "Q"}}},
			},
			want: true,
		},
		{
			name: "regular bash command",
			tool: acpsdk.ToolCallUpdate{Kind: acpsdk.Ptr(acpsdk.ToolKindExecute), Meta: map[string]any{"toolName": "bash"}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isQwenUserQuestion(tt.tool); got != tt.want {
				t.Errorf("isQwenUserQuestion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractQwenQuestions(t *testing.T) {
	tool := acpsdk.ToolCallUpdate{
		Meta: map[string]any{
			"qwenQuestions": []any{
				map[string]any{
					"header":      "Header 1",
					"question":    "Which framework?",
					"multiSelect": true,
					"options": []any{
						map[string]any{"label": "React", "description": "UI library"},
						map[string]any{"label": "Vue"},
					},
				},
			},
		},
	}

	qs := extractQwenQuestions(tool)
	if len(qs) != 1 {
		t.Fatalf("len(qs) = %d, want 1", len(qs))
	}
	q := qs[0]
	if q.Header != "Header 1" || q.Question != "Which framework?" || !q.MultiSelect {
		t.Errorf("unexpected question fields: %+v", q)
	}
	if len(q.Options) != 2 || q.Options[0].Label != "React" || q.Options[0].Description != "UI library" {
		t.Errorf("unexpected options: %+v", q.Options)
	}
}

func TestQwenQuestionsToChatInputRequest(t *testing.T) {
	questions := []qwenQuestion{
		{
			Question: "Framework?",
			Options:  []qwenOption{{Label: "React", Description: "UI"}},
		},
		{
			Question:    "Features?",
			MultiSelect: true,
			Options:     []qwenOption{{Label: "Auth"}, {Label: "Logging"}},
		},
	}

	req := qwenQuestionsToChatInputRequest(acpsdk.ToolCallUpdate{}, questions)
	if req.Mode != ports.ChatInputModeForm {
		t.Fatalf("Mode = %v, want form", req.Mode)
	}

	schema := req.Schema
	props, _ := schema["properties"].(map[string]any)

	q0, _ := props["question_0"].(map[string]any)
	if q0["type"] != "string" || q0["oneOf"] == nil {
		t.Errorf("question_0 invalid: %+v", q0)
	}
	if props["question_0_custom"] == nil {
		t.Errorf("missing question_0_custom")
	}

	q1, _ := props["question_1"].(map[string]any)
	if q1["type"] != "array" || q1["items"] == nil {
		t.Errorf("question_1 invalid: %+v", q1)
	}
}

func TestQwenFormatAnswers(t *testing.T) {
	questions := []qwenQuestion{
		{Question: "Lang", MultiSelect: false},
		{Question: "Features", MultiSelect: true},
	}
	content := map[string]any{
		"question_0":        "Go",
		"question_0_custom": "",
		"question_1":        []any{"Auth", "Metrics"},
		"question_1_custom": "Traces",
	}

	answers := qwenFormatAnswers(questions, content)
	if answers["0"] != "Go" {
		t.Errorf("answers[0] = %q, want Go", answers["0"])
	}
	if answers["1"] != "Auth, Metrics, Traces" {
		t.Errorf("answers[1] = %q, want Auth, Metrics, Traces", answers["1"])
	}
}

func TestQwenUserQuestion_FlowAndRewriter(t *testing.T) {
	var wireBuffer bytes.Buffer
	mockStdin := &testWriteCloser{Buffer: &wireBuffer}
	transport, rewriter, _ := newLegacyACPTransport(mockStdin)

	incoming := `{"jsonrpc":"2.0","id":42,"method":"session/request_permission","params":{"toolCall":{"toolCallId":"call-1"}}}` + "\n"
	transport.intercept([]byte(incoming), func(string, json.RawMessage) bool { return false })
	transport.registerAnswersForToolCall("call-1", map[string]string{"0": "Option A"})

	sdkResp := []byte(`{"jsonrpc":"2.0","id":42,"result":{"outcome":{"outcome":"selected","optionId":"proceed_once"}}}` + "\n")
	if _, err := rewriter.Write(sdkResp); err != nil {
		t.Fatalf("rewriter.Write: %v", err)
	}

	var parsed struct {
		Result struct {
			Outcome struct {
				OptionID string `json:"optionId"`
			} `json:"outcome"`
			Answers map[string]string `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(wireBuffer.Bytes(), &parsed); err != nil {
		t.Fatalf("parse error: %v, raw: %s", err, wireBuffer.String())
	}
	if parsed.Result.Outcome.OptionID != "proceed_once" || parsed.Result.Answers["0"] != "Option A" {
		t.Errorf("unexpected rewritten result: %+v", parsed.Result)
	}
}

func TestQwenUserQuestion_RequestPermissionInteraction(t *testing.T) {
	params := acpsdk.RequestPermissionRequest{
		SessionId: "s1",
		ToolCall: acpsdk.ToolCallUpdate{
			ToolCallId: "call-99",
			Meta: map[string]any{
				"qwenInteractionKind": "user_question",
				"qwenQuestions": []any{
					map[string]any{
						"question": "Which path?",
						"options":  []any{map[string]any{"label": "Path A"}, map[string]any{"label": "Path B"}},
					},
				},
			},
		},
		Options: []acpsdk.PermissionOption{
			{OptionId: "proceed_once", Kind: acpsdk.PermissionOptionKindAllowOnce},
		},
	}

	t.Run("accept", func(t *testing.T) {
		conv := newTestConversation()
		resp := testRequestPermissionFlow(t, conv, params, ports.ChatInputActionAccept, map[string]any{"question_0": "Path A"})
		if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "proceed_once" {
			t.Errorf("outcome = %+v, want proceed_once", resp.Outcome)
		}
	})

	t.Run("decline", func(t *testing.T) {
		conv := newTestConversation()
		resp := testRequestPermissionFlow(t, conv, params, ports.ChatInputActionCancel, nil)
		if resp.Outcome.Cancelled == nil {
			t.Errorf("outcome = %+v, want cancelled", resp.Outcome)
		}
	})
}

func TestQwenUserQuestion_YOLOShouldNotBypass(t *testing.T) {
	conv := newTestConversation()
	conv.permissionMode = ports.PermissionModeBypassPermissions
	conv.permissionFor = func(ports.PermissionMode, acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
		return "proceed_once", true
	}

	// 1. Regular tool is auto-approved in YOLO mode
	regular := acpsdk.RequestPermissionRequest{
		ToolCall: acpsdk.ToolCallUpdate{ToolCallId: "exec-1", Kind: acpsdk.Ptr(acpsdk.ToolKindExecute)},
		Options:  []acpsdk.PermissionOption{{OptionId: "proceed_once", Kind: acpsdk.PermissionOptionKindAllowOnce}},
	}
	resp, err := conv.RequestPermission(context.Background(), regular)
	if err != nil || resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "proceed_once" {
		t.Fatalf("regular tool not auto-approved: %+v, err: %v", resp.Outcome, err)
	}

	// 2. Qwen question MUST NOT be bypassed; must wait for user input
	qwenTool := acpsdk.RequestPermissionRequest{
		ToolCall: acpsdk.ToolCallUpdate{
			ToolCallId: "qwen-1",
			Meta: map[string]any{
				"qwenInteractionKind": "user_question",
				"qwenQuestions":       []any{map[string]any{"question": "Q?", "options": []any{map[string]any{"label": "A"}}}},
			},
		},
		Options: []acpsdk.PermissionOption{{OptionId: "proceed_once", Kind: acpsdk.PermissionOptionKindAllowOnce}},
	}
	qwenResp := testRequestPermissionFlow(t, conv, qwenTool, ports.ChatInputActionAccept, map[string]any{"question_0": "A"})
	if qwenResp.Outcome.Selected == nil || qwenResp.Outcome.Selected.OptionId != "proceed_once" {
		t.Fatalf("expected answered question, got %+v", qwenResp.Outcome)
	}
}

func newTestConversation() *conversation {
	return &conversation{
		pendingInputs: make(map[string]*parkedInput),
		accepted:      make(map[string]persistentInteractionCommand),
		events:        make(chan ports.ChatEvent, 10),
	}
}

func testRequestPermissionFlow(
	t *testing.T,
	conv *conversation,
	params acpsdk.RequestPermissionRequest,
	action ports.ChatInputAction,
	content map[string]any,
) acpsdk.RequestPermissionResponse {
	t.Helper()
	done := make(chan acpsdk.RequestPermissionResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := conv.RequestPermission(context.Background(), params)
		if err != nil {
			errCh <- err
			return
		}
		done <- resp
	}()

	select {
	case event := <-conv.events:
		if event.Kind != ports.ChatEventInputRequested {
			t.Fatalf("unexpected event: %v", event.Kind)
		}
		if err := conv.ResolveInput(context.Background(), event.RequestID, ports.ChatInputResponse{
			Action:  action,
			Content: content,
		}); err != nil {
			t.Fatalf("ResolveInput: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for ChatEventInputRequested")
	}

	select {
	case resp := <-done:
		return resp
	case err := <-errCh:
		t.Fatalf("RequestPermission failed: %v", err)
		return acpsdk.RequestPermissionResponse{}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for RequestPermission to return")
		return acpsdk.RequestPermissionResponse{}
	}
}

type testWriteCloser struct {
	*bytes.Buffer
}

func (t *testWriteCloser) Close() error {
	return nil
}
