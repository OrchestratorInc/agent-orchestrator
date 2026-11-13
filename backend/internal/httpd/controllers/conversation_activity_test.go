package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func (f *fakeConversationService) Activity(_ context.Context, session domain.SessionID, id string) (domain.ConversationActivity, error) {
	if session != "p1-1" {
		return domain.ConversationActivity{}, domain.ErrConversationActivityNotFound
	}
	for _, row := range f.snapshot.Activities {
		if row.ID == id {
			return row, nil
		}
	}
	return domain.ConversationActivity{}, domain.ErrConversationActivityNotFound
}

func TestConversationCompactPagesAndFullActivity(t *testing.T) {
	output := strings.Repeat("complete output\n", 1000)
	image := strings.Repeat("a", 660<<10)
	detail, _ := json.Marshal(map[string]any{"toolName": "image", "content": []any{map[string]any{"type": "image", "data": image, "mimeType": "image/png"}}, "output": []any{map[string]any{"data": image}}})
	service := &fakeConversationService{snapshot: chatsvc.Snapshot{Activities: []domain.ConversationActivity{
		{ID: "command:1", Sequence: 2, Revision: 3, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted, CommandOutput: output, Detail: json.RawMessage(`{"command":"cat log","exitCode":0}`)},
		{ID: "image", Sequence: 3, Revision: 1, Kind: domain.ActivityKindMCPTool, Status: domain.ActivityStatusCompleted, Detail: detail},
		{ID: "live", Sequence: 4, Revision: 2, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusRunning, CommandOutput: output},
	}}}
	server := conversationTestServer(t, service)
	get := func(path string, out any) int {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if out != nil {
			if err := json.NewDecoder(response.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
		return response.StatusCode
	}
	var plain, compact controllers.ConversationSnapshotResponse
	get("/api/v1/sessions/p1-1/conversation", &plain)
	get("/api/v1/sessions/p1-1/conversation?compactDetails=1&dedupeBodies=1", &compact)
	if len(plain.Activities) != len(compact.Activities) {
		t.Fatal("row count changed")
	}
	for i, preview := range compact.Activities {
		original := plain.Activities[i]
		if preview.ID != original.ID || preview.Sequence != original.Sequence || preview.Revision != original.Revision {
			t.Fatal("ids/order/revisions changed")
		}
		var full controllers.ConversationActivityResponse
		status := get(fmt.Sprintf("/api/v1/sessions/p1-1/conversation/activities/%s", strings.ReplaceAll(preview.ID, ":", "%3A")), &full)
		if status != http.StatusOK || full.ID != original.ID || full.DetailOmitted {
			t.Fatalf("full read status=%d id=%s omitted=%v", status, full.ID, full.DetailOmitted)
		}
		if original.ID == "image" {
			if full.Detail["content"].([]any)[0].(map[string]any)["data"] != image || full.Detail["outputOmitted"] != "content" {
				t.Fatal("full image/marker mismatch")
			}
		} else if full.Detail["output"] != output {
			t.Fatal("expanded output mismatch")
		}
	}
	if !compact.Activities[0].DetailOmitted || !compact.Activities[1].DetailOmitted {
		t.Fatal("settled detail not previewed")
	}
	if compact.Activities[2].DetailOmitted || compact.Activities[2].Detail["output"] != output {
		t.Fatal("running stream changed")
	}
	for _, path := range []string{"p1-1/conversation/activities/missing", "other/conversation/activities/image"} {
		if status := get("/api/v1/sessions/"+path, nil); status != http.StatusNotFound {
			t.Fatalf("missing row status=%d", status)
		}
	}
}
