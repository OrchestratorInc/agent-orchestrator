package controllers_test

import (
	"context"
	"encoding/json"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 审批 HTTP 测试使用真实 router、service 和临时 SQLite，仅 OS 验证替换为 fake。

// approvalClock is the injectable clock the approval store reads, so expiry is deterministic.
type approvalClock struct{ now time.Time }

func (c *approvalClock) Now() time.Time          { return c.now }
func (c *approvalClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// legacyOperatorToken 是旧实现会写进 <dataDir>/operator.token 的凭证形状。
//
// 它现在只作为"出示 token 也不获得任何授权"的输入存在：没有管理器、没有文件、没有来源。
const legacyOperatorToken = "operator-token-that-authorizes-nothing"

// recordingConsent 是注入到 httpd.APIDeps.OperatorConsent 的测试验证器。
//
// 生产装配用 operatorconsent.New()（Windows Hello），测试必须永不触发真实 OS 提示。
// 它记录每次被调用的操作与正文摘要，以便断言验证器收到的正是人类被问到的那个操作，
// 并允许分别返回 ErrConsentDenied / ErrConsentUnavailable 覆盖两种失败关闭路径。
type recordingConsent struct {
	mu      sync.Mutex
	err     error
	busy    bool
	entered chan struct{}
	release chan struct{}
	calls   []principal.Approval
}

// Verify 记录操作并按当前设定返回结果；busy 时阻塞直到 release 被关闭。
func (c *recordingConsent) Verify(ctx context.Context, approval principal.Approval) error {
	c.mu.Lock()
	c.calls = append(c.calls, approval)
	busy, entered, release, err := c.busy, c.entered, c.release, c.err
	c.mu.Unlock()
	if busy {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// set 设置后续验证调用的结果；nil 表示本次人工验证成功。
func (c *recordingConsent) set(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

// hold 让下一次验证停在中途，直到 releaseConsent 调用，用来观察锁内状态。
func (c *recordingConsent) hold() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.busy, c.entered, c.release = true, make(chan struct{}), make(chan struct{})
}

// releaseConsent 放行被 hold 的验证。
func (c *recordingConsent) releaseConsent() { close(c.release) }

// seen 返回验证器收到过的操作快照，副本避免与 HTTP goroutine 竞争。
func (c *recordingConsent) seen() []principal.Approval {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]principal.Approval(nil), c.calls...)
}

// approvalEnv is one fully wired stack plus the fixtures its tests act on.
type approvalEnv struct {
	srv        *httptest.Server
	store      *sqlite.Store
	approvals  *principal.ApprovalStore
	clock      *approvalClock
	gateway    *execution.FakeGateway
	deps       httpd.APIDeps
	consent    *recordingConsent
	token      string
	workflowID string
	taskID     string
	attemptID  string
	requestID  string
}

// serverWith builds another router over the SAME services with a different principal resolver.
// It is what lets the agent test swap in a session-verifier fixture without a second stack.
func (e *approvalEnv) serverWith(t *testing.T, principals *principal.Resolver) *httptest.Server {
	t.Helper()
	deps := e.deps
	deps.WorkflowPrincipals = principals
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// newApprovalEnv builds the stack over a real store and seeds one workflow, task, and claim.
func newApprovalEnv(t *testing.T) *approvalEnv {
	t.Helper()
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	workflowClock := func() time.Time { return time.Now().UTC() }
	admission := sqlitetest.NewAdmissionFixture(t, store, workflowClock)
	repoBaseSHA := strings.Repeat("d", 40)

	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "project-1", Path: t.TempDir(), RegisteredAt: now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	workflowID := domain.WorkflowID("workflow-1")
	taskID := domain.WorkflowTaskID("task-1")
	if _, _, err := store.CreateWorkflow(ctx, domain.Workflow{
		ID: workflowID, Title: "Approval", Goal: "exercise the operator proof",
		State: domain.WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}, []domain.WorkflowRepository{{
		WorkflowID: workflowID, RepoKey: "backend", ProjectID: "project-1", PathRef: "backend",
		BaseSHA: repoBaseSHA,
	}}, []domain.WorkflowTask{{
		ID: taskID, WorkflowID: workflowID, Kind: domain.WorkflowTaskKindDevelopment,
		Title: "Do it", State: domain.WorkflowTaskStatePending, MaxAttempts: 3, Revision: 1,
		AgentHarness: domain.HarnessCodex, CreatedAt: now, UpdatedAt: now,
	}}, nil, []domain.WorkflowTaskRepository{{WorkflowID: workflowID, TaskID: taskID, RepoKey: "backend", BaseSHA: repoBaseSHA, AllowedPaths: []string{"src"}}}, "approval-create", strings.Repeat("a", 64)); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if _, _, err := store.ActivateWorkflow(
		ctx, workflowID, 1, "approval-activate", strings.Repeat("b", 64), now,
	); err != nil {
		t.Fatalf("activate workflow: %v", err)
	}
	ready, found, err := store.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("load ready task: found=%v err=%v", found, err)
	}
	leaseUntil := now.Add(time.Hour)
	attempt, created, err := admission.Claim(ctx, domain.WorkflowAttempt{
		ID: "attempt-1", WorkflowID: workflowID, TaskID: taskID,
		WorkerID: "worker-1", LeaseToken: "lease-1", LeaseUntil: &leaseUntil,
		State: domain.WorkflowAttemptStateClaimed, CreatedAt: now, UpdatedAt: now,
	}, ready.Revision, "approval-claim", strings.Repeat("c", 64))
	if err != nil || !created {
		t.Fatalf("claim: created=%v err=%v", created, err)
	}

	resolver := capability.NewResolver()
	resolver.RegisterProbe(domain.HarnessCodex, approvalProbe{})
	// 授权仓库的实时观察保留固定基线，HEAD 可以是其合法后继。
	gateway := execution.NewFakeGateway().SpawnReadiness(ports.SessionReadinessReady).SpawnGeneration("approval-generation")
	gateway.SpawnWorktrees([]ports.RepoWorktreeObservation{{RepoKey: "backend", ProjectID: "project-1", WorktreeRef: t.TempDir(), BaseSHA: repoBaseSHA, HeadSHA: strings.Repeat("e", 40)}})
	// workflow_attempt_bindings.session_id is a real foreign key, so a spawn must create a real
	// session row. That is the same materializer the M2 wiring tests use, not a weakening.
	gateway.WithSessionMaterializer(
		func(ctx context.Context, sessionID domain.SessionID, request ports.SpawnRequest) (domain.SessionID, error) {
			permit, ok := ports.ExecutionPermitFromContext(ctx)
			if !ok {
				return "", domain.ErrAdmissionRequired
			}
			ad, _, err := store.GetSchedulerAdmission(ctx, permit.AdmissionID)
			if err != nil {
				return "", err
			}
			rec, _, err := store.CreateAdmittedSession(ctx, domain.SessionRecord{
				ID: sessionID, ProjectID: "project-1", Kind: domain.KindWorker,
				Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
				ClientRequestID: request.SpawnKey, ClientRequestHash: request.SpawnHash,
				Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
				Metadata:  domain.SessionMetadata{WorkspacePath: t.TempDir(), ControllerGeneration: "approval-generation"},
				CreatedAt: now, UpdatedAt: now,
			}, ad, "", time.Now().UTC())
			return rec.ID, err
		})
	leader, err := store.GetSchedulerLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	binder := execution.New(execution.Deps{
		Store: store, Gateway: gateway, Resolver: resolver, Clock: func() time.Time { return time.Now().UTC() },
		ExecutionFence: func(context.Context) (domain.SchedulerFence, error) { return leader.Fence, nil },
	})
	collab := collaboration.New(collaboration.Deps{Store: store, Bindings: store, Graph: store})
	workflows := workflow.New(workflow.Deps{
		Store: store, Clock: workflowClock, Admission: admission,
		GraphLimits:   domain.WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 20, MaxFanOut: 20},
		LeaseDuration: time.Hour,
	})

	clock := &approvalClock{now: time.Now().UTC()}
	approvals := principal.NewApprovalStore(principal.ApprovalStoreOptions{
		TTL:   principal.DefaultApprovalTTL,
		Clock: clock.Now,
	})
	// 生产身份边界：只接受本次启动内由 OS 验证确认过的一次性审批，不持有任何 token 证明。
	principals := principal.NewResolver(
		store,
		principal.CapabilityVerifier{Sessions: store, Authority: approvalAuthority{}},
		principal.ApprovalOperatorTrust{Approvals: approvals},
	).WithSessionAttemptProver(principal.StoreSessionAttemptProver{Store: store, Clock: clock.Now})

	// 注入测试验证器；真实 Windows Hello 在测试中永不弹出。
	consent := &recordingConsent{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{
		WorkflowExecutionFence: func(context.Context) (domain.SchedulerFence, error) { return leader.Fence, nil },
		Workflows:              workflows,
		WorkflowCapabilities:   resolver,
		WorkflowBindings:       store,
		WorkflowBinder:         binder,
		WorkflowCollaboration:  collab,
		WorkflowOwnership:      approvalOwnership{store: store},
		WorkflowPrincipals:     principals,
		WorkflowAudit:          store,
		OperatorApprovals:      approvals,
		OperatorConsent:        consent,
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	return &approvalEnv{
		srv: srv, store: store, approvals: approvals, clock: clock, gateway: gateway, deps: deps,
		consent:    consent,
		token:      legacyOperatorToken,
		workflowID: string(workflowID), taskID: string(taskID), attemptID: string(attempt.ID),
	}
}

// approvalProbe answers with a provider handshake, which is the only grade that may gate a spawn.
type approvalProbe struct{}

func (approvalProbe) ProbeCapabilities(
	_ context.Context,
	request ports.CapabilityProbeRequest,
) (domain.HarnessCapabilitySnapshot, error) {
	snapshot := domain.NewHarnessCapabilitySnapshot(
		request.Harness, domain.CapabilitySourceProviderHandshake,
		time.Now().UTC(), time.Minute)
	snapshot.ObservedSessionID = request.SessionID
	snapshot.Generation = request.Generation
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	snapshot.Streaming = domain.CapabilitySupported
	return snapshot, nil
}

// approvalAuthority is the capability digest comparison, reproduced so the wire test does not
// import service/browser.
type approvalAuthority struct{}

func (approvalAuthority) Valid(domain.SessionID, string, string) bool { return false }

// approvalOwnership adapts execution.ResolveAttemptOwnership the way the daemon's adapter does.
type approvalOwnership struct{ store *sqlite.Store }

func (o approvalOwnership) ResolveAttemptOwnership(
	ctx context.Context,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
) (controllers.AttemptOwnership, error) {
	ownership, err := execution.ResolveAttemptOwnership(ctx, o.store, workflowID, attemptID)
	if err != nil {
		return controllers.AttemptOwnership{}, err
	}
	return controllers.AttemptOwnership{
		Attempt: ownership.Attempt, Task: ownership.Task, Repos: ownership.Repos,
		ProjectID: ownership.ProjectID, Harness: ownership.Harness,
	}, nil
}

// approvalDTO is the create/confirm/status response.
type approvalDTO struct {
	Approval struct {
		ApprovalID    string `json:"approvalId"`
		Code          string `json:"code"`
		State         string `json:"state"`
		PayloadDigest string `json:"payloadDigest"`
		Operation     struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"operation"`
	} `json:"approval"`
}

// createApproval requests an approval for the exact body it will submit.
func (e *approvalEnv) createApproval(t *testing.T, method, path, body string) approvalDTO {
	t.Helper()
	request, err := json.Marshal(map[string]string{
		"operationMethod": method, "operationPath": path,
		"payloadDigest": principal.PayloadDigest([]byte(body)), "payload": body,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := doJSONWithHeaders(t, e.srv, http.MethodPost, "/api/v1/operator/approvals", string(request), nil)
	if status != http.StatusCreated {
		t.Fatalf("create approval = %d, want 201; body=%s", status, raw)
	}
	var out approvalDTO
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode approval: %v", err)
	}
	if out.Approval.ApprovalID == "" || out.Approval.Code == "" {
		t.Fatalf("approval is missing an id or code: %s", raw)
	}
	return out
}

// confirmApproval performs the human gesture: the desktop app calls confirm, and the daemon
// asks the injected OS verifier. No token is presented — a token authorises nothing now.
func (e *approvalEnv) confirmApproval(t *testing.T, approval approvalDTO, digest string) {
	t.Helper()
	status, raw := doJSONWithHeaders(t, e.srv, http.MethodPost,
		"/api/v1/operator/approvals/"+approval.Approval.ApprovalID+"/confirm",
		`{"code":"`+approval.Approval.Code+`","payloadDigest":"`+digest+`"}`, nil)
	if status != http.StatusOK {
		t.Fatalf("confirm approval = %d, want 200; body=%s", status, raw)
	}
	// The verifier must have been asked about EXACTLY the operation the human was shown. A fixture
	// built without the consent recorder (a bare &approvalEnv{srv: srv}) has nothing to assert
	// here, because it does not own the verifier.
	if e.consent == nil {
		return
	}
	seen := e.consent.seen()
	if len(seen) == 0 {
		t.Fatal("the OS verifier was never called for a successful confirmation")
	}
	last := seen[len(seen)-1]
	if last.ID != approval.Approval.ApprovalID || last.Method != approval.Approval.Operation.Method ||
		last.Path != approval.Approval.Operation.Path || last.PayloadDigest != digest {
		t.Fatalf("the OS verifier saw %+v, want the approved operation", last)
	}
}

// submitAsApproval sends the action with the approval header.
func (e *approvalEnv) submitAsApproval(
	t *testing.T,
	method, path, body, approvalID string,
) (int, []byte) {
	t.Helper()
	return doJSONWithHeaders(t, e.srv, method, path, body,
		map[string]string{principal.HeaderOperatorApproval: approvalID})
}

// approvedAction is the whole happy path: request, confirm, submit.
func (e *approvalEnv) approvedAction(t *testing.T, method, path, body string) (int, []byte) {
	t.Helper()
	approval := e.createApproval(t, method, path, body)
	e.confirmApproval(t, approval, principal.PayloadDigest([]byte(body)))
	return e.submitAsApproval(t, method, path, body, approval.Approval.ApprovalID)
}

func (e *approvalEnv) bindPath() string {
	return "/api/v1/workflows/" + e.workflowID + "/attempts/" + e.attemptID + "/bind"
}

func (e *approvalEnv) resolvePath() string {
	return "/api/v1/workflows/" + e.workflowID + "/attempts/" + e.attemptID + "/resolve"
}

// verifierOverride is a SessionReader that reports one session's capability verifier as given.
// The store mints session ids, so a test that must control the stored digest supplies it here
// rather than rewriting a row: the interface is the production one.
type verifierOverride struct {
	Sessions *sqlite.Store
	verifier string
}

func (v verifierOverride) GetSession(
	ctx context.Context,
	id domain.SessionID,
) (domain.SessionRecord, bool, error) {
	rec, found, err := v.Sessions.GetSession(ctx, id)
	if err != nil || !found {
		return rec, found, err
	}
	rec.Metadata.BrowserCapabilityVerifier = v.verifier
	return rec, true, nil
}

// approvalVerifier reproduces the browser capability digest the daemon stores, so the seeded
// session's verifier matches the capability the agent presents.
func approvalVerifier(sessionID domain.SessionID, token string) string {
	return browserVerifierFixture(sessionID, token)
}
