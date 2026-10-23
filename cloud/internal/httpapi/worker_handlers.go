package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	activitylog "github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/logctx"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/roleprompt"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/go-chi/chi/v5"
)

type workerGitHubPATStore interface {
	WorkerGitHubPAT(context.Context, string, string, string, int64) (domain.WorkerGitHubPAT, error)
}

// workerGitHubPATGrant resolves the configured PAT only for the authenticated
// worker's own session. A missing PAT is deliberately indistinguishable from
// no configured fallback so the GitHub App broker remains the normal path.
func (s *Server) workerGitHubPATGrant(ctx context.Context, claims worker.Claims) (worker.CheckoutGrantResponse, bool) {
	store, ok := s.store.(workerGitHubPATStore)
	if !ok || s.secretCipher == nil {
		return worker.CheckoutGrantResponse{}, false
	}
	credential, err := store.WorkerGitHubPAT(ctx, claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch)
	if err != nil {
		if !errors.Is(err, postgres.ErrNotFound) && !errors.Is(err, postgres.ErrForbidden) {
			s.logger.Warn("resolve worker GitHub personal access token", "error", err)
		}
		return worker.CheckoutGrantResponse{}, false
	}
	parsed, err := url.Parse(credential.CloneURL)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.User != nil {
		s.logger.Warn("reject worker GitHub personal access token for non-GitHub repository", "session_id", claims.SessionID)
		return worker.CheckoutGrantResponse{}, false
	}
	secret, err := s.secretCipher.Decrypt(credential.EncryptedSecret, credential.Nonce, providerSecretAssociatedData("user:"+credential.OwnerUserID, githubPATProvider))
	if err != nil {
		s.logger.Error("decrypt worker GitHub personal access token", "error", err)
		return worker.CheckoutGrantResponse{}, false
	}
	if len(secret) == 0 {
		return worker.CheckoutGrantResponse{}, false
	}
	defer clear(secret)
	return worker.CheckoutGrantResponse{
		CloneURL: credential.CloneURL,
		Token:    string(secret),
		// A PAT has no provider expiry. This is only a response freshness bound;
		// the worker asks again whenever Git needs credentials.
		ExpiresAt: time.Now().Add(time.Hour),
	}, true
}

// patWriteGrant returns the session's decrypted PAT grant when a PAT write path
// is wired and the session has a valid PAT, so GitHub write handlers can prefer
// it over the possibly write-incapable checkout broker. Returns false to fall
// back to the broker.
func (s *Server) patWriteGrant(ctx context.Context, claims worker.Claims) (worker.CheckoutGrantResponse, bool) {
	if s.patWrites == nil {
		return worker.CheckoutGrantResponse{}, false
	}
	return s.workerGitHubPATGrant(ctx, claims)
}

// Worker events are namespaced so a compromised sandbox cannot forge a
// control-plane or billing event onto its own session stream.
var workerEventTypes = map[string]struct{}{
	"agent.activity":       {},
	"agent.ready":          {},
	"worker.ready":         {},
	"chat.assistant_delta": {},
	"chat.activity":        {},
	// workspace.setup reports the project's Setup commands' outcome so the
	// session shows why its setup is missing.
	worker.SetupEventType: {},
	// workspace.startup reports whether the worker could prepare the
	// session's workspace; a failure becomes the session's startup error.
	worker.WorkspaceStartupEventType: {},
	// workspace.script.step reports one step of a Setup run with its output,
	// live while it runs.
	worker.ScriptStepEventType: {},
}

const (
	maxWorkerEventType   = 100
	maxWorkerControlBody = 8 << 10
	maxWorkerOutput      = 16 << 10
	maxWorkerError       = 4 << 10
)

// workerBootstrap redeems a one-time ticket for a live worker credential. It is
// the only unauthenticated worker route: the ticket itself is the proof, and it
// is consumed atomically so a replayed token buys nothing.
func (s *Server) workerBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.workerTokens == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "Worker bootstrap is not enabled.")
		return
	}
	var input worker.BootstrapRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(input.BootstrapToken) == "" {
		writeError(w, r, http.StatusUnauthorized, "INVALID_BOOTSTRAP", "A bootstrap token is required.")
		return
	}

	ticket, err := s.store.RedeemWorkerBootstrapTicket(r.Context(), input.BootstrapToken)
	if errors.Is(err, postgres.ErrInvalidTicket) {
		writeError(w, r, http.StatusUnauthorized, "INVALID_BOOTSTRAP", "The bootstrap token is invalid, expired, or already used.")
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if ticket.WorkerEpoch <= 0 {
		s.logger.Error("worker bootstrap produced no epoch", "session_id", ticket.SessionID, "request_id", requestID(r))
		writeError(w, r, http.StatusInternalServerError, "BOOTSTRAP_FAILED", "Worker bootstrap identity was not assigned.")
		return
	}

	launch, err := s.store.WorkerLaunchSpec(r.Context(), ticket.OrgID, ticket.SessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.startLaunchInTUIIfChatUnsupported(r, ticket.OrgID, &launch,
		slices.Contains(input.Capabilities, worker.CapabilityChatStart)); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	launchContext, err := launchContextFrom(launch, launchCapabilities{
		resolvesWorkspacePath: slices.Contains(input.Capabilities, worker.CapabilityWorkspacePathPlaceholder),
		multiRepo:             slices.Contains(input.Capabilities, worker.CapabilityMultiRepo),
		artifacts:             s.artifactsEnabled() && slices.Contains(input.Capabilities, worker.CapabilityArtifacts),
	})
	if err != nil {
		s.logger.Error("build worker launch context", "error", err, "project_id", launch.ProjectID, "request_id", requestID(r))
		writeError(w, r, http.StatusInternalServerError, "BOOTSTRAP_FAILED", "The project's role instructions are invalid.")
		return
	}

	workerID := worker.NextWorkerID(ticket.SessionID, ticket.WorkerEpoch)
	// Read the startup timing before the registration and first heartbeat
	// clear it, so the support trail can measure time-to-ready.
	readiness, readinessErr := s.sandboxReadiness(r.Context(), ticket.OrgID, ticket.SessionID)
	if err := s.store.RegisterWorkerBootstrap(
		r.Context(),
		ticket.OrgID,
		ticket.SessionID,
		workerID,
		input.Version,
		ticket.WorkerEpoch,
		input.Capabilities,
	); err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	_, installedCleanup := launchContext.ProjectScripts()
	s.recordInstalledCleanup(r, ticket.OrgID, ticket.SessionID, workerID, ticket.WorkerEpoch, installedCleanup)

	scopes := issuedWorkerScopes(ticket.Scopes, launch)
	token, err := s.workerTokens.Issue(worker.Claims{
		OrgID:     ticket.OrgID,
		SessionID: ticket.SessionID,
		WorkerID:  workerID,
		Epoch:     ticket.WorkerEpoch,
		Scopes:    scopes,
	}, s.workerTokenTTL())
	if err != nil {
		s.logger.Error("issue worker token", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The worker credential could not be issued.")
		return
	}

	payload, _ := json.Marshal(map[string]any{"workerId": workerID, "epoch": ticket.WorkerEpoch})
	if _, err := s.store.AppendSessionEvent(
		r.Context(), ticket.OrgID, ticket.SessionID, "worker.connected", payload,
	); err != nil {
		s.logger.Warn("append worker.connected event", "error", err, "request_id", requestID(r))
	}
	s.recordWorkerReady(r, ticket.OrgID, ticket.SessionID, workerID, input.Version, ticket.WorkerEpoch, readiness, readinessErr)

	writeJSON(w, http.StatusOK, worker.BootstrapResponse{
		WorkerToken: token,
		WorkerID:    workerID,
		Epoch:       ticket.WorkerEpoch,
		ExpiresIn:   int(s.workerTokenTTL().Seconds()),
		SessionID:   ticket.SessionID,
		Launch:      launchContext,
	})
}

// serveWorkerBinary returns a worker or helper binary addressed by its sha256.
// A worker whose baked copy is stale fetches the exact build the control plane
// runs and heals itself, so the reconciler never uploads multi-megabyte binaries
// on provision. The bytes are not secret — they ship in every sandbox image — so
// the route is content-addressed rather than authenticated.
func (s *Server) serveWorkerBinary(w http.ResponseWriter, r *http.Request) {
	requested := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "sha256")))
	binary, ok := s.workerBinariesBySHA[requested]
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found", "No worker binary matches that hash.")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(binary)))
	// Content-addressed bytes are immutable: a hash always maps to the same
	// binary, so any cache may keep it indefinitely.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(binary)
}

