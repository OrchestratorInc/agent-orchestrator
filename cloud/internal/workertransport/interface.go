package workertransport

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

const (
	InterfaceTUI  = "tui"
	InterfaceChat = "chat"
)

type interfacePayload struct {
	SourceInterface      string    `json:"sourceInterface"`
	TargetInterface      string    `json:"targetInterface"`
	Policy               string    `json:"policy"`
	Rollback             bool      `json:"rollback"`
	NativeConversationID string    `json:"nativeConversationId"`
	Model                string    `json:"model,omitempty"`
	ReasoningEffort      string    `json:"reasoningEffort,omitempty"`
	SelectionAt          time.Time `json:"selectionAt,omitempty"`
	SessionID            string    `json:"sessionId"`
}

type interfaceInspectResult struct {
	Idle                 bool `json:"idle"`
	WaitingForInput      bool `json:"waitingForInput"`
	DecisionPending      bool `json:"decisionPending"`
	DraftPresent         bool `json:"draftPresent"`
	QuiescenceUnverified bool `json:"quiescenceUnverified"`
}

type interfaceReadyResult struct {
	Ready     bool   `json:"ready"`
	Interface string `json:"interface"`
}

// InterfaceTransition drives the run-time swap between the interactive agent
// terminal (TUI) and the headless turn-based Chat controller. It owns the
// current committed interface and the chat runner lifecycle.
type InterfaceTransition struct {
	mu             sync.Mutex
	current        string
	chatRun        context.CancelFunc
	chatDone       chan struct{}
	chatRunning    bool
	chatReady      bool
	chatGeneration uint64
	agentTermID    string
}

func (t *InterfaceTransition) Current() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.current
}

func (s *Supervisor) handleInterface(
	ctx context.Context,
	input interfacePayload,
	kind string,
) (any, error) {
	switch kind {
	case "interface.inspect":
		return s.inspectInterface()
	case "interface.native-id":
		return map[string]any{"nativeConversationId": s.nativeConversationID(ctx, input)}, nil
	case "interface.interrupt":
		return map[string]bool{"ok": true}, s.interruptInterface(ctx)
	case "interface.stop":
		if input.Rollback && s.iface.Current() != input.SourceInterface {
			return map[string]bool{"ok": true}, nil
		}
		return map[string]bool{"ok": true}, s.stopInterface(ctx, input.Policy)
	case "interface.start":
		return map[string]bool{"ok": true}, s.startInterface(ctx, input)
	case "interface.ready":
		return s.controllerReady(input.TargetInterface), nil
	default:
		return nil, errors.New("unsupported interface command")
	}
}

func (s *Supervisor) controllerReady(expected string) interfaceReadyResult {
	s.iface.mu.Lock()
	current := s.iface.current
	chatReady := s.iface.chatRunning && s.iface.chatReady
	s.iface.mu.Unlock()
	if expected != "" && current != expected {
		return interfaceReadyResult{Interface: current}
	}

	ready := false
	switch current {
	case InterfaceChat:
		ready = chatReady
	case InterfaceTUI:
		s.mu.Lock()
		agentTerminalID := s.AgentTerminalID
		_, terminalOpen := s.terminals[agentTerminalID]
		ready = s.agentStarted && terminalOpen && (!s.holdAgentInput || s.workspaceReady)
		s.mu.Unlock()
	}
	return interfaceReadyResult{Ready: ready, Interface: current}
}

func (s *Supervisor) inspectInterface() (any, error) {
	s.iface.mu.Lock()
	defer s.iface.mu.Unlock()
	if s.iface.current == InterfaceTUI {
		if s.startupTaskWaiting() {
			if s.startupFailureWaiting() {
				// The startup failed and waits for the session's retry, which
				// may never come: no task runs, but the terminal keeps it. The
				// drain waits a bounded time and then fails, leaving the
				// session (and its task) in the terminal; a stop-now switch
				// still moves it.
				return interfaceInspectResult{QuiescenceUnverified: true}, nil
			}
			// The terminal agent has not started yet (its workspace is not
			// ready) and runs the session's task from its command line once it
			// does. A drain waits for that task, as for any running turn, so a
			// switch to Chat does not drop it.
			return interfaceInspectResult{Idle: false}, nil
		}
		idle, unverified := s.tuiSourceIdle()
		return interfaceInspectResult{
			Idle:                 idle,
			QuiescenceUnverified: unverified,
		}, nil
	}
	// Chat work is headless, so it can report its actual turn activity.
	idle := true
	if s.iface.current == InterfaceChat {
		if activity, ok := s.ChatRunner.(chatActivity); ok {
			idle = activity.Idle()
		}
	}
	return interfaceInspectResult{
		Idle:            idle,
		WaitingForInput: false,
	}, nil
}

