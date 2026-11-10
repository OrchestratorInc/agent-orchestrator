package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// autoSpawnFailureLimit is how many automatic starts in a row may fail with
// the same error code for one project and harness before automatic starts
// stop. A bad saved model or a missing login fails identically every time,
// and tracker intake and schedules used to retry it hundreds of times.
const autoSpawnFailureLimit = 3

// automaticSpawn reports a start no person asked for just now: tracker intake
// (CreatedBy automation) or a schedule occurrence (AutomationRunID). Manual
// starts from the desktop, CLI or mobile, and agents running `ao spawn`, are
// never paused.
func automaticSpawn(cfg ports.SpawnConfig) bool {
	return cfg.AutomationRunID != nil || cfg.CreatedBy == domain.SessionCreatedByAutomation
}

type autoSpawnKey struct {
	project domain.ProjectID
	harness domain.AgentHarness
}

type autoSpawnStreak struct {
	code     string
	message  string
	count    int
	settings string // the project's agent settings when the streak began
	pausedAt time.Time
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

// paused returns the pause in force for key, if any.
func (b *autoSpawnBreaker) paused(key autoSpawnKey, settings string) (domain.AutoSpawnPause, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	streak := b.current(key, settings)
	if streak == nil || streak.pausedAt.IsZero() {
		return domain.AutoSpawnPause{}, false
	}
	return streak.pause(key), true
}

// fail records one failed automatic start and reports whether it tripped
// the breaker just now.
func (b *autoSpawnBreaker) fail(key autoSpawnKey, settings, code, message string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.streaks == nil {
		b.streaks = map[autoSpawnKey]*autoSpawnStreak{}
	}
	streak := b.current(key, settings)
	if streak == nil || streak.code != code {
		streak = &autoSpawnStreak{code: code, settings: settings}
		b.streaks[key] = streak
	}
	streak.count++
	streak.message = message
	if streak.count >= autoSpawnFailureLimit && streak.pausedAt.IsZero() {
		streak.pausedAt = now
		return true
	}
	return false
}

// succeed clears every streak for the project and harness: any start that
// worked, manual or automatic, proves the configuration can launch.
func (b *autoSpawnBreaker) succeed(key autoSpawnKey) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.streaks, key)
}

// pauses lists the pauses in force for a project.
func (b *autoSpawnBreaker) pauses(project domain.ProjectID, settings string) []domain.AutoSpawnPause {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []domain.AutoSpawnPause
	for key := range b.streaks {
		if key.project != project {
			continue
		}
		if streak := b.current(key, settings); streak != nil && !streak.pausedAt.IsZero() {
			out = append(out, streak.pause(key))
		}
	}
	return out
}

func (s *autoSpawnStreak) pause(key autoSpawnKey) domain.AutoSpawnPause {
	return domain.AutoSpawnPause{Harness: key.harness, ErrorCode: s.code, Message: s.message, Failures: s.count, PausedAt: s.pausedAt}
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
	return s.autoSpawns.pauses(domain.ProjectID(project.ID), autoSpawnSettings(project))
}

// spawnUnlessAutoPaused runs spawn, refusing an automatic start whose project
// and harness are paused, and keeps the breaker's counts.
func (s *Service) spawnUnlessAutoPaused(ctx context.Context, cfg ports.SpawnConfig, spawn func() (domain.Session, int, int, error)) (domain.Session, int, int, error) {
	var key autoSpawnKey
	var settings string
	auto := automaticSpawn(cfg) && cfg.ProjectID != "" && s.store != nil
	if auto {
		project, ok, err := s.store.GetProject(ctx, string(cfg.ProjectID))
		if err == nil && ok {
			key, settings = autoSpawnKey{project: cfg.ProjectID, harness: autoSpawnHarness(cfg, project)}, autoSpawnSettings(project)
			if pause, paused := s.autoSpawns.paused(key, settings); paused {
				return domain.Session{}, 0, 0, apierr.Conflict("AUTO_SPAWN_PAUSED",
					fmt.Sprintf("Automatic starts for this project are paused after %d failed starts in a row (%s). Start a session yourself or change the project's agent or model settings to resume.", pause.Failures, pause.ErrorCode),
					map[string]any{"errorCode": pause.ErrorCode, "harness": string(pause.Harness)})
			}
		} else {
			auto = false
		}
	}
	sess, promptBytes, systemPromptBytes, err := spawn()
	switch {
	case err == nil:
		// An async start answers before it is usable; its outcome settles in
		// settleAsyncSpawn, which keeps the breaker for it.
		if sess.ProvisionState != domain.SessionProvisionProvisioning {
			s.autoSpawns.succeed(autoSpawnKey{project: sess.ProjectID, harness: sess.Harness})
		}
	case auto:
		s.recordAutoSpawnFailure(key, settings, err)
	}
	return sess, promptBytes, systemPromptBytes, err
}

// recordAutoSpawnFailure counts one failed automatic start. A cancelled start
// is not a failure, and a pause refusal is not a new attempt.
func (s *Service) recordAutoSpawnFailure(key autoSpawnKey, settings string, err error) {
	_, code := telemetrymeta.ErrorKindAndCode(toSpawnAPIError(err))
	if code == "" || code == "SPAWN_CANCELLED" || code == "AUTO_SPAWN_PAUSED" || errors.Is(err, context.Canceled) {
		return
	}
	message := code
	var apiErr *apierr.Error
	if errors.As(toSpawnAPIError(err), &apiErr) && apiErr.Kind != apierr.KindInternal {
		message = apiErr.Message
	}
	if s.autoSpawns.fail(key, settings, code, message, s.now()) && s.logger != nil {
		s.logger.Warn("session: automatic starts paused after repeated identical failures",
			"project", key.project, "harness", key.harness, "code", code, "failures", autoSpawnFailureLimit)
	}
}

// settleAutoSpawn keeps the breaker for an asynchronous start: a schedule's
// orchestrator start (always async) fails or succeeds in the background.
func (s *Service) settleAutoSpawn(rec domain.SessionRecord, ready bool, cause error) {
	key := autoSpawnKey{project: rec.ProjectID, harness: rec.Harness}
	if ready {
		s.autoSpawns.succeed(key)
		return
	}
	if rec.AutomationRunID == nil || s.store == nil {
		return
	}
	project, ok, err := s.store.GetProject(context.Background(), string(rec.ProjectID))
	if err != nil || !ok {
		return
	}
	s.recordAutoSpawnFailure(key, autoSpawnSettings(project), cause)
}
