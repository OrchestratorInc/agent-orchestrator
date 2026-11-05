package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/scripted"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"

	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// 本文件保留 HTTP 全链与失败矩阵，正式 CLI 证据位于 m2_formal_cli_e2e_test.go。
//
// m2_session_manager_e2e_test.go proves the real Session Manager over real git worktrees, but
// it uses HTTP approvals. m2_e2e_test.go proves the HTTP/service stack, but with a fake
// session spawner. operator_approvals_m2_test.go proves the approval proof, but over a fake
// gateway. None of them walks the chain a person actually performs:
//
//	plan (two repos pinned to two different historical commits)
//	  → operator bind, authorised by a single-use human approval
//	  → real Session Manager materialises two real worktrees at exactly those commits
//	  → the spawned agent speaks through the real collaboration routes
//	  → the agent is refused every operator verb
//	  → the human decides and resolves, through approvals, and the audit trail records it
//	  → reconcile/resume re-attach instead of re-spawning
//	  → generation fencing refuses stale controller work
//	  → a daemon restart invalidates approvals but keeps every durable fact
//
// Everything is real except the same three things the siblings already substitute and explain:
// a terminal runtime that starts nothing, an agent registry that answers without inspecting an
// install, and the scripted Chat DRIVER (adapters/chatdriver/scripted), which speaks AO's own
// port vocabulary and starts no provider. No network, no provider, no real data dir: everything
// lives under t.TempDir().

// ---------------------------------------------------------------------------
// fixture: the sibling's stack, plus the approval proof and the audit writer
// ---------------------------------------------------------------------------

type m2ffFixture struct {
	t          *testing.T
	store      *store.Store
	approvals  *principal.ApprovalStore
	consent    *m2Consent
	chat       *chatsvc.Service
	srv        *httptest.Server
	base       string
	dataDir    string
	repoA      string
	repoB      string
	projectA   domain.ProjectID
	projectB   domain.ProjectID
	managedDir string
	// deploy is the immutable project/workspace layout, reused by a restart so the second
	// daemon composes over the SAME repositories and the SAME managed root.
	deploy m2ffDeploy
}

type m2ffDeploy struct {
	projectA   domain.ProjectID
	projectB   domain.ProjectID
	repoA      string
	repoB      string
	dataDir    string
	managedDir string
}

// m2ffPlant is everything one boot needs that is not derived from the store.
type m2ffPlant struct {
	store      *store.Store
	dataDir    string
	managedDir string
	repoA      string
	repoB      string
}

// newM2FFFixture builds one daemon boot over an existing store and an existing layout.
//
// A second call with the same plant is a RESTART: a fresh router, fresh services, a fresh
// in-memory approval table, and the same SQLite file, git repositories, and managed root.
func newM2FFFixture(t *testing.T, plant m2ffPlant) *m2ffFixture {
	t.Helper()
	ctx := context.Background()
	clock := func() time.Time { return time.Now().UTC() }

	st := plant.store

	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: string(m2ffProjectA), Path: plant.repoA, RegisteredAt: time.Now().UTC().Truncate(time.Second),
		Config: domain.ProjectConfig{DefaultBranch: "main", Worker: domain.RoleOverride{Harness: m2smDriverHarness}},
	}); err != nil {
		t.Fatalf("seed project A: %v", err)
	}
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: string(m2ffProjectB), Path: plant.repoB, RegisteredAt: time.Now().UTC().Truncate(time.Second),
		Config: domain.ProjectConfig{DefaultBranch: "main", Worker: domain.RoleOverride{Harness: m2smDriverHarness}},
	}); err != nil {
		t.Fatalf("seed project B: %v", err)
	}

	ws, err := gitworktree.New(gitworktree.Options{
		ManagedRoot: plant.managedDir,
		RepoResolver: gitworktree.StaticRepoResolver{
			m2ffProjectA: plant.repoA, m2ffProjectB: plant.repoB,
		},
	})
	if err != nil {
		t.Fatalf("gitworktree: %v", err)
	}

	// The same scripted driver the siblings use: it speaks AO's port vocabulary and starts no
	// provider. Steering is enabled so the generation fence, not a missing capability, is what a
	// stale intervention hits.
	registry := scripted.Registry(scripted.NewDriver(scripted.Config{
		Harness: m2smDriverHarness,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
			ports.ChatCapabilitySteer:     true,
		},
		Steerable: true,
	}))

	lcm := lifecycle.New(st, nil)
	chatSvc := chatsvc.New(chatsvc.Options{
		Store:      st,
		Reader:     m2smSnapshotReader(st),
		PageReader: m2smPageReader(st),
		Sessions:   st,
		Drivers:    registry,
		Activity:   lcm,
		Log:        slog.New(slog.DiscardHandler),
		Now:        clock,
		NewID:      m2smIDFactory("m2ff"),
	})
	t.Cleanup(func() { chatSvc.StopAll(context.Background()) })

	recorder := capability.NewRecorder(st)
	resolver := capability.NewResolver(capability.WithRecorder(recorder), capability.WithClock(clock))
	probe := capability.NewChatDriverProbe(
		m2smChatProbe{svc: chatSvc}, registry, m2smReadiness{}, clock, capability.DefaultTTL,
	)
	resolver.RegisterProbe(m2smDriverHarness, probe)

	mgr := sessionmanager.New(sessionmanager.Deps{
		Runtime:   &m2smRuntime{},
		Agents:    m2smAgents{},
		Workspace: ws,
		Store:     st,
		Messenger: &m2smMessenger{},
		Chat:      m2smChatLauncher{svc: chatSvc},
		Lifecycle: lcm,
		DataDir:   plant.dataDir,
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
		Executable: func() (string, error) {
			return filepath.Join(plant.dataDir, "ao"), nil
		},
		Logger: slog.New(slog.DiscardHandler),
	})
	mgr.SetAgentReadiness(m2smReadiness{})

	gateway := execution.NewLocalGateway(execution.LocalGatewayDeps{
		Sessions: mgr, Resumer: mgr, Records: st, Commits: st, Worktrees: st, Turns: st,
		Chats: chatSvc, Controllers: chatSvc,
		Capabilities: recorder, Clock: clock, Log: slog.New(slog.DiscardHandler),
	})
	binder := execution.New(execution.Deps{
		Store: st, Gateway: gateway, Resolver: resolver, Clock: clock,
	})
	interventions := intervention.New(intervention.Deps{
		Store: st, Bindings: st, BindingWriter: binder, Gateway: gateway,
		Resolver: resolver, Clock: clock,
	})
	collab := collaboration.New(collaboration.Deps{
		Store: st, Bindings: st, Graph: st, Clock: clock,
	})
	workflows := workflow.New(workflow.Deps{
		Store: st, Clock: clock,
		GraphLimits:   domain.WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 12, MaxFanOut: 8},
		LeaseDuration: 15 * time.Minute,
	})

	// 只替代 OS 人工确认边界；每次装配使用新的内存审批表。
	consent := &m2Consent{}
	approvals := principal.NewApprovalStore(principal.ApprovalStoreOptions{
		TTL:   principal.DefaultApprovalTTL,
		Clock: clock,
	})
	principals := principal.NewResolver(
		st,
		principal.CapabilityVerifier{Sessions: st, Authority: browser.NewAuthority()},
		principal.ApprovalOperatorTrust{Approvals: approvals},
	).WithLeaseProver(principal.StoreLeaseProver{Attempts: st, Clock: clock})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Workflows:             workflows,
		WorkflowCapabilities:  resolver,
		WorkflowBindings:      st,
		WorkflowBinder:        binder,
		WorkflowInterventions: interventions,
		WorkflowCollaboration: collab,
		WorkflowOwnership:     m2smOwnership{store: st},
		WorkflowPrincipals:    principals,
		WorkflowAudit:         st,
		OperatorApprovals:     approvals,
		OperatorConsent:       consent,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	return &m2ffFixture{
		t: t, store: st, approvals: approvals, consent: consent,
		chat: chatSvc, srv: srv, base: srv.URL + "/api/v1",
		dataDir: plant.dataDir, managedDir: plant.managedDir,
		repoA: plant.repoA, repoB: plant.repoB,
		projectA: m2ffProjectA, projectB: m2ffProjectB,
		deploy: m2ffDeploy{
			projectA: m2ffProjectA, projectB: m2ffProjectB,
			repoA: plant.repoA, repoB: plant.repoB,
			dataDir: plant.dataDir, managedDir: plant.managedDir,
		},
	}
}

const (
	m2ffProjectA = domain.ProjectID("m2ff-project-a")
	m2ffProjectB = domain.ProjectID("m2ff-project-b")
)

// stop is a daemon shutdown: the in-memory controllers and the HTTP listener go away, the
// database and the data dir stay exactly where they are.
func (f *m2ffFixture) stop() {
	f.chat.StopAll(context.Background())
	f.srv.Close()
}

// ---------------------------------------------------------------------------
// wire helpers
// ---------------------------------------------------------------------------

type m2ffResp struct {
	Status int
	Body   map[string]any
	Raw    string
}

// do sends one request with an explicit header set. Nothing is added implicitly: a helper that
// always attached the token would make the refusal tests prove nothing.
func (f *m2ffFixture) do(method, path, body string, headers map[string]string) m2ffResp {
	f.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.base+path, reader)
	if err != nil {
		f.t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatalf("read %s %s: %v", method, path, err)
	}
	decoded := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if len(strings.TrimSpace(string(raw))) > 0 {
		_ = decoder.Decode(&decoded)
	}
	return m2ffResp{Status: resp.StatusCode, Body: decoded, Raw: string(raw)}
}

// asOperator 为 HTTP 场景逐次申请原生审批，不代表正式 CLI 入口。
func (f *m2ffFixture) asOperator(method, path, body string) m2ffResp {
	f.t.Helper()
	return f.do(method, path, body, m2ApprovedHeaders(f.t, f.base, method, path, body))
}

func (f *m2ffFixture) text(body map[string]any, keys ...string) string {
	var current any = body
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	value, _ := current.(string)
	return value
}

func (f *m2ffFixture) number(body map[string]any, keys ...string) int64 {
	var current any = body
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		current = object[key]
	}
	value, _ := current.(json.Number)
	n, _ := value.Int64()
	return n
}

// ---------------------------------------------------------------------------
// HTTP 审批流程
// ---------------------------------------------------------------------------

// m2ffApproval is one requested approval: the id and the pairing code the human would be
// shown between the terminal and the desktop app.
type m2ffApproval struct {
	ID   string
	Code string
	Path string
	Body string
}

