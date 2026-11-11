package controllers_test

import (
	"net/http"
	"testing"
)

// 未命名会话必须保持空名，才能由现有自动标题链路命名且不冒充人工改名。
func TestIndependentOrchestratorStartsWithoutReservedTitle(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators", `{"projectId":"ao","newSession":true,"mode":"chat"}`)
	if status != http.StatusCreated {
		t.Fatalf("创建独立编排器: status=%d body=%s", status, body)
	}
	if svc.lastSpawn.DisplayName != "" {
		t.Fatalf("固定默认名会阻断自动命名: displayName=%q", svc.lastSpawn.DisplayName)
	}
}
