package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

var (
	modelCatalogLoadTimeout = 30 * time.Second
	// Retained for compatibility with focused tests that construct an obviously
	// old timestamp; production freshness is calendar-day based below.
	modelCatalogTrustWindow = 6 * time.Hour
	// How long a cached catalog is trusted before AO asks a cache-first client to
	// revalidate in the background. Long, because rediscovery runs an agent CLI:
	// this covers drift a fingerprint cannot see, not routine correctness.
	modelCatalogMonitorInterval = time.Minute
	modelCatalogWakeThreshold   = 3 * time.Minute
	modelCatalogMaxRetries      = 3
	modelCatalogRetryDelays     = [...]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
)

// catalogNeedsRevalidation uses the machine's calendar date, not an elapsed
// duration. A catalog remains fresh through the user's local day and becomes
// due after midnight, including after timezone changes or sleep/wake.
func catalogNeedsRevalidation(lastSuccess, now time.Time) bool {
	if lastSuccess.IsZero() {
		return true
	}
	y, m, d := now.Date()
	ly, lm, ld := lastSuccess.In(now.Location()).Date()
	return y != ly || m != lm || d != ld
}

func catalogClockDiscontinuity(previous, now time.Time, previousZone string, previousOffset int) bool {
	zone, offset := now.Zone()
	return catalogNeedsRevalidation(previous, now) || now.Sub(previous) > modelCatalogWakeThreshold || zone != previousZone || offset != previousOffset || now.Before(previous)
}

type modelLoadMode uint8

const (
	modelLoadCached modelLoadMode = iota
	modelLoadRevalidate
	modelLoadRefresh
)

type modelCatalogCall struct {
	done       chan struct{}
	catalog    ports.AgentModelCatalog
	err        error
	generation int64
}

// Service owns normalized harness readiness and the unchanged model catalog.
// Consumers share coordinator checks instead of probing adapters directly.
type Service struct {
	agents    []agentregistry.HarnessAgent
	readiness *readinessCoordinator
	// savedDefaultSignIn reports the sign-in of the saved account new sessions
	// of a harness start on; handled is false when they start on this computer.
	savedDefaultSignIn func(ctx context.Context, harness domain.AgentHarness) (domain.AgentAccountIdentityCheck, bool)
	cache              ports.AgentModelCatalogCache
	discoverer         ports.AgentModelDiscoverer
	modelDiscoveryDir  string
	projects           ProjectLookup
	sessions           SessionUsageLookup
	providerEntries    func(ctx context.Context) []agentcreds.GatewayEntry
	resolverMu         map[string]*sync.Mutex
	modelCallMu        sync.Mutex
	modelCalls         map[string]*modelCatalogCall
	modelGeneration    map[string]int64
	discoverySlots     chan struct{}
	// inputChecks tracks background credential-input checks for cached
	// catalogs, so a cache read never waits on them.
	inputCheckMu  sync.Mutex
	inputChecks   map[string]catalogInputCheck
	ctx           context.Context
	now           func() time.Time
	codexAccounts *codexAccountManager
	codexSwitches *codexAccountSwitchCoordinator
	logger        *slog.Logger
}

// Deps contains optional durable dependencies for the agent catalog service.
type Deps struct {
	Cache             ports.AgentModelCatalogCache
	Discoverer        ports.AgentModelDiscoverer
	ModelDiscoveryDir string
	Projects          ProjectLookup
	Sessions          SessionUsageLookup
	// ProviderEntries supplies the stored gateway entries a role's provider pin
	// resolves against during model discovery. Nil means none are configured.
	ProviderEntries        func(ctx context.Context) []agentcreds.GatewayEntry
	Context                context.Context
	Logger                 *slog.Logger
	CodexAccountRoot       string
	CodexPendingRoot       string
	CodexSwitchStagingRoot string
	CodexGlobalHome        string
	CodexAccounts          ports.CodexAccountClientFactory
	CodexAccountSwitches   ports.CodexAccountSwitchStore
	CodexOperationGate     ports.CodexOperationGate
	// Clock overrides time.Now for deterministic account-bootstrap retry tests.
	Clock func() time.Time
}

// ProjectLookup resolves the launch context used by project-scoped model
// discovery.
type ProjectLookup interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// SessionUsageLookup provides durable session facts used to rank agent choices.
// The SQLite store satisfies this narrow read boundary.
type SessionUsageLookup interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

// New returns an agent service backed by the daemon's shipped adapter registry.
func New() *Service {
	return NewWithDeps(Deps{})
}

// NewWithDeps returns the production service with in-memory readiness and a
// durable model-catalog cache.
func NewWithDeps(deps Deps) *Service {
	agents := agentregistry.Harnessed()
	svc := newService(agents, deps.Cache, deps.Projects, deps.Discoverer)
	svc.modelDiscoveryDir = deps.ModelDiscoveryDir
	svc.providerEntries = deps.ProviderEntries
	if deps.Logger != nil {
		svc.logger = deps.Logger
	}
	if deps.CodexAccountRoot != "" && deps.CodexGlobalHome != "" {
		svc.codexAccounts = newCodexAccountManager(deps.Context, deps.CodexAccountRoot, deps.CodexPendingRoot, deps.CodexSwitchStagingRoot, deps.CodexGlobalHome, deps.CodexAccounts, deps.Logger, deps.CodexOperationGate)
		if deps.Clock != nil {
			svc.codexAccounts.now = deps.Clock
		}
	}
	svc.readiness = newReadinessCoordinator(readinessCoordinatorConfig{
		Agents: agents, Factory: agentregistry.Harnessed, Context: deps.Context, Logger: deps.Logger,
		AuthenticationCheck: svc.authenticationWithAccount,
		// A catalog built while the agent was signed out carries that failure
		// (a rejected-credential warning or bare fallback aliases). A detected
		// login must rediscover it without anyone pressing refresh.
		OnAuthenticationRecovered: svc.InvalidateModelCatalogs,
	})
	if svc.codexAccounts != nil {
		svc.codexAccounts.onAuthenticationChanged = func() {
			svc.readiness.Invalidate(string(domain.HarnessCodex), readinessInvalidateAuthentication)
		}
	}
	if svc.codexAccounts != nil && deps.CodexAccountSwitches != nil && deps.CodexOperationGate != nil {
		svc.codexSwitches = newCodexAccountSwitchCoordinator(
			deps.Context, svc, deps.CodexAccountSwitches, deps.CodexOperationGate,
			deps.Clock, svc.PublishCodexAccounts,
		)
	}
	svc.sessions = deps.Sessions
	if deps.Context != nil {
		svc.ctx = deps.Context
	}
	if deps.Clock != nil {
		svc.now = deps.Clock
	}
	return svc
}

