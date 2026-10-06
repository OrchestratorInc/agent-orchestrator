package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	testingsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/testing"
)

type testingServiceFake struct {
	tool, token, request string
	input                json.RawMessage
	fail                 error
	calls                int
}

func (f *testingServiceFake) CreateRun(_ context.Context, in testingsvc.CreateRunInput) (domain.TestRunRecord, error) {
	f.calls++
	return domain.TestRunRecord{ID: "run", ProjectID: in.ProjectID, CreatedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)}, f.fail
}
func (f *testingServiceFake) StartAttempt(_ context.Context, id domain.TestRunID, _ testingsvc.StartAttemptInput) (testingsvc.StartAttemptResult, error) {
	f.calls++
	return testingsvc.StartAttemptResult{RunID: id, AttemptID: "attempt", WorkerSessionID: "worker"}, f.fail
}
func (f *testingServiceFake) Cancel(_ context.Context, id domain.TestAttemptID) (domain.TestAttemptRecord, error) {
	f.calls++
	return domain.TestAttemptRecord{ID: id, Phase: domain.TestAttemptFinished, Outcome: domain.TestOutcomeCancelled, CleanupState: domain.TestCleanupPending, RecordingGap: "no video"}, f.fail
}
func (f *testingServiceFake) Execute(_ context.Context, _ domain.TestAttemptID, session domain.SessionID, token, request, name string, input json.RawMessage) (testingsvc.ToolResult, error) {
	f.calls++
	f.tool = name
	f.token = token
	f.request = request
	f.input = input
	if session != "worker" || token != "capability" {
		return testingsvc.ToolResult{}, apierr.Forbidden("INVALID_TEST_CAPABILITY", "Wrong capability")
	}
	return testingsvc.ToolResult{Action: &domain.TestActionResult{Delivered: true}, Evidence: []domain.TestEvidenceReceipt{}}, f.fail
}
func (f *testingServiceFake) ListEvidence(_ context.Context, _ domain.TestAttemptID) ([]domain.TestEvidenceReceipt, error) {
	f.calls++
	return nil, f.fail
}
func testingRouter(f TestingService) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	(&TestingController{Svc: f}).Register(r)
	return r
}
func testingRequest(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set(testingsvc.CapabilityHeader, token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestTestingManagementAndSevenExactToolRoutes(t *testing.T) {
	f := &testingServiceFake{}
	router := testingRouter(f)
	for _, test := range []struct {
		method, path, body string
		status             int
	}{{http.MethodPost, "/api/v1/testing/runs", `{"projectId":"ao","issueUrl":"url","issueSnapshot":"issue","commitSha":"abc","recipeId":"native","requester":"maintainer"}`, 201}, {http.MethodPost, "/api/v1/testing/runs/run/attempts", `{"workerPrompt":"investigate","timeoutSeconds":60}`, 201}, {http.MethodPost, "/api/v1/testing/attempts/attempt/cancel", "", 200}, {http.MethodGet, "/api/v1/testing/attempts/attempt/evidence", "", 200}} {
		w := testingRequest(router, test.method, test.path, test.body, "")
		if w.Code != test.status {
			t.Fatalf("%s: %d %s", test.path, w.Code, w.Body.String())
		}
	}
	w := testingRequest(router, http.MethodGet, "/api/v1/testing/attempts/attempt/evidence", "", "")
	if !strings.Contains(w.Body.String(), `"evidence":[]`) {
		t.Fatal("nil evidence array reached wire")
	}
	for _, name := range testingsvc.ToolNames {
		w := testingRequest(router, http.MethodPost, "/api/v1/testing/attempts/attempt/tools/"+name, `{"sessionId":"worker","requestId":"unique","input":{}}`, "capability")
		if w.Code != 200 || f.tool != name || f.token != "capability" || f.request != "unique" || string(f.input) != "{}" {
			t.Fatal("tool forwarding", name, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "capability") {
			t.Fatal("capability leaked in response")
		}
	}
	w = testingRequest(router, http.MethodPost, "/api/v1/testing/attempts/attempt/tools/other", `{}`, "capability")
	if w.Code != 404 {
		t.Fatal("unknown tool route registered")
	}
}
func TestTestingErrorsStrictBodiesAndProviderMissing(t *testing.T) {
	for _, path := range []string{"/api/v1/testing/runs", "/api/v1/testing/runs/run/attempts", "/api/v1/testing/attempts/attempt/cancel", "/api/v1/testing/attempts/attempt/tools/screenshot"} {
		w := testingRequest(testingRouter(nil), http.MethodPost, path, `{}`, "")
		if w.Code != 503 || !strings.Contains(w.Body.String(), "TESTING_PROVIDER_NOT_CONFIGURED") || !strings.Contains(w.Body.String(), "requestId") {
			t.Fatal("missing provider envelope", w.Code, w.Body.String())
		}
	}
	f := &testingServiceFake{}
	router := testingRouter(f)
	for _, body := range []string{`{"sessionId":"worker","requestId":"id","input":{},"target":"host"}`, `{"sessionId":"worker","requestId":"id","input":{}} {}`, `null`, `{"sessionId":"","requestId":"id","input":{}}`} {
		before := f.calls
		w := testingRequest(router, http.MethodPost, "/api/v1/testing/attempts/attempt/tools/screenshot", body, "capability")
		if w.Code != 400 || f.calls != before {
			t.Fatal("bad body admitted", w.Code, w.Body.String())
		}
	}
	w := testingRequest(router, http.MethodPost, "/api/v1/testing/attempts/attempt/tools/screenshot", `{"sessionId":"worker","requestId":"id","input":{}}`, "")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "INVALID_TEST_CAPABILITY") {
		t.Fatal("header was optional")
	}
	for _, test := range []struct {
		err    error
		status int
	}{{apierr.Conflict("TEST_TARGET_CHANGED", "changed", nil), 409}, {apierr.Internal("TEST_EVIDENCE_WRITE_FAILED", "disk full"), 500}, {apierr.NotFound("TEST_ATTEMPT_NOT_FOUND", "unknown"), 404}} {
		f.fail = test.err
		w := testingRequest(router, http.MethodGet, "/api/v1/testing/attempts/attempt/evidence", "", "")
		if w.Code != test.status || !strings.Contains(w.Body.String(), "requestId") {
			t.Fatal("service error envelope", w.Code, w.Body.String())
		}
	}
}
