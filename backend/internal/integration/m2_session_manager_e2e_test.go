package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/scripted"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
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

// R-07: the REAL Session Manager over REAL git worktrees.
//
// The M2 E2E fixture (m2_e2e_test.go) substitutes the Session Manager with a
// fakeSessionSpawner that writes rows and never runs Git. That proves the HTTP/service
// stack but not the chain this file proves:
//
//	Workflow → Claim → Bind → LocalGateway → real SessionManager
//	         → scripted Chat driver → real git worktree → Queue/Steer/Stop
//	         → Reconcile/Resume
//
// What is real here: the migrated SQLite store, the session manager itself (spawn,
// worktree provisioning, controller launch, resume), the gitworktree adapter, Git, the
// Chat service with its controller, the execution/intervention/collaboration
// coordinators, the workflow service, and the HTTP router.
//
// What is fake, and why:
//
//   - the terminal runtime: a chat session runs no agent inside a runtime, so the only
//     thing the fake does is answer liveness. Nothing is executed.
//   - the agent adapter registry: it answers the harness catalogue without inspecting an
//     installed CLI.
//   - the Chat DRIVER is scripted (adapters/chatdriver/scripted): it speaks AO's own port
//     vocabulary and starts no provider. It is the same driver the M2 E2E uses.
//
// The fakes below are local to this package ON PURPOSE: session_manager's own test fakes
// are unexported, and exporting test helpers from that package to serve an integration
// test would be a production-package change made for tests. Everything here is written
// against the exported Manager/Deps surface.

// ---------------------------------------------------------------------------
// fakes (local, minimal, exported surface only)
// ---------------------------------------------------------------------------

// m2smRuntime is a terminal runtime that never starts anything.
type m2smRuntime struct {
	mu      sync.Mutex
	created int
}

func (r *m2smRuntime) Create(context.Context, ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created++
	return ports.RuntimeHandle{ID: "m2sm-handle"}, nil
}

func (r *m2smRuntime) Destroy(context.Context, ports.RuntimeHandle) error { return nil }

func (r *m2smRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return "", nil
}

func (r *m2smRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return true, nil
}

// ProbeFencedRuntime reports alive for the handle this fake invented, so a live-session
// probe never turns a chat session into a dead one for the wrong reason.
func (r *m2smRuntime) ProbeFencedRuntime(_ context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	if ref.Handle.ID == "" {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	return ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
}

// m2smAgent is a harness adapter that builds no launch command of consequence: a chat
// session does not use it (the Chat service owns the controller), and a TUI session is
// never spawned here.
type m2smAgent struct{}

func (m2smAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}

func (m2smAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return []string{"m2sm-launch"}, nil
}

func (m2smAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryInCommand, nil
}

func (m2smAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error { return nil }

func (m2smAgent) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return nil, false, nil
}

func (m2smAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

// m2smAgents resolves the harnesses this fixture uses without touching an install.
type m2smAgents struct{}

func (m2smAgents) Agent(h domain.AgentHarness) (ports.Agent, bool) {
	if h == domain.HarnessCodex {
		return m2smAgent{}, true
	}
	return nil, false
}

// m2smMessenger accepts terminal writes. A chat session writes none, so a recorded
// message would be a bug rather than evidence.
type m2smMessenger struct {
	mu   sync.Mutex
	msgs []string
}

func (m *m2smMessenger) Send(_ context.Context, _ domain.SessionID, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
	return nil
}

// m2smChatLauncher is the integration-side equivalent of daemon.chatLauncher: it
// forwards the Session Manager's ChatLauncher calls to the real Chat service.
type m2smChatLauncher struct {
	svc             *chatsvc.Service
	workspaceChecks chan error
}

func (l m2smChatLauncher) SupportsChat(h domain.AgentHarness) bool { return l.svc.SupportsChat(h) }

func (l m2smChatLauncher) PreflightChat(ctx context.Context, h domain.AgentHarness, p ports.PermissionMode) error {
	return l.svc.PreflightChat(ctx, h, p)
}

// StartChat 观察真实 Manager 回调的结果，不改变验证逻辑或返回错误。
func (l m2smChatLauncher) StartChat(ctx context.Context, cfg sessionmanager.ChatStart) (sessionmanager.ChatStarted, error) {
	if cfg.ValidateWorkspace != nil && l.workspaceChecks != nil {
		verify := cfg.ValidateWorkspace
		cfg.ValidateWorkspace = func(ctx context.Context) error {
			err := verify(ctx)
			select {
			case l.workspaceChecks <- err:
			default:
			}
			return err
		}
	}
	return l.svc.StartChat(ctx, cfg)
}

func (l m2smChatLauncher) StartChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	return l.svc.StartChatTurn(ctx, id, text)
}

func (l m2smChatLauncher) RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error) {
	return l.svc.RelaySessionChatTurn(ctx, id, text, clientMessageID, options)
}

func (l m2smChatLauncher) HasLiveChatController(id domain.SessionID) bool {
	return l.svc.HasLiveChatController(id)
}

func (l m2smChatLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	return l.svc.StopChat(ctx, id)
}