// workerReconnect returns the durable launch context to a worker that
// re-presented a persisted token, so a restart never redeems a fresh bootstrap
// ticket for a sandbox it is already registered on.
func (s *Server) workerReconnect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	launch, err := s.store.WorkerLaunchSpec(r.Context(), claims.OrgID, claims.SessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	// No Chat-start fallback here (startLaunchInTUIIfChatUnsupported): the
	// worker may be live, already running the interface it started in.
	launchContext, err := launchContextFrom(launch, launchCapabilities{
		resolvesWorkspacePath: headerReportsCapability(r, worker.CapabilityWorkspacePathPlaceholder),
		multiRepo:             headerReportsCapability(r, worker.CapabilityMultiRepo),
		artifacts:             s.artifactsEnabled() && headerReportsCapability(r, worker.CapabilityArtifacts),
	})
	if err != nil {
		s.logger.Error("build worker reconnect context", "error", err, "project_id", launch.ProjectID, "request_id", requestID(r))
		writeError(w, r, http.StatusInternalServerError, "RECONNECT_FAILED", "The project's role instructions are invalid.")
		return
	}
	// A reconnecting worker (a restart, or a reopened session refreshing its
	// scripts) reinstalls its Cleanup hook with these commands.
	_, installedCleanup := launchContext.ProjectScripts()
	s.recordInstalledCleanup(r, claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch, installedCleanup)
	writeJSON(w, http.StatusOK, worker.BootstrapResponse{
		WorkerID:  claims.WorkerID,
		Epoch:     claims.Epoch,
		ExpiresIn: int(s.workerTokenTTL().Seconds()),
		SessionID: claims.SessionID,
		Launch:    launchContext,
	})
}

// installedCleanupRecorder is the optional Store capability that stores the
// Cleanup commands a worker was given (postgres.Store.RecordInstalledCleanupCommands).
type installedCleanupRecorder interface {
	RecordInstalledCleanupCommands(ctx context.Context, orgID, sessionID, workerID string, epoch int64, commands []string) error
}

// recordInstalledCleanup stores the Cleanup commands a worker is being given
// (each led by its time limit's marker when it sets one), which it installs as
// its Cleanup hook's commands, so a Cleanup that runs
// them after the project's config changed is read knowing the values they
// assign to secret-named variables. A failure only loses that knowledge
// (the project's current Cleanup is used instead) and never fails the
// worker's connection.
func (s *Server) recordInstalledCleanup(r *http.Request, orgID, sessionID, workerID string, epoch int64, commands []string) {
	recorder, ok := s.store.(installedCleanupRecorder)
	if !ok {
		return
	}
	if err := recorder.RecordInstalledCleanupCommands(r.Context(), orgID, sessionID, workerID, epoch, commands); err != nil {
		s.logger.Warn("record the worker's cleanup commands", "error", err, "session_id", sessionID, "request_id", requestID(r))
	}
}

// headerReportsCapability reports whether a worker request's
// worker.CapabilitiesHeader lists capability.
func headerReportsCapability(r *http.Request, capability string) bool {
	for _, reported := range strings.Split(r.Header.Get(worker.CapabilitiesHeader), ",") {
		if strings.TrimSpace(reported) == capability {
			return true
		}
	}
	return false
}

// launchCapabilities is what the worker a launch context is for reported it
// can do.
type launchCapabilities struct {
	// resolvesWorkspacePath is whether the worker substitutes
	// worker.WorkspacePathPlaceholder.
	resolvesWorkspacePath bool
	// multiRepo is whether the worker advertised worker.CapabilityMultiRepo.
	multiRepo bool
	// artifacts is whether cloud artifacts are on and the worker advertised
	// worker.CapabilityArtifacts. A session's requested output reaches the
	// worker only then.
	artifacts bool
}

// launchContextFrom projects a stored launch spec onto the wire type shared by
// bootstrap and reconnect, for a worker with the given capabilities.
func launchContextFrom(launch domain.WorkerLaunch, capabilities launchCapabilities) (worker.LaunchContext, error) {
	resolvesWorkspacePath := capabilities.resolvesWorkspacePath
	var agentConfig domain.ProjectAgentConfig
	if len(launch.AgentConfig) > 0 {
		if err := json.Unmarshal(launch.AgentConfig, &agentConfig); err != nil {
			return worker.LaunchContext{}, err
		}
	}
	agentRules, orchestratorRules, err := projectRoleRules(launch.ProjectConfig)
	if err != nil {
		return worker.LaunchContext{}, err
	}
	// Each step's own time limit goes beside the commands, so a worker that
	// predates per-step limits runs the plain commands with the default.
	timedPostCreate, timedPreRemove := domain.ProjectScripts(launch.ProjectConfig, launch.RepositoryURL)
	postCreate, postCreateTimeouts := domain.SplitTimedScriptCommands(timedPostCreate)
	preRemove, preRemoveTimeouts := domain.SplitTimedScriptCommands(timedPreRemove)
	// Additional repositories are project-level (stored on the project config),
	// so every session of the project clones the same set, on every sandbox
	// provider. Decoded before the prompt is built so both the project context
	// (which makes every role aware the project is multi-repo) and the worker's
	// clone list draw from the same source.
	var extraRepos []worker.RepoRef
	var promptExtras []roleprompt.RepoRef
	if repos := domain.ProjectAdditionalRepos(launch.ProjectConfig); len(repos) > 0 {
		extraRepos = make([]worker.RepoRef, 0, len(repos))
		promptExtras = make([]roleprompt.RepoRef, 0, len(repos))
		for _, repo := range repos {
			extraRepos = append(extraRepos, worker.RepoRef{URL: repo.URL, Branch: repo.Branch})
			promptExtras = append(promptExtras, roleprompt.RepoRef{URL: repo.URL, Branch: repo.Branch, Dir: domain.RepoDirName(repo.URL)})
		}
	}
	// Only a multi-repo worker honours the start folder; an older one always
	// starts in the main checkout, so the prompt must not claim otherwise.
	agentStartsIn := ""
	if capabilities.multiRepo && domain.ProjectAgentStartsIn(launch.ProjectConfig) == domain.ProjectAgentStartsInParent {
		agentStartsIn = domain.ProjectAgentStartsInParent
	}
	systemPrompt := roleprompt.Build(roleprompt.Config{
		Role:          launch.Kind,
		ProjectID:     launch.ProjectID,
		ProjectName:   launch.ProjectName,
		RepositoryURL: launch.RepositoryURL,
		DefaultBranch: launch.DefaultBranch,
		// The worker knows where the session actually works (it depends on the
		// provider, the workspace user's home and a Coder connection's working
		// directory) and substitutes it before launching the agent. An older
		// worker shows the prompt as is, so it gets the path it always showed.
		WorkspacePath:       workspacePathFor(resolvesWorkspacePath),
		AgentRules:          agentRules,
		OrchestratorRules:   orchestratorRules,
		ExtraRepos:          promptExtras,
		AgentStartsInParent: agentStartsIn == domain.ProjectAgentStartsInParent,
		Standalone:          domain.IsStandaloneRepositoryURL(launch.RepositoryURL),
		MultiRepoWorker:     capabilities.multiRepo,
	})
	return worker.LaunchContext{
		SessionID:       launch.SessionID,
		ProjectID:       launch.ProjectID,
		ProjectName:     launch.ProjectName,
		Kind:            launch.Kind,
		Harness:         launch.Harness,
		DisplayName:     launch.DisplayName,
		Branch:          launch.Branch,
		Prompt:          launch.Prompt,
		AgentSessionID:  launch.AgentSessionID,
		Interface:       string(launch.Interface),
		ParentSessionID: launch.ParentSessionID,
		Mode:            launch.Mode,
		Model:           launch.Model,
		AgentConfig:     agentConfig,
		ReasoningEffort: launch.ReasoningEffort,
		SelectionAt:     launch.SelectionAt,
		DeniedCommands:  launch.DeniedCommands,
		RepositoryURL:   launch.RepositoryURL,
		DefaultBranch:   launch.DefaultBranch,
		ExtraRepos:      extraRepos,
		AgentStartsIn:   agentStartsIn,
		SystemPrompt:    systemPrompt,
		PostCreate:      postCreate,
		PreRemove:       preRemove,
		RequestedOutput: requestedOutputFor(launch, capabilities.artifacts),
		Artifacts:       capabilities.artifacts,

		PostCreateTimeoutMinutes: postCreateTimeouts,
		PreRemoveTimeoutMinutes:  preRemoveTimeouts,
	}, nil
}

