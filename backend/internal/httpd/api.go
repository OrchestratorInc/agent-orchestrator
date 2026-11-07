package httpd

import (
	"context"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/presence"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/coordination"
	integrationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/integration"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualitygate"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualityreview"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/taskcommit"
)

// APIDeps bundles every service the API layer's controllers depend on.
type APIDeps struct {
	UsageFlush         func(context.Context)
	Awake              controllers.AwakeService
	Agents             controllers.AgentCatalog
	CodexAccounts      controllers.CodexAccountService
	AgentAccounts      controllers.AgentAccountService
	Projects           projectsvc.Manager
	Sessions           controllers.SessionService
	ArtifactVersions   controllers.ArtifactVersionService
	Automations        controllers.AutomationService
	DesktopWorkspaces  controllers.DesktopWorkspaceService
	Activity           controllers.ActivityRecorder
	NativeSessions     ports.AgentNativeSessionResolver
	UsageHooks         controllers.UsageHookRecorder
	UsageSummary       controllers.UsageSummaryService
	SessionMemory      controllers.SessionMemoryService
	SessionSteps       controllers.SessionStepsReader
	PRs                prsvc.ActionManager
	Reviews            reviewsvc.Manager
	Notifications      controllers.NotificationService
	Reports            controllers.ReportService
	NotificationStream controllers.NotificationStream
	Push               controllers.PushRegistry
	Import             controllers.ImportService
	Directories        controllers.DirectoryBrowserService
	ShellTerminals     controllers.ShellTerminalService
	Cues               controllers.CueService
	// Conversations is nil until a Chat driver is wired; the controller then
	// answers 501 rather than panicking, matching the other optional surfaces.
	Conversations controllers.ConversationService
	// ConversationSearch is the chat full-text index; nil answers 501.
	ConversationSearch controllers.ConversationSearch
	// Settings is the daemon-owned preference surface.
	Settings  controllers.SettingsService
	DevImport controllers.DevImportService
	CDC       cdc.Source
	Events    cdcSubscriber
	Telemetry ports.EventSink
	// TelemetryIdentity is the live telemetry identity, served to the phone by
	// GET /api/v1/telemetry/identity and updated by the desktop over loopback.
	TelemetryIdentity        ports.TelemetryIdentityStore
	LifetimeSessions         func(context.Context) (int64, error)
	Mobile                   *controllers.MobileController
	BrowserLive              *BrowserLiveHub
	Browser                  controllers.BrowserService
	BrowserChrome            controllers.BrowserChromeService
	PreviewServer            controllers.ManagedPreviewServer
	SessionCapabilities      controllers.SessionCapabilityValidator
	ShellPreviewCapabilities controllers.ShellPreviewCapabilityValidator
	SystemChecks             controllers.SystemChecker
	// HostID is this machine's stable, machine-bound identity, served by the
	// unauthenticated GET /api/v1/identity probe so a phone can confirm which
	// machine answered before presenting a credential.
	HostID string
	// Endpoints reports how this daemon can currently be reached, for the
	// phone's endpoint-refresh route.
	Endpoints controllers.EndpointSource
	Installer controllers.Installer
	// ComputerUse is the Cua Driver integration behind Settings and the computer-use tools.
	ComputerUse controllers.ComputerUse
	AgentAuth   controllers.AgentAuthService
	// GitHub is the local GitHub PAT + repos surface.
	GitHub controllers.GitHubPATService
	// LinkPreview unfurls external URLs for the renderer's hover cards; nil
	// leaves the route answering 501.
	LinkPreview controllers.LinkPreviewService

	// Workflows is the durable multi-repository orchestration core. Nil leaves
	// every workflow route mounted and answering 501, like the other optional
	// surfaces.
	Workflows                controllers.WorkflowService
	WorkflowQualityReader    controllers.QualityReader
	WorkflowArtifacts        *artifact.Service
	WorkflowGates            *qualitygate.Service
	WorkflowReviews          *qualityreview.Service
	WorkflowCommits          *taskcommit.Service
	WorkflowIntegrations     *integrationsvc.Service
	WorkflowCoordination     *coordination.Service
	Scheduler                controllers.SchedulerService
	Budget                   controllers.BudgetService
	WorkflowExecutionPermits controllers.ExecutionPermitReader
	WorkflowExecutionFence   func(context.Context) (domain.SchedulerFence, error)
	// WorkflowClock 供使用受控时钟的装配共享时间源；nil 保留真实 UTC 时间。
	WorkflowClock            func() time.Time
	WorkflowRetrySchedule    func(context.Context, domain.WorkflowID, domain.WorkflowTaskID, int64) error
	WorkflowMaterializer     controllers.TaskRequestMaterializer
	WorkflowMaterializations controllers.TaskRequestMaterializationReader
	WorkflowRecovery         controllers.WorkflowRecovery
	// WorkflowCapabilities, WorkflowBindings, WorkflowBinder, WorkflowInterventions,
	// WorkflowCollaboration, and WorkflowOwnership are the M2 slots. Each is independent so a
	// deployment can wire the M1 surface alone; a nil slot leaves only its own routes answering
	// 501. WorkflowBinder may additionally implement controllers.AttemptResumer and
	// controllers.AttemptLiveTarget; those capabilities are discovered per request, so wiring a
	// coordinator that lacks them is valid and answers 501 / omits the liveness fields.
	WorkflowCapabilities  controllers.CapabilityResolver
	WorkflowBindings      controllers.BindingReader
	WorkflowBinder        controllers.AttemptBinder
	WorkflowInterventions controllers.InterventionCoordinator
	WorkflowCollaboration controllers.CollaborationCoordinator
	// WorkflowOwnership resolves the durable Task/Project/Repo facts a bind must carry. A nil
	// slot leaves the bind route answering 501: binding with body-supplied facts is exactly the
	// defect the M2 acceptance found (B-03).
	WorkflowOwnership controllers.OwnershipResolver
	// WorkflowPrincipals derives the trusted caller for the M2 routes. A nil resolver refuses
	// every M2 request: an unwired identity check is not permission.
	WorkflowPrincipals *principal.Resolver
	// WorkflowAudit appends sensitive operator decisions to the audit trail. A nil recorder means
	// "no durable audit table exists yet" — the decisions still work — and a WIRED recorder that
	// fails makes the request fail rather than reporting an unrecorded decision.
	WorkflowAudit principal.AuditRecorder
	// OperatorApprovals 是本次 daemon 启动的一次性审批表，缺失时拒绝审批授权。
	OperatorApprovals *principal.ApprovalStore
	// OperatorConsent 是 OS 原生人工验证边界，缺失或验证失败都不确认审批。
	OperatorConsent principal.ConsentVerifier

	// Presence tracks which mobile devices are currently running the app.
	// Nil disables presence tracking (the roster then reports every device offline).
	Presence *presence.Tracker

	// DeviceRoster and DeviceLive back the desktop-only mobile device roster.
	DeviceRoster controllers.DeviceRoster
	DeviceLive   controllers.LiveSet
}

