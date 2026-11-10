package controllers_test

import (
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 显式新建跨过旧单例入口；相互冲突的动作在产生副作用之前拒绝。
func TestSessionsAPI_IndependentOrchestrator(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators", `{"projectId":"ao","newSession":true,"mode":"chat"}`)
	if status != http.StatusCreated || !svc.lastSpawn.IndependentOrchestrator || svc.lastSpawn.Kind != domain.KindOrchestrator {
		t.Fatalf("status=%d body=%s spawn=%+v", status, body, svc.lastSpawn)
	}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/orchestrators", `{"projectId":"ao","newSession":true,"clean":true}`)
	if status != http.StatusBadRequest {
		t.Fatalf("conflicting options: %d %s", status, body)
	}
}
