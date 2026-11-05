package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/scripted"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// M2 假工作区集成测试使用真实 SQLite、HTTP、LocalGateway 与各协调器。
// 探针、SessionSpawner 和工作区 Observer 是显式测试替身；Spawner 写入真实持久化行，
// Observer 返回假工作区的固定 HEAD，并逐次比对其持有的归属快照与数据库行。
// 固定 HEAD 不代表真实 Git 验证，真实证据由 m2_session_manager 与 m2_f03 测试提供。
// 幂等、身份、绑定状态机、generation、Queue 与协作等业务门禁均使用生产代码。

const (
	m2ProjectID = "m2-project"
	m2RepoKey   = domain.WorkflowRepoKey("repo-main")

	// m2SteerHarness advertises and implements steering.
	m2SteerHarness = domain.HarnessCodex
	// m2QueueHarness does neither, so the degradation path is reachable.
	m2QueueHarness = domain.HarnessClaudeCode
)

// ---------------------------------------------------------------------------
// 显式测试边界
// ---------------------------------------------------------------------------

// fakeSessionSpawner creates real session rows in the real store.
//
// It records every call so a test can prove a spawn happened exactly once (or never), and it
// keeps the client-request key on the row so the gateway's own idempotency lookup finds it —
// which is what makes Reconcile able to adopt instead of re-spawn.
type fakeSessionSpawner struct {
	store *store.Store
	// chats starts the new session's Chat controller, which is what a real spawn does: by
	// the time the gateway observes the session, a controller exists and the observation
	// carries the real controller generation. Without that ordering a bind would record a
	// placeholder generation and every later fence would fail for the wrong reason.
	chats *chatsvc.Service
	// workspace is the directory the created session row names. It exists so the row is
	// self-consistent; no Git operation ever runs against it.
	workspace string
	spawns    atomic.Int32
	// ownedWorktrees 固定假工作区归属；只保护快照，不创建假文件系统。
	worktreesMu    sync.Mutex
	ownedWorktrees map[domain.SessionID][]domain.SessionWorktreeRecord
	// failNext, when set, fails the next Spawn without creating a row, so a
	// spawn_unknown path can be exercised honestly (the caller gets an error after
	// the gateway already committed to asking).
	failNext atomic.Bool
}

func (s *fakeSessionSpawner) Spawn(
	ctx context.Context,
	cfg ports.SpawnConfig,
) (domain.SessionRecord, int, int, error) {
	s.spawns.Add(1)
	// loseAnswer models the one outcome that must never be read as "nothing happened": the
	// session row IS created, and the caller learns nothing. Everything below still runs, so
	// the row exists for Reconcile to adopt.
	loseAnswer := s.failNext.Swap(false)
	now := time.Now().UTC().Truncate(time.Second)
	// A real spawner would create a worktree; this one names a directory that already
	// exists, because the assertions here are about durable rows rather than about Git.
	rec, err := s.store.CreateSession(ctx, domain.SessionRecord{
		ProjectID:         cfg.ProjectID,
		Kind:              cfg.Kind,
		Harness:           cfg.Harness,
		Mode:              domain.SessionModeChat,
		Metadata:          domain.SessionMetadata{WorkspacePath: s.workspace},
		Activity:          domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		ClientRequestID:   cfg.ClientRequestID,
		ClientRequestHash: cfg.ClientRequestHash,
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	if err != nil {
		return domain.SessionRecord{}, 0, 0, err
	}
	// A real spawn records one session_worktrees row per repository it materialised,
	// and the observation those rows produce is what the coordinator's repo gate reads.
	// Writing them here is the fake's job: without them a bound attempt would claim a
	// session whose worktrees nobody created, which is exactly what the gate refuses.
	if err := s.recordWorktrees(ctx, rec, cfg); err != nil {
		return domain.SessionRecord{}, 0, 0, err
	}
	if loseAnswer {
		// The row exists and the answer is lost. The gateway classifies this as
		// ErrSpawnOutcomeUnknown, so the binding lands at spawn_unknown — exactly the state
		// Reconcile exists for.
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("%w: injected lost answer",
			ports.ErrSpawnOutcomeUnknown)
	}
	// Start the controller before returning, so the gateway's observation sees a live
	// session rather than a row that merely exists.
	if s.chats != nil {
		if _, startErr := s.chats.Start(ctx, chatsvc.StartConfig{
			SessionID:     rec.ID,
			ProjectID:     rec.ProjectID,
			Harness:       rec.Harness,
			WorkspacePath: s.workspace,
		}); startErr != nil {
			return domain.SessionRecord{}, 0, 0, startErr
		}
	}
	return rec, 0, 0, nil
}

// m2SpawnCount reports how many spawns the fake has served.
func (s *fakeSessionSpawner) m2SpawnCount() int { return int(s.spawns.Load()) }

// scriptedReadiness is the install/auth answer the real ChatDriverProbe reads.
//
// It is not a provider call: it is what the daemon's readiness layer would have cached for an
// installed, signed-in harness.
type scriptedReadiness struct{}

func (scriptedReadiness) EnsureAgentReadiness(
	_ context.Context,
	agentID string,
	_ domain.AgentReadinessPurpose,
) (domain.AgentReadinessSnapshot, error) {
	now := time.Now().UTC()
	return domain.AgentReadinessSnapshot{
		ID:           agentID,
		Label:        agentID,
		Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled, CheckedAt: &now},
		Authentication: domain.AgentAuthenticationObservation{
			State: domain.AgentAuthenticationAuthorized, CheckedAt: &now,
		},
		EffectiveReadiness: domain.AgentReadinessReady,
	}, nil
}

func (scriptedReadiness) InvalidateAgentInstallation(string)   {}
func (scriptedReadiness) InvalidateAgentAuthentication(string) {}
func (scriptedReadiness) RecheckAgent(string)                  {}

// chatControllerProbe is the SessionProber the real ChatDriverProbe uses for session-scoped
// requests. It reads the live Chat service, exactly as the daemon's own adapter does.
type chatControllerProbe struct {
	svc *chatsvc.Service
}

func (p chatControllerProbe) SessionGeneration(sessionID domain.SessionID) (string, bool) {
	controller, err := p.svc.Controller(sessionID)
	if err != nil || controller == nil || controller.State() == ports.ChatControllerStopped {
		return "", false
	}
	return controller.Generation(), true
}

func (p chatControllerProbe) ChatCapabilities(sessionID domain.SessionID) (ports.ChatCapabilities, bool) {
	controller, err := p.svc.Controller(sessionID)
	if err != nil || controller == nil || controller.State() == ports.ChatControllerStopped {
		return nil, false
	}
	return controller.Capabilities(), true
}

// fakeOwnership adapts execution.ResolveAttemptOwnership to the controller's resolver, the same
// way the daemon's ownershipAdapter does. It lives here rather than in the daemon because the
// daemon is another workstream's file; the production function it calls is the real one.
type fakeOwnership struct {
	store *store.Store
}

func (o fakeOwnership) ResolveAttemptOwnership(
	ctx context.Context,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
) (controllers.AttemptOwnership, error) {
	ownership, err := execution.ResolveAttemptOwnership(ctx, o.store, workflowID, attemptID)
	if err != nil {
		return controllers.AttemptOwnership{}, err
	}
	return controllers.AttemptOwnership{
		Attempt:   ownership.Attempt,
		Task:      ownership.Task,
		Repos:     ownership.Repos,
		ProjectID: ownership.ProjectID,
		Harness:   ownership.Harness,
	}, nil
}

// ---------------------------------------------------------------------------
// the fixture
// ---------------------------------------------------------------------------

type m2Env struct {
	store    *store.Store
	recorder *capability.Recorder
	resolver *capability.Resolver
	chat     *chatsvc.Service
	binder   *execution.Coordinator
	interv   *intervention.Coordinator
	collab   *collaboration.Coordinator
	workflow *workflow.Service
	spawner  *fakeSessionSpawner
	drivers  map[domain.AgentHarness]*scripted.Driver
	srv      *httptest.Server
	// baseURL is srv.URL plus the API prefix.
	baseURL string
	// sessionIDs is populated as binds happen so a scenario can act on the right session.
	sessionIDs sync.Map // domain.WorkflowAttemptID -> domain.SessionID
}

// newM2Env builds the whole stack over a store the caller supplies.
//
// Passing the store in is what makes the restart scenario honest: the second fixture gets the
// SAME durable database and nothing else from the first.
func newM2Env(t *testing.T, st *store.Store) *m2Env {
	t.Helper()
	clock := func() time.Time { return time.Now().UTC() }
	workspace := t.TempDir()

	steeringDriver := scripted.NewDriver(scripted.Config{
		Harness: m2SteerHarness,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
			ports.ChatCapabilitySteer:     true,
		},
		Steerable: true,
	})
	queueDriver := scripted.NewDriver(scripted.Config{Harness: m2QueueHarness})
	registry := scripted.Registry(steeringDriver, queueDriver)

	recorder := capability.NewRecorder(st)
	resolver := capability.NewResolver(capability.WithRecorder(recorder), capability.WithClock(clock))

	// The REAL probe, over scripted drivers and the real Chat service.
	probe := capability.NewChatDriverProbe(
		nil, registry, scriptedReadiness{}, clock, capability.DefaultTTL,
	)

	chatSvc := chatsvc.New(chatsvc.Options{
		Store:              st,
		Reader:             m2SnapshotReader(st),
		PageReader:         m2PageReader(st),
		Sessions:           st,
		Drivers:            registry,
		Activity:           m2ActivityRecorder{},
		Log:                slog.New(slog.DiscardHandler),
		CapabilityEvidence: recorder,
		NewID:              m2IDFactory("chat"),
		Now:                clock,
	})
	t.Cleanup(func() { chatSvc.StopAll(context.Background()) })

	// The probe reads the live controller for session-scoped requests.
	sessionProbe := chatControllerProbe{svc: chatSvc}
	probe = capability.NewChatDriverProbe(
		sessionProbe, registry, scriptedReadiness{}, clock, capability.DefaultTTL,
	)
	resolver.RegisterProbe(m2SteerHarness, probe)
	resolver.RegisterProbe(m2QueueHarness, probe)

	spawner := &fakeSessionSpawner{
		store: st, chats: chatSvc, workspace: workspace,
		ownedWorktrees: make(map[domain.SessionID][]domain.SessionWorktreeRecord),
	}
	if err := spawner.restoreFakeWorktrees(context.Background()); err != nil {
		t.Fatalf("恢复假工作区观察边界: %v", err)
	}
	gateway := execution.NewLocalGateway(execution.LocalGatewayDeps{
		Sessions:  spawner,
		Records:   st,
		Commits:   st,
		Worktrees: st,
		// Turns is what lets a steer receipt name AO's own turn row as well as the
		// provider's. Without it the applied state is unprovable, so the daemon wires the
		// store here and so does this fixture.
		Turns:        st,
		Chats:        chatSvc,
		Controllers:  chatSvc,
		Capabilities: recorder,
		Clock:        clock,
		Log:          slog.New(slog.DiscardHandler),
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
	// Graph limits and lease duration are deployment settings, not hidden defaults: the
	// service refuses to run without them, which is why the production wiring sets them
	// explicitly. These mirror internal/daemon/workflow_wiring.go.
	workflows := workflow.New(workflow.Deps{
		Store: st,
		Clock: clock,
		GraphLimits: domain.WorkflowGraphLimits{
			MaxTasks: 50, MaxDepth: 12, MaxFanOut: 8,
		},
		LeaseDuration: 15 * time.Minute,
	})

	// The real identity resolution: a worker proves its session capability AND its attempt
	// lease, both checked against durable rows, and the operator proves itself with the operator
	// credential (a loopback peer alone is not the operator, which is the R-04 rule). A resolver
	// wired without the agent proofs refuses every agent request, which is the correct default but
	// would make the collaboration scenario prove nothing.
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
		WorkflowOwnership:     fakeOwnership{store: st},
		WorkflowPrincipals:    principals,
		OperatorApprovals:     approvals,
		OperatorConsent:       &m2Consent{},
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	return &m2Env{
		store: st, recorder: recorder, resolver: resolver, chat: chatSvc,
		binder: binder, interv: interventions, collab: collab, workflow: workflows,
		spawner: spawner,
		drivers: map[domain.AgentHarness]*scripted.Driver{
			m2SteerHarness: steeringDriver, m2QueueHarness: queueDriver,
		},
		srv:     srv,
		baseURL: srv.URL + "/api/v1",
	}
}

func m2IDFactory(prefix string) chatsvc.IDFactory {
	var (
		mu      sync.Mutex
		counter int
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		counter++
		return fmt.Sprintf("m2-%s-%04d", prefix, counter)
	}
}

func m2SnapshotReader(st *store.Store) chatsvc.SnapshotReader {
	return chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshot(ctx, conversationID)
		if err != nil {
			return chatsvc.ConversationRows{}, err
		}
		return chatsvc.ConversationRows{
			Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns,
			Messages: rows.Messages, Activities: rows.Activities,
			BranchPoints: rows.BranchPoints, BranchedFromEarlierMessage: rows.BranchedFromEarlierMessage,
		}, nil
	})
}