// requestApproval asks for authority over ONE concrete operation and ONE exact body.
//
// Creation is unauthenticated on loopback by design: it grants nothing, because nothing can
// happen until OS verification confirms it.
//
// The operation path is the FULL concrete path the approved request will carry
// ("/api/v1/workflows/..."), because the allowlist it is matched against names the mounted
// routes, not the fixture's shortened helper paths.
func (f *m2ffFixture) requestApproval(method, path, body string) m2ffApproval {
	f.t.Helper()
	operationPath := "/api/v1" + path
	request := fmt.Sprintf(
		`{"operationMethod":%q,"operationPath":%q,"payloadDigest":%q,"payload":%q}`,
		method, operationPath, principal.PayloadDigest([]byte(body)), body)
	created := f.do(http.MethodPost, "/operator/approvals", request, nil)
	if created.Status != http.StatusCreated {
		f.t.Fatalf("request approval for %s %s: %d %s", method, path, created.Status, created.Raw)
	}
	approval := m2ffApproval{
		ID:   f.text(created.Body, "approval", "approvalId"),
		Code: f.text(created.Body, "approval", "code"),
		Path: path, Body: body,
	}
	if approval.ID == "" || approval.Code == "" {
		f.t.Fatalf("approval is missing an id or code: %s", created.Raw)
	}
	// The response must name the operation the human is being asked about, verbatim.
	if got := f.text(created.Body, "approval", "operation", "path"); got != operationPath {
		f.t.Fatalf("approval operation path = %q, want %q", got, operationPath)
	}
	if got := f.text(created.Body, "approval", "payloadDigest"); got != principal.PayloadDigest([]byte(body)) {
		f.t.Fatalf("approval digest = %q, want the digest of the exact body", got)
	}
	return approval
}

// confirmApproval 经真实 HTTP 路由调用注入的 OS 验证边界。
func (f *m2ffFixture) confirmApproval(approval m2ffApproval) {
	f.t.Helper()
	body := fmt.Sprintf(`{"code":%q,"payloadDigest":%q}`,
		approval.Code, principal.PayloadDigest([]byte(approval.Body)))
	confirmed := f.do(http.MethodPost, "/operator/approvals/"+approval.ID+"/confirm", body, nil)
	if confirmed.Status != http.StatusOK {
		f.t.Fatalf("confirm approval: %d %s", confirmed.Status, confirmed.Raw)
	}
	if state := f.text(confirmed.Body, "approval", "state"); state != string(principal.ApprovalStateConfirmed) {
		f.t.Fatalf("approval state after confirm = %q, want confirmed", state)
	}
	// The confirm response never republishes the code.
	if code := f.text(confirmed.Body, "approval", "code"); code != "" {
		f.t.Fatalf("confirm response carries the pairing code: %s", confirmed.Raw)
	}
}

// approvedAction is the whole operator path: request, confirm, consume.
func (f *m2ffFixture) approvedAction(method, path, body string) m2ffResp {
	f.t.Helper()
	approval := f.requestApproval(method, path, body)
	f.confirmApproval(approval)
	return f.submitWithApproval(approval)
}

func (f *m2ffFixture) submitWithApproval(approval m2ffApproval) m2ffResp {
	f.t.Helper()
	return f.do(http.MethodPost, approval.Path, approval.Body,
		map[string]string{principal.HeaderOperatorApproval: approval.ID})
}

// asAgent presents the spawned session's capability plus the attempt's lease. Both are
// verified against durable rows; neither is derived from the request body.
func (f *m2ffFixture) asAgent(capabilityToken, lease string, attemptID domain.WorkflowAttemptID) map[string]string {
	return map[string]string{
		principal.HeaderAgentCapability: capabilityToken,
		principal.HeaderAttemptID:       string(attemptID),
		principal.HeaderAttemptLease:    lease,
	}
}

// ---------------------------------------------------------------------------
// durable readers
// ---------------------------------------------------------------------------

func (f *m2ffFixture) binding(attemptID domain.WorkflowAttemptID) domain.WorkflowAttemptBinding {
	f.t.Helper()
	binding, found, err := f.store.GetWorkflowAttemptBinding(context.Background(), attemptID)
	if err != nil || !found {
		f.t.Fatalf("read binding %s: found=%v err=%v", attemptID, found, err)
	}
	return binding
}

func (f *m2ffFixture) session(id domain.SessionID) domain.SessionRecord {
	f.t.Helper()
	rec, found, err := f.store.GetSession(context.Background(), id)
	if err != nil || !found {
		f.t.Fatalf("read session %s: found=%v err=%v", id, found, err)
	}
	return rec
}

func (f *m2ffFixture) repoBindings(attemptID domain.WorkflowAttemptID) map[domain.WorkflowRepoKey]domain.WorkflowAttemptRepoBinding {
	f.t.Helper()
	rows, err := f.store.ListWorkflowAttemptRepoBindings(context.Background(), attemptID)
	if err != nil {
		f.t.Fatalf("list repo bindings: %v", err)
	}
	byKey := make(map[domain.WorkflowRepoKey]domain.WorkflowAttemptRepoBinding, len(rows))
	for _, row := range rows {
		byKey[row.RepoKey] = row
	}
	return byKey
}

func (f *m2ffFixture) worktrees(sessionID domain.SessionID) map[string]domain.SessionWorktreeRecord {
	f.t.Helper()
	rows, err := f.store.ListSessionWorktrees(context.Background(), sessionID)
	if err != nil {
		f.t.Fatalf("list session worktrees: %v", err)
	}
	byRepo := make(map[string]domain.SessionWorktreeRecord, len(rows))
	for _, row := range rows {
		byRepo[row.RepoName] = row
	}
	return byRepo
}

// sessionsFor counts the sessions whose client request belongs to this fixture. A real session
// count is asked of the whole table and then filtered by project, because every session this
// fixture creates belongs to one of its two projects.
func (f *m2ffFixture) sessionsFor() []domain.SessionRecord {
	f.t.Helper()
	rows, err := f.store.ListAllSessions(context.Background())
	if err != nil {
		f.t.Fatalf("list sessions: %v", err)
	}
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, row := range rows {
		if row.ProjectID == m2ffProjectA || row.ProjectID == m2ffProjectB {
			out = append(out, row)
		}
	}
	return out
}

func (f *m2ffFixture) audits(workflowID domain.WorkflowID) []store.AuditEntry {
	f.t.Helper()
	rows, err := f.store.ListWorkflowAudits(context.Background(), string(workflowID), 100, 0)
	if err != nil {
		f.t.Fatalf("list workflow audits: %v", err)
	}
	return rows
}

// ---------------------------------------------------------------------------
// workflow composition (two independent repositories, two different pins)
// ---------------------------------------------------------------------------

// m2ffRepo builds an independent repository with three commits and returns its directory.
//
// "Independent" is asserted, not assumed: the caller compares the two repositories' commits and
// their content, and the fixture writes different files with different bytes.
func m2ffRepo(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	m2smGit(t, dir, "init")
	m2smGit(t, dir, "config", "core.autocrlf", "false")
	m2smGit(t, dir, "config", "user.email", "ao@example.com")
	m2smGit(t, dir, "config", "user.name", "AO Tests")
	for i, content := range []string{
		name + " first\n", name + " second\n", name + " third\n",
	} {
		file := filepath.Join(dir, fmt.Sprintf("history-%d.txt", i))
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
		m2smGit(t, dir, "add", filepath.Base(file))
		m2smGit(t, dir, "commit", "-m", fmt.Sprintf("%s commit %d", name, i))
	}
	m2smGit(t, dir, "branch", "-M", "main")
	return dir
}

// createWorkflowWithPins plans the workflow: repo-a is pinned HEAD~1, repo-b HEAD~2. Because
// repo-b has its own (different) history, the two pins necessarily differ.
func (f *m2ffFixture) createWorkflowWithPins(key, prompt, pinA, pinB string) (domain.WorkflowID, domain.WorkflowTaskID) {
	f.t.Helper()
	taskID := domain.WorkflowTaskID(key + "-task")
	body := fmt.Sprintf(`{
		"title": "M2 full flow",
		"goal": "walk the entire chain once",
		"idempotencyKey": "%s",
		"repositories": [
			{"repoKey": "repo-a", "projectId": "%s", "baseSha": "%s"},
			{"repoKey": "repo-b", "projectId": "%s", "baseSha": "%s"}
		],
		"tasks": [
			{"id": "%s", "kind": "development", "title": "work", "agentHarness": "%s", "maxAttempts": 3}
		],
		"taskRepositories": [
			{"taskId": "%s", "repoKey": "repo-a", "baseSha": "%s"},
			{"taskId": "%s", "repoKey": "repo-b", "baseSha": "%s"}
		]
	}`, key, m2ffProjectA, pinA, m2ffProjectB, pinB,
		taskID, m2smDriverHarness,
		taskID, pinA, taskID, pinB)

	created := f.asOperator(http.MethodPost, "/workflows", body)
	if created.Status != http.StatusCreated {
		f.t.Fatalf("create workflow: %d %s", created.Status, created.Raw)
	}
	workflowID := domain.WorkflowID(f.text(created.Body, "workflow", "id"))
	revision := f.number(created.Body, "workflow", "revision")

	activated := f.asOperator(http.MethodPost, "/workflows/"+string(workflowID)+"/activate",
		fmt.Sprintf(`{"expectedRevision":%d,"idempotencyKey":"%s-activate"}`, revision, key))
	if activated.Status != http.StatusOK {
		f.t.Fatalf("activate: %d %s", activated.Status, activated.Raw)
	}
	_ = prompt
	return workflowID, taskID
}

// claimAndStart leases the task to a worker and moves the attempt to running. Those are M1
// worker surfaces (no operator proof involved), so they use the bare wire.
func (f *m2ffFixture) claimAndStart(
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
	key string,
) (domain.WorkflowAttemptID, string) {
	f.t.Helper()
	task := f.asOperator(http.MethodGet, "/workflows/"+string(workflowID)+"/tasks/"+string(taskID), "")
	if task.Status != http.StatusOK {
		f.t.Fatalf("read task: %d %s", task.Status, task.Raw)
	}
	revision := f.number(task.Body, "task", "revision")

	claimed := f.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/tasks/"+string(taskID)+"/claim",
		fmt.Sprintf(`{"expectedRevision":%d,"workerId":"w1","idempotencyKey":"%s-claim"}`, revision, key), nil)
	if claimed.Status != http.StatusCreated {
		f.t.Fatalf("claim: %d %s", claimed.Status, claimed.Raw)
	}
	attemptID := domain.WorkflowAttemptID(f.text(claimed.Body, "attempt", "id"))
	lease := f.text(claimed.Body, "leaseToken")
	if attemptID == "" || lease == "" {
		f.t.Fatalf("claim answered without an attempt id or lease: %s", claimed.Raw)
	}

	started := f.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/start",
		fmt.Sprintf(`{"leaseToken":%q,"idempotencyKey":"%s-start"}`, lease, key), nil)
	if started.Status != http.StatusOK {
		f.t.Fatalf("start: %d %s", started.Status, started.Raw)
	}
	return attemptID, lease
}

