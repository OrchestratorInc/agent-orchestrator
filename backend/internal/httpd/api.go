package httpd

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/clienttz"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/presence"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

type APIDeps struct {
	SessionForks             controllers.SessionForkService
	UsageFlush               func(context.Context)
	Awake                    controllers.AwakeService
	Agents                   controllers.AgentCatalog
	CodexAccounts            controllers.CodexAccountService
	AgentAccounts            controllers.AgentAccountService
	Projects                 projectsvc.Manager
	Sessions                 controllers.SessionService
	ArtifactVersions         controllers.ArtifactVersionService
	Automations              controllers.AutomationService
	DesktopWorkspaces        controllers.DesktopWorkspaceService
	Activity                 controllers.ActivityRecorder
	PRRefresh                controllers.PRRefresher
	NativeSessions           ports.AgentNativeSessionResolver
	UsageHooks               controllers.UsageHookRecorder
	UsageSummary             controllers.UsageSummaryService
	UsageReport              controllers.UsageReportService
	UsageLimitHistory        controllers.UsageLimitHistoryService
	UsageLimitSetting        controllers.UsageLimitHistorySetting
	UsagePrices              controllers.UsagePriceService
	SessionMemory            controllers.SessionMemoryService
	SessionSteps             controllers.SessionStepsReader
	PRs                      prsvc.ActionManager
	Reviews                  reviewsvc.Manager
	Notifications            controllers.NotificationService
	Reports                  controllers.ReportService
	NotificationStream       controllers.NotificationStream
	Push                     controllers.PushRegistry
	Import                   controllers.ImportService
	SessionImport            controllers.SessionImportService
	Directories              controllers.DirectoryBrowserService
	ShellTerminals           controllers.ShellTerminalService
	Cues                     controllers.CueService
	Conversations            controllers.ConversationService
	ConversationSearch       controllers.ConversationSearch
	Settings                 controllers.SettingsService
	DevImport                controllers.DevImportService
	CDC                      cdc.Source
	Events                   cdcSubscriber
	Telemetry                ports.EventSink
	TelemetryIdentity        ports.TelemetryIdentityStore
	LifetimeSessions         func(context.Context) (int64, error)
	Mobile                   *controllers.MobileController
	BrowserLive              *BrowserLiveHub
	Browser                  controllers.BrowserService
	BrowserChrome            controllers.BrowserChromeService
	Panes                    controllers.PaneService
	PaneTerminals            controllers.PaneTerminalService
	PreviewServer            controllers.ManagedPreviewServer
	SessionCapabilities      controllers.SessionCapabilityValidator
	ShellPreviewCapabilities controllers.ShellPreviewCapabilityValidator
	SystemChecks             controllers.SystemChecker
	HostID                   string
	Endpoints                controllers.EndpointSource
	Installer                controllers.Installer
	ComputerUse              controllers.ComputerUse
	AgentAuth                controllers.AgentAuthService
	GitHub                   controllers.GitHubPATService
	LinkPreview              controllers.LinkPreviewService
	SessionSearch            controllers.SessionSearchService

	Presence       *presence.Tracker
	ClientTimeZone *clienttz.Recorder

	DeviceRoster  controllers.DeviceRoster
	DeviceLive    controllers.LiveSet
	DeviceAccess  controllers.DeviceAccess
	AccessDevices *mobilebridge.AccessDevices
}

func normalizeAPIDeps(deps APIDeps, log *slog.Logger) APIDeps {
	if deps.DeviceLive == nil && deps.Presence != nil {
		deps.DeviceLive = deps.Presence
	}
	if deps.DeviceRoster != nil && deps.DeviceLive == nil {
		log.Warn("mobile device roster has no liveness tracker wired; every device will report offline")
	}
	return deps
}

