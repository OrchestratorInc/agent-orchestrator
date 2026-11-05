package shellagent

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// State is what a shell tab shows about the agent running in it.
type State string

const (
	// StateWorking: the agent is drawing output (a turn is in progress).
	StateWorking State = "working"
	// StateIdle: the agent is at its prompt with nothing to ask.
	StateIdle State = "idle"
	// StateWaiting: the agent stopped on a question or a permission prompt.
	StateWaiting State = "waiting"
)

// Agent is one shell terminal's current agent.
type Agent struct {
	HandleID string
	Harness  domain.AgentHarness
	State    State
	// Since is when State last changed.
	Since time.Time
}

// Runtime is the slice of the runtime adapter the manager needs.
type Runtime interface {
	ports.TerminalForegroundWatcher
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
}

// Timing knobs; variables so tests can shorten them.
var (
	// screenLines is how much of the rendered screen a quiet agent is read for.
	screenLines = 60
	// screenReadTimeout bounds one screen read.
	screenReadTimeout = 3 * time.Second
	// recheckAfter re-reads the screen of a quiet agent whose screen still
	// claimed a turn in progress, so a stale "working" settles.
	recheckAfter = 3 * time.Second
	// watchRetryMin/Max bound the reconnect backoff to a terminal host.
	watchRetryMin = 500 * time.Millisecond
	watchRetryMax = 30 * time.Second
	// maxWatchRetries ends a watch whose host keeps failing.
	maxWatchRetries = 8
	// startupGrace is how long after an agent appears its output is read as
	// it drawing its first screen, not as a turn: the screen decides instead.
	startupGrace = 2500 * time.Millisecond
)

// Manager follows every tracked shell terminal. It holds one watcher
// connection per terminal; the terminal host pushes a change the moment the
// foreground program changes or starts or stops drawing, so detection needs no
// polling.
type Manager struct {
	rt     Runtime
	styled ports.StyledTerminalOutputReader
	agents ports.AgentResolver
	log    *slog.Logger
	now    func() time.Time
	base   context.Context

	mu      sync.Mutex
	tracked map[string]*watch
	current map[string]Agent
	subs    map[*subscriber]struct{}
}

type watch struct {
	cancel context.CancelFunc
	// gen invalidates a pending recheck once a newer reading arrived.
	gen uint64
	// pgid and appeared identify the agent process currently in the
	// foreground and when it showed up, for the startup grace.
	pgid     int
	appeared time.Time
}

type subscriber struct{ ch chan []Agent }

// NewManager builds a manager whose watches live until ctx ends. agents may be
// nil, in which case only the generic prompt patterns detect waiting.
func NewManager(ctx context.Context, rt Runtime, agents ports.AgentResolver, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	styled, _ := rt.(ports.StyledTerminalOutputReader)
	return &Manager{
		rt: rt, styled: styled, agents: agents, log: log, now: time.Now, base: ctx,
		tracked: map[string]*watch{},
		current: map[string]Agent{},
		subs:    map[*subscriber]struct{}{},
	}
}

// TrackShellTerminal starts following a shell terminal. Tracking one twice is
// a no-op.
func (m *Manager) TrackShellTerminal(handleID string) {
	if m == nil || handleID == "" {
		return
	}
	m.mu.Lock()
	if _, ok := m.tracked[handleID]; ok {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.base)
	w := &watch{cancel: cancel}
	m.tracked[handleID] = w
	m.mu.Unlock()
	go m.run(ctx, handleID, w)
}

// UntrackShellTerminal stops following a terminal and forgets its agent.
func (m *Manager) UntrackShellTerminal(handleID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	w := m.tracked[handleID]
	delete(m.tracked, handleID)
	m.mu.Unlock()
	if w != nil {
		w.cancel()
	}
	m.set(handleID, nil)
}