func projectRoleRules(config json.RawMessage) (string, string, error) {
	if len(config) == 0 {
		return "", "", nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(config, &values); err != nil {
		return "", "", err
	}
	decodeString := func(key string) string {
		var value string
		if raw := values[key]; len(raw) > 0 {
			// Cloud project config predates typed role rules and accepts arbitrary
			// values. Ignore legacy/non-string collisions rather than making a
			// one-time worker bootstrap ticket permanently unusable.
			_ = json.Unmarshal(raw, &value)
		}
		return value
	}
	return decodeString("agentRules"), decodeString("orchestratorRules"), nil
}

// indexWorkerBinaries maps each non-empty binary to its sha256 hex so the control
// plane can serve the exact build a stale worker needs.
func indexWorkerBinaries(binaries ...[]byte) map[string][]byte {
	index := make(map[string][]byte, len(binaries))
	for _, binary := range binaries {
		if len(binary) == 0 {
			continue
		}
		sum := sha256.Sum256(binary)
		index[hex.EncodeToString(sum[:])] = binary
	}
	return index
}

// issuedWorkerScopes narrows the bootstrap ticket's full scope set to what the
// session's durable row entitles it to: worker:orchestrate only for
// orchestrator sessions, worker:report only for sessions an orchestrator
// spawned. Heartbeat renewal re-issues the presented claims, so a strip here
// is permanent for the worker's lifetime.
func issuedWorkerScopes(ticketScopes []string, launch domain.WorkerLaunch) []string {
	scopes := slices.Clone(ticketScopes)
	if launch.Kind != "orchestrator" {
		scopes = slices.DeleteFunc(scopes, func(scope string) bool {
			return scope == "worker:orchestrate"
		})
	}
	if launch.ParentSessionID == "" {
		scopes = slices.DeleteFunc(scopes, func(scope string) bool {
			return scope == "worker:report"
		})
	}
	return scopes
}

type workerContextKey struct{}

// workerAgentStartedStore is the optional Store capability that says whether
// a worker epoch started its coding agent (agent.ready, or a claimed turn).
type workerAgentStartedStore interface {
	WorkerAgentStarted(ctx context.Context, orgID, sessionID, workerID string, epoch int64) (bool, error)
}

// workerAuth authenticates a live worker. A valid signature is not enough: the
// claimed epoch must still be the session's current one, so a worker that a
// recreate replaced is rejected even while its token is unexpired.
func (s *Server) workerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.workerTokens == nil {
			writeError(w, r, http.StatusNotFound, "not_found", "Worker routes are not enabled.")
			return
		}
		scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
		if !ok || !strings.EqualFold(scheme, "Worker") {
			writeError(w, r, http.StatusUnauthorized, "WORKER_AUTH_REQUIRED", "A worker credential is required.")
			return
		}
		claims, err := s.workerTokens.Verify(strings.TrimSpace(token))
		if err != nil {
			writeError(w, r, http.StatusUnauthorized, "INVALID_WORKER_TOKEN", "The worker credential is invalid or expired.")
			return
		}
		current, err := s.store.WorkerConnectionCurrent(
			r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch,
		)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		if !current {
			writeError(w, r, http.StatusUnauthorized, "STALE_WORKER_TOKEN", "The worker credential has been replaced.")
			return
		}
		logctx.Annotate(r.Context(), logctx.Fields{
			OrgID:     claims.OrgID,
			SessionID: claims.SessionID,
			WorkerID:  claims.WorkerID,
		})
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), workerContextKey{}, claims)))
	})
}

func workerFrom(r *http.Request) worker.Claims {
	claims, _ := r.Context().Value(workerContextKey{}).(worker.Claims)
	return claims
}

