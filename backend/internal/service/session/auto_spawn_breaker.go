package session

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// autoSpawnFailureLimit is how many automatic starts in a row may fail with
// the same error code for one launch configuration before automatic starts
// stop. A bad saved model or a missing login fails identically every time,
// and tracker intake and schedules used to retry it hundreds of times.
const autoSpawnFailureLimit = 3

// A paused key admits one automatic probe after this cooldown.
const autoSpawnCooldown = 30 * time.Minute

// automaticSpawn reports a start no person asked for just now: tracker intake
// (CreatedBy automation) or a schedule occurrence (AutomationRunID). Manual
// starts from the desktop, CLI or mobile, and agents running `ao spawn`, are
// never paused.
func automaticSpawn(cfg ports.SpawnConfig) bool {
	return cfg.AutomationRunID != nil || cfg.CreatedBy == domain.SessionCreatedByAutomation
}

type autoSpawnKey struct {
	project       domain.ProjectID
	harness       domain.AgentHarness
	model, effort string
	kind          domain.SessionKind
}

type autoSpawnStreak struct {
	code     string
	message  string
	count    int
	settings string // the project's agent settings when the streak began
	pausedAt time.Time
	probing  bool
}

// autoSpawnBreaker counts consecutive automatic start failures.
// ponytail: in memory, so a daemon restart allows three more attempts;
// persist it if restart loops turn out to matter.
type autoSpawnBreaker struct {
	mu      sync.Mutex
	streaks map[autoSpawnKey]*autoSpawnStreak
}

// current returns key's streak, dropping it when the project's agent settings
// changed since it began: a new model or harness deserves a fresh try.
func (b *autoSpawnBreaker) current(key autoSpawnKey, settings string) *autoSpawnStreak {
	streak := b.streaks[key]
	if streak != nil && streak.settings != settings {
		delete(b.streaks, key)
		return nil
	}
	return streak
}

// autoSpawnAttempt pins admission identity through synchronous and async outcomes.
type autoSpawnAttempt struct {
	key       autoSpawnKey
	state     *autoSpawnStreak
	automatic bool
	settled   bool // guarded by the breaker mutex
}
type autoSpawnAttemptKey struct{}

func autoSpawnAttemptFrom(ctx context.Context) *autoSpawnAttempt {
	attempt, _ := ctx.Value(autoSpawnAttemptKey{}).(*autoSpawnAttempt)
	return attempt
}

func (b *autoSpawnBreaker) begin(key autoSpawnKey, settings string, automatic bool, now time.Time) (*autoSpawnAttempt, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.streaks == nil {
		b.streaks = map[autoSpawnKey]*autoSpawnStreak{}
	}
	streak := b.current(key, settings)
	if streak == nil {
		if !automatic {
			return nil, false
		}
		streak = &autoSpawnStreak{settings: settings}
		b.streaks[key] = streak
	}
	if automatic && !streak.pausedAt.IsZero() {
		if streak.probing || now.Before(streak.pausedAt.Add(autoSpawnCooldown)) {
			return nil, true
		}
		streak.probing, streak.pausedAt = true, now
	}
	return &autoSpawnAttempt{key: key, state: streak, automatic: automatic}, false
}

// finish ignores duplicate outcomes and outcomes from a replaced generation.
// An empty code is a success or a transient failure and clears this streak.
func (b *autoSpawnBreaker) finish(attempt *autoSpawnAttempt, code, message string, now time.Time) bool {
	if attempt == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if attempt.settled {
		return false
	}
	attempt.settled = true
	streak := b.streaks[attempt.key]
	if streak != attempt.state {
		return false
	}
	if code == "" {
		// Retire the identity so already-running old attempts cannot recreate
		// the streak after a successful start or transient recovery.
		delete(b.streaks, attempt.key)
		return false
	}
	if !attempt.automatic {
		return false
	}
	if streak.code != code {
		streak.code, streak.count, streak.pausedAt = code, 0, time.Time{}
	}
	streak.count++
	streak.message = message
	if streak.probing || (streak.count >= autoSpawnFailureLimit && streak.pausedAt.IsZero()) {
		streak.probing = false
		streak.pausedAt = now
		return true
	}
	return false
}

// pauses lists the pauses in force for a project.
func (b *autoSpawnBreaker) pauses(project domain.ProjectID, settings string, now time.Time) []domain.AutoSpawnPause {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []domain.AutoSpawnPause
	for key := range b.streaks {
		if key.project != project {
			continue
		}
		if streak := b.current(key, settings); streak != nil && !streak.pausedAt.IsZero() && (streak.probing || now.Before(streak.pausedAt.Add(autoSpawnCooldown))) {
			out = append(out, streak.pause(key))
		}
	}
	return out
}

func (s *autoSpawnStreak) pause(key autoSpawnKey) domain.AutoSpawnPause {
	return domain.AutoSpawnPause{Harness: key.harness, Model: key.model, Effort: key.effort, Kind: string(key.kind), ErrorCode: s.code, Message: s.message, Failures: s.count, PausedAt: s.pausedAt}
}

