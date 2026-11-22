package httpd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/computeruse"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
)

func remainingRequestBudget(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return time.Until(deadline)
}

type timeoutProbeSessionService struct {
	controllers.SessionService
	uploadBudget  chan time.Duration
	sendBudget    chan time.Duration
	previewBudget chan time.Duration
	spawnSetup    time.Duration
	spawnRuns     time.Duration
	spawnLive     chan bool
	spawnBudget   chan time.Duration
	resumeBudget  chan time.Duration
	resumeSetup   time.Duration
}

// Spawn extends the request's budget by spawnSetup (none when negative), runs
// past the ordinary budget, and reports whether its context was still live.
func (s *timeoutProbeSessionService) Spawn(ctx context.Context, _ ports.SpawnConfig) (domain.Session, int, int, error) {
	if s.spawnBudget != nil {
		// A spawn with no Setup steps still extends: the base budget is then its whole budget.
		ctx, cancel := ports.ExtendSpawnRequestBudget(ctx, 0)
		defer cancel()
		s.spawnBudget <- remainingRequestBudget(ctx)
		return domain.Session{}, 0, 0, errors.New("probe only")
	}
	if s.spawnSetup >= 0 {
		var cancel context.CancelFunc
		ctx, cancel = ports.ExtendSpawnRequestBudget(ctx, s.spawnSetup)
		defer cancel()
	}
	time.Sleep(s.spawnRuns)
	s.spawnLive <- ctx.Err() == nil
	return domain.Session{}, 0, 0, errors.New("probe only")
}

func (s *timeoutProbeSessionService) GetRecord(ctx context.Context, _ domain.SessionID) (domain.SessionRecord, error) {
	s.previewBudget <- remainingRequestBudget(ctx)
	return domain.SessionRecord{}, errors.New("probe only")
}

// recordBudget is the deadline GetRecord saw. The route has run by the time it
// is read, so a route that never reached GetRecord fails here instead of
// blocking the test.
func (s *timeoutProbeSessionService) recordBudget(t *testing.T) time.Duration {
	t.Helper()
	select {
	case budget := <-s.previewBudget:
		return budget
	case <-time.After(time.Second):
		t.Fatal("the route never read the session record")
		return 0
	}
}

func (s *timeoutProbeSessionService) ResumeAgent(ctx context.Context, id domain.SessionID) (sessionsvc.ResumeAgentOutcome, error) {
	// An import's first open extends the budget by its Setup steps; with none
	// the request keeps its base budget.
	ctx, cancel := ports.ExtendSpawnRequestBudget(ctx, s.resumeSetup)
	defer cancel()
	s.resumeBudget <- remainingRequestBudget(ctx)
	return sessionsvc.ResumeAgentOutcome{Session: domain.Session{ID: id}}, nil
}

func (s *timeoutProbeSessionService) StageAttachments(ctx context.Context, _ domain.SessionID, _ []ports.SpawnAttachment) ([]string, error) {
	s.uploadBudget <- remainingRequestBudget(ctx)
	return []string{"attachment.txt"}, nil
}

func (s *timeoutProbeSessionService) Send(ctx context.Context, _ domain.SessionID, _ string, _ *ports.SpawnAttachment) error {
	s.sendBudget <- remainingRequestBudget(ctx)
	return nil
}

func TestAttachmentUploadRouteUsesExtendedTimeout(t *testing.T) {
	svc := &timeoutProbeSessionService{uploadBudget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: 250 * time.Millisecond},
		discardLogger(),
		nil,
		APIDeps{Sessions: svc},
		ControlDeps{},
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/attachments", bytes.NewBufferString(`{"attachments":[{"mimeType":"text/plain","data":"YQ=="}]}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST attachments status = %d, want 201; body=%s", response.Code, response.Body.String())
	}
	budget := <-svc.uploadBudget
	if budget < 9*time.Minute || budget > 10*time.Minute {
		t.Fatalf("attachment upload budget = %s, want about 10 minutes", budget)
	}
}