func m2PageReader(st *store.Store) chatsvc.SnapshotPageReader {
	return chatsvc.SnapshotPageReaderFunc(func(ctx context.Context, id string, before, limit int64) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshotPage(ctx, id, before, limit)
		return chatsvc.ConversationRows{
			Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch,
			Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities,
		}, err
	})
}

type m2ActivityRecorder struct{}

func (m2ActivityRecorder) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	return nil
}

// openM2Store opens a fully migrated store through the same public path the daemon's own tests
// use, so the migrations asserted here are the ones a real install applies.
func openM2Store(t *testing.T) *store.Store {
	t.Helper()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: m2ProjectID, Path: t.TempDir(),
		RegisteredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return st
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// m2Response is one wire answer, already decoded.
type m2Response struct {
	Status int
	Body   map[string]any
	Raw    string
}

// do 对审批闭集中的 HTTP 操作逐次申请原生确认，其他请求不添加身份。
func (e *m2Env) do(t *testing.T, method, path, body string) m2Response {
	t.Helper()
	return e.doWithHeaders(t, method, path, body, m2ApprovedHeaders(t, e.baseURL, method, path, body))
}

func (e *m2Env) doWithHeaders(
	t *testing.T,
	method, path, body string,
	headers map[string]string,
) m2Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.baseURL+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	decoded := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = decoder.Decode(&decoded)
	}
	return m2Response{Status: resp.StatusCode, Body: decoded, Raw: string(raw)}
}

// dig walks a decoded body. A missing path yields nil, which lets an assertion say "absent"
// rather than panicking.
func dig(body map[string]any, path ...string) any {
	var current any = body
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

func str(value any) string {
	text, _ := value.(string)
	return text
}

func boolean(value any) bool {
	flag, _ := value.(bool)
	return flag
}

// ---------------------------------------------------------------------------
// scenario setup helpers
// ---------------------------------------------------------------------------

// createWorkflow builds and activates a workflow with one task per harness, over HTTP, and
// returns the workflow id, both task ids, and their revisions.
func (e *m2Env) createWorkflow(t *testing.T, key string) (domain.WorkflowID, map[domain.AgentHarness]domain.WorkflowTaskID) {
	t.Helper()
	// Task ids are globally unique in the schema (workflow_tasks.id is the primary key), so
	// they are namespaced by the workflow's own key. Reusing "task-steer" across two
	// workflows would fail the second create for a reason having nothing to do with M2.
	steerTask := domain.WorkflowTaskID(key + "-task-steer")
	queueTask := domain.WorkflowTaskID(key + "-task-queue")
	body := fmt.Sprintf(`{
		"title": "M2 acceptance",
		"goal": "prove the multi-agent path",
		"idempotencyKey": "%s",
		"repositories": [{"repoKey": "%s", "projectId": "%s"}],
		"tasks": [
			{"id": "%s", "kind": "development", "title": "steerable work", "agentHarness": "%s"},
			{"id": "%s", "kind": "development", "title": "queue-only work", "agentHarness": "%s"}
		],
		"taskRepositories": [
			{"taskId": "%s", "repoKey": "%s"},
			{"taskId": "%s", "repoKey": "%s"}
		]
	}`, key, m2RepoKey, m2ProjectID,
		steerTask, m2SteerHarness, queueTask, m2QueueHarness,
		steerTask, m2RepoKey, queueTask, m2RepoKey)

	created := e.do(t, http.MethodPost, "/workflows", body)
	if created.Status != http.StatusCreated {
		t.Fatalf("create workflow: status=%d body=%s", created.Status, created.Raw)
	}
	workflowID := domain.WorkflowID(str(dig(created.Body, "workflow", "id")))
	if workflowID == "" {
		t.Fatalf("create workflow returned no id: %s", created.Raw)
	}
	revision, _ := dig(created.Body, "workflow", "revision").(json.Number).Int64()

	activated := e.do(t, http.MethodPost, "/workflows/"+string(workflowID)+"/activate",
		fmt.Sprintf(`{"expectedRevision":%d,"idempotencyKey":"%s-activate"}`, revision, key))
	if activated.Status != http.StatusOK {
		t.Fatalf("activate workflow: status=%d body=%s", activated.Status, activated.Raw)
	}
	return workflowID, map[domain.AgentHarness]domain.WorkflowTaskID{
		m2SteerHarness: steerTask,
		m2QueueHarness: queueTask,
	}
}

// claimAndStart claims one task and starts its attempt, returning the attempt id and the lease.
func (e *m2Env) claimAndStart(
	t *testing.T,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
	key string,
) (domain.WorkflowAttemptID, string) {
	t.Helper()
	task := e.do(t, http.MethodGet, "/workflows/"+string(workflowID)+"/tasks/"+string(taskID), "")
	if task.Status != http.StatusOK {
		t.Fatalf("read task %s: status=%d body=%s", taskID, task.Status, task.Raw)
	}
	taskRevision, _ := dig(task.Body, "task", "revision").(json.Number).Int64()

	claimed := e.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/tasks/"+string(taskID)+"/claim",
		fmt.Sprintf(`{"expectedRevision":%d,"workerId":"worker-1","idempotencyKey":"%s-claim"}`,
			taskRevision, key))
	if claimed.Status != http.StatusCreated {
		t.Fatalf("claim %s: status=%d body=%s", taskID, claimed.Status, claimed.Raw)
	}
	attemptID := domain.WorkflowAttemptID(str(dig(claimed.Body, "attempt", "id")))
	lease := str(dig(claimed.Body, "leaseToken"))
	if attemptID == "" || lease == "" {
		t.Fatalf("claim returned no attempt/lease: %s", claimed.Raw)
	}

	started := e.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/start",
		fmt.Sprintf(`{"leaseToken":"%s","idempotencyKey":"%s-start"}`, lease, key))
	if started.Status != http.StatusOK {
		t.Fatalf("start %s: status=%d body=%s", attemptID, started.Status, started.Raw)
	}
	return attemptID, lease
}

