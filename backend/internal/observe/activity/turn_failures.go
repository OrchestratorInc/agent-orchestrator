package activity

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const turnFailureErrorLogEvery = time.Minute

const DefaultTurnFailureTick = 2 * time.Second

const TurnFailureEvent = "stop-failure"

const TurnInterruptEvent = "interrupt"

var turnFailureHarnesses = []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode}

type turnFailureSource interface {
	ListTurnFailureCandidates(ctx context.Context, harness domain.AgentHarness) ([]domain.TurnFailureCandidate, error)
}

type TurnFailureConfig struct {
	Tick   time.Duration
	Logger *slog.Logger
}

type TurnFailureObserver struct {
	sessions turnFailureSource
	sink     activitySink
	agents   ports.AgentResolver
	tick     time.Duration
	logger   *slog.Logger
	watches  map[domain.SessionID]*turnFailureWatch
}

type turnFailureWatch struct {
	path          string
	size          int64
	modTime       time.Time
	failure       ports.TurnFailure
	failed        bool
	reported      turnFailureKey
	errorKey      turnFailureKey
	errorLoggedAt time.Time
}

type turnFailureKey struct {
	launchID string
	at       int64
}

func NewTurnFailureObserver(sessions turnFailureSource, sink activitySink, agents ports.AgentResolver, cfg TurnFailureConfig) *TurnFailureObserver {
	o := &TurnFailureObserver{
		sessions: sessions,
		sink:     sink,
		agents:   agents,
		tick:     cfg.Tick,
		logger:   cfg.Logger,
		watches:  make(map[domain.SessionID]*turnFailureWatch),
	}
	if o.tick <= 0 {
		o.tick = DefaultTurnFailureTick
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

func (o *TurnFailureObserver) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "turn failure observer")
}

func (o *TurnFailureObserver) Poll(ctx context.Context) error {
	watched := make(map[domain.SessionID]bool, len(o.watches))
	for _, harness := range turnFailureHarnesses {
		detector, ok := o.detector(harness)
		if !ok {
			continue
		}
		sessions, err := o.sessions.ListTurnFailureCandidates(ctx, harness)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			watched[session.ID] = true
			o.reconcile(ctx, session, detector)
		}
	}
	for id := range o.watches {
		if !watched[id] {
			delete(o.watches, id)
		}
	}
	return nil
}

func (o *TurnFailureObserver) detector(harness domain.AgentHarness) (ports.TranscriptTurnFailureDetector, bool) {
	if o.agents == nil {
		return nil, false
	}
	agent, ok := o.agents.Agent(harness)
	if !ok {
		return nil, false
	}
	detector, ok := agent.(ports.TranscriptTurnFailureDetector)
	return detector, ok
}

func (o *TurnFailureObserver) reconcile(ctx context.Context, session domain.TurnFailureCandidate, detector ports.TranscriptTurnFailureDetector) {
	path := session.NativeTranscriptPath
	info, err := os.Stat(path)
	if err != nil {
		delete(o.watches, session.ID)
		return
	}
	watch := o.watches[session.ID]
	if watch == nil || watch.path != path || watch.size != info.Size() || !watch.modTime.Equal(info.ModTime()) {
		failure, failed, err := detector.LastTurnFailure(ctx, path)
		if err != nil {
			o.logger.Debug("turn failure observer: transcript unreadable", "session", session.ID, "err", err)
			return
		}
		if watch == nil || watch.path != path {
			watch = &turnFailureWatch{}
			o.watches[session.ID] = watch
		}
		watch.path, watch.size, watch.modTime = path, info.Size(), info.ModTime()
		watch.failure, watch.failed = failure, failed
	}
	if !watch.failed {
		return
	}
	if session.Activity.LastActivityAt.IsZero() || watch.failure.At.Before(session.Activity.LastActivityAt) ||
		watch.failure.At.Before(session.LatestUserPromptAt) {
		return
	}
	key := turnFailureKey{launchID: session.RuntimeLaunchID, at: watch.failure.At.UnixNano()}
	if watch.reported == key {
		return
	}
	event, what := TurnFailureEvent, "ended on an error"
	if watch.failure.Interrupted {
		event, what = TurnInterruptEvent, "was interrupted"
	}
	revision := session.Revision
	err = o.sink.ApplyActivitySignal(ctx, session.ID, ports.ActivitySignal{
		Valid:            true,
		State:            domain.ActivityIdle,
		Timestamp:        watch.failure.At,
		ExpectedRevision: &revision,
		ExpectedHarness:  session.Harness,
		Event:            event,
		LaunchID:         session.RuntimeLaunchID,
	})
	if err != nil {
		if now := time.Now(); watch.errorKey != key || now.Sub(watch.errorLoggedAt) >= turnFailureErrorLogEvery {
			watch.errorKey, watch.errorLoggedAt = key, now
			o.logger.Error("turn failure observer: reconciliation failed", "session", session.ID, "err", err)
		}
		return
	}
	watch.reported = key
	o.logger.Info("agent turn "+what+"; marking the session idle",
		"session", session.ID, "harness", session.Harness, "error_message", watch.failure.Message)
}