func (s *Supervisor) interruptInterface(ctx context.Context) error {
	if s.iface.Current() == InterfaceChat {
		if runner, ok := s.ChatRunner.(chatInterrupter); ok {
			runner.Interrupt()
			return nil
		}
		return errors.New("chat controller cannot interrupt the active turn")
	}
	agentTerminalID := s.agentTerminalID()
	s.mu.Lock()
	terminal := s.terminals[agentTerminalID]
	s.mu.Unlock()
	if terminal == nil {
		return nil
	}
	_, err := terminal.pty.Write([]byte{0x03})
	return err
}

func (s *Supervisor) tuiSourceIdle() (bool, bool) {
	stopAt, err := worker.ReadTUIStop(s.DataDir)
	s.mu.Lock()
	_, open := s.terminals[s.AgentTerminalID]
	startedAt, inputAt := s.tuiStartedAt, s.lastTUIInputAt
	s.mu.Unlock()
	if !open {
		return true, false
	}
	verified := err == nil && !startedAt.IsZero() && stopAt.After(startedAt) && stopAt.After(inputAt)
	return verified, !verified
}

func (s *Supervisor) stopInterface(ctx context.Context, policy string) error {
	if s.iface.Current() == InterfaceTUI {
		// The drain verdict is checked again at the actual stop boundary. A
		// keystroke arriving between inspect and stop must not be interrupted.
		stopAt, stopErr := worker.ReadTUIStop(s.DataDir)
		s.mu.Lock()
		id := s.AgentTerminalID
		_, open := s.terminals[id]
		if open && policy != "interrupt" {
			verified := stopErr == nil && !s.tuiStartedAt.IsZero() &&
				stopAt.After(s.tuiStartedAt) && stopAt.After(s.lastTUIInputAt)
			if !verified {
				s.mu.Unlock()
				return errors.New("terminal activity changed before stop; retry after the agent finishes")
			}
		}
		s.tuiHandoffClosing = true
		// The session's own terminal agent that could not start never starts
		// now either; its failure must not stay set in Chat.
		failureReported := s.ownAgentStartFailed
		s.ownAgentStartFailed = false
		if pending := s.pendingTUIStart; pending != nil {
			// A switch away before the workspace became ready (or while the
			// terminal agent's start failed): the terminal agent never
			// started, and now never does.
			failureReported = failureReported || pending.reported
			s.pendingTUIStart = nil
			s.agentStarting = false
			s.pendingAgentTerminalData = nil
			s.pendingAgentTerminalSize = nil
		}
		s.mu.Unlock()
		if failureReported && s.AgentStartFailed != nil {
			// The start that failed no longer stands in the session's way.
			s.AgentStartFailed(ctx, nil)
		}
		err := s.closeTerminalForInterfaceHandoff(ctx, id)
		if err != nil {
			s.mu.Lock()
			s.tuiHandoffClosing = false
			s.mu.Unlock()
		}
		return err
	} else {
		return s.stopChat(ctx)
	}
}

func (s *Supervisor) startInterface(ctx context.Context, input interfacePayload) error {
	if input.TargetInterface == InterfaceChat {
		return s.startChat(ctx)
	}
	return s.startTUI(ctx, input, false)
}