// Snapshot returns every terminal currently running an agent, ordered by
// handle id.
func (m *Manager) Snapshot() []Agent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) snapshotLocked() []Agent {
	out := make([]Agent, 0, len(m.current))
	for _, a := range m.current {
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b Agent) int { return strings.Compare(a.HandleID, b.HandleID) })
	return out
}

// Subscribe delivers the current snapshot at once and the full snapshot again
// after every change. A slow reader only ever sees the latest one.
func (m *Manager) Subscribe() (<-chan []Agent, func()) {
	sub := &subscriber{ch: make(chan []Agent, 1)}
	m.mu.Lock()
	m.subs[sub] = struct{}{}
	sub.ch <- m.snapshotLocked()
	m.mu.Unlock()
	return sub.ch, func() {
		m.mu.Lock()
		delete(m.subs, sub)
		m.mu.Unlock()
	}
}

func (m *Manager) run(ctx context.Context, handleID string, w *watch) {
	defer func() {
		m.mu.Lock()
		if m.tracked[handleID] == w {
			delete(m.tracked, handleID)
		}
		m.mu.Unlock()
		m.set(handleID, nil)
	}()
	handle := ports.RuntimeHandle{ID: handleID}
	backoff := watchRetryMin
	for attempt := 0; attempt < maxWatchRetries; attempt++ {
		started := m.now()
		err := m.rt.WatchTerminalForeground(ctx, handle, func(fg ports.TerminalForeground) {
			m.observe(ctx, handleID, w, fg)
		})
		if ctx.Err() != nil || errors.Is(err, ports.ErrTerminalForegroundUnavailable) {
			return
		}
		// The host went away or the connection dropped. Forget the agent
		// until the watch reconnects and the host reports it again.
		m.set(handleID, nil)
		if m.now().Sub(started) > watchRetryMax {
			attempt, backoff = 0, watchRetryMin
		}
		m.log.Debug("shell agent watch interrupted; retrying", "handleId", handleID, "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, watchRetryMax)
	}
}

// observe turns one foreground reading into the terminal's agent state.
func (m *Manager) observe(ctx context.Context, handleID string, w *watch, fg ports.TerminalForeground) {
	harness := ClassifyArgv(fg.Argv)
	now := m.now()
	m.mu.Lock()
	w.gen++
	gen := w.gen
	if harness == "" {
		w.pgid = 0
	} else if w.pgid != fg.PGID {
		w.pgid, w.appeared = fg.PGID, now
	}
	appeared := w.appeared
	starting := now.Sub(appeared) < startupGrace
	m.mu.Unlock()

	if harness == "" {
		m.set(handleID, nil)
		return
	}
	state := StateWorking
	if !fg.Busy || starting {
		state = m.quietState(ctx, handleID, harness)
	}
	m.set(handleID, &Agent{HandleID: handleID, Harness: harness, State: state})
	recheck := time.Duration(0)
	switch {
	case fg.Busy && starting:
		// Still drawing when the grace ends means a real turn; the host sends
		// nothing new while it stays busy, so look again then.
		recheck = startupGrace - now.Sub(appeared) + 50*time.Millisecond
	case !fg.Busy && state == StateWorking:
		// The screen still shows a turn in progress although nothing is being
		// drawn. Look again shortly rather than trusting it indefinitely.
		recheck = recheckAfter
	}
	if recheck > 0 {
		time.AfterFunc(recheck, func() {
			m.mu.Lock()
			stale := w.gen != gen || m.tracked[handleID] != w
			m.mu.Unlock()
			if stale || ctx.Err() != nil {
				return
			}
			m.observe(ctx, handleID, w, fg)
		})
	}
}

// quietState decides between idle, waiting and working for an agent that has
// stopped drawing, from what its screen shows. It prefers the harness's own
// screen inspector, the same one AO uses for its managed sessions, and falls
// back to recognizing a generic approval prompt.
func (m *Manager) quietState(ctx context.Context, handleID string, harness domain.AgentHarness) State {
	ctx, cancel := context.WithTimeout(ctx, screenReadTimeout)
	defer cancel()
	handle := ports.RuntimeHandle{ID: handleID}

	var styled, plain string
	var styledRead, plainRead bool
	readStyled := func() string {
		if !styledRead {
			styledRead = true
			if m.styled != nil {
				styled, _ = m.styled.GetStyledOutput(ctx, handle, screenLines)
			}
		}
		return styled
	}
	readPlain := func() string {
		if !plainRead {
			plainRead = true
			plain, _ = m.rt.GetOutput(ctx, handle, screenLines)
		}
		return plain
	}

	if m.agents != nil {
		if agent, ok := m.agents.Agent(harness); ok {
			if inspector, ok := agent.(ports.TerminalSurfaceInspector); ok {
				if screen := readStyled(); screen != "" {
					switch inspector.InspectTerminalSurface(screen).Work {
					case ports.TerminalSurfaceWorkWaitingInput, ports.TerminalSurfaceWorkBlocked:
						return StateWaiting
					case ports.TerminalSurfaceWorkActive:
						return StateWorking
					case ports.TerminalSurfaceWorkIdle:
						return StateIdle
					}
				}
			}
			if detector, ok := agent.(ports.TerminalActivityDetector); ok {
				screen := readPlain()
				if opts, ok := agent.(ports.TerminalActivityOptionsProvider); ok && opts.TerminalActivityOptions().PreferStyledOutput {
					if s := readStyled(); s != "" {
						screen = s
					}
				}
				if activity, ok := detector.DetectTerminalActivity(screen); ok {
					switch activity {
					case domain.ActivityWaitingInput, domain.ActivityBlocked:
						return StateWaiting
					case domain.ActivityActive:
						return StateWorking
					case domain.ActivityIdle:
						return StateIdle
					}
				}
			}
		}
	}
	screen := readStyled()
	if screen == "" {
		screen = readPlain()
	}
	if looksLikeApprovalPrompt(screen) {
		return StateWaiting
	}
	return StateIdle
}

// set records a terminal's agent (nil clears it) and notifies subscribers
// when anything visible changed.
func (m *Manager) set(handleID string, next *Agent) {
	m.mu.Lock()
	prev, had := m.current[handleID]
	switch {
	case next == nil && !had:
		m.mu.Unlock()
		return
	case next == nil:
		delete(m.current, handleID)
	case had && prev.Harness == next.Harness && prev.State == next.State:
		m.mu.Unlock()
		return
	default:
		next.Since = m.now().UTC()
		m.current[handleID] = *next
	}
	snapshot := m.snapshotLocked()
	for sub := range m.subs {
		select {
		case <-sub.ch:
		default:
		}
		sub.ch <- snapshot
	}
	m.mu.Unlock()
}

var (
	ansiEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[\x40-\x5f])`)
	// approvalPrompt matches the questions agent TUIs ask before acting:
	// permission dialogs and yes/no confirmations.
	approvalPrompt = regexp.MustCompile(`(?i)(do you want to (proceed|continue|make this edit|create|run|allow|apply)|would you like to (run|make|apply|proceed)|allow (once|always|this|command|execution|edit|tool)|requires? (your )?(approval|permission)|permission (required|request)|approve (this|the|command|edit)|waiting for (your )?(approval|confirmation|input)|\((y/n|yes/no)\)|\[(y/n|yes/no)\])`)
	// approvalPromptLines bounds the search to the bottom of the screen, where
	// a live prompt sits, so a question answered earlier does not match.
	approvalPromptLines = 15
)

func looksLikeApprovalPrompt(screen string) bool {
	plain := ansiEscape.ReplaceAllString(strings.ReplaceAll(screen, "\r", ""), "")
	lines := strings.Split(plain, "\n")
	kept := make([]string, 0, approvalPromptLines)
	for i := len(lines) - 1; i >= 0 && len(kept) < approvalPromptLines; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			kept = append(kept, lines[i])
		}
	}
	return approvalPrompt.MatchString(strings.Join(kept, "\n"))
}
