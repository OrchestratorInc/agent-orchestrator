package httpapi

import (
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
	"github.com/aoagents/agent-orchestrator/cloud/internal/projectsnapshot"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/go-chi/chi/v5"
)

const autolinkProjectID = "00000000-0000-0000-0000-0000000000d4"
const autolinkOrgID = "00000000-0000-0000-0000-0000000000a1"

func TestCreateSessionInitialEffort(t *testing.T) {
	for _, effort := range []string{"medium", "unsupported"} {
		t.Run(effort, func(t *testing.T) {
			store := &stubAutolinkStore{}
			srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
			req := createSessionRequestHTTP(t, "worker", "")
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			req.Body = io.NopCloser(strings.NewReader(strings.TrimSuffix(string(body), "}") + `,"reasoningEffort":"` + effort + `"}`))
			recorder := httptest.NewRecorder()
			srv.createSession(recorder, req)
			if effort == "medium" {
				if recorder.Code != http.StatusCreated || store.captured.ReasoningEffort != effort {
					t.Fatalf("status=%d captured effort=%q body=%s", recorder.Code, store.captured.ReasoningEffort, recorder.Body)
				}
			} else if recorder.Code != http.StatusUnprocessableEntity || store.created {
				t.Fatalf("unsupported effort accepted: status=%d", recorder.Code)
			}
		})
	}
}

// stubAutolinkStore embeds Store (nil) so it satisfies the interface while
// implementing only the methods createSession reaches on the auto-link path.
type stubAutolinkStore struct {
	Store
	orchestratorID       string
	orchProvider         string
	orchFound            bool
	orchErr              error
	captured             domain.CreateSession
	created              bool
	preference           string
	preferenceConfigured bool
	preferenceReads      int
	projectConfig        json.RawMessage
	repositoryURL        string
}

func (s *stubAutolinkStore) GetUserSandboxProvider(_ context.Context, _ string) (string, bool, error) {
	s.preferenceReads++
	return s.preference, s.preferenceConfigured, nil
}

func (s *stubAutolinkStore) PutUserSandboxProvider(_ context.Context, _ string, provider string, initializeOnly bool) (string, error) {
	if initializeOnly && s.preferenceConfigured {
		return "", postgres.ErrConflict
	}
	s.preference = provider
	s.preferenceConfigured = true
	return provider, nil
}

func (s *stubAutolinkStore) ProjectActiveOrchestrator(
	_ context.Context, _, _ string,
) (string, string, bool, error) {
	return s.orchestratorID, s.orchProvider, s.orchFound, s.orchErr
}

func (s *stubAutolinkStore) GetProject(
	_ context.Context, _ domain.Principal, _, projectID string,
) (domain.Project, error) {
	return domain.Project{ID: projectID, Config: s.projectConfig, RepositoryURL: s.repositoryURL}, nil
}

func (s *stubAutolinkStore) UserAgentCredentialAvailable(
	_ context.Context, _, _ string,
) (bool, error) {
	return true, nil
}

func (s *stubAutolinkStore) CreateSession(
	_ context.Context, _ domain.Principal, _, _ string, _ int, input domain.CreateSession,
) (domain.Session, error) {
	s.created = true
	s.captured = input
	return domain.Session{ID: "00000000-0000-0000-0000-0000000000e5", Kind: input.Kind}, nil
}

func createSessionRequestHTTP(t *testing.T, kind, provider string) *http.Request {
	t.Helper()
	body := `{"projectId":"` + autolinkProjectID + `","kind":"` + kind +
		`","harness":"claude-code","displayName":"add-logger","prompt":"do the work","mode":"trusted"` +
		func() string {
			if provider == "" {
				return ""
			}
			return `,"provider":"` + provider + `"`
		}() + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/cloud/v1/orgs/"+autolinkOrgID+"/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "22222222-2222-2222-2222-222222222222")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgId", autolinkOrgID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, principalKey, domain.Principal{UserID: "00000000-0000-0000-0000-0000000000f6"})
	return req.WithContext(ctx)
}