func (f *m2ffFixture) bindPath(workflowID domain.WorkflowID, attemptID domain.WorkflowAttemptID) string {
	return "/workflows/" + string(workflowID) + "/attempts/" + string(attemptID) + "/bind"
}

func (f *m2ffFixture) attemptPath(workflowID domain.WorkflowID, attemptID domain.WorkflowAttemptID, verb string) string {
	base := "/workflows/" + string(workflowID) + "/attempts/" + string(attemptID)
	if verb == "" {
		return base
	}
	return base + "/" + verb
}

// issueAgentCapability mints the worker capability and persists its verifier on the session row,
// exactly the way a real spawn does before the worker process starts.
func (f *m2ffFixture) issueAgentCapability(sessionID domain.SessionID) string {
	f.t.Helper()
	ctx := context.Background()
	token, verifier, err := browser.NewAuthority().Issue(sessionID)
	if err != nil {
		f.t.Fatalf("issue capability: %v", err)
	}
	rec, found, err := f.store.GetSession(ctx, sessionID)
	if err != nil || !found {
		f.t.Fatalf("read session %s: found=%v err=%v", sessionID, found, err)
	}
	if ok, err := f.store.UpdateBrowserCapabilityVerifier(ctx, sessionID, rec.ControllerOwner(), verifier); err != nil || !ok {
		f.t.Fatalf("persist capability verifier: ok=%v err=%v", ok, err)
	}
	return token
}

// ---------------------------------------------------------------------------
// the test
// ---------------------------------------------------------------------------