// NewWithAgents returns an agent service over a caller-provided adapter slice.
// It is used by focused tests.
func NewWithAgents(agents []agentregistry.HarnessAgent) *Service {
	svc := newService(agents, nil, nil, nil)
	svc.readiness = newReadinessCoordinator(readinessCoordinatorConfig{Agents: agents})
	return svc
}

func newService(agents []agentregistry.HarnessAgent, cache ports.AgentModelCatalogCache, projects ProjectLookup, discoverer ports.AgentModelDiscoverer) *Service {
	resolverMu := make(map[string]*sync.Mutex, len(agents))
	for _, item := range agents {
		resolverMu[string(item.Harness)] = &sync.Mutex{}
	}
	return &Service{agents: agents, readiness: newReadinessCoordinator(readinessCoordinatorConfig{Agents: agents}), cache: cache, discoverer: discoverer, projects: projects, resolverMu: resolverMu, modelCalls: map[string]*modelCatalogCall{}, modelGeneration: map[string]int64{}, discoverySlots: make(chan struct{}, 2), ctx: context.Background(), now: time.Now, logger: slog.Default()}
}

// WarmModelCatalogs starts the bounded cache scheduler. Readiness is never held
// up by model discovery; at most two adapter discoveries run at once.
func (s *Service) WarmModelCatalogs(ctx context.Context) {
	if s.cache == nil || s.discoverer == nil {
		return
	}
	go func() {
		s.prefetchModelCatalogs(ctx, false)
		s.monitorModelCatalogFreshness(ctx)
	}()
}

func (s *Service) prefetchModelCatalogs(ctx context.Context, force bool) {
	scopeCache, ok := s.cache.(ports.AgentModelCatalogScopeCache)
	var records []ports.CachedAgentModelCatalog
	var err error
	if ok {
		records, err = scopeCache.ListAgentModelCatalogs(ctx)
	} else {
		for _, item := range s.agents {
			rows, listErr := s.cache.ListAgentModelCatalogsByAgent(ctx, string(item.Harness))
			if listErr != nil {
				err = listErr
				break
			}
			records = append(records, rows...)
		}
	}
	if err != nil || ctx.Err() != nil {
		return
	}
	readiness, err := s.readiness.EnsureInstallation(ctx, nil, domain.AgentReadinessPurposeDisplay)
	if err != nil {
		return
	}
	installed := make(map[string]struct{}, len(readiness))
	for _, item := range readiness {
		if item.Installation.State == domain.AgentInstallationInstalled {
			installed[item.ID] = struct{}{}
		}
	}
	// Retain the newest row for every agent/project scope and seed one global job
	// for installed adapters that have never been discovered.
	latest := make(map[string]ports.CachedAgentModelCatalog, len(records)+len(installed))
	for _, record := range records {
		// Older builds persisted a separate Settings catalog. It is no longer used.
		if record.ProjectID == "@browse:all" {
			continue
		}
		key := record.AgentID + "\x00" + record.ProjectID
		current, exists := latest[key]
		if !exists || record.FetchedAt.After(current.FetchedAt) {
			latest[key] = record
		}
	}
	records = records[:0]
	for agentID := range installed {
		key := agentID + "\x00"
		if _, exists := latest[key]; !exists {
			latest[key] = ports.CachedAgentModelCatalog{AgentID: agentID}
		}
	}
	for _, record := range latest {
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].ProjectID == records[j].ProjectID {
			return records[i].AgentID < records[j].AgentID
		}
		return records[i].ProjectID < records[j].ProjectID
	})
	jobs := make(chan ports.CachedAgentModelCatalog, len(records))
	for _, record := range records {
		if _, eligible := installed[record.AgentID]; !eligible {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if force || catalogNeedsRevalidation(record.LastSuccessAt, s.now()) || record.RefreshState != "idle" {
			jobs <- record
		}
	}
	close(jobs)
	var workers sync.WaitGroup
	for range cap(s.discoverySlots) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for record := range jobs {
				if ctx.Err() != nil {
					return
				}
				_, _ = s.revalidateModels(ctx, record.AgentID, record.ProjectID)
			}
		}()
	}
	workers.Wait()
}

func (s *Service) monitorModelCatalogFreshness(ctx context.Context) {
	ticker := time.NewTicker(modelCatalogMonitorInterval)
	defer ticker.Stop()
	last := s.now()
	lastZone, lastOffset := last.Zone()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := s.now()
			zone, offset := now.Zone()
			if catalogClockDiscontinuity(last, now, lastZone, lastOffset) {
				s.prefetchModelCatalogs(ctx, true)
			}
			last, lastZone, lastOffset = now, zone, offset
		}
	}
}

// Models returns one normalized model catalog, stamped with when this user last
// ran each model. Cached values survive daemon restarts; refresh forces a new
// documented CLI discovery attempt. Discovery failures degrade to the last
// cached catalog or a custom model input.
func (s *Service) Models(ctx context.Context, agentID, projectID string, refresh bool) (ports.AgentModelCatalog, error) {
	catalog, err := s.modelCatalog(ctx, agentID, projectID, refresh)
	if err != nil {
		return catalog, err
	}
	// Usage is keyed by the caller's real project, not the credential scope the
	// catalog lookup may substitute for it.
	return s.withModelUsage(ctx, agentID, projectID, catalog), nil
}

// ModelCatalog returns the persisted catalog without session-history enrichment.
// Settings reads the same harness-reported catalog as task pickers; recency
// belongs to task pickers. Cached reads never wait for CLI discovery or input
// checks, which run in the background. A scope with no persisted catalog still
// needs its first discovery.
func (s *Service) ModelCatalog(ctx context.Context, agentID, projectID string, refresh bool) (ports.AgentModelCatalog, error) {
	return s.modelCatalog(ctx, agentID, projectID, refresh)
}

