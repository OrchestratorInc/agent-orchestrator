package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// installedCleanupStore serves a launch spec and records the Cleanup commands
// stored for the worker it was given to.
type installedCleanupStore struct {
	Store
	launch domain.WorkerLaunch

	mu       sync.Mutex
	recorded []recordedCleanup
}

type recordedCleanup struct {
	orgID, sessionID, workerID string
	epoch                      int64
	commands                   []string
}

func (s *installedCleanupStore) WorkerLaunchSpec(context.Context, string, string) (domain.WorkerLaunch, error) {
	return s.launch, nil
}

func (s *installedCleanupStore) RedeemWorkerBootstrapTicket(context.Context, string) (domain.AccessTicket, error) {
	return domain.AccessTicket{OrgID: testOrgID, SessionID: testOrchestratorID, WorkerEpoch: 7, Scopes: []string{"worker:connect"}}, nil
}

func (s *installedCleanupStore) RegisterWorkerBootstrap(context.Context, string, string, string, string, int64, []string) error {
	return nil
}

func (s *installedCleanupStore) AppendSessionEvent(context.Context, string, string, string, json.RawMessage) (domain.ClientEvent, error) {
	return domain.ClientEvent{}, nil
}

func (s *installedCleanupStore) RecordInstalledCleanupCommands(_ context.Context, orgID, sessionID, workerID string, epoch int64, commands []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorded = append(s.recorded, recordedCleanup{orgID, sessionID, workerID, epoch, commands})
	return nil
}

// The Cleanup commands a worker is given when it bootstraps and whenever it
// reconnects (a restart, or a reopened session refreshing its scripts, after
// which it reinstalls its hook) are stored for that worker, so a Cleanup that
// runs them after the project's config changed is read knowing them.
func TestWorkerConnectionsStoreTheirCleanupCommands(t *testing.T) {
	launched := []string{`export DEPLOY_TOKEN=FAKEVAL96x; printf '%s\n' "$DEPLOY_TOKEN"`}
	config, err := json.Marshal(map[string]any{"preRemove": launched})
	if err != nil {
		t.Fatal(err)
	}
	store := &installedCleanupStore{launch: domain.WorkerLaunch{
		SessionID: testOrchestratorID, ProjectID: "project-1", ProjectConfig: config, Kind: "worker", Harness: "codex",
		RepositoryURL: "https://github.com/octo/widgets.git", DefaultBranch: "main",
	}}
	srv := testServer(store)
	srv.workerTokens = worker.NewTokenManager([]byte(strings.Repeat("k", 32)))

	w := httptest.NewRecorder()
	srv.workerBootstrap(w, httptest.NewRequest(http.MethodPost, "/worker/bootstrap", strings.NewReader(`{"bootstrapToken":"ticket"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d; body = %s", w.Code, w.Body.String())
	}
	var bootstrap worker.BootstrapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &bootstrap); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	srv.workerReconnect(w, workerRequest(t, http.MethodGet, "/worker/reconnect", "", "worker:connect"))
	if w.Code != http.StatusOK {
		t.Fatalf("reconnect status = %d; body = %s", w.Code, w.Body.String())
	}
	if len(store.recorded) != 2 {
		t.Fatalf("recorded = %+v", store.recorded)
	}
	for index, want := range []recordedCleanup{
		{testOrgID, testOrchestratorID, bootstrap.WorkerID, 7, bootstrap.Launch.PreRemove},
		{testOrgID, testOrchestratorID, "w1", 1, launched},
	} {
		got := store.recorded[index]
		if got.orgID != want.orgID || got.sessionID != want.sessionID || got.workerID != want.workerID || got.epoch != want.epoch ||
			strings.Join(got.commands, "\n") != strings.Join(launched, "\n") || strings.Join(want.commands, "\n") != strings.Join(launched, "\n") {
			t.Fatalf("recorded %d = %+v, want %+v", index, got, want)
		}
	}
}