// normalizeAPIDeps closes the Presence/DeviceLive duplication trap structurally.
// Liveness enters APIDeps twice — Presence drives the heartbeat middleware that
// touches it, DeviceLive is what the device roster reads — and nothing enforces
// they stay the same tracker. If a future edit set Presence but left DeviceLive
// nil (or re-split them), the roster would silently and permanently report
// every device offline: no error, no log, no test failure short of a live
// phone. Defaulting DeviceLive to Presence here, at the one place APIDeps is
// consumed to build the API, makes that trap unreachable rather than merely
// currently avoided by careful call-site wiring.
//
// A nil Presence on its own is not an error: the roster must keep listing and
// managing devices with every device simply reporting offline (see
// MobileDevicesController.List's own nil-Presence fallback) — that decision
// stands. What IS a real mis-wiring is a live DeviceRoster with no liveness
// source at all after the fallback above; that gets exactly one startup
// warning, because a silent-forever-offline roster is precisely what a
// startup log is for.
func normalizeAPIDeps(deps APIDeps, log *slog.Logger) APIDeps {
	if deps.DeviceLive == nil && deps.Presence != nil {
		deps.DeviceLive = deps.Presence
	}
	// An unwired M2 principal resolver would make every M2 route refuse (a nil resolver refuses
	// everything), which is correct but says nothing about why. The fallback below keeps that
	// refusal AND makes it diagnosable: it installs a resolver with NO operator trust, so every
	// operator request is refused and every agent request is refused too, because there is no
	// verifier or lease prover to check one against.
	//
	// It is fail-closed by construction rather than by intent: there is no loopback shortcut here
	// any more. A loopback peer proves only "some local process" — every worker AO launches shares
	// that address — so a resolver that treated the address as the operator would let a worker
	// approve its own task request or resolve its own attempt. Production wiring must supply
	// WorkflowPrincipals built over a native consent approval table
	// (principal.ApprovalOperatorTrust + a per-boot ApprovalStore); see daemon/daemon.go.
	if deps.WorkflowPrincipals == nil && (deps.WorkflowBinder != nil || deps.WorkflowBindings != nil ||
		deps.WorkflowCollaboration != nil || deps.WorkflowInterventions != nil) {
		deps.WorkflowPrincipals = principal.NewResolver(nil, nil, nil)
		log.Warn("M2 workflow routes are wired without an identity resolver; " +
			"every M2 caller is refused until an operator trust is configured")
	}
	if deps.DeviceRoster != nil && deps.DeviceLive == nil {
		log.Warn("mobile device roster has no liveness tracker wired; every device will report offline")
	}
	return deps
}