// workerHeartbeat records liveness and renews the worker's short-lived token.
// This is the only path that promotes a sandbox to running.
func (s *Server) workerHeartbeat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	var input worker.HeartbeatRequest
	// Unknown fields are ignored here, unlike other requests: a worker newer
	// than this control plane (a rollback) may report more, and rejecting
	// its heartbeat would take every such session offline.
	if err := decodeJSONTolerant(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.store.MarkWorkerSeen(
		r.Context(),
		claims.OrgID,
		claims.SessionID,
		claims.WorkerID,
		input.Version,
		claims.Epoch,
		input.Capabilities,
	); err != nil {
		if errors.Is(err, postgres.ErrStaleWorker) {
			writeError(w, r, http.StatusUnauthorized, "STALE_WORKER_TOKEN", "The worker credential has been replaced.")
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	if input.Busy {
		if store, ok := s.store.(interface {
			MarkSandboxBusy(ctx context.Context, orgID, sessionID string) error
		}); ok {
			// Best effort: a missed mark only makes idle pause slightly eager,
			// and the next busy heartbeat records it again.
			if err := store.MarkSandboxBusy(r.Context(), claims.OrgID, claims.SessionID); err != nil {
				s.logger.Warn("record sandbox activity", "session_id", claims.SessionID, "error", err)
			}
		}
	}
	renewed, err := s.workerTokens.Issue(claims, s.workerTokenTTL())
	if err != nil {
		s.logger.Error("renew worker token", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The worker credential could not be renewed.")
		return
	}
	writeJSON(w, http.StatusOK, worker.HeartbeatResponse{
		OK:          true,
		WorkerToken: renewed,
		ExpiresIn:   int(s.workerTokenTTL().Seconds()),
		Features:    []string{worker.HeartbeatFeatureBusy},
	})
}

// workerCheckoutGrant brokers a fresh repository-scoped installation token.
// The worker identity supplies the org and session; no repository or
// installation identifier is accepted from the sandbox.
func (s *Server) workerCheckoutGrant(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:git") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:git scope is required.")
		return
	}
	// Prefer the GitHub App installation grant: it is minted fresh per request and
	// never goes stale. A stored PAT is used only as a fallback when the App path
	// cannot serve this project (no installation / not App-connected / mint
	// failed). A PAT's validation_state is a cached snapshot, so preferring it
	// could let a rotted PAT shadow a healthy App installation and fail every clone
	// with "Invalid username or token"; the App-first order prevents that.
	if s.checkoutBroker != nil {
		grant, err := s.checkoutBroker.IssueCheckoutGrant(r.Context(), claims.OrgID, claims.SessionID)
		if err == nil && grant.Token != "" && grant.CloneURL != "" && grant.ExpiresAt.After(time.Now()) {
			writeJSON(w, http.StatusOK, worker.CheckoutGrantResponse{
				CloneURL: grant.CloneURL, Token: grant.Token, ExpiresAt: grant.ExpiresAt,
			})
			return
		}
		if pat, ok := s.workerGitHubPATGrant(r.Context(), claims); ok {
			writeJSON(w, http.StatusOK, pat)
			return
		}
		if errors.Is(err, postgres.ErrForbidden) || errors.Is(err, postgres.ErrNotFound) {
			s.recordGitGrantFailure(r, claims, "checkout", "CHECKOUT_NOT_AUTHORIZED", err)
			writeError(w, r, http.StatusForbidden, "CHECKOUT_NOT_AUTHORIZED", "This session does not have an active repository grant.")
			return
		}
		if err != nil {
			s.logger.Error("issue worker checkout grant", "error", err, "request_id", requestID(r))
			s.recordGitGrantFailure(r, claims, "checkout", "SCM_BROKER_FAILED", err)
			writeError(w, r, http.StatusBadGateway, "SCM_BROKER_FAILED", "A repository checkout grant could not be issued.")
			return
		}
		s.logger.Error("worker checkout broker returned an invalid grant", "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "SCM_BROKER_FAILED", "A repository checkout grant could not be issued.")
		return
	}
	// No App broker configured: fall back to a stored PAT.
	if pat, ok := s.workerGitHubPATGrant(r.Context(), claims); ok {
		writeJSON(w, http.StatusOK, pat)
		return
	}
	writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Repository checkout is not available.")
}

func (s *Server) workerPushGrant(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:git") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:git scope is required.")
		return
	}
	// Prefer the App installation push grant; fall back to a stored PAT only when
	// the App path cannot serve this project. Same rationale as workerCheckoutGrant:
	// the App write token is minted fresh, while a cached-valid PAT may be stale.
	if s.checkoutBroker != nil {
		grant, err := s.checkoutBroker.IssuePushGrant(r.Context(), claims.OrgID, claims.SessionID)
		if err == nil && grant.Token != "" && grant.CloneURL != "" && grant.ExpiresAt.After(time.Now()) {
			writeJSON(w, http.StatusOK, worker.CheckoutGrantResponse{
				CloneURL: grant.CloneURL, Token: grant.Token, ExpiresAt: grant.ExpiresAt,
			})
			return
		}
		if pat, ok := s.workerGitHubPATGrant(r.Context(), claims); ok {
			writeJSON(w, http.StatusOK, pat)
			return
		}
		if errors.Is(err, postgres.ErrForbidden) || errors.Is(err, postgres.ErrNotFound) {
			writeError(w, r, http.StatusForbidden, "PUSH_NOT_AUTHORIZED", "This session does not have an active repository grant.")
			return
		}
		if err != nil {
			s.logger.Error("issue worker push grant", "error", err, "request_id", requestID(r))
			writeError(w, r, http.StatusBadGateway, "SCM_BROKER_FAILED", "A repository push grant could not be issued.")
			return
		}
		s.logger.Error("worker push broker returned an invalid grant", "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "SCM_BROKER_FAILED", "A repository push grant could not be issued.")
		return
	}
	// No App broker configured: fall back to a stored PAT.
	if pat, ok := s.workerGitHubPATGrant(r.Context(), claims); ok {
		writeJSON(w, http.StatusOK, pat)
		return
	}
	writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Repository push is not available.")
}

// workerGitHubToken gives worker-local Git tooling the same short-lived,
// repository-scoped write grant used by the explicit push bridge. The worker
// must request it on demand; no GitHub credential is persisted in the sandbox.
func (s *Server) workerGitHubToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:git") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:git scope is required.")
		return
	}
	// Prefer the GitHub App installation grant, falling back to a stored PAT only
	// when the App path cannot serve this project — the same precedence as
	// workerCheckoutGrant / workerPushGrant. This endpoint backs the sandbox git
	// credential helper, which git invokes for every fetch and push, so a PAT-first
	// order here lets a cached-valid-but-rotted PAT shadow a healthy App
	// installation and fail every git operation with "Authentication failed" even
	// though the App can push. The App token is minted fresh per request and never
	// goes stale, so it is the safe default; the broad PAT is the fallback for
	// projects the App cannot serve (no installation / not App-connected / a remote
	// broker that cannot push).
	if s.checkoutBroker != nil {
		// git invokes the credential helper with credential.useHttpPath=true, so
		// it can name the exact repository it is fetching or pushing. When it does,
		// mint an App token scoped to that repository (primary or a declared extra
		// the App is installed on); an App-uninstalled extra returns ErrForbidden
		// here and falls through to the PAT, giving the same App-first/PAT-fallback
		// precedence per repository. Without a repository (older helpers, the gh
		// CLI wrapper), fall back to the broad multi-repository push grant.
		repo := workerRequestedRepository(r)
		var (
			grant githubapp.CheckoutGrant
			err   error
		)
		if repo != "" {
			grant, err = s.checkoutBroker.IssuePushGrantForRepo(r.Context(), claims.OrgID, claims.SessionID, repo)
		} else {
			grant, err = s.checkoutBroker.IssuePushGrant(r.Context(), claims.OrgID, claims.SessionID)
		}
		if err == nil && grant.Token != "" && grant.ExpiresAt.After(time.Now()) {
			writeJSON(w, http.StatusOK, worker.GitHubTokenResponse{Token: grant.Token, ExpiresAt: grant.ExpiresAt})
			return
		}
		if pat, ok := s.workerGitHubPATGrant(r.Context(), claims); ok {
			writeJSON(w, http.StatusOK, worker.GitHubTokenResponse{Token: pat.Token, ExpiresAt: pat.ExpiresAt})
			return
		}
		if errors.Is(err, postgres.ErrForbidden) || errors.Is(err, postgres.ErrNotFound) {
			writeError(w, r, http.StatusForbidden, "PUSH_NOT_AUTHORIZED", "This session does not have an active repository grant.")
			return
		}
		if err != nil {
			s.logger.Error("issue worker GitHub token", "error", err, "request_id", requestID(r))
			writeError(w, r, http.StatusBadGateway, "SCM_BROKER_FAILED", "A GitHub credential could not be issued.")
			return
		}
		s.logger.Error("worker GitHub broker returned an invalid grant", "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "SCM_BROKER_FAILED", "A GitHub credential could not be issued.")
		return
	}
	// No App broker configured: fall back to a stored PAT.
	if grant, ok := s.workerGitHubPATGrant(r.Context(), claims); ok {
		writeJSON(w, http.StatusOK, worker.GitHubTokenResponse{Token: grant.Token, ExpiresAt: grant.ExpiresAt})
		return
	}
	writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "GitHub credentials are not available.")
}

// workerRequestedRepository extracts the "owner/repo" the git credential helper
// named via the ?repo= query parameter (git supplies host+path because the
// helper runs with credential.useHttpPath=true). It returns "" for a missing or
// malformed value, so the caller falls back to the broad multi-repository push
// grant rather than failing — the repository hint only ever narrows the grant's
// scope, it is never a hard requirement.
func workerRequestedRepository(r *http.Request) string {
	raw := strings.TrimSpace(r.URL.Query().Get("repo"))
	if raw == "" {
		return ""
	}
	raw = strings.TrimSuffix(strings.Trim(raw, "/"), ".git")
	owner, repo, ok := strings.Cut(raw, "/")
	if !ok ||
		owner == "" || repo == "" ||
		strings.ContainsAny(owner, "/ \t") ||
		strings.ContainsAny(repo, "/ \t") {
		return ""
	}
	return owner + "/" + repo
}