func (l m2smChatLauncher) QueueChatPrompt(ctx context.Context, id domain.SessionID, text string) (string, error) {
	turn, err := l.svc.QueueUserMessage(ctx, id, ports.ChatUserMessage{
		Text: text, Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		return "", err
	}
	return turn.ID, nil
}

func (l m2smChatLauncher) DrainChatQueue(ctx context.Context, id domain.SessionID) error {
	return l.svc.DrainQueued(ctx, id)
}

// m2smReadiness reports an installed, signed-in harness without a probe process.
type m2smReadiness struct{}

func (m2smReadiness) EnsureAgentReadiness(
	_ context.Context,
	agentID string,
	_ domain.AgentReadinessPurpose,
) (domain.AgentReadinessSnapshot, error) {
	now := time.Now().UTC()
	return domain.AgentReadinessSnapshot{
		ID: agentID, Label: agentID,
		Installation:       domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled, CheckedAt: &now},
		Authentication:     domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized, CheckedAt: &now},
		EffectiveReadiness: domain.AgentReadinessReady,
	}, nil
}

func (m2smReadiness) InvalidateAgentInstallation(string)   {}
func (m2smReadiness) InvalidateAgentAuthentication(string) {}
func (m2smReadiness) RecheckAgent(string)                  {}

// m2smChatProbe reads the live controller exactly as the daemon's adapter does.
type m2smChatProbe struct{ svc *chatsvc.Service }

func (p m2smChatProbe) SessionGeneration(id domain.SessionID) (string, bool) {
	controller, err := p.svc.Controller(id)
	if err != nil || controller == nil || controller.State() == ports.ChatControllerStopped {
		return "", false
	}
	return controller.Generation(), true
}

func (p m2smChatProbe) ChatCapabilities(id domain.SessionID) (ports.ChatCapabilities, bool) {
	controller, err := p.svc.Controller(id)
	if err != nil || controller == nil || controller.State() == ports.ChatControllerStopped {
		return nil, false
	}
	return controller.Capabilities(), true
}

// m2smActivity records nothing: lifecycle is the real recorder below.
type m2smOwnership struct{ store *store.Store }

func (o m2smOwnership) ResolveAttemptOwnership(
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

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

const (
	m2smDriverHarness = domain.HarnessCodex
	m2smRepoA         = domain.WorkflowRepoKey("repo-a")
	m2smRepoB         = domain.WorkflowRepoKey("repo-b")
)

type m2smFixture struct {
	t               *testing.T
	store           *store.Store
	chat            *chatsvc.Service
	mgr             *sessionmanager.Manager
	driver          *scripted.Driver
	workspaceChecks <-chan error
	srv             *httptest.Server
	base            string

	dataDir     string
	managedRoot string
	projectA    domain.ProjectID
	projectB    domain.ProjectID
	repoA       string
	repoB       string
}

// newM2SMFixture builds the whole chain over an EXISTING store and managed root, which
// is what makes the restart scenario a restart rather than a second test.
func newM2SMFixture(t *testing.T, st *store.Store, dataDir, managedRoot string) *m2smFixture {
	t.Helper()
	ctx := context.Background()
	clock := func() time.Time { return time.Now().UTC() }

	projectA, projectB := domain.ProjectID("m2sm-project-a"), domain.ProjectID("m2sm-project-b")
	// 重启复用已注册仓库，不能将同一 ProjectID 改绑到新建的临时仓库。
	repoFor := func(id domain.ProjectID) string {
		project, found, err := st.GetProject(ctx, string(id))
		if err != nil {
			t.Fatal(err)
		}
		if found {
			return project.Path
		}
		return m2smGitRepo(t)
	}
	repoA, repoB := repoFor(projectA), repoFor(projectB)
	for _, project := range []domain.ProjectRecord{
		{
			ID: string(projectA), Path: repoA, RegisteredAt: time.Now().UTC().Truncate(time.Second),
			Config: domain.ProjectConfig{DefaultBranch: "main", Worker: domain.RoleOverride{Harness: m2smDriverHarness}},
		},
		{
			ID: string(projectB), Path: repoB, RegisteredAt: time.Now().UTC().Truncate(time.Second),
			Config: domain.ProjectConfig{DefaultBranch: "main", Worker: domain.RoleOverride{Harness: m2smDriverHarness}},
		},
	} {
		if err := st.UpsertProject(ctx, project); err != nil {
			t.Fatalf("seed project %s: %v", project.ID, err)
		}
	}

	ws, err := gitworktree.New(gitworktree.Options{
		ManagedRoot: managedRoot,
		RepoResolver: gitworktree.StaticRepoResolver{
			projectA: repoA, projectB: repoB,
		},
	})
	if err != nil {
		t.Fatalf("gitworktree: %v", err)
	}

	driver := scripted.NewDriver(scripted.Config{
		Harness: m2smDriverHarness,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
			ports.ChatCapabilitySteer:     true,
		},
		Steerable: true,
	})
	registry := scripted.Registry(driver)

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
		NewID:      m2smIDFactory("m2sm"),
	})
	t.Cleanup(func() { chatSvc.StopAll(context.Background()) })

	recorder := capability.NewRecorder(st)
	resolver := capability.NewResolver(capability.WithRecorder(recorder), capability.WithClock(clock))
	probe := capability.NewChatDriverProbe(
		m2smChatProbe{svc: chatSvc}, registry, m2smReadiness{}, clock, capability.DefaultTTL,
	)
	resolver.RegisterProbe(m2smDriverHarness, probe)

	workspaceChecks := make(chan error, 64)
	mgr := sessionmanager.New(sessionmanager.Deps{
		Runtime:   &m2smRuntime{},
		Agents:    m2smAgents{},
		Workspace: ws,
		Store:     st,
		Messenger: &m2smMessenger{},
		Chat:      m2smChatLauncher{svc: chatSvc, workspaceChecks: workspaceChecks},
		Lifecycle: lcm,
		DataDir:   dataDir,
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
		Executable: func() (string, error) {
			return filepath.Join(dataDir, "ao"), nil
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
	approvals := principal.NewApprovalStore(principal.ApprovalStoreOptions{Clock: clock})
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
		OperatorConsent:       &m2Consent{},
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	return &m2smFixture{
		t: t, store: st, chat: chatSvc, mgr: mgr, driver: driver,
		workspaceChecks: workspaceChecks,
		srv:             srv, base: srv.URL + "/api/v1",
		dataDir: dataDir, managedRoot: managedRoot,
		projectA: projectA, projectB: projectB, repoA: repoA, repoB: repoB,
	}
}

// stopControllers releases the in-memory controllers, which is what a daemon shutdown
// does. It deliberately closes nothing that owns the database.
func (f *m2smFixture) stopControllers() { f.chat.StopAll(context.Background()) }

func (f *m2smFixture) closeServer() { f.srv.Close() }

func m2smGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	m2smGit(t, dir, "init")
	m2smGit(t, dir, "config", "core.autocrlf", "false")
	m2smGit(t, dir, "config", "user.email", "ao@example.com")
	m2smGit(t, dir, "config", "user.name", "AO Tests")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	m2smGit(t, dir, "add", "README.md")
	m2smGit(t, dir, "commit", "-m", "base")
	m2smGit(t, dir, "branch", "-M", "main")
	return dir
}

func m2smGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// m2smIDFactory 为每次启动生成唯一前缀，避免 SQLite 重开时复用 controller generation。
func m2smIDFactory(prefix string) chatsvc.IDFactory {
	prefix += "-" + rand.Text()
	var (
		mu      sync.Mutex
		counter int
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		counter++
		return fmt.Sprintf("%s-%05d", prefix, counter)
	}
}

func m2smSnapshotReader(st *store.Store) chatsvc.SnapshotReader {
	return chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshot(ctx, id)
		return chatsvc.ConversationRows{
			Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns,
			Messages: rows.Messages, Activities: rows.Activities,
			BranchPoints: rows.BranchPoints, BranchedFromEarlierMessage: rows.BranchedFromEarlierMessage,
		}, err
	})
}