// API owns one controller per resource and is the single Register call the
// router invokes to mount the /api/v1 surface.
type API struct {
	cfg           config.Config
	deps          APIDeps
	agents        *controllers.AgentsController
	codexAccounts *controllers.CodexAccountsController
	agentAccounts *controllers.AgentAccountsController
	projects      *controllers.ProjectsController
	sessions      *controllers.SessionsController
	automations   *controllers.AutomationsController
	desktop       *controllers.DesktopWorkspaceController
	usage         *controllers.UsageController
	prs           *controllers.PRsController
	reviews       *controllers.ReviewsController
	notifications *controllers.NotificationsController
	reports       *controllers.ReportsController
	push          *controllers.PushController
	imports       *controllers.ImportController
	fs            *controllers.FSController
	shellTerms    *controllers.ShellTerminalsController
	cues          *controllers.CuesController
	conversations *controllers.ConversationsController
	settings      *controllers.SettingsController
	dev           *controllers.DevController
	browser       *controllers.BrowserController
	system        *controllers.SystemController
	identity      *controllers.IdentityController
	endpoints     *controllers.EndpointsController
	telemetryID   *controllers.TelemetryIdentityController
	systemInstall *controllers.SystemInstallController
	computerUse   *controllers.ComputerUseController
	agentAuth     *controllers.AgentAuthController
	linkPreview   *controllers.LinkPreviewController
	github        *controllers.GitHubController
	workflows     *controllers.WorkflowsController
	scheduler     *controllers.SchedulerController
	quality       *controllers.WorkflowQualityController
	operator      *controllers.OperatorController
	events        *EventsController
}

// NewAPI constructs the API surface from its dependencies. cfg carries the
// per-request timeout so the REST group can apply it without re-reading the
// environment.
func NewAPI(cfg config.Config, deps APIDeps) *API {
	return newAPIWithLogger(cfg, deps, loggerOrDefault(nil))
}