func (s *Server) workerRaisePullRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:git") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:git scope is required.")
		return
	}
	if s.checkoutBroker == nil && s.patWrites == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Raising a pull request is not available.")
		return
	}
	var input worker.RaisePullRequestRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.HeadBranch = strings.TrimSpace(input.HeadBranch)
	if input.Title == "" || len(input.Title) > 256 {
		writeError(w, r, http.StatusBadRequest, "INVALID_TITLE", "The pull request title must be 1-256 characters.")
		return
	}
	if input.HeadBranch == "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_HEAD_BRANCH", "The pushed branch name is required.")
		return
	}
	raiseInput := domain.RaisePullRequest{
		Title:      input.Title,
		Body:       input.Body,
		HeadBranch: input.HeadBranch,
		BaseBranch: input.BaseBranch,
		Repository: strings.Trim(strings.TrimSpace(input.Repository), "/"),
	}
	// Prefer the GitHub App (checkout broker) to open the PR: it uses a fresh
	// installation token that can't go stale. Fall back to the user's PAT only when
	// the broker cannot complete the write — a repository authorized through the
	// remote capability broker returns errRemotePushNotSupported, and some projects
	// are not App-connected. A PAT-first order let a cached-valid-but-rotted PAT
	// (validation_state is a cached snapshot) shadow a healthy App installation and
	// fail every PR with "pull request could not be opened", the same class of bug
	// the credential-grant endpoints avoid by being App-first.
	var (
		pr  domain.PullRequest
		err error
	)
	if s.checkoutBroker != nil {
		pr, err = s.checkoutBroker.RaisePullRequest(r.Context(), claims.OrgID, claims.SessionID, raiseInput)
	}
	if err != nil || s.checkoutBroker == nil {
		if grant, ok := s.patWriteGrant(r.Context(), claims); ok {
			var cloneURL string
			if cloneURL, err = s.patPullRequestCloneURL(r.Context(), claims, grant.CloneURL, raiseInput.Repository); err == nil {
				pr, err = s.patWrites.RaisePullRequest(
					r.Context(), claims.OrgID, claims.SessionID, cloneURL, grant.Token, raiseInput,
				)
			}
		} else if s.checkoutBroker == nil {
			writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Raising a pull request is not available.")
			return
		}
	}
	if errors.Is(err, postgres.ErrForbidden) || errors.Is(err, postgres.ErrNotFound) {
		writeError(w, r, http.StatusForbidden, "PULL_REQUEST_NOT_AUTHORIZED", "This session does not have an active repository grant.")
		return
	}
	if errors.Is(err, postgres.ErrInvalid) {
		writeError(w, r, http.StatusBadRequest, "INVALID_PULL_REQUEST", "The pull request could not be opened with the given branches.")
		return
	}
	if err != nil {
		s.logger.Error("raise worker pull request", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "PULL_REQUEST_FAILED", "The pull request could not be opened.")
		return
	}
	s.appendSessionProjectionEvent(r.Context(), claims.OrgID, claims.SessionID, "pull_request.created", pr)
	s.recordWorkerPullRequestOpened(r.Context(), claims.OrgID, pr)
	writeJSON(w, http.StatusCreated, worker.RaisePullRequestResponse{
		ID:         pr.ID,
		Number:     pr.Number,
		HTMLURL:    pr.URL,
		HeadBranch: pr.SourceBranch,
		BaseBranch: pr.TargetBranch,
	})
}

// workerClaimPullRequest records a pull request opened by worker-side tooling
// such as the GitHub CLI. GitHub has already created the PR by this point; this
// route makes the control plane the authoritative place that associates it
// with the worker/session and wakes the inspector projection.
func (s *Server) workerClaimPullRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:git") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:git scope is required.")
		return
	}
	if s.checkoutBroker == nil && s.patWrites == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Pull request tracking is not available.")
		return
	}
	var input worker.ClaimPullRequestRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Reference = strings.TrimSpace(input.Reference)
	if input.Reference == "" || len(input.Reference) > 2048 {
		writeError(w, r, http.StatusBadRequest, "INVALID_PULL_REQUEST", "A pull request number or URL is required.")
		return
	}
	// Prefer the GitHub App (checkout broker) to claim, falling back to the user's
	// PAT only when the broker cannot complete it — the same App-first/PAT-fallback
	// precedence as workerRaisePullRequest / the credential-grant endpoints. A
	// PAT-first order here let a cached-valid-but-rotted PAT (validation_state is a
	// cached snapshot) shadow a healthy App installation and fail every claim with
	// "The pull request could not be tracked" (GitHub 401) even though the App can
	// track it — the same class of bug the raise/merge/token paths avoid by being
	// App-first. The App token is minted fresh per request and never goes stale.
	var (
		pr  domain.PullRequest
		err error
	)
	if s.checkoutBroker != nil {
		pr, err = s.checkoutBroker.ClaimPullRequest(r.Context(), claims.OrgID, claims.SessionID, input.Reference)
	}
	if err != nil || s.checkoutBroker == nil {
		if grant, ok := s.patWriteGrant(r.Context(), claims); ok {
			referenced, _, parseErr := githubapp.ParsePullRequestTarget(input.Reference)
			var cloneURL string
			if cloneURL, err = s.patPullRequestCloneURL(r.Context(), claims, grant.CloneURL, referenced); parseErr == nil && err == nil {
				pr, err = s.patWrites.ClaimPullRequest(
					r.Context(), claims.OrgID, claims.SessionID, cloneURL, grant.Token, input.Reference,
				)
			} else if parseErr != nil {
				err = postgres.ErrInvalid
			} else if errors.Is(err, postgres.ErrForbidden) {
				err = postgres.ErrInvalid
			}
		} else if s.checkoutBroker == nil {
			writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Pull request tracking is not available.")
			return
		}
	}
	if errors.Is(err, postgres.ErrForbidden) || errors.Is(err, postgres.ErrNotFound) {
		writeError(w, r, http.StatusForbidden, "PULL_REQUEST_NOT_AUTHORIZED", "This session does not have an active repository grant.")
		return
	}
	if errors.Is(err, postgres.ErrInvalid) {
		writeError(w, r, http.StatusBadRequest, "INVALID_PULL_REQUEST", "The pull request reference is invalid for this repository.")
		return
	}
	if err != nil {
		s.logger.Error("claim worker pull request", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "PULL_REQUEST_FAILED", "The pull request could not be tracked.")
		return
	}
	s.appendSessionProjectionEvent(r.Context(), claims.OrgID, claims.SessionID, "pull_request.claimed", pr)
	s.recordWorkerPullRequestOpened(r.Context(), claims.OrgID, pr)
	writeJSON(w, http.StatusOK, worker.ClaimPullRequestResponse{
		ID: pr.ID, Number: pr.Number, HTMLURL: pr.URL,
	})
}