func m2smPageReader(st *store.Store) chatsvc.SnapshotPageReader {
	return chatsvc.SnapshotPageReaderFunc(func(ctx context.Context, id string, before, limit int64) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshotPage(ctx, id, before, limit)
		return chatsvc.ConversationRows{
			Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch,
			Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities,
		}, err
	})
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

type m2smResp struct {
	Status int
	Body   map[string]any
	Raw    string
}

func (f *m2smFixture) do(method, path, body string) m2smResp {
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
	for key, value := range m2ApprovedHeaders(f.t, f.base, method, path, body) {
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
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = decoder.Decode(&decoded)
	}
	return m2smResp{Status: resp.StatusCode, Body: decoded, Raw: string(raw)}
}

func (f *m2smFixture) path(body map[string]any, keys ...string) any {
	var current any = body
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

func (f *m2smFixture) text(body map[string]any, keys ...string) string {
	value, _ := f.path(body, keys...).(string)
	return value
}

func (f *m2smFixture) number(body map[string]any, keys ...string) int64 {
	value, _ := f.path(body, keys...).(json.Number)
	n, _ := value.Int64()
	return n
}

// createWorkflow builds and activates a two-repository workflow with one task.
func (f *m2smFixture) createWorkflow(key string) (domain.WorkflowID, domain.WorkflowTaskID) {
	f.t.Helper()
	return f.createWorkflowWithBases(key, "", "")
}

// createWorkflowWithBases is createWorkflow with an explicit base commit per
// repository, which is what a caller that planned from a historical commit
// records.
func (f *m2smFixture) createWorkflowWithBases(
	key, baseSHAA, baseSHAB string,
) (domain.WorkflowID, domain.WorkflowTaskID) {
	f.t.Helper()
	taskID := domain.WorkflowTaskID(key + "-task")
	body := fmt.Sprintf(`{
		"title": "R-07 real session manager",
		"goal": "prove the real chain",
		"idempotencyKey": "%s",
		"repositories": [
			{"repoKey": "%s", "projectId": "%s", "baseSha": "%s"},
			{"repoKey": "%s", "projectId": "%s", "baseSha": "%s"}
		],
		"tasks": [
			{"id": "%s", "kind": "development", "title": "work", "agentHarness": "%s"}
		],
		"taskRepositories": [
			{"taskId": "%s", "repoKey": "%s"},
			{"taskId": "%s", "repoKey": "%s"}
		]
	}`, key, m2smRepoA, f.projectA, baseSHAA, m2smRepoB, f.projectB, baseSHAB,
		taskID, m2smDriverHarness,
		taskID, m2smRepoA, taskID, m2smRepoB)

	created := f.do(http.MethodPost, "/workflows", body)
	if created.Status != http.StatusCreated {
		f.t.Fatalf("create workflow: %d %s", created.Status, created.Raw)
	}
	workflowID := domain.WorkflowID(f.text(created.Body, "workflow", "id"))
	revision := f.number(created.Body, "workflow", "revision")

	activated := f.do(http.MethodPost, "/workflows/"+string(workflowID)+"/activate",
		fmt.Sprintf(`{"expectedRevision":%d,"idempotencyKey":"%s-activate"}`, revision, key))
	if activated.Status != http.StatusOK {
		f.t.Fatalf("activate: %d %s", activated.Status, activated.Raw)
	}
	return workflowID, taskID
}

func (f *m2smFixture) claimAndStart(
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
	key string,
) (domain.WorkflowAttemptID, string) {
	f.t.Helper()
	task := f.do(http.MethodGet, "/workflows/"+string(workflowID)+"/tasks/"+string(taskID), "")
	if task.Status != http.StatusOK {
		f.t.Fatalf("read task: %d %s", task.Status, task.Raw)
	}
	revision := f.number(task.Body, "task", "revision")

	claimed := f.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/tasks/"+string(taskID)+"/claim",
		fmt.Sprintf(`{"expectedRevision":%d,"workerId":"w1","idempotencyKey":"%s-claim"}`, revision, key))
	if claimed.Status != http.StatusCreated {
		f.t.Fatalf("claim: %d %s", claimed.Status, claimed.Raw)
	}
	attemptID := domain.WorkflowAttemptID(f.text(claimed.Body, "attempt", "id"))
	lease := f.text(claimed.Body, "leaseToken")

	started := f.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/start",
		fmt.Sprintf(`{"leaseToken":"%s","idempotencyKey":"%s-start"}`, lease, key))
	if started.Status != http.StatusOK {
		f.t.Fatalf("start: %d %s", started.Status, started.Raw)
	}
	return attemptID, lease
}

