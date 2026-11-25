package activity

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Default activity observation settings.
const (
	DefaultTickInterval = 30 * time.Second
	DefaultStaleAfter   = 2 * time.Minute
	DefaultOutputLines  = 40
)

// Config controls activity reconciliation polling.
type Config struct {
	DataDir     string
	Tick        time.Duration
	StaleAfter  time.Duration
	OutputLines int
	Clock       func() time.Time
	Logger      *slog.Logger
	// StartAfter holds the poll loop until it closes; nil starts at once. The
	// daemon closes it once SetSessionEnv is installed, so no state change is
	// read, and its native identity resolved, without the session's
	// environment.
	StartAfter <-chan struct{}
	// Rechecks, when set, names sessions whose screen should be read before
	// their activity goes stale (see RecheckSource).
	Rechecks RecheckSource
}

// RecheckSource asks for an early screen read of an active session: a hook
// arrived whose meaning only the screen can settle, such as a Kimi
// StopFailure taken for a child's that may have been the parent's own.
type RecheckSource interface {
	TerminalActivityRecheckDue(id domain.SessionID, now time.Time) bool
}

type sessionSource interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type activitySink interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, signal ports.ActivitySignal) error
}

type outputReader interface {
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
}

// Observer reconciles stale hook activity from adapter-owned terminal markers.
type Observer struct {
	dataDir     string
	sessions    sessionSource
	sink        activitySink
	runtime     outputReader
	agents      ports.AgentResolver
	tick        time.Duration
	staleAfter  time.Duration
	outputLines int
	clock       func() time.Time
	logger      *slog.Logger
	startAfter  <-chan struct{}
	rechecks    RecheckSource
	sessionEnv  atomic.Pointer[SessionEnvFunc]
}

// SessionEnvFunc builds the environment a session's agent process runs in.
type SessionEnvFunc func(ctx context.Context, session domain.SessionRecord) (map[string]string, error)

// SetSessionEnv gives native-identity lookups the session's runtime
// environment: an adapter whose lookup runs the agent CLI may need a
// project-level profile or credential the daemon's own environment lacks.
// The daemon sets it once the session manager exists; lookups before that run
// without one.
func (o *Observer) SetSessionEnv(fn SessionEnvFunc) {
	o.sessionEnv.Store(&fn)
}

// New builds an activity observer.
func New(sessions sessionSource, sink activitySink, runtime outputReader, agents ports.AgentResolver, cfg Config) *Observer {
	o := &Observer{
		dataDir:     cfg.DataDir,
		sessions:    sessions,
		sink:        sink,
		runtime:     runtime,
		agents:      agents,
		tick:        cfg.Tick,
		staleAfter:  cfg.StaleAfter,
		outputLines: cfg.OutputLines,
		clock:       cfg.Clock,
		logger:      cfg.Logger,
		startAfter:  cfg.StartAfter,
		rechecks:    cfg.Rechecks,
	}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.staleAfter <= 0 {
		o.staleAfter = DefaultStaleAfter
	}
	if o.outputLines <= 0 {
		o.outputLines = DefaultOutputLines
	}
	if o.clock == nil {
		// UTC, not bare time.Now: this timestamp becomes sessions.activity_last_at,
		// and the SQLite driver stores a time.Time by its String() form. A
		// local-zone value keeps its monotonic reading and offset, which then
		// fails to compare as a timestamp against the "… +0000 UTC" rows the
		// other writers produce.
		o.clock = func() time.Time { return time.Now().UTC() }
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Start runs polling until the context is canceled.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	if o.startAfter == nil {
		return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "activity observer")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			return
		case <-o.startAfter:
		}
		<-observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "activity observer")
	}()
	return done
}

// Poll performs one activity reconciliation pass.
func (o *Observer) Poll(ctx context.Context) error {
	sessions, err := o.sessions.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	now := o.clock()
	for _, session := range sessions {
		o.reconcile(ctx, session, now)
	}
	return nil
}