func (s *Server) workerReportGitRefs(w http.ResponseWriter, r *http.Request) {
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:git") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:git scope is required.")
		return
	}
	var input worker.ReportGitRefsRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(input.Refs) > 128 {
		writeError(w, r, http.StatusBadRequest, "INVALID_GIT_REFS", "Too many branch heads.")
		return
	}
	refs := make([]domain.WorkerGitRef, 0, len(input.Refs))
	seen := make(map[string]struct{}, len(input.Refs))
	for _, ref := range input.Refs {
		branch := strings.TrimSpace(ref.Branch)
		if branch == "" || len(branch) > 255 || len(ref.SHA) != 40 || strings.ContainsAny(branch, "\x00\r\n") {
			writeError(w, r, http.StatusBadRequest, "INVALID_GIT_REFS", "Invalid branch head.")
			return
		}
		if _, err := hex.DecodeString(ref.SHA); err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_GIT_REFS", "Invalid branch head.")
			return
		}
		if _, exists := seen[branch]; exists {
			writeError(w, r, http.StatusBadRequest, "INVALID_GIT_REFS", "Duplicate branch head.")
			return
		}
		seen[branch] = struct{}{}
		refs = append(refs, domain.WorkerGitRef{Branch: branch, SHA: ref.SHA})
	}
	store, ok := s.store.(interface {
		WorkerGitHubCheckoutContext(context.Context, string, string) (domain.GitHubCheckoutContext, error)
		ReplaceWorkerGitRefs(context.Context, string, string, int64, []domain.WorkerGitRef) error
	})
	if !ok || s.github == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SCM_UNAVAILABLE", "Pull request tracking is not available.")
		return
	}
	checkout, err := store.WorkerGitHubCheckoutContext(r.Context(), claims.OrgID, claims.SessionID)
	if err != nil || checkout.GitHubRepositoryID <= 0 {
		writeError(w, r, http.StatusForbidden, "REPOSITORY_NOT_AUTHORIZED", "This session has no active repository grant.")
		return
	}
	if err := store.ReplaceWorkerGitRefs(r.Context(), claims.OrgID, claims.SessionID, checkout.GitHubRepositoryID, refs); err != nil {
		s.logger.Error("record worker branch heads", "error", err)
		writeError(w, r, http.StatusInternalServerError, "GIT_REFS_FAILED", "Branch heads could not be recorded.")
		return
	}
	if err := s.github.ReconcileWorkerGitRefs(r.Context(), claims.OrgID, checkout.GitHubRepositoryID, refs); err != nil {
		s.logger.Error("reconcile worker branch heads with webhooks", "error", err)
		writeError(w, r, http.StatusBadGateway, "PR_RECONCILE_FAILED", "Pull request webhooks could not be reconciled.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// The GitHub App webhook can be delivered to a different environment from the
// worker that opened the PR. Record the bell notification at the worker write
// boundary so it does not depend on webhook routing.
func (s *Server) recordWorkerPullRequestOpened(ctx context.Context, orgID string, pr domain.PullRequest) {
	if err := s.store.RecordPullRequestOpened(ctx, orgID, pr, "worker:"+pr.ID); err != nil {
		// GitHub may already have created the PR, so do not report the operation as
		// failed solely because its notification could not be written.
		s.logger.Error("record worker pull request notification", "error", err, "pull_request_id", pr.ID)
	}
}

func (s *Server) workerSubmitReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:git") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:git scope is required.")
		return
	}
	if s.checkoutBroker == nil && s.patWrites == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Submitting a review is not available.")
		return
	}
	reviewRunID := chi.URLParam(r, "reviewRunId")
	if requireUUID(reviewRunID, "reviewRunId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "reviewRunId must be a UUID.")
		return
	}
	var input worker.SubmitReviewRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result := domain.SubmitReviewResult{Verdict: contract.AOReviewVerdict(strings.TrimSpace(input.Verdict)), Body: input.Body}
	var (
		run domain.ReviewRun
		err error
	)
	if grant, ok := s.patWriteGrant(r.Context(), claims); ok {
		run, err = s.patWrites.SubmitReview(r.Context(), claims.OrgID, claims.SessionID, reviewRunID, grant.Token, result)
	} else if s.checkoutBroker != nil {
		run, err = s.checkoutBroker.SubmitReview(r.Context(), claims.OrgID, claims.SessionID, reviewRunID, result)
	} else {
		writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Submitting a review is not available.")
		return
	}
	if errors.Is(err, postgres.ErrForbidden) || errors.Is(err, postgres.ErrNotFound) {
		writeError(w, r, http.StatusForbidden, "REVIEW_NOT_AUTHORIZED", "This session may not submit a verdict for this review.")
		return
	}
	if errors.Is(err, postgres.ErrInvalid) {
		writeError(w, r, http.StatusBadRequest, "INVALID_REVIEW", "The review verdict could not be recorded.")
		return
	}
	if err != nil {
		s.logger.Error("submit worker review", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "REVIEW_FAILED", "The review could not be delivered.")
		return
	}
	s.appendSessionProjectionEvent(r.Context(), claims.OrgID, claims.SessionID, "review.submitted", run)
	writeJSON(w, http.StatusOK, worker.SubmitReviewResponse{ID: run.ID, Status: string(run.Status)})
}

// appendSessionProjectionEvent makes a completed worker-side state change
// observable to connected browser projections. The source of truth remains the
// normal database record; event persistence is deliberately best-effort here
// so an event-stream outage cannot turn a successful GitHub operation into a
// failed worker command.
func (s *Server) appendSessionProjectionEvent(
	ctx context.Context, orgID, sessionID, eventType string, payload any,
) {
	if s.store == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		s.logger.Error("marshal session projection event", "event_type", eventType, "error", err)
		return
	}
	if _, err := s.store.AppendSessionEvent(ctx, orgID, sessionID, eventType, raw); err != nil {
		s.logger.Error("append session projection event", "event_type", eventType, "error", err)
	}
}