func (s *Service) modelCatalog(ctx context.Context, agentID, projectID string, refresh bool) (ports.AgentModelCatalog, error) {
	if s.discoverer == nil {
		return ports.AgentModelCatalog{}, apierr.Internal("MODEL_DISCOVERY_UNAVAILABLE", "Model discovery is unavailable")
	}
	var err error
	projectID, err = s.modelCatalogScope(ctx, projectID)
	if err != nil {
		return ports.AgentModelCatalog{}, err
	}
	if !refresh {
		if _, ok := s.agent(agentID); !ok {
			return ports.AgentModelCatalog{}, apierr.NotFound("AGENT_NOT_FOUND", "Unknown agent adapter")
		}
		cached, ok, err := s.cachedCatalog(ctx, agentID, projectID)
		if err != nil {
			return ports.AgentModelCatalog{}, err
		}
		if ok {
			// Claude provider model IDs are credential-scoped. The cache is served
			// at once and its local discovery inputs are checked in the background;
			// while that runs the catalog reports itself refreshing, so open views
			// re-read it and pick up the rediscovered catalog when inputs changed.
			inputsChecking := agentID == "claude-code" && s.checkCatalogInputsInBackground(agentID, projectID, cached.BinaryVersion)
			cached.Catalog = applyCustomModelEntryPolicy(cached.Catalog, s.discoverer.Manual(agentID))
			due := catalogNeedsRevalidation(catalogLastSuccess(cached.Catalog), s.now())
			needsRecovery := cached.RefreshState == "refreshing"
			retriesExhausted := modelCatalogRetriesExhausted(cached)
			cached.Catalog.RefreshRecommended = !retriesExhausted && (due || needsRecovery || cached.RefreshState == "error" || cached.RefreshState == "queued")
			if !retriesExhausted && (due || needsRecovery) && (cached.RetryAt.IsZero() || !s.now().Before(cached.RetryAt)) {
				go func() { _, _ = s.revalidateModels(s.ctx, agentID, projectID) }()
			} else if retriesExhausted {
				go s.revalidateChangedInputs(agentID, projectID, cached.BinaryVersion)
			} else if !due && agentID != "claude-code" {
				time.AfterFunc(10*time.Millisecond, func() {
					s.revalidateChangedInputs(agentID, projectID, cached.BinaryVersion)
				})
			}
			if inputsChecking && cached.Catalog.RefreshState == "" {
				cached.Catalog.RefreshState = "refreshing"
			}
			return cached.Catalog, nil
		}
	}
	mode := modelLoadCached
	if refresh {
		mode = modelLoadRefresh
	}
	return s.coalesceModelLoad(ctx, agentID, projectID, mode)
}

// rediscoverDriftedProjectCatalogs rediscovers project catalogs whose models
// differ from a device-global catalog whose models just changed. A provider
// connected inside the harness need not change the discovery fingerprint, so
// project pickers would otherwise keep the old list until the next day.
func (s *Service) rediscoverDriftedProjectCatalogs(agentID string, global []ports.AgentModelInfo) {
	if s.cache == nil || s.projects == nil {
		return
	}
	want := modelIDSet(global)
	go func() {
		records, err := s.cache.ListAgentModelCatalogsByAgent(s.ctx, agentID)
		if err != nil {
			return
		}
		for _, record := range records {
			if s.ctx.Err() != nil {
				return
			}
			if record.ProjectID == "" || record.ProjectID == "@browse:all" {
				continue
			}
			if _, ok := credentialTypeFromScope(record.ProjectID); ok {
				continue
			}
			// A missing project's scope falls back to the global one, and an
			// archived project has no picker to refresh.
			project, ok, err := s.projects.GetProject(s.ctx, record.ProjectID)
			if err != nil || !ok || !project.ArchivedAt.IsZero() {
				continue
			}
			var catalog ports.AgentModelCatalog
			if json.Unmarshal([]byte(record.CatalogJSON), &catalog) != nil || maps.Equal(modelIDSet(catalog.Models), want) {
				continue
			}
			s.requeueCatalog(record.ProjectID, catalog)
			_, _ = s.revalidateModels(s.ctx, agentID, record.ProjectID)
		}
	}()
}

func modelIDSet(models []ports.AgentModelInfo) map[string]struct{} {
	ids := make(map[string]struct{}, len(models))
	for _, model := range models {
		ids[model.ID] = struct{}{}
	}
	return ids
}

// catalogInputCheckTTL is how long a matching input check stands for a cached
// catalog. Logins and logouts invalidate catalogs directly, so this only bounds
// how long an out-of-band credential change goes unnoticed.
const catalogInputCheckTTL = 2 * time.Minute

type catalogInputCheck struct {
	running     bool
	checkedAt   time.Time
	fingerprint string
}

// checkCatalogInputsInBackground starts, at most once at a time and once per
// catalogInputCheckTTL, a check of whether a cached catalog's discovery inputs
// changed, rediscovering the catalog when they did. It reports whether a check
// is in flight.
func (s *Service) checkCatalogInputsInBackground(agentID, projectID, fingerprint string) bool {
	key := agentID + "\x00" + projectID
	s.inputCheckMu.Lock()
	if s.inputChecks == nil {
		s.inputChecks = make(map[string]catalogInputCheck)
	}
	check := s.inputChecks[key]
	if check.running {
		s.inputCheckMu.Unlock()
		return true
	}
	if check.fingerprint == fingerprint && !check.checkedAt.IsZero() && s.now().Sub(check.checkedAt) < catalogInputCheckTTL {
		s.inputCheckMu.Unlock()
		return false
	}
	s.inputChecks[key] = catalogInputCheck{running: true}
	s.inputCheckMu.Unlock()
	go func() {
		current := fingerprint
		if s.ctx.Err() == nil && s.modelCatalogInputsChanged(s.ctx, agentID, projectID, fingerprint) {
			_, _ = s.revalidateModels(s.ctx, agentID, projectID)
			if cached, ok, err := s.cachedCatalog(s.ctx, agentID, projectID); err == nil && ok {
				current = cached.BinaryVersion
			}
		}
		s.inputCheckMu.Lock()
		s.inputChecks[key] = catalogInputCheck{checkedAt: s.now(), fingerprint: current}
		s.inputCheckMu.Unlock()
	}()
	return true
}

func (s *Service) revalidateChangedInputs(agentID, projectID, cachedFingerprint string) {
	if s.ctx.Err() != nil {
		return
	}
	if s.modelCatalogInputsChanged(s.ctx, agentID, projectID, cachedFingerprint) {
		_, _ = s.revalidateModels(s.ctx, agentID, projectID)
	}
}

func (s *Service) modelCatalogInputsChanged(ctx context.Context, agentID, projectID, cachedFingerprint string) bool {
	item, ok := s.agent(agentID)
	if !ok {
		return false
	}
	var binary string
	if resolver, ok := item.Agent.(ports.AgentBinaryResolver); ok {
		lock := s.resolverMu[agentID]
		lock.Lock()
		resolved, err := resolver.ResolveBinary(ctx)
		lock.Unlock()
		if err != nil {
			return false
		}
		binary = resolved
	}
	request, err := s.modelDiscoveryRequest(ctx, agentID, projectID, binary)
	if err != nil {
		return false
	}
	return s.discoverer.CatalogFingerprint(ctx, request) != cachedFingerprint
}

