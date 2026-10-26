package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func (f *fakeConversationService) Activity(_ context.Context, _ domain.SessionID, activityID string) (domain.ConversationActivity, error) {
	for _, activity := range f.snapshot.Activities {
		if activity.ID == activityID {
			return activity, nil
		}
	}
	return domain.ConversationActivity{}, domain.ErrNoConversationActivity
}

func previewFixture(t *testing.T) chatsvc.Snapshot {
	t.Helper()
	heredoc := "cat > notes.md <<'EOF'\n" + strings.Repeat("a long line of text\n", 100) + "EOF"
	output := "spawned session del-9 \"worker\" (working)\n" + strings.Repeat("log line\n", 200)
	detail := func(fields map[string]any) []byte {
		encoded, err := json.Marshal(fields)
		if err != nil {
			t.Fatalf("encode detail: %v", err)
		}
		return encoded
	}
	now := time.Now().UTC()
	return chatsvc.Snapshot{
		SessionID: "p1-1",
		Mode:      domain.SessionModeChat,
		Activities: []domain.ConversationActivity{
			{
				ID: "settled", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
				Summary: heredoc, CreatedAt: now,
				Detail: detail(map[string]any{
					"command": heredoc, "rawCommand": heredoc, "input": map[string]any{"command": heredoc},
					"output": output, "exitCode": 0, "cwd": "/repo",
				}),
			},
			{
				ID: "running", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusRunning,
				Summary: "make build", CreatedAt: now,
				Detail:        detail(map[string]any{"command": "make build", "rawCommand": "make build"}),
				CommandOutput: output,
			},
			{
				ID: "small", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusFailed,
				Summary: "go vet ./...", CreatedAt: now,
				Detail: detail(map[string]any{"command": "go vet ./...", "rawCommand": "/bin/zsh -lc 'go vet ./...'", "output": "vet: ok\n", "exitCode": 1}),
			},
			{
				ID: "edit", Kind: domain.ActivityKindFileChange, Status: domain.ActivityStatusCompleted,
				Summary: "Edited notes.md", CreatedAt: now,
				Detail: detail(map[string]any{"files": []any{map[string]any{
					"path": "notes.md", "status": "modified", "additions": 100, "deletions": 0,
					"patch": strings.Repeat("+a long line of text\n", 100),
				}}}),
			},
			{
				ID: "steer", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
				Summary: "steer", CreatedAt: now,
				Detail: detail(map[string]any{"event": "steer", "text": strings.Repeat("words ", 200)}),
			},
		},
	}
}

func getConversationJSON(t *testing.T, service *fakeConversationService, path string) (int, map[string]any) {
	t.Helper()
	server := conversationTestServer(t, service)
	resp, err := http.Get(server.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return resp.StatusCode, decoded
}

func activitiesByID(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, item := range body["activities"].([]any) {
		activity := item.(map[string]any)
		out[activity["id"].(string)] = activity
	}
	return out
}

func TestConversationPreviewCutsSettledActivityBodies(t *testing.T) {
	fixture := previewFixture(t)
	status, body := getConversationJSON(t, &fakeConversationService{snapshot: fixture}, "/api/v1/sessions/p1-1/conversation?activityDetail=preview")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	activities := activitiesByID(t, body)

	settled := activities["settled"]
	detail := settled["detail"].(map[string]any)
	if settled["detailOmitted"] != true || settled["detailBytes"].(float64) < 4000 {
		t.Fatalf("settled omission = %v / %v", settled["detailOmitted"], settled["detailBytes"])
	}
	if len(settled["summary"].(string)) > 512 || len(detail["command"].(string)) > 512 || len(detail["output"].(string)) > 512 {
		t.Fatalf("settled row kept long text: summary %d, command %d, output %d", len(settled["summary"].(string)), len(detail["command"].(string)), len(detail["output"].(string)))
	}
	// Collapsed rows read these: the command category, the spawn line, the exit state.
	if !strings.HasPrefix(detail["command"].(string), "cat > notes.md") || !strings.HasPrefix(detail["output"].(string), "spawned session del-9") {
		t.Fatalf("preview lost the head of command or output: %v", detail)
	}
	if detail["exitCode"] != float64(0) || detail["cwd"] != "/repo" {
		t.Fatalf("small fields changed: %v", detail)
	}
	if _, ok := detail["rawCommand"]; ok {
		t.Fatalf("rawCommand copy still sent")
	}
	if _, ok := detail["input"]; ok {
		t.Fatalf("large input still sent")
	}

	running := activities["running"]
	runningDetail := running["detail"].(map[string]any)
	if running["detailOmitted"] != nil || runningDetail["output"] != fixture.Activities[1].CommandOutput {
		t.Fatalf("running row must stream full output: %v", running)
	}
	if _, ok := runningDetail["rawCommand"]; ok {
		t.Fatalf("running row kept a rawCommand identical to command")
	}

	small := activities["small"]
	if small["detailOmitted"] != nil || small["detail"].(map[string]any)["rawCommand"] != "/bin/zsh -lc 'go vet ./...'" {
		t.Fatalf("small row changed: %v", small)
	}

	edit := activities["edit"]
	file := edit["detail"].(map[string]any)["files"].([]any)[0].(map[string]any)
	if edit["detailOmitted"] != true || file["patch"] != nil || file["path"] != "notes.md" || file["additions"] != float64(100) {
		t.Fatalf("file change preview = %v", edit)
	}

	steer := activities["steer"]
	if steer["detailOmitted"] != nil || len(steer["detail"].(map[string]any)["text"].(string)) != 1200 {
		t.Fatalf("visible system text was cut: %v", steer)
	}
}

func TestConversationWithoutPreviewKeepsFullActivityDetail(t *testing.T) {
	fixture := previewFixture(t)
	_, body := getConversationJSON(t, &fakeConversationService{snapshot: fixture}, "/api/v1/sessions/p1-1/conversation")
	settled := activitiesByID(t, body)["settled"]
	detail := settled["detail"].(map[string]any)
	if settled["detailOmitted"] != nil || settled["summary"] != fixture.Activities[0].Summary || detail["rawCommand"] != detail["command"] || detail["input"] == nil {
		t.Fatalf("default page changed: %v", settled)
	}
}

func TestConversationRejectsUnknownActivityDetail(t *testing.T) {
	status, body := getConversationJSON(t, &fakeConversationService{}, "/api/v1/sessions/p1-1/conversation?activityDetail=lean")
	if status != http.StatusBadRequest || body["code"] != "CONVERSATION_ACTIVITY_DETAIL_INVALID" {
		t.Fatalf("status = %d, body = %v", status, body)
	}
}

func TestConversationActivityReturnsFullDetail(t *testing.T) {
	fixture := previewFixture(t)
	service := &fakeConversationService{snapshot: fixture}
	status, body := getConversationJSON(t, service, "/api/v1/sessions/p1-1/conversation/activities/settled")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	detail := body["detail"].(map[string]any)
	if body["summary"] != fixture.Activities[0].Summary || detail["rawCommand"] != detail["command"] || len(detail["output"].(string)) < 1000 || body["detailOmitted"] != nil {
		t.Fatalf("activity = %v", body)
	}

	status, body = getConversationJSON(t, service, "/api/v1/sessions/p1-1/conversation/activities/missing")
	if status != http.StatusNotFound || body["code"] != "CHAT_ACTIVITY_NOT_FOUND" {
		t.Fatalf("missing activity status = %d, body = %v", status, body)
	}
}