// workerEvent publishes one worker-originated event onto the session stream.
func (s *Server) workerEvent(w http.ResponseWriter, r *http.Request) {
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:event") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:event scope is required.")
		return
	}
	var input struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := decodeJSONLimit(w, r, &input, maxWorkerOutput+maxWorkerControlBody); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Type = strings.TrimSpace(input.Type)
	if !allowedWorkerEventType(input.Type) {
		writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_TYPE", "The worker event type is not allowed.")
		return
	}
	// ao_events.payload is constrained to a JSON object. Unmarshalling into a
	// map is not enough of a check on its own: JSON null unmarshals into a nil
	// map without error, so it would pass here and then fail the constraint as
	// a 500 rather than being refused as the bad request it is.
	if len(input.Payload) > 0 {
		var object map[string]any
		if err := json.Unmarshal(input.Payload, &object); err != nil || object == nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The worker event payload must be a JSON object.")
			return
		}
	}
	switch input.Type {
	case "agent.activity":
		var activity worker.ActivityEvent
		if err := json.Unmarshal(input.Payload, &activity); err != nil ||
			!worker.ValidActivityEvent(activity) {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The agent activity payload is invalid.")
			return
		}
		launch, err := s.store.WorkerLaunchSpec(
			r.Context(), claims.OrgID, claims.SessionID,
		)
		if err != nil {
			s.writeWorkerStoreError(w, r, err)
			return
		}
		if activity.Harness != launch.Harness {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The activity harness does not match this session.")
			return
		}
		err = s.store.SetWorkerActivity(
			r.Context(),
			claims.OrgID,
			claims.SessionID,
			claims.WorkerID,
			claims.Epoch,
			activity,
		)
		if err != nil {
			s.writeWorkerStoreError(w, r, err)
			return
		}
		if err := s.store.AppendInteractiveConversationFacts(r.Context(), claims.OrgID, claims.SessionID,
			activity.Event, activity.SourceInterface, activity.LatestUserPrompt, activity.LatestAssistantUpdate); err != nil {
			s.writeWorkerStoreError(w, r, err)
			return
		}
		s.appendSessionProjectionEvent(
			r.Context(), claims.OrgID, claims.SessionID, input.Type, activity,
		)
	case "worker.ready", "agent.ready":
		var ready worker.ReadyEvent
		if err := json.Unmarshal(input.Payload, &ready); err != nil ||
			ready.WorkerID != claims.WorkerID ||
			ready.Epoch != claims.Epoch ||
			strings.TrimSpace(ready.Version) == "" ||
			len(ready.Version) > 100 ||
			len(ready.Capabilities) > 64 {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The worker.ready payload is invalid.")
			return
		}
		if _, err := s.store.AppendSessionEvent(
			r.Context(), claims.OrgID, claims.SessionID, input.Type, input.Payload,
		); err != nil {
			s.writeWorkerStoreError(w, r, err)
			return
		}
	case worker.SetupEventType:
		var outcome worker.SetupEvent
		if err := json.Unmarshal(input.Payload, &outcome); err != nil ||
			(outcome.Status != worker.SetupSucceeded && outcome.Status != worker.SetupFailed &&
				outcome.Status != worker.SetupTimedOut) || outcome.Step < 0 ||
			len(input.Payload) > maxWorkerControlBody {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The setup payload is invalid.")
			return
		}
		// The same redaction as each step's events (worker.RedactScriptOutput).
		outcome.Output = activitylog.Excerpt(worker.RedactScriptOutput(outcome.Output), 2048)
		outcome.Command = activitylog.Truncate(worker.RedactScriptOutput(outcome.Command), 512)
		outcome.Repository = activitylog.Truncate(outcome.Repository, 256)
		outcome.Dir = activitylog.Truncate(outcome.Dir, 1024)
		outcome.User = activitylog.Truncate(outcome.User, 128)
		outcome.LogPath = activitylog.Truncate(outcome.LogPath, 1024)
		outcome.Hint = activitylog.Truncate(outcome.Hint, 512)
		if len(outcome.Groups) > 32 {
			outcome.Groups = outcome.Groups[:32]
		}
		for index, group := range outcome.Groups {
			outcome.Groups[index] = activitylog.Truncate(group, 64)
		}
		if !domain.ValidScriptStepTimeoutMinutes(outcome.TimeoutMinutes) {
			outcome.TimeoutMinutes = 0
		}
		logArgs := []any{
			"org_id", claims.OrgID, "session_id", claims.SessionID, "status", outcome.Status,
			"step", outcome.Step, "exit_code", outcome.ExitCode, "duration_ms", outcome.DurationMS,
		}
		if outcome.Status == worker.SetupSucceeded {
			s.logger.Info("project setup finished", logArgs...)
		} else {
			s.logger.Warn("project setup did not succeed", append(logArgs,
				"command", outcome.Command, "repository", outcome.Repository, "dir", outcome.Dir,
				"user", outcome.User, "groups", strings.Join(outcome.Groups, " "),
				"log", outcome.LogPath, "hint", outcome.Hint, "output", outcome.Output)...)
		}
		payload, err := json.Marshal(outcome)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The setup payload is invalid.")
			return
		}
		if _, err := s.store.AppendSessionEvent(r.Context(), claims.OrgID, claims.SessionID, input.Type, payload); err != nil {
			s.writeWorkerStoreError(w, r, err)
			return
		}
	case worker.ScriptStepEventType:
		// A worker reports only its Setup steps: Cleanup runs while the
		// control plane deletes the workspace, which reports those itself.
		// The step's output is redacted again and bounded before it is kept.
		var step worker.ScriptStepEvent
		if err := json.Unmarshal(input.Payload, &step); err != nil ||
			!worker.ValidScriptStepEvent(step) || step.Phase != worker.ScriptPhaseSetup {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The script step payload is invalid.")
			return
		}
		// A worker runs Setup before its coding agent starts and never again
		// in the same epoch, so a running step from an epoch whose agent
		// started (it reported agent.ready, or claimed a Chat turn) is not the
		// worker's (the agent can reach the worker's credential): it would keep
		// the session reading as working. A step's final event is still taken,
		// as one sent just before the agent started may land just after.
		if step.Status == worker.ScriptStepRunning {
			if started, ok := s.store.(workerAgentStartedStore); ok {
				agentStarted, err := started.WorkerAgentStarted(r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch)
				if err != nil {
					s.writeWorkerStoreError(w, r, err)
					return
				}
				if agentStarted {
					writeError(w, r, http.StatusConflict, "AGENT_ALREADY_STARTED",
						"This worker's coding agent already started, so its Setup is no longer running.")
					return
				}
			}
		}
		// When the step last wrote output, by this control plane's clock.
		step = worker.StampScriptStepOutputAt(step, time.Now())
		payload, err := json.Marshal(worker.SanitizeScriptStepEvent(step))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The script step payload is invalid.")
			return
		}
		if _, err := s.store.AppendSessionEvent(r.Context(), claims.OrgID, claims.SessionID, input.Type, payload); err != nil {
			s.writeWorkerStoreError(w, r, err)
			return
		}
	case worker.WorkspaceStartupEventType:
		var startup worker.WorkspaceStartupEvent
		if err := json.Unmarshal(input.Payload, &startup); err != nil ||
			len(input.Payload) > maxWorkerOutput+maxWorkerControlBody ||
			(startup.Status == worker.WorkspaceStartupFailed &&
				(!domain.IsWorkspaceStartupErrorCode(startup.Code) || strings.TrimSpace(startup.Message) == "")) ||
			(startup.Status != worker.WorkspaceStartupFailed && startup.Status != worker.WorkspaceStartupReady) {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The workspace startup payload is invalid.")
			return
		}
		var err error
		if startup.Status == worker.WorkspaceStartupReady {
			err = s.store.ClearWorkspaceStartupError(r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch)
		} else {
			// The script redaction: a Setup failure's detail is its output.
			// RedactScriptOutput strips escapes and control characters before
			// it masks, so a colour code between a secret's name and its value
			// never hides the pair.
			message := activitylog.Truncate(worker.RedactScriptOutput(strings.TrimSpace(startup.Message)), 1024)
			detail := activitylog.Excerpt(worker.RedactScriptOutput(startup.Detail), 4096)
			s.logger.Warn("workspace startup failed in the worker",
				"org_id", claims.OrgID, "session_id", claims.SessionID, "code", startup.Code, "message", message)
			err = s.store.RecordWorkspaceStartupError(r.Context(), claims.OrgID, claims.SessionID,
				claims.WorkerID, claims.Epoch, startup.Code, message, detail)
		}
		if err != nil {
			s.writeWorkerStoreError(w, r, err)
			return
		}
	case "chat.activity":
		var output worker.OutputEvent
		if err := json.Unmarshal(input.Payload, &output); err != nil ||
			requireUUID(output.TurnID, "turnId") != nil || output.Attempt <= 0 ||
			output.Activity == nil || output.Activity.ID == "" || len(output.Activity.ID) > 256 ||
			len(input.Payload) > maxWorkerOutput+maxWorkerControlBody {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The chat activity payload is invalid.")
			return
		}
		if err := s.store.AppendWorkerTurnActivity(r.Context(), claims.OrgID, claims.SessionID,
			claims.WorkerID, output.TurnID, claims.Epoch, output.Attempt, *output.Activity); err != nil {
			s.writeWorkerStoreError(w, r, err)
			return
		}
	case "chat.assistant_delta":
		var output worker.OutputEvent
		if err := json.Unmarshal(input.Payload, &output); err != nil ||
			requireUUID(output.TurnID, "turnId") != nil ||
			output.Attempt <= 0 ||
			(output.Stream != "stdout" && output.Stream != "stderr") ||
			output.Text == "" ||
			len(output.Text) > maxWorkerOutput {
			writeError(w, r, http.StatusBadRequest, "INVALID_EVENT_PAYLOAD", "The assistant output payload is invalid.")
			return
		}
		if err := s.store.AppendWorkerTurnOutput(
			r.Context(),
			claims.OrgID,
			claims.SessionID,
			claims.WorkerID,
			output.TurnID,
			claims.Epoch,
			output.Attempt,
			output.Stream,
			output.Text,
			output.ItemID,
		); err != nil {
			s.writeWorkerStoreError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func allowedWorkerEventType(eventType string) bool {
	if eventType == "" || len(eventType) > maxWorkerEventType {
		return false
	}
	_, allowed := workerEventTypes[eventType]
	return allowed
}

func (s *Server) workerClaimTurn(w http.ResponseWriter, r *http.Request) {
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:turn:claim") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:turn:claim scope is required.")
		return
	}
	var input worker.ClaimTurnRequest
	if err := decodeJSONLimit(w, r, &input, maxWorkerControlBody); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	turn, ok, err := s.store.ClaimWorkerTurn(
		r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch,
	)
	if err != nil {
		s.writeWorkerStoreError(w, r, err)
		return
	}
	response := worker.ClaimTurnResponse{}
	if ok {
		response.Turn = &worker.Turn{
			ID:              turn.ID,
			Prompt:          turn.Prompt,
			Model:           turn.Model,
			ReasoningEffort: turn.ReasoningEffort,
			Mode:            turn.Mode,
			ApprovalMode:    turn.ApprovalMode,
			DeniedCommands:  turn.DeniedCommands,
			Harness:         turn.Harness,
			Attempt:         turn.Attempt,
			CancelRequested: turn.CancelRequested,
			AgentSessionID:  turn.AgentSessionID,
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) workerTurnCancellation(w http.ResponseWriter, r *http.Request) {
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:turn:poll") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:turn:poll scope is required.")
		return
	}
	turnID := chi.URLParam(r, "turnId")
	attempt, err := strconv.Atoi(r.URL.Query().Get("attempt"))
	if requireUUID(turnID, "turnId") != nil || err != nil || attempt <= 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "A valid turnId and attempt are required.")
		return
	}
	requested, err := s.store.WorkerTurnCancellationRequested(
		r.Context(),
		claims.OrgID,
		claims.SessionID,
		claims.WorkerID,
		turnID,
		claims.Epoch,
		attempt,
	)
	if err != nil {
		s.writeWorkerStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, worker.CancellationResponse{Requested: requested})
}

