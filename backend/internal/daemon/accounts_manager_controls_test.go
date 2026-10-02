package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestAccountsManagerControlProductionDependency(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "daemon.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		selector, ok := literal.Type.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "APIDeps" {
			return true
		}
		for _, element := range literal.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := field.Key.(*ast.Ident)
			if ok && key.Name == "AccountsManagerControls" {
				value, ok := field.Value.(*ast.Ident)
				found = ok && value.Name == "sessionSvc"
			}
		}
		return true
	})
	if !found {
		t.Fatal("production daemon omits the coordinated session service from AccountsManagerControls")
	}
}

type publicControlCatalog struct {
	*core.ManagementClient
	mu          sync.Mutex
	credentials []core.CredentialSummary
	bindings    core.BindingSnapshot
	removed     []string
	listErr     error
}

func (c *publicControlCatalog) ListCredentials(context.Context) ([]core.CredentialSummary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]core.CredentialSummary(nil), c.credentials...), c.listErr
}

func (*publicControlCatalog) CredentialPublicID(ref string) (string, error) { return ref, nil }

func (c *publicControlCatalog) SynchronizeBindings(_ context.Context, snapshot core.BindingSnapshot) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bindings = snapshot
	return nil
}

func (c *publicControlCatalog) RemoveCredential(_ context.Context, ref string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removed = append(c.removed, ref)
	for i, credential := range c.credentials {
		if credential.Ref == ref {
			c.credentials = append(c.credentials[:i], c.credentials[i+1:]...)
			break
		}
	}
	return nil
}

type publicControlRuntime struct {
	runtimeselect.Runtime
	mu        sync.Mutex
	destroyed int
	owners    map[string]ports.FencedRuntimeRef
}

func (*publicControlRuntime) LaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	return []ports.RuntimeHandle{{ID: "target-" + string(id)}}, nil
}

func (*publicControlRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return "Working on the current request", nil
}

func (r *publicControlRuntime) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	return r.GetOutput(ctx, handle, lines)
}

func (r *publicControlRuntime) ProbeFencedRuntime(_ context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, found := r.owners[ref.Handle.ID]
	if !found {
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	if owner != ref {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonGenerationMismatch}
	}
	return ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
}

func (r *publicControlRuntime) IsAlive(_ context.Context, handle ports.RuntimeHandle) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, alive := r.owners[handle.ID]
	return alive, nil
}

func (r *publicControlRuntime) IsChildAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	return r.IsAlive(ctx, handle)
}

func (r *publicControlRuntime) Destroy(_ context.Context, handle ports.RuntimeHandle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.destroyed++
	delete(r.owners, handle.ID)
	return nil
}

type publicControlFixture struct {
	store   *sqlite.Store
	manager *sessionmanager.Manager
	router  http.Handler
	runtime *publicControlRuntime
	catalog *publicControlCatalog
	events  *publicControlEvents
	seeded  int
}

type publicControlEvents struct {
	telemetryadapter.NoopSink
	mu     sync.Mutex
	events []ports.TelemetryEvent
}