func (f *m2smFixture) bind(
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	key, prompt string,
) m2smResp {
	f.t.Helper()
	return f.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/bind",
		fmt.Sprintf(`{"harness":"%s","mode":"chat","prompt":%q,"idempotencyKey":"%s-bind"}`,
			m2smDriverHarness, prompt, key))
}

func (f *m2smFixture) binding(attemptID domain.WorkflowAttemptID) domain.WorkflowAttemptBinding {
	f.t.Helper()
	binding, found, err := f.store.GetWorkflowAttemptBinding(context.Background(), attemptID)
	if err != nil || !found {
		f.t.Fatalf("read binding: found=%v err=%v", found, err)
	}
	return binding
}

func (f *m2smFixture) session(id domain.SessionID) domain.SessionRecord {
	f.t.Helper()
	rec, found, err := f.store.GetSession(context.Background(), id)
	if err != nil || !found {
		f.t.Fatalf("read session %s: found=%v err=%v", id, found, err)
	}
	return rec
}

// worktreeListed reports whether `git worktree list` in repo names path.
func worktreeListedIn(t *testing.T, repo, path string) bool {
	t.Helper()
	out := m2smGit(t, repo, "worktree", "list", "--porcelain")
	clean := filepath.Clean(path)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			if filepath.Clean(rest) == clean {
				return true
			}
		}
	}
	return false
}

// dirCount counts entries under root, so "one worktree, not two" is a filesystem fact.
func dirCount(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read %s: %v", root, err)
	}
	return len(entries)
}

func m2smSessionCount(t *testing.T, st *store.Store) int {
	t.Helper()
	rows, err := st.ListAllSessions(context.Background())
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return len(rows)
}

// newFixture opens a fresh store under a fresh data dir.
func newM2SMStandalone(t *testing.T) *m2smFixture {
	t.Helper()
	dataDir := t.TempDir()
	return newM2SMFixture(t, sqlitetest.MustOpenAt(t, dataDir), dataDir, t.TempDir())
}

// ---------------------------------------------------------------------------
// scenario 1: happy path through the REAL session manager
// ---------------------------------------------------------------------------

