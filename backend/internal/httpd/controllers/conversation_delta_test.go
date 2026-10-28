package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type deltaConversationService struct {
	fakeConversationService
	after, changed, limit int64
}

func (f *deltaConversationService) SnapshotDelta(_ context.Context, _ domain.SessionID, after, changed, limit int64) (chatsvc.Snapshot, error) {
	f.after, f.changed, f.limit = after, changed, limit
	return chatsvc.Snapshot{AfterSequence: &after, ChangeCursor: changed + 1}, nil
}
func TestConversationDeltaQueryContract(t *testing.T) {
	service := &deltaConversationService{}
	server := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: newFakeSessionService(), Conversations: service}, httpd.ControlDeps{}))
	defer server.Close()
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"?afterSequence=5&afterChange=10&limit=20", 200},
		{"?afterSequence=0", 400}, {"?afterSequence=-1", 400}, {"?afterSequence=bad", 400},
		{"?afterSequence=5&beforeSequence=3", 400}, {"?afterChange=10", 400},
		{"?afterSequence=5&afterChange=0", 400}, {"?afterSequence=5&afterChange=-1", 400},
	} {
		t.Run(tc.query, func(t *testing.T) {
			response, err := http.Get(server.URL + "/api/v1/sessions/p1-1/conversation" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d want %d", response.StatusCode, tc.status)
			}
			if tc.status == 200 {
				var body struct {
					AfterSequence *int64 `json:"afterSequence"`
					ChangeCursor  int64  `json:"changeCursor"`
				}
				if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.AfterSequence == nil || *body.AfterSequence != 5 || body.ChangeCursor != 11 || service.after != 5 || service.changed != 10 || service.limit != 20 {
					t.Fatalf("delta contract body=%+v service=%+v", body, service)
				}
			}
		})
	}
}