func (s *publicControlEvents) Emit(_ context.Context, event ports.TelemetryEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func newPublicControlFixture(t *testing.T) *publicControlFixture {
	t.Helper()
	store := sqlitetest.MustOpen(t)
	cfg := config.Config{DataDir: t.TempDir()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := &publicControlRuntime{owners: make(map[string]ports.FencedRuntimeRef)}
	agents, err := buildAgentResolver(config.DefaultAgent, log)
	if err != nil {
		t.Fatal(err)
	}
	catalog := &publicControlCatalog{credentials: []core.CredentialSummary{
		{Ref: "account-a", Provider: core.ProviderCodex, Kind: core.CredentialOAuth, Status: core.CredentialActive},
		{Ref: "account-b", Provider: core.ProviderCodex, Kind: core.CredentialOAuth, Status: core.CredentialActive},
	}}
	accounts := accountsvc.New(catalog, store)
	ctx, cancel := context.WithCancel(t.Context())
	svc, _, owner, err := startSession(ctx, cfg, rt, store, lifecycle.New(store, nil), newSessionMessenger(store, rt, log), telemetryadapter.NoopSink{}, nil, agents, nil, nil, nil, nil, nil, nil, nil, nil, nil, accounts, log)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		join, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := owner.WaitAgentSwitchWorkers(join); err != nil {
			t.Error(err)
		}
	})
	controls, _ := any(svc).(controllers.AccountsManagerControls)
	events := &publicControlEvents{}
	router := httpd.NewRouterWithControl(cfg, log, nil, httpd.APIDeps{Sessions: svc, AccountsManagerService: accounts, AccountsManagerControls: controls, Telemetry: events}, httpd.ControlDeps{})
	return &publicControlFixture{store: store, manager: owner.(*sessionmanager.Manager), router: router, runtime: rt, catalog: catalog, events: events}
}

func (f *publicControlFixture) seed(t *testing.T, mode domain.AccountsManagerConnectionMode, account string) domain.AccountsManagerSessionRoute {
	t.Helper()
	now := time.Now().UTC()
	f.seeded++
	if err := f.store.UpsertProject(t.Context(), domain.ProjectRecord{ID: "controls", Path: t.TempDir(), RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	record, err := f.store.CreateSession(t.Context(), domain.SessionRecord{
		ProjectID: "controls", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now,
		Metadata: domain.SessionMetadata{RuntimeHandleID: fmt.Sprintf("private-runtime-%d", f.seeded), RuntimeLaunchID: fmt.Sprintf("private-generation-%d", f.seeded), WorkspacePath: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := f.store.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: record.ID, Provider: domain.AccountsManagerProviderCodex, Mode: mode, AccountID: account})
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.mu.Lock()
	f.runtime.owners[record.Metadata.RuntimeHandleID] = ports.FencedRuntimeRef{SessionID: record.ID, Handle: ports.RuntimeHandle{ID: record.Metadata.RuntimeHandleID}, Generation: record.Metadata.RuntimeLaunchID}
	f.runtime.mu.Unlock()
	return binding
}

func TestAccountsManagerControlProductionInUseRemoval(t *testing.T) {
	f := newPublicControlFixture(t)
	a := f.seed(t, domain.AccountsManagerManaged, "account-a")
	b := f.seed(t, domain.AccountsManagerManaged, "account-b")
	native := f.seed(t, domain.AccountsManagerNative, "")
	preview := f.request(t, http.MethodGet, "/accounts-manager/accounts/account-a/removal-impact", "", http.StatusOK)
	var impact controllers.AccountsManagerRemovalImpactResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &impact); err != nil {
		t.Fatal(err)
	}
	if len(impact.Sessions) != 1 || impact.Sessions[0].SessionID != string(a.SessionID) {
		t.Fatal("impact included an unrelated managed or native binding")
	}
	path := "/accounts-manager/accounts/account-a/removals"
	f.request(t, http.MethodPost, path, fmt.Sprintf(`{"operationId":"public-remove","expectedRevision":%d,"confirmed":false}`, impact.Revision), http.StatusBadRequest)
	f.request(t, http.MethodPost, path, fmt.Sprintf(`{"operationId":"public-remove","expectedRevision":%d,"confirmed":true}`, impact.Revision+1), http.StatusConflict)
	if _, found, err := f.store.GetAccountsManagerRemoval(t.Context(), "public-remove"); err != nil || found {
		t.Fatal("rejected confirmation created durable removal state")
	}
	body := fmt.Sprintf(`{"operationId":"public-remove","expectedRevision":%d,"confirmed":true}`, impact.Revision)
	f.request(t, http.MethodPost, path, body, http.StatusAccepted)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, found, err := f.store.GetAccountsManagerRemoval(t.Context(), "public-remove")
		if err != nil || !found {
			t.Fatalf("removal read: found=%v err=%v", found, err)
		}
		if op.Phase == domain.AccountsManagerRemovalComplete {
			break
		}
		if op.Phase == domain.AccountsManagerRemovalRecovery || time.Now().After(deadline) {
			t.Fatalf("real coordinator removal phase=%s code=%s", op.Phase, op.ErrorCode)
		}
		time.Sleep(time.Millisecond)
	}
	f.request(t, http.MethodGet, "/accounts-manager/removals/public-remove", "", http.StatusOK)
	f.request(t, http.MethodPost, path, body, http.StatusAccepted)
	f.request(t, http.MethodPost, "/accounts-manager/removals/public-remove/retry", "", http.StatusConflict)
	f.request(t, http.MethodPost, "/accounts-manager/removals/public-remove/cancel", "", http.StatusConflict)
	for _, preserved := range []domain.AccountsManagerSessionRoute{b, native} {
		current, found, err := f.store.GetAccountsManagerSessionRoute(t.Context(), preserved.SessionID, preserved.Provider)
		if err != nil || !found || current.AccountID != preserved.AccountID || current.Mode != preserved.Mode || current.Revision != preserved.Revision || current.Blocked {
			t.Fatal("removal changed an unrelated session")
		}
	}
	f.catalog.mu.Lock()
	if len(f.catalog.removed) != 1 || f.catalog.removed[0] != "account-a" || len(f.catalog.credentials) != 1 || f.catalog.credentials[0].Ref != "account-b" {
		t.Error("credential removal was duplicated or selected another account")
	}
	f.catalog.mu.Unlock()
	f.runtime.mu.Lock()
	defer f.runtime.mu.Unlock()
	if f.runtime.destroyed != 1 || len(f.runtime.owners) != 2 {
		t.Fatal("exact teardown did not preserve unrelated runtimes")
	}
}

func (f *publicControlFixture) request(t *testing.T, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
	if body == "" {
		req.Body = http.NoBody
	}
	req.RemoteAddr = "127.0.0.1:43210"
	req.Header.Set("X-Request-ID", "public-control-test")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	f.router.ServeHTTP(res, req)
	if res.Code != status {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, res.Code, status, res.Body.String())
	}
	if status >= http.StatusBadRequest {
		var body envelope.APIError
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.RequestID != "public-control-test" {
			t.Fatalf("error lost request ID: %v, %s", err, res.Body.String())
		}
	}
	for _, private := range []string{"private-runtime", "private-generation", "127.0.0.1:43210"} {
		if strings.Contains(res.Body.String(), private) {
			t.Fatal("public account control leaked private execution details")
		}
	}
	return res
}