// A worker created for a project that has an active orchestrator is auto-linked
// to it and inherits its provider, even when the client selected (or the
// control plane defaults to) a different provider. This is what makes the
// orchestrator see, drive, and receive reports from a UI-created worker.
func TestCreateSessionAutoLinksWorkerToProjectOrchestrator(t *testing.T) {
	t.Parallel()
	store := &stubAutolinkStore{
		orchestratorID: "00000000-0000-0000-0000-0000000000b2",
		orchProvider:   sandbox.ProviderCoder,
		orchFound:      true,
	}
	// Default provider nodeops, client asks for nodeops; the orchestrator runs
	// on coder, so the worker must come out coder and parented.
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)

	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "worker", sandbox.ProviderNodeOps))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if !store.created {
		t.Fatal("CreateSession was not called")
	}
	if store.captured.ParentSessionID != store.orchestratorID {
		t.Fatalf("ParentSessionID = %q, want %q", store.captured.ParentSessionID, store.orchestratorID)
	}
	if store.captured.Provider != sandbox.ProviderCoder {
		t.Fatalf("worker provider = %q, want %q (inherited from orchestrator)", store.captured.Provider, sandbox.ProviderCoder)
	}
}

// With no active orchestrator in the project, the worker stays standalone: no
// parent, and it keeps the client-selected provider.
func TestCreateSessionLeavesWorkerStandaloneWithoutOrchestrator(t *testing.T) {
	t.Parallel()
	store := &stubAutolinkStore{orchFound: false}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderCoder)

	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "worker", sandbox.ProviderNodeOps))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if store.captured.ParentSessionID != "" {
		t.Fatalf("ParentSessionID = %q, want empty (standalone)", store.captured.ParentSessionID)
	}
	if store.captured.Provider != sandbox.ProviderNodeOps {
		t.Fatalf("worker provider = %q, want %q (client selection)", store.captured.Provider, sandbox.ProviderNodeOps)
	}
}

// An orchestrator is never auto-linked to itself or another orchestrator: the
// project lookup must not run for kind=orchestrator.
func TestCreateSessionDoesNotAutoLinkOrchestrator(t *testing.T) {
	t.Parallel()
	// orchFound=true would link if the handler wrongly consulted it for an
	// orchestrator; assert it stays empty regardless.
	store := &stubAutolinkStore{
		orchestratorID: "00000000-0000-0000-0000-0000000000b2",
		orchProvider:   sandbox.ProviderCoder,
		orchFound:      true,
	}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)

	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "orchestrator", sandbox.ProviderNodeOps))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if store.captured.ParentSessionID != "" {
		t.Fatalf("orchestrator ParentSessionID = %q, want empty", store.captured.ParentSessionID)
	}
	if store.captured.Provider != sandbox.ProviderNodeOps {
		t.Fatalf("orchestrator provider = %q, want %q", store.captured.Provider, sandbox.ProviderNodeOps)
	}
}

func TestCreateSessionProviderPreferencePrecedence(t *testing.T) {
	cases := []struct {
		name, kind, explicit, parentProvider, preference, want string
		linked                                                 bool
		wantReads                                              int
	}{
		{"orchestrator uses preference", "orchestrator", "", "", sandbox.ProviderCoder, sandbox.ProviderCoder, false, 1},
		{"standalone worker uses preference", "worker", "", "", sandbox.ProviderCoder, sandbox.ProviderCoder, false, 1},
		{"explicit wins", "orchestrator", sandbox.ProviderNodeOps, "", sandbox.ProviderCoder, sandbox.ProviderNodeOps, false, 0},
		{"linked worker inherits", "worker", "", sandbox.ProviderNodeOps, sandbox.ProviderCoder, sandbox.ProviderNodeOps, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubAutolinkStore{preference: tc.preference, preferenceConfigured: true, orchFound: tc.linked, orchProvider: tc.parentProvider, orchestratorID: "00000000-0000-0000-0000-0000000000b2"}
			srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
			rec := httptest.NewRecorder()
			srv.createSession(rec, createSessionRequestHTTP(t, tc.kind, tc.explicit))
			if rec.Code != http.StatusCreated || store.captured.Provider != tc.want || store.preferenceReads != tc.wantReads {
				t.Fatalf("status=%d provider=%q reads=%d, want 201 %q %d; body=%s", rec.Code, store.captured.Provider, store.preferenceReads, tc.want, tc.wantReads, rec.Body.String())
			}
		})
	}
}

