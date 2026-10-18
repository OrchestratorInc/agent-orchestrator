package session

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// applyProviderWorkers bounds how many sessions relaunch concurrently.
// ponytail: fixed constant, no config knob; raise only if batch applies of
// large boards measurably stall.
const applyProviderWorkers = 3

// ProviderStaleness is one running claude-code session whose agent process was
// launched with a different gateway identity than a relaunch would pick up now.
type ProviderStaleness struct {
	SessionID      domain.SessionID `json:"sessionId"`
	DisplayName    string           `json:"displayName,omitempty"`
	Mode           string           `json:"mode"`
	StampBaseURL   string           `json:"stampBaseUrl,omitempty"`
	CurrentBaseURL string           `json:"currentBaseUrl,omitempty"`
	StampModel     string           `json:"stampModel,omitempty"`
	CurrentModel   string           `json:"currentModel,omitempty"`
}

// ProviderApplyResult is the per-session outcome of applying a provider switch.
// A failed apply leaves the session exited and restorable — resume failures
// never terminate the session.
type ProviderApplyResult struct {
	SessionID domain.SessionID `json:"sessionId"`
	State     string           `json:"state" enum:"applied,skipped,failed"`
	Error     string           `json:"error,omitempty"`
}

const (
	providerApplyApplied = "applied"
	providerApplySkipped = "skipped"
	providerApplyFailed  = "failed"
)

// resolveCurrentStamp resolves the gateway identity a relaunch of the session
// would pick up now. Injectable for tests so staleness never depends on the
// developer's real ~/.claude. nil uses agentcreds.ResolveClaudeSettings.
//
// ponytail: resolved from the project env only, not the full launch env —
// a per-agent env override of ANTHROPIC_* would produce a false stale, which
// costs one extra relaunch, never data.
func (s *Service) resolveCurrentStamp(ctx context.Context, workingDir string, projectEnv map[string]string) (string, string) {
	if s.resolveStampOverride != nil {
		return s.resolveStampOverride(ctx, workingDir, projectEnv)
	}
	resolved := agentcreds.ResolveClaudeSettings(ctx, workingDir, projectEnv, agentcreds.ResolveOptions{})
	return resolved.Env["ANTHROPIC_BASE_URL"], resolved.Env["ANTHROPIC_MODEL"]
}

// ProviderStaleness lists running claude-code sessions whose provider stamp
// differs from what a relaunch would resolve now.
func (s *Service) ProviderStaleness(ctx context.Context) ([]ProviderStaleness, error) {
	records, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("provider staleness: %w", err)
	}
	// Non-nil so an empty result serializes as [] not null (the response
	// schema promises an array).
	out := []ProviderStaleness{}
	for _, rec := range records {
		stamp, current, ok, err := s.compareProviderStamp(ctx, rec)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, ProviderStaleness{
			SessionID:      rec.ID,
			DisplayName:    rec.DisplayName,
			Mode:           string(rec.Mode),
			StampBaseURL:   stamp.baseURL,
			CurrentBaseURL: current.baseURL,
			StampModel:     stamp.model,
			CurrentModel:   current.model,
		})
	}
	return out, nil
}

type providerStamp struct {
	baseURL string
	model   string
}

func differentProviderStamp(a, b providerStamp) bool {
	return a.baseURL != b.baseURL || a.model != b.model
}

// eligibleProviderSwitchSession filters to sessions whose agent can actually be
// exited and resumed: live claude-code workers.
func eligibleProviderSwitchSession(rec domain.SessionRecord) bool {
	return rec.Harness == domain.HarnessClaudeCode &&
		!rec.IsTerminated &&
		rec.Activity.State != domain.ActivityExited &&
		!rec.IsTaskPreparation
}

func (s *Service) compareProviderStamp(ctx context.Context, rec domain.SessionRecord) (stamp, current providerStamp, stale bool, err error) {
	if !eligibleProviderSwitchSession(rec) {
		return stamp, current, false, nil
	}
	project, projectEnv, err := s.sessionProjectEnv(ctx, rec)
	if err != nil {
		return stamp, current, false, err
	}
	// The effective provider launch env — a role's provider pin, else the
	// default gateway entry — changes what a relaunch would resolve, so it
	// folds into the current-resolution env exactly as the launch path applies
	// it; a role switch then flags the affected sessions stale like any gateway
	// edit. The copy keeps the project env map unshared: the overlay must not
	// leak into other comparisons of the same project.
	env := projectEnv
	var entries []agentcreds.GatewayEntry
	if s.providerEntries != nil {
		entries = s.providerEntries(ctx)
	}
	if overlay := agentcreds.ProviderLaunchEnv(sessionmanager.RoleProviderPin(rec.Kind, project.Config), string(rec.ProjectID), entries); len(overlay) > 0 {
		env = make(map[string]string, len(projectEnv)+len(overlay))
		for key, value := range projectEnv {
			env[key] = value
		}
		for key, value := range overlay {
			env[key] = value
		}
	}
	stamp = providerStamp{baseURL: rec.Metadata.ProviderBaseURL, model: rec.Metadata.ProviderModel}
	currentBaseURL, currentModel := s.resolveCurrentStamp(ctx, rec.Metadata.WorkspacePath, env)
	current = providerStamp{baseURL: currentBaseURL, model: currentModel}
	// A pre-feature session has an empty stamp; if a gateway resolves now it
	// counts as stale — applying is harmless (exit+resume preserves context).
	// ponytail: an unknown stamp is treated as stale, not skipped, so legacy
	// sessions converge on the current provider instead of lingering.
	stale = differentProviderStamp(stamp, current) || (stamp.baseURL == "" && current.baseURL != "")
	return stamp, current, stale, nil
}

