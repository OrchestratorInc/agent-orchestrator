package session

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/reqid"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

type spawnAcceptance struct {
	start         time.Time
	createdBy     domain.SessionCreator
	firstEligible bool
	project       domain.ProjectRecord
	autoSpawn     *autoSpawnAttempt

	deferred  bool
	sessionID domain.SessionID

	mu         sync.Mutex
	answered   bool
	answeredMs int64
	held       func(answeredMs int64)
}

func (a *spawnAcceptance) answer(ms int64) {
	a.mu.Lock()
	a.answered, a.answeredMs = true, ms
	held := a.held
	a.held = nil
	a.mu.Unlock()
	if held != nil {
		held(ms)
	}
}

func (a *spawnAcceptance) deliver(report func(answeredMs int64)) {
	a.mu.Lock()
	if !a.answered {
		a.held = report
		a.mu.Unlock()
		return
	}
	ms := a.answeredMs
	a.mu.Unlock()
	report(ms)
}

type spawnAcceptanceKey struct{}

type spawnFacts struct {
	createdBy   domain.SessionCreator
	surface     domain.RequestSurface
	githubActor string
	requestID   string
	trace       *sessionmanager.SpawnTrace
}

type asyncSpawnAttempt struct {
	start         time.Time
	facts         spawnFacts
	acceptance    *spawnAcceptance
	firstEligible bool
}

type asyncSpawnAttempts struct {
	mu   sync.Mutex
	open map[domain.SessionID]*asyncSpawnAttempt
}

type provisionObserverSetter interface {
	SetProvisionObserver(sessionmanager.ProvisionObserver)
}

func (s *Service) spawnFactsFromRequest(ctx context.Context, createdBy domain.SessionCreator, withActor bool) spawnFacts {
	facts := spawnFacts{createdBy: createdBy, requestID: reqid.FromContext(ctx), trace: sessionmanager.SpawnTraceFrom(ctx)}
	if surface, ok := telemetrymeta.SurfaceFromContext(ctx); ok {
		facts.surface = surface
	}
	if withActor {
		if actor, ok := s.githubActor(ctx); ok {
			facts.githubActor = actor
		}
	}
	return facts
}

type sessionTelemetryReader interface {
	GetSessionTelemetry(ctx context.Context, id domain.SessionID) (domain.SessionTelemetryFacts, error)
}

func (s *Service) observeProvision(ctx context.Context, rec domain.SessionRecord, state domain.SessionProvisionState, cause error) {
	switch state {
	case domain.SessionProvisionProvisioning:
		s.openAsyncSpawn(ctx, rec)
	case domain.SessionProvisionReady, domain.SessionProvisionFailed:
		s.asyncSpawns.mu.Lock()
		attempt := s.asyncSpawns.open[rec.ID]
		delete(s.asyncSpawns.open, rec.ID)
		s.asyncSpawns.mu.Unlock()
		if attempt != nil {
			s.settleAsyncSpawn(rec, attempt, state == domain.SessionProvisionReady, cause)
		}
	}
}

func (s *Service) openAsyncSpawn(ctx context.Context, rec domain.SessionRecord) {
	s.asyncSpawns.mu.Lock()
	_, already := s.asyncSpawns.open[rec.ID]
	s.asyncSpawns.mu.Unlock()
	if already {
		return
	}
	attempt := &asyncSpawnAttempt{start: s.monoNow()}
	if acc, ok := ctx.Value(spawnAcceptanceKey{}).(*spawnAcceptance); ok && acc.sessionID == "" {
		acc.deferred, acc.sessionID = true, rec.ID
		attempt.start, attempt.acceptance, attempt.firstEligible = acc.start, acc, acc.firstEligible
		attempt.facts = s.spawnFactsFromRequest(ctx, acc.createdBy, false)
	} else {
		var createdBy domain.SessionCreator
		if reader, ok := s.store.(sessionTelemetryReader); ok {
			if facts, err := reader.GetSessionTelemetry(ctx, rec.ID); err == nil {
				createdBy = facts.CreatedBy
			}
		}
		attempt.facts = s.spawnFactsFromRequest(ctx, createdBy, false)
		attempt.firstEligible = s.onlySession(ctx, rec.ID)
		s.firstSession.begin(attempt.firstEligible)
	}
	s.asyncSpawns.mu.Lock()
	if _, raced := s.asyncSpawns.open[rec.ID]; raced {
		s.asyncSpawns.mu.Unlock()
		if attempt.acceptance == nil {
			s.firstSession.finish(s.milestones, attempt.firstEligible, false)
		}
		return
	}
	if s.asyncSpawns.open == nil {
		s.asyncSpawns.open = map[domain.SessionID]*asyncSpawnAttempt{}
	}
	s.asyncSpawns.open[rec.ID] = attempt
	s.asyncSpawns.mu.Unlock()
}

func (s *Service) abandonAsyncSpawn(acc *spawnAcceptance) bool {
	s.asyncSpawns.mu.Lock()
	defer s.asyncSpawns.mu.Unlock()
	if attempt, ok := s.asyncSpawns.open[acc.sessionID]; ok && attempt.acceptance == acc {
		delete(s.asyncSpawns.open, acc.sessionID)
		return true
	}
	return false
}

func (s *Service) settleAsyncSpawn(rec domain.SessionRecord, attempt *asyncSpawnAttempt, ready bool, cause error) {
	if attempt.acceptance != nil {
		s.settleAutoSpawn(attempt.acceptance.autoSpawn, ready, cause)
	}
	elapsed := s.monoNow().Sub(attempt.start).Milliseconds()
	firstForInstall, firstKnown := s.firstSession.finish(s.milestones, attempt.firstEligible, ready)
	report := func(answered int64) {
		if !ready {
			s.emitSpawnFailed(spawnFailure{kind: rec.Kind, harness: rec.Harness, projectID: rec.ProjectID, mode: rec.Mode, facts: attempt.facts}, cause, answered, elapsed)
			return
		}
		s.emitSpawned(rec, answered, elapsed, firstForInstall, firstKnown, attempt.facts)
		if attempt.firstEligible && attempt.acceptance != nil {
			s.emitFirstSessionSpawned(rec, attempt.acceptance.project, attempt.facts.requestID)
		}
	}
	if attempt.acceptance == nil {
		report(-1)
		return
	}
	attempt.acceptance.deliver(report)
}

func (s *Service) onlySession(ctx context.Context, id domain.SessionID) bool {
	if s.store == nil {
		return false
	}
	rows, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if row.ID != id && !row.IsTaskPreparation {
			return false
		}
	}
	return true
}

func (s *Service) monoNow() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

func nonNegativeMs(ms int64) int64 {
	if ms < 0 {
		return 0
	}
	return ms
}