func TestAccountsManagerControlProductionUnavailableRedaction(t *testing.T) {
	f := newPublicControlFixture(t)
	binding := f.seed(t, domain.AccountsManagerNative, "")
	f.catalog.mu.Lock()
	f.catalog.listErr = fmt.Errorf("private-endpoint secret-token: %w", core.ErrUnavailable)
	f.catalog.mu.Unlock()
	path := "/sessions/" + string(binding.SessionID) + "/account-switches"
	body := fmt.Sprintf(`{"operationId":"redaction-switch","expectedRevision":%d,"mode":"managed","accountId":"account-b","policy":"drain","newConversation":true}`, binding.Revision)
	res := f.request(t, http.MethodPost, path, body, http.StatusServiceUnavailable)
	if _, found, err := f.store.GetAccountsManagerSwitch(t.Context(), "redaction-switch"); err != nil || found {
		t.Fatalf("unavailable admission created an operation: found=%v err=%v", found, err)
	}
	if release, allowed := f.manager.AcquireSessionInput(binding.SessionID); !allowed {
		t.Fatal("unavailable admission retained an input fence")
	} else {
		release()
	}
	f.events.mu.Lock()
	defer f.events.mu.Unlock()
	if len(f.events.events) != 1 || f.events.events[0].Name != "ao.http.5xx" ||
		f.events.events[0].RequestID != "public-control-test" ||
		f.events.events[0].Payload["path"] != "/api/v1/sessions/{sessionId}/account-switches" {
		t.Fatal("unavailable telemetry lost its safe route or request correlation")
	}
	encoded, err := json.Marshal(f.events.events)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-endpoint", "secret-token", "private-runtime", "private-generation", string(binding.SessionID), "account-b", "redaction-switch"} {
		if strings.Contains(string(encoded), private) || strings.Contains(res.Body.String(), private) {
			t.Fatal("unavailable response or telemetry leaked private account details")
		}
	}
}

