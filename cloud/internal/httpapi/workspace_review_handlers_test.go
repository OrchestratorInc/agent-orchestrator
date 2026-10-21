package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/go-chi/chi/v5"
)

type workspaceReviewHandlerStore struct {
	Store
	provider       string
	modelOrgID     string
	modelSessionID string
	modelResponse  json.RawMessage
	modelSourceErr error
	createdKind    string
	createdPayload json.RawMessage
	requestTimeout time.Duration
	eventType      string
	errorCode      string
}

func (s *workspaceReviewHandlerStore) UserModelCatalogSession(_ context.Context, _ domain.Principal, orgID, _ string) (string, string, error) {
	s.modelOrgID = orgID
	return orgID, s.modelSessionID, s.modelSourceErr
}

func (s *workspaceReviewHandlerStore) GetSession(_ context.Context, _ domain.Principal, _, _ string) (domain.Session, error) {
	return domain.Session{SandboxProvider: s.provider}, nil
}

func (s *workspaceReviewHandlerStore) ResumeSession(_ context.Context, _ domain.Principal, _, sessionID string) (domain.SandboxLifecycle, error) {
	return domain.SandboxLifecycle{SessionID: sessionID}, nil
}

func (s *workspaceReviewHandlerStore) CreateWorkspaceRequest(_ context.Context, _ domain.Principal, orgID, sessionID, kind string, payload json.RawMessage, timeout time.Duration) (domain.WorkerRequest, error) {
	s.createdKind, s.createdPayload = kind, payload
	s.requestTimeout = timeout
	return domain.WorkerRequest{ID: "00000000-0000-4000-8000-000000000003", OrgID: orgID, SessionID: sessionID}, nil
}

func (s *workspaceReviewHandlerStore) GetWorkspaceRequest(_ context.Context, _ domain.Principal, _, _, _ string) (domain.WorkerRequest, error) {
	if s.errorCode != "" {
		return domain.WorkerRequest{Status: "failed", ErrorCode: s.errorCode, ErrorMessage: "stale"}, nil
	}
	response := s.modelResponse
	if response == nil {
		response, _ = json.Marshal(map[string]any{})
	}
	return domain.WorkerRequest{Status: "succeeded", Response: response}, nil
}

func (s *workspaceReviewHandlerStore) AppendSessionEvent(_ context.Context, _, _, eventType string, _ json.RawMessage) (domain.ClientEvent, error) {
	s.eventType = eventType
	return domain.ClientEvent{}, nil
}

func TestWorkspaceReviewHandlersDispatchForEveryProvider(t *testing.T) {
	operations := []struct {
		name    string
		method  string
		target  string
		body    any
		handler func(*Server, http.ResponseWriter, *http.Request)
		kind    string
	}{
		{"summary", http.MethodGet, "/workspace/review", nil, (*Server).getWorkspaceReview, "workspace.review.summary"},
		{"tree", http.MethodGet, "/workspace/tree?path=src", nil, (*Server).getWorkspaceReviewTree, "workspace.review.tree"},
		{"search", http.MethodGet, "/workspace/search?query=app&limit=20", nil, (*Server).getWorkspaceReviewSearch, "workspace.review.search"},
		{"file", http.MethodGet, "/workspace/review/file?path=README.md&scope=staged", nil, (*Server).getWorkspaceReviewFile, "workspace.review.file"},
		{"diffs", http.MethodPost, "/workspace/review/diffs", worker.WorkspaceReviewDiffsRequest{Scope: worker.WorkspaceReviewStaged, Paths: []string{"README.md"}, ContextLines: 3}, (*Server).postWorkspaceReviewDiffs, "workspace.review.diffs"},
		{"revision", http.MethodGet, "/workspace/review/revision?path=README.md&scope=combined&side=after&workspaceVersion=v1", nil, (*Server).getWorkspaceReviewRevision, "workspace.review.revision"},
		{"write", http.MethodPut, "/workspace/review/file", worker.WorkspaceReviewWriteRequest{Path: "README.md", Content: "new\n", ExpectedFileFingerprint: "fp"}, (*Server).putWorkspaceReviewFile, "workspace.review.write"},
		{"chat models", http.MethodGet, "/chat-models", nil, (*Server).getChatModels, "chat.models"},
	}
	for _, provider := range []string{sandbox.ProviderDocker, sandbox.ProviderNodeOps, sandbox.ProviderCoder} {
		for _, operation := range operations {
			t.Run(provider+"/"+operation.name, func(t *testing.T) {
				store := &workspaceReviewHandlerStore{provider: provider}
				server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}
				request := workspaceReviewRequest(t, operation.method, operation.target, operation.body)
				recorder := httptest.NewRecorder()
				operation.handler(server, recorder, request)
				if recorder.Code != http.StatusOK || store.createdKind != operation.kind {
					t.Fatalf("status=%d kind=%q body=%s", recorder.Code, store.createdKind, recorder.Body.String())
				}
				if operation.name == "write" && store.eventType != "workspace.changed" {
					t.Fatalf("write event = %q", store.eventType)
				}
			})
		}
	}
}