func (s *Server) workerCompleteTurn(w http.ResponseWriter, r *http.Request) {
	s.workerFinishTurn(w, r, "completed")
}

func (s *Server) workerFailTurn(w http.ResponseWriter, r *http.Request) {
	s.workerFinishTurn(w, r, "failed")
}

func (s *Server) workerFinishTurn(w http.ResponseWriter, r *http.Request, outcome string) {
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:turn:complete") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:turn:complete scope is required.")
		return
	}
	turnID := chi.URLParam(r, "turnId")
	if requireUUID(turnID, "turnId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "turnId must be a UUID.")
		return
	}
	attempt := 0
	errorMessage := ""
	if outcome == "failed" {
		var input worker.FailTurnRequest
		if err := decodeJSONLimit(w, r, &input, maxWorkerError+maxWorkerControlBody); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
			return
		}
		attempt = input.Attempt
		errorMessage = strings.TrimSpace(input.Error)
		if errorMessage == "" || len(errorMessage) > maxWorkerError {
			writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The failure message is invalid.")
			return
		}
	} else {
		var input worker.FinishTurnRequest
		if err := decodeJSONLimit(w, r, &input, maxWorkerControlBody); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
			return
		}
		attempt = input.Attempt
		if input.Cancelled {
			outcome = "cancelled"
		}
	}
	if attempt <= 0 {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "attempt must be positive.")
		return
	}
	alreadyFinished, err := s.store.FinishWorkerTurn(
		r.Context(),
		claims.OrgID,
		claims.SessionID,
		claims.WorkerID,
		turnID,
		claims.Epoch,
		attempt,
		outcome,
		errorMessage,
	)
	if err != nil {
		s.writeWorkerStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, worker.FinishTurnResponse{
		OK:              true,
		AlreadyFinished: alreadyFinished,
	})
}

type workerReviewCredentialStore interface {
	WorkerReviewCredential(context.Context, string, string, string, int64, string) (domain.WorkerCredential, error)
}

func (s *Server) workerCredential(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:credential:read") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:credential:read scope is required.")
		return
	}
	if s.secretCipher == nil {
		writeError(w, r, http.StatusServiceUnavailable, "CREDENTIALS_UNAVAILABLE", "Coding-agent credentials are unavailable.")
		return
	}
	// A worker redeems only its own session's harness credential, or the
	// snapshotted harness of a running review it owns. There is deliberately no
	// arbitrary-provider selector: the worker token is readable by the agent, so
	// one would let a compromised session pull every connected credential.
	if r.URL.Query().Has("provider") {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "provider is not supported.")
		return
	}
	reviewRunID := r.URL.Query().Get("reviewRunId")
	var credential domain.WorkerCredential
	var err error
	if reviewRunID != "" {
		if requireUUID(reviewRunID, "reviewRunId") != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "reviewRunId must be a UUID.")
			return
		}
		store, ok := s.store.(workerReviewCredentialStore)
		if !ok {
			writeError(w, r, http.StatusNotImplemented, "not_implemented", "Reviewer credentials are unavailable.")
			return
		}
		credential, err = store.WorkerReviewCredential(r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch, reviewRunID)
	} else {
		credential, err = s.store.WorkerAgentCredential(r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch)
	}
	if err != nil {
		s.writeWorkerStoreError(w, r, err)
		return
	}
	if !validAgentProvider(credential.Provider) ||
		!validAgentCredentialType(credential.Provider, credential.CredentialType) {
		writeError(w, r, http.StatusUnprocessableEntity, "INVALID_CREDENTIAL", "The selected coding-agent credential is invalid.")
		return
	}
	secretOwner := claims.OrgID
	if credential.OwnerUserID != "" {
		secretOwner = "user:" + credential.OwnerUserID
	}
	plaintext, err := s.secretCipher.Decrypt(
		credential.EncryptedSecret,
		credential.Nonce,
		providerSecretAssociatedData(secretOwner, credential.Provider),
	)
	if err != nil {
		s.logger.Error("decrypt worker credential", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusInternalServerError, "CREDENTIAL_DECRYPTION_FAILED", "The coding-agent credential could not be decrypted.")
		return
	}
	defer clear(plaintext)
	writeJSON(w, http.StatusOK, worker.CredentialResponse{
		Provider:       credential.Provider,
		CredentialType: credential.CredentialType,
		Secret:         string(plaintext),
	})
}

func (s *Server) writeWorkerStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, postgres.ErrStaleWorker) {
		writeError(w, r, http.StatusUnauthorized, "STALE_WORKER_TOKEN", "The worker credential has been replaced.")
		return
	}
	s.writeStoreError(w, r, err)
}

// workerTokenTTL is how long an issued worker credential stays valid. An
// operator who shortens it gets a shorter blast radius on a leaked token at the
// cost of more renewals; an unset value falls back to the protocol default
// rather than to zero, which Issue would treat as "no lifetime at all".
func (s *Server) workerTokenTTL() time.Duration {
	if s.workerTokenLifetime > 0 {
		return s.workerTokenLifetime
	}
	return worker.DefaultTokenTTL
}

func workspacePathFor(resolvesWorkspacePath bool) string {
	if resolvesWorkspacePath {
		return worker.WorkspacePathPlaceholder
	}
	return worker.LegacyWorkspacePath
}

// workerExtraReposStore reads the additional repositories a session's project
// declares (postgres.Store.WorkerSessionExtraRepos).
type workerExtraReposStore interface {
	WorkerSessionExtraRepos(ctx context.Context, orgID, sessionID string) ([]domain.RepoRef, error)
}

// patPullRequestCloneURL is the repository a personal-access-token pull
// request write targets: the session's main repository (primaryCloneURL) when
// requested is empty or names it, otherwise the project's additional
// repository requested names (owner/name). Any other repository is
// postgres.ErrForbidden: a session only opens pull requests in repositories it
// checked out.
func (s *Server) patPullRequestCloneURL(ctx context.Context, claims worker.Claims, primaryCloneURL, requested string) (string, error) {
	if domain.IsStandaloneRepositoryURL(primaryCloneURL) {
		// A standalone project has no repository to open pull requests in.
		return "", postgres.ErrForbidden
	}
	requested = strings.Trim(strings.TrimSpace(requested), "/")
	if requested == "" {
		return primaryCloneURL, nil
	}
	if _, primary, err := domain.CanonicalGitHubRepoURL(strings.TrimSuffix(primaryCloneURL, ".git")); err == nil &&
		strings.EqualFold(primary, requested) {
		return primaryCloneURL, nil
	}
	store, ok := s.store.(workerExtraReposStore)
	if !ok {
		return "", postgres.ErrForbidden
	}
	extras, err := store.WorkerSessionExtraRepos(ctx, claims.OrgID, claims.SessionID)
	if err != nil {
		return "", err
	}
	for _, extra := range extras {
		canonical, fullName, err := domain.CanonicalGitHubRepoURL(extra.URL)
		if err == nil && strings.EqualFold(fullName, requested) {
			return canonical + ".git", nil
		}
	}
	return "", postgres.ErrForbidden
}

// requestedOutputFor is the session's requested output while cloud artifacts
// are on, and empty otherwise.
func requestedOutputFor(launch domain.WorkerLaunch, artifacts bool) string {
	if !artifacts {
		return ""
	}
	return launch.RequestedOutput
}