func (s *Service) sessionProjectEnv(ctx context.Context, rec domain.SessionRecord) (domain.ProjectRecord, map[string]string, error) {
	if rec.ProjectID == "" {
		return domain.ProjectRecord{}, nil, nil
	}
	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return domain.ProjectRecord{}, nil, fmt.Errorf("provider staleness: project %s: %w", rec.ProjectID, err)
	}
	if !ok {
		return domain.ProjectRecord{}, nil, nil
	}
	return project, project.Config.Env, nil
}

// ApplyProviderSwitch relaunches running claude-code sessions so they pick up
// the now-effective gateway configuration. Empty sessionIDs targets every
// stale session; explicit IDs are applied only when actually stale.
// A per-session failure (exit or resume) never affects the other sessions in
// the batch: a failed resume leaves the session exited and restorable.
func (s *Service) ApplyProviderSwitch(ctx context.Context, sessionIDs []domain.SessionID) ([]ProviderApplyResult, error) {
	if _, ok := s.manager.(exitAgentCommander); !ok {
		return nil, apierr.Conflict("AGENT_EXIT_UNSUPPORTED", "This build cannot exit an agent independently", nil)
	}
	targets := sessionIDs
	if len(targets) == 0 {
		stale, err := s.ProviderStaleness(ctx)
		if err != nil {
			return nil, err
		}
		targets = make([]domain.SessionID, 0, len(stale))
		for _, item := range stale {
			targets = append(targets, item.SessionID)
		}
	} else {
		// Dedup keeping first-occurrence order: a duplicated ID would
		// otherwise run two concurrent exit+resume cycles on one session.
		seen := make(map[domain.SessionID]bool, len(targets))
		unique := make([]domain.SessionID, 0, len(targets))
		for _, id := range targets {
			if !seen[id] {
				seen[id] = true
				unique = append(unique, id)
			}
		}
		targets = unique
	}
	results := make([]ProviderApplyResult, len(targets))
	type indexed struct {
		index  int
		result ProviderApplyResult
	}
	completions := make(chan indexed, len(targets))
	semaphore := make(chan struct{}, applyProviderWorkers)
	for index, id := range targets {
		go func(index int, id domain.SessionID) {
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				completions <- indexed{index: index, result: ProviderApplyResult{SessionID: id, State: providerApplyFailed, Error: ctx.Err().Error()}}
				return
			}
			completions <- indexed{index: index, result: s.applyProviderToSession(ctx, id)}
		}(index, id)
	}
	for range targets {
		res := <-completions
		results[res.index] = res.result
	}
	return results, nil
}

func (s *Service) applyProviderToSession(ctx context.Context, id domain.SessionID) ProviderApplyResult {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return ProviderApplyResult{SessionID: id, State: providerApplyFailed, Error: err.Error()}
	}
	if !ok {
		return ProviderApplyResult{SessionID: id, State: providerApplyFailed, Error: "session not found"}
	}
	if !eligibleProviderSwitchSession(rec) {
		return ProviderApplyResult{SessionID: id, State: providerApplySkipped, Error: "session is not a running claude-code session"}
	}
	_, _, stale, err := s.compareProviderStamp(ctx, rec)
	if err != nil {
		return ProviderApplyResult{SessionID: id, State: providerApplyFailed, Error: err.Error()}
	}
	if !stale {
		return ProviderApplyResult{SessionID: id, State: providerApplySkipped, Error: "session already runs the effective provider"}
	}
	manager, ok := s.manager.(exitAgentCommander)
	if !ok {
		return ProviderApplyResult{SessionID: id, State: providerApplyFailed, Error: "this build cannot exit an agent independently"}
	}
	if _, err := manager.ExitAgent(ctx, id); err != nil {
		return ProviderApplyResult{SessionID: id, State: providerApplyFailed, Error: err.Error()}
	}
	// A resume failure deliberately leaves the session exited: every failure
	// path in the relaunch commits before MarkSpawned, so the session stays
	// restorable and the error below is the user-facing signal.
	if _, err := s.manager.ResumeAgentWithMode(ctx, id); err != nil {
		return ProviderApplyResult{SessionID: id, State: providerApplyFailed, Error: err.Error()}
	}
	return ProviderApplyResult{SessionID: id, State: providerApplyApplied}
}