// startTUI starts the terminal agent of a switch to the terminal. While the
// workspace is not ready, and always when retried is set, the start is
// reserved (pendingTUIStart) and started by startPendingTUI, which tries a
// failing start again and then reports it as the session's startup error
// with a Retry. Otherwise a failed start fails the switch.
func (s *Supervisor) startTUI(ctx context.Context, input interfacePayload, retried bool) error {
	// Chat -> TUI closes the old agent terminal record. Obtain the replacement
	// handle from the control plane before starting the PTY; otherwise the
	// worker starts a process on the stale bootstrap handle while the renderer
	// attaches to a newly ticketed handle. The two processes then diverge (and
	// Codex can report an active thread writer).
	terminal, err := s.ensureAgentTerminal(ctx)
	if err != nil && !retried {
		return err
	}
	// A start that is retried (the Chat start fallback, after the control
	// plane already moved the session) is reserved even without a terminal:
	// startPendingTUI asks for it again, and a lasting failure is reported
	// with a Retry, rather than the worker staying in a stopped Chat with the
	// task queued for a terminal that never starts.
	s.mu.Lock()
	if retried || (s.holdAgentInput && !s.workspaceReady) {
		// The checkout or the project's Setup is still running (or failed and
		// waits for a retry). The terminal agent runs its task from its command
		// line as soon as it starts, so it must not start in a workspace that is
		// not ready: commit the switch, reserve the agent terminal (its input
		// and viewport are kept, as during a TUI session's own startup) and
		// start it when MarkWorkspaceReady releases the workspace.
		if terminal.TerminalID != "" {
			s.AgentTerminalID = terminal.TerminalID
		}
		s.agentStarting = true
		// An earlier switch away (stopInterface) refused input to the closing
		// terminal; this one is reserved and takes input again.
		s.tuiHandoffClosing = false
		s.pendingTUIStart = &pendingTUIStart{input: input, terminal: terminal}
		s.mu.Unlock()
		// After s.mu: the interface lock is taken first everywhere else. The
		// start MarkWorkspaceReady queues runs on this same loop, so after this.
		s.iface.mu.Lock()
		s.iface.current = InterfaceTUI
		s.iface.mu.Unlock()
		if retried {
			return s.startPendingTUI(ctx)
		}
		return nil
	}
	s.mu.Unlock()
	return s.openTUIAgent(ctx, input, terminal)
}

// ensureAgentTerminal is Control.EnsureAgentTerminal, refusing a response
// without a terminal.
func (s *Supervisor) ensureAgentTerminal(ctx context.Context) (worker.AgentTerminalResponse, error) {
	terminal, err := s.Control.EnsureAgentTerminal(ctx)
	if err != nil {
		return worker.AgentTerminalResponse{}, err
	}
	if strings.TrimSpace(terminal.TerminalID) == "" {
		return worker.AgentTerminalResponse{}, errors.New("control plane returned no agent terminal")
	}
	return terminal, nil
}

// pendingTUIStart is a switch to the terminal committed while the workspace
// was not ready yet. MarkWorkspaceReady starts it. A start that fails is
// tried again (attempts) and then waits for the session's Retry (failed); its
// failure was reported to the control plane (reported). Guarded by s.mu.
type pendingTUIStart struct {
	input    interfacePayload
	terminal worker.AgentTerminalResponse
	attempts int
	failed   bool
	reported bool
}

// agentStartAttempts is how many times a terminal agent start that fails
// (the coding-agent credential could not be read, the command could not be
// built, the process did not start) is tried before it waits for a Retry.
const agentStartAttempts = 3

// defaultAgentStartRetryDelay is the pause before the second attempt; each
// later one waits longer.
const defaultAgentStartRetryDelay = 2 * time.Second