// bindAttempt binds one attempt over HTTP and returns the response.
func (e *m2Env) bindAttempt(
	t *testing.T,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	harness domain.AgentHarness,
	key string,
	prompt string,
) m2Response {
	t.Helper()
	body := fmt.Sprintf(
		`{"harness":"%s","mode":"chat","prompt":%q,"idempotencyKey":"%s-bind"}`,
		harness, prompt, key)
	return e.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/bind", body)
}

// bindAndStartTurn binds an attempt and puts one turn in flight on its session, so steering has
// something to steer. It returns the response, the session id, and the live provider turn.
func (e *m2Env) bindAndStartTurn(
	t *testing.T,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	harness domain.AgentHarness,
	key string,
) (m2Response, domain.SessionID, string) {
	t.Helper()
	bound := e.bindAttempt(t, workflowID, attemptID, harness, key, "start the work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind %s: status=%d body=%s", harness, bound.Status, bound.Raw)
	}
	sessionID := domain.SessionID(str(dig(bound.Body, "binding", "sessionId")))
	if sessionID == "" {
		t.Fatalf("bind returned no session id: %s", bound.Raw)
	}
	e.sessionIDs.Store(attemptID, sessionID)

	// The spawner already started this session's controller, so the turn below dispatches
	// against the generation the bind recorded.
	if _, err := e.chat.Send(context.Background(), sessionID, ports.ChatUserMessage{
		Text: "begin", ClientMessageID: key + "-turn-1", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("start turn on %s: %v", sessionID, err)
	}
	turn := m2AwaitTurn(t, e.chat, sessionID)
	return bound, sessionID, turn
}

// m2AwaitTurn waits for the dispatched provider turn to become visible.
func m2AwaitTurn(t *testing.T, svc *chatsvc.Service, id domain.SessionID) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if turn, _, ok := svc.RunningProviderTurn(context.Background(), id); ok {
			return turn
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no provider turn became visible for %s", id)
	return ""
}

// stopAllChat stops every controller the fixture started.
func (e *m2Env) stopAllChat(t *testing.T) {
	t.Helper()
	e.chat.StopAll(context.Background())
}

// bindState reads one attempt's durable binding state straight from the store.
func (e *m2Env) bindState(t *testing.T, attemptID domain.WorkflowAttemptID) domain.WorkflowAttemptBinding {
	t.Helper()
	binding, found, err := e.store.GetWorkflowAttemptBinding(context.Background(), attemptID)
	if err != nil || !found {
		t.Fatalf("read binding %s: found=%v err=%v", attemptID, found, err)
	}
	return binding
}

// ---------------------------------------------------------------------------
// Scenario 1: create + activate + claim + start
// ---------------------------------------------------------------------------

