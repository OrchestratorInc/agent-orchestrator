package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type taskNameHandlerStore struct {
	Store
	launch     domain.WorkerLaunch
	result     domain.TaskNameResult
	err        error
	input      domain.TaskNameRequest
	claims     worker.Claims
	calls      int
	registered bool
}

func (s *taskNameHandlerStore) NameWorkerTask(_ context.Context, orgID, sessionID, workerID string, epoch int64, input domain.TaskNameRequest) (domain.TaskNameResult, error) {
	s.calls++
	s.input = input
	s.claims = worker.Claims{OrgID: orgID, SessionID: sessionID, WorkerID: workerID, Epoch: epoch}
	return s.result, s.err
}
func (s *taskNameHandlerStore) WorkerLaunchSpec(context.Context, string, string) (domain.WorkerLaunch, error) {
	return s.launch, nil
}
func (s *taskNameHandlerStore) RedeemWorkerBootstrapTicket(context.Context, string) (domain.AccessTicket, error) {
	return domain.AccessTicket{OrgID: testOrgID, SessionID: testOrchestratorID, WorkerEpoch: 1, Scopes: []string{"worker:connect"}}, nil
}
func (s *taskNameHandlerStore) RegisterWorkerBootstrap(context.Context, string, string, string, string, int64, []string) error {
	s.registered = true
	return nil
}
func (s *taskNameHandlerStore) AppendSessionEvent(context.Context, string, string, string, json.RawMessage) (domain.ClientEvent, error) {
	return domain.ClientEvent{}, nil
}

func TestWorkerTaskNameScopeValidationAndFencing(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		scope      bool
		err        error
		status     int
		calls      int
		code       string
	}{
		{"reserve", `{"title":"Fix login","slug":"login","occupied":["login-2"]}`, true, nil, 200, 1, ""},
		{"claim", `{"claim":true}`, true, nil, 200, 1, ""},
		{"scope", `{}`, false, nil, 403, 0, "SCOPE_REQUIRED"},
		{"null", `null`, true, nil, 400, 0, "invalid_request"},
		{"unknown field", `{"workerId":"other"}`, true, nil, 400, 0, "invalid_request"},
		{"multiple objects", `{} {}`, true, nil, 400, 0, "invalid_request"},
		{"too large", `{"title":"` + strings.Repeat("a", 9000) + `"}`, true, nil, 400, 0, "invalid_request"},
		{"too many occupied", `{"occupied":[` + strings.Repeat(`"x",`, 100) + `"x"]}`, true, nil, 400, 0, "invalid_request"},
		{"stale", `{}`, true, postgres.ErrStaleWorker, 401, 1, "STALE_WORKER_TOKEN"},
		{"collision", `{}`, true, postgres.ErrConflict, 409, 1, "conflict"},
		{"invalid", `{}`, true, postgres.ErrInvalid, 422, 1, "validation_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &taskNameHandlerStore{result: domain.TaskNameResult{State: domain.TaskNamingReserved, Title: "Fix login", Branch: "ao/login/root", Slug: "login"}, err: tc.err}
			srv := testServer(store)
			scope := []string{}
			if tc.scope {
				scope = append(scope, "worker:connect")
			}
			w := httptest.NewRecorder()
			srv.workerTaskName(w, workerRequest(t, http.MethodPost, "/worker/task-name", tc.body, scope...))
			if w.Code != tc.status || store.calls != tc.calls || tc.code != "" && !strings.Contains(w.Body.String(), tc.code) {
				t.Fatalf("response=%d %s calls=%d", w.Code, w.Body, store.calls)
			}
			if tc.calls > 0 && (store.claims.WorkerID != "w1" || store.claims.Epoch != 1 || store.claims.OrgID != testOrgID || store.claims.SessionID != testOrchestratorID) {
				t.Fatalf("claims = %+v", store.claims)
			}
		})
	}
}