// startPendingTUI starts the terminal agent a switch committed while the
// workspace was not ready. It runs on Run's serial loop, queued by
// MarkWorkspaceReady (or a retry of a failed start), so it never interleaves
// with an interface command; a switch away from the terminal in the meantime
// cancelled it (stopInterface).
//
// A failed start is tried again after a short pause, and after
// agentStartAttempts it is reported (AgentStartFailed) as the session's
// startup error, whose Retry (retryFailedAgentStart) tries again. The
// terminal stays reserved meanwhile, and the session's task with it: it runs
// once, when a start succeeds (or in Chat, after a switch back).
func (s *Supervisor) startPendingTUI(ctx context.Context) error {
	s.mu.Lock()
	pending := s.pendingTUIStart
	ready := !s.holdAgentInput || s.workspaceReady
	s.mu.Unlock()
	if pending == nil || !ready {
		return nil
	}
	if s.iface.Current() != InterfaceTUI {
		s.mu.Lock()
		if s.pendingTUIStart == pending {
			s.pendingTUIStart = nil
		}
		s.mu.Unlock()
		return nil
	}
	terminal := pending.terminal
	var err error
	if terminal.TerminalID == "" {
		// Reserved without its terminal (startTUI): ask for it again.
		terminal, err = s.ensureAgentTerminal(ctx)
		if err == nil {
			s.mu.Lock()
			if s.pendingTUIStart == pending {
				pending.terminal = terminal
				s.AgentTerminalID = terminal.TerminalID
			}
			s.mu.Unlock()
		}
	}
	if err == nil {
		err = s.openTUIAgent(ctx, pending.input, terminal)
	}
	s.mu.Lock()
	if s.pendingTUIStart != pending {
		s.mu.Unlock()
		return err
	}
	if err == nil {
		s.pendingTUIStart = nil
		reported := pending.reported
		s.mu.Unlock()
		if reported && s.AgentStartFailed != nil {
			s.AgentStartFailed(ctx, nil)
		}
		return nil
	}
	pending.attempts++
	attempt := pending.attempts
	lasting := attempt >= agentStartAttempts
	if lasting {
		pending.failed = true
		pending.reported = true
	}
	delay := s.agentStartRetryDelay
	s.mu.Unlock()
	if !lasting {
		if delay <= 0 {
			delay = defaultAgentStartRetryDelay
		}
		s.Logger.Warn("start the terminal agent after the workspace became ready; trying again",
			"attempt", attempt, "error", err)
		time.AfterFunc(time.Duration(attempt)*delay, func() {
			s.queueOnLoop(func(ctx context.Context) error {
				s.mu.Lock()
				current := s.pendingTUIStart == pending && !pending.failed
				s.mu.Unlock()
				if !current {
					return nil
				}
				return s.startPendingTUI(ctx)
			})
		})
		return err
	}
	s.Logger.Error("start the terminal agent after the workspace became ready; waiting for a retry", "error", err)
	if s.AgentStartFailed != nil {
		s.AgentStartFailed(ctx, err)
	}
	return err
}

// retryFailedAgentStart tries again a terminal agent start that failed and
// waits for a retry (startPendingTUI), on Run's serial loop. It reports
// whether there was one.
func (s *Supervisor) retryFailedAgentStart() bool {
	s.mu.Lock()
	pending := s.pendingTUIStart
	if pending == nil || !pending.failed {
		s.mu.Unlock()
		return false
	}
	pending.failed = false
	pending.attempts = 0
	s.mu.Unlock()
	s.queueOnLoop(s.startPendingTUI)
	return true
}

// openTUIAgent starts the terminal agent of a switch to the terminal on the
// agent terminal the control plane ticketed for it.
func (s *Supervisor) openTUIAgent(ctx context.Context, input interfacePayload, terminal worker.AgentTerminalResponse) error {
	// Rebuild the interactive command only when a real native conversation was
	// observed after ChatUI's turn. For a fresh session there is no identity to
	// resume; keep the plain bootstrap command instead of manufacturing a resume
	// command for a conversation that does not exist.
	nativeConversationID := s.nativeConversationID(ctx, input)
	s.mu.Lock()
	needsFreshCommand := s.AgentCommand.Path == ""
	s.mu.Unlock()
	if nativeConversationID != "" || needsFreshCommand || input.Model != "" || input.ReasoningEffort != "" {
		if err := s.refreshAgentCommand(ctx, nativeConversationID, input.Model, input.ReasoningEffort); err != nil {
			return err
		}
	}
	s.setAgentTerminalID(terminal.TerminalID)
	s.iface.mu.Lock()
	s.iface.current = InterfaceTUI
	s.iface.mu.Unlock()
	if err := s.openTerminal(ctx, worker.TerminalCommand{
		TerminalID:         terminal.TerminalID,
		NextOutputSequence: terminal.NextOutputSequence,
		Kind:               "agent",
	}); err != nil {
		// openTerminal ran the command's cleanup: the next start builds it
		// again rather than reusing its removed launch files.
		s.mu.Lock()
		s.AgentCommand = workerexec.Command{}
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	s.agentStarting = false
	s.agentStarted = true
	if input.Model != "" {
		s.SelectedModel = input.Model
		s.SelectedEffort = input.ReasoningEffort
		s.SelectionAt = input.SelectionAt
	}
	s.mu.Unlock()
	s.flushReadyAgentTerminal()
	if s.AgentReady != nil {
		s.AgentReady(ctx)
	}
	return nil
}

func (s *Supervisor) agentTerminalID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.AgentTerminalID
}

func (s *Supervisor) setAgentTerminalID(id string) {
	s.mu.Lock()
	s.AgentTerminalID = id
	s.mu.Unlock()
}