func TestM2SessionManagerBindSpawnsRealWorktrees(t *testing.T) {
	f := newM2SMStandalone(t)

	workflowID, taskID := f.createWorkflow("m2sm-happy")
	attemptID, _ := f.claimAndStart(workflowID, taskID, "m2sm-happy")
	before := m2smSessionCount(t, f.store)

	bound := f.bind(workflowID, attemptID, "m2sm-happy", "do the work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", bound.Status, bound.Raw)
	}
	if state := f.text(bound.Body, "binding", "state"); state != string(domain.WorkflowBindingStateBoundReady) {
		t.Fatalf("state = %q, want bound_ready; body=%s", state, bound.Raw)
	}

	// The session exists durably, and it is ONE new row.
	if got := m2smSessionCount(t, f.store); got != before+1 {
		t.Fatalf("sessions = %d, want %d (one spawn)", got, before+1)
	}
	binding := f.binding(attemptID)
	if binding.SessionID == nil {
		t.Fatal("the binding names no session")
	}
	sessionID := *binding.SessionID
	rec := f.session(sessionID)

	// A REAL controller generation, not a placeholder.
	if rec.Metadata.ControllerGeneration == "" ||
		strings.HasPrefix(rec.Metadata.ControllerGeneration, "pending:") {
		t.Fatalf("controller generation = %q, want a real one", rec.Metadata.ControllerGeneration)
	}
	if binding.ControllerGeneration != rec.Metadata.ControllerGeneration {
		t.Fatalf("binding generation %q != session generation %q",
			binding.ControllerGeneration, rec.Metadata.ControllerGeneration)
	}

	// Two repositories, two worktrees: on disk, registered by git, and recorded.
	rows, err := f.store.ListSessionWorktrees(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list worktrees: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("worktree rows = %d, want 2 (one per repository)", len(rows))
	}
	byRepo := map[string]domain.SessionWorktreeRecord{}
	for _, row := range rows {
		byRepo[row.RepoName] = row
	}
	for key, repo := range map[string]string{"repo-a": f.repoA, "repo-b": f.repoB} {
		row, ok := byRepo[key]
		if !ok {
			t.Fatalf("no worktree row for %s; rows=%v", key, rows)
		}
		if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
			t.Fatalf("%s worktree does not exist on disk at %q: %v", key, row.WorktreePath, statErr)
		}
		if !worktreeListedIn(t, repo, row.WorktreePath) {
			t.Fatalf("%s is not registered as a worktree of %s", row.WorktreePath, repo)
		}
		if head := strings.TrimSpace(m2smGit(t, row.WorktreePath, "rev-parse", "HEAD")); head == "" {
			t.Fatalf("%s worktree has no HEAD", key)
		}
	}

	// The session row's WorkspacePath IS the root worktree on disk.
	root := byRepo[string(m2smRepoA)].WorktreePath
	if filepath.Clean(rec.Metadata.WorkspacePath) != filepath.Clean(root) {
		t.Fatalf("WorkspacePath = %q, want the repo-a worktree %q", rec.Metadata.WorkspacePath, root)
	}
	if _, err := os.Stat(rec.Metadata.WorkspacePath); err != nil {
		t.Fatalf("WorkspacePath does not exist: %v", err)
	}
}