// A managed preview carries the dev server's HMR socket and event streams; the
// REST budget cut them at a minute (504) and reloaded the page.
func TestManagedPreviewAppProxyHasNoRequestTimeout(t *testing.T) {
	svc := &timeoutProbeSessionService{previewBudget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: 250 * time.Millisecond},
		discardLogger(),
		nil,
		APIDeps{Sessions: svc, PreviewServer: struct {
			controllers.ManagedPreviewServer
		}{}},
		ControlDeps{},
	)
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/sessions/ao-1/preview/app/_next/webpack-hmr", nil))
	if budget := svc.recordBudget(t); budget != 0 {
		t.Fatalf("managed preview budget = %s, want no deadline", budget)
	}
}

// The agent browser's stream socket stays open as long as its tab does.
func TestAgentBrowserProxyHasNoRequestTimeout(t *testing.T) {
	svc := &timeoutProbeSessionService{previewBudget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: 250 * time.Millisecond, AgentBrowserURL: "http://127.0.0.1:9"},
		discardLogger(),
		nil,
		APIDeps{Sessions: svc},
		ControlDeps{},
	)
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/sessions/ao-1/agent-browser/ws/cdp", nil))
	if budget := svc.recordBudget(t); budget != 0 {
		t.Fatalf("agent browser budget = %s, want no deadline", budget)
	}
}

func TestAttachmentCapableSendUsesExtendedTimeoutOnlyForUploads(t *testing.T) {
	const configuredTimeout = 250 * time.Millisecond
	svc := &timeoutProbeSessionService{sendBudget: make(chan time.Duration, 2)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: configuredTimeout},
		discardLogger(),
		nil,
		APIDeps{Sessions: svc},
		ControlDeps{},
	)
	for _, tc := range []struct {
		name   string
		body   string
		upload bool
	}{
		{name: "plain text", body: `{"message":"hello"}`},
		{name: "attachment", body: `{"message":"hello","attachment":{"mimeType":"text/plain","data":"YQ=="}}`, upload: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/send", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.upload {
				req.Header.Set(attachmentUploadHeader, "1")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("send status = %d, want 200; body=%s", response.Code, response.Body.String())
			}
			budget := <-svc.sendBudget
			if tc.upload && (budget < 9*time.Minute || budget > 10*time.Minute) {
				t.Fatalf("attachment budget = %s, want about 10 minutes", budget)
			}
			if !tc.upload && (budget <= 0 || budget > configuredTimeout) {
				t.Fatalf("plain text budget = %s, want no more than %s", budget, configuredTimeout)
			}
		})
	}
}

// timeoutProbePRs records the request budget a PR comment post runs under.
type timeoutProbePRs struct {
	prsvc.ActionManager
	budget chan time.Duration
}

func (p *timeoutProbePRs) PostComment(ctx context.Context, _ prsvc.CommentRequest) (prsvc.CommentResult, error) {
	p.budget <- remainingRequestBudget(ctx)
	return prsvc.CommentResult{ID: "c1"}, nil
}

func (p *timeoutProbePRs) Merge(ctx context.Context, _ prsvc.MergeRequest) (prsvc.MergeResult, error) {
	p.budget <- remainingRequestBudget(ctx)
	return prsvc.MergeResult{}, nil
}

func (p *timeoutProbePRs) MergeStack(ctx context.Context, _ prsvc.StackMergeRequest) (prsvc.StackMergeResult, error) {
	p.budget <- remainingRequestBudget(ctx)
	return prsvc.StackMergeResult{}, nil
}