// credentialScopePrefix marks a model-catalog scope that is not a project but a
// cloud credential kind. A cloud session has no local project, yet its picker
// must show the models the pushed credential can run; encoding the credential
// type into the scope caches each provider's catalog separately with no schema
// change. '@' and ':' cannot appear in a real project ID (projectIDPattern), so
// the project and credential namespaces never collide.
const credentialScopePrefix = "@cred:"

// credentialTypeFromScope returns the credential type a scope carries, if any.
func credentialTypeFromScope(scope string) (string, bool) {
	rest, ok := strings.CutPrefix(scope, credentialScopePrefix)
	if !ok || strings.TrimSpace(rest) == "" {
		return "", false
	}
	return rest, true
}

// roleScopeMarker marks the role suffix of a model-catalog scope:
// "<projectID>@role:<role>". A role-scoped catalog resolves the role's
// provider pin into the discovery env, so a pinned role's picker shows the
// pinned provider's models. '@' and ':' cannot appear in a real project ID,
// so the namespaces never collide (same argument as credentialScopePrefix).
const roleScopeMarker = "@role:"

// roleScopeParts splits a role-scoped catalog scope. ok is false for plain
// project (or credential) scopes.
func roleScopeParts(scope string) (projectID, role string, ok bool) {
	project, rolePart, has := strings.Cut(scope, roleScopeMarker)
	if !has || project == "" || rolePart == "" || strings.Contains(rolePart, roleScopeMarker) {
		return "", "", false
	}
	return project, rolePart, true
}

func (s *Service) modelCatalogScope(ctx context.Context, projectID string) (string, error) {
	// A credential scope has no backing project; keep it verbatim so its catalog
	// caches under its own key instead of collapsing to the device-global scope.
	if _, ok := credentialTypeFromScope(projectID); ok {
		return projectID, nil
	}
	// A role scope backs onto its project; keep it verbatim for the same
	// per-role cache-key reason once the project itself is real.
	scope := projectID
	if id, _, ok := roleScopeParts(projectID); ok {
		projectID = id
	}
	if strings.TrimSpace(projectID) == "" || s.projects == nil {
		return "", nil
	}
	if _, ok, err := s.projects.GetProject(ctx, projectID); err != nil {
		return "", fmt.Errorf("resolve model catalog project %s: %w", projectID, err)
	} else if !ok {
		return "", nil
	}
	return scope, nil
}

func (s *Service) modelDiscoveryRequest(ctx context.Context, agentID, projectID, binary string) (ports.AgentModelDiscoveryRequest, error) {
	request := ports.AgentModelDiscoveryRequest{AgentID: agentID, Binary: binary}
	// Credential-scoped discovery reflects a cloud session's pushed credential,
	// not a local project: leave WorkingDir/Env empty and let the adapter unlock
	// that provider's models from the credential type alone.
	if credentialType, ok := credentialTypeFromScope(projectID); ok {
		request.CredentialType = credentialType
		return request, nil
	}
	// A role-scoped scope backs onto its project and folds the role's provider
	// pin into the discovery env, mirroring what the launch path applies.
	if id, role, ok := roleScopeParts(projectID); ok && s.projects != nil {
		project, found, err := s.projects.GetProject(ctx, id)
		if err != nil {
			return ports.AgentModelDiscoveryRequest{}, fmt.Errorf("resolve model discovery project %s: %w", id, err)
		}
		if !found {
			return s.globalModelDiscoveryRequest(request)
		}
		return s.rolePinnedDiscoveryRequest(ctx, request, project, role), nil
	}
	if strings.TrimSpace(projectID) == "" || s.projects == nil {
		return s.globalModelDiscoveryRequest(request)
	}
	project, ok, err := s.projects.GetProject(ctx, projectID)
	if err != nil {
		return ports.AgentModelDiscoveryRequest{}, fmt.Errorf("resolve model discovery project %s: %w", projectID, err)
	}
	if !ok {
		return s.globalModelDiscoveryRequest(request)
	}
	request.WorkingDir = project.Path
	if len(project.Config.Env) > 0 {
		request.Env = make(map[string]string, len(project.Config.Env))
		for key, value := range project.Config.Env {
			request.Env[key] = value
		}
	}
	return request, nil
}

// rolePinnedDiscoveryRequest resolves project-scoped discovery with the role's
// provider pin overlaid on the project env, exactly as the launch path applies
// it. A pinless role (or a pin matching no configured gateway) yields the same
// request the plain project scope would.
func (s *Service) rolePinnedDiscoveryRequest(ctx context.Context, request ports.AgentModelDiscoveryRequest, project domain.ProjectRecord, role string) ports.AgentModelDiscoveryRequest {
	request.WorkingDir = project.Path
	env := make(map[string]string, len(project.Config.Env)+4)
	for key, value := range project.Config.Env {
		env[key] = value
	}
	var pin string
	switch role {
	case "orchestrator":
		pin = project.Config.Orchestrator.Provider
	case "reviewer":
		if len(project.Config.Reviewers) > 0 {
			pin = project.Config.Reviewers[0].Provider
		}
	default:
		pin = project.Config.Worker.Provider
	}
	var entries []agentcreds.GatewayEntry
	if s.providerEntries != nil {
		entries = s.providerEntries(ctx)
	}
	for key, value := range agentcreds.ProviderLaunchEnv(pin, project.ID, entries) {
		env[key] = value
	}
	if len(env) > 0 {
		request.Env = env
	}
	return request
}

func (s *Service) globalModelDiscoveryRequest(request ports.AgentModelDiscoveryRequest) (ports.AgentModelDiscoveryRequest, error) {
	if s.modelDiscoveryDir == "" {
		return request, nil
	}
	if !filepath.IsAbs(s.modelDiscoveryDir) {
		return ports.AgentModelDiscoveryRequest{}, fmt.Errorf("model discovery directory must be absolute: %s", s.modelDiscoveryDir)
	}
	if err := os.MkdirAll(s.modelDiscoveryDir, 0o700); err != nil {
		return ports.AgentModelDiscoveryRequest{}, fmt.Errorf("create model discovery directory: %w", err)
	}
	request.WorkingDir = s.modelDiscoveryDir
	return request, nil
}

// RevalidateModels rediscovers a cache-first catalog after the normal read path
// marks it old enough to refresh in the background.
func (s *Service) RevalidateModels(ctx context.Context, agentID, projectID string) (ports.AgentModelCatalog, error) {
	catalog, err := s.revalidateModels(ctx, agentID, projectID)
	if err != nil {
		return catalog, err
	}
	return s.withModelUsage(ctx, agentID, projectID, catalog), nil
}

