package controllers_test

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type changeTestPage struct {
	ChangeCursor string `json:"changeCursor"`
	ChangesOnly  bool   `json:"changesOnly"`
	PageDigest   string `json:"pageDigest"`
	Turns        []struct {
		ID string `json:"id"`
	} `json:"turns"`
	Messages []struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	} `json:"messages"`
	Activities []struct {
		ID string `json:"id"`
	} `json:"activities"`
}

func getChangeTestPage(t *testing.T, base, query string) (int, changeTestPage) {
	t.Helper()
	resp, err := http.Get(base + "/api/v1/sessions/p1-1/conversation" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page changeTestPage
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, page
}

func TestConversationChangedSinceReturnsOnlyChangedRows(t *testing.T) {
	old := time.Now().Add(-time.Minute)
	service := &fakeConversationService{snapshot: chatsvc.Snapshot{
		Conversation: domain.ConversationRecord{ID: "conv-1", ActiveBranchID: "branch-1", LatestSequence: 3},
		Turns:        []domain.ConversationTurn{{ID: "turn-1", State: domain.TurnStateRunning, RequestedAt: old}},
		Messages: []domain.ConversationMessage{
			{ID: "m1", TurnID: "turn-1", Sequence: 1, Revision: 1, Text: "hi", CreatedAt: old, UpdatedAt: old},
			{ID: "m3", TurnID: "turn-1", Sequence: 3, Revision: 4, Text: "stream", Streaming: true, CreatedAt: old, UpdatedAt: old},
		},
		Activities: []domain.ConversationActivity{
			{ID: "a2", TurnID: "turn-1", Sequence: 2, Revision: 2, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted, CreatedAt: old, UpdatedAt: old},
		},
	}}
	server := conversationTestServer(t, service)

	status, first := getChangeTestPage(t, server.URL, "")
	if status != http.StatusOK || first.ChangeCursor == "" || first.ChangesOnly || first.PageDigest != "" {
		t.Fatalf("full read = %d %+v", status, first)
	}

	// The streamed message grows and a new command starts after the first read.
	service.snapshot.Conversation.LatestSequence = 4
	service.snapshot.Messages[1].Revision, service.snapshot.Messages[1].UpdatedAt = 5, time.Now()
	service.snapshot.Activities = append(service.snapshot.Activities, domain.ConversationActivity{
		ID: "a4", TurnID: "turn-1", Sequence: 4, Revision: 0, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusRunning,
		CreatedAt: old, UpdatedAt: old,
	})
	status, delta := getChangeTestPage(t, server.URL, "?changedSince="+url.QueryEscape(first.ChangeCursor))
	if status != http.StatusOK || !delta.ChangesOnly {
		t.Fatalf("changedSince read = %d %+v", status, delta)
	}
	if len(delta.Messages) != 1 || delta.Messages[0].ID != "m3" || delta.Messages[0].Revision != 5 {
		t.Fatalf("messages = %+v, want only the streamed m3", delta.Messages)
	}
	if len(delta.Activities) != 1 || delta.Activities[0].ID != "a4" {
		t.Fatalf("activities = %+v, want only the new a4", delta.Activities)
	}
	if len(delta.Turns) != 1 || delta.ChangeCursor == "" || delta.ChangeCursor == first.ChangeCursor {
		t.Fatalf("turns/cursor = %+v / %q", delta.Turns, delta.ChangeCursor)
	}
	hash := fnv.New32a()
	for _, key := range []string{"message:m1:1", "activity:a2:2", "message:m3:5", "activity:a4:0"} {
		_, _ = hash.Write([]byte(key + "\n"))
	}
	if want := fmt.Sprintf("%08x", hash.Sum32()); delta.PageDigest != want {
		t.Fatalf("pageDigest = %q, want %q over the full page", delta.PageDigest, want)
	}

	// A cursor from another branch cannot describe this page row by row.
	service.snapshot.Conversation.ActiveBranchID = "branch-2"
	status, full := getChangeTestPage(t, server.URL, "?changedSince="+url.QueryEscape(delta.ChangeCursor))
	if status != http.StatusOK || full.ChangesOnly || len(full.Messages) != 2 || len(full.Activities) != 2 {
		t.Fatalf("branch-changed read = %d %+v, want the full page", status, full)
	}
}

func TestConversationChangedSinceRejectsInvalidCursors(t *testing.T) {
	service := &fakeConversationService{snapshot: chatsvc.Snapshot{Conversation: domain.ConversationRecord{ID: "conv-1", LatestSequence: 1}}}
	server := conversationTestServer(t, service)
	_, first := getChangeTestPage(t, server.URL, "")
	for _, query := range []string{
		"?changedSince=not-a-cursor",
		"?changedSince=" + url.QueryEscape(first.ChangeCursor) + "&beforeSequence=5",
	} {
		if status, _ := getChangeTestPage(t, server.URL, query); status != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400", query, status)
		}
	}
}
