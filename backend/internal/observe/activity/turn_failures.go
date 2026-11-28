package activity

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/observe"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// turnFailureErrorLogEvery rate-limits the error log for a failed turn whose
// report keeps failing: the report is retried every tick, the log is not.
const turnFailureErrorLogEvery = time.Minute

// DefaultTurnFailureTick is how often active terminal transcripts are checked
// for a turn that ended on an error. A transcript is re-read only when it
// changed, so the short tick costs a stat per active session.
const DefaultTurnFailureTick = 2 * time.Second

// TurnFailureEvent is the activity event a transcript-proven failed turn
// reports, matching Claude Code's StopFailure hook.
const TurnFailureEvent = "stop-failure"

// TurnInterruptEvent is the activity event a transcript-proven Esc interrupt
// reports, matching Codex's Interrupt hook.
const TurnInterruptEvent = "interrupt"

// turnFailureHarnesses are the harnesses whose terminal turn can end without
// firing a hook. Codex runs its Stop hook only after a successful turn, so a
// turn that fails or is interrupted reports nothing; Claude Code reports a
// failed turn through StopFailure but fires no hook for an Esc interrupt.
var turnFailureHarnesses = []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode}

// turnFailureSource lists the sessions worth checking: live terminal sessions
// of one harness that are in a turn and have a native transcript path.
type turnFailureSource interface {
	ListTurnFailureCandidates(ctx context.Context, harness domain.AgentHarness) ([]domain.TurnFailureCandidate, error)
}

// TurnFailureConfig controls failed-turn polling.
type TurnFailureConfig struct {
	Tick   time.Duration
	Logger *slog.Logger
	// Wake, when set, signals that a session changed (a turn may have started).
	// The observer then polls every Tick while it watches a turn and every
	// IdleTick otherwise; a wake while it watches none polls at once, or one
	// Tick after the previous poll if that is later. Without it (or with
	// IdleTick not above Tick) it polls every Tick, as before.
	Wake     <-chan struct{}
	IdleTick time.Duration
}

// TurnFailureObserver marks a terminal session idle when its harness's
// transcript proves the current turn ended on an error or was interrupted.
// Some harnesses fire no hook at all for such a turn (Codex for a failed or
// interrupted turn, Claude Code for an Esc interrupt), so the hook-driven
// activity stayed active: the session read "working", queued messages were
// held, and Finish-then-switch waited for a stop that never came. The 30 second stale-activity observer only recovers that
// after two minutes, and only when the screen positively shows an idle prompt.
type TurnFailureObserver struct {
	sessions turnFailureSource
	sink     activitySink
	agents   ports.AgentResolver
	tick     time.Duration
	logger   *slog.Logger
	watches  map[domain.SessionID]*turnFailureWatch

	wake     <-chan struct{}
	idleTick time.Duration
	// candidates is how many sessions the last poll found in a turn. The
	// observer stays on its short tick while there is one, even before a watch
	// opens on it (its transcript may not be readable yet).
	candidates int
	// pollFailed records that the last poll returned an error. The observer
	// does not know whether a turn runs then, so it keeps its short tick.
	pollFailed bool
	lastPoll   time.Time
}

// turnFailureWatch caches one session's transcript read. The transcript is
// read again only when its path, size or modification time changes.
type turnFailureWatch struct {
	path    string
	size    int64
	modTime time.Time
	failure ports.TurnFailure
	failed  bool
	// reported is the failure the lifecycle already accepted. It may have
	// applied it, kept the parent active while a subagent runs (recording the
	// idle for when the child ends), or fenced it for a stale launch: in every
	// case re-sending the same failure on a later revision gets the same
	// answer, so it is sent and logged once.
	reported turnFailureKey
	// errorKey and errorLoggedAt rate-limit the error log while one failure's
	// report keeps failing.
	errorKey      turnFailureKey
	errorLoggedAt time.Time
}

// turnFailureKey identifies one failed turn of one runtime launch.
type turnFailureKey struct {
	launchID string
	at       int64
}