func TestWorkerTaskNamingLegacyCompatibility(t *testing.T) {
	for _, bootstrap := range []bool{false, true} {
		for _, tc := range []struct {
			name, state, slug string
			supported         bool
			result            domain.TaskNameResult
			status, calls     int
		}{
			{name: "pending legacy", state: "pending", result: domain.TaskNameResult{State: "done", Branch: "ao/12345678"}, status: 200, calls: 1},
			{name: "pending supported", state: "pending", supported: true, status: 200},
			{name: "reserved legacy", state: "reserved", slug: "login", status: 409},
			{name: "named legacy", state: "done", slug: "login", status: 409},
			{name: "old legacy", state: "done", status: 200},
			{name: "raced reservation", state: "pending", result: domain.TaskNameResult{State: "reserved", Branch: "ao/login/root", Slug: "login"}, status: 409, calls: 1},
		} {
			t.Run(tc.name+map[bool]string{true: " bootstrap", false: " reconnect"}[bootstrap], func(t *testing.T) {
				store := &taskNameHandlerStore{launch: domain.WorkerLaunch{SessionID: testOrchestratorID, ProjectID: "project", Kind: "worker", Harness: "codex", Branch: "ao/12345678", TaskNamingState: tc.state, TaskNameSlug: tc.slug}, result: tc.result}
				srv := testServer(store)
				srv.workerTokens = worker.NewTokenManager([]byte("task-naming-test-key"))
				w := httptest.NewRecorder()
				if bootstrap {
					body := `{"bootstrapToken":"test"}`
					if tc.supported {
						body = `{"bootstrapToken":"test","capabilities":["` + worker.CapabilityTaskNaming + `"]}`
					}
					srv.workerBootstrap(w, httptest.NewRequest(http.MethodPost, "/worker/bootstrap", strings.NewReader(body)))
					if !store.registered {
						t.Fatal("legacy fallback happened before registration")
					}
				} else {
					r := workerRequest(t, http.MethodGet, "/worker/reconnect", "", "worker:connect")
					if tc.supported {
						r.Header.Set(worker.CapabilitiesHeader, worker.CapabilityTaskNaming)
					}
					srv.workerReconnect(w, r)
				}
				if w.Code != tc.status || store.calls != tc.calls {
					t.Fatalf("response = %d %s calls=%d", w.Code, w.Body, store.calls)
				}
				if tc.calls > 0 && !store.input.Skip {
					t.Fatalf("compatibility request = %+v", store.input)
				}
				if w.Code == 409 && !strings.Contains(w.Body.String(), "WORKER_UPGRADE_REQUIRED") {
					t.Fatalf("upgrade response = %s", w.Body)
				}
				if w.Code == 200 {
					var result worker.BootstrapResponse
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					want := tc.state
					if tc.calls > 0 {
						want = "done"
					}
					if result.Launch.TaskNamingState != want {
						t.Fatalf("launch metadata = %+v", result.Launch)
					}
				}
			})
		}
	}
}

func TestCloudSessionTaskNamingMetadataAndTitleOptIn(t *testing.T) {
	for _, generate := range []bool{false, true} {
		store := &stubAutolinkStore{}
		srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
		req := createSessionRequestHTTP(t, "worker", "")
		if generate {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			body["generateTitle"] = true
			raw, _ := json.Marshal(body)
			req.Body = io.NopCloser(strings.NewReader(string(raw)))
		}
		w := httptest.NewRecorder()
		srv.createSession(w, req)
		if w.Code != 201 || store.captured.GenerateTitle != generate {
			t.Fatalf("create title opt-in = %d %+v %s", w.Code, store.captured, w.Body)
		}
	}
	response := toSessionResponse(domain.Session{TaskNamingState: "reserved", TaskNameSlug: "login"}, nil)
	if response.TaskNamingState != "reserved" || response.TaskNameSlug != "login" {
		t.Fatalf("public naming metadata = %+v", response)
	}
}