// newAPIWithLogger carries the daemon logger to controllers that emit service
// errors, so their logs use the same configured handler as the rest of HTTP.
func newAPIWithLogger(cfg config.Config, deps APIDeps, log *slog.Logger) *API {
	api := &API{
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
		sessions: &controllers.SessionsController{
			Svc:                      deps.Sessions,
			Projects:                 deps.Projects,
			Activity:                 deps.Activity,
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
		usage:         &controllers.UsageController{Svc: deps.UsageSummary, Log: loggerOrDefault(log), Memory: deps.SessionMemory, Steps: deps.SessionSteps, Pressure: memoryPressure(deps.SessionMemory)},
		prs:           &controllers.PRsController{Svc: deps.PRs},
		reviews:       &controllers.ReviewsController{Svc: deps.Reviews},
		notifications: &controllers.NotificationsController{Svc: deps.Notifications, Stream: deps.NotificationStream},
		reports:       &controllers.ReportsController{Svc: deps.Reports},
		push:          &controllers.PushController{Registry: deps.Push},
		imports:       &controllers.ImportController{Svc: deps.Import},
		fs:            &controllers.FSController{Svc: deps.Directories},
		shellTerms:    &controllers.ShellTerminalsController{Svc: deps.ShellTerminals},
		cues:          &controllers.CuesController{Svc: deps.Cues},
		conversations: &controllers.ConversationsController{Svc: deps.Conversations, Renders: attachmentstore.New(cfg.DataDir), Search: deps.ConversationSearch, UserMessages: userMessageRecorder(deps.Sessions)},
		settings:      &controllers.SettingsController{Svc: deps.Settings},
		dev:           &controllers.DevController{Import: deps.DevImport},
		browser:       &controllers.BrowserController{Svc: deps.Browser, Chrome: deps.BrowserChrome},
		system:        &controllers.SystemController{Checks: deps.SystemChecks},
		identity:      &controllers.IdentityController{HostID: deps.HostID},
		endpoints:     &controllers.EndpointsController{Source: deps.Endpoints},
		telemetryID:   &controllers.TelemetryIdentityController{Source: deps.TelemetryIdentity, LifetimeSessions: deps.LifetimeSessions},
		systemInstall: &controllers.SystemInstallController{Installer: deps.Installer},
		computerUse:   &controllers.ComputerUseController{Svc: deps.ComputerUse},
		agentAuth:     &controllers.AgentAuthController{Svc: deps.AgentAuth},
		linkPreview:   &controllers.LinkPreviewController{Svc: deps.LinkPreview},
		github:        &controllers.GitHubController{Svc: deps.GitHub},
		workflows: &controllers.WorkflowsController{
			Svc:              deps.Workflows,
			ExecutionPermits: deps.WorkflowExecutionPermits,
			ExecutionFence:   deps.WorkflowExecutionFence,
			Now:              deps.WorkflowClock,
			RetrySchedule:    deps.WorkflowRetrySchedule,
			Materializer:     deps.WorkflowMaterializer,
			Materializations: deps.WorkflowMaterializations,
			Recovery:         deps.WorkflowRecovery,
			Capabilities:     deps.WorkflowCapabilities,
			Bindings:         deps.WorkflowBindings,
			Binder:           deps.WorkflowBinder,
			Interventions:    deps.WorkflowInterventions,
			Collaboration:    deps.WorkflowCollaboration,
			Ownership:        deps.WorkflowOwnership,
			Principals:       deps.WorkflowPrincipals,
			Audit:            deps.WorkflowAudit,
		},
		operator: &controllers.OperatorController{
			Approvals:  deps.OperatorApprovals,
			Consent:    deps.OperatorConsent,
			Principals: deps.WorkflowPrincipals,
		},
		events: &EventsController{Source: deps.CDC, Live: deps.Events},
	}
	api.scheduler = &controllers.SchedulerController{Svc: deps.Scheduler, Budget: deps.Budget, Workflows: api.workflows}
	api.quality = &controllers.WorkflowQualityController{Workflows: api.workflows, Scheduler: api.scheduler, Store: deps.WorkflowQualityReader, Artifacts: deps.WorkflowArtifacts, Gates: deps.WorkflowGates, Reviews: deps.WorkflowReviews, Commits: deps.WorkflowCommits, Integrations: deps.WorkflowIntegrations, Coordination: deps.WorkflowCoordination}
	return api
}

const attachmentUploadHeader = "X-AO-Attachment-Upload"

// setupRoute reports a request that may run the project's Setup steps.
func setupRoute(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	switch chi.RouteContext(req.Context()).RoutePattern() {
	case "/api/v1/sessions", "/api/v1/orchestrators", "/api/v1/sessions/{sessionId}/restore":
		return true
	}
	return false
}

// serveWithSetupBudget serves a Setup route under budget, like
// middleware.Timeout, except that the operation may extend the deadline
// (ports.ExtendSpawnRequestBudget) once it knows its Setup steps.
func serveWithSetupBudget(next http.Handler, w http.ResponseWriter, req *http.Request, budget time.Duration) {
	ctx, timedOut, release := ports.NewSpawnRequestContext(req.Context(), budget)
	defer release()
	next.ServeHTTP(w, req.WithContext(ctx))
	if timedOut() {
		w.WriteHeader(http.StatusGatewayTimeout)
	}
}

// Register mounts the bounded /api/v1 REST surface. Long-lived surfaces such
// as muxed terminal streams stay outside this timeout group.
func (a *API) Register(root chi.Router) {
	timeout := a.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = config.DefaultRequestTimeout
	}
	root.Route("/api/v1", func(r chi.Router) {
		// Serve the OpenAPI document from the same origin as the routes it describes.
		r.Get("/openapi.yaml", apispec.ServeYAML)

		r.Group(func(r chi.Router) {
			// Large base64 bodies can spend longer than the ordinary REST budget
			// uploading over a phone connection. Only attachment-bearing requests
			// opt in; ordinary calls to the same routes keep the configured timeout.
			r.Use(func(next http.Handler) http.Handler {
				ordinary := middleware.Timeout(timeout)(next)
				upload := middleware.Timeout(max(timeout, 10*time.Minute))(next)
				clone := middleware.Timeout(max(timeout, 30*time.Minute))(next)
				computerUse := middleware.Timeout(max(timeout, 2*time.Minute))(next)
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					// A clone downloads the whole repository inside the request,
					// which takes minutes for a large repository.
					if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern == "/api/v1/projects/clone" ||
						pattern == "/api/v1/projects/clone/prepare" {
						clone.ServeHTTP(w, req)
						return
					}
					// A run_steps batch may run for the driver's full 60 s before
					// its step report and closing window read, and an action is
					// followed by its own read: either outlasts the REST budget.
					if chi.RouteContext(req.Context()).RoutePattern() == "/api/v1/computer-use/call" {
						computerUse.ServeHTTP(w, req)
						return
					}
					// A spawn, an orchestrator start or a restore runs the project's
					// Setup inside the request, and a Setup step may take far longer
					// than one REST request: the operation extends this budget by its
					// Setup steps' limits once it knows its project.
					if setupRoute(req) {
						budget := timeout
						if attachmentUploadRoute(req) {
							budget = max(timeout, 10*time.Minute)
						}
						serveWithSetupBudget(next, w, req, budget)
						return
					}
					if attachmentUploadRoute(req) {
						upload.ServeHTTP(w, req)
						return
					}
					// A managed preview carries the dev server's HMR socket and event
					// streams, and the agent browser its stream socket: both stay open
					// as long as the page does.
					if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern == "/api/v1/sessions/{sessionId}/preview/app/*" ||
						pattern == "/api/v1/sessions/{sessionId}/agent-browser/*" {
						next.ServeHTTP(w, req)
						return
					}
					ordinary.ServeHTTP(w, req)
				})
			})
			r.Use(presenceMiddleware(a.deps.Presence))
			a.agents.Register(r)
			a.codexAccounts.Register(r)
			a.agentAccounts.Register(r)
			a.projects.Register(r)
			a.sessions.Register(r)
			a.automations.Register(r)
			a.desktop.Register(r)
			a.usage.Register(r)
			a.prs.Register(r)
			a.reviews.Register(r)
			a.notifications.Register(r)
			a.reports.Register(r)
			a.push.Register(r)
			a.imports.Register(r)
			a.fs.Register(r)
			a.shellTerms.Register(r)
			a.cues.Register(r)
			a.conversations.Register(r)
			a.settings.Register(r)
			a.dev.Register(r)
			a.browser.Register(r)
			a.system.Register(r)
			a.identity.Register(r)
			a.endpoints.Register(r)
			a.telemetryID.Register(r)
			a.systemInstall.Register(r)
			a.computerUse.Register(r)
			a.agentAuth.Register(r)
			a.linkPreview.Register(r)
			a.github.Register(r)
			a.workflows.Register(r)
			a.workflows.RegisterM2(r)
			a.quality.Register(r)
			a.scheduler.Register(r)
			a.operator.Register(r)
			// Sibling REST controllers plug in here.
		})
		// Long-lived streams intentionally bypass the REST timeout middleware.
		a.notifications.RegisterStream(r)
		a.codexAccounts.RegisterStreams(r)
		a.sessions.RegisterStreams(r)
		a.events.Register(r)
	})
}