func TestWorkspaceReviewHandlerMapsStaleSnapshotToConflict(t *testing.T) {
	store := &workspaceReviewHandlerStore{provider: sandbox.ProviderDocker, errorCode: "WORKSPACE_SNAPSHOT_STALE"}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}
	request := workspaceReviewRequest(t, http.MethodPost, "/workspace/review/diffs", worker.WorkspaceReviewDiffsRequest{
		Scope: worker.WorkspaceReviewCombined, Paths: []string{"README.md"}, WorkspaceVersion: "stale",
	})
	recorder := httptest.NewRecorder()
	server.postWorkspaceReviewDiffs(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestListUserAgentModelsUsesCurrentUsersLiveWorker(t *testing.T) {
	store := &workspaceReviewHandlerStore{
		modelOrgID: workspaceTestOrgID, modelSessionID: workspaceTestSessionID,
		modelResponse: json.RawMessage(`{"models":[{"id":"gpt-supported","displayName":"Supported","default":true,"efforts":["low","high"]}]}`),
	}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}
	request := workspaceReviewRequest(t, http.MethodGet, "/orgs/"+workspaceTestOrgID+"/providers/codex/models", nil)
	routeContext := chi.RouteContext(request.Context())
	routeContext.URLParams.Add("agent", "codex")
	recorder := httptest.NewRecorder()

	server.listUserAgentModels(recorder, request)

	if recorder.Code != http.StatusOK || store.createdKind != "chat.models" {
		t.Fatalf("status=%d kind=%q body=%s", recorder.Code, store.createdKind, recorder.Body.String())
	}
	if store.modelOrgID != workspaceTestOrgID || store.requestTimeout < 30*time.Second {
		t.Fatalf("catalog scope=%q timeout=%s, want requested org and time for worker discovery", store.modelOrgID, store.requestTimeout)
	}
	var response worker.ChatModelsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Models) != 1 || response.Models[0].ID != "gpt-supported" || len(response.Models[0].Efforts) != 2 {
		t.Fatalf("models = %+v", response.Models)
	}
}

func TestListUserAgentModelsRejectsMalformedWorkerCatalog(t *testing.T) {
	for _, response := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`null`),
		json.RawMessage(`{"models":null}`),
		json.RawMessage(`{"models":{}}`),
		json.RawMessage(`{"models":[{"id":"","displayName":"No id"}]}`),
		json.RawMessage(`{"models":[{"id":"one","displayName":" "}]}`),
		json.RawMessage(`{"models":[{"id":"one","displayName":"One"},{"id":"one","displayName":"Duplicate"}]}`),
	} {
		store := &workspaceReviewHandlerStore{
			modelOrgID: workspaceTestOrgID, modelSessionID: workspaceTestSessionID,
			modelResponse: response,
		}
		server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}
		request := workspaceReviewRequest(t, http.MethodGet, "/orgs/"+workspaceTestOrgID+"/providers/codex/models", nil)
		chi.RouteContext(request.Context()).URLParams.Add("agent", "codex")
		recorder := httptest.NewRecorder()

		server.listUserAgentModels(recorder, request)

		if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "INVALID_WORKER_RESPONSE") {
			t.Fatalf("response=%s status=%d body=%s", response, recorder.Code, recorder.Body.String())
		}
	}
}

func TestListUserAgentModelsDoesNotReuseCatalogAfterConnectionChange(t *testing.T) {
	store := &workspaceReviewHandlerStore{modelSourceErr: postgres.ErrNotFound}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}
	request := workspaceReviewRequest(t, http.MethodGet, "/orgs/"+workspaceTestOrgID+"/providers/codex/models", nil)
	routeContext := chi.RouteContext(request.Context())
	routeContext.URLParams.Add("agent", "codex")
	recorder := httptest.NewRecorder()

	server.listUserAgentModels(recorder, request)

	if recorder.Code != http.StatusConflict || store.createdKind != "" || !strings.Contains(recorder.Body.String(), "MODEL_CATALOG_UNAVAILABLE") {
		t.Fatalf("status=%d kind=%q body=%s", recorder.Code, store.createdKind, recorder.Body.String())
	}
}