func (s *Supervisor) refreshAgentCommand(ctx context.Context, nativeConversationID, model, effort string) error {
	if s.AgentCommandFactory == nil {
		return nil
	}
	command, err := s.AgentCommandFactory(ctx, nativeConversationID, model, effort)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.AgentCommand = command
	s.mu.Unlock()
	return nil
}

func (s *Supervisor) nativeConversationID(ctx context.Context, input interfacePayload) string {
	if id, err := s.Control.AgentSessionID(ctx); err == nil && strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	// The worker's bootstrap value is a fallback only. Hooks can discover a
	// newer provider conversation while this worker is running (for example
	// after a ChatUI turn), so prefer the control-plane value above.
	if id := strings.TrimSpace(s.AgentSessionID); id != "" {
		return id
	}
	return strings.TrimSpace(input.NativeConversationID)
}

func (s *Supervisor) stopChat(ctx context.Context) error {
	// The turn records its interrupted/completed result before Idle becomes true.
	// Cancelling the controller first also cancels that durable write, leaving
	// the old turn visibly running after the interface has changed.
	if activity, ok := s.ChatRunner.(chatActivity); ok {
		fencer, ok := s.ChatRunner.(interface{ FenceClaims() func() })
		if !ok {
			return errors.New("chat controller cannot fence new turns during handoff")
		}
		release := fencer.FenceClaims()
		defer release()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for !activity.Idle() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
	s.iface.mu.Lock()
	cancel := s.iface.chatRun
	done := s.iface.chatDone
	s.iface.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}
	// The Chat runner owns the provider's native thread writer. Cancelling its
	// context only requests shutdown; do not let the TUI start until the
	// headless provider process has actually exited and released that writer.
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FallBackToTUI replaces a Chat start that cannot run with the terminal
// agent, as a Chat-to-TUI handoff's target start does: it stops the Chat
// controller and opens the agent terminal with a freshly built command. It is
// for a session whose Chat controller has not taken a turn yet (it still
// waits on ChatWorkspaceReady), after the control plane moved the session to
// the TUI. A session not in Chat is left alone. It runs on Run's serial loop,
// as the control plane's interface commands do, so it never interleaves with
// one of them; it waits for that loop.
func (s *Supervisor) FallBackToTUI(ctx context.Context) error {
	return s.runOnLoop(ctx, func(ctx context.Context) error {
		if s.iface.Current() != InterfaceChat {
			return nil
		}
		if err := s.stopChat(ctx); err != nil {
			return err
		}
		// The control plane already moved the session to the terminal, so a
		// start that fails is tried again and then reported with a Retry
		// (startPendingTUI): the session's task waits in the turn queue for
		// the terminal's forwarder, and must not wait behind an agent that
		// silently never starts.
		return s.startTUI(ctx, interfacePayload{TargetInterface: InterfaceTUI}, true)
	})
}

func (s *Supervisor) startChat(ctx context.Context) error {
	if s.ChatRunner == nil {
		return errors.New("chat controller is unavailable for this session")
	}
	s.iface.mu.Lock()
	if s.iface.chatRunning {
		s.iface.mu.Unlock()
		return nil
	}
	if resetter, ok := s.ChatRunner.(interface{ ResetInterrupt() }); ok {
		resetter.ResetInterrupt()
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.iface.chatGeneration++
	generation := s.iface.chatGeneration
	done := make(chan struct{})
	s.iface.chatRun = cancel
	s.iface.chatDone = done
	s.iface.chatRunning = true
	s.iface.chatReady = false
	s.iface.current = InterfaceChat
	s.iface.mu.Unlock()
	go func() {
		if ready := s.ChatWorkspaceReady; ready != nil {
			select {
			case <-runCtx.Done():
			case <-ready:
			}
		}
		if runCtx.Err() == nil {
			s.iface.mu.Lock()
			if s.iface.chatGeneration == generation {
				s.iface.chatReady = true
			}
			s.iface.mu.Unlock()
			if err := s.ChatRunner.Run(runCtx); err != nil && runCtx.Err() == nil {
				s.Logger.Warn("chat controller stopped", "error", err)
			}
		}
		s.iface.mu.Lock()
		if s.iface.chatGeneration == generation {
			s.iface.chatRun = nil
			s.iface.chatDone = nil
			s.iface.chatRunning = false
			s.iface.chatReady = false
		}
		s.iface.mu.Unlock()
		close(done)
	}()
	return nil
}