func attachmentUploadRoute(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	route := chi.RouteContext(req.Context()).RoutePattern()
	// This route only accepts attachments, including from older clients.
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
		"/api/v1/reviews/{reviewId}/conversation/messages":
		return true
	default:
		return false
	}
}

// notFoundJSON returns the locked envelope for unmatched routes. Chi's default
// 404 is a text/plain body; the API surface must answer JSON so consumers can
// parse it uniformly.
func notFoundJSON(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ROUTE_NOT_FOUND",
		r.Method+" "+r.URL.Path+" has no handler", nil)
}

// methodNotAllowedJSON returns the locked envelope when a method probes a
// known path without a matching verb (e.g. PUT /projects/{id} after we drop
// the legacy PUT alias).
func methodNotAllowedJSON(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "METHOD_NOT_ALLOWED",
		r.Method+" not allowed on "+r.URL.Path, nil)
}

// memoryPressure is the memory service's cheap pressure read when it offers
// one; nil leaves the route at 501.
func memoryPressure(svc controllers.SessionMemoryService) controllers.MemoryPressureReader {
	if p, ok := svc.(controllers.MemoryPressureReader); ok {
		return p
	}
	return nil
}

// newAgentAccountsController moves sessions through the session service when
// it can, so a move maps errors like every other session command.
func newAgentAccountsController(deps APIDeps) *controllers.AgentAccountsController {
	c := &controllers.AgentAccountsController{Svc: deps.AgentAccounts}
	if mover, ok := deps.Sessions.(controllers.AgentAccountSessionMover); ok {
		c.Sessions = mover
	}
	return c
}

// agentBrowserURL is the host's agent browser, or nil when it has none.
// config.Load already accepted only a loopback http URL.
func agentBrowserURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if raw == "" || err != nil {
		return nil
	}
	return u
}

// userMessageRecorder reuses the session service, which owns session telemetry,
// to report human chat sends. Nil when the service cannot record.
func userMessageRecorder(sessions any) controllers.UserMessageRecorder {
	recorder, _ := sessions.(controllers.UserMessageRecorder)
	return recorder
}