func TestListUserAgentModelsEmptyAndUnsupportedCatalogsRemainUnavailable(t *testing.T) {
	for _, agent := range []string{"codex", "cursor", "opencode"} {
		t.Run(agent, func(t *testing.T) {
			store := &workspaceReviewHandlerStore{modelSessionID: workspaceTestSessionID, modelResponse: json.RawMessage(`{"models":[]}`)}
			server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}
			request := workspaceReviewRequest(t, http.MethodGet, "/providers/"+agent+"/models", nil)
			chi.RouteContext(request.Context()).URLParams.Add("agent", agent)
			recorder := httptest.NewRecorder()
			server.listUserAgentModels(recorder, request)
			if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "MODEL_CATALOG_UNAVAILABLE") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if agent != "codex" && store.createdKind != "" {
				t.Fatalf("unsupported agent dispatched worker request %q", store.createdKind)
			}
		})
	}
}

func workspaceReviewRequest(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("orgId", workspaceTestOrgID)
	routeContext.URLParams.Add("sessionId", workspaceTestSessionID)
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	return request.WithContext(context.WithValue(request.Context(), principalKey, domain.Principal{UserID: "user-1"}))
}

// ?repository= selects which checkout a workspace request is about, and is
// left out of a checkout request, which always concerns the main repository.
func TestWorkspaceRequestsForwardTheRepositorySelector(t *testing.T) {
	store := &workspaceReviewHandlerStore{provider: sandbox.ProviderCoder}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}

	recorder := httptest.NewRecorder()
	server.getWorkspaceReviewTree(recorder, workspaceReviewRequest(t, http.MethodGet, "/workspace/tree?path=src&repository=acme/voice-calling-ai", nil))
	if recorder.Code != http.StatusOK || string(store.createdPayload) != `{"path":"src","repository":"acme/voice-calling-ai"}` {
		t.Fatalf("status=%d payload=%s", recorder.Code, store.createdPayload)
	}

	recorder = httptest.NewRecorder()
	server.getWorkspaceReview(recorder, workspaceReviewRequest(t, http.MethodGet, "/workspace/review?repository=web", nil))
	if recorder.Code != http.StatusOK || string(store.createdPayload) != `{"repository":"web"}` {
		t.Fatalf("summary payload=%s", store.createdPayload)
	}

	recorder = httptest.NewRecorder()
	server.getWorkspaceReview(recorder, workspaceReviewRequest(t, http.MethodGet, "/workspace/review", nil))
	if string(store.createdPayload) != `{}` {
		t.Fatalf("no selector payload=%s", store.createdPayload)
	}

	for _, bad := range []string{"../etc", "a/b/c", "a%20b", "..", "a/"} {
		recorder = httptest.NewRecorder()
		store.createdKind = ""
		server.getWorkspaceReview(recorder, workspaceReviewRequest(t, http.MethodGet, "/workspace/review?repository="+bad, nil))
		if recorder.Code != http.StatusBadRequest || store.createdKind != "" {
			t.Errorf("%q: status=%d kind=%q", bad, recorder.Code, store.createdKind)
		}
	}

	payload, ok := withWorkspaceRepository(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/workspace/checkout?repository=web", nil), "workspace.checkout", json.RawMessage(`{}`))
	if !ok || string(payload) != `{}` {
		t.Fatalf("checkout payload=%s", payload)
	}
}

func TestWorkspaceRepositoriesListsTheMainCheckoutForASingleRepoSession(t *testing.T) {
	store := &workspaceReviewHandlerStore{provider: sandbox.ProviderCoder}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}
	recorder := httptest.NewRecorder()
	server.getWorkspaceRepositories(recorder, workspaceReviewRequest(t, http.MethodGet, "/workspace/repositories?repository=web", nil))
	if recorder.Code != http.StatusOK || store.createdKind != "workspace.review.summary" || string(store.createdPayload) != `{}` {
		t.Fatalf("status=%d kind=%q payload=%s", recorder.Code, store.createdKind, store.createdPayload)
	}
	var response struct {
		Repositories []map[string]any `json:"repositories"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Repositories) != 1 || response.Repositories[0]["main"] != true {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}