type API struct {
	cfg           config.Config
	deps          APIDeps
	agents        *controllers.AgentsController
	codexAccounts *controllers.CodexAccountsController
	agentAccounts *controllers.AgentAccountsController
	projects      *controllers.ProjectsController
	sessions      *controllers.SessionsController
	sessionForks  *controllers.SessionForksController
	automations   *controllers.AutomationsController
	desktop       *controllers.DesktopWorkspaceController
	usage         *controllers.UsageController
	prs           *controllers.PRsController
	reviews       *controllers.ReviewsController
	notifications *controllers.NotificationsController
	reports       *controllers.ReportsController
	push          *controllers.PushController
	imports       *controllers.ImportController
	sessionImport *controllers.SessionImportController
	fs            *controllers.FSController
	shellTerms    *controllers.ShellTerminalsController
	cues          *controllers.CuesController
	conversations *controllers.ConversationsController
	settings      *controllers.SettingsController
	dev           *controllers.DevController
	browser       *controllers.BrowserController
	panes         *controllers.PanesController
	system        *controllers.SystemController
	identity      *controllers.IdentityController
	endpoints     *controllers.EndpointsController
	telemetryID   *controllers.TelemetryIdentityController
	systemInstall *controllers.SystemInstallController
	computerUse   *controllers.ComputerUseController
	agentAuth     *controllers.AgentAuthController
	linkPreview   *controllers.LinkPreviewController
	sessionSearch *controllers.SessionSearchController
	github        *controllers.GitHubController
	events        *EventsController
}

func NewAPI(cfg config.Config, deps APIDeps) *API {
	return newAPIWithLogger(cfg, deps, loggerOrDefault(nil))
}

func newAPIWithLogger(cfg config.Config, deps APIDeps, log *slog.Logger) *API {
	return &API{
		cfg:  cfg,
		deps: deps,
		agents: &controllers.AgentsController{
			Catalog: deps.Agents,
		},
		codexAccounts: &controllers.CodexAccountsController{Svc: deps.CodexAccounts},
		agentAccounts: newAgentAccountsController(deps),
		projects: &controllers.ProjectsController{
			Mgr: deps.Projects,
		},
		sessionForks: &controllers.SessionForksController{Svc: deps.SessionForks},
		sessions: &controllers.SessionsController{
			Svc:                      deps.Sessions,
			Projects:                 deps.Projects,
			Activity:                 deps.Activity,
			PRRefresh:                deps.PRRefresh,
			NativeSessions:           deps.NativeSessions,
			DataDir:                  cfg.DataDir,
			Usage:                    deps.UsageHooks,
			Attachments:              attachmentstore.New(cfg.DataDir),
			PreviewServer:            deps.PreviewServer,
			Capabilities:             deps.SessionCapabilities,
			ArtifactVersions:         deps.ArtifactVersions,
			ShellPreviewCapabilities: deps.ShellPreviewCapabilities,
			AgentBrowser:             agentBrowserURL(cfg.AgentBrowserURL),
			AgentBrowserToken:        cfg.AgentBrowserToken,
		},
		automations:   &controllers.AutomationsController{Svc: deps.Automations},
		desktop:       &controllers.DesktopWorkspaceController{Svc: deps.DesktopWorkspaces},
		usage:         &controllers.UsageController{Svc: deps.UsageSummary, Log: loggerOrDefault(log), Memory: deps.SessionMemory, Steps: deps.SessionSteps, Pressure: memoryPressure(deps.SessionMemory), Report: deps.UsageReport, LimitHistory: deps.UsageLimitHistory, LimitHistorySetting: deps.UsageLimitSetting, Prices: deps.UsagePrices},
		prs:           &controllers.PRsController{Svc: deps.PRs},
		reviews:       &controllers.ReviewsController{Svc: deps.Reviews},
		notifications: &controllers.NotificationsController{Svc: deps.Notifications, Stream: deps.NotificationStream},
		reports:       &controllers.ReportsController{Svc: deps.Reports},
		push:          &controllers.PushController{Registry: deps.Push, Devices: deps.AccessDevices},
		imports:       &controllers.ImportController{Svc: deps.Import},
		sessionImport: &controllers.SessionImportController{Svc: deps.SessionImport},
		fs:            &controllers.FSController{Svc: deps.Directories},
		shellTerms:    &controllers.ShellTerminalsController{Svc: deps.ShellTerminals},
		cues:          &controllers.CuesController{Svc: deps.Cues},
		conversations: &controllers.ConversationsController{Svc: deps.Conversations, Renders: attachmentstore.New(cfg.DataDir), Search: deps.ConversationSearch, UserMessages: userMessageRecorder(deps.Sessions)},
		settings:      &controllers.SettingsController{Svc: deps.Settings},
		dev:           &controllers.DevController{Import: deps.DevImport},
		browser:       &controllers.BrowserController{Svc: deps.Browser, Chrome: deps.BrowserChrome},
		panes:         &controllers.PanesController{Svc: deps.Panes, Terminals: deps.PaneTerminals, Sessions: deps.Sessions, Capabilities: deps.SessionCapabilities},
		system:        &controllers.SystemController{Checks: deps.SystemChecks},
		identity:      &controllers.IdentityController{HostID: deps.HostID},
		endpoints:     &controllers.EndpointsController{Source: deps.Endpoints},
		telemetryID:   &controllers.TelemetryIdentityController{Source: deps.TelemetryIdentity, LifetimeSessions: deps.LifetimeSessions},
		systemInstall: &controllers.SystemInstallController{Installer: deps.Installer},
		computerUse:   &controllers.ComputerUseController{Svc: deps.ComputerUse},
		agentAuth:     &controllers.AgentAuthController{Svc: deps.AgentAuth},
		linkPreview:   &controllers.LinkPreviewController{Svc: deps.LinkPreview},
		sessionSearch: &controllers.SessionSearchController{Svc: deps.SessionSearch},
		github:        &controllers.GitHubController{Svc: deps.GitHub},
		events:        &EventsController{Source: deps.CDC, Live: deps.Events},
	}
}

