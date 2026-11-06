package shellagent

import (
	"context"
	"encoding/json"
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
	// StateWorking means the agent is drawing output.
	StateWorking State = "working"
	// StateIdle means the agent is at its prompt with nothing to ask.
	StateIdle State = "idle"
	// StateWaiting means the agent stopped on a question or permission prompt.
	StateWaiting State = "waiting"
)

// Agent is the coding agent running in one shell terminal.
type Agent struct {
	HandleID string              `json:"handleId"`
	Harness  domain.AgentHarness `json:"harness"`
	State    State               `json:"state"`
}

// Runtime is the slice of the runtime adapter the manager needs.
type Runtime interface {
	ports.TerminalForegroundWatcher
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
}

// Variables so tests can shorten them.
var (
	screenLines       = 60
	screenReadTimeout = 3 * time.Second
	watchRetryMin     = 500 * time.Millisecond
	watchRetryMax     = 30 * time.Second
)

// Manager remembers shell terminals and watches them while a client subscribes.
// The terminal host pushes readings when the foreground program changes or
// starts or stops drawing, so nothing polls.
type Manager struct {
	rt     Runtime
	styled ports.StyledTerminalOutputReader
	agents ports.AgentResolver
	log    *slog.Logger
	base   context.Context

	mu      sync.Mutex
	tracked map[string]context.CancelFunc
	current map[string]Agent
	subs    map[int]func(json.RawMessage)
	nextSub int
}

// NewManager builds a manager whose watches live while clients subscribe and
// ctx is active. agents may be nil; then generic prompts detect waiting.
func NewManager(ctx context.Context, rt Runtime, agents ports.AgentResolver, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	styled, _ := rt.(ports.StyledTerminalOutputReader)
	return &Manager{
		rt: rt, styled: styled, agents: agents, log: log, base: ctx,
		tracked: map[string]context.CancelFunc{},
		current: map[string]Agent{},
		subs:    map[int]func(json.RawMessage){},
	}
}

// TrackShellTerminal starts following a shell terminal; tracking twice is a no-op.
func (m *Manager) TrackShellTerminal(handleID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tracked[handleID]; ok || handleID == "" {
		return
	}
	m.tracked[handleID] = nil
	if len(m.subs) > 0 {
		m.startWatchLocked(handleID)
	}
}

func (m *Manager) startWatchLocked(handleID string) {
	ctx, cancel := context.WithCancel(m.base)
	m.tracked[handleID] = cancel
	go m.watch(ctx, handleID)
}

// UntrackShellTerminal stops following a terminal and forgets its agent.
func (m *Manager) UntrackShellTerminal(handleID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel := m.tracked[handleID]; cancel != nil {
		cancel()
	}
	delete(m.tracked, handleID)
	m.setLocked(handleID, nil)
}

