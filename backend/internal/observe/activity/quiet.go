package activity

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// quietScreen is the screen a stale terminal showed, and since when it has
// shown it with no command running.
type quietScreen struct {
	launchID string
	digest   [sha256.Size]byte
	since    time.Time
}

// reconcileUndetected watches a terminal whose adapter has no
// TerminalActivityDetector. Its hooks are the only activity source, so a turn
// whose end hook never reaches AO (an Esc interrupt, a provider error, a lost
// callback) reads active forever. LastActivityAt records only when the session
// became active, so time alone cannot tell that from a long turn.
//
// An adapter's TerminalSurfaceInspector is authoritative and settles the state
// from one rendered frame. Otherwise the evidence is the terminal itself: an
// agent TUI animates a spinner or timer while a turn runs, and a tool runs as a
// command under the agent. A screen unchanged for quietAfter with no command
// running reads as waiting on the user. That surfaces the stall as Needs input
// without claiming the turn ended, so nothing gated on idle acts on a guess.
func (o *Observer) reconcileUndetected(ctx context.Context, session domain.SessionRecord, agent ports.Agent, now time.Time, previous quietScreen) {
	if domain.NormalizeSessionMode(session.Mode) != domain.SessionModeTUI ||
		session.Activity.State != domain.ActivityActive || session.Activity.LastActivityAt.IsZero() ||
		now.Sub(session.Activity.LastActivityAt) < o.staleAfter {
		return
	}
	handle := ports.RuntimeHandle{ID: session.Metadata.RuntimeHandleID}
	output, rendered, err := o.currentScreen(ctx, handle)
	if err != nil {
		o.logger.Debug("activity observer: terminal output unavailable", "session", session.ID, "err", err)
		return
	}
	if inspector, ok := agent.(ports.TerminalSurfaceInspector); ok && rendered {
		switch inspector.InspectTerminalSurface(output).Work {
		case ports.TerminalSurfaceWorkIdle:
			o.apply(ctx, session, agent, domain.ActivityIdle, "terminal-idle", now)
			return
		case ports.TerminalSurfaceWorkWaitingInput:
			o.apply(ctx, session, agent, domain.ActivityWaitingInput, "terminal-waiting-input", now)
			return
		case ports.TerminalSurfaceWorkActive:
			return
		}
	}
	if strings.TrimSpace(output) == "" {
		return
	}
	screen := quietScreen{launchID: session.Metadata.RuntimeLaunchID, digest: sha256.Sum256([]byte(output)), since: now}
	if previous.launchID != screen.launchID || previous.digest != screen.digest {
		o.quiet[session.ID] = screen
		return
	}
	commands, ok := o.runtime.(ports.WorkloadCommandInspector)
	if !ok {
		return
	}
	running, err := commands.RunningWorkloadCommands(ctx, handle, ports.SupervisedProcessRef{SessionID: session.ID, LaunchID: session.Metadata.RuntimeLaunchID})
	if err != nil {
		o.logger.Debug("activity observer: running commands unavailable", "session", session.ID, "err", err)
		return
	}
	// A running command is work in progress. It restarts the window, so the
	// agent gets a full window to respond after the command finishes.
	if running == 0 {
		screen.since = previous.since
	}
	o.quiet[session.ID] = screen
	if now.Sub(screen.since) >= quietAfter {
		o.apply(ctx, session, agent, domain.ActivityWaitingInput, "terminal-quiet", now)
	}
}

// currentScreen reads the rendered viewport where the runtime has one, and
// reports whether it did. A detached host from before rendered capture falls
// back to its output.
func (o *Observer) currentScreen(ctx context.Context, handle ports.RuntimeHandle) (string, bool, error) {
	if styled, ok := o.runtime.(ports.StyledTerminalOutputReader); ok {
		output, err := styled.GetStyledOutput(ctx, handle, o.outputLines)
		if !errors.Is(err, ports.ErrStyledTerminalOutputUnavailable) {
			return output, err == nil, err
		}
	}
	output, err := o.runtime.GetOutput(ctx, handle, o.outputLines)
	return output, false, err
}