// A stack merge waits up to two minutes for GitHub to land it; the one-minute
// REST budget cancelled it, and the layers it landed went unrecorded. A plain
// merge keeps the ordinary budget.
func TestStackMergeRouteUsesExtendedTimeout(t *testing.T) {
	const configuredTimeout = 250 * time.Millisecond
	svc := &timeoutProbePRs{budget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(config.Config{RequestTimeout: configuredTimeout}, discardLogger(), nil, APIDeps{PRs: svc}, ControlDeps{})
	const head = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, tc := range []struct {
		path, body string
		long       bool
	}{
		{path: "/api/v1/prs/2/stack/merge", body: `{"prUrl":"https://github.com/acme/widgets/pull/2","expectedHeads":[{"number":2,"headSha":"` + head + `"}]}`, long: true},
		{path: "/api/v1/prs/2/merge", body: `{"prUrl":"https://github.com/acme/widgets/pull/2","expectedHeadSha":"` + head + `"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		select {
		case budget := <-svc.budget:
			if tc.long && (budget < 179*time.Second || budget > 3*time.Minute) {
				t.Fatalf("%s budget = %s, want about 3 minutes", tc.path, budget)
			}
			if !tc.long && (budget <= 0 || budget > configuredTimeout) {
				t.Fatalf("%s budget = %s, want no more than %s", tc.path, budget, configuredTimeout)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s never reached the PR service: status=%d body=%s", tc.path, response.Code, response.Body.String())
		}
	}
}

func TestPRCommentWithFilesUsesExtendedTimeout(t *testing.T) {
	const configuredTimeout = 250 * time.Millisecond
	prs := &timeoutProbePRs{budget: make(chan time.Duration, 2)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: configuredTimeout},
		discardLogger(),
		nil,
		APIDeps{PRs: prs},
		ControlDeps{},
	)
	for _, tc := range []struct {
		name   string
		body   string
		upload bool
	}{
		{name: "text", body: `{"prUrl":"https://github.com/acme/app/pull/7","body":"hello"}`},
		{name: "files", body: `{"prUrl":"https://github.com/acme/app/pull/7","body":"![a.png](attachment:0)","attachments":[{"name":"a.png","contentType":"image/png","data":"iVBORw=="}]}`, upload: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/prs/7/comments", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.upload {
				req.Header.Set(attachmentUploadHeader, "1")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("comment status = %d, want 200; body=%s", response.Code, response.Body.String())
			}
			budget := <-prs.budget
			if tc.upload && (budget < 9*time.Minute || budget > 10*time.Minute) {
				t.Fatalf("comment with files budget = %s, want about 10 minutes", budget)
			}
			if !tc.upload && (budget <= 0 || budget > configuredTimeout) {
				t.Fatalf("text comment budget = %s, want no more than %s", budget, configuredTimeout)
			}
		})
	}
}

type timeoutProbeProjectManager struct {
	projectsvc.Manager
	budget chan time.Duration
}

func (m *timeoutProbeProjectManager) PrepareClone(ctx context.Context, _ projectsvc.CloneInput) (projectsvc.ClonePreparationResult, error) {
	m.budget <- remainingRequestBudget(ctx)
	return projectsvc.ClonePreparationResult{}, nil
}

func (m *timeoutProbeProjectManager) PrepareCloneWithProgress(ctx context.Context, in projectsvc.CloneInput, _ func(projectsvc.CloneProgress)) (projectsvc.ClonePreparationResult, error) {
	return m.PrepareClone(ctx, in)
}

func (m *timeoutProbeProjectManager) Clone(ctx context.Context, _ projectsvc.CloneInput) (projectsvc.Project, error) {
	m.budget <- remainingRequestBudget(ctx)
	return projectsvc.Project{}, errors.New("probe only")
}

// A clone downloads the whole repository inside the request; a ~500 MB
// repository took longer than the one-minute REST budget and failed.
func TestCloneRoutesUseExtendedTimeout(t *testing.T) {
	mgr := &timeoutProbeProjectManager{budget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: 250 * time.Millisecond},
		discardLogger(),
		nil,
		APIDeps{Projects: mgr},
		ControlDeps{},
	)
	for _, path := range []string{"/api/v1/projects/clone", "/api/v1/projects/clone/prepare", "/api/v1/projects/clone/prepare/stream"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"remoteUrl":"https://github.com/acme/repo","destinationParent":"/tmp"}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(httptest.NewRecorder(), req)
		select {
		case budget := <-mgr.budget:
			if budget < 29*time.Minute || budget > 30*time.Minute {
				t.Fatalf("%s budget = %s, want about 30 minutes", path, budget)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s never reached the project manager", path)
		}
	}
}

type timeoutProbeComputerUse struct {
	controllers.ComputerUse
	budget chan time.Duration
}

func (c *timeoutProbeComputerUse) Call(ctx context.Context, _ computeruse.CallRequest) (computeruse.CallResult, error) {
	c.budget <- remainingRequestBudget(ctx)
	return computeruse.CallResult{}, nil
}

// A run_steps batch has the driver's 60 s plus its report and closing read;
// the one-minute REST budget cut it off with a 503 and no step report.
func TestComputerUseCallUsesExtendedTimeout(t *testing.T) {
	svc := &timeoutProbeComputerUse{budget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: 250 * time.Millisecond},
		discardLogger(),
		nil,
		APIDeps{ComputerUse: svc},
		ControlDeps{},
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/computer-use/call", bytes.NewBufferString(`{"tool":"run_steps","arguments":{"steps":[]}}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	select {
	case budget := <-svc.budget:
		if budget < 119*time.Second || budget > 2*time.Minute {
			t.Fatalf("computer-use call budget = %s, want about 2 minutes", budget)
		}
	case <-time.After(time.Second):
		t.Fatalf("the route never reached Call: status=%d body=%s", response.Code, response.Body.String())
	}
}

// A spawn runs the project's Setup inside the request; a Setup step longer than
// one REST request used to die at the flat deadline ("signal: killed"). The
// request keeps the ordinary budget until the spawn extends it.
func TestSpawnRouteBudgetCoversSetupOnlyOnceExtended(t *testing.T) {
	const configuredTimeout = 200 * time.Millisecond
	previous := setupRequestBudget
	setupRequestBudget = configuredTimeout
	t.Cleanup(func() { setupRequestBudget = previous })
	for _, tc := range []struct {
		name  string
		setup time.Duration
		live  bool
	}{
		{"extended by its Setup", 400 * time.Millisecond, true},
		{"never extended", -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &timeoutProbeSessionService{spawnSetup: tc.setup, spawnRuns: 350 * time.Millisecond, spawnLive: make(chan bool, 1)}
			router := NewRouterWithControl(config.Config{RequestTimeout: configuredTimeout}, discardLogger(), nil, APIDeps{Sessions: svc}, ControlDeps{})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewBufferString(`{"projectId":"p1","kind":"worker","harness":"claude-code"}`))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			select {
			case live := <-svc.spawnLive:
				if live != tc.live {
					t.Fatalf("spawn context live past the ordinary budget = %v, want %v", live, tc.live)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("the route never reached Spawn: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

// A cold spawn fetches, creates a worktree and starts the agent inside one
// request: setup routes start from a 120s budget, every other route keeps the
// configured REST timeout.
func TestSetupRoutesStartFromTheSpawnBudget(t *testing.T) {
	svc := &timeoutProbeSessionService{spawnBudget: make(chan time.Duration, 1), sendBudget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(config.Config{RequestTimeout: config.DefaultRequestTimeout}, discardLogger(), nil, APIDeps{Sessions: svc}, ControlDeps{})
	for _, path := range []string{"/api/v1/sessions"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"projectId":"p1","kind":"worker","harness":"claude-code"}`))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		select {
		case budget := <-svc.spawnBudget:
			if budget <= 110*time.Second || budget > 120*time.Second {
				t.Fatalf("%s budget = %s, want about 120s", path, budget)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s never reached Spawn: status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/s1/send", bytes.NewBufferString(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	select {
	case budget := <-svc.sendBudget:
		if budget > config.DefaultRequestTimeout {
			t.Fatalf("send budget = %s, want at most the REST timeout %s", budget, config.DefaultRequestTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("send never reached the service: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSetupRoutesAreTheOnesThatRunSetup(t *testing.T) {
	router := chi.NewRouter()
	seen := map[string]bool{}
	record := func(w http.ResponseWriter, req *http.Request) { seen[req.Method+" "+req.URL.Path] = setupRoute(req) }
	router.Post("/api/v1/sessions", record)
	router.Post("/api/v1/orchestrators", record)
	router.Post("/api/v1/sessions/{sessionId}/restore", record)
	router.Post("/api/v1/sessions/{sessionId}/resume-agent", record)
	router.Post("/api/v1/sessions/{sessionId}/fork", record)
	router.Post("/api/v1/sessions/{sessionId}/send", record)
	router.Get("/api/v1/sessions", record)
	for _, call := range [][2]string{{"POST", "/api/v1/sessions"}, {"POST", "/api/v1/orchestrators"}, {"POST", "/api/v1/sessions/s1/restore"}, {"POST", "/api/v1/sessions/s1/resume-agent"}, {"POST", "/api/v1/sessions/s1/fork"}, {"POST", "/api/v1/sessions/s1/send"}, {"GET", "/api/v1/sessions"}} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(call[0], call[1], nil))
	}
	want := map[string]bool{"POST /api/v1/sessions": true, "POST /api/v1/orchestrators": true, "POST /api/v1/sessions/s1/restore": true, "POST /api/v1/sessions/s1/resume-agent": true, "POST /api/v1/sessions/s1/fork": true, "POST /api/v1/sessions/s1/send": false, "GET /api/v1/sessions": false}
	for call, setup := range want {
		if seen[call] != setup {
			t.Errorf("setupRoute(%s) = %v, want %v", call, seen[call], setup)
		}
	}
}

// Resuming reads the agent's whole native history; a large imported Codex
// thread took over a minute, past the ordinary REST budget.
func TestResumeAgentRouteUsesExtendedTimeout(t *testing.T) {
	svc := &timeoutProbeSessionService{resumeBudget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: 250 * time.Millisecond},
		discardLogger(),
		nil,
		APIDeps{Sessions: svc},
		ControlDeps{},
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/resume-agent", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("POST resume-agent status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	budget := <-svc.resumeBudget
	if budget < 9*time.Minute || budget > 10*time.Minute {
		t.Fatalf("resume-agent budget = %s, want about 10 minutes", budget)
	}
}

// The first open of an imported chat runs the project's Setup, which may be
// allowed longer than the base resume budget.
func TestResumeAgentRouteExtendsBudgetForSetup(t *testing.T) {
	svc := &timeoutProbeSessionService{resumeBudget: make(chan time.Duration, 1), resumeSetup: 20 * time.Minute}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: 250 * time.Millisecond},
		discardLogger(),
		nil,
		APIDeps{Sessions: svc},
		ControlDeps{},
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/resume-agent", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("POST resume-agent status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if budget := <-svc.resumeBudget; budget < 29*time.Minute || budget > 30*time.Minute {
		t.Fatalf("resume-agent budget with 20 minutes of Setup = %s, want about 30 minutes", budget)
	}
}

type scanBudgetProbe struct{ scan, imported time.Duration }

func (p *scanBudgetProbe) Scan(ctx context.Context) (sessionimport.Preview, error) {
	deadline, _ := ctx.Deadline()
	p.scan = time.Until(deadline)
	return sessionimport.Preview{}, nil
}

func (p *scanBudgetProbe) Import(ctx context.Context, _ []string) ([]sessionimport.Result, error) {
	deadline, _ := ctx.Deadline()
	p.imported = time.Until(deadline)
	return []sessionimport.Result{}, nil
}

// Scanning and importing both read every chosen history, so both get the long budget.
func TestSessionImportUsesExtendedTimeout(t *testing.T) {
	probe := &scanBudgetProbe{}
	router := NewRouterWithControl(config.Config{RequestTimeout: 250 * time.Millisecond}, discardLogger(), nil, APIDeps{SessionImport: probe}, ControlDeps{})
	scan := httptest.NewRecorder()
	router.ServeHTTP(scan, httptest.NewRequest(http.MethodGet, "/api/v1/session-import", nil))
	imported := httptest.NewRecorder()
	router.ServeHTTP(imported, httptest.NewRequest(http.MethodPost, "/api/v1/session-import", strings.NewReader(`{"ids":["a"]}`)))
	if scan.Code != http.StatusOK || probe.scan < 9*time.Minute || imported.Code != http.StatusOK || probe.imported < 9*time.Minute {
		t.Fatalf("scan %d in %s, import %d in %s: %s", scan.Code, probe.scan, imported.Code, probe.imported, imported.Body)
	}
}
