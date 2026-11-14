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
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type State string

const (
	StateWorking State = "working"
	StateIdle    State = "idle"
	StateWaiting State = "waiting"
)

type Agent struct {
	HandleID string              `json:"handleId"`
	Harness  domain.AgentHarness `json:"harness"`
	State    State               `json:"state"`
}

type Runtime interface {
	ports.TerminalForegroundWatcher
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
}

var (
	screenLines            = 60
	screenReadTimeout      = 3 * time.Second
	initialWatchRetryDelay = 500 * time.Millisecond
	watchRetryMax          = 30 * time.Second
	maxWatchFailures       = 6
)

type shellWatch struct{ cancel context.CancelFunc }

type Manager struct {
	rt     Runtime
	styled ports.StyledTerminalOutputReader
	agents ports.AgentResolver
	log    *slog.Logger
	base   context.Context

	mu      sync.Mutex
	tracked map[string]*shellWatch
	current map[string]Agent
	subs    map[int]func(json.RawMessage)
	nextSub int
}

func NewManager(ctx context.Context, rt Runtime, agents ports.AgentResolver, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	styled, _ := rt.(ports.StyledTerminalOutputReader)
	return &Manager{
		rt: rt, styled: styled, agents: agents, log: log, base: ctx,
		tracked: map[string]*shellWatch{},
		current: map[string]Agent{},
		subs:    map[int]func(json.RawMessage){},
	}
}

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
	w := &shellWatch{cancel: cancel}
	m.tracked[handleID] = w
	go m.watch(ctx, handleID, w)
}

func (m *Manager) UntrackShellTerminal(handleID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w := m.tracked[handleID]; w != nil {
		w.cancel()
	}
	delete(m.tracked, handleID)
	m.setLocked(handleID, nil)
}

func (m *Manager) SubscribeShellAgents(fn func(json.RawMessage)) func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextSub
	m.nextSub++
	m.subs[id] = fn
	for handleID, w := range m.tracked {
		if w == nil {
			m.startWatchLocked(handleID)
		}
	}
	fn(m.snapshotLocked())
	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		if len(m.subs) == 0 {
			for handleID, w := range m.tracked {
				if w != nil {
					w.cancel()
				}
				m.tracked[handleID] = nil
			}
			clear(m.current)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) watch(ctx context.Context, handleID string, self *shellWatch) {
	defer m.apply(ctx, handleID, nil)
	handle := ports.RuntimeHandle{ID: handleID}
	failures := 0
	for backoff := initialWatchRetryDelay; ; backoff = min(backoff*2, watchRetryMax) {
		var connected atomic.Bool
		err := m.rt.WatchTerminalForeground(ctx, handle, func(fg ports.TerminalForeground) {
			connected.Store(true)
			m.observe(ctx, handleID, fg)
		})
		if ctx.Err() != nil || errors.Is(err, ports.ErrTerminalForegroundUnavailable) {
			return
		}
		if connected.Load() {
			failures, backoff = 0, initialWatchRetryDelay
		} else {
			failures++
			if failures >= maxWatchFailures {
				m.log.Debug("shell agent watch gave up on an unreachable host", "handleId", handleID, "err", err)
				m.mu.Lock()
				if m.tracked[handleID] == self {
					m.tracked[handleID] = nil
					m.setLocked(handleID, nil)
				}
				m.mu.Unlock()
				self.cancel()
				return
			}
		}
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
		if fg.EchoOnly && m.currentState(handleID, harness) == StateIdle {
			return
		}
		state = StateIdle
		if m.waiting(ctx, handleID, harness) {
			state = StateWaiting
		}
	}
	m.apply(ctx, handleID, &Agent{HandleID: handleID, Harness: harness, State: state})
}

func (m *Manager) currentState(handleID string, harness domain.AgentHarness) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agent, ok := m.current[handleID]; ok && agent.Harness == harness {
		return agent.State
	}
	return ""
}

func (m *Manager) apply(ctx context.Context, handleID string, next *Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() == nil {
		m.setLocked(handleID, next)
	}
}

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
	ansiEscape     = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[\x40-\x5f])`)
	approvalPrompt = regexp.MustCompile(`(?i)(do you want to (proceed|continue|make this edit|create|run|allow|apply)|would you like to (run|make|apply|proceed)|allow (once|always|this|command|execution|edit|tool)|requires? (your )?(approval|permission)|permission (required|request)|approve (this|the|command|edit)|waiting for (your )?(approval|confirmation|input)|\((y/n|yes/no)\)|\[(y/n|yes/no)\])`)
)

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