// TestM2SessionManagerBindHonorsPinnedBaseSHA is the end-to-end form of the
// defect: a workflow that records a HISTORICAL base commit for each repository
// must produce worktrees checked out at exactly those commits, even though the
// repositories' default branches are the tip. It runs the whole chain —
// workflow -> claim -> bind -> LocalGateway -> real SessionManager ->
// gitworktree -> git — so a pin dropped anywhere along it is visible here.
func TestM2SessionManagerBindHonorsPinnedBaseSHA(t *testing.T) {
	f := newM2SMStandalone(t)

	// Two more commits give each repository a distinguishable history. They land
	// BEFORE the workflow is created, so the pins are real ancestors of main.
	for _, repo := range []string{f.repoA, f.repoB} {
		for _, name := range []string{"second", "third"} {
			path := filepath.Join(repo, name+".txt")
			if err := os.WriteFile(path, []byte(name+"\n"), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			m2smGit(t, repo, "add", name+".txt")
			m2smGit(t, repo, "commit", "-m", name)
		}
	}
	pinA := strings.TrimSpace(m2smGit(t, f.repoA, "rev-parse", "HEAD~1"))
	pinB := strings.TrimSpace(m2smGit(t, f.repoB, "rev-parse", "HEAD~2"))
	if pinA == pinB {
		t.Fatal("the fixture produced the same pinned commit for both repositories")
	}
	tips := map[string]string{
		"repo-a": strings.TrimSpace(m2smGit(t, f.repoA, "rev-parse", "HEAD")),
		"repo-b": strings.TrimSpace(m2smGit(t, f.repoB, "rev-parse", "HEAD")),
	}

	workflowID, taskID := f.createWorkflowWithBases("m2sm-pinned", pinA, pinB)
	attemptID, _ := f.claimAndStart(workflowID, taskID, "m2sm-pinned")

	// Main advances AFTER planning: the pin must still win.
	for _, repo := range []string{f.repoA, f.repoB} {
		path := filepath.Join(repo, "after-planning.txt")
		if err := os.WriteFile(path, []byte("moved on\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		m2smGit(t, repo, "add", "after-planning.txt")
		m2smGit(t, repo, "commit", "-m", "main advanced after planning")
	}
	for key, tip := range tips {
		repo := f.repoA
		if key == "repo-b" {
			repo = f.repoB
		}
		if now := strings.TrimSpace(m2smGit(t, repo, "rev-parse", "HEAD")); now == tip {
			t.Fatalf("%s did not advance, so this scenario proves nothing", key)
		}
	}

	bound := f.bind(workflowID, attemptID, "m2sm-pinned", "do the work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", bound.Status, bound.Raw)
	}
	binding := f.binding(attemptID)
	if binding.SessionID == nil {
		t.Fatal("the binding names no session")
	}
	rows, err := f.store.ListSessionWorktrees(context.Background(), *binding.SessionID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("worktree rows = %d err=%v, want 2", len(rows), err)
	}
	byRepo := map[string]domain.SessionWorktreeRecord{}
	for _, row := range rows {
		byRepo[row.RepoName] = row
	}

	for _, tc := range []struct {
		key string
		pin string
	}{{"repo-a", pinA}, {"repo-b", pinB}} {
		row, ok := byRepo[tc.key]
		if !ok {
			t.Fatalf("no %s worktree row", tc.key)
		}
		// The filesystem fact, read straight from git inside the worktree.
		head := strings.TrimSpace(m2smGit(t, row.WorktreePath, "rev-parse", "HEAD"))
		if head != tc.pin {
			t.Fatalf("%s worktree HEAD = %s, want the pinned commit %s (main tip was %s)",
				tc.key, head, tc.pin, tips[tc.key])
		}
		// And the row records the pin as both the base and the creation commit.
		if row.BaseSHA != tc.pin {
			t.Fatalf("%s BaseSHA = %q, want the pin %q", tc.key, row.BaseSHA, tc.pin)
		}
		if row.CreationSHA != tc.pin {
			t.Fatalf("%s CreationSHA = %q, want the pin %q", tc.key, row.CreationSHA, tc.pin)
		}
	}
}

// ---------------------------------------------------------------------------
// scenario 2: duplicate spawn creates nothing
// ---------------------------------------------------------------------------
func TestM2SessionManagerDuplicateBindCreatesOneSession(t *testing.T) {
	f := newM2SMStandalone(t)

	workflowID, taskID := f.createWorkflow("m2sm-dupe")
	attemptID, _ := f.claimAndStart(workflowID, taskID, "m2sm-dupe")

	first := f.bind(workflowID, attemptID, "m2sm-dupe", "do the work")
	if first.Status != http.StatusOK {
		t.Fatalf("first bind: %d %s", first.Status, first.Raw)
	}
	sessionsAfterFirst := m2smSessionCount(t, f.store)
	dirsAfterFirst := dirCount(t, f.managedRoot)
	if dirsAfterFirst == 0 {
		t.Fatal("no worktree directory was created")
	}

	// The SAME spawn key: this is a retry of one request, not a new attempt.
	second := f.bind(workflowID, attemptID, "m2sm-dupe", "do the work")
	if second.Status != http.StatusOK {
		t.Fatalf("second bind: %d %s", second.Status, second.Raw)
	}
	if second.Status == 0 || second.Body == nil {
		t.Fatal("second bind answered nothing")
	}
	if reason := f.text(second.Body, "reasonCode"); reason != "binding_replayed" {
		t.Fatalf("reasonCode = %q, want binding_replayed; body=%s", reason, second.Raw)
	}

	if got := m2smSessionCount(t, f.store); got != sessionsAfterFirst {
		t.Fatalf("sessions = %d after a repeated bind, want %d", got, sessionsAfterFirst)
	}
	if got := dirCount(t, f.managedRoot); got != dirsAfterFirst {
		t.Fatalf("worktree directories = %d after a repeated bind, want %d", got, dirsAfterFirst)
	}
}

// ---------------------------------------------------------------------------
// scenario 3: queue + steer + stop over HTTP against the real session
// ---------------------------------------------------------------------------

func TestM2SessionManagerQueueSteerStopKeepsWorktree(t *testing.T) {
	f := newM2SMStandalone(t)

	workflowID, taskID := f.createWorkflow("m2sm-intervene")
	attemptID, _ := f.claimAndStart(workflowID, taskID, "m2sm-intervene")
	bound := f.bind(workflowID, attemptID, "m2sm-intervene", "start the work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", bound.Status, bound.Raw)
	}
	sessionID := domain.SessionID(f.text(bound.Body, "binding", "sessionId"))

	// The spawn's opening prompt is a real turn, so the controller is BUSY: the
	// scripted driver emits turn.started and never completes.
	if turn, _, ok := f.chat.RunningProviderTurn(context.Background(), sessionID); !ok || turn == "" {
		t.Fatalf("no provider turn is running for %s; the queue path would not be exercised", sessionID)
	}
	runningTurn, _, _ := f.chat.RunningProviderTurn(context.Background(), sessionID)

	queued := f.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		`{"mode":"queue","text":"after this turn, check the tests","clientMessageId":"m2sm-queue-1"}`)
	if queued.Status != http.StatusOK {
		t.Fatalf("queue: %d %s", queued.Status, queued.Raw)
	}
	steered := f.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		fmt.Sprintf(`{"mode":"steer","text":"use the existing helper","targetProviderTurnId":%q,"clientMessageId":"m2sm-steer-1"}`, runningTurn))
	if steered.Status != http.StatusOK {
		t.Fatalf("steer: %d %s", steered.Status, steered.Raw)
	}
	if state := f.text(steered.Body, "intervention", "state"); state != string(domain.WorkflowInterventionStateApplied) {
		t.Fatalf("steer state = %q, want applied; body=%s", state, steered.Raw)
	}

	// A dirty file written before the stop must survive it.
	binding := f.binding(attemptID)
	rows, err := f.store.ListSessionWorktrees(context.Background(), *binding.SessionID)
	if err != nil || len(rows) == 0 {
		t.Fatalf("worktree rows: %v err=%v", len(rows), err)
	}
	dirtyPath := filepath.Join(rows[0].WorktreePath, "UNCOMMITTED.txt")
	if err := os.WriteFile(dirtyPath, []byte("work in progress\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	stopped := f.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		`{"mode":"stop","clientMessageId":"m2sm-stop-1"}`)
	if stopped.Status != http.StatusOK {
		t.Fatalf("stop: %d %s", stopped.Status, stopped.Raw)
	}
	if state := f.text(stopped.Body, "intervention", "state"); state != string(domain.WorkflowInterventionStateStopConfirmed) {
		t.Fatalf("stop state = %q, want stop_confirmed; body=%s", state, stopped.Raw)
	}

	// Three receipts, all durable, all distinct modes.
	stored, err := f.store.ListWorkflowInterventionsForAttempt(context.Background(), workflowID, taskID)
	if err != nil {
		t.Fatalf("list interventions: %v", err)
	}
	modes := map[domain.WorkflowInterventionMode]int{}
	for _, row := range stored {
		modes[row.Mode]++
	}
	if modes[domain.WorkflowInterventionModeQueue] != 1 ||
		modes[domain.WorkflowInterventionModeSteer] != 1 ||
		modes[domain.WorkflowInterventionModeStop] != 1 {
		t.Fatalf("intervention modes = %v, want one queue, one steer, one stop", modes)
	}

	// The worktree is still there, with the uncommitted work untouched.
	if _, err := os.Stat(rows[0].WorktreePath); err != nil {
		t.Fatalf("the worktree disappeared after a stop: %v", err)
	}
	content, err := os.ReadFile(dirtyPath)
	if err != nil {
		t.Fatalf("the dirty file disappeared after a stop: %v", err)
	}
	if string(content) != "work in progress\n" {
		t.Fatalf("dirty file content = %q", content)
	}
}

// ---------------------------------------------------------------------------
// scenario 4: daemon restart re-attaches instead of respawning
// ---------------------------------------------------------------------------

func TestM2SessionManagerRestartReattachesExistingWorktree(t *testing.T) {
	dataDir := t.TempDir()
	managedRoot := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	first := newM2SMFixture(t, st, dataDir, managedRoot)

	workflowID, taskID := first.createWorkflow("m2sm-restart")
	attemptID, _ := first.claimAndStart(workflowID, taskID, "m2sm-restart")
	bound := first.bind(workflowID, attemptID, "m2sm-restart", "do the work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", bound.Status, bound.Raw)
	}
	binding := first.binding(attemptID)
	if binding.SessionID == nil {
		t.Fatal("no session bound")
	}
	sessionID := *binding.SessionID
	rowsBefore, err := st.ListSessionWorktrees(context.Background(), sessionID)
	if err != nil || len(rowsBefore) != 2 {
		t.Fatalf("worktree rows before restart = %d err=%v, want 2", len(rowsBefore), err)
	}
	sessionsBefore := m2smSessionCount(t, st)
	dirsBefore := dirCount(t, managedRoot)

	// The daemon goes away: controllers stop, the HTTP server closes, nothing else.
	first.stopControllers()
	first.closeServer()

	second := newM2SMFixture(t, st, dataDir, managedRoot)

	// 绑定可读；无 controller 的重启观察必须明确未就绪，随后由 Resume 恢复。
	reread := second.do(http.MethodGet,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/binding", "")
	if reread.Status != http.StatusOK {
		t.Fatalf("binding after restart: %d %s", reread.Status, reread.Raw)
	}
	if got := second.text(reread.Body, "binding", "sessionId"); got != string(sessionID) {
		t.Fatalf("session after restart = %q, want %q", got, sessionID)
	}
	reconciled := second.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/reconcile", "")
	if reconciled.Status != http.StatusConflict || second.text(reconciled.Body, "code") != "SESSION_NOT_READY" ||
		second.binding(attemptID).State != domain.WorkflowBindingStateRecoveryUncertain {
		t.Fatalf("重启后的未恢复状态错误: %d %s", reconciled.Status, reconciled.Raw)
	}

	// Resume re-attaches to the SAME worktrees: no new session, no new directory, and
	// the recorded paths are still exactly what is on disk.
	resumed := second.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/resume", "")
	if resumed.Status != http.StatusOK {
		t.Fatalf("resume: %d %s", resumed.Status, resumed.Raw)
	}
	if got := m2smSessionCount(t, st); got != sessionsBefore {
		t.Fatalf("sessions after restart = %d, want %d (resume must not spawn)", got, sessionsBefore)
	}
	if got := dirCount(t, managedRoot); got != dirsBefore {
		t.Fatalf("worktree directories after restart = %d, want %d", got, dirsBefore)
	}
	rowsAfter, err := st.ListSessionWorktrees(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list worktrees after restart: %v", err)
	}
	for _, row := range rowsAfter {
		if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
			t.Fatalf("recorded worktree %q is gone after restart: %v", row.WorktreePath, statErr)
		}
	}
}