// NewTurnFailureObserver builds a failed-turn observer.
func NewTurnFailureObserver(sessions turnFailureSource, sink activitySink, agents ports.AgentResolver, cfg TurnFailureConfig) *TurnFailureObserver {
	o := &TurnFailureObserver{
		sessions: sessions,
		sink:     sink,
		agents:   agents,
		tick:     cfg.Tick,
		logger:   cfg.Logger,
		watches:  make(map[domain.SessionID]*turnFailureWatch),
		wake:     cfg.Wake,
		idleTick: cfg.IdleTick,
	}
	if o.tick <= 0 {
		o.tick = DefaultTurnFailureTick
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Start runs polling until the context is canceled.
func (o *TurnFailureObserver) Start(ctx context.Context) <-chan struct{} {
	if o.wake == nil || o.idleTick <= o.tick {
		return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "turn failure observer")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.pollAndLog(ctx)
		// With no turn to watch the 2 s tick only re-runs two empty queries, so
		// wait IdleTick. A turn starting changes its session, and that change
		// polls at once, no sooner than one tick after the previous poll.
		watching := o.busy()
		timer := time.NewTimer(o.nextPoll(watching))
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-o.wake:
				if !watching {
					watching = true // poll now; the poll decides whether to stay fast
					timer.Reset(max(0, time.Until(o.lastPoll.Add(o.tick))))
				}
			case <-timer.C:
				o.pollAndLog(ctx)
				watching = o.busy()
				timer.Reset(o.nextPoll(watching))
			}
		}
	}()
	return done
}

// busy reports a turn in progress: a candidate session or an open watch.
// busy reports a turn in progress: a candidate session or an open watch, or
// a failed poll, after which it cannot tell.
func (o *TurnFailureObserver) busy() bool {
	return o.pollFailed || o.candidates > 0 || len(o.watches) > 0
}

func (o *TurnFailureObserver) nextPoll(watching bool) time.Duration {
	if watching {
		return o.tick
	}
	return o.idleTick
}

func (o *TurnFailureObserver) pollAndLog(ctx context.Context) {
	o.lastPoll = time.Now()
	err := o.Poll(ctx)
	o.pollFailed = err != nil
	if err != nil && ctx.Err() == nil {
		o.logger.Error("turn failure observer: poll failed", "err", err)
	}
}

// Poll performs one pass. It is not safe for concurrent use. The store
// returns only live terminal sessions in a turn (active, or paused on the user:
// Codex reports an approval prompt as waiting_input and fires no hook when the
// approved turn continues, so a turn that then fails would otherwise stay
// "needs input"; Esc at a Claude permission prompt, which reads blocked, ends
// the turn). Chat sessions report turn ends through their controller, and an
// idle session has nothing to recover.
func (o *TurnFailureObserver) Poll(ctx context.Context) error {
	watched := make(map[domain.SessionID]bool, len(o.watches))
	candidates := 0
	for _, harness := range turnFailureHarnesses {
		detector, ok := o.detector(harness)
		if !ok {
			continue
		}
		// Not cancellable: a cancellable context makes the SQLite driver and
		// database/sql start a watcher goroutine per query, twice per tick.
		sessions, err := o.sessions.ListTurnFailureCandidates(context.WithoutCancel(ctx), harness)
		if err != nil {
			return err
		}
		candidates += len(sessions)
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
	o.candidates = candidates
	return nil
}

// detector returns the harness's failed-turn capability.
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
	// The current state was entered by a hook (a prompt, or an approval
	// request) at LastActivityAt. A failure recorded before it belongs to an
	// earlier turn: replaying it would idle a turn that is still running.
	// A prompt submitted while the session still read active (within a poll
	// of an Esc, before the harness records the prompt) starts a turn without
	// moving LastActivityAt, so the newest prompt bounds the turn too.
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
		Valid: true,
		State: domain.ActivityIdle,
		// Dated when the turn ended, not when this poll saw it: terminal input
		// typed after the failure is newer than this idle, so a Chat switch
		// still waits for the turn that input started.
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
