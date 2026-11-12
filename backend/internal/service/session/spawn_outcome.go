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

// An asynchronous Chat start answers the spawn request before its worktree,
// Setup and controller exist. Its single outcome, ao.sessions.spawned once the
// session is usable or ao.sessions.spawn_failed, is reported when the start
// settles. Every write of the session's provision state reaches
// observeProvision: "provisioning" opens one attempt, the first "ready" or
// "failed" after it closes the attempt, and anything else is ignored. An
// attempt this process did not open (one interrupted by a daemon restart)
// reports nothing rather than an invented duration.

// spawnAcceptance carries what spawnWithTelemetry knew when it accepted the
// request, through manager.Spawn's context, to the provision observer.
type spawnAcceptance struct {
	start         time.Time
	createdBy     domain.SessionCreator
	firstEligible bool
	project       domain.ProjectRecord
	autoSpawn     *autoSpawnAttempt

	// Set by the observer when the start went asynchronous. Written on the
	// spawn goroutine before manager.Spawn returns, read after it returns.
	deferred  bool
	sessionID domain.SessionID

	// When manager.Spawn answered the request (duration_ms). The background
	// start can settle first; its report then waits here for that time.
	mu         sync.Mutex
	answered   bool
	answeredMs int64
	held       func(answeredMs int64)
}

// answer records when the spawn request was answered and releases an outcome
// that settled before then.
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

// deliver runs report with the answer time, now or once the request is answered.
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

// spawnFacts is the request context an outcome event carries. Captured while
// the request is live, because the outcome may be reported from the
// background long after it returned.
type spawnFacts struct {
	createdBy   domain.SessionCreator
	surface     domain.RequestSurface
	githubActor string
	requestID   string
	// trace is the start's step timings and launch facts; nil for a retry.
	trace *sessionmanager.SpawnTrace
}

type asyncSpawnAttempt struct {
	start time.Time
	facts spawnFacts
	// acceptance is nil for a retry of a failed start.
	acceptance    *spawnAcceptance
	firstEligible bool
}

type asyncSpawnAttempts struct {
	mu   sync.Mutex
	open map[domain.SessionID]*asyncSpawnAttempt
}

// provisionObserverSetter is implemented by *sessionmanager.Manager.
type provisionObserverSetter interface {
	SetProvisionObserver(sessionmanager.ProvisionObserver)
}

// spawnFactsFromRequest reads the request's attribution. withActor also
// resolves the GitHub login, which can be a network call: only the synchronous
// path does that, after the session launched. Asynchronous starts leave it to
// the exporter, which stamps the login the daemon already resolved.
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

// sessionTelemetryReader reads created_by back for a retried start.
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
		// A retry of a failed start: a fresh attempt timed from its own
		// request. It is the install's first session only if no other exists.
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

// abandonAsyncSpawn drops an attempt whose Spawn call failed after going
// asynchronous, so the caller reports that failure instead. It reports false
// when the background start already settled the attempt.
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
	// The first_for_install claim is taken only by a start that became usable.
	firstForInstall, firstKnown := s.firstSession.finish(s.milestones, attempt.firstEligible, ready)
	// A start cancelled on purpose (the session was killed mid-start, or the
	// daemon shut down) reports ao.sessions.spawn_cancelled, not a failure
	// (emitSpawnFailed). A start that ran out of its time budget
	// (context.DeadlineExceeded) still reports as failed.
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
	// duration_ms keeps its meaning, the time until manager.Spawn answered;
	// an outcome that settles first waits for it. A retry has no such answer,
	// so it omits duration_ms.
	if attempt.acceptance == nil {
		report(-1)
		return
	}
	attempt.acceptance.deliver(report)
}

// onlySession reports whether id is the install's only session.
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

// monoNow is the clock for spawn durations. Unlike now(), it does not convert
// to UTC, which would strip Go's monotonic reading and turn a duration into a
// wall-clock difference that jumps with clock adjustments. Durations only ever
// compare two readings from this process.
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
