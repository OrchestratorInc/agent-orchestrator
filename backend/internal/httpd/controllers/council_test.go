package controllers_test

import (
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

func TestSessionsAPI_SpawnCouncilFansOutAndLinksCohort(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/council",
		`{"projectId":"ao","prompt":"fix the bug","displayName":"compare","members":[{"harness":"codex","model":"gpt-5"},{"harness":"claude-code","model":"sonnet"}]}`)
	if status != http.StatusCreated {
		t.Fatalf("POST council = %d, want 201; body=%s", status, body)
	}
	var resp controllers.SpawnCouncilResponse
	mustJSON(t, body, &resp)
	if resp.GroupID == "" {
		t.Fatalf("missing groupId: %s", body)
	}
	if len(resp.Members) != 2 {
		t.Fatalf("members = %d, want 2; body=%s", len(resp.Members), body)
	}
	for _, member := range resp.Members {
		if member.Session == nil {
			t.Fatalf("member %s missing session: %s", member.Harness, body)
		}
		if member.Session.CouncilGroupID != resp.GroupID {
			t.Fatalf("member %s councilGroupId = %q, want %q", member.Harness, member.Session.CouncilGroupID, resp.GroupID)
		}
	}
	if resp.Members[0].Harness != "codex" || resp.Members[1].Harness != "claude-code" {
		t.Fatalf("members out of request order: %#v", resp.Members)
	}
}

func TestSessionsAPI_SpawnCouncilRejectsInvalidMode(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/council",
		`{"mode":"bogus","members":[{"harness":"codex"},{"harness":"claude-code"}]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid mode = %d, want 400; body=%s", status, body)
	}
}

func TestSessionsAPI_SpawnCouncilRequiresMembers(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/council", `{"members":[]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("no members = %d, want 400; body=%s", status, body)
	}
}
