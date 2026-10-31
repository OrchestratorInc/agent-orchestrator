package chat

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestMCPAppPendingApprovalIsolation(t *testing.T) {
	for _, method := range []string{"tools/call", "ui/message"} {
		t.Run(method, func(t *testing.T) {
			s := &Service{}
			controller := &Controller{generation: "generation-1"}
			app := domain.MCPAppRecord{ConversationID: "conversation-1", ActivityID: "app-1"}
			params := json.RawMessage(`{"name":"write","arguments":{}}`)
			if method == "ui/message" {
				params = json.RawMessage(`{"role":"user","content":[{"type":"text","text":"Explain"}]}`)
			}
			request := MCPAppRequest{Method: method, Params: params}
			var tokens []string
			for range 16 {
				response, approved, err := s.appApproval(controller, app, request, "Allow action")
				if err != nil || approved || response.ApprovalToken == "" {
					t.Fatalf("create pending approval: %+v, approved=%v, err=%v", response, approved, err)
				}
				tokens = append(tokens, response.ApprovalToken)
			}
			if response, approved, err := s.appApproval(controller, app, request, "Allow action"); !errors.Is(err, ErrMCPApp) || approved || response.ApprovalToken != "" {
				t.Fatalf("saturated app admitted another request: %+v, approved=%v, err=%v", response, approved, err)
			}
			for _, other := range []domain.MCPAppRecord{
				{ConversationID: app.ConversationID, ActivityID: "app-2"},
				{ConversationID: "conversation-2", ActivityID: "app-3"},
			} {
				response, approved, err := s.appApproval(controller, other, request, "Allow action")
				if err != nil || approved || response.ApprovalToken == "" {
					t.Fatalf("saturated app blocked %+v: %+v, approved=%v, err=%v", other, response, approved, err)
				}
			}
			if response, approved, err := s.appApproval(&Controller{generation: "generation-2"}, app, request, "Allow action"); err != nil || approved || response.ApprovalToken == "" {
				t.Fatalf("old controller blocked replacement: %+v, approved=%v, err=%v", response, approved, err)
			}

			// A full allowance must still accept confirmation and release the slot.
			confirmed := request
			confirmed.ApprovalToken = tokens[0]
			if _, approved, err := s.appApproval(controller, app, confirmed, "Allow action"); err != nil || !approved {
				t.Fatalf("confirm saturated app's request: approved=%v, err=%v", approved, err)
			}
			if _, _, err := s.appApproval(controller, app, request, "Allow action"); err != nil {
				t.Fatalf("consumed approval did not release its slot: %v", err)
			}

			value, _ := s.mcpAppApprovals.Load(tokens[1])
			expired, ok := value.(mcpAppApproval)
			if !ok {
				t.Fatal("pending approval missing")
			}
			expired.expires = time.Now().Add(-time.Second)
			s.mcpAppApprovals.Store(tokens[1], expired)
			if _, _, err := s.appApproval(controller, app, request, "Allow action"); err != nil {
				t.Fatalf("expired approval did not release its slot: %v", err)
			}
			if _, exists := s.mcpAppApprovals.Load(tokens[1]); exists {
				t.Fatal("expired approval was retained")
			}
		})
	}
}
