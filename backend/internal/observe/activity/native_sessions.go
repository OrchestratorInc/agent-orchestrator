package activity

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DefaultNativeSessionTick keeps three polls inside the 10 second window a
// semantic send waits for its prompt to be accepted.
const DefaultNativeSessionTick = 3 * time.Second

// nativeTurnEndGrace is how long a session must have been active before an idle
// report with no reply ends the turn. A prompt can be recorded just before the
// harness reports itself busy, and that gap must not end a turn just started.
const nativeTurnEndGrace = 5 * time.Second

// maxNativeMessageText matches the limit on text sent to the hook endpoint.
const maxNativeMessageText = 16 << 10

// NativeSessionConfig controls native session polling.
type NativeSessionConfig struct {
	DataDir string
	Tick    time.Duration
	Clock   func() time.Time
	Logger  *slog.Logger
}

// NativeSessionObserver turns what a hookless harness reports about its live
// sessions into the activity signals its hooks would have sent. It compares
// message times with the session's stored activity time, so a poll missed
// mid-turn or a daemon restart neither loses a turn nor reports one twice.
type NativeSessionObserver struct {
	dataDir  string
	sessions sessionSource
	sink     activitySink
	agents   ports.AgentResolver
	tick     time.Duration
	clock    func() time.Time
	logger   *slog.Logger
}

// NewNativeSessionObserver builds a native session observer.
func NewNativeSessionObserver(sessions sessionSource, sink activitySink, agents ports.AgentResolver, cfg NativeSessionConfig) *NativeSessionObserver {
	o := &NativeSessionObserver{
		dataDir:  cfg.DataDir,
		sessions: sessions,
		sink:     sink,
		agents:   agents,
		tick:     cfg.Tick,
		clock:    cfg.Clock,
		logger:   cfg.Logger,
	}
	if o.tick <= 0 {
		o.tick = DefaultNativeSessionTick
	}
	if o.clock == nil {
		// UTC for the reason Observer gives: this becomes activity_last_at.
		o.clock = func() time.Time { return time.Now().UTC() }
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Start runs polling until the context is canceled.
func (o *NativeSessionObserver) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "native session observer")
}

// Poll performs one reconciliation pass. A harness is only asked about its
// sessions while AO has one running.
func (o *NativeSessionObserver) Poll(ctx context.Context) error {
	if o.agents == nil || o.dataDir == "" {
		return nil
	}
	sessions, err := o.sessions.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	byHarness := make(map[domain.AgentHarness][]domain.SessionRecord)
	for _, session := range sessions {
		if session.IsTerminated || session.Metadata.RuntimeHandleID == "" || session.Activity.State == domain.ActivityExited {
			continue
		}
		byHarness[session.Harness] = append(byHarness[session.Harness], session)
	}
	for harness, records := range byHarness {
		agent, ok := o.agents.Agent(harness)
		if !ok {
			continue
		}
		source, ok := agent.(ports.NativeSessionObserver)
		if !ok {
			continue
		}
		snapshots, ok, err := source.ObserveNativeSessions(ctx, o.dataDir)
		if err != nil {
			o.logger.Debug("native session observer: sessions unavailable", "harness", harness, "err", err)
			continue
		}
		if !ok {
			continue
		}
		for _, record := range records {
			if snapshot, found := matchNativeSession(record, snapshots); found {
				o.reconcile(ctx, record, snapshot)
			}
		}
	}
	return nil
}

// matchNativeSession finds the session running in rec's workspace. The identity
// AO already holds wins, then the newest session.
func matchNativeSession(rec domain.SessionRecord, snapshots []ports.NativeSessionSnapshot) (ports.NativeSessionSnapshot, bool) {
	workspace := canonicalPath(rec.Metadata.WorkspacePath)
	if workspace == "" {
		return ports.NativeSessionSnapshot{}, false
	}
	var match ports.NativeSessionSnapshot
	found := false
	for _, snapshot := range snapshots {
		if canonicalPath(snapshot.WorkspacePath) != workspace {
			continue
		}
		if snapshot.AgentSessionID == rec.Metadata.AgentSessionID {
			return snapshot, true
		}
		if !found || snapshot.StartedAt.After(match.StartedAt) {
			match, found = snapshot, true
		}
	}
	return match, found
}

// canonicalPath resolves symlinks because a harness reports its real working
// directory, while AO keeps the path it created the workspace under.
func canonicalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func (o *NativeSessionObserver) reconcile(ctx context.Context, rec domain.SessionRecord, snapshot ports.NativeSessionSnapshot) {
	latest, state := rec.Activity.LastActivityAt, rec.Activity.State
	emit := func(signal ports.ActivitySignal) bool {
		signal.Valid = true
		signal.AgentSessionID = snapshot.AgentSessionID
		signal.TranscriptPath = snapshot.TranscriptPath
		signal.ExpectedHarness = rec.Harness
		signal.LaunchID = rec.Metadata.RuntimeLaunchID
		if err := o.sink.ApplyActivitySignal(ctx, rec.ID, signal); err != nil {
			o.logger.Error("native session observer: reconciliation failed", "session", rec.ID, "event", signal.Event, "err", err)
			return false
		}
		latest, state = signal.Timestamp, signal.State
		return true
	}
	now := o.clock()

	if rec.FirstSignalAt.IsZero() || rec.Metadata.AgentSessionID != snapshot.AgentSessionID {
		at := latest
		if snapshot.StartedAt.After(at) {
			at = snapshot.StartedAt
		}
		if at.IsZero() {
			at = now
		}
		// Identity only: the turn below decides whether the session is working.
		if !emit(ports.ActivitySignal{Event: "session-start", State: state, Timestamp: at}) {
			return
		}
	}
	if snapshot.Prompt.At.After(latest) && !emit(nativePromptSignal(snapshot.Prompt)) {
		return
	}
	if snapshot.Working {
		if state != domain.ActivityActive {
			emit(ports.ActivitySignal{Event: "terminal-active", State: domain.ActivityActive, Timestamp: now})
		}
		return
	}
	switch {
	case snapshot.Reply.At.After(latest):
		emit(ports.ActivitySignal{
			Event:                 "stop",
			State:                 domain.ActivityIdle,
			Timestamp:             snapshot.Reply.At,
			FinalAssistantMessage: capNativeText(snapshot.Reply.Text),
		})
	case state == domain.ActivityActive && now.Sub(latest) >= nativeTurnEndGrace:
		// The turn ended with no reply, such as an interrupt.
		emit(ports.ActivitySignal{Event: "stop", State: domain.ActivityIdle, Timestamp: now})
	}
}

// nativePromptSignal mirrors the prompt hook, including keeping AO's own
// coordination turns out of the user's latest prompt.
func nativePromptSignal(prompt ports.NativeMessage) ports.ActivitySignal {
	signal := ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, Timestamp: prompt.At}
	if domain.IsCoordinationMessage(prompt.Text) {
		signal.ConversationCheckpointOrigin = domain.ConversationCheckpointOriginCoordination
		signal.CoordinationID, _ = domain.CoordinationDeliveryID(prompt.Text)
		return signal
	}
	text := capNativeText(prompt.Text)
	signal.ConversationCheckpointOrigin = domain.ConversationCheckpointOriginHuman
	signal.LatestUserPrompt = text
	signal.TurnUserRequest = text
	return signal
}

func capNativeText(value string) string {
	value = domain.SanitizeControlChars(strings.TrimSpace(value))
	if len(value) <= maxNativeMessageText {
		return value
	}
	return strings.ToValidUTF8(value[:maxNativeMessageText], "")
}
