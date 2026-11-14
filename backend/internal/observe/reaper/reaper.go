package reaper

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const DefaultTickInterval = 5 * time.Second

const (
	massDeadMinSessions = 5
	massDeadFraction    = 0.5
)

type Config struct {
	Tick       time.Duration
	Clock      func() time.Time
	Logger     *slog.Logger
	StartAfter <-chan struct{}
}

type sessionSource interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type runtimeObservationSink interface {
	ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f ports.RuntimeFacts) error
}

type runtimeProber interface {
	IsAlive(context.Context, ports.RuntimeHandle) (bool, error)
}

type Reaper struct {
	sink       runtimeObservationSink
	sessions   sessionSource
	runtime    runtimeProber
	workload   ports.SupervisedProcessInspector
	tick       time.Duration
	clock      func() time.Time
	logger     *slog.Logger
	startAfter <-chan struct{}

	missingHandleMu     sync.Mutex
	warnedMissingHandle map[domain.SessionID]struct{}
}

func New(sink runtimeObservationSink, sessions sessionSource, runtime runtimeProber, cfg Config) *Reaper {
	r := &Reaper{
		sink:                sink,
		sessions:            sessions,
		runtime:             runtime,
		tick:                cfg.Tick,
		clock:               cfg.Clock,
		logger:              cfg.Logger,
		startAfter:          cfg.StartAfter,
		warnedMissingHandle: make(map[domain.SessionID]struct{}),
	}
	if workload, ok := runtime.(ports.SupervisedProcessInspector); ok {
		r.workload = workload
	}
	if r.tick <= 0 {
		r.tick = DefaultTickInterval
	}
	if r.clock == nil {
		r.clock = func() time.Time { return time.Now().UTC() }
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	return r
}

func (r *Reaper) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go r.loop(ctx, done)
	return done
}

func (r *Reaper) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	if r.startAfter != nil {
		select {
		case <-ctx.Done():
			return
		case <-r.startAfter:
		}
	}
	t := time.NewTicker(r.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Tick(ctx); err != nil {
				r.logger.Error("reaper: tick failed", "err", err)
				sentryobs.CaptureBackground(ctx, err, "reaper", "reaper_tick", nil)
			}
		}
	}
}

var errMassDeath = errors.New("reaper: mass-death circuit breaker tripped")

func (r *Reaper) Tick(ctx context.Context) error {
	now := r.clock()

	sessions, err := r.sessions.ListAllSessions(ctx)
	if err != nil {
		return err
	}

	type observation struct {
		id    domain.SessionID
		facts ports.RuntimeFacts
	}
	var observations []observation
	dead := 0
	for _, sess := range sessions {
		if sess.IsTerminated {
			continue
		}
		facts, ok := r.probeOne(ctx, sess, now)
		if !ok {
			continue
		}
		if facts.Runtime == ports.ProbeDead {
			dead++
		}
		observations = append(observations, observation{id: sess.ID, facts: facts})
	}

	if dead >= massDeadMinSessions && float64(dead) > massDeadFraction*float64(len(observations)) {
		r.logger.Error("reaper: mass-death circuit breaker tripped, reporting the pass as inconclusive",
			"dead", dead, "probed", len(observations))
		sentryobs.CaptureBackground(ctx, errMassDeath, "reaper", "reaper_breaker", map[string]string{"reason": "mass_death"})
		for i := range observations {
			if observations[i].facts.Runtime == ports.ProbeDead {
				observations[i].facts.Runtime = ports.ProbeFailed
			}
		}
	}

	for _, obs := range observations {
		if err := r.sink.ApplyRuntimeObservation(ctx, obs.id, obs.facts); err != nil {
			r.logger.Error("reaper: ApplyRuntimeObservation failed",
				"session", obs.id, "err", err)
			sentryobs.CaptureBackground(ctx, err, "reaper", "runtime_observation", nil)
		}
	}
	return nil
}

func (r *Reaper) probeOne(ctx context.Context, sess domain.SessionRecord, now time.Time) (ports.RuntimeFacts, bool) {
	if domain.NormalizeSessionMode(sess.Mode) == domain.SessionModeChat {
		return ports.RuntimeFacts{}, false
	}
	if sess.RuntimeReleased() {
		return ports.RuntimeFacts{}, false
	}
	handle, ok := handleFromRecord(sess)
	if !ok {
		r.warnMissingHandleOnce(sess.ID)
		return ports.RuntimeFacts{}, false
	}
	alive, probeErr := r.runtime.IsAlive(ctx, handle)
	facts := ports.RuntimeFacts{ObservedAt: now, LaunchID: sess.Metadata.RuntimeLaunchID, Workload: ports.ProbeFailed}
	switch {
	case probeErr != nil:
		facts.Runtime = ports.ProbeFailed
		r.logger.Debug("reaper: probe error reported as failed fact",
			"session", sess.ID, "err", probeErr)
	case !alive:
		facts.Runtime = ports.ProbeDead
	default:
		facts.Runtime = ports.ProbeAlive
		if facts.LaunchID != "" && r.workload != nil {
			workloadAlive, workloadErr := r.workload.IsSupervisedProcessAlive(ctx, handle, ports.SupervisedProcessRef{
				SessionID: sess.ID,
				LaunchID:  facts.LaunchID,
			})
			switch {
			case workloadErr != nil:
				facts.Workload = ports.ProbeFailed
				r.logger.Debug("reaper: workload probe error reported as failed fact",
					"session", sess.ID, "launch", facts.LaunchID, "err", workloadErr)
			case workloadAlive:
				facts.Workload = ports.ProbeAlive
			default:
				facts.Workload = ports.ProbeDead
			}
		}
	}

	return facts, true
}

func (r *Reaper) warnMissingHandleOnce(id domain.SessionID) {
	r.missingHandleMu.Lock()
	if _, warned := r.warnedMissingHandle[id]; warned {
		r.missingHandleMu.Unlock()
		return
	}
	r.warnedMissingHandle[id] = struct{}{}
	r.missingHandleMu.Unlock()

	r.logger.Warn("reaper: session has no runtime handle metadata, skipping",
		"session", id)
}

func handleFromRecord(rec domain.SessionRecord) (ports.RuntimeHandle, bool) {
	id := rec.Metadata.RuntimeHandleID
	if id == "" {
		return ports.RuntimeHandle{}, false
	}
	return ports.RuntimeHandle{ID: id}, true
}
