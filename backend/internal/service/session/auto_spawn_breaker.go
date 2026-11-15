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

const autoSpawnFailureLimit = 3

const autoSpawnCooldown = 30 * time.Minute

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
	settings string
	pausedAt time.Time
	probing  bool
}

type autoSpawnBreaker struct {
	mu      sync.Mutex
	streaks map[autoSpawnKey]*autoSpawnStreak
}

func (b *autoSpawnBreaker) current(key autoSpawnKey, settings string) *autoSpawnStreak {
	streak := b.streaks[key]
	if streak != nil && streak.settings != settings {
		delete(b.streaks, key)
		return nil
	}
	return streak
}

type autoSpawnAttempt struct {
	key       autoSpawnKey
	state     *autoSpawnStreak
	automatic bool
	settled   bool
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

func (s *Service) AutoSpawnPauses(ctx context.Context, project domain.ProjectRecord) []domain.AutoSpawnPause {
	return s.autoSpawns.pauses(domain.ProjectID(project.ID), autoSpawnSettings(project), s.now())
}

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