func TestAccountsManagerControlProductionSessionRead(t *testing.T) {
	f := newPublicControlFixture(t)
	for _, mode := range []domain.AccountsManagerConnectionMode{domain.AccountsManagerNative, domain.AccountsManagerManaged} {
		account := ""
		if mode == domain.AccountsManagerManaged {
			account = "account-a"
		}
		binding := f.seed(t, mode, account)
		res := f.request(t, http.MethodGet, "/sessions/"+string(binding.SessionID)+"/account", "", http.StatusOK)
		var got controllers.AccountsManagerSessionResponse
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Mode != string(mode) || got.AccountID != account || got.Revision != binding.Revision || got.Switch != nil {
			t.Fatalf("committed binding changed during read: %+v", got)
		}
	}
}

func TestAccountsManagerControlProductionSwitchCancellation(t *testing.T) {
	f := newPublicControlFixture(t)
	binding := f.seed(t, domain.AccountsManagerNative, "")
	path := "/sessions/" + string(binding.SessionID) + "/account-switches"
	body := fmt.Sprintf(`{"operationId":"public-switch","expectedRevision":%d,"mode":"managed","accountId":"account-b","policy":"drain","newConversation":true}`, binding.Revision)
	f.request(t, http.MethodPost, path, body, http.StatusAccepted)
	f.request(t, http.MethodPost, path, body, http.StatusAccepted)
	op, found, err := f.store.GetAccountsManagerSwitch(t.Context(), "public-switch")
	if err != nil || !found || op.SourceMode != domain.AccountsManagerNative || op.TargetAccountID != "account-b" {
		t.Fatalf("no durable real-manager admission: found=%v err=%v", found, err)
	}
	if release, allowed := f.manager.AcquireSessionInput(binding.SessionID); allowed {
		release()
		t.Fatal("coordinator did not fence input")
	}
	f.request(t, http.MethodPost, path+"/public-switch/cancel", "", http.StatusAccepted)
	f.request(t, http.MethodPost, path+"/public-switch/cancel", "", http.StatusAccepted)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if release, allowed := f.manager.AcquireSessionInput(binding.SessionID); allowed {
			release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled operation retained input fence")
		}
		time.Sleep(time.Millisecond)
	}
	op, _, err = f.store.GetAccountsManagerSwitch(t.Context(), op.ID)
	if err != nil || op.Phase != domain.AccountsManagerSwitchCancelled || op.TargetRevision != 0 {
		t.Fatalf("cancel did not preserve pre-stop state: %s %v", op.Phase, err)
	}
	f.request(t, http.MethodPost, path+"/public-switch/retry", "", http.StatusConflict)
	current, _, err := f.store.GetAccountsManagerSessionRoute(t.Context(), binding.SessionID, binding.Provider)
	if err != nil || current.Mode != binding.Mode || current.Revision != binding.Revision || current.Blocked {
		t.Fatalf("cancel changed committed route: %+v %v", current, err)
	}
	f.runtime.mu.Lock()
	defer f.runtime.mu.Unlock()
	if f.runtime.destroyed != 0 {
		t.Fatal("cancelled drain stopped the source")
	}
}