// revalidateModels is the unstamped variant for background callers that discard
// the catalog and should not pay for a session-history scan.
func (s *Service) revalidateModels(ctx context.Context, agentID, projectID string) (ports.AgentModelCatalog, error) {
	var err error
	projectID, err = s.modelCatalogScope(ctx, projectID)
	if err != nil {
		return ports.AgentModelCatalog{}, err
	}
	return s.coalesceModelLoad(ctx, agentID, projectID, modelLoadRevalidate)
}

// InvalidateModelCatalogs marks existing scopes due and schedules cache-first
// revalidation. The last successful choices remain visible throughout.
func (s *Service) InvalidateModelCatalogs(agentID string) {
	if s.cache == nil {
		return
	}
	go func() {
		records, err := s.cache.ListAgentModelCatalogsByAgent(s.ctx, agentID)
		if err != nil {
			return
		}
		for _, record := range records {
			if record.ProjectID == "@browse:all" {
				continue
			}
			if s.ctx.Err() != nil {
				return
			}
			var catalog ports.AgentModelCatalog
			if json.Unmarshal([]byte(record.CatalogJSON), &catalog) == nil {
				s.requeueCatalog(record.ProjectID, catalog)
			}
			_, _ = s.revalidateModels(s.ctx, agentID, record.ProjectID)
		}
	}()
}

// requeueCatalog marks a cached scope due and drops its retry backoff, so the
// next revalidation runs discovery. The last successful choices remain visible.
func (s *Service) requeueCatalog(projectID string, catalog ports.AgentModelCatalog) {
	catalog.RefreshRecommended = true
	catalog.RefreshState = "queued"
	catalog.RefreshError = ""
	// The warning described the inputs being invalidated (for example a
	// rejected login). Clear it so clients stop showing it while the new
	// discovery runs; a repeat failure restores it.
	catalog.Warning = ""
	catalog.WarningCode = ""
	catalog.LastSuccessAt = nil
	catalog.RetryAt = nil
	_ = s.saveCatalog(s.ctx, projectID, catalog, time.Now().UTC().UnixNano(), 0)
}

// InvalidateProjectModelCatalogs marks every cached agent catalog for a changed
// project due without disturbing device-global scopes.
func (s *Service) InvalidateProjectModelCatalogs(projectID string) {
	if s.cache == nil || strings.TrimSpace(projectID) == "" {
		return
	}
	go func() {
		scopeCache, ok := s.cache.(ports.AgentModelCatalogScopeCache)
		if !ok {
			return
		}
		records, err := scopeCache.ListAgentModelCatalogs(s.ctx)
		if err != nil {
			return
		}
		for _, record := range records {
			if record.ProjectID != projectID || s.ctx.Err() != nil {
				continue
			}
			var catalog ports.AgentModelCatalog
			if json.Unmarshal([]byte(record.CatalogJSON), &catalog) != nil {
				continue
			}
			s.requeueCatalog(projectID, catalog)
			_, _ = s.revalidateModels(s.ctx, record.AgentID, projectID)
		}
	}()
}

func (s *Service) coalesceModelLoad(
	ctx context.Context,
	agentID, projectID string,
	mode modelLoadMode,
) (ports.AgentModelCatalog, error) {
	key := agentID + "\x00" + projectID
	s.modelCallMu.Lock()
	if active := s.modelCalls[key]; active != nil {
		s.modelCallMu.Unlock()
		select {
		case <-active.done:
			return active.catalog, active.err
		case <-ctx.Done():
			return ports.AgentModelCatalog{}, ctx.Err()
		}
	}
	wallGeneration := time.Now().UTC().UnixNano()
	if s.modelGeneration[key] < wallGeneration {
		s.modelGeneration[key] = wallGeneration
	} else {
		s.modelGeneration[key]++
	}
	call := &modelCatalogCall{done: make(chan struct{}), generation: s.modelGeneration[key]}
	s.modelCalls[key] = call
	s.modelCallMu.Unlock()

	baseCtx := s.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	go func() {
		select {
		case s.discoverySlots <- struct{}{}:
			defer func() { <-s.discoverySlots }()
		case <-baseCtx.Done():
			call.err = baseCtx.Err()
			s.modelCallMu.Lock()
			delete(s.modelCalls, key)
			close(call.done)
			s.modelCallMu.Unlock()
			return
		}
		loadCtx, cancel := context.WithTimeout(baseCtx, modelCatalogLoadTimeout)
		defer cancel()
		call.catalog, call.err = s.loadModels(loadCtx, agentID, projectID, mode, call.generation)
		s.modelCallMu.Lock()
		delete(s.modelCalls, key)
		close(call.done)
		s.modelCallMu.Unlock()
	}()

	select {
	case <-call.done:
		return call.catalog, call.err
	case <-ctx.Done():
		return ports.AgentModelCatalog{}, ctx.Err()
	}
}