func TestM2FullFlowOperatorBindThroughDaemonRestart(t *testing.T) {
	// Two genuinely independent repositories: different content, different commits.
	repoA := m2ffRepo(t, "alpha")
	repoB := m2ffRepo(t, "bravo")
	if headA, headB := strings.TrimSpace(m2smGit(t, repoA, "rev-parse", "HEAD")),
		strings.TrimSpace(m2smGit(t, repoB, "rev-parse", "HEAD")); headA == headB {
		t.Fatalf("the two repositories produced the same tip %s; the fixture is not independent", headA)
	}
	for _, repo := range []string{repoA, repoB} {
		content, err := os.ReadFile(filepath.Join(repo, "history-0.txt"))
		if err != nil {
			t.Fatalf("read history-0.txt in %s: %v", repo, err)
		}
		if strings.Contains(string(content), "alpha") && strings.Contains(string(content), "bravo") {
			t.Fatalf("%s content is not repository-specific: %q", repo, content)
		}
	}

	dataDir := t.TempDir()
	managedDir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	plant := m2ffPlant{store: st, dataDir: dataDir, managedDir: managedDir, repoA: repoA, repoB: repoB}
	f := newM2FFFixture(t, plant)

	// -------------------------------------------------------------------
	// 1. Workflow setup: two repos, two DIFFERENT historical pins.
	// -------------------------------------------------------------------
	pinA := strings.TrimSpace(m2smGit(t, repoA, "rev-parse", "HEAD~1"))
	pinB := strings.TrimSpace(m2smGit(t, repoB, "rev-parse", "HEAD~2"))
	if pinA == pinB {
		t.Fatalf("the two pins are identical (%s); the scenario would prove nothing", pinA)
	}
	tipA := strings.TrimSpace(m2smGit(t, repoA, "rev-parse", "HEAD"))
	tipB := strings.TrimSpace(m2smGit(t, repoB, "rev-parse", "HEAD"))

	workflowID, taskID := f.createWorkflowWithPins("m2ff-full", "do the work", pinA, pinB)

	// The PLANNING output is durable before anything spawns: each repository records the
	// historical commit the plan pinned, and the two differ.
	workflowRepos, err := f.store.ListWorkflowRepositories(context.Background(), workflowID)
	if err != nil {
		t.Fatalf("list workflow repositories: %v", err)
	}
	if len(workflowRepos) != 2 {
		t.Fatalf("workflow repositories = %d, want 2", len(workflowRepos))
	}
	planByKey := map[domain.WorkflowRepoKey]domain.WorkflowRepository{}
	for _, repo := range workflowRepos {
		planByKey[repo.RepoKey] = repo
	}
	if got := planByKey[m2smRepoA].BaseSHA; got != pinA {
		t.Fatalf("planned repo-a base = %q, want the pinned HEAD~1 %q", got, pinA)
	}
	if got := planByKey[m2smRepoB].BaseSHA; got != pinB {
		t.Fatalf("planned repo-b base = %q, want the pinned HEAD~2 %q", got, pinB)
	}
	if planByKey[m2smRepoA].BaseSHA == planByKey[m2smRepoB].BaseSHA {
		t.Fatal("the two planned bases are identical")
	}
	if got := planByKey[m2smRepoA].ProjectID; got != m2ffProjectA {
		t.Fatalf("repo-a project = %q, want %q", got, m2ffProjectA)
	}
	if got := planByKey[m2smRepoB].ProjectID; got != m2ffProjectB {
		t.Fatalf("repo-b project = %q, want %q", got, m2ffProjectB)
	}
	// The task scope carries the same pins, which is what the attempt repo bindings inherit.
	taskRepos, err := f.store.ListWorkflowTaskRepositories(context.Background(), workflowID, taskID)
	if err != nil || len(taskRepos) != 2 {
		t.Fatalf("task repositories = %d err=%v, want 2", len(taskRepos), err)
	}
	scopeByKey := map[domain.WorkflowRepoKey]domain.WorkflowTaskRepository{}
	for _, scope := range taskRepos {
		scopeByKey[scope.RepoKey] = scope
	}
	if got := scopeByKey[m2smRepoA].BaseSHA; got != pinA {
		t.Fatalf("task scope repo-a base = %q, want %q", got, pinA)
	}
	if got := scopeByKey[m2smRepoB].BaseSHA; got != pinB {
		t.Fatalf("task scope repo-b base = %q, want %q", got, pinB)
	}

	attemptID, lease := f.claimAndStart(workflowID, taskID, "m2ff-full")

	// Nothing has been bound yet: the repo bindings are the BIND's durable projection, created
	// when the attempt acquires a session, so before the bind there is nothing to read.
	if preBind := f.repoBindings(attemptID); len(preBind) != 0 {
		t.Fatalf("repo bindings exist before any bind: %+v", preBind)
	}
	if _, found, err := f.store.GetWorkflowAttemptBinding(context.Background(), attemptID); err != nil || found {
		t.Fatalf("a binding row exists before the bind: found=%v err=%v", found, err)
	}
	// The planning pins are already durable where the bind will read them: the task scope.
	if got := scopeByKey[m2smRepoA].BaseSHA; got != pinA {
		t.Fatalf("task scope repo-a base = %q, want %q", got, pinA)
	}

	// -------------------------------------------------------------------
	// 2. Operator bind, authorised by a single-use human approval.
	// -------------------------------------------------------------------
	sessionsBefore := len(f.sessionsFor())
	bindBody := fmt.Sprintf(
		`{"harness":%q,"mode":"chat","prompt":"do the work","idempotencyKey":"m2ff-full-bind"}`,
		m2smDriverHarness)
	bindApproval := f.requestApproval(http.MethodPost, f.bindPath(workflowID, attemptID), bindBody)

	// An approval that has NOT been confirmed authorises nothing: the human gesture is the
	// difference between asking and being allowed.
	if refused := f.submitWithApproval(bindApproval); refused.Status != http.StatusForbidden {
		t.Fatalf("unconfirmed approval bind = %d, want 403; body=%s", refused.Status, refused.Raw)
	}

	f.confirmApproval(bindApproval)
	bound := f.submitWithApproval(bindApproval)
	if bound.Status != http.StatusOK {
		t.Fatalf("approved bind = %d, want 200; body=%s", bound.Status, bound.Raw)
	}
	if spawned, _ := bound.Body["spawned"].(bool); !spawned {
		t.Fatalf("bind did not report a spawn: %s", bound.Raw)
	}
	if state := f.text(bound.Body, "binding", "state"); state != string(domain.WorkflowBindingStateBoundReady) {
		t.Fatalf("binding state = %q, want bound_ready; body=%s", state, bound.Raw)
	}

	// The binding row exists, names a session, and holds a REAL controller generation.
	binding := f.binding(attemptID)
	if binding.SessionID == nil {
		t.Fatal("the binding names no session")
	}
	sessionID := *binding.SessionID
	if strings.TrimSpace(binding.ControllerGeneration) == "" ||
		strings.HasPrefix(binding.ControllerGeneration, "pending:") {
		t.Fatalf("binding generation = %q, want a real controller generation", binding.ControllerGeneration)
	}
	rec := f.session(sessionID)
	if rec.Metadata.ControllerGeneration != binding.ControllerGeneration {
		t.Fatalf("binding generation %q != session generation %q",
			binding.ControllerGeneration, rec.Metadata.ControllerGeneration)
	}
	if got := len(f.sessionsFor()); got != sessionsBefore+1 {
		t.Fatalf("sessions = %d after one bind, want %d", got, sessionsBefore+1)
	}

	// The approval is spent: the same one cannot authorise a second request.
	if replay := f.submitWithApproval(bindApproval); replay.Status != http.StatusForbidden {
		t.Fatalf("replayed bind approval = %d, want 403; body=%s", replay.Status, replay.Raw)
	}

	// -------------------------------------------------------------------
	// 3. Session Manager materialised two real worktrees at the pinned commits.
	// -------------------------------------------------------------------
	rows := f.worktrees(sessionID)
	if len(rows) != 2 {
		t.Fatalf("session_worktrees rows = %d, want 2 (one per repository)", len(rows))
	}
	repoOf := map[string]string{"repo-a": repoA, "repo-b": repoB}
	pinOf := map[string]string{"repo-a": pinA, "repo-b": pinB}
	projectOf := map[string]domain.ProjectID{"repo-a": m2ffProjectA, "repo-b": m2ffProjectB}
	for _, key := range []string{"repo-a", "repo-b"} {
		row, ok := rows[key]
		if !ok {
			t.Fatalf("no worktree row for %s; rows=%v", key, rows)
		}
		// The filesystem fact, read straight from git inside the worktree.
		if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
			t.Fatalf("%s worktree is not on disk at %q: %v", key, row.WorktreePath, statErr)
		}
		if !worktreeListedIn(t, repoOf[key], row.WorktreePath) {
			t.Fatalf("%s is not registered as a worktree of its repository", row.WorktreePath)
		}
		if head := strings.TrimSpace(m2smGit(t, row.WorktreePath, "rev-parse", "HEAD")); head != pinOf[key] {
			t.Fatalf("%s worktree HEAD = %s, want the pinned commit %s", key, head, pinOf[key])
		}
		if row.CreationSHA != pinOf[key] {
			t.Fatalf("%s CreationSHA = %q, want %q", key, row.CreationSHA, pinOf[key])
		}
		if row.BaseSHA != pinOf[key] {
			t.Fatalf("%s BaseSHA = %q, want %q", key, row.BaseSHA, pinOf[key])
		}
		// The worktree's own repository, read from git, must be the repository the row means.
		if row.ProjectID != projectOf[key] || filepath.Clean(row.RepoPath) != filepath.Clean(repoOf[key]) {
			t.Fatalf("%s 持久化归属错误: project=%s repo=%s", key, row.ProjectID, row.RepoPath)
		}
		//
		// `--git-common-dir` is relative to the worktree on a POSIX git and absolute on a
		// Windows one, so both forms are resolved before the comparison.
		commonDir := strings.TrimSpace(m2smGit(t, row.WorktreePath, "rev-parse", "--git-common-dir"))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(row.WorktreePath, commonDir)
		}
		if resolved, want := filepath.Clean(commonDir), filepath.Clean(filepath.Join(repoOf[key], ".git")); !strings.EqualFold(resolved, want) {
			t.Fatalf("%s worktree belongs to %s, want %s", key, resolved, want)
		}
	}

	// Per-repository ProjectID, from the durable attempt binding: repo-a is project A, repo-b
	// is project B. This is the fact the spawn consumed and the operator UI renders.
	bound2 := f.repoBindings(attemptID)
	if got := bound2[m2smRepoA].ProjectID; got != projectOf["repo-a"] {
		t.Fatalf("repo-a ProjectID = %q, want %q", got, projectOf["repo-a"])
	}
	if got := bound2[m2smRepoB].ProjectID; got != projectOf["repo-b"] {
		t.Fatalf("repo-b ProjectID = %q, want %q", got, projectOf["repo-b"])
	}
	// The binding projected the observed heads, and they equal the pins.
	if got := bound2[m2smRepoA].ExpectedBaseSHA; got != pinA {
		t.Fatalf("repo-a ExpectedBaseSHA after bind = %q, want %q", got, pinA)
	}
	if got := bound2[m2smRepoB].ExpectedBaseSHA; got != pinB {
		t.Fatalf("repo-b ExpectedBaseSHA after bind = %q, want %q", got, pinB)
	}
	for key, pin := range pinOf {
		if got := bound2[domain.WorkflowRepoKey(key)].ObservedHeadSHA; got != pin {
			t.Fatalf("%s ObservedHeadSHA = %q, want the pinned commit %q", key, got, pin)
		}
		if got := bound2[domain.WorkflowRepoKey(key)].State; got != domain.WorkflowRepoBindingStateVerified {
			t.Fatalf("%s repo binding state = %q, want verified", key, got)
		}
	}
	// The attempt row is the one the bind recorded, and its session agrees with the binding.
	attempt, found, err := f.store.GetWorkflowAttempt(context.Background(), attemptID)
	if err != nil || !found {
		t.Fatalf("read attempt: found=%v err=%v", found, err)
	}
	if attempt.SessionID == nil || *attempt.SessionID != sessionID {
		t.Fatalf("attempt session = %v, want the bound %s", attempt.SessionID, sessionID)
	}
	if attempt.WorkflowID != workflowID || attempt.TaskID != taskID {
		t.Fatalf("attempt identity = %s/%s, want %s/%s",
			attempt.WorkflowID, attempt.TaskID, workflowID, taskID)
	}
	if attempt.State != domain.WorkflowAttemptStateRunning {
		t.Fatalf("attempt state = %q, want running", attempt.State)
	}

	// The manifest the spawn recorded is the same two pins the caller planned from.
	if tipA == pinA || tipB == pinB {
		t.Fatal("a repository's tip equals its pin, so the pin proves nothing")
	}

	// -------------------------------------------------------------------
	// 4. The agent speaks through the real collaboration routes.
	// -------------------------------------------------------------------
	agentToken := f.issueAgentCapability(sessionID)
	agentHeaders := f.asAgent(agentToken, lease, attemptID)

	message := f.do(http.MethodPost, "/workflows/"+string(workflowID)+"/messages",
		`{"kind":"progress","body":"half done","clientMessageId":"m2ff-msg-1"}`, agentHeaders)
	if message.Status != http.StatusCreated {
		t.Fatalf("agent message = %d, want 201; body=%s", message.Status, message.Raw)
	}
	if kind := f.text(message.Body, "message", "senderKind"); kind != string(domain.WorkflowMessageSenderAgent) {
		t.Fatalf("message senderKind = %q, want agent (derived, not supplied)", kind)
	}
	stored, err := f.store.ListWorkflowMessages(context.Background(), workflowID, 100, 0)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored messages = %d err=%v, want 1", len(stored), err)
	}
	if stored[0].SenderKind != domain.WorkflowMessageSenderAgent {
		t.Fatalf("stored senderKind = %q, want agent", stored[0].SenderKind)
	}
	if stored[0].SenderKind == domain.WorkflowMessageSenderHuman {
		t.Fatal("the agent's message was attributed to a human")
	}
	if stored[0].SenderTaskID == nil || *stored[0].SenderTaskID != taskID {
		t.Fatalf("stored sender task = %v, want the caller's own task %s", stored[0].SenderTaskID, taskID)
	}

	raised := f.do(http.MethodPost, "/workflows/"+string(workflowID)+"/task-requests",
		`{"requestId":"m2ff-req-1","requestKind":"increase_budget","payload":{"credits":10}}`, agentHeaders)
	if raised.Status != http.StatusCreated && raised.Status != http.StatusOK {
		t.Fatalf("agent task request = %d, want 201/200; body=%s", raised.Status, raised.Raw)
	}
	requestID := f.text(raised.Body, "request", "id")
	if requestID != "m2ff-req-1" {
		t.Fatalf("task request id = %q, want m2ff-req-1", requestID)
	}
	requestRevision := f.number(raised.Body, "request", "revision")
	if requestRevision < 1 {
		t.Fatalf("task request revision = %d, want >= 1", requestRevision)
	}

	// -------------------------------------------------------------------
	// 5. The agent cannot approve itself.
	// -------------------------------------------------------------------
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"bind", http.MethodPost, f.bindPath(workflowID, attemptID),
			`{"harness":"codex","mode":"chat","idempotencyKey":"m2ff-agent-bind"}`},
		{"decide", http.MethodPost,
			"/workflows/" + string(workflowID) + "/task-requests/" + requestID + "/decide",
			fmt.Sprintf(`{"approve":true,"expectedRevision":%d}`, requestRevision)},
		{"resolve", http.MethodPost, f.attemptPath(workflowID, attemptID, "resolve"),
			`{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"m2ff-agent-resolve"}`},
	} {
		refused := f.do(tc.method, tc.path, tc.body, agentHeaders)
		if refused.Status != http.StatusForbidden {
			t.Fatalf("agent %s = %d, want 403; body=%s", tc.name, refused.Status, refused.Raw)
		}
		if code := f.text(refused.Body, "code"); code != "COLLAB_NOT_AUTHORIZED" {
			t.Fatalf("agent %s code = %q, want COLLAB_NOT_AUTHORIZED", tc.name, code)
		}
	}

	// An approval is not a way for an agent to become the operator either: it may request one
	// (that grants nothing) but Agent 标记会被拒绝，清除标记后仍须 OS 验证。
	agentApprovalBody := fmt.Sprintf(
		`{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"m2ff-agent-resolve-2"}`)
	agentApprovalRequest := fmt.Sprintf(
		`{"operationMethod":"POST","operationPath":%q,"payloadDigest":%q,"payload":%q}`,
		"/api/v1"+f.attemptPath(workflowID, attemptID, "resolve"),
		principal.PayloadDigest([]byte(agentApprovalBody)), agentApprovalBody)
	createdByAgent := f.do(http.MethodPost, "/operator/approvals", agentApprovalRequest, nil)
	if createdByAgent.Status != http.StatusCreated {
		t.Fatalf("agent may request an approval (it grants nothing): %d %s",
			createdByAgent.Status, createdByAgent.Raw)
	}
	agentApprovalID := f.text(createdByAgent.Body, "approval", "approvalId")
	agentApprovalCode := f.text(createdByAgent.Body, "approval", "code")
	confirmBody := fmt.Sprintf(`{"code":%q,"payloadDigest":%q}`,
		agentApprovalCode, principal.PayloadDigest([]byte(agentApprovalBody)))
	agentConfirmed := f.do(http.MethodPost, "/operator/approvals/"+agentApprovalID+"/confirm", confirmBody, agentHeaders)
	if agentConfirmed.Status != http.StatusForbidden {
		t.Fatalf("agent confirming an approval = %d, want 403; body=%s",
			agentConfirmed.Status, agentConfirmed.Raw)
	}
	// Still pending, so it authorises nothing.
	if state, _, found := f.approvals.State(agentApprovalID); !found || state != principal.ApprovalStatePending {
		t.Fatalf("approval state after the agent's refused confirm = %q found=%v, want pending", state, found)
	}
	// Even presented with the agent's credentials, the unconfirmed approval is refused.
	if used := f.do(http.MethodPost, f.attemptPath(workflowID, attemptID, "resolve"), agentApprovalBody,
		map[string]string{
			principal.HeaderOperatorApproval: agentApprovalID,
			principal.HeaderAgentCapability:  agentToken,
			principal.HeaderAttemptID:        string(attemptID),
			principal.HeaderAttemptLease:     lease,
		}); used.Status != http.StatusForbidden {
		t.Fatalf("agent + approval resolve = %d, want 403; body=%s", used.Status, used.Raw)
	}

	// -------------------------------------------------------------------
	// 6. Human decides the task request, through the approval flow.
	// -------------------------------------------------------------------
	auditsBefore := len(f.audits(workflowID))
	decideBody := fmt.Sprintf(`{"approve":true,"expectedRevision":%d}`, requestRevision)
	decidePath := "/workflows/" + string(workflowID) + "/task-requests/" + requestID + "/decide"
	decided := f.approvedAction(http.MethodPost, decidePath, decideBody)
	if decided.Status != http.StatusAccepted {
		t.Fatalf("approved decide = %d, want 202; body=%s", decided.Status, decided.Raw)
	}
	if got := f.text(decided.Body, "materialization"); got != string(domain.WorkflowTaskRequestMaterializationPending) {
		t.Fatalf("materialization = %q, want pending; body=%s", got, decided.Raw)
	}
	if materialized, _ := decided.Body["materialized"].(bool); materialized {
		t.Fatalf("materialized = true, but M2 creates no task: %s", decided.Raw)
	}
	decidedRequest, foundReq, err := f.store.GetWorkflowTaskRequest(context.Background(), workflowID, requestID)
	if err != nil || !foundReq {
		t.Fatalf("read decided request: found=%v err=%v", foundReq, err)
	}
	if decidedRequest.State != domain.WorkflowTaskRequestStateApproved {
		t.Fatalf("request state = %q, want approved", decidedRequest.State)
	}
	if decidedRequest.DecidedByKind == nil || *decidedRequest.DecidedByKind != domain.WorkflowTaskRequestDeciderHuman {
		t.Fatalf("decidedByKind = %v, want human", decidedRequest.DecidedByKind)
	}

	// Exactly ONE audit row for the decision, carrying the CONFIRMING credential's fingerprint
	// and no credential material.
	afterDecide := f.audits(workflowID)
	if len(afterDecide) != auditsBefore+1 {
		t.Fatalf("audit rows = %d after one decision, want %d: %+v",
			len(afterDecide), auditsBefore+1, afterDecide)
	}
	decisionAudit := afterDecide[len(afterDecide)-1]
	if decisionAudit.Decision != "decide_task_request" {
		t.Fatalf("audit decision = %q, want decide_task_request", decisionAudit.Decision)
	}
	if decisionAudit.ActorKind != string(principal.KindOperator) {
		t.Fatalf("audit actor kind = %q, want operator", decisionAudit.ActorKind)
	}
	if decisionAudit.RequestID != requestID {
		t.Fatalf("audit request id = %q, want %q", decisionAudit.RequestID, requestID)
	}
	wantFingerprint := principal.NativeConsentIdentity
	if decisionAudit.TokenFingerprint != wantFingerprint {
		t.Fatalf("audit fingerprint = %q, want the confirming credential's %q",
			decisionAudit.TokenFingerprint, wantFingerprint)
	}
	if strings.Contains(decisionAudit.TokenFingerprint, agentToken) {
		t.Fatal("the audit row carries token material")
	}

	// -------------------------------------------------------------------
	// 7. Human resolves the attempt, through the approval flow.
	// -------------------------------------------------------------------
	taskBefore, found, err := f.store.GetWorkflowTask(context.Background(), workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("reload task: found=%v err=%v", found, err)
	}
	// Resolve is the only exit from UNKNOWN, so the attempt must reach it first. That is what
	// an expired lease does, and it is the store's own recovery path rather than a test write.
	if _, err := f.store.RecoverExpiredWorkflowAttempts(context.Background(), time.Now().UTC().Add(2*time.Hour)); err != nil {
		t.Fatalf("expire attempts: %v", err)
	}
	attemptAfterExpiry, found, err := f.store.GetWorkflowAttempt(context.Background(), attemptID)
	if err != nil || !found {
		t.Fatalf("read expired attempt: found=%v err=%v", found, err)
	}
	if attemptAfterExpiry.State != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("attempt state after expiry = %q, want unknown", attemptAfterExpiry.State)
	}
	taskForResolve, found, err := f.store.GetWorkflowTask(context.Background(), workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("reload task for resolve: found=%v err=%v", found, err)
	}
	if taskForResolve.Revision <= taskBefore.Revision {
		t.Fatalf("task revision did not advance across expiry: %d -> %d",
			taskBefore.Revision, taskForResolve.Revision)
	}
	resolveBody := fmt.Sprintf(
		`{"outcome":"succeeded","expectedTaskRevision":%d,"idempotencyKey":"m2ff-resolve-1"}`,
		taskForResolve.Revision)
	resolved := f.approvedAction(http.MethodPost, f.attemptPath(workflowID, attemptID, "resolve"), resolveBody)
	if resolved.Status != http.StatusOK {
		t.Fatalf("approved resolve = %d, want 200; body=%s", resolved.Status, resolved.Raw)
	}
	if state := f.text(resolved.Body, "attempt", "state"); state != string(domain.WorkflowAttemptStateSucceeded) {
		t.Fatalf("attempt state = %q, want succeeded; body=%s", state, resolved.Raw)
	}
	attemptResolved, found, err := f.store.GetWorkflowAttempt(context.Background(), attemptID)
	if err != nil || !found {
		t.Fatalf("reload resolved attempt: found=%v err=%v", found, err)
	}
	if attemptResolved.State != domain.WorkflowAttemptStateSucceeded {
		t.Fatalf("durable attempt state = %q, want succeeded", attemptResolved.State)
	}

	// A second audit row, and only one more.
	afterResolve := f.audits(workflowID)
	if len(afterResolve) != len(afterDecide)+1 {
		t.Fatalf("audit rows = %d after one resolve, want %d: %+v",
			len(afterResolve), len(afterDecide)+1, afterResolve)
	}
	resolveAudit := afterResolve[len(afterResolve)-1]
	if resolveAudit.Decision != "resolve_attempt" {
		t.Fatalf("audit decision = %q, want resolve_attempt", resolveAudit.Decision)
	}
	if resolveAudit.Outcome != string(domain.WorkflowAttemptStateSucceeded) {
		t.Fatalf("audit outcome = %q, want succeeded", resolveAudit.Outcome)
	}
	if resolveAudit.TokenFingerprint != wantFingerprint {
		t.Fatalf("resolve audit fingerprint = %q, want %q", resolveAudit.TokenFingerprint, wantFingerprint)
	}

	// -------------------------------------------------------------------
	// 8. Reconcile / resume re-attach; they never spawn a second session.
	// -------------------------------------------------------------------
	sessionsAfterResolve := len(f.sessionsFor())
	dirsBefore := dirCount(t, managedDir)

	reconciled := f.approvedAction(http.MethodPost, f.attemptPath(workflowID, attemptID, "reconcile"), "")
	if reconciled.Status != http.StatusOK {
		t.Fatalf("approved reconcile = %d, want 200; body=%s", reconciled.Status, reconciled.Raw)
	}
	if spawned, _ := reconciled.Body["spawned"].(bool); spawned {
		t.Fatalf("reconcile reported spawned=true: %s", reconciled.Raw)
	}
	resumed := f.approvedAction(http.MethodPost, f.attemptPath(workflowID, attemptID, "resume"), "")
	if resumed.Status != http.StatusOK {
		t.Fatalf("approved resume = %d, want 200; body=%s", resumed.Status, resumed.Raw)
	}
	if spawned, _ := resumed.Body["spawned"].(bool); spawned {
		t.Fatalf("resume reported spawned=true: %s", resumed.Raw)
	}
	if got := f.text(resumed.Body, "binding", "sessionId"); got != string(sessionID) {
		t.Fatalf("resume reported session %q, want the existing %q", got, sessionID)
	}
	if got := len(f.sessionsFor()); got != sessionsAfterResolve {
		t.Fatalf("sessions = %d after reconcile+resume, want %d (neither may spawn)", got, sessionsAfterResolve)
	}
	if got := dirCount(t, managedDir); got != dirsBefore {
		t.Fatalf("worktree directories = %d after reconcile+resume, want %d", got, dirsBefore)
	}
	for key, row := range f.worktrees(sessionID) {
		if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
			t.Fatalf("%s worktree disappeared across recovery: %v", key, statErr)
		}
	}

	// -------------------------------------------------------------------
	// 9. Generation fencing: a replaced controller makes stale work refuse.
	// -------------------------------------------------------------------
	pinnedGeneration := f.binding(attemptID).ControllerGeneration
	if pinnedGeneration == "" {
		t.Fatal("the binding has no controller generation to fence against")
	}

	// Replace the live controller. The chat service rotates the generation; the BINDING keeps
	// the generation it recorded at bind time, so the two now disagree — which is exactly what a
	// stale caller looks like.
	if err := f.chat.Stop(context.Background(), sessionID); err != nil {
		t.Fatalf("stop the bound controller: %v", err)
	}
	if _, err := f.chat.Start(context.Background(), chatsvc.StartConfig{
		SessionID:            sessionID,
		ProjectID:            rec.ProjectID,
		Harness:              m2smDriverHarness,
		WorkspacePath:        rec.Metadata.WorkspacePath,
		ControllerGeneration: "m2ff-replacement-generation",
	}); err != nil {
		t.Fatalf("start a replacement controller: %v", err)
	}
	replacedRec := f.session(sessionID)
	if replacedRec.Metadata.ControllerGeneration == pinnedGeneration {
		t.Fatalf("the controller generation did not rotate (%q); the fence would prove nothing",
			pinnedGeneration)
	}

	// Every intervention mode, sent with the generation the caller LAST SAW (the binding's), must
	// be refused as stale. The route maps ErrExecutionStaleGeneration to 409 TURN_MISMATCH.
	interventionPath := f.attemptPath(workflowID, attemptID, "interventions")
	staleRefusals := 0
	for _, tc := range []struct {
		mode string
		body string
	}{
		{"steer", fmt.Sprintf(
			`{"mode":"steer","text":"stale guidance","clientMessageId":"m2ff-stale-steer",`+
				`"targetProviderTurnId":"turn-nobody-owns","harness":%q,"controllerGeneration":%q}`,
			m2smDriverHarness, pinnedGeneration)},
		{"queue", fmt.Sprintf(
			`{"mode":"queue","text":"stale queue","clientMessageId":"m2ff-stale-queue",`+
				`"controllerGeneration":%q}`, pinnedGeneration)},
		{"stop", fmt.Sprintf(
			`{"mode":"stop","clientMessageId":"m2ff-stale-stop","controllerGeneration":%q}`,
			pinnedGeneration)},
	} {
		refused := f.asOperator(http.MethodPost, interventionPath, tc.body)
		if refused.Status == http.StatusOK {
			t.Fatalf("stale %s was accepted; the generation fence did not fire: %s", tc.mode, refused.Raw)
		}
		if refused.Status != http.StatusConflict {
			t.Fatalf("stale %s = %d, want 409; body=%s", tc.mode, refused.Status, refused.Raw)
		}
		code := f.text(refused.Body, "code")
		reason := f.text(refused.Body, "details", "reasonCode")
		// The fence fires in one of two places depending on the mode and the path it takes; both
		// are stable, exported spellings of the same fact.
		if code != "TURN_MISMATCH" && !strings.EqualFold(reason, "session_fence_failed") {
			t.Fatalf("stale %s code/reason = %q/%q, want TURN_MISMATCH or session_fence_failed; body=%s",
				tc.mode, code, reason, refused.Raw)
		}
		staleRefusals++
	}
	if staleRefusals != 3 {
		t.Fatalf("stale refusals = %d, want 3 (queue, steer, stop)", staleRefusals)
	}
	// A refused intervention is not a receipt.
	for _, key := range []string{"m2ff-stale-steer", "m2ff-stale-queue", "m2ff-stale-stop"} {
		if row, found, err := f.store.GetWorkflowInterventionByClientMessage(context.Background(), workflowID, key); err != nil {
			t.Fatalf("read intervention %s: %v", key, err)
		} else if found {
			t.Fatalf("a stale %s persisted a receipt: %+v", key, row)
		}
	}
	// The stale calls destroyed nothing.
	for key, row := range f.worktrees(sessionID) {
		if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
			t.Fatalf("%s worktree was destroyed by a stale call: %v", key, statErr)
		}
		if head := strings.TrimSpace(m2smGit(t, row.WorktreePath, "rev-parse", "HEAD")); head != pinOf[key] {
			t.Fatalf("%s worktree HEAD changed to %s across stale calls, want %s", key, head, pinOf[key])
		}
	}

	// The other half of the fence: the CURRENT (replacement) generation must be USABLE.
	//
	// A controller replacement rotates the generation of the SAME session. The binding's
	// controller_generation has to follow it, otherwise the fence — which is correct — refuses
	// every intervention forever: fail-closed, but the live controller becomes unaddressable.
	// The repair is a re-anchor: the coordinator's bindObservation path records the observed
	// generation for the session the binding ALREADY names, under a revision+state CAS, and it
	// can never re-point the binding at another session.
	//
	// `resume` is the route that observes the session, so it is the route that re-anchors. A
	// re-anchor is not a transition: the binding is already bound_ready, so its state is
	// untouched and only the recorded controller identity moves.
	currentGeneration := replacedRec.Metadata.ControllerGeneration
	if currentGeneration == "" || currentGeneration == pinnedGeneration {
		t.Fatalf("the replacement generation = %q, want a value distinct from %q",
			currentGeneration, pinnedGeneration)
	}
	bindingBefore := f.binding(attemptID)

	// Put one turn in flight on the replacement controller. This is what a live controller
	// looks like — the session is addressable and a steer has a turn to join — and it is also
	// what makes the session read as live again to the recovery path, which refuses to relaunch
	// an agent that has not exited.
	if _, err := f.chat.Send(context.Background(), sessionID, ports.ChatUserMessage{
		Text: "begin after the replacement", ClientMessageID: "m2ff-current-turn-1",
		Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("start a turn on the replacement controller: %v", err)
	}
	currentTurn := m2AwaitTurn(t, f.chat, sessionID)
	if _, generation, ok := f.chat.RunningProviderTurn(context.Background(), sessionID); !ok ||
		generation != currentGeneration {
		t.Fatalf("running turn generation = %q ok=%v, want the live %q",
			generation, ok, currentGeneration)
	}

	// 内存 turn 先于异步活动投影可见；等待新 controller 的真实事件清除 exited，再验证 re-anchor。
	activityDeadline := time.Now().Add(5 * time.Second)
	for f.session(sessionID).Activity.State == domain.ActivityExited {
		if time.Now().After(activityDeadline) {
			t.Fatal("the replacement controller's turn never cleared the exited activity state")
		}
		time.Sleep(5 * time.Millisecond)
	}
	reanchored := f.approvedAction(http.MethodPost, f.attemptPath(workflowID, attemptID, "resume"), "")
	if reanchored.Status != http.StatusOK {
		t.Fatalf("resume after the controller replacement = %d, want 200; body=%s",
			reanchored.Status, reanchored.Raw)
	}
	if reason := f.text(reanchored.Body, "reasonCode"); reason != "generation_reanchored" {
		t.Fatalf("resume reasonCode = %q, want generation_reanchored; body=%s", reason, reanchored.Raw)
	}
	if got := f.text(reanchored.Body, "binding", "controllerGeneration"); got != currentGeneration {
		t.Fatalf("resume reported binding generation %q, want the live %q; body=%s",
			got, currentGeneration, reanchored.Raw)
	}
	bindingAfter := f.binding(attemptID)
	if bindingAfter.ControllerGeneration != currentGeneration {
		t.Fatalf("parsed binding generation = %q, want the live %q",
			bindingAfter.ControllerGeneration, currentGeneration)
	}
	if bindingAfter.SessionID == nil || *bindingAfter.SessionID != sessionID {
		t.Fatalf("the re-anchor moved the binding to session %v, want the unchanged %s",
			bindingAfter.SessionID, sessionID)
	}
	if bindingAfter.Revision <= bindingBefore.Revision {
		t.Fatalf("binding revision = %d after the re-anchor, want more than %d: the CAS never wrote",
			bindingAfter.Revision, bindingBefore.Revision)
	}
	if bindingAfter.State != bindingBefore.State {
		t.Fatalf("binding state = %q after the re-anchor, want the unchanged %q",
			bindingAfter.State, bindingBefore.State)
	}
	// Idempotent: a second observation at the same generation has nothing to repair.
	repeated := f.approvedAction(http.MethodPost, f.attemptPath(workflowID, attemptID, "resume"), "")
	if repeated.Status != http.StatusOK {
		t.Fatalf("second resume = %d, want 200; body=%s", repeated.Status, repeated.Raw)
	}
	if reason := f.text(repeated.Body, "reasonCode"); reason == "generation_reanchored" {
		t.Fatalf("a repeat observation re-anchored again; body=%s", repeated.Raw)
	}
	if again := f.binding(attemptID); again.Revision != bindingAfter.Revision {
		t.Fatalf("binding revision moved from %d to %d across a no-op observation",
			bindingAfter.Revision, again.Revision)
	}

	// The re-anchor repaired the RECORDED identity and nothing else: the generation this attempt
	// was originally bound with is still refused, with the stable stale-generation code, while the
	// live controller exists to be compared against. This is the assertion that keeps the fix from
	// becoming a "generation always passes" hole.
	staleAfter := f.asOperator(http.MethodPost, interventionPath, fmt.Sprintf(
		`{"mode":"queue","text":"stale after re-anchor","clientMessageId":"m2ff-stale-after",`+
			`"controllerGeneration":%q}`, pinnedGeneration))
	if staleAfter.Status != http.StatusConflict {
		t.Fatalf("pre-rotation queue after the re-anchor = %d, want 409; body=%s",
			staleAfter.Status, staleAfter.Raw)
	}
	if code := f.text(staleAfter.Body, "code"); code != "TURN_MISMATCH" &&
		!strings.EqualFold(f.text(staleAfter.Body, "details", "reasonCode"), "session_fence_failed") {
		t.Fatalf("pre-rotation queue after the re-anchor code = %q, want TURN_MISMATCH or "+
			"session_fence_failed; body=%s", code, staleAfter.Raw)
	}
	if persisted, found, err := f.store.GetWorkflowInterventionByClientMessage(
		context.Background(), workflowID, "m2ff-stale-after",
	); err != nil {
		t.Fatalf("read the refused post-re-anchor queue: %v", err)
	} else if found {
		t.Fatalf("a pre-rotation queue persisted a receipt after the re-anchor: %+v", persisted)
	}

	// Queue, then Steer, then Stop — all at the RE-ANCHORED generation must succeed. Stop comes
	// last because it settles the binding and would stop everything after it.
	acceptedQueue := f.asOperator(http.MethodPost, interventionPath, fmt.Sprintf(
		`{"mode":"queue","text":"current generation","clientMessageId":"m2ff-current-queue",`+
			`"controllerGeneration":%q}`, currentGeneration))
	if acceptedQueue.Status != http.StatusOK {
		t.Fatalf("current-generation queue = %d, want 200; body=%s",
			acceptedQueue.Status, acceptedQueue.Raw)
	}
	if got := f.text(acceptedQueue.Body, "intervention", "controllerGeneration"); got != currentGeneration {
		t.Fatalf("queued receipt generation = %q, want %q; body=%s",
			got, currentGeneration, acceptedQueue.Raw)
	}
	acceptedSteer := f.asOperator(http.MethodPost, interventionPath, fmt.Sprintf(
		`{"mode":"steer","text":"current guidance","clientMessageId":"m2ff-current-steer",`+
			`"targetProviderTurnId":%q,"harness":%q,"controllerGeneration":%q}`,
		currentTurn, m2smDriverHarness, currentGeneration))
	if acceptedSteer.Status != http.StatusOK {
		t.Fatalf("current-generation steer = %d, want 200; body=%s",
			acceptedSteer.Status, acceptedSteer.Raw)
	}
	if state := f.text(acceptedSteer.Body, "intervention", "state"); state != "applied" {
		t.Fatalf("steer state = %q, want applied; body=%s", state, acceptedSteer.Raw)
	}
	acceptedStop := f.asOperator(http.MethodPost, interventionPath, fmt.Sprintf(
		`{"mode":"stop","clientMessageId":"m2ff-current-stop","controllerGeneration":%q}`,
		currentGeneration))
	if acceptedStop.Status != http.StatusOK {
		t.Fatalf("current-generation stop = %d, want 200; body=%s",
			acceptedStop.Status, acceptedStop.Raw)
	}
	if state := f.text(acceptedStop.Body, "intervention", "state"); state != "stop_confirmed" {
		t.Fatalf("stop state = %q, want stop_confirmed; body=%s", state, acceptedStop.Raw)
	}
	// A successful intervention is a receipt, and it names the generation it acted on.
	for _, key := range []string{"m2ff-current-queue", "m2ff-current-steer", "m2ff-current-stop"} {
		row, found, err := f.store.GetWorkflowInterventionByClientMessage(context.Background(), workflowID, key)
		if err != nil {
			t.Fatalf("read intervention %s: %v", key, err)
		}
		if !found {
			t.Fatalf("%s was accepted but persisted no receipt", key)
		}
		if row.ControllerGeneration != currentGeneration {
			t.Fatalf("%s generation = %q, want %q", key, row.ControllerGeneration, currentGeneration)
		}
	}
	// The re-anchor repaired the RECORDED identity only; the session is the same one.
	if bindingFinal := f.binding(attemptID); bindingFinal.SessionID == nil ||
		*bindingFinal.SessionID != sessionID {
		t.Fatalf("binding session = %v after the accepted interventions, want %s",
			bindingFinal.SessionID, sessionID)
	}

	for key, row := range f.worktrees(sessionID) {
		if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
			t.Fatalf("%s worktree was destroyed across the fenced calls: %v", key, statErr)
		}
		if head := strings.TrimSpace(m2smGit(t, row.WorktreePath, "rev-parse", "HEAD")); head != pinOf[key] {
			t.Fatalf("%s worktree HEAD is %s after the fenced calls, want %s", key, head, pinOf[key])
		}
	}

	// -------------------------------------------------------------------
	// 10. Daemon restart.
	// -------------------------------------------------------------------
	// An approval issued BEFORE the restart, confirmed and ready to use. A restart must refuse it:
	// approvals live in memory, per boot.
	preRestartBody := fmt.Sprintf(
		`{"outcome":"succeeded","expectedTaskRevision":%d,"idempotencyKey":"m2ff-prerestart"}`,
		taskForResolve.Revision)
	preRestartApproval := f.requestApproval(http.MethodPost, f.attemptPath(workflowID, attemptID, "resolve"), preRestartBody)
	f.confirmApproval(preRestartApproval)

	sessionsBeforeRestart := len(f.sessionsFor())
	pinsBeforeRestart := map[string]string{}
	for key, row := range f.worktrees(sessionID) {
		pinsBeforeRestart[key] = row.CreationSHA
	}
	projectsBeforeRestart := map[domain.WorkflowRepoKey]domain.ProjectID{}
	for key, row := range f.repoBindings(attemptID) {
		projectsBeforeRestart[key] = row.ProjectID
	}
	replacedGenerationBeforeRestart := f.session(sessionID).Metadata.ControllerGeneration
	if replacedGenerationBeforeRestart != currentGeneration {
		t.Fatalf("the session generation before the restart = %q, want the re-anchored "+
			"controller %q", replacedGenerationBeforeRestart, currentGeneration)
	}
	// The binding as it stands immediately before the restart. Step 9's accepted queue/steer/stop
	// legitimately advanced it, so this — not the pre-intervention snapshot — is what must
	// survive unchanged.
	bindingPreRestart := f.binding(attemptID)

	f.stop()

	second := newM2FFFixture(t, plant)

	// (a) The pre-restart approval is refused — the table is gone.
	afterRestart := second.do(http.MethodPost, preRestartApproval.Path, preRestartApproval.Body,
		map[string]string{principal.HeaderOperatorApproval: preRestartApproval.ID})
	if afterRestart.Status != http.StatusForbidden {
		t.Fatalf("pre-restart approval after restart = %d, want 403; body=%s",
			afterRestart.Status, afterRestart.Raw)
	}
	if code := second.text(afterRestart.Body, "code"); code != principal.CodeApprovalInvalid {
		t.Fatalf("pre-restart approval code = %q, want %q", code, principal.CodeApprovalInvalid)
	}
	if reason := second.text(afterRestart.Body, "details", "reason"); reason != principal.ReasonApprovalUnknown {
		t.Fatalf("pre-restart approval reason = %q, want %q", reason, principal.ReasonApprovalUnknown)
	}

	// 重启后仍需新的逐操作审批，原生确认不依赖持久化 token。
	if reread := second.asOperator(http.MethodGet, f.attemptPath(workflowID, attemptID, "binding"), ""); reread.Status != http.StatusOK {
		t.Fatalf("重启后审批读取 = %d, want 200; body=%s", reread.Status, reread.Raw)
	}
	if _, err := os.Stat(filepath.Join(dataDir, principal.OperatorTokenFileName)); !os.IsNotExist(err) {
		t.Fatalf("不应产生 operator token 文件: %v", err)
	}
	// 新建审批可以再次通过原生确认。
	freshApproval := second.requestApproval(http.MethodPost, second.bindPath(workflowID, attemptID), "")
	second.confirmApproval(freshApproval)
	// (The bind above is only confirmed, never submitted: the point is that confirmation still
	// works after a restart, not that a settled binding re-binds.)

	// (c) Every durable fact survived unchanged.
	//
	// The generation invariant is now the CORRECT one: the binding tracks the controller that
	// was CURRENT when the daemon stopped, which is the re-anchored value from step 9 — not the
	// pre-rotation generation it was bound with. A rotation is recorded only through the
	// re-anchor CAS, which requires the same session, a real (never a `pending:` placeholder)
	// generation, and the exact revision and state the caller read; so "the binding followed the
	// live controller" and "the binding never invents an owner" are both true at once.
	rereadBinding := second.binding(attemptID)
	if rereadBinding.SessionID == nil || *rereadBinding.SessionID != sessionID {
		t.Fatalf("binding after restart names session %v, want %s", rereadBinding.SessionID, sessionID)
	}
	if rereadBinding.ControllerGeneration == pinnedGeneration {
		t.Fatalf("binding generation after restart = %q, the PRE-rotation one; it must track the "+
			"controller that was current before the restart (%q)",
			rereadBinding.ControllerGeneration, currentGeneration)
	}
	if rereadBinding.ControllerGeneration != currentGeneration {
		t.Fatalf("binding generation after restart = %q, want the re-anchored %q",
			rereadBinding.ControllerGeneration, currentGeneration)
	}
	if rereadBinding.ControllerGeneration != bindingPreRestart.ControllerGeneration {
		t.Fatalf("binding generation across the restart = %q, want the durable %q",
			rereadBinding.ControllerGeneration, bindingPreRestart.ControllerGeneration)
	}
	// The revision proves two things at once: the re-anchor in step 9 was a real CAS write (it is
	// greater than the pre-re-anchor revision), and the restart itself invented no write (it is
	// exactly the value the binding held before the daemon stopped).
	if rereadBinding.Revision <= bindingBefore.Revision {
		t.Fatalf("binding revision after restart = %d, want more than the pre-re-anchor %d",
			rereadBinding.Revision, bindingBefore.Revision)
	}
	if rereadBinding.Revision != bindingPreRestart.Revision {
		t.Fatalf("binding revision across the restart = %d, want %d",
			rereadBinding.Revision, bindingPreRestart.Revision)
	}
	// The OLD generation is not the recorded one after the restart, and it is still refused. The
	// restart leaves no live controller, so the fence cannot be evaluated here (the coordinator
	// answers session_not_live before it ever compares generations) — the strong form of this
	// assertion lives in step 9, where a live controller exists. What must hold here is that the
	// pre-rotation generation was never re-adopted by the restart, and that presenting it still
	// produces a refusal rather than a receipt.
	if stray := second.asOperator(http.MethodPost, interventionPath, fmt.Sprintf(
		`{"mode":"stop","clientMessageId":"m2ff-postrestart-stale","controllerGeneration":%q}`,
		pinnedGeneration)); stray.Status != http.StatusConflict {
		t.Fatalf("pre-rotation stop after restart = %d, want 409; body=%s", stray.Status, stray.Raw)
	} else if code := second.text(stray.Body, "code"); code != "TURN_MISMATCH" &&
		code != "SESSION_NOT_READY" &&
		!strings.EqualFold(second.text(stray.Body, "details", "reasonCode"), "session_fence_failed") {
		t.Fatalf("pre-rotation stop after restart code = %q, want TURN_MISMATCH, "+
			"SESSION_NOT_READY, or session_fence_failed; body=%s", code, stray.Raw)
	}
	if persisted, found, err := second.store.GetWorkflowInterventionByClientMessage(
		context.Background(), workflowID, "m2ff-postrestart-stale",
	); err != nil {
		t.Fatalf("read the refused pre-rotation stop: %v", err)
	} else if found {
		t.Fatalf("a pre-rotation stop persisted a receipt after restart: %+v", persisted)
	}
	rowsAfterRestart := second.worktrees(sessionID)
	if len(rowsAfterRestart) != 2 {
		t.Fatalf("worktree rows after restart = %d, want 2", len(rowsAfterRestart))
	}
	for key, row := range rowsAfterRestart {
		if row.CreationSHA != pinsBeforeRestart[key] {
			t.Fatalf("%s CreationSHA after restart = %q, want %q",
				key, row.CreationSHA, pinsBeforeRestart[key])
		}
		if row.CreationSHA != pinOf[key] {
			t.Fatalf("%s CreationSHA after restart = %q, want the pinned commit %q",
				key, row.CreationSHA, pinOf[key])
		}
		if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
			t.Fatalf("%s worktree is gone after restart: %v", key, statErr)
		}
		if head := strings.TrimSpace(m2smGit(t, row.WorktreePath, "rev-parse", "HEAD")); head != pinOf[key] {
			t.Fatalf("%s worktree HEAD after restart = %s, want %s", key, head, pinOf[key])
		}
	}
	for key, project := range projectsBeforeRestart {
		if got := second.repoBindings(attemptID)[key].ProjectID; got != project {
			t.Fatalf("%s ProjectID after restart = %q, want %q", key, got, project)
		}
	}
	if got := second.repoBindings(attemptID)[m2smRepoA].ProjectID; got != m2ffProjectA {
		t.Fatalf("repo-a ProjectID after restart = %q, want %q", got, m2ffProjectA)
	}
	if got := second.repoBindings(attemptID)[m2smRepoB].ProjectID; got != m2ffProjectB {
		t.Fatalf("repo-b ProjectID after restart = %q, want %q", got, m2ffProjectB)
	}

	// 前一步已明确 Stop：重启后保持 stop_confirmed，不重新启动 controller。
	// 活跃会话的真实恢复由 FormalCLI 与 SessionManager 重启用例验证。
	dirsAfterRestart := dirCount(t, managedDir)
	reconciledAfter := second.approvedAction(http.MethodPost, second.attemptPath(workflowID, attemptID, "reconcile"), "")
	if reconciledAfter.Status != http.StatusOK || second.text(reconciledAfter.Body, "reasonCode") != "not_unknown" {
		t.Fatalf("reconcile after stopped restart = %d, want 200/not_unknown; body=%s",
			reconciledAfter.Status, reconciledAfter.Raw)
	}
	if spawned, _ := reconciledAfter.Body["spawned"].(bool); spawned {
		t.Fatalf("reconcile after restart reported spawned=true: %s", reconciledAfter.Raw)
	}
	if got := second.binding(attemptID).State; got != domain.WorkflowBindingStateStopConfirmed {
		t.Fatalf("binding state after reconcile = %q, want the settled stop_confirmed", got)
	}
	resumedAfter := second.approvedAction(http.MethodPost, second.attemptPath(workflowID, attemptID, "resume"), "")
	if resumedAfter.Status != http.StatusOK || second.text(resumedAfter.Body, "reasonCode") != "not_resumable" {
		t.Fatalf("resume after stopped restart = %d, want 200/not_resumable; body=%s", resumedAfter.Status, resumedAfter.Raw)
	}
	if second.binding(attemptID).State != domain.WorkflowBindingStateStopConfirmed || second.chat.HasLiveChatController(sessionID) {
		t.Fatal("已停止的会话被重启恢复激活")
	}
	if spawned, _ := resumedAfter.Body["spawned"].(bool); spawned {
		t.Fatalf("resume after restart reported spawned=true: %s", resumedAfter.Raw)
	}
	if got := second.text(resumedAfter.Body, "binding", "sessionId"); got != string(sessionID) {
		t.Fatalf("resume after restart reported session %q, want the existing %q", got, sessionID)
	}
	if got := len(second.sessionsFor()); got != sessionsBeforeRestart {
		t.Fatalf("sessions after restart = %d, want %d (the same one)", got, sessionsBeforeRestart)
	}
	if got := dirCount(t, managedDir); got != dirsAfterRestart {
		t.Fatalf("worktree directories after restart = %d, want %d (re-verified, not recreated)",
			got, dirsAfterRestart)
	}
	if got := len(second.audits(workflowID)); got < len(afterResolve) {
		t.Fatalf("audit rows after restart = %d, want at least the %d recorded before it", got, len(afterResolve))
	}
}