const attachmentUploadHeader = "X-AO-Attachment-Upload"

func setupRoute(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	switch chi.RouteContext(req.Context()).RoutePattern() {
	case "/api/v1/sessions", "/api/v1/orchestrators", "/api/v1/sessions/{sessionId}/restore",
		"/api/v1/sessions/{sessionId}/resume-agent",
		"/api/v1/sessions/{sessionId}/fork":
		return true
	}
	return false
}

var setupRequestBudget = 120 * time.Second

func serveWithSetupBudget(next http.Handler, w http.ResponseWriter, req *http.Request, budget time.Duration) {
	ctx, timedOut, release := ports.NewSpawnRequestContext(req.Context(), budget)
	defer release()
	next.ServeHTTP(w, req.WithContext(ctx))
	if timedOut() {
		w.WriteHeader(http.StatusGatewayTimeout)
	}
}

func (a *API) Register(root chi.Router) {
	timeout := a.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = config.DefaultRequestTimeout
	}
	root.Route("/api/v1", func(r chi.Router) {
		r.Get("/openapi.yaml", apispec.ServeYAML)

		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				ordinary := middleware.Timeout(timeout)(next)
				upload := middleware.Timeout(max(timeout, 10*time.Minute))(next)
				clone := middleware.Timeout(max(timeout, 30*time.Minute))(next)
				computerUse := middleware.Timeout(max(timeout, 2*time.Minute))(next)
				stackMerge := middleware.Timeout(max(timeout, 3*time.Minute))(next)
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern == "/api/v1/projects/clone" ||
						pattern == "/api/v1/projects/clone/prepare" || pattern == "/api/v1/projects/clone/prepare/stream" {
						clone.ServeHTTP(w, req)
						return
					}
					if chi.RouteContext(req.Context()).RoutePattern() == "/api/v1/computer-use/call" {
						computerUse.ServeHTTP(w, req)
						return
					}
					if chi.RouteContext(req.Context()).RoutePattern() == "/api/v1/prs/{id}/stack/merge" {
						stackMerge.ServeHTTP(w, req)
						return
					}
					if setupRoute(req) {
						budget := max(timeout, setupRequestBudget)
						if attachmentUploadRoute(req) ||
							chi.RouteContext(req.Context()).RoutePattern() == "/api/v1/sessions/{sessionId}/resume-agent" {
							budget = max(timeout, 10*time.Minute)
						}
						serveWithSetupBudget(next, w, req, budget)
						return
					}
					if attachmentUploadRoute(req) || sessionImportRoute(req) {
						upload.ServeHTTP(w, req)
						return
					}
					if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern == "/api/v1/sessions/{sessionId}/preview/app/*" ||
						pattern == "/api/v1/sessions/{sessionId}/agent-browser/*" {
						next.ServeHTTP(w, req)
						return
					}
					ordinary.ServeHTTP(w, req)
				})
			})
			r.Use(presenceMiddleware(a.deps.Presence))
			r.Use(clientTimeZoneMiddleware(a.deps.ClientTimeZone))
			a.agents.Register(r)
			a.codexAccounts.Register(r)
			a.agentAccounts.Register(r)
			a.projects.Register(r)
			a.sessions.Register(r)
			a.sessionForks.Register(r)
			a.automations.Register(r)
			a.desktop.Register(r)
			a.usage.Register(r)
			a.prs.Register(r)
			a.reviews.Register(r)
			a.notifications.Register(r)
			a.reports.Register(r)
			a.push.Register(r)
			a.imports.Register(r)
			a.sessionImport.Register(r)
			a.fs.Register(r)
			a.shellTerms.Register(r)
			a.cues.Register(r)
			a.conversations.Register(r)
			a.settings.Register(r)
			a.dev.Register(r)
			a.browser.Register(r)
			a.panes.Register(r)
			a.system.Register(r)
			a.identity.Register(r)
			a.endpoints.Register(r)
			a.telemetryID.Register(r)
			a.systemInstall.Register(r)
			a.computerUse.Register(r)
			a.agentAuth.Register(r)
			a.linkPreview.Register(r)
			a.sessionSearch.Register(r)
			a.github.Register(r)
		})
		a.notifications.RegisterStream(r)
		a.codexAccounts.RegisterStreams(r)
		a.sessions.RegisterStreams(r)
		a.events.Register(r)
	})
}