// SubscribeShellAgents calls fn with the current agents as JSON at once and
// after every change. fn runs with the manager's lock held and must not block.
func (m *Manager) SubscribeShellAgents(fn func(json.RawMessage)) func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextSub
	m.nextSub++
	m.subs[id] = fn
	if len(m.subs) == 1 {
		for handleID := range m.tracked {
			m.startWatchLocked(handleID)
		}
	}
	fn(m.snapshotLocked())
	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		if len(m.subs) == 0 {
			for handleID, cancel := range m.tracked {
				if cancel != nil {
					cancel()
				}
				m.tracked[handleID] = nil
			}
			clear(m.current)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) watch(ctx context.Context, handleID string) {
	defer m.apply(ctx, handleID, nil)
	handle := ports.RuntimeHandle{ID: handleID}
	for backoff := watchRetryMin; ; backoff = min(backoff*2, watchRetryMax) {
		err := m.rt.WatchTerminalForeground(ctx, handle, func(fg ports.TerminalForeground) {
			m.observe(ctx, handleID, fg)
		})
		if ctx.Err() != nil || errors.Is(err, ports.ErrTerminalForegroundUnavailable) {
			return
		}
		// The host went away; forget the agent until a new reading arrives.
		m.apply(ctx, handleID, nil)
		m.log.Debug("shell agent watch interrupted; retrying", "handleId", handleID, "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func (m *Manager) observe(ctx context.Context, handleID string, fg ports.TerminalForeground) {
	harness := ClassifyArgv(fg.Argv)
	if harness == "" {
		m.apply(ctx, handleID, nil)
		return
	}
	state := StateWorking
	if !fg.Busy {
		state = StateIdle
		if m.waiting(ctx, handleID, harness) {
			state = StateWaiting
		}
	}
	m.apply(ctx, handleID, &Agent{HandleID: handleID, Harness: harness, State: state})
}

// apply records a reading unless its watch already ended: a screen read can
// finish after the terminal closed.
func (m *Manager) apply(ctx context.Context, handleID string, next *Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() == nil {
		m.setLocked(handleID, next)
	}
}

// waiting reports whether a quiet agent's screen shows a question or a
// permission prompt. It asks the harness's own screen check, the one AO uses
// for its managed sessions, and falls back to common approval wording.
func (m *Manager) waiting(ctx context.Context, handleID string, harness domain.AgentHarness) bool {
	ctx, cancel := context.WithTimeout(ctx, screenReadTimeout)
	defer cancel()
	handle := ports.RuntimeHandle{ID: handleID}
	read := func(styled bool) string {
		if styled && m.styled != nil {
			if screen, _ := m.styled.GetStyledOutput(ctx, handle, screenLines); screen != "" {
				return screen
			}
		}
		screen, _ := m.rt.GetOutput(ctx, handle, screenLines)
		return screen
	}
	var agent ports.Agent
	if m.agents != nil {
		agent, _ = m.agents.Agent(harness)
	}
	if inspector, ok := agent.(ports.TerminalSurfaceInspector); ok {
		work := inspector.InspectTerminalSurface(read(true)).Work
		return work == ports.TerminalSurfaceWorkWaitingInput || work == ports.TerminalSurfaceWorkBlocked
	}
	if detector, ok := agent.(ports.TerminalActivityDetector); ok {
		opts, _ := agent.(ports.TerminalActivityOptionsProvider)
		styled := opts != nil && opts.TerminalActivityOptions().PreferStyledOutput
		if activity, ok := detector.DetectTerminalActivity(read(styled)); ok {
			return activity == domain.ActivityWaitingInput || activity == domain.ActivityBlocked
		}
	}
	return looksLikeApprovalPrompt(read(true))
}

func (m *Manager) setLocked(handleID string, next *Agent) {
	prev, had := m.current[handleID]
	switch {
	case next == nil && !had, next != nil && had && prev == *next:
		return
	case next == nil:
		delete(m.current, handleID)
	default:
		m.current[handleID] = *next
	}
	snapshot := m.snapshotLocked()
	for _, fn := range m.subs {
		fn(snapshot)
	}
}

func (m *Manager) snapshotLocked() json.RawMessage {
	agents := make([]Agent, 0, len(m.current))
	for _, a := range m.current {
		agents = append(agents, a)
	}
	slices.SortFunc(agents, func(a, b Agent) int { return strings.Compare(a.HandleID, b.HandleID) })
	b, _ := json.Marshal(agents)
	return b
}

var (
	ansiEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[\x40-\x5f])`)
	// approvalPrompt matches the questions agent TUIs ask before acting.
	approvalPrompt = regexp.MustCompile(`(?i)(do you want to (proceed|continue|make this edit|create|run|allow|apply)|would you like to (run|make|apply|proceed)|allow (once|always|this|command|execution|edit|tool)|requires? (your )?(approval|permission)|permission (required|request)|approve (this|the|command|edit)|waiting for (your )?(approval|confirmation|input)|\((y/n|yes/no)\)|\[(y/n|yes/no)\])`)
)

// looksLikeApprovalPrompt checks the last 15 non-blank lines, where a live
// prompt sits, so a question answered earlier does not match.
func looksLikeApprovalPrompt(screen string) bool {
	lines := strings.Split(ansiEscape.ReplaceAllString(strings.ReplaceAll(screen, "\r", ""), ""), "\n")
	kept := make([]string, 0, 15)
	for i := len(lines) - 1; i >= 0 && len(kept) < 15; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			kept = append(kept, lines[i])
		}
	}
	return approvalPrompt.MatchString(strings.Join(kept, "\n"))
}