// TestM2CreateActivateClaimStart covers acceptance step 1 over the wire: a workflow is
// defined, activated, its task claimed by a worker (which mints a one-time lease), and the
// attempt started. Nothing here starts an agent.
func TestM2CreateActivateClaimStart(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-1")
	attemptID, lease := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-1")

	if attemptID == "" {
		t.Fatal("claim produced no attempt id")
	}
	// The lease is the proof the worker uses to speak as this attempt; it must be a real
	// secret, not an echo of the attempt id.
	if lease == "" || strings.Contains(lease, string(attemptID)) {
		t.Fatalf("lease %q looks like a fabricated token", lease)
	}

	attempt, found, err := env.store.GetWorkflowAttempt(context.Background(), attemptID)
	if err != nil || !found {
		t.Fatalf("read attempt: found=%v err=%v", found, err)
	}
	if attempt.State != domain.WorkflowAttemptStateRunning {
		t.Errorf("attempt state = %q, want running after start", attempt.State)
	}
	if attempt.WorkflowID != workflowID {
		t.Errorf("attempt workflow = %q, want %q", attempt.WorkflowID, workflowID)
	}
	// Starting must not have created a session: that is the bind route's job, and a
	// start that spawned would put work in flight before capability evidence was asked.
	if got := env.spawner.m2SpawnCount(); got != 0 {
		t.Fatalf("spawns after start = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: bind over HTTP
// ---------------------------------------------------------------------------

// TestM2BindIsDurableAndSpawnsOnce covers acceptance step 2: the bind route answers 200,
// reports spawned=true, lands the binding in a session-bearing state, and creates a session
// row in the right project with the task's repositories.
func TestM2BindIsDurableAndSpawnsOnce(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-2")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-2")

	bound := env.bindAttempt(t, workflowID, attemptID, m2SteerHarness, "scenario-2", "do the work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: status=%d body=%s", bound.Status, bound.Raw)
	}
	if !boolean(dig(bound.Body, "spawned")) {
		t.Errorf("spawned = false, want true for a first bind; body=%s", bound.Raw)
	}
	if boolean(dig(bound.Body, "frozen")) {
		t.Errorf("frozen = true on a successful first bind; body=%s", bound.Raw)
	}

	state := domain.WorkflowBindingState(str(dig(bound.Body, "binding", "state")))
	if state != domain.WorkflowBindingStateBoundReady &&
		state != domain.WorkflowBindingStateSessionProvisioning {
		t.Fatalf("binding state = %q, want bound_ready or session_provisioning", state)
	}
	sessionID := domain.SessionID(str(dig(bound.Body, "binding", "sessionId")))
	if sessionID == "" {
		t.Fatalf("binding has no session id: %s", bound.Raw)
	}

	// The durable row must agree with the wire.
	persisted := env.bindState(t, attemptID)
	if persisted.State != state {
		t.Errorf("stored state = %q, want %q", persisted.State, state)
	}
	if persisted.SessionID == nil || *persisted.SessionID != sessionID {
		t.Errorf("stored session = %v, want %q", persisted.SessionID, sessionID)
	}

	// And the session row is real, with the right project and repos.
	record, found, err := env.store.GetSession(context.Background(), sessionID)
	if err != nil || !found {
		t.Fatalf("read session %s: found=%v err=%v", sessionID, found, err)
	}
	if record.ProjectID != m2ProjectID {
		t.Errorf("session project = %q, want %q", record.ProjectID, m2ProjectID)
	}
	repos := dig(bound.Body, "binding", "repositories")
	list, _ := repos.([]any)
	if len(list) == 0 {
		t.Fatalf("binding reports no repositories: %s", bound.Raw)
	}
	if got := env.spawner.m2SpawnCount(); got != 1 {
		t.Fatalf("spawns = %d, want exactly 1", got)
	}

	// A replayed bind must not spawn again. That is the whole retry rule.
	replayed := env.bindAttempt(t, workflowID, attemptID, m2SteerHarness, "scenario-2", "do the work")
	if replayed.Status != http.StatusOK {
		t.Fatalf("replayed bind: status=%d body=%s", replayed.Status, replayed.Raw)
	}
	if got := env.spawner.m2SpawnCount(); got != 1 {
		t.Fatalf("spawns after replay = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: two harnesses, two sessions
// ---------------------------------------------------------------------------

// TestM2TwoHarnessesBindTwoSessions covers acceptance step 3: a second task on the other
// harness gets its own attempt, its own bind, and its own session — with a distinct harness.
func TestM2TwoHarnessesBindTwoSessions(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-3")
	steerAttempt, _ := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-3-steer")
	queueAttempt, _ := env.claimAndStart(t, workflowID, tasks[m2QueueHarness], "scenario-3-queue")

	steerBound := env.bindAttempt(t, workflowID, steerAttempt, m2SteerHarness, "scenario-3-steer", "work a")
	queueBound := env.bindAttempt(t, workflowID, queueAttempt, m2QueueHarness, "scenario-3-queue", "work b")
	for _, bound := range []m2Response{steerBound, queueBound} {
		if bound.Status != http.StatusOK {
			t.Fatalf("bind: status=%d body=%s", bound.Status, bound.Raw)
		}
	}

	steerSession := domain.SessionID(str(dig(steerBound.Body, "binding", "sessionId")))
	queueSession := domain.SessionID(str(dig(queueBound.Body, "binding", "sessionId")))
	if steerSession == "" || queueSession == "" {
		t.Fatalf("sessions = %q/%q, want two distinct ids", steerSession, queueSession)
	}
	if steerSession == queueSession {
		t.Fatal("both attempts bound to the same session")
	}

	ctx := context.Background()
	steerRecord, _, err := env.store.GetSession(ctx, steerSession)
	if err != nil {
		t.Fatalf("read steer session: %v", err)
	}
	queueRecord, _, err := env.store.GetSession(ctx, queueSession)
	if err != nil {
		t.Fatalf("read queue session: %v", err)
	}
	if steerRecord.Harness != m2SteerHarness {
		t.Errorf("steer session harness = %q, want %q", steerRecord.Harness, m2SteerHarness)
	}
	if queueRecord.Harness != m2QueueHarness {
		t.Errorf("queue session harness = %q, want %q", queueRecord.Harness, m2QueueHarness)
	}
	if got := env.spawner.m2SpawnCount(); got != 2 {
		t.Fatalf("spawns = %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: a normal message over the interventions route
// ---------------------------------------------------------------------------

// TestM2QueueDeliversANormalMessage covers acceptance step 4.
//
// The daemon exposes exactly one HTTP path for putting a message on a workflow attempt:
// POST /workflows/{id}/attempts/{aid}/interventions with mode=queue. There is no separate
// "send" route on the M2 surface, so this test drives the queue mode and reports that as the
// answer to "which endpoint does the daemon actually expose" — it is the interventions route,
// and mode=queue is the send path. execution.Coordinator.SendMessage exists but is not routed.
func TestM2QueueDeliversANormalMessage(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-4")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2QueueHarness], "scenario-4")
	_, sessionID, _ := env.bindAndStartTurn(t, workflowID, attemptID, m2QueueHarness, "scenario-4")

	queued := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		`{"mode":"queue","text":"a normal follow-up","clientMessageId":"scenario-4-msg-1"}`)
	if queued.Status != http.StatusOK {
		t.Fatalf("queue: status=%d body=%s", queued.Status, queued.Raw)
	}
	state := str(dig(queued.Body, "intervention", "state"))
	// The controller is busy with the turn started above, so the message lands as a durable
	// queued row. Queued is never rendered as sent, which is why it is a distinct state.
	if state != string(domain.WorkflowInterventionStateQueued) &&
		state != string(domain.WorkflowInterventionStateApplied) &&
		state != string(domain.WorkflowInterventionStateDelivered) {
		t.Fatalf("state = %q, want queued/delivered/applied; body=%s", state, queued.Raw)
	}
	if !boolean(dig(queued.Body, "performed")) {
		t.Error("performed = false on a first delivery")
	}
	t.Logf("queue receipt: status=%d state=%q", queued.Status, state)
	if got := str(dig(queued.Body, "intervention", "sessionId")); got != string(sessionID) {
		t.Errorf("receipt session = %q, want %q", got, sessionID)
	}

	// The durable row is what a client replays against.
	row, found, err := env.store.GetWorkflowInterventionByClientMessage(
		context.Background(), workflowID, "scenario-4-msg-1")
	if err != nil || !found {
		t.Fatalf("read intervention: found=%v err=%v", found, err)
	}
	if row.State != domain.WorkflowInterventionState(state) {
		t.Errorf("stored state = %q, want %q", row.State, state)
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: steer the steerable harness
// ---------------------------------------------------------------------------

// TestM2SteerOnTheSteerableHarness covers acceptance step 5, to the real contract.
//
// A steer against a live provider turn answers 200 with state `applied`, the durable row carries
// BOTH turn ids (AO's own row and the provider's), and the provider was asked exactly once — for
// exactly the turn the caller named. Both ids are required rather than preferred: `applied` is
// only provable when the receipt names the durable row that absorbed the guidance as well as the
// provider turn, and a row with one of them is a claim the timeline cannot explain.
//
// The turn id comes from chat.Service.RunningProviderTurn — the accessor a production gateway
// pins with — not from this test's own bookkeeping.
func TestM2SteerOnTheSteerableHarness(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-5")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-5")
	_, sessionID, turn := env.bindAndStartTurn(t, workflowID, attemptID, m2SteerHarness, "scenario-5")

	generation := m2BindingGeneration(t, env, attemptID)
	steered := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		fmt.Sprintf(`{"mode":"steer","text":"narrow it","clientMessageId":"scenario-5-steer-1",
			"targetProviderTurnId":"%s","harness":"%s"}`, turn, m2SteerHarness))
	if steered.Status != http.StatusOK {
		t.Fatalf("steer: status=%d, want 200; body=%s", steered.Status, steered.Raw)
	}
	if state := str(dig(steered.Body, "intervention", "state")); state != "applied" {
		t.Fatalf("state = %q, want applied; body=%s", state, steered.Raw)
	}
	if !boolean(dig(steered.Body, "performed")) {
		t.Error("performed = false on a first steer")
	}

	row, found, err := env.store.GetWorkflowInterventionByClientMessage(
		context.Background(), workflowID, "scenario-5-steer-1")
	if err != nil || !found {
		t.Fatalf("read intervention: found=%v err=%v", found, err)
	}
	if row.State != domain.WorkflowInterventionStateApplied {
		t.Errorf("stored state = %q, want applied", row.State)
	}
	// Both ids, on the durable row. This is what `applied` is licensed by.
	if row.ProviderTurnID != turn {
		t.Errorf("stored provider turn = %q, want %q", row.ProviderTurnID, turn)
	}
	if row.ChatTurnID == "" {
		t.Error("stored chat turn id is empty; an applied steer must name AO's own turn row")
	}
	if row.TargetProviderTurnID != turn {
		t.Errorf("stored target turn = %q, want %q", row.TargetProviderTurnID, turn)
	}
	if row.ControllerGeneration != generation {
		t.Errorf("stored generation = %q, want %q", row.ControllerGeneration, generation)
	}
	if row.SessionID == nil || *row.SessionID != sessionID {
		t.Errorf("stored session = %v, want %q", row.SessionID, sessionID)
	}

	// The named chat turn must be a real row on that conversation, not a plausible string.
	conversation, err := env.store.ConversationForSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	chatTurn, err := env.store.TurnByID(context.Background(), row.ChatTurnID)
	if err != nil {
		t.Fatalf("the recorded chat turn %q does not exist: %v", row.ChatTurnID, err)
	}
	if chatTurn.ConversationID != conversation.ID {
		t.Errorf("recorded chat turn belongs to conversation %q, want %q",
			chatTurn.ConversationID, conversation.ID)
	}
	if chatTurn.ProviderTurnID != turn {
		t.Errorf("recorded chat turn's provider id = %q, want %q", chatTurn.ProviderTurnID, turn)
	}

	if got := env.drivers[m2SteerHarness].SteerCalls(); got != 1 {
		t.Fatalf("provider steers = %d, want exactly 1", got)
	}
}

// m2BindingGeneration reads the recorded controller generation for one attempt.
func m2BindingGeneration(t *testing.T, env *m2Env, attemptID domain.WorkflowAttemptID) string {
	t.Helper()
	binding, found, err := env.store.GetWorkflowAttemptBinding(context.Background(), attemptID)
	if err != nil || !found {
		t.Fatalf("read binding %s: found=%v err=%v", attemptID, found, err)
	}
	if binding.ControllerGeneration == "" {
		t.Fatal("binding has no controller generation")
	}
	return binding.ControllerGeneration
}

// ---------------------------------------------------------------------------
// Scenario 6: the non-steerable harness refuses, then queues
// ---------------------------------------------------------------------------

// TestM2SteerRefusedThenQueueableOnTheQueueOnlyHarness covers acceptance step 6.
//
// The refusal must be STEER_UNSUPPORTED with queueAvailable, and queueing afterwards must
// produce a real queued receipt. Nothing about the refusal may read as a delivery.
func TestM2SteerRefusedThenQueueableOnTheQueueOnlyHarness(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-6")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2QueueHarness], "scenario-6")
	_, _, turn := env.bindAndStartTurn(t, workflowID, attemptID, m2QueueHarness, "scenario-6")

	refused := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		fmt.Sprintf(`{"mode":"steer","text":"this provider cannot take it",
			"clientMessageId":"scenario-6-steer-1","targetProviderTurnId":"%s","harness":"%s"}`,
			turn, m2QueueHarness))
	if refused.Status != http.StatusConflict {
		t.Fatalf("steer on a queue-only harness: status=%d, want 409; body=%s",
			refused.Status, refused.Raw)
	}
	// The stable code must tell a client "this provider cannot do it" so it can offer the queue.
	// A handshake that simply did not advertise steer is exactly that fact, and it is asserted
	// exactly: accepting "some 409" would let the mapping drift back to CAPABILITY_UNKNOWN, which
	// reads to a client as a transient failure it should retry.
	if code := str(dig(refused.Body, "code")); code != "STEER_UNSUPPORTED" {
		t.Fatalf("code = %q, want STEER_UNSUPPORTED; body=%s", code, refused.Raw)
	}
	if !boolean(dig(refused.Body, "details", "queueAvailable")) {
		t.Errorf("queueAvailable = false, but queueing is the honest alternative; body=%s",
			refused.Raw)
	}
	// The refusal must leave no applied receipt and no provider call.
	if got := env.drivers[m2QueueHarness].SteerCalls(); got != 0 {
		t.Fatalf("provider steers = %d, want 0", got)
	}

	queued := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		`{"mode":"queue","text":"this one works","clientMessageId":"scenario-6-queue-1"}`)
	if queued.Status != http.StatusOK {
		t.Fatalf("queue: status=%d body=%s", queued.Status, queued.Raw)
	}
	state := domain.WorkflowInterventionState(str(dig(queued.Body, "intervention", "state")))
	if state != domain.WorkflowInterventionStateQueued {
		t.Fatalf("state = %q, want queued; body=%s", state, queued.Raw)
	}
	if got := env.drivers[m2QueueHarness].SendCalls(); got != 1 {
		// One dispatch (the initial turn) plus nothing from the queue: the controller is
		// busy, so the queued message waits.
		t.Fatalf("provider sends = %d, want 1 (the queued message must not have dispatched)", got)
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: agent-to-agent collaboration over HTTP
// ---------------------------------------------------------------------------

// TestM2AgentCollaborationMessageAndCrossWorkflowRefusal covers acceptance step 7.
//
// An agent speaks with its capability token plus its attempt lease; the daemon derives the
// sender from those, never from the body. A message naming another workflow's task is refused
// and leaves no row.
func TestM2AgentCollaborationMessageAndCrossWorkflowRefusal(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-7")
	otherWorkflowID, otherTasks := env.createWorkflow(t, "scenario-7-other")
	attemptID, lease := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-7")
	bound := env.bindAttempt(t, workflowID, attemptID, m2SteerHarness, "scenario-7", "work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: status=%d body=%s", bound.Status, bound.Raw)
	}
	sessionID := domain.SessionID(str(dig(bound.Body, "binding", "sessionId")))

	token, verifier := m2IssueCapability(t, env, sessionID)

	agentHeaders := func(attempt domain.WorkflowAttemptID, leaseToken string) map[string]string {
		return map[string]string{
			principal.HeaderAgentCapability: token,
			principal.HeaderAttemptID:       string(attempt),
			principal.HeaderAttemptLease:    leaseToken,
			"Content-Type":                  "application/json",
		}
	}
	_ = verifier

	created := env.doWithHeaders(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/messages",
		`{"kind":"progress","body":"half done","clientMessageId":"scenario-7-msg-1"}`,
		agentHeaders(attemptID, lease))
	if created.Status != http.StatusCreated {
		t.Fatalf("agent message: status=%d body=%s", created.Status, created.Raw)
	}
	if !boolean(dig(created.Body, "created")) {
		t.Errorf("created = false on a first message")
	}
	if got := str(dig(created.Body, "message", "senderKind")); got != "agent" {
		t.Errorf("senderKind = %q, want agent (derived, not supplied)", got)
	}

	rows, err := env.store.ListWorkflowMessages(context.Background(), workflowID, 100, 0)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored messages = %d, want 1", len(rows))
	}

	// A cross-workflow target is refused and writes nothing.
	before := len(rows)
	crossed := env.doWithHeaders(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/messages",
		fmt.Sprintf(`{"kind":"progress","body":"across the wall","clientMessageId":"scenario-7-msg-2","toTaskId":"%s"}`,
			otherTasks[m2QueueHarness]),
		agentHeaders(attemptID, lease))
	if crossed.Status != http.StatusConflict && crossed.Status != http.StatusForbidden {
		t.Fatalf("cross-workflow message: status=%d, want 409 or 403; body=%s",
			crossed.Status, crossed.Raw)
	}
	after, err := env.store.ListWorkflowMessages(context.Background(), workflowID, 100, 0)
	if err != nil {
		t.Fatalf("list messages after refusal: %v", err)
	}
	if len(after) != before {
		t.Fatalf("a refused cross-workflow message wrote %d row(s)", len(after)-before)
	}
	otherRows, err := env.store.ListWorkflowMessages(context.Background(), otherWorkflowID, 100, 0)
	if err != nil {
		t.Fatalf("list other workflow messages: %v", err)
	}
	if len(otherRows) != 0 {
		t.Fatalf("the other workflow grew %d message(s)", len(otherRows))
	}
	_ = otherWorkflowID
}

// m2IssueCapability mints a worker capability and persists its verifier on the session row, the
// way a real spawn does. It returns the raw token (which only the worker would hold) and the
// stored verifier.
func m2IssueCapability(t *testing.T, env *m2Env, sessionID domain.SessionID) (string, string) {
	t.Helper()
	authority := browser.NewAuthority()
	token, verifier, err := authority.Issue(sessionID)
	if err != nil {
		t.Fatalf("issue capability: %v", err)
	}
	ctx := context.Background()
	record, found, err := env.store.GetSession(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("read session %s: found=%v err=%v", sessionID, found, err)
	}
	record.Metadata.BrowserCapabilityVerifier = verifier
	if err := env.store.UpdateSession(ctx, record); err != nil {
		t.Fatalf("persist capability verifier: %v", err)
	}
	return token, verifier
}

// ---------------------------------------------------------------------------
// Scenario 8: task request as agent, decided as operator
// ---------------------------------------------------------------------------

// TestM2TaskRequestRaisedByAgentDecidedByOperator covers acceptance step 8.
//
// An approval that cannot be materialized must not read as success: the daemon answers 501
// with TASK_REQUEST_REQUIRES_APPROVAL and materialized=false, because workflow.Service has no
// way to add a task to a live graph. That is an accepted M2 limitation, not a defect — the
// decision itself IS recorded, and the response says exactly that.
func TestM2TaskRequestRaisedByAgentDecidedByOperator(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-8")
	attemptID, lease := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-8")
	bound := env.bindAttempt(t, workflowID, attemptID, m2SteerHarness, "scenario-8", "work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: status=%d body=%s", bound.Status, bound.Raw)
	}
	sessionID := domain.SessionID(str(dig(bound.Body, "binding", "sessionId")))
	token, _ := m2IssueCapability(t, env, sessionID)
	agentHeaders := map[string]string{
		principal.HeaderAgentCapability: token,
		principal.HeaderAttemptID:       string(attemptID),
		principal.HeaderAttemptLease:    lease,
	}

	raised := env.doWithHeaders(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/task-requests",
		`{"requestId":"scenario-8-req-1","requestKind":"create_task","payload":{"title":"another"}}`,
		agentHeaders)
	if raised.Status != http.StatusCreated && raised.Status != http.StatusOK {
		t.Fatalf("raise task request: status=%d body=%s", raised.Status, raised.Raw)
	}
	requestID := str(dig(raised.Body, "request", "id"))
	if requestID == "" {
		t.Fatalf("no request id in %s", raised.Raw)
	}
	requestRevision, _ := dig(raised.Body, "request", "revision").(json.Number).Int64()

	// The operator decides. Approval records the decision but creates no task, so the
	// answer is 202 Accepted with materialization=pending — never a 200 that implies the
	// work was created.
	tasksBefore := m2CountWorkflowTasks(t, env, workflowID)
	decided := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/task-requests/"+requestID+"/decide",
		fmt.Sprintf(`{"approve":true,"expectedRevision":%d}`, requestRevision))
	if decided.Status != http.StatusAccepted {
		t.Fatalf("decide: status=%d, want 202; body=%s", decided.Status, decided.Raw)
	}
	if got := str(dig(decided.Body, "materialization")); got != string(domain.WorkflowTaskRequestMaterializationPending) {
		t.Fatalf("materialization = %q, want pending; body=%s", got, decided.Raw)
	}
	if got := str(dig(decided.Body, "reasonCode")); got != domain.TaskRequestReasonApprovedNotMaterialized {
		t.Fatalf("reasonCode = %q, want %q; body=%s", got, domain.TaskRequestReasonApprovedNotMaterialized, decided.Raw)
	}
	if boolean(dig(decided.Body, "materialized")) {
		t.Fatal("materialized = true, but this daemon cannot create the requested task")
	}
	// The approval created no task row: the recorded decision is the whole effect.
	if after := m2CountWorkflowTasks(t, env, workflowID); after != tasksBefore {
		t.Fatalf("workflow_tasks rows went from %d to %d across a non-materializing approval", tasksBefore, after)
	}
	// The decision itself was recorded: the request is no longer pending.
	detail, err := env.collab.GetTaskRequest(context.Background(), workflowID, requestID)
	if err != nil {
		t.Fatalf("read task request: %v", err)
	}
	if detail.State == domain.WorkflowTaskRequestStatePending {
		t.Error("the request is still pending; the decision was not recorded")
	}
}

// ---------------------------------------------------------------------------
// Scenario 9: stop, then a queue is refused
// ---------------------------------------------------------------------------

// TestM2StopConfirmsTheBindingAndRefusesLaterQueue covers acceptance step 9.
//
// The stop must land stop_confirmed on the receipt AND advance the binding to
// stop_confirmed, and a later queue on that attempt must be refused — the turn those
// messages would address is the turn that was just stopped.
func TestM2StopConfirmsTheBindingAndRefusesLaterQueue(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-9")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-9")
	_, _, _ = env.bindAndStartTurn(t, workflowID, attemptID, m2SteerHarness, "scenario-9")

	stopped := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		`{"mode":"stop","clientMessageId":"scenario-9-stop-1"}`)
	if stopped.Status != http.StatusOK {
		t.Fatalf("stop: status=%d body=%s", stopped.Status, stopped.Raw)
	}
	state := domain.WorkflowInterventionState(str(dig(stopped.Body, "intervention", "state")))
	if state != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("state = %q, want stop_confirmed; body=%s", state, stopped.Raw)
	}
	if got := env.drivers[m2SteerHarness].InterruptCalls(); got != 1 {
		t.Fatalf("provider interrupts = %d, want 1", got)
	}
	t.Logf("stop receipt: status=%d state=%q", stopped.Status, state)

	// The binding must agree: an intervention receipt that says stopped over a binding that
	// still says bound_ready would let the next caller steer a turn that no longer exists.
	if binding := env.bindState(t, attemptID); binding.State != domain.WorkflowBindingStateStopConfirmed {
		t.Fatalf("binding state = %q, want stop_confirmed", binding.State)
	}

	after := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		`{"mode":"queue","text":"too late","clientMessageId":"scenario-9-queue-1"}`)
	if after.Status != http.StatusConflict {
		t.Fatalf("queue after stop: status=%d, want 409; body=%s", after.Status, after.Raw)
	}
	if got := env.drivers[m2SteerHarness].SendCalls(); got != 1 {
		t.Fatalf("provider sends = %d, want only the original turn", got)
	}
}