func sessionImportRoute(req *http.Request) bool {
	return chi.RouteContext(req.Context()).RoutePattern() == "/api/v1/session-import"
}

func attachmentUploadRoute(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	route := chi.RouteContext(req.Context()).RoutePattern()
	if route == "/api/v1/sessions/{sessionId}/attachments" {
		return true
	}
	if req.Header.Get(attachmentUploadHeader) != "1" {
		return false
	}
	switch route {
	case "/api/v1/sessions",
		"/api/v1/orchestrators/delegate",
		"/api/v1/sessions/{sessionId}/send",
		"/api/v1/sessions/{sessionId}/conversation/messages",
		"/api/v1/sessions/{sessionId}/provider-switch",
		"/api/v1/sessions/{sessionId}/conversation/steer",
		"/api/v1/sessions/{sessionId}/conversation/steer-or-send",
		"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/queue/edit",
		"/api/v1/reviews/{reviewId}/conversation/messages",
		"/api/v1/prs/{id}/comments":
		return true
	default:
		return false
	}
}

func notFoundJSON(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ROUTE_NOT_FOUND",
		r.Method+" "+r.URL.Path+" has no handler", nil)
}

func methodNotAllowedJSON(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "METHOD_NOT_ALLOWED",
		r.Method+" not allowed on "+r.URL.Path, nil)
}

func memoryPressure(svc controllers.SessionMemoryService) controllers.MemoryPressureReader {
	if p, ok := svc.(controllers.MemoryPressureReader); ok {
		return p
	}
	return nil
}

func newAgentAccountsController(deps APIDeps) *controllers.AgentAccountsController {
	c := &controllers.AgentAccountsController{Svc: deps.AgentAccounts}
	if mover, ok := deps.Sessions.(controllers.AgentAccountSessionMover); ok {
		c.Sessions = mover
	}
	return c
}

func agentBrowserURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if raw == "" || err != nil {
		return nil
	}
	return u
}

func userMessageRecorder(sessions any) controllers.UserMessageRecorder {
	recorder, _ := sessions.(controllers.UserMessageRecorder)
	return recorder
}