func (s *Service) loadModels(ctx context.Context, agentID, projectID string, mode modelLoadMode, generation int64) (ports.AgentModelCatalog, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentModelCatalog{}, err
	}
	item, ok := s.agent(agentID)
	if !ok {
		return ports.AgentModelCatalog{}, apierr.NotFound("AGENT_NOT_FOUND", "Unknown agent adapter")
	}
	if s.discoverer == nil {
		return ports.AgentModelCatalog{}, apierr.Internal("MODEL_DISCOVERY_UNAVAILABLE", "Model discovery is unavailable")
	}
	cached, hasCached, err := s.cachedCatalog(ctx, agentID, projectID)
	if err != nil {
		return ports.AgentModelCatalog{}, err
	}
	cached.ProjectID = projectID
	policy := s.discoverer.Manual(agentID)
	if hasCached {
		cached.Catalog = applyCustomModelEntryPolicy(cached.Catalog, policy)
	}
	var binary string
	if resolver, ok := item.Agent.(ports.AgentBinaryResolver); ok {
		lock := s.resolverMu[agentID]
		lock.Lock()
		resolved, err := resolver.ResolveBinary(ctx)
		lock.Unlock()
		if err == nil {
			binary = resolved
		}
	}
	request, err := s.modelDiscoveryRequest(ctx, agentID, projectID, binary)
	if err != nil {
		return ports.AgentModelCatalog{}, err
	}
	// Fingerprints the same inputs the discovery run would read, so a change to
	// either the executable or the configuration behind it invalidates the cache.
	version := s.discoverer.CatalogFingerprint(ctx, request)
	inputsChanged := hasCached && cached.BinaryVersion != version
	explicitlyInvalidated := hasCached && (cached.RefreshState == "queued" || cached.RefreshState == "refreshing")
	if hasCached && mode == modelLoadCached && cached.BinaryVersion == version {
		// A command-backed catalog can drift without the binary or its config
		// changing (a provider adds a model), which no fingerprint can see. Ask
		// cache-first clients to revalidate in the background once the catalog is
		// old enough, so staleness resolves itself instead of waiting for someone
		// to press a refresh button.
		cached.Catalog.RefreshRecommended = !modelCatalogRetriesExhausted(cached) && catalogNeedsRevalidation(catalogLastSuccess(cached.Catalog), s.now())
		return cached.Catalog, nil
	}

	if mode != modelLoadRefresh && hasCached && !inputsChanged && !explicitlyInvalidated && modelCatalogRetriesExhausted(cached) {
		cached.Catalog.RefreshRecommended = false
		return cached.Catalog, nil
	}
	if mode != modelLoadRefresh && hasCached && !inputsChanged && !explicitlyInvalidated && !cached.RetryAt.IsZero() && s.now().Before(cached.RetryAt) {
		cached.Catalog.RefreshState = "error"
		cached.Catalog.RefreshError = cached.RefreshError
		cached.Catalog.RetryAt = modelCatalogRetryAt(cached.RetryAt)
		cached.Catalog.RefreshRecommended = true
		return cached.Catalog, nil
	}
	if mode == modelLoadRefresh || inputsChanged || explicitlyInvalidated {
		cached.RetryCount = 0
		cached.RetryAt = time.Time{}
		cached.RefreshError = ""
		cached.Catalog.RetryAt = nil
	}
	// Only an explicit user refresh surfaces a loading state. Automatic daily,
	// wake, and input-change revalidation keeps the last-known-good catalog
	// visible without causing cache subscribers to show a loader.
	if mode == modelLoadRefresh {
		_ = s.persistCatalogState(ctx, cached, hasCached, "refreshing", "", time.Time{}, generation)
	}
	discovered, discoverErr := s.discoverer.Discover(ctx, request)
	discovered = applyCustomModelEntryPolicy(discovered, policy)
	discovered.BinaryVersion = version
	persistCtx := s.ctx
	if persistCtx == nil {
		persistCtx = context.Background()
	}
	if errors.Is(discoverErr, ports.ErrAgentModelDiscoverySignInRequired) {
		return s.keepCatalogUntilSignIn(persistCtx, item.Manifest.Name, cached, hasCached, policy, version, generation), nil
	}
	if discoverErr != nil {
		warningCode := modelCatalogWarningCode(discoverErr)
		// Provider model IDs are credential-scoped. Reuse a cached catalog only
		// when it was produced from the same discovery inputs; otherwise a revoked
		// key or provider switch could leave invalid IDs in the picker.
		cacheMatchesInputs := hasCached && cached.BinaryVersion == version
		if cacheMatchesInputs && len(cached.Catalog.Models) > 0 {
			cached.Catalog.Stale = true
			cached.Catalog.Warning = discoverErr.Error()
			cached.Catalog.WarningCode = warningCode
			cached.Catalog.RefreshRecommended = true
			if err := s.saveFailedCatalog(persistCtx, cached, cached.Catalog, generation); err != nil {
				cached.Catalog.Warning = appendCacheWarning(cached.Catalog.Warning)
			} else if updated, ok, _ := s.cachedCatalog(persistCtx, agentID, projectID); ok {
				return updated.Catalog, nil
			}
			return cached.Catalog, nil
		}
		if len(discovered.Models) > 0 {
			discovered.Stale = true
			discovered.Warning = discoverErr.Error()
			discovered.WarningCode = warningCode
			discovered.RefreshRecommended = true
			previous := cached
			if !cacheMatchesInputs {
				previous.Catalog.LastSuccessAt = nil
				previous.Catalog.Metadata = catalogMetadata(request)
			}
			if err := s.saveFailedCatalog(persistCtx, previous, discovered, generation); err != nil {
				discovered.Warning = appendCacheWarning(discovered.Warning)
			} else if updated, ok, _ := s.cachedCatalog(persistCtx, agentID, projectID); ok {
				return updated.Catalog, nil
			}
			return discovered, nil
		}
		fallback := policy
		fallback.BinaryVersion = version
		fallback.Stale = true
		fallback.Warning = discoverErr.Error()
		fallback.WarningCode = warningCode
		fallback.RefreshRecommended = true
		// An empty catalog still carries retry history. Preserve it for the same
		// inputs so cold discovery failures eventually exhaust their retry budget.
		previous := cached
		if !cacheMatchesInputs {
			previous = decodedCatalog{Catalog: fallback, ProjectID: projectID}
			previous.Catalog.Metadata = catalogMetadata(request)
		}
		if err := s.saveFailedCatalog(persistCtx, previous, fallback, generation); err == nil {
			if updated, found, _ := s.cachedCatalog(persistCtx, agentID, projectID); found {
				return updated.Catalog, nil
			}
		}
		return fallback, nil
	}
	// A partial FX refresh updates successful connections and keeps only the
	// unavailable connections' previous models in the shared catalog.
	if agentID == "fx" && hasCached {
		unavailable := strings.Split(discovered.Metadata["unavailableProviders"], ",")
		for _, model := range cached.Catalog.Models {
			for _, provider := range unavailable {
				if provider != "" && model.Provider == provider {
					discovered.Models = append(discovered.Models, model)
					break
				}
			}
		}
	}
	now := s.now().UTC()
	discovered.ValidatedAt = now
	discovered.LastSuccessAt = &now
	discovered.InputFingerprint = version
	discovered.Metadata = catalogMetadata(request)
	// A refresh replaces metadata wholesale; carry user-set effort overrides
	// across so they keep annotating the freshly discovered models.
	if overrides := effortOverrides(cached.Catalog); len(overrides) > 0 {
		if raw, err := json.Marshal(overrides); err == nil {
			discovered.Metadata[effortOverridesMetadataKey] = string(raw)
		}
		discovered.Models = applyEffortOverrides(discovered.Models, overrides)
	}
	discovered.RefreshState = "idle"
	discovered.RefreshError = ""
	discovered.RetryAt = nil
	discovered.RefreshRecommended = false
	if err := s.saveCatalog(persistCtx, projectID, discovered, generation, 0); err != nil {
		discovered.Warning = appendCacheWarning(discovered.Warning)
	}
	if projectID == "" && hasCached && !maps.Equal(modelIDSet(cached.Catalog.Models), modelIDSet(discovered.Models)) {
		s.rediscoverDriftedProjectCatalogs(agentID, discovered.Models)
	}
	return discovered, nil
}