// ---------------------------------------------------------------------------
// Scenario 10: an interrupted session is reported, not guessed
// ---------------------------------------------------------------------------

// TestM2InterruptedSessionReportsNonLiveWithAReason covers acceptance step 10.
//
// The session row is moved out of a live state through the store — the same durable fact a
// real interruption produces — and the observe route must report the new binding state plus a
// non-live answer WITH a reason code. It must not report the session as gone, and it must not
// pretend a provider can be addressed.
//
// Two durable shapes are exercised, because they are genuinely different facts:
//
//   - the agent exited inside a live session (activity_state = exited, session not
//     terminated): reported as readiness=exited, live=false, with a reason; and
//   - the session was terminated: the gateway classifies that as ErrExecutionTerminated and the
//     route maps it to 409 SESSION_TERMINATED. Reporting 500 here would mean an unclassified
//     error reached the envelope, which is a different and worse answer than a refusal.
func TestM2InterruptedSessionReportsNonLiveWithAReason(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-10")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-10")
	bound, sessionID, _ := env.bindAndStartTurn(t, workflowID, attemptID, m2SteerHarness, "scenario-10")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: status=%d body=%s", bound.Status, bound.Raw)
	}

	ctx := context.Background()
	// The agent process exited: the durable session says so, without claiming the session
	// itself was destroyed. Stop the controller first so the change is not contradicted by a
	// live one.
	env.chat.StopAll(ctx)
	record, found, err := env.store.GetSession(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("read session: found=%v err=%v", found, err)
	}
	record.Activity.State = domain.ActivityExited
	record.Activity.LastActivityAt = time.Now().UTC()
	if err := env.store.UpdateSession(ctx, record); err != nil {
		t.Fatalf("mark session exited: %v", err)
	}

	binding := env.do(t, http.MethodGet,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/binding", "")
	if binding.Status != http.StatusOK {
		t.Fatalf("read binding: status=%d body=%s", binding.Status, binding.Raw)
	}
	if state := str(dig(binding.Body, "binding", "state")); state == "" {
		t.Fatalf("binding response carries no state: %s", binding.Raw)
	}

	observed := env.do(t, http.MethodGet,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/observe", "")
	if observed.Status != http.StatusOK {
		t.Fatalf("observe: status=%d body=%s", observed.Status, observed.Raw)
	}
	if readiness := str(dig(observed.Body, "observation", "readiness")); readiness != string(ports.SessionReadinessExited) {
		t.Errorf("readiness = %q, want exited", readiness)
	}
	if boolean(dig(observed.Body, "observation", "terminated")) {
		t.Error("terminated = true, but only the agent exited")
	}
	// Live must be present-and-false with a reason: a missing value would read as "unknown",
	// which is a different and less useful answer.
	live, present := dig(observed.Body, "live").(bool)
	if !present {
		t.Fatalf("live is absent from the observe response: %s", observed.Raw)
	}
	if live {
		t.Error("live = true for an exited session")
	}
	if reason := str(dig(observed.Body, "reasonCode")); reason == "" {
		t.Error("no reasonCode accompanies live=false")
	}

	// The terminated shape is a different durable fact from "the agent exited", and it has its
	// own answer: the gateway classifies it as ErrExecutionTerminated and the route maps that to
	// 409 SESSION_TERMINATED. A 500 here would mean an unclassified error reached the envelope,
	// which is how this case read before the mapping existed.
	record, _, err = env.store.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("read session again: %v", err)
	}
	record.IsTerminated = true
	if err := env.store.UpdateSession(ctx, record); err != nil {
		t.Fatalf("terminate session: %v", err)
	}
	terminated := env.do(t, http.MethodGet,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/observe", "")
	if terminated.Status == http.StatusInternalServerError {
		t.Fatalf("a terminated session answered 500: the refusal is unclassified. body=%s",
			terminated.Raw)
	}
	if terminated.Status != http.StatusConflict {
		t.Fatalf("terminated observe: status=%d, want 409; body=%s", terminated.Status, terminated.Raw)
	}
	if code := str(dig(terminated.Body, "code")); code != "SESSION_TERMINATED" {
		t.Fatalf("code = %q, want SESSION_TERMINATED; body=%s", code, terminated.Raw)
	}
	t.Logf("terminated observe: status=%d code=%q", terminated.Status,
		str(dig(terminated.Body, "code")))
}

// ---------------------------------------------------------------------------
// Scenario 11: restart over the same store
// ---------------------------------------------------------------------------

// TestM2RestartReadsBindingsWithoutInMemoryState covers acceptance step 11.
//
// A second fixture is built over the SAME store with fresh resolvers, coordinators, and Chat
// service — no in-memory state carries over. The bindings must still be readable, and a
// reconcile on an unknown binding must adopt the existing session instead of spawning.
func TestM2RestartReadsBindingsWithoutInMemoryState(t *testing.T) {
	st := openM2Store(t)
	first := newM2Env(t, st)

	workflowID, tasks := first.createWorkflow(t, "scenario-11")
	attemptID, _ := first.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-11")
	bound := first.bindAttempt(t, workflowID, attemptID, m2SteerHarness, "scenario-11", "work")
	if bound.Status != http.StatusOK {
		t.Fatalf("bind: status=%d body=%s", bound.Status, bound.Raw)
	}
	sessionID := str(dig(bound.Body, "binding", "sessionId"))
	if sessionID == "" {
		t.Fatalf("no session bound: %s", bound.Raw)
	}
	spawnsBefore := first.spawner.m2SpawnCount()
	first.chat.StopAll(context.Background())

	// The daemon restarts: everything in memory is gone, INCLUDING the spawner. The restart
	// gets its own fake over the same store, and the count that matters is the durable one:
	// the session rows. A fresh spawner with 0 calls, plus an unchanged session count, is a
	// stronger statement than a shared counter — it proves the restart did not even have the
	// opportunity to reuse in-memory bookkeeping.
	second := newM2Env(t, st)
	if second.spawner == first.spawner {
		t.Fatal("the restart fixture reused the same spawner; the restart would not be a restart")
	}
	if got := second.spawner.m2SpawnCount(); got != 0 {
		t.Fatalf("the restarted fixture already spawned %d time(s)", got)
	}
	sessionsBefore := m2CountSessions(t, st)

	reread := second.do(t, http.MethodGet,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/binding", "")
	if reread.Status != http.StatusOK {
		t.Fatalf("binding after restart: status=%d body=%s", reread.Status, reread.Raw)
	}
	if got := str(dig(reread.Body, "binding", "sessionId")); got != sessionID {
		t.Fatalf("session after restart = %q, want %q", got, sessionID)
	}

	// Reconcile 实时发现 controller 尚未恢复，必须闭锁并保留原有 Session。
	reconciled := second.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/reconcile", "")
	if reconciled.Status != http.StatusConflict || str(dig(reconciled.Body, "code")) != "SESSION_NOT_READY" ||
		second.bindState(t, attemptID).State != domain.WorkflowBindingStateRecoveryUncertain ||
		str(dig(reconciled.Body, "details", "binding", "sessionId")) != sessionID {
		t.Fatalf("重启未恢复态: status=%d body=%s", reconciled.Status, reconciled.Raw)
	}
	if got := second.spawner.m2SpawnCount(); got != 0 {
		t.Fatalf("the restarted daemon spawned %d time(s); a settled binding must not re-spawn", got)
	}
	if got := m2CountSessions(t, st); got != sessionsBefore {
		t.Fatalf("durable sessions went from %d to %d across a restart reconcile",
			sessionsBefore, got)
	}
	_ = spawnsBefore
}

// m2CountSessions counts every session row, which is the durable fact a restart must not
// change when nothing was asked to spawn.
func m2CountSessions(t *testing.T, st *store.Store) int {
	t.Helper()
	rows, err := st.ListSessions(context.Background(), m2ProjectID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return len(rows)
}

// ---------------------------------------------------------------------------
// Scenario 12: adoption of a committed session, never a re-spawn
// ---------------------------------------------------------------------------

// TestM2ReconcileAdoptsACommittedSession covers acceptance step 12.
//
// The spawn commits a session and then the answer is lost, so the binding lands at
// spawn_unknown. Reconcile must find that session by its stable spawn request id, promote the
// binding to bound_ready, and never spawn again. A second reconcile is a no-op.
func TestM2ReconcileAdoptsACommittedSession(t *testing.T) {
	st := openM2Store(t)
	env := newM2Env(t, st)

	workflowID, tasks := env.createWorkflow(t, "scenario-12")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-12")

	// A lost answer: the session row IS created and the caller learns nothing. This is the
	// one outcome that must never be read as "nothing happened".
	env.spawner.failNext.Store(true)
	bound := env.bindAttempt(t, workflowID, attemptID, m2SteerHarness, "scenario-12", "work")
	// 409, not 200: an unknown spawn is a refusal, not a success. A 200 here would invite the
	// caller to treat "we cannot tell" as "it worked".
	if bound.Status != http.StatusConflict {
		t.Fatalf("bind with a lost answer: status=%d, want 409; body=%s", bound.Status, bound.Raw)
	}
	if code := str(dig(bound.Body, "code")); code != "SPAWN_OUTCOME_UNKNOWN" {
		t.Fatalf("code = %q, want SPAWN_OUTCOME_UNKNOWN; body=%s", code, bound.Raw)
	}
	// A refusal carries the binding under `details`, not at the top level: the error envelope
	// owns the response shape, and the evidence rides along inside it.
	if state := domain.WorkflowBindingState(str(dig(bound.Body, "details", "binding", "state"))); state != domain.WorkflowBindingStateSpawnUnknown {
		t.Fatalf("state = %q, want spawn_unknown; body=%s", state, bound.Raw)
	}
	if !boolean(dig(bound.Body, "details", "frozen")) {
		t.Error("frozen = false for an unknown spawn; a caller could retry and duplicate work")
	}
	// The session row exists even though the answer was lost — that is what makes adopting it
	// possible at all.
	spawnsBefore := env.spawner.m2SpawnCount()
	if spawnsBefore != 1 {
		t.Fatalf("spawns = %d, want 1", spawnsBefore)
	}

	reconciled := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/reconcile", "")
	if reconciled.Status != http.StatusOK {
		t.Fatalf("reconcile: status=%d body=%s", reconciled.Status, reconciled.Raw)
	}
	// The recovered session has a row and no controller — the spawn failed before one started
	// — so the honest landing state is session_provisioning, not bound_ready. What matters is
	// that it is a SESSION-BEARING state reached without spawning.
	state := domain.WorkflowBindingState(str(dig(reconciled.Body, "binding", "state")))
	if state != domain.WorkflowBindingStateBoundReady &&
		state != domain.WorkflowBindingStateSessionProvisioning {
		t.Fatalf("state after reconcile = %q, want bound_ready or session_provisioning", state)
	}
	if boolean(dig(reconciled.Body, "spawned")) {
		t.Fatal("reconcile reported spawned=true")
	}
	if got := env.spawner.m2SpawnCount(); got != spawnsBefore {
		t.Fatalf("spawns after reconcile = %d, want %d (recovery must never re-spawn)",
			got, spawnsBefore)
	}
	persisted := env.bindState(t, attemptID)
	if persisted.State != state {
		t.Errorf("stored state = %q, want %q", persisted.State, state)
	}
	t.Logf("reconcile adoption: status=%d state=%q spawned=%v",
		reconciled.Status, state, boolean(dig(reconciled.Body, "spawned")))
	if persisted.SessionID == nil {
		t.Fatal("recovery adopted nothing: the binding still names no session")
	}
	// And the adopted session is the one the lost spawn actually created.
	if _, found, err := env.store.GetSession(context.Background(), *persisted.SessionID); err != nil || !found {
		t.Fatalf("adopted session %s does not exist: found=%v err=%v", *persisted.SessionID, found, err)
	}

	// 再次观察仍没有 controller；闭锁执行但保留已找回的 Session，不允许重新创建。
	again := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/reconcile", "")
	if again.Status != http.StatusConflict || str(dig(again.Body, "code")) != "SESSION_NOT_READY" ||
		str(dig(again.Body, "details", "binding", "sessionId")) != string(*persisted.SessionID) {
		t.Fatalf("second reconcile 未正确闭锁: status=%d body=%s", again.Status, again.Raw)
	}
	if got := env.spawner.m2SpawnCount(); got != spawnsBefore {
		t.Fatalf("spawns after second reconcile = %d, want %d", got, spawnsBefore)
	}
}

// ---------------------------------------------------------------------------
// Scenario 13: cross-workflow isolation
// ---------------------------------------------------------------------------

// TestM2CrossWorkflowIsolation covers acceptance step 13: a binding read through the wrong
// workflow id is a 404 (never a 409, which would confirm the attempt exists elsewhere), an
// intervention carrying a mismatched attempt is refused, and the other workflow's binding is
// untouched.
func TestM2CrossWorkflowIsolation(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowA, tasksA := env.createWorkflow(t, "scenario-13a")
	workflowB, tasksB := env.createWorkflow(t, "scenario-13b")
	attemptA, _ := env.claimAndStart(t, workflowA, tasksA[m2SteerHarness], "scenario-13a")
	attemptB, _ := env.claimAndStart(t, workflowB, tasksB[m2SteerHarness], "scenario-13b")

	// Each bind needs its own idempotency key: the spawn request id is global, and reusing one
	// across two attempts is refused (correctly — that is the duplicate-spawn guard).
	for _, pair := range []struct {
		workflow domain.WorkflowID
		attempt  domain.WorkflowAttemptID
		key      string
	}{
		{workflowA, attemptA, "scenario-13a"},
		{workflowB, attemptB, "scenario-13b"},
	} {
		if resp := env.bindAttempt(t, pair.workflow, pair.attempt, m2SteerHarness, pair.key, "work"); resp.Status != http.StatusOK {
			t.Fatalf("bind %s: status=%d body=%s", pair.attempt, resp.Status, resp.Raw)
		}
	}
	bindingBBefore := env.bindState(t, attemptB)

	// A's attempt through B's workflow path: 404, so nothing is confirmed.
	crossed := env.do(t, http.MethodGet,
		"/workflows/"+string(workflowB)+"/attempts/"+string(attemptA)+"/binding", "")
	if crossed.Status != http.StatusNotFound {
		t.Fatalf("cross-workflow binding read: status=%d, want 404; body=%s",
			crossed.Status, crossed.Raw)
	}

	// An intervention naming A's attempt on B's route is refused, and B's binding is untouched.
	intervened := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowB)+"/attempts/"+string(attemptA)+"/interventions",
		`{"mode":"queue","text":"across the wall","clientMessageId":"scenario-13-msg-1"}`)
	if intervened.Status != http.StatusNotFound && intervened.Status != http.StatusConflict &&
		intervened.Status != http.StatusForbidden {
		t.Fatalf("cross-workflow intervention: status=%d body=%s", intervened.Status, intervened.Raw)
	}
	if after := env.bindState(t, attemptB); after.Revision != bindingBBefore.Revision ||
		after.State != bindingBBefore.State {
		t.Fatalf("the other workflow's binding changed: %+v -> %+v", bindingBBefore, after)
	}
	if _, found, err := env.store.GetWorkflowInterventionByClientMessage(
		context.Background(), workflowB, "scenario-13-msg-1"); err != nil {
		t.Fatalf("read intervention: %v", err)
	} else if found {
		t.Error("a refused cross-workflow intervention wrote a row on the wrong workflow")
	}
}

// ---------------------------------------------------------------------------
// Scenario 14: a stale controller generation is refused
// ---------------------------------------------------------------------------

// TestM2StaleGenerationIsRefused covers acceptance step 14, over the wire.
//
// Two fences exist and they fire in a fixed order, so both are exercised separately:
//
//  1. resolveTarget's observation fence compares the LIVE controller with the binding's recorded
//     generation. It runs first, and it is what catches a genuinely replaced controller.
//  2. fenceGeneration compares the generation the CALLER sent with the binding's. It only gets a
//     chance once (1) passes, which is why the first case below keeps the controller alive.
//
// Both must refuse before the provider is asked. A negative control runs last: the same steer
// with no generation supplied still cannot reach the provider, so omitting the field degrades to
// a refusal rather than to a mis-pinned steer.
func TestM2StaleGenerationIsRefused(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	workflowID, tasks := env.createWorkflow(t, "scenario-14")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "scenario-14")
	_, sessionID, turn := env.bindAndStartTurn(t, workflowID, attemptID, m2SteerHarness, "scenario-14")
	bindingGeneration := m2BindingGeneration(t, env, attemptID)
	steersBefore := env.drivers[m2SteerHarness].SteerCalls()

	// (1) The request-level fence, with a live controller so nothing earlier can fire. The caller
	// claims a generation the binding does not hold.
	wrongGeneration := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		fmt.Sprintf(`{"mode":"steer","text":"wrong generation","clientMessageId":"scenario-14-steer-1",`+
			`"targetProviderTurnId":"%s","harness":"%s","controllerGeneration":"a-generation-nobody-owns"}`,
			turn, m2SteerHarness))
	if wrongGeneration.Status != http.StatusConflict {
		t.Fatalf("a wrong-generation steer: status=%d, want 409; body=%s",
			wrongGeneration.Status, wrongGeneration.Raw)
	}
	wrongCode := str(dig(wrongGeneration.Body, "code"))
	wrongReason := str(dig(wrongGeneration.Body, "details", "reasonCode"))
	t.Logf("request-level fence: status=%d code=%q reason=%q",
		wrongGeneration.Status, wrongCode, wrongReason)
	// The wired field reached the coordinator and produced the fence's own advice code. TURN_MISMATCH
	// is what a stale generation means to a client: the turn the caller meant is no longer
	// addressable by generation. Asserted exactly, because accepting "some 409" would let the
	// mapping drift back to a capability verdict about the provider, which this is not.
	if wrongCode != "TURN_MISMATCH" {
		t.Fatalf("code = %q, want TURN_MISMATCH; body=%s", wrongCode, wrongGeneration.Raw)
	}
	if got := env.drivers[m2SteerHarness].SteerCalls(); got != steersBefore {
		t.Fatalf("provider steers = %d, want %d (a fenced request must not reach the provider)",
			got, steersBefore)
	}

	// (2) The observation fence: the controller is genuinely replaced, so the binding's recorded
	// generation no longer describes what is running.
	ctx := context.Background()
	if err := env.chat.Stop(ctx, sessionID); err != nil {
		t.Fatalf("stop controller: %v", err)
	}
	if _, err := env.chat.Start(ctx, chatsvc.StartConfig{
		SessionID:            sessionID,
		ProjectID:            m2ProjectID,
		Harness:              m2SteerHarness,
		WorkspacePath:        t.TempDir(),
		ControllerGeneration: "replacement-generation",
	}); err != nil {
		t.Fatalf("restart controller: %v", err)
	}
	if _, err := env.chat.Send(ctx, sessionID, ports.ChatUserMessage{
		Text: "second life", ClientMessageID: "scenario-14-turn-2",
		Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("send on replacement controller: %v", err)
	}
	liveTurn := m2AwaitTurn(t, env.chat, sessionID)

	replaced := env.do(t, http.MethodPost,
		"/workflows/"+string(workflowID)+"/attempts/"+string(attemptID)+"/interventions",
		fmt.Sprintf(`{"mode":"steer","text":"stale guidance","clientMessageId":"scenario-14-steer-2",`+
			`"targetProviderTurnId":"%s","harness":"%s"}`, liveTurn, m2SteerHarness))
	if replaced.Status != http.StatusConflict {
		t.Fatalf("a steer after a controller replacement: status=%d, want 409; body=%s",
			replaced.Status, replaced.Raw)
	}
	replacedCode := str(dig(replaced.Body, "code"))
	replacedReason := str(dig(replaced.Body, "details", "reasonCode"))
	t.Logf("observation fence: status=%d code=%q reason=%q",
		replaced.Status, replacedCode, replacedReason)
	if got := env.drivers[m2SteerHarness].SteerCalls(); got != steersBefore {
		t.Fatalf("provider steers after replacement = %d, want %d", got, steersBefore)
	}
	// Nothing was settled: a fenced refusal is not a receipt.
	for _, key := range []string{"scenario-14-steer-1", "scenario-14-steer-2"} {
		if row, found, err := env.store.GetWorkflowInterventionByClientMessage(ctx, workflowID, key); err != nil {
			t.Fatalf("read intervention %s: %v", key, err)
		} else if found && row.State == domain.WorkflowInterventionStateApplied {
			t.Fatalf("%s produced an applied receipt: %+v", key, row)
		}
	}
	if bindingGeneration == "" {
		t.Fatal("the captured binding generation was empty; the scenario proved nothing")
	}
}

// m2CountWorkflowTasks counts a workflow's task rows. It is how a test proves an
// approval created no task, which the 202/pending contract asserts positively rather
// than by trusting the reason code alone.
func m2CountWorkflowTasks(t *testing.T, env *m2Env, workflowID domain.WorkflowID) int {
	t.Helper()
	tasks, err := env.store.ListWorkflowTasks(context.Background(), workflowID, "", 200, 0)
	if err != nil {
		t.Fatalf("list workflow tasks: %v", err)
	}
	return len(tasks)
}