// ---------------------------------------------------------------------------
// the negative matrix: no operator proof, nothing happens
// ---------------------------------------------------------------------------

func TestM2FullFlowRefusesEverythingWithoutOperatorProof(t *testing.T) {
	repoA := m2ffRepo(t, "charlie")
	repoB := m2ffRepo(t, "delta")
	pinA := strings.TrimSpace(m2smGit(t, repoA, "rev-parse", "HEAD~1"))
	pinB := strings.TrimSpace(m2smGit(t, repoB, "rev-parse", "HEAD~2"))
	if pinA == pinB {
		t.Fatalf("the two pins are identical (%s)", pinA)
	}

	dataDir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	f := newM2FFFixture(t, m2ffPlant{
		store: st, dataDir: dataDir, managedDir: t.TempDir(), repoA: repoA, repoB: repoB,
	})

	workflowID, taskID := f.createWorkflowWithPins("m2ff-refusals", "do the work", pinA, pinB)
	attemptID, _ := f.claimAndStart(workflowID, taskID, "m2ff-refusals")

	// The request this matrix will try (and fail) to decide. It is seeded through the store
	// because raising one requires a bound attempt, and NOTHING here may bind: the whole point is
	// that no operator proof was presented. The row is the same shape the agent route writes.
	workflowRec, foundWorkflow, err := f.store.GetWorkflow(context.Background(), workflowID)
	if err != nil || !foundWorkflow {
		t.Fatalf("read workflow: found=%v err=%v", foundWorkflow, err)
	}
	const refusalPayload = `{"credits":5}`
	raised, createdReq, err := f.store.CreateWorkflowTaskRequest(context.Background(), domain.WorkflowTaskRequest{
		ID: "m2ff-refusal-req", WorkflowID: workflowID, RequesterTaskID: taskID,
		RequestKind: domain.WorkflowTaskRequestKindIncreaseBudget,
		RequestJSON: refusalPayload, RequestSHA: collaboration.RequestSHA(refusalPayload),
		WorkflowRevision: workflowRec.Revision,
	}, time.Now().UTC())
	if err != nil || !createdReq {
		t.Fatalf("seed task request: created=%v err=%v", createdReq, err)
	}
	requestRevision := raised.Revision

	task, found, err := f.store.GetWorkflowTask(context.Background(), workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("read task: found=%v err=%v", found, err)
	}

	decideBody := fmt.Sprintf(`{"approve":true,"expectedRevision":%d}`, requestRevision)
	bindBody := fmt.Sprintf(`{"harness":%q,"mode":"chat","prompt":"work","idempotencyKey":"m2ff-refuse-bind"}`, m2smDriverHarness)
	resolveBody := fmt.Sprintf(
		`{"outcome":"succeeded","expectedTaskRevision":%d,"idempotencyKey":"m2ff-refuse-resolve"}`, task.Revision)

	ops := []struct {
		name string
		path string
		body string
	}{
		{"bind", f.bindPath(workflowID, attemptID), bindBody},
		{"decide", "/workflows/" + string(workflowID) + "/task-requests/m2ff-refusal-req/decide", decideBody},
		{"resolve", f.attemptPath(workflowID, attemptID, "resolve"), resolveBody},
		{"reconcile", f.attemptPath(workflowID, attemptID, "reconcile"), ""},
		{"resume", f.attemptPath(workflowID, attemptID, "resume"), ""},
	}

	// 1. NO header at all: every operator route refuses. A loopback peer alone is not the
	// operator, which is the whole reason the approval proof exists.
	for _, op := range ops {
		refused := f.do(http.MethodPost, op.path, op.body, nil)
		if refused.Status != http.StatusForbidden {
			t.Fatalf("no-proof %s = %d, want 403; body=%s", op.name, refused.Status, refused.Raw)
		}
		if code := f.text(refused.Body, "code"); code != "COLLAB_NOT_AUTHORIZED" {
			t.Fatalf("no-proof %s code = %q, want COLLAB_NOT_AUTHORIZED; body=%s",
				op.name, code, refused.Raw)
		}
	}

	// 2. A random token is not a proof.
	for _, op := range ops {
		refused := f.do(http.MethodPost, op.path, op.body,
			map[string]string{principal.HeaderOperatorToken: "not-the-operator-token"})
		if refused.Status != http.StatusForbidden {
			t.Fatalf("wrong-token %s = %d, want 403; body=%s", op.name, refused.Status, refused.Raw)
		}
	}

	// 3. A random approval id is not a proof.
	for _, op := range ops {
		refused := f.do(http.MethodPost, op.path, op.body,
			map[string]string{principal.HeaderOperatorApproval: "no-such-approval"})
		if refused.Status != http.StatusForbidden {
			t.Fatalf("unknown-approval %s = %d, want 403; body=%s", op.name, refused.Status, refused.Raw)
		}
		if code := f.text(refused.Body, "code"); code != principal.CodeApprovalInvalid {
			t.Fatalf("unknown-approval %s code = %q, want %q", op.name, code, principal.CodeApprovalInvalid)
		}
		if reason := f.text(refused.Body, "details", "reason"); reason != principal.ReasonApprovalUnknown {
			t.Fatalf("unknown-approval %s reason = %q, want %q", op.name, reason, principal.ReasonApprovalUnknown)
		}
	}
	// Nothing above may have produced a binding or a decision.
	if _, found, err := f.store.GetWorkflowAttemptBinding(context.Background(), attemptID); err != nil || found {
		t.Fatalf("a refused bind created a binding row: found=%v err=%v", found, err)
	}
	if got := len(f.audits(workflowID)); got != 0 {
		t.Fatalf("refused requests wrote %d audit row(s)", got)
	}

	// 4. A confirmed approval is single-use: the second use is refused.
	replayBody := fmt.Sprintf(`{"harness":%q,"mode":"chat","prompt":"work","idempotencyKey":"m2ff-replay-bind"}`, m2smDriverHarness)
	replayApproval := f.requestApproval(http.MethodPost, f.bindPath(workflowID, attemptID), replayBody)
	// 同用户进程即便提供旧 token，OS 拒绝后审批仍不可消费。
	f.consent.deny(principal.ErrConsentDenied)
	denied := f.do(http.MethodPost, "/operator/approvals/"+replayApproval.ID+"/confirm",
		fmt.Sprintf(`{"code":%q,"payloadDigest":%q}`, replayApproval.Code, principal.PayloadDigest([]byte(replayBody))),
		map[string]string{principal.HeaderOperatorToken: "legacy-token-readable-by-agent"})
	if denied.Status != http.StatusForbidden || len(f.consent.approvals()) != 1 {
		t.Fatalf("旧 token 绕过原生审批: %d %s", denied.Status, denied.Raw)
	}
	if refused := f.submitWithApproval(replayApproval); refused.Status != http.StatusForbidden {
		t.Fatalf("OS 拒绝后仍能消费: %d %s", refused.Status, refused.Raw)
	}
	f.consent.deny(nil)
	f.confirmApproval(replayApproval)
	first := f.submitWithApproval(replayApproval)
	if first.Status != http.StatusOK {
		t.Fatalf("first use of the replay approval = %d, want 200; body=%s", first.Status, first.Raw)
	}
	second := f.submitWithApproval(replayApproval)
	if second.Status != http.StatusForbidden {
		t.Fatalf("replayed approval = %d, want 403; body=%s", second.Status, second.Raw)
	}
	if reason := f.text(second.Body, "details", "reason"); reason != principal.ReasonApprovalConsumed {
		t.Fatalf("replayed approval reason = %q, want %q", reason, principal.ReasonApprovalConsumed)
	}

	// 5. An approval for the BIND route does not authorise DECIDE: the operation is part of the
	// binding, so a route cannot be swapped.
	crossBody := decideBody
	bindForCross := f.requestApproval(http.MethodPost, f.bindPath(workflowID, attemptID),
		fmt.Sprintf(`{"harness":%q,"mode":"chat","prompt":"cross","idempotencyKey":"m2ff-cross-bind"}`, m2smDriverHarness))
	f.confirmApproval(bindForCross)
	crossed := f.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/task-requests/m2ff-refusal-req/decide", crossBody,
		map[string]string{principal.HeaderOperatorApproval: bindForCross.ID})
	if crossed.Status != http.StatusForbidden {
		t.Fatalf("bind approval used for decide = %d, want 403; body=%s", crossed.Status, crossed.Raw)
	}
	if reason := f.text(crossed.Body, "details", "reason"); reason != principal.ReasonApprovalOperationMismatch {
		t.Fatalf("cross-operation approval reason = %q, want %q",
			reason, principal.ReasonApprovalOperationMismatch)
	}

	// Nothing in this whole test may have decided the task request it raised.
	undecided, foundUndecided, err := f.store.GetWorkflowTaskRequest(context.Background(), workflowID, "m2ff-refusal-req")
	if err != nil || !foundUndecided {
		t.Fatalf("read task request: found=%v err=%v", foundUndecided, err)
	}
	if undecided.State != domain.WorkflowTaskRequestStatePending {
		t.Fatalf("task request state = %q, want pending: a refused approval decided it", undecided.State)
	}
	if got := len(f.audits(workflowID)); got != 0 {
		t.Fatalf("a refused flow wrote %d audit row(s)", got)
	}
}