// keepCatalogUntilSignIn handles discovery skipped because the agent is
// clearly signed out. That is not a failure: no retry budget is spent and no
// retry timer is set. A cached catalog keeps its models but loses any earlier
// failure marker, and a first load stores an idle placeholder. Both carry the
// sign-in warning, so cache-first reads show it too. The record's validation
// times are cleared so it is due for revalidation regardless of when it last
// loaded: a sign-in made outside AO (for example from a terminal) is picked up
// by the next picker read or daemon start, not only by AO's own auth probe.
// The refresh state stays idle because the picker shows a spinner for queued.
func (s *Service) keepCatalogUntilSignIn(ctx context.Context, agentName string, cached decodedCatalog, hasCached bool, policy ports.AgentModelCatalog, version string, generation int64) ports.AgentModelCatalog {
	catalog := cached.Catalog
	if !hasCached {
		catalog = policy
		catalog.BinaryVersion = version
		catalog.InputFingerprint = version
	}
	catalog.ValidatedAt = time.Time{}
	catalog.LastSuccessAt = nil
	catalog.Stale = false
	catalog.Warning = agentName + " is not signed in; sign in to load its models"
	catalog.WarningCode = ports.ModelCatalogWarningAuthRequired
	catalog.RefreshState = "idle"
	catalog.RefreshError = ""
	catalog.RetryAt = nil
	catalog.RefreshRecommended = false
	if err := s.saveCatalog(ctx, cached.ProjectID, catalog, generation, 0); err != nil {
		catalog.Warning = appendCacheWarning(catalog.Warning)
	}
	return catalog
}

// modelCatalogWarningCode classifies a discovery failure that clients can
// resolve with the agent's own login.
func modelCatalogWarningCode(err error) string {
	switch {
	case errors.Is(err, ports.ErrAgentModelDiscoveryCredentialExpired):
		return ports.ModelCatalogWarningAuthExpired
	case errors.Is(err, ports.ErrAgentModelDiscoveryCredentialRejected),
		errors.Is(err, ports.ErrAgentModelDiscoverySignInRequired):
		return ports.ModelCatalogWarningAuthRequired
	default:
		return ""
	}
}

func applyCustomModelEntryPolicy(catalog, policy ports.AgentModelCatalog) ports.AgentModelCatalog {
	entryMode := policy.CustomModelEntry
	if entryMode == "" {
		if policy.AllowCustom {
			entryMode = ports.CustomModelEntryDirect
		} else {
			entryMode = ports.CustomModelEntryNone
		}
	}
	catalog.CustomModelEntry = entryMode
	catalog.AllowCustom = entryMode == ports.CustomModelEntryDirect
	return catalog
}

func appendCacheWarning(current string) string {
	const next = "Models loaded, but AO could not update the model cache."
	if current == "" {
		return next
	}
	return current + " " + next
}

type decodedCatalog struct {
	Catalog       ports.AgentModelCatalog
	ProjectID     string
	BinaryVersion string
	LastSuccessAt time.Time
	RefreshState  string
	RefreshError  string
	RetryCount    int64
	RetryAt       time.Time
	Generation    int64
}

func (s *Service) cachedCatalog(ctx context.Context, agentID, projectID string) (decodedCatalog, bool, error) {
	if s.cache == nil {
		return decodedCatalog{}, false, nil
	}
	record, ok, err := s.cache.GetAgentModelCatalog(ctx, agentID, projectID)
	if err != nil || !ok {
		return decodedCatalog{}, ok, err
	}
	var catalog ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &catalog); err != nil {
		return decodedCatalog{}, false, fmt.Errorf("decode cached model catalog for %s: %w", agentID, err)
	}
	if catalog.Models == nil {
		catalog.Models = []ports.AgentModelInfo{}
	}
	// Per-model effort overrides live in the catalog metadata and reapply on
	// every read, so a user-tuned effort on an off-seed or custom model
	// survives catalog refreshes without mutating stored discovery output.
	catalog.Models = applyEffortOverrides(catalog.Models, effortOverrides(catalog))
	if catalog.LastSuccessAt == nil || catalog.LastSuccessAt.IsZero() {
		lastSuccess := record.LastSuccessAt
		if lastSuccess.IsZero() {
			lastSuccess = catalog.ValidatedAt
		}
		if !lastSuccess.IsZero() {
			catalog.LastSuccessAt = &lastSuccess
		}
	}
	catalog.RefreshState = record.RefreshState
	catalog.RefreshError = record.RefreshError
	catalog.RetryAt = modelCatalogRetryAt(record.RetryAt)
	return decodedCatalog{Catalog: catalog, ProjectID: record.ProjectID, BinaryVersion: record.BinaryVersion, LastSuccessAt: catalogLastSuccess(catalog), RefreshState: record.RefreshState, RefreshError: record.RefreshError, RetryCount: record.RetryCount, RetryAt: record.RetryAt, Generation: record.Generation}, true, nil
}

func catalogLastSuccess(catalog ports.AgentModelCatalog) time.Time {
	if catalog.LastSuccessAt == nil {
		return time.Time{}
	}
	return *catalog.LastSuccessAt
}

func (s *Service) saveCatalog(ctx context.Context, projectID string, catalog ports.AgentModelCatalog, generation, retryCount int64) error {
	if s.cache == nil {
		return nil
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		return fmt.Errorf("encode model catalog for %s: %w", catalog.AgentID, err)
	}
	metadata, _ := json.Marshal(catalog.Metadata)
	return s.cache.UpsertAgentModelCatalog(ctx, ports.CachedAgentModelCatalog{
		AgentID:          catalog.AgentID,
		ProjectID:        projectID,
		BinaryVersion:    catalog.BinaryVersion,
		CatalogJSON:      string(data),
		Source:           catalog.Source,
		FetchedAt:        catalog.FetchedAt,
		MetadataJSON:     string(metadata),
		InputFingerprint: catalog.InputFingerprint,
		LastSuccessAt:    catalogLastSuccess(catalog),
		RefreshState:     catalog.RefreshState,
		RefreshError:     catalog.RefreshError,
		RetryCount:       retryCount,
		RetryAt:          catalogRetryTime(catalog.RetryAt),
		Generation:       generation,
	})
}

func catalogMetadata(request ports.AgentModelDiscoveryRequest) map[string]string {
	metadata := map[string]string{"scope": "global"}
	if request.WorkingDir != "" {
		metadata["scope"] = "project"
	}
	if request.Binary != "" {
		metadata["binary"] = request.Binary
	}
	return metadata
}