func (o *Observer) reconcile(ctx context.Context, session domain.SessionRecord, now time.Time) {
	if session.IsTerminated || session.Metadata.RuntimeHandleID == "" || o.agents == nil {
		return
	}
	agent, ok := o.agents.Agent(session.Harness)
	if !ok {
		return
	}
	detector, ok := agent.(ports.TerminalActivityDetector)
	if !ok {
		return
	}
	var options ports.TerminalActivityOptions
	if provider, ok := agent.(ports.TerminalActivityOptionsProvider); ok {
		options = provider.TerminalActivityOptions()
	}
	nativeDecisionCards := options.SupportsBlocked
	rendered, supportsRendered := agent.(interface{ RequiresRenderedActivity() bool })
	requiresRendered := supportsRendered && rendered.RequiresRenderedActivity()
	// Hook-owned permission blocks stay sticky unless the adapter can observe
	// the current rendered modal and its resolution.
	if session.Activity.State == domain.ActivityBlocked && !requiresRendered && !nativeDecisionCards {
		return
	}

	nativeDecisionCards = nativeDecisionCards || requiresRendered
	continuous, ok := agent.(ports.ContinuousTerminalActivityDetector)
	if !ok || !continuous.ContinuouslyDetectTerminalActivity() {
		if session.Activity.State == domain.ActivityWaitingInput {
			waitingDetector, canRecoverWaiting := agent.(ports.WaitingTerminalActivityDetector)
			if !canRecoverWaiting || !waitingDetector.ContinuouslyDetectTerminalActivityWhileWaiting() {
				return
			}
		} else if session.Activity.State != domain.ActivityActive || session.Activity.LastActivityAt.IsZero() ||
			(now.Sub(session.Activity.LastActivityAt) < o.staleAfter && !o.recheckDue(session.ID, now)) {
			return
		}
	} else if session.Activity.State != domain.ActivityActive &&
		session.Activity.State != domain.ActivityIdle &&
		session.Activity.State != domain.ActivityWaitingInput && (!nativeDecisionCards || session.Activity.State != domain.ActivityBlocked) {
		return
	}
	var output string
	var err error
	if requiresRendered {
		reader, ok := o.runtime.(interface {
			GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error)
		})
		if !ok {
			return
		}
		output, err = reader.GetStyledOutput(ctx, ports.RuntimeHandle{ID: session.Metadata.RuntimeHandleID}, o.outputLines)
	} else if rendered, ok := o.runtime.(ports.StyledTerminalOutputReader); options.PreferStyledOutput && ok {
		output, err = rendered.GetStyledOutput(ctx, ports.RuntimeHandle{ID: session.Metadata.RuntimeHandleID}, o.outputLines)
	} else {
		output, err = o.runtime.GetOutput(ctx, ports.RuntimeHandle{ID: session.Metadata.RuntimeHandleID}, o.outputLines)
	}
	if err != nil {
		o.logger.Debug("activity observer: terminal output unavailable", "session", session.ID, "err", err)
		return
	}
	state, ok := detector.DetectTerminalActivity(output)
	if !ok || (state != domain.ActivityActive && state != domain.ActivityIdle && state != domain.ActivityWaitingInput && (!nativeDecisionCards || state != domain.ActivityBlocked)) {
		return
	}
	if state == session.Activity.State {
		return
	}
	nativeID := ""
	if resolver, supports := agent.(ports.AgentNativeSessionResolver); supports && o.dataDir != "" {
		resolveCfg := ports.NativeSessionResolveConfig{DataDir: o.dataDir, SessionID: session.ID, LaunchID: session.Metadata.RuntimeLaunchID}
		if fn := o.sessionEnv.Load(); fn != nil && *fn != nil {
			if env, envErr := (*fn)(ctx, session); envErr != nil {
				o.logger.Debug("activity observer: session environment unavailable", "session", session.ID, "err", envErr)
			} else {
				resolveCfg.Env = env
			}
		}
		id, found, resolveErr := resolver.ResolveNativeSessionID(ctx, resolveCfg)
		if resolveErr != nil {
			// Native identity is optional evidence; a failed lookup must not
			// discard activity established by the current terminal frame.
			o.logger.Debug("activity observer: native identity unavailable", "session", session.ID, "err", resolveErr)
		} else if found {
			nativeID = id
		}
	}
	event := "terminal-" + string(state)
	if nativeDecisionCards && session.Activity.State == domain.ActivityBlocked && state != domain.ActivityBlocked {
		// A current native composer/busy frame resolves the prior modal. Keep
		// its observed destination state: lifecycle accepts this boundary for
		// active, idle, and waiting input, and clears the blocked tool flight.
		event = "permission-resolved"
	} else if state == domain.ActivityWaitingInput {
		event = "terminal-waiting-input"
	}
	err = o.sink.ApplyActivitySignal(ctx, session.ID, ports.ActivitySignal{
		AgentSessionID:   nativeID,
		Valid:            true,
		State:            state,
		Timestamp:        now,
		ExpectedRevision: &session.Revision,
		Event:            event,
		LaunchID:         session.Metadata.RuntimeLaunchID,
	})
	if err != nil {
		o.logger.Error("activity observer: reconciliation failed", "session", session.ID, "err", err)
	}
}

func (o *Observer) recheckDue(id domain.SessionID, now time.Time) bool {
	return o.rechecks != nil && o.rechecks.TerminalActivityRecheckDue(id, now)
}