// TestM2SessionManagerRestartReportsAMissingWorktree is R-07's negative half: a session
// whose recorded worktree directory was deleted must be reported as a typed uncertainty.
// It must not be silently recreated and must not read as ready.
func TestM2SessionManagerRestartReportsAMissingWorktree(t *testing.T) {
	dataDir := t.TempDir()
	managedRoot := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	f := newM2SMFixture(t, st, dataDir, managedRoot)

	workflowID, taskID := f.createWorkflow("m2sm-missing")
	attemptID, _ := f.claimAndStart(workflowID, taskID, "m2sm-missing")
	bound := f.bind(workflowID, attemptID, "m2sm-missing", "do the work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", bound.Status, bound.Raw)
	}
	binding := f.binding(attemptID)
	rows, err := st.ListSessionWorktrees(context.Background(), *binding.SessionID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("worktree rows = %d err=%v, want 2", len(rows), err)
	}
	// Delete ONE of the two repositories' worktrees: a multi-repo session must refuse to
	// call itself restored while a repository is gone.
	var missing string
	for _, row := range rows {
		if row.RepoName == string(m2smRepoB) {
			missing = row.WorktreePath
		}
	}
	if missing == "" {
		t.Fatal("no repo-b worktree row")
	}
	sessionsBefore := m2smSessionCount(t, st)
	if err := os.RemoveAll(missing); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatalf("worktree still exists after removal: %v", statErr)
	}
	// The session must be terminated for a restart to take the restore path: a live chat
	// session is resumed by relaunch, not by restoring a workspace, and this scenario is
	// about the WORKSPACE fact.
	rec := f.session(*binding.SessionID)
	rec.IsTerminated = true
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("terminate session: %v", err)
	}

	f.stopControllers()
	f.srv.Close()
	second := newM2SMFixture(t, st, dataDir, managedRoot)

	resumed := second.do(http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/resume", "")
	if resumed.Status == http.StatusOK {
		t.Fatalf("a resume over a missing worktree answered 200: %s", resumed.Raw)
	}
	if resumed.Status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", resumed.Status, resumed.Raw)
	}
	code := second.text(resumed.Body, "code")
	if code == "" {
		t.Fatalf("the refusal carries no typed code: %s", resumed.Raw)
	}
	// Not silently recreated.
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatalf("the missing worktree was recreated: %v", statErr)
	}
	// The uncertainty is TYPED: a frozen recovery_uncertain binding with a named failure,
	// not a silent success and not a generic 500.
	bindingAfter := second.binding(attemptID)
	if bindingAfter.State == domain.WorkflowBindingStateRunning ||
		bindingAfter.State == domain.WorkflowBindingStateBoundReady {
		t.Fatalf("binding state = %q after a failed resume; it must not claim a usable session",
			bindingAfter.State)
	}
	if bindingAfter.State != domain.WorkflowBindingStateRecoveryUncertain {
		t.Fatalf("binding state = %q, want recovery_uncertain", bindingAfter.State)
	}
	if bindingAfter.FailureCode == "" {
		t.Fatal("the frozen binding records no failure code")
	}
	// And no session row was created for the attempt.
	if got := m2smSessionCount(t, st); got != sessionsBefore {
		t.Fatalf("sessions = %d after a failed resume, want %d (nothing may be spawned)",
			got, sessionsBefore)
	}
}

