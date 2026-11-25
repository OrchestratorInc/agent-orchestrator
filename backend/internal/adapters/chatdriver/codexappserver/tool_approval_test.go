package codexappserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func toolApprovalConversation() *conversation {
	return &conversation{
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		pending:       map[string]*parkedRequest{},
		pendingInputs: map[string]*parkedInput{},
		events:        make(chan ports.ChatEvent, eventBuffer),
	}
}

func TestAToolApprovalIsAskedInTheChatAndAnswered(t *testing.T) {
	for _, tt := range []struct {
		name, params, decision, want string
		offered                      []string
	}{
		{
			name:     "allowed one time",
			params:   `{"mode":"form","serverName":"linear","threadId":"t1","turnId":"turn-1","message":"Allow the linear MCP server to run tool \"create_issue\"?","_meta":{"codex_approval_kind":"mcp_tool_call"},"requestedSchema":{"type":"object","properties":{}}}`,
			offered:  []string{"accept", "cancel"},
			decision: "accept", want: `{"action":"accept","content":{}}`,
		},
		{
			name:     "allowed for the session",
			params:   `{"mode":"form","serverName":"linear","turnId":null,"message":"Allow?","_meta":{"codex_approval_kind":"mcp_tool_call","persist":["session","always"]},"requestedSchema":null}`,
			offered:  []string{"accept", "acceptForSession", "cancel"},
			decision: "acceptForSession", want: `{"_meta":{"persist":"session"},"action":"accept"}`,
		},
		{
			name:     "cancelled",
			params:   `{"serverName":"linear","message":"Allow?","_meta":{"codex_approval_kind":"mcp_tool_call","persist":"session"}}`,
			offered:  []string{"accept", "acceptForSession", "cancel"},
			decision: "cancel", want: `{"action":"cancel"}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := toolApprovalConversation()
			type answer struct {
				result any
				err    error
			}
			done := make(chan answer, 1)
			go func() {
				result, err := c.handleServerRequest(context.Background(), serverRequest{
					ID: json.RawMessage(`7`), Method: codexproto.MethodMcpServerElicitationRequest, Params: json.RawMessage(tt.params),
				})
				done <- answer{result, err}
			}()
			asked := nextEvent(t, c.events, ports.ChatEventApprovalRequested)
			if asked.ActivityKind != domain.ActivityKindApproval || asked.Summary == "" || len(asked.Decisions) != len(tt.offered) ||
				!strings.Contains(string(asked.Detail), `"subjectKind":"mcp_tool"`) || !strings.Contains(string(asked.Detail), `"server":"linear"`) {
				t.Fatalf("asked = %+v", asked)
			}
			for i, option := range asked.Decisions {
				if option.ID != tt.offered[i] || option.Label == "" {
					t.Fatalf("decision %d = %+v, want %s", i, option, tt.offered[i])
				}
			}
			if err := c.ResolveRequest(context.Background(), asked.RequestID, ports.ChatDecision{ID: tt.decision}); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-done:
				raw, _ := json.Marshal(got.result)
				if got.err != nil || string(raw) != tt.want {
					t.Fatalf("reply = %s, %v, want %s", raw, got.err, tt.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the handler did not answer")
			}
		})
	}
}

func TestOtherQuestionsOfAToolServerAreStillRefused(t *testing.T) {
	for name, params := range map[string]string{
		"a form to fill":    `{"mode":"form","serverName":"docs","message":"Your name?","_meta":{"codex_approval_kind":"mcp_tool_call"},"requestedSchema":{"type":"object","properties":{"name":{"type":"string"}}}}`,
		"a page to open":    `{"mode":"url","serverName":"docs","url":"https://docs.example.com/login","_meta":{"codex_approval_kind":"mcp_tool_call"}}`,
		"not a tool call":   `{"mode":"form","serverName":"docs","message":"Continue?","requestedSchema":{"type":"object","properties":{}}}`,
		"another form kind": `{"mode":"openai/form","serverName":"docs","_meta":{"codex_approval_kind":"mcp_tool_call"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := toolApprovalConversation()
			result, err := c.handleServerRequest(context.Background(), serverRequest{
				ID: json.RawMessage(`7`), Method: codexproto.MethodMcpServerElicitationRequest, Params: json.RawMessage(params),
			})
			if result != nil || err == nil || len(c.pending) != 0 {
				t.Fatalf("result = %#v, %v, pending %d", result, err, len(c.pending))
			}
		})
	}
	background := toolApprovalConversation()
	background.disableNetwork = true
	if _, err := background.handleServerRequest(context.Background(), serverRequest{
		ID: json.RawMessage(`7`), Method: codexproto.MethodMcpServerElicitationRequest,
		Params: json.RawMessage(`{"serverName":"linear","_meta":{"codex_approval_kind":"mcp_tool_call"}}`),
	}); err == nil {
		t.Fatal("a background task was asked for a tool approval")
	}
}