// autoSpawnSettings fingerprints the project settings a start depends on:
// the default agent config and the per-role harness and agent config.
func autoSpawnSettings(project domain.ProjectRecord) string {
	settings, _ := json.Marshal([]any{project.Config.AgentConfig, project.Config.Worker, project.Config.Orchestrator})
	return string(settings)
}

func autoSpawnHarness(cfg ports.SpawnConfig, project domain.ProjectRecord) domain.AgentHarness {
	if cfg.Harness != "" {
		return cfg.Harness
	}
	if cfg.Kind == domain.KindOrchestrator {
		return project.Config.Orchestrator.Harness
	}
	return project.Config.Worker.Harness
}

// AutoSpawnPauses lists the automatic-start pauses in force for a project,
// for the project list the desktop reads.
func (s *Service) AutoSpawnPauses(ctx context.Context, project domain.ProjectRecord) []domain.AutoSpawnPause {
	return s.autoSpawns.pauses(domain.ProjectID(project.ID), autoSpawnSettings(project), s.now())
}

// spawnUnlessAutoPaused runs spawn, refusing an automatic start whose project
// launch configuration is paused, and keeps the breaker's counts.
func (s *Service) spawnUnlessAutoPaused(ctx context.Context, cfg ports.SpawnConfig, spawn func(context.Context) (domain.Session, int, int, error)) (domain.Session, int, int, error) {
	var attempt *autoSpawnAttempt
	if cfg.ProjectID != "" && s.store != nil {
		project, ok, err := s.store.GetProject(ctx, string(cfg.ProjectID))
		if err == nil && ok {
			harness := autoSpawnHarness(cfg, project)
			launch := cfg
			launch.Harness = harness
			tuning := sessionmanager.EffectiveSpawnAgentConfig(launch, project.Config)
			key := autoSpawnKey{project: cfg.ProjectID, harness: harness, model: tuning.Model, effort: tuning.Effort, kind: cfg.Kind}
			var paused bool
			attempt, paused = s.autoSpawns.begin(key, autoSpawnSettings(project), automaticSpawn(cfg), s.now())
			if paused {
				return domain.Session{}, 0, 0, apierr.Conflict("AUTO_SPAWN_PAUSED",
					"Automatic starts for this launch configuration are paused. A probe is allowed after a 30-minute cooldown. Start this configuration manually to retry sooner.",
					map[string]any{"harness": string(harness), "model": key.model, "effort": key.effort})
			}
			ctx = context.WithValue(ctx, autoSpawnAttemptKey{}, attempt)
		}
	}
	sess, promptBytes, systemPromptBytes, err := spawn(ctx)
	if err != nil || sess.ProvisionState != domain.SessionProvisionProvisioning {
		s.settleAutoSpawn(attempt, err == nil, err)
	}
	return sess, promptBytes, systemPromptBytes, err
}

// Settlement is in-memory and uses admission-time identity. It performs no
// blocking project lookup and cannot attach an old outcome to new settings.
func (s *Service) settleAutoSpawn(attempt *autoSpawnAttempt, ready bool, err error) {
	if attempt == nil {
		return
	}
	if ready {
		s.autoSpawns.finish(attempt, "", "", s.now())
		return
	}
	if !attempt.automatic {
		return
	}
	mapped := toSpawnAPIError(err)
	kind, code := telemetrymeta.ErrorKindAndCode(mapped)
	if code == "AUTO_SPAWN_PAUSED" {
		return
	}
	if kind == "internal" || errors.Is(err, context.Canceled) || !autoSpawnConfigFailure(code) {
		s.autoSpawns.finish(attempt, "", "", s.now())
		return
	}
	message := code
	var apiErr *apierr.Error
	if errors.As(mapped, &apiErr) && apiErr.Kind != apierr.KindInternal {
		message = apiErr.Message
	}
	if s.autoSpawns.finish(attempt, code, message, s.now()) && s.logger != nil {
		s.logger.Warn("session: automatic launch configuration paused", "project", attempt.key.project, "harness", attempt.key.harness, "code", code)
	}
}

func autoSpawnConfigFailure(code string) bool {
	switch code {
	case "UNSUPPORTED_MODEL", "UNSUPPORTED_EFFORT", "UNKNOWN_HARNESS", "AGENT_REQUIRED",
		"AGENT_BINARY_NOT_FOUND", "RUNTIME_PREREQUISITE_MISSING", "CHAT_PERMISSION_MODE_UNSUPPORTED",
		"SESSION_MODE_UNSUPPORTED", "CHAT_AUTH_REQUIRED", "AGENT_AUTH_REQUIRED", "TARGET_AGENT_UNAUTHORIZED",
		"CODEX_ACCOUNT_AUTH_UNVERIFIED", "AGENT_ACCOUNT_DEFAULT_UNUSABLE", "CHAT_DRIVER_INCOMPATIBLE":
		return true
	default:
		return false
	}
}
