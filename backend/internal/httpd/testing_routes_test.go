package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	testingsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/testing"
)

func TestTestingRoutesOnLoopbackButBlockedOnLAN(t *testing.T) {
	router := NewRouterWithControl(config.Config{}, nil, nil, APIDeps{}, ControlDeps{})
	paths := []string{"/api/v1/testing/runs", "/api/v1/testing/runs/run/attempts", "/api/v1/testing/attempts/attempt/cancel", "/api/v1/testing/attempts/attempt/evidence"}
	for _, name := range testingsvc.ToolNames {
		paths = append(paths, "/api/v1/testing/attempts/attempt/tools/"+name)
	}
	for _, path := range paths {
		method := http.MethodPost
		if strings.HasSuffix(path, "/evidence") {
			method = http.MethodGet
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
		if w.Code != 503 || !strings.Contains(w.Body.String(), "TESTING_PROVIDER_NOT_CONFIGURED") {
			t.Fatal("loopback route missing", path, w.Code, w.Body.String())
		}
		w = httptest.NewRecorder()
		lanControlBlock(router).ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
		if w.Code != http.StatusNotFound {
			t.Fatal("testing exposed on LAN", path, w.Code)
		}
	}
}