// effortOverridesMetadataKey stores user-set per-model effort overrides inside
// the cached catalog's metadata as a JSON map of model id to effort, so they
// survive catalog refreshes (issue #6098).
const effortOverridesMetadataKey = "effortOverrides"

// effortOverrides decodes the catalog's persisted effort overrides. A missing
// or malformed entry yields nil.
func effortOverrides(catalog ports.AgentModelCatalog) map[string]string {
	raw := catalog.Metadata[effortOverridesMetadataKey]
	if raw == "" {
		return nil
	}
	var overrides map[string]string
	if json.Unmarshal([]byte(raw), &overrides) != nil {
		return nil
	}
	return overrides
}

// applyEffortOverrides annotates models with user-set efforts they do not
// already advertise. It never removes a provider-advertised level.
func applyEffortOverrides(models []ports.AgentModelInfo, overrides map[string]string) []ports.AgentModelInfo {
	if len(overrides) == 0 {
		return models
	}
	for i := range models {
		effort := strings.TrimSpace(overrides[models[i].ID])
		if effort == "" || containsEffort(models[i].Efforts, effort) {
			continue
		}
		models[i].Efforts = append(models[i].Efforts, effort)
	}
	return models
}

func containsEffort(efforts []string, effort string) bool {
	for _, candidate := range efforts {
		if strings.EqualFold(candidate, effort) {
			return true
		}
	}
	return false
}

// SetModelEffortOverride records a user-set reasoning effort for one model in
// the cached catalog, keeping off-seed and custom models tunable across
// refreshes. An empty effort clears the override. The override only annotates
// the picker and spawn validation; choosing the level per role or per turn
// stays with the existing model/effort settings.
func (s *Service) SetModelEffortOverride(ctx context.Context, agentID, projectID, modelID, effort string) error {
	if s.discoverer == nil {
		return apierr.Internal("MODEL_DISCOVERY_UNAVAILABLE", "Model discovery is unavailable")
	}
	var err error
	projectID, err = s.modelCatalogScope(ctx, projectID)
	if err != nil {
		return err
	}
	modelID = strings.TrimSpace(modelID)
	effort = strings.TrimSpace(effort)
	if modelID == "" {
		return apierr.Invalid("MODEL_ID_REQUIRED", "A model id is required", nil)
	}
	cached, ok, err := s.cachedCatalog(ctx, agentID, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.NotFound("MODEL_CATALOG_NOT_FOUND", "No cached model catalog to override")
	}
	overrides := effortOverrides(cached.Catalog)
	if overrides == nil {
		overrides = map[string]string{}
	}
	if effort == "" {
		delete(overrides, modelID)
	} else {
		overrides[modelID] = effort
	}
	if cached.Catalog.Metadata == nil {
		cached.Catalog.Metadata = map[string]string{}
	}
	if len(overrides) == 0 {
		delete(cached.Catalog.Metadata, effortOverridesMetadataKey)
	} else if raw, err := json.Marshal(overrides); err == nil {
		cached.Catalog.Metadata[effortOverridesMetadataKey] = string(raw)
	}
	cached.Catalog.Models = applyEffortOverrides(cached.Catalog.Models, overrides)
	return s.saveCatalog(ctx, projectID, cached.Catalog, cached.Generation, cached.RetryCount)
}

func (s *Service) persistCatalogState(ctx context.Context, cached decodedCatalog, hasCached bool, state, message string, retryAt time.Time, generation int64) error {
	if !hasCached {
		return nil
	}
	catalog := cached.Catalog
	catalog.RefreshState = state
	catalog.RefreshError = message
	catalog.RetryAt = modelCatalogRetryAt(retryAt)
	return s.saveCatalog(ctx, cached.ProjectID, catalog, generation, cached.RetryCount)
}

func (s *Service) saveFailedCatalog(ctx context.Context, previous decodedCatalog, catalog ports.AgentModelCatalog, generation int64) error {
	retryCount := previous.RetryCount + 1
	catalog.LastSuccessAt = previous.Catalog.LastSuccessAt
	catalog.RefreshState = "error"
	catalog.RefreshError = catalog.Warning
	catalog.InputFingerprint = catalog.BinaryVersion
	catalog.Metadata = previous.Catalog.Metadata
	catalog.RetryAt = nil
	catalog.RefreshRecommended = retryCount <= int64(modelCatalogMaxRetries)
	var retryDelay time.Duration
	if retryCount <= int64(modelCatalogMaxRetries) {
		retryDelay = modelCatalogRetryDelays[retryCount-1]
		retryAt := s.now().Add(retryDelay).UTC()
		catalog.RetryAt = &retryAt
	}
	if err := s.saveCatalog(ctx, previous.ProjectID, catalog, generation, retryCount); err != nil {
		return err
	}
	if catalog.RetryAt != nil && s.cache != nil {
		retryAt := *catalog.RetryAt
		time.AfterFunc(retryDelay, func() {
			if s.ctx.Err() != nil {
				return
			}
			record, ok, err := s.cache.GetAgentModelCatalog(s.ctx, catalog.AgentID, previous.ProjectID)
			if err != nil || !ok || record.Generation != generation || record.RefreshState != "error" || !record.RetryAt.Equal(retryAt) {
				return
			}
			_, _ = s.revalidateModels(s.ctx, catalog.AgentID, previous.ProjectID)
		})
	}
	return nil
}

func modelCatalogRetriesExhausted(cached decodedCatalog) bool {
	return cached.RefreshState == "error" && cached.RetryCount > int64(modelCatalogMaxRetries)
}

func modelCatalogRetryAt(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func catalogRetryTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}

func (s *Service) agent(agentID string) (agentregistry.HarnessAgent, bool) {
	for _, item := range s.agents {
		if string(item.Harness) == agentID {
			return item, true
		}
	}
	return agentregistry.HarnessAgent{}, false
}

// ResolveAgentBinary resolves one harness through its shipped adapter. This is
// the shared boundary for features that must launch the same executable normal
// session startup recognizes, including managed locations outside PATH.
func (s *Service) ResolveAgentBinary(ctx context.Context, agentID string) (string, error) {
	item, ok := s.agent(agentID)
	if !ok {
		return "", apierr.Invalid("AGENT_UNKNOWN", fmt.Sprintf("unknown agent %q", agentID), nil)
	}
	resolver, ok := item.Agent.(ports.AgentBinaryResolver)
	if !ok {
		return "", fmt.Errorf("agent %s: %w", agentID, ports.ErrAgentBinaryNotFound)
	}
	lock := s.resolverMu[agentID]
	lock.Lock()
	defer lock.Unlock()
	return resolver.ResolveBinary(ctx)
}