func TestCreateSessionProviderPreferenceUnavailable(t *testing.T) {
	store := &stubAutolinkStore{preference: "removed-provider", preferenceConfigured: true}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "orchestrator", ""))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "provider_unavailable") || store.created {
		t.Fatalf("status=%d created=%t body=%s", rec.Code, store.created, rec.Body.String())
	}
}

func TestCreateSessionProviderUnsetUsesDeploymentDefault(t *testing.T) {
	store := &stubAutolinkStore{}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "orchestrator", ""))
	if rec.Code != http.StatusCreated || store.captured.Provider != sandbox.ProviderNodeOps {
		t.Fatalf("status=%d provider=%q body=%s", rec.Code, store.captured.Provider, rec.Body.String())
	}
}

func TestCreateSessionProviderPreferenceAcrossDeviceFlows(t *testing.T) {
	store := &stubAutolinkStore{}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
	put := httptest.NewRecorder()
	srv.putUserPreferences(put, preferenceRequest(http.MethodPut, `{"sandboxProvider":"coder"}`))
	if put.Code != http.StatusOK {
		t.Fatalf("save preference: %d %s", put.Code, put.Body.String())
	}
	get := httptest.NewRecorder()
	srv.getUserPreferences(get, preferenceRequest(http.MethodGet, ""))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"sandboxProvider":"coder"`) {
		t.Fatalf("read preference: %d %s", get.Code, get.Body.String())
	}

	for _, kind := range []string{"orchestrator", "worker"} {
		rec := httptest.NewRecorder()
		srv.createSession(rec, createSessionRequestHTTP(t, kind, ""))
		if rec.Code != http.StatusCreated || store.captured.Provider != sandbox.ProviderCoder {
			t.Fatalf("%s: %d provider=%q body=%s", kind, rec.Code, store.captured.Provider, rec.Body.String())
		}
	}

	// A later account change does not move an existing orchestrator's tree.
	store.orchFound = true
	store.orchProvider = sandbox.ProviderCoder
	store.orchestratorID = "00000000-0000-0000-0000-0000000000b2"
	put = httptest.NewRecorder()
	srv.putUserPreferences(put, preferenceRequest(http.MethodPut, `{"sandboxProvider":"nodeops"}`))
	if put.Code != http.StatusOK {
		t.Fatalf("switch preference: %d %s", put.Code, put.Body.String())
	}
	linked := httptest.NewRecorder()
	srv.createSession(linked, createSessionRequestHTTP(t, "worker", ""))
	if linked.Code != http.StatusCreated || store.captured.Provider != sandbox.ProviderCoder || store.captured.ParentSessionID != store.orchestratorID {
		t.Fatalf("linked worker: %d provider=%q parent=%q body=%s", linked.Code, store.captured.Provider, store.captured.ParentSessionID, linked.Body.String())
	}
}

// Creating a session wakes the sandbox reconciler so provisioning starts now
// instead of at the next reconcile tick.
func TestCreateSessionWakesTheSandboxReconciler(t *testing.T) {
	t.Parallel()
	store := &stubAutolinkStore{}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
	wakes := 0
	srv.sandboxWake = func() { wakes++ }

	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "orchestrator", sandbox.ProviderNodeOps))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if wakes != 1 {
		t.Fatalf("sandbox wakes = %d, want 1", wakes)
	}
}

// A project set up for a sandbox provider runs its sessions there even when
// the client sends a different saved preference.
func TestCreateSessionUsesTheProjectSandboxProvider(t *testing.T) {
	t.Parallel()
	store := &stubAutolinkStore{
		projectConfig:        json.RawMessage(`{"sandboxProvider":"coder"}`),
		preference:           sandbox.ProviderNodeOps,
		preferenceConfigured: true,
	}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)

	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "orchestrator", sandbox.ProviderNodeOps))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if store.captured.Provider != sandbox.ProviderCoder {
		t.Fatalf("session provider = %q, want %q (from the project)", store.captured.Provider, sandbox.ProviderCoder)
	}
	if store.preferenceReads != 0 {
		t.Fatal("a project provider must override the account preference without reading it")
	}
}

type fakeProjectSnapshots struct {
	snapshotID string
	ensured    []projectsnapshot.Request
}

func (f *fakeProjectSnapshots) Lookup(_ context.Context, request projectsnapshot.Request) (string, bool) {
	if f.snapshotID == "" || request.BaseSnapshotID != "sh-harness" {
		return "", false
	}
	return f.snapshotID, true
}

func (f *fakeProjectSnapshots) Ensure(request projectsnapshot.Request) {
	f.ensured = append(f.ensured, request)
}

func freestyleServer(store Store, snapshots ProjectSnapshotter) *Server {
	return New(Options{
		Store:                     store,
		SandboxProvider:           sandbox.ProviderFreestyle,
		AvailableSandboxProviders: []string{sandbox.ProviderFreestyle},
		Provisioning: sandbox.ProvisioningDefaults{
			Provider: sandbox.ProviderFreestyle,
			Release:  "test",
			Freestyle: sandbox.FreestyleConfig{
				APIKey:          "key",
				DefaultSnapshot: "sh-harness",
				WorkerTokenTTL:  15 * time.Minute,
			},
		},
		ProjectSnapshots: snapshots,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// A Freestyle session boots from its project's prepared snapshot when one
// exists, and every Freestyle session keeps that snapshot fresh.
func TestCreateSessionBootsFromTheProjectSnapshot(t *testing.T) {
	t.Parallel()
	store := &stubAutolinkStore{repositoryURL: "https://github.com/acme/repo"}
	snapshots := &fakeProjectSnapshots{snapshotID: "sh-project"}
	srv := freestyleServer(store, snapshots)

	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "orchestrator", sandbox.ProviderFreestyle))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	got := sandbox.FreestyleSnapshot(sandbox.Plan{ResourceProfile: store.captured.ResourceProfile})
	if got != "sh-project" {
		t.Fatalf("session boots from %q, want the project snapshot", got)
	}
	if len(snapshots.ensured) != 1 || snapshots.ensured[0].SessionID == "" || snapshots.ensured[0].BaseSnapshotID != "sh-harness" ||
		snapshots.ensured[0].RepositoryKey != "url:acme/repo" {
		t.Fatalf("Ensure calls = %+v; want one, keyed to the new session, the repository and the harness snapshot", snapshots.ensured)
	}
}

// A project whose repository is not on GitHub cannot be keyed to a snapshot,
// so its sessions boot from the harness snapshot and none is built.
func TestCreateSessionWithoutAGitHubRepositorySkipsTheProjectSnapshot(t *testing.T) {
	t.Parallel()
	store := &stubAutolinkStore{}
	snapshots := &fakeProjectSnapshots{snapshotID: "sh-project"}
	srv := freestyleServer(store, snapshots)

	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "orchestrator", sandbox.ProviderFreestyle))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if got := sandbox.FreestyleSnapshot(sandbox.Plan{ResourceProfile: store.captured.ResourceProfile}); got != "sh-harness" || len(snapshots.ensured) != 0 {
		t.Fatalf("boots from %q with %d builds; want the harness snapshot and no build", got, len(snapshots.ensured))
	}
}

// Without a project snapshot the session boots from the harness snapshot.
func TestCreateSessionFallsBackToTheHarnessSnapshot(t *testing.T) {
	t.Parallel()
	store := &stubAutolinkStore{}
	srv := freestyleServer(store, &fakeProjectSnapshots{})

	rec := httptest.NewRecorder()
	srv.createSession(rec, createSessionRequestHTTP(t, "orchestrator", sandbox.ProviderFreestyle))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if got := sandbox.FreestyleSnapshot(sandbox.Plan{ResourceProfile: store.captured.ResourceProfile}); got != "sh-harness" {
		t.Fatalf("session boots from %q, want the harness snapshot", got)
	}
}