// ---------------------------------------------------------------------------
// scenario 5: an abnormal controller exit is reported, not guessed
// ---------------------------------------------------------------------------

func TestM2SessionManagerAbnormalExitIsReported(t *testing.T) {
	f := newM2SMStandalone(t)

	workflowID, taskID := f.createWorkflow("m2sm-exit")
	attemptID, _ := f.claimAndStart(workflowID, taskID, "m2sm-exit")
	bound := f.bind(workflowID, attemptID, "m2sm-exit", "do the work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", bound.Status, bound.Raw)
	}
	sessionID := domain.SessionID(f.text(bound.Body, "binding", "sessionId"))
	if !f.chat.HasLiveChatController(sessionID) {
		t.Fatal("no live controller after a successful bind")
	}

	// The provider stream ends without a completed turn: this is exactly what a crashed
	// or exited provider looks like to the controller.
	conversations := f.driver.Conversations()
	if len(conversations) == 0 {
		t.Fatal("the driver opened no conversation, so this proves nothing")
	}
	if err := conversations[len(conversations)-1].Close(); err != nil {
		t.Fatalf("close conversation: %v", err)
	}

	// The controller reports the exit durably.
	deadline := time.Now().Add(10 * time.Second)
	var rec domain.SessionRecord
	for {
		rec = f.session(sessionID)
		if rec.Activity.State == domain.ActivityExited || rec.IsTerminated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session activity = %q after the provider stream ended, want exited",
				rec.Activity.State)
		}
		time.Sleep(10 * time.Millisecond)
	}

	observed := f.do(http.MethodGet,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/observe", "")
	if observed.Status != http.StatusOK {
		t.Fatalf("observe: %d %s", observed.Status, observed.Raw)
	}
	if readiness := f.text(observed.Body, "observation", "readiness"); readiness != string(ports.SessionReadinessExited) {
		t.Fatalf("readiness = %q, want exited; body=%s", readiness, observed.Raw)
	}
	live, present := f.path(observed.Body, "live").(bool)
	if !present {
		t.Fatalf("live is absent from the observe response: %s", observed.Raw)
	}
	if live {
		t.Fatalf("live = true for a session whose provider exited: %s", observed.Raw)
	}
	if reason := f.text(observed.Body, "reasonCode"); reason == "" {
		t.Fatal("live=false carries no reason code")
	}
	// The binding must not claim a running provider.
	if state := f.binding(attemptID).State; state == domain.WorkflowBindingStateRunning {
		t.Fatalf("binding state = %q after the provider left", state)
	}
}
