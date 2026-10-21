package httpd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type timeoutProbeSessionService struct {
	controllers.SessionService
	genericBudget chan time.Duration
	switchBudget  chan time.Duration
	uploadBudget  chan time.Duration
	sendBudget    chan time.Duration
	previewBudget chan time.Duration
	spawnSetup    time.Duration
	spawnRuns     time.Duration
	spawnLive     chan bool
}

// Spawn extends the request's budget by spawnSetup (none when negative), runs
// past the ordinary budget, and reports whether its context was still live.
func (s *timeoutProbeSessionService) Spawn(ctx context.Context, _ ports.SpawnConfig) (domain.Session, int, int, error) {
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

func (s *timeoutProbeSessionService) List(ctx context.Context, _ sessionsvc.ListFilter) ([]domain.Session, error) {
	s.genericBudget <- remainingRequestBudget(ctx)
	return nil, nil
}

func (s *timeoutProbeSessionService) SwitchAgent(
	ctx context.Context,
	id domain.SessionID,
	in sessionsvc.SwitchAgentInput,
) (domain.AgentSwitch, error) {
	s.switchBudget <- remainingRequestBudget(ctx)
	now := time.Now().UTC()
	return domain.AgentSwitch{
		ID:            "switch-timeout-probe",
		SessionID:     id,
		TargetHarness: in.TargetHarness,
		State:         domain.AgentSwitchPreparingHandoff,
		RequestedAt:   now,
		UpdatedAt:     now,
	}, nil
}

func (s *timeoutProbeSessionService) StageAttachments(ctx context.Context, _ domain.SessionID, _ []ports.SpawnAttachment) ([]string, error) {
	s.uploadBudget <- remainingRequestBudget(ctx)
	return []string{"attachment.txt"}, nil
}

func (s *timeoutProbeSessionService) Send(ctx context.Context, _ domain.SessionID, _ string, _ *ports.SpawnAttachment) error {
	s.sendBudget <- remainingRequestBudget(ctx)
	return nil
}

func remainingRequestBudget(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return time.Until(deadline)
}

func TestSwitchAgentRouteUsesOrdinaryTimeoutAndReturnsAccepted(t *testing.T) {
	const configuredTimeout = 250 * time.Millisecond
	svc := &timeoutProbeSessionService{
		genericBudget: make(chan time.Duration, 1),
		switchBudget:  make(chan time.Duration, 1),
	}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: configuredTimeout},
		discardLogger(),
		nil,
		APIDeps{Sessions: svc},
		ControlDeps{},
	)

	genericResponse := httptest.NewRecorder()
	router.ServeHTTP(genericResponse, httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))
	if genericResponse.Code != http.StatusOK {
		t.Fatalf("GET sessions status = %d, want 200", genericResponse.Code)
	}
	genericBudget := <-svc.genericBudget

	switchRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/ao-1/switch-agent",
		bytes.NewBufferString(`{"targetHarness":"codex"}`),
	)
	switchRequest.Header.Set("Content-Type", "application/json")
	switchRequest.Header.Set(attachmentUploadHeader, "1")
	switchResponse := httptest.NewRecorder()
	router.ServeHTTP(switchResponse, switchRequest)
	if switchResponse.Code != http.StatusAccepted {
		t.Fatalf("POST switch-agent status = %d, want 202; body=%s", switchResponse.Code, switchResponse.Body.String())
	}
	switchBudget := <-svc.switchBudget
	for name, budget := range map[string]time.Duration{"generic": genericBudget, "switch": switchBudget} {
		if budget <= 0 || budget > configuredTimeout {
			t.Fatalf("%s request budget = %s, want ordinary timeout no greater than %s", name, budget, configuredTimeout)
		}
	}
	if delta := genericBudget - switchBudget; delta < -50*time.Millisecond || delta > 50*time.Millisecond {
		t.Fatalf("generic and switch budgets differ by %s: generic=%s switch=%s", delta, genericBudget, switchBudget)
	}
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

// A spawn runs the project's Setup inside the request; a Setup step longer than
// one REST request used to die at the flat deadline ("signal: killed"). The
// request keeps the ordinary budget until the spawn extends it.
func TestSpawnRouteBudgetCoversSetupOnlyOnceExtended(t *testing.T) {
	const configuredTimeout = 200 * time.Millisecond
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

func TestSetupRoutesAreTheOnesThatRunSetup(t *testing.T) {
	router := chi.NewRouter()
	seen := map[string]bool{}
	record := func(w http.ResponseWriter, req *http.Request) { seen[req.Method+" "+req.URL.Path] = setupRoute(req) }
	router.Post("/api/v1/sessions", record)
	router.Post("/api/v1/orchestrators", record)
	router.Post("/api/v1/sessions/{sessionId}/restore", record)
	router.Post("/api/v1/sessions/{sessionId}/send", record)
	router.Get("/api/v1/sessions", record)
	for _, call := range [][2]string{{"POST", "/api/v1/sessions"}, {"POST", "/api/v1/orchestrators"}, {"POST", "/api/v1/sessions/s1/restore"}, {"POST", "/api/v1/sessions/s1/send"}, {"GET", "/api/v1/sessions"}} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(call[0], call[1], nil))
	}
	want := map[string]bool{"POST /api/v1/sessions": true, "POST /api/v1/orchestrators": true, "POST /api/v1/sessions/s1/restore": true, "POST /api/v1/sessions/s1/send": false, "GET /api/v1/sessions": false}
	for call, setup := range want {
		if seen[call] != setup {
			t.Errorf("setupRoute(%s) = %v, want %v", call, seen[call], setup)
		}
	}
}
