package workertransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
	"github.com/creack/pty"
)

type Control interface {
	ClaimTransport(context.Context) (*worker.TransportRequest, error)
	ClaimTurn(context.Context) (*worker.Turn, error)
	// WaitForWork blocks until the control plane signals a new turn/transport
	// enqueue for this session (or a short server-side timeout), replacing the
	// old busy-poll. It returns no work; the caller re-runs the claim RPCs.
	WaitForWork(context.Context) error
	CompleteTurn(context.Context, string, int, bool) error
	FailTurn(context.Context, string, int, string) error
	CompleteTransport(context.Context, string, int, any) error
	FailTransport(context.Context, string, int, string, string) error
	PublishTerminalOutput(context.Context, string, int64, []byte) error
	PublishTerminalExit(context.Context, string, int, bool) error
	AgentSessionID(context.Context) (string, error)
	EnsureAgentTerminal(context.Context) (worker.AgentTerminalResponse, error)
}

// workWaitFallback bounds the loop's back-off when WaitForWork is unavailable
// (an older control plane without the endpoint) or errors transiently, so the
// worker degrades to a slow poll rather than a tight spin.
const workWaitFallback = 2 * time.Second

// Fallback PTY geometry when an open request carries no client dimensions. The
// agent terminal is spawned at worker boot (StartAgent), autonomously, long
// before any human attaches — so there is no viewer width to honor yet, and the
// coding agent draws its full-screen intro (the welcome box, "What's new") once,
// committing that fixed-layout box art to scrollback. Committed scrollback never
// reflows: a viewer whose pane is NARROWER than the boot width sees every line
// overflow and wrap, garbling the banner permanently (the live region still
// repaints correctly on the client's resize — only history is stuck).
//
// So the fallback must be a width the viewer is essentially always at least as
// wide as. 80 is the canonical terminal width every agent TUI is designed to
// render at, and every realistic AO viewer pane is >= 80 columns, so the banner
// renders cleanly (under-filling at worst, never overflowing). The client's
// authoritative resize immediately expands the live UI to the full pane width.
// A client-provided size, when present, always wins over these.
const (
	fallbackTerminalColumns = 80
	fallbackTerminalRows    = 24
)

type Supervisor struct {
	Control   Control
	Workspace string
	// Repositories are the session's additional repository checkouts beside
	// Workspace (worker.SessionRepoCheckouts without the main one). Workspace
	// requests select one with "repository".
	Repositories []worker.RepoCheckout
	// MainRepository is the main repository's identity (URL and GitHub
	// owner/name); its Name and Path are always the Workspace folder's.
	MainRepository      worker.RepoCheckout
	repositories        repositoryWorkspaces
	DataDir             string
	Harness             string
	SelectedModel       string
	SelectedEffort      string
	SelectionAt         time.Time
	CompareBase         string
	Shell               string
	AgentCommand        workerexec.Command
	AgentCommandFactory AgentCommandFactory
	ReviewCommand       workerexec.Command
	// ReviewCommandFactory resolves a fresh, provider-specific command on each
	// review launch. It lets the Cloud reviewer selector change independently
	// from the session's already-running interactive harness.
	ReviewCommandFactory func(context.Context, string) (workerexec.Command, error)
	ProjectReviewCommand func(context.Context, worker.TerminalCommand) (workerexec.Command, error)
	AgentTerminalID      string
	Started              chan<- error
	PollInterval         time.Duration
	Logger               *slog.Logger
	// Streams, when non-nil, holds a persistent duplex terminal stream per
	// open terminal for low-latency input/output. The polled transport stays
	// authoritative whenever a stream is absent or unhealthy.
	Streams StreamDialer

	// ChatRunner is the headless turn-based Chat controller. Nil means the
	// session cannot switch into the Chat interface.
	ChatRunner ChatRunner
	// ChatWorkspaceReady gates a committed Chat controller until checkout and
	// restore complete. Nil means no additional gate.
	ChatWorkspaceReady <-chan struct{}
	// RequestCheckout wakes workspace preparation after a failed checkout when
	// the user opens the session. A ready workspace treats it as a no-op.
	RequestCheckout func() error
	// InitialInterface is the committed launch interface ("tui" or "chat").
	InitialInterface string
	// AgentSessionID is the provider-native conversation identity shared by the
	// TUI and Chat controllers. It is the resume hint used on both sides.
	AgentSessionID string

	// PersistAgent keeps the interactive agent alive across worker restarts
	// (see persistentAgent). It takes effect only when dtach is installed.
	PersistAgent bool
	// StartupTask reports that the terminal agent's first start in this
	// worker runs the session's task from its command line. Until it starts,
	// a drain treats the terminal as busy (see startupTaskWaiting).
	StartupTask bool
	// AgentReady, when set, reports that a switch to the terminal started the
	// terminal agent (the agent.ready event a terminal session's own start
	// publishes).
	AgentReady func(context.Context)
	// AgentStartFailed, when set, reports that the terminal agent a switch to
	// the terminal committed (or the session's own, OwnAgentStartFailed)
	// could not start, after a few attempts, so the
	// session shows a startup error with a Retry (the workspace.checkout
	// request, see retryFailedAgentStart). With a nil error it reports that
	// the failure no longer stands: a retry started the agent, or a switch
	// away cancelled the start.
	AgentStartFailed func(context.Context, error)
	// agentStartRetryDelay overrides defaultAgentStartRetryDelay (tests).
	agentStartRetryDelay time.Duration

	persistOnce              sync.Once
	persist                  *persistentAgent
	mu                       sync.Mutex
	terminals                map[string]*terminalProcess
	activity                 activity
	iface                    InterfaceTransition
	notificationStreams      map[*terminalStream]struct{}
	holdAgentInput           bool
	workspaceReady           bool
	agentStarting            bool
	agentStarted             bool
	pendingAgentTerminalData [][]byte
	pendingAgentTerminalSize *worker.TerminalCommand
	tuiStartedAt             time.Time
	// lastTUIInputAt is the latest agent terminal input that could change
	// what the agent does (see mayChangeAgentTerminal).
	lastTUIInputAt time.Time
	// lastTUISubmitAt is the latest agent terminal input that could submit a
	// prompt (see mayStartTurn).
	lastTUISubmitAt   time.Time
	tuiHandoffClosing bool
	// pendingTUIStart is a switch to the terminal committed before the
	// workspace was ready; MarkWorkspaceReady starts it.
	pendingTUIStart *pendingTUIStart
	// startupTaskAbandoned records that the terminal agent's start failed, so
	// the StartupTask it carried no longer holds a drain.
	startupTaskAbandoned bool
	// workspaceStartupFailed records a failed checkout or Setup that waits for
	// the session's retry (SetWorkspaceStartupFailed).
	workspaceStartupFailed bool
	// ownAgentStartFailed records that the session's own terminal agent could
	// not start and waits for the session's retry (OwnAgentStartFailed).
	ownAgentStartFailed bool

	// The worker's own commands (FallBackToTUI) run on Run's serial loop,
	// like the control plane's interface commands, never beside them.
	loopOnce     sync.Once
	loopWake     chan struct{}
	loopMu       sync.Mutex
	loopCommands []loopCommand
}

// loopCommand is a worker-originated command waiting for Run's serial loop.
type loopCommand struct {
	run  func(context.Context) error
	done chan error
}

// ChatRunner executes the headless Chat controller kind for a session. Run
// blocks until ctx is canceled, mirroring the terminal supervisor's lifetime.
type ChatRunner interface {
	Run(ctx context.Context) error
}

// AgentCommandFactory rebuilds the native interactive command when a TUI is
// reopened. The provider conversation ID is learned after worker bootstrap, so
// reusing the bootstrap command would start a fresh TUI after ChatUI work.
type AgentCommandFactory func(context.Context, string, string, string) (workerexec.Command, error)

// modelDiscoveryCommand is the coding agent's command a model catalog is read
// with: the running agent's, or one built like it (binary, config home and
// credential) when none has started yet, as in a Chat session before its
// first turn.
func (s *Supervisor) modelDiscoveryCommand(ctx context.Context, nativeID, selectedModel, selectedEffort string) (workerexec.Command, error) {
	s.mu.Lock()
	command := s.AgentCommand
	s.mu.Unlock()
	if command.Path != "" || s.AgentCommandFactory == nil {
		// Only the builder's Cleanup belongs to this request.
		command.Cleanup = nil
		return command, nil
	}
	return s.AgentCommandFactory(ctx, nativeID, selectedModel, selectedEffort)
}

// chatActivity is implemented by the durable headless controller. Keeping it
// optional preserves the runner boundary for alternate worker implementations
// while allowing a real Chat turn to drain before a TUI handoff begins.
type chatActivity interface {
	Idle() bool
}

// chatInterrupter cancels the active headless turn without stopping the
// controller itself. A Chat -> TUI stop-now handoff needs this distinction:
// the coordinator first ends the running turn, then stops the runner only
// after its provider process has released the native conversation writer.
type chatInterrupter interface {
	Interrupt() bool
}

// HoldAgentInputUntilWorkspaceReady preserves user input and durable turns
// until the checkout has completed. Call this before Run.
func (s *Supervisor) HoldAgentInputUntilWorkspaceReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holdAgentInput = true
	s.workspaceReady = false
}

// MarkWorkspaceReady releases prompts collected while the agent was waiting for
// its checkout. If the agent PTY has not started yet, the prompts remain queued
// until StartAgent makes the terminal live.
//
// A switch to the terminal committed while the workspace was not ready starts
// its terminal agent now, on Run's serial loop.
func (s *Supervisor) MarkWorkspaceReady() {
	s.mu.Lock()
	s.workspaceReady = true
	pendingTUI := s.pendingTUIStart != nil
	s.mu.Unlock()
	s.flushReadyAgentTerminal()
	if pendingTUI {
		s.queueOnLoop(s.startPendingTUI)
	}
}

// StartStartupAgent starts a terminal session's own agent once its workspace
// is ready (StartAgent on a freshly ticketed agent terminal), unless an
// interface switch made while the workspace was being prepared took over: the
// session is in Chat now, or the switch back to the terminal starts the agent
// itself. It runs on Run's serial loop, so it never interleaves with an
// interface command, and reports whether it started the agent. A command it
// does not start is cleaned up.
func (s *Supervisor) StartStartupAgent(ctx context.Context, command workerexec.Command) (bool, error) {
	// Atomic: when ctx ends first, runOnLoop returns while the loop may still
	// run the command.
	var started atomic.Bool
	err := s.runOnLoop(ctx, func(ctx context.Context) error {
		s.mu.Lock()
		superseded := s.pendingTUIStart != nil || s.agentStarted || s.agentStarting
		s.mu.Unlock()
		if superseded || s.iface.Current() != InterfaceTUI {
			if command.Cleanup != nil {
				command.Cleanup()
			}
			return nil
		}
		terminal, err := s.Control.EnsureAgentTerminal(ctx)
		if err != nil {
			if command.Cleanup != nil {
				command.Cleanup()
			}
			return fmt.Errorf("initialize agent terminal: %w", err)
		}
		if err := s.StartAgent(ctx, command, terminal); err != nil {
			return fmt.Errorf("start interactive coding-agent terminal: %w", err)
		}
		started.Store(true)
		return nil
	})
	return started.Load(), err
}

// Interface is the committed interface the transport runs ("tui" or "chat").
func (s *Supervisor) Interface() string {
	return s.iface.Current()
}

// AbandonStartupTask records that the terminal agent could not start, so its
// StartupTask no longer holds a drain.
func (s *Supervisor) AbandonStartupTask() {
	s.mu.Lock()
	s.startupTaskAbandoned = true
	s.mu.Unlock()
}

// SetWorkspaceStartupFailed records that the workspace's checkout or Setup
// failed and waits for the session's retry (true), or that a retry is
// preparing it again (false).
func (s *Supervisor) SetWorkspaceStartupFailed(failed bool) {
	s.mu.Lock()
	s.workspaceStartupFailed = failed
	s.mu.Unlock()
}

// OwnAgentStartFailed reports that the session's own terminal agent (its
// first start in this worker, StartStartupAgent) could not start and waits for
// the session's retry, as AgentStartFailed reports a switch's start. Until the
// retry (ClearOwnAgentStartFailed) a drain to Chat is bounded as for a failed
// Setup (startupFailureWaiting), and a switch away from the terminal clears
// the failure (stopInterface). It runs on Run's serial loop, so it never
// interleaves with a switch: a session a switch already took off the terminal
// reports nothing.
func (s *Supervisor) OwnAgentStartFailed(ctx context.Context, err error) error {
	return s.runOnLoop(ctx, func(ctx context.Context) error {
		if s.iface.Current() != InterfaceTUI {
			return nil
		}
		s.mu.Lock()
		s.ownAgentStartFailed = true
		s.mu.Unlock()
		if s.AgentStartFailed != nil {
			s.AgentStartFailed(ctx, err)
		}
		return nil
	})
}

// ClearOwnAgentStartFailed records that the session's retry starts its own
// terminal agent again.
func (s *Supervisor) ClearOwnAgentStartFailed() {
	s.mu.Lock()
	s.ownAgentStartFailed = false
	s.mu.Unlock()
}

// startupFailureWaiting reports a startup that failed and waits for the
// session's retry with no terminal agent running: the workspace's checkout
// or Setup, or the terminal agent's start (startPendingTUI, or the session's
// own, OwnAgentStartFailed).
func (s *Supervisor) startupFailureWaiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agentStarted {
		return false
	}
	return (s.holdAgentInput && !s.workspaceReady && s.workspaceStartupFailed) ||
		(s.pendingTUIStart != nil && s.pendingTUIStart.failed) || s.ownAgentStartFailed
}

// startupTaskWaiting reports a terminal agent that has not started yet and
// runs the session's task once it does: a terminal session's own agent, or the
// one a switch to the terminal made while the workspace was being prepared.
// An own start that failed and waits for the session's retry still holds the
// task: the retry runs it.
func (s *Supervisor) startupTaskWaiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.StartupTask && !s.agentStarted && (!s.startupTaskAbandoned || s.ownAgentStartFailed) &&
		(s.pendingTUIStart != nil || s.InitialInterface != InterfaceChat)
}

type terminalProcess struct {
	cancel                context.CancelFunc
	pty                   *os.File
	cleanup               func()
	interfaceHandoffClose bool
	// persistent marks the interactive agent running under dtach: the process
	// here is only a client, and the agent outlives it.
	persistent bool
	// shellPID is the workspace shell's process id (0 for agent and reviewer
	// terminals); a process under it keeps the session from idling.
	shellPID int
	stream   atomic.Pointer[terminalStream]
	done     chan struct{}
	// outputID belongs to the terminal rather than a WebSocket connection. A
	// stream redial must continue its sequence so direct relay frames and the
	// durable replay log use the same cursor.
	outputID atomic.Int64
	// outputMu keeps output publication in cursor order across the stream and
	// its HTTP fallback, including resends of unacknowledged stream frames.
	outputMu sync.Mutex
	// sizeMu serializes PTY size changes so a repaint's nudge cannot restore a
	// size that a concurrent resize just replaced.
	sizeMu sync.Mutex
	// input tracks whether the interactive agent takes the turns the worker types
	// into it (agent terminals only; see agent_input.go).
	input agentInput
}

func (s *Supervisor) Run(ctx context.Context) error {
	if s.Control == nil {
		return errors.New("worker transport control is required")
	}
	if s.Workspace == "" {
		return errors.New("worker transport workspace is required")
	}
	if s.Shell == "" {
		s.Shell = "/bin/sh"
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	s.terminals = make(map[string]*terminalProcess)
	s.notificationStreams = make(map[*terminalStream]struct{})
	workspace, err := openWorkspace(s.Workspace)
	if err != nil {
		return err
	}
	workspace.compareBase = s.CompareBase
	defer workspace.Close()
	defer s.repositories.closeAll()
	defer s.closeAllTerminals()
	switch s.InitialInterface {
	case InterfaceChat:
		s.iface.current = InterfaceChat
	default:
		s.iface.current = InterfaceTUI
	}
	if s.iface.current == InterfaceTUI && s.AgentTerminalID != "" {
		err := s.openTerminal(ctx, worker.TerminalCommand{
			TerminalID: s.AgentTerminalID,
			Kind:       "agent",
		})
		if s.Started != nil {
			s.Started <- err
		}
		if err != nil {
			return err
		}
	} else if s.iface.current == InterfaceChat {
		// A worker may be replaced or restarted after the committed interface
		// changed to Chat. Starting the transport loop alone is not enough: the
		// headless controller owns the durable turn queue and must be restarted
		// too, otherwise ChatUI accepts a message that no worker will execute.
		err := s.startChat(ctx)
		if s.Started != nil {
			s.Started <- err
		}
		if err != nil {
			return err
		}
	} else if s.Started != nil {
		s.Started <- nil
	}

	for {
		if s.runLoopCommands(ctx) {
			continue
		}
		request, err := s.Control.ClaimTransport(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.Logger.Warn("claim worker transport request", "error", err)
		} else if request != nil {
			// Read-only workspace/review requests and browser fetches run off the
			// serial loop so a large-repo review (which reads every file and spawns
			// many git procs per call) or a burst of page resources cannot wedge
			// shell input and turn forwarding. Writes (workspace.write /
			// workspace.review.write) and terminal control stay serial, so the
			// review write-guard's read-check-write keeps its serialization against
			// other writes. See isConcurrentlyHandledKind.
			if isConcurrentlyHandledKind(request.Kind) {
				go s.handle(ctx, workspace, request)
				continue
			}
			s.handle(ctx, workspace, request)
			continue
		}
		handled, err := s.forwardTurn(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.Logger.Warn("forward agent message", "error", err)
		} else if handled {
			continue
		}
		// No work right now. Block until the control plane wakes us on a new
		// turn/transport enqueue (NOTIFY), or a short server-side timeout,
		// instead of busy-polling the claim routes. The claims above remain the
		// source of truth; WaitForWork is only an accelerant.
		if err := s.waitForWork(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.Logger.Warn("wait for worker work", "error", err)
			// An older control plane without the wait endpoint, or a transient
			// error: back off briefly so the loop never spins.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(workWaitFallback):
			}
		}
	}
}

func (s *Supervisor) loopWakeChannel() chan struct{} {
	s.loopOnce.Do(func() { s.loopWake = make(chan struct{}, 1) })
	return s.loopWake
}

// runOnLoop runs command on Run's serial loop, waking the loop from its wait
// for control plane work, and returns the command's result. It returns
// ctx's error if ctx ends first.
func (s *Supervisor) runOnLoop(ctx context.Context, command func(context.Context) error) error {
	done := s.queueOnLoop(command)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// queueOnLoop queues command for Run's serial loop, waking the loop from its
// wait for control plane work, without waiting for it. The returned channel
// receives the command's result.
func (s *Supervisor) queueOnLoop(command func(context.Context) error) <-chan error {
	queued := loopCommand{run: command, done: make(chan error, 1)}
	s.loopMu.Lock()
	s.loopCommands = append(s.loopCommands, queued)
	s.loopMu.Unlock()
	s.wakeLoop()
	return queued.done
}

// wakeLoop cuts Run's wait for control plane work short, so the loop runs
// its worker commands and tries to forward a queued turn again.
func (s *Supervisor) wakeLoop() {
	select {
	case s.loopWakeChannel() <- struct{}{}:
	default:
	}
}

// runLoopCommands runs the queued worker commands in order and reports
// whether there were any.
func (s *Supervisor) runLoopCommands(ctx context.Context) bool {
	s.loopMu.Lock()
	queued := s.loopCommands
	s.loopCommands = nil
	s.loopMu.Unlock()
	for _, command := range queued {
		command.done <- command.run(ctx)
	}
	return len(queued) > 0
}

// waitForWork is Control.WaitForWork, cut short when a worker command is
// queued for the loop (a nil error then).
func (s *Supervisor) waitForWork(ctx context.Context) error {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.loopWakeChannel():
			cancel()
		case <-waitCtx.Done():
		}
	}()
	err := s.Control.WaitForWork(waitCtx)
	if err != nil && waitCtx.Err() != nil && ctx.Err() == nil {
		return nil
	}
	return err
}

// ConfigureAgent reserves an agent terminal before its PTY may be started. A
// browser can attach during checkout, so keeping this identity lets the worker
// buffer its early input and latest viewport instead of acknowledging requests
// that no process can handle yet.
func (s *Supervisor) ConfigureAgent(command workerexec.Command, terminalID string) error {
	if terminalID == "" {
		return errors.New("agent terminal id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.AgentTerminalID != "" || s.agentStarting || s.agentStarted {
		return errors.New("interactive agent terminal is already configured")
	}
	s.AgentCommand = command
	s.AgentTerminalID = terminalID
	s.agentStarting = true
	return nil
}

// DiscardConfiguredAgent releases the command cleanup when checkout fails
// before the reserved agent terminal could start.
func (s *Supervisor) DiscardConfiguredAgent(terminalID string) {
	s.mu.Lock()
	if !s.agentStarting || s.agentStarted || s.AgentTerminalID != terminalID {
		s.mu.Unlock()
		return
	}
	cleanup := s.AgentCommand.Cleanup
	s.AgentCommand = workerexec.Command{}
	s.AgentTerminalID = ""
	s.agentStarting = false
	s.pendingAgentTerminalData = nil
	s.pendingAgentTerminalSize = nil
	s.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

// StartAgent adds the coding-agent PTY after the workspace transport is already
// serving. ConfigureAgent may have reserved its identity while checkout ran;
// otherwise this method keeps the original one-step setup behavior.
func (s *Supervisor) StartAgent(ctx context.Context, command workerexec.Command, terminal worker.AgentTerminalResponse) error {
	terminalID := terminal.TerminalID
	if terminalID == "" {
		return errors.New("agent terminal id is required")
	}
	s.mu.Lock()
	if s.AgentTerminalID == "" {
		s.AgentCommand = command
		s.AgentTerminalID = terminalID
		s.agentStarting = true
	} else if s.AgentTerminalID != terminalID || !s.agentStarting || s.agentStarted {
		s.mu.Unlock()
		return errors.New("interactive agent terminal is already configured")
	}
	s.mu.Unlock()
	if err := s.openTerminal(ctx, worker.TerminalCommand{TerminalID: terminalID, NextOutputSequence: terminal.NextOutputSequence, Kind: "agent"}); err != nil {
		s.mu.Lock()
		s.AgentCommand = workerexec.Command{}
		s.AgentTerminalID = ""
		s.agentStarting = false
		s.pendingAgentTerminalData = nil
		s.pendingAgentTerminalSize = nil
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	s.agentStarting = false
	s.agentStarted = true
	s.mu.Unlock()
	s.flushReadyAgentTerminal()
	return nil
}

// SetReviewCommand configures the fresh coding-agent command used by automated
// PR review terminals. Review processes share the checkout but must not resume
// the interactive session's native conversation.
func (s *Supervisor) SetReviewCommand(command workerexec.Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ReviewCommand = command
}

// SetReviewCommandFactory configures provider-specific fresh reviewer commands.
func (s *Supervisor) SetReviewCommandFactory(factory func(context.Context, string) (workerexec.Command, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ReviewCommandFactory = factory
}

func (s *Supervisor) forwardTurn(ctx context.Context) (bool, error) {
	// The Chat controller owns the durable turn queue while ChatUI is active.
	// Do not claim a turn here: doing so races the headless runner and either
	// drops the turn or fails it before the Chat controller can execute it.
	if s.iface.Current() == InterfaceChat {
		return false, nil
	}
	// Do not claim a queued user turn until the agent PTY is actually live. The
	// workspace transport starts first, so claiming here would otherwise mark
	// the initial task failed while the coding agent is still booting.
	s.mu.Lock()
	agentTerminalID := s.AgentTerminalID
	workspaceReady := !s.holdAgentInput || s.workspaceReady
	agentStarted := s.agentStarted
	s.mu.Unlock()
	if agentTerminalID == "" || !agentStarted || !workspaceReady {
		return false, nil
	}
	// A live PTY is not yet an agent taking input: a prompt typed while its
	// TUI boots arrives in one read with its Enter, which the TUI takes as a
	// paste and leaves unsubmitted. Leave the turn queued until it is ready.
	if !s.agentInputReady(agentTerminalID) {
		return false, nil
	}
	turn, err := s.Control.ClaimTurn(ctx)
	if err != nil || turn == nil {
		return false, err
	}
	if turn.CancelRequested {
		return true, s.Control.CompleteTurn(ctx, turn.ID, turn.Attempt, true)
	}
	if err := s.writeAgentPrompt(agentTerminalID, worker.EncodeTerminalInput(turn.Prompt)); err != nil {
		if failErr := s.Control.FailTurn(
			ctx, turn.ID, turn.Attempt, err.Error(),
		); failErr != nil {
			return true, errors.Join(err, failErr)
		}
		return true, err
	}
	return true, s.Control.CompleteTurn(ctx, turn.ID, turn.Attempt, false)
}

// isConcurrentlyHandledKind reports whether a transport request is a read-only
// workspace/review request or a browser fetch that may run off the serial loop.
// Offloading these keeps an expensive review (ReviewSummary reads every tracked
// file and spawns hundreds of git procs) from blocking terminal input and turn
// forwarding. It is safe: the workspace struct is immutable after construction,
// file writes are atomic (write-temp + rename) so a concurrent read never sees a
// torn file, and every mutating kind (workspace.write, workspace.review.write)
// plus terminal control stays on the serial loop, so writes remain serialized
// against each other and the review write-guard keeps its TOCTOU-free property.
func isConcurrentlyHandledKind(kind string) bool {
	switch kind {
	case "browser.fetch",
		"chat.models",
		"workspace.list", "workspace.read", "workspace.diff", "workspace.diff-file", "workspace.repositories",
		"workspace.review.summary", "workspace.review.tree", "workspace.review.search",
		"workspace.review.file", "workspace.review.diffs", "workspace.review.revision":
		return true
	default:
		return false
	}
}

func (s *Supervisor) handle(
	ctx context.Context,
	mainWorkspace *workspace,
	request *worker.TransportRequest,
) {
	var response any
	var err error
	workspace, repositoryName := mainWorkspace, ""
	if strings.HasPrefix(request.Kind, "workspace.") && request.Kind != "workspace.checkout" {
		workspace, repositoryName, err = s.workspaceFor(mainWorkspace, request.Payload)
		if err != nil {
			s.respondTransport(ctx, request, nil, err)
			return
		}
	}
	switch request.Kind {
	case "workspace.repositories":
		response = worker.WorkspaceRepositoriesResponse{Repositories: s.workspaceRepositories()}
	case "workspace.review.summary":
		var summary worker.WorkspaceReviewResponse
		summary, err = workspace.ReviewSummary(ctx)
		if err == nil && repositoryName == "" {
			summary.Repositories = s.workspaceRepositories()
		}
		response = summary
	case "workspace.checkout":
		// A session's Retry: a terminal agent start that failed after the
		// workspace became ready tries again; otherwise the workspace's
		// checkout and Setup do.
		if s.retryFailedAgentStart() {
			response = map[string]bool{"requested": true}
		} else if s.RequestCheckout == nil {
			err = errors.New("workspace checkout is unavailable")
		} else {
			err = s.RequestCheckout()
			response = map[string]bool{"requested": err == nil}
		}
	case "workspace.list":
		var input worker.WorkspaceListRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = workspace.List(input)
		}
	case "workspace.read":
		var input worker.WorkspaceReadRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = workspace.Read(input)
		}
	case "workspace.diff-file":
		var input worker.WorkspaceDiffFileRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			s.Logger.Info("workspace diff-file request started", "request_id", request.ID, "path", input.Path)
			response, err = workspace.DiffFile(ctx, input)
			if err != nil {
				failureCode, _ := transportError(err)
				s.Logger.Warn("workspace diff-file request failed", "request_id", request.ID, "path", input.Path, "failure_code", failureCode)
			} else {
				file := response.(worker.WorkspaceDiffFile)
				s.Logger.Info("workspace diff-file request completed", "request_id", request.ID, "path", file.Path, "size", file.Size, "binary", file.Binary, "deleted", file.Deleted, "diff_truncated", file.DiffTruncated)
			}
		}
	case "workspace.write":
		var input worker.WorkspaceWriteRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = workspace.Write(input)
		}
	case "workspace.diff":
		response, err = workspace.Diff(ctx)
	case "harness.inspect":
		var input worker.HarnessInspectRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = inspectHarnesses(ctx, input)
		}
	case "harness.install":
		var input worker.HarnessInstallRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = installHarness(ctx, input)
		}
	case "workspace.review.tree":
		var input worker.WorkspaceReviewTreeRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = workspace.ReviewTree(ctx, input)
		}
	case "workspace.review.search":
		var input worker.WorkspaceReviewSearchRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = workspace.ReviewSearch(ctx, input)
		}
	case "workspace.review.file":
		var input worker.WorkspaceReviewFileRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = workspace.ReviewFile(ctx, input)
		}
	case "workspace.review.diffs":
		var input worker.WorkspaceReviewDiffsRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			var diffs worker.WorkspaceReviewDiffsResponse
			diffs, err = workspace.ReviewDiffs(ctx, input)
			for index := range diffs.Groups {
				diffs.Groups[index].Repository = repositoryName
			}
			response = diffs
		}
	case "workspace.review.revision":
		var input worker.WorkspaceReviewRevisionRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = workspace.ReviewRevision(ctx, input)
		}
	case "workspace.review.write":
		var input worker.WorkspaceReviewWriteRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = workspace.ReviewWrite(ctx, input)
		}
	case "browser.fetch":
		var input worker.BrowserFetchRequest
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = fetchBrowser(ctx, input)
		}
	case "chat.models":
		if s.Harness != "codex" && s.Harness != "claude-code" {
			err = errors.New("model catalog is unavailable for this provider")
		} else if s.Harness == "claude-code" {
			nativeID := s.nativeConversationID(ctx, interfacePayload{})
			s.mu.Lock()
			selectedModel, selectedEffort, selectionAt := s.SelectedModel, s.SelectedEffort, s.SelectionAt
			s.mu.Unlock()
			var command workerexec.Command
			command, err = s.modelDiscoveryCommand(ctx, nativeID, selectedModel, selectedEffort)
			if err == nil && command.Cleanup != nil {
				defer command.Cleanup()
			}
			var models []worker.ChatModel
			var nativeModel, nativeEffort string
			var catalog worker.ChatModelsResponse
			if err == nil {
				catalog, err = workerexec.DiscoverClaudeModels(ctx, command, nativeID, selectedModel)
				models, nativeModel, nativeEffort = catalog.Models, catalog.Model, catalog.ReasoningEffort
			}
			if err == nil {
				model, effort, settingsErr := workerexec.ClaudeConversationSettingsAfter(s.DataDir, nativeID, selectionAt)
				if settingsErr != nil {
					err = settingsErr
				} else {
					if claudeCatalogHasModel(models, model) {
						nativeModel = model
						if effort != "" {
							nativeEffort = effort
						}
					} else if claudeCatalogHasModel(models, selectedModel) {
						nativeModel, nativeEffort = selectedModel, selectedEffort
					}
					response = worker.ChatModelsResponse{Models: models, Model: nativeModel, ReasoningEffort: nativeEffort, Modes: catalog.Modes}
				}
			}
		} else {
			nativeID := s.nativeConversationID(ctx, interfacePayload{})
			s.mu.Lock()
			selectedModel, selectedEffort, selectionAt := s.SelectedModel, s.SelectedEffort, s.SelectionAt
			s.mu.Unlock()
			var models []worker.ChatModel
			var command workerexec.Command
			command, err = s.modelDiscoveryCommand(ctx, nativeID, selectedModel, selectedEffort)
			if err == nil && command.Cleanup != nil {
				defer command.Cleanup()
			}
			if err == nil {
				models, err = workerexec.DiscoverCodexModels(ctx, command)
			}
			if err == nil {
				model, effort, settingsErr := workerexec.CodexConversationSettingsAfter(s.DataDir, nativeID, selectionAt)
				if settingsErr != nil {
					err = settingsErr
				} else {
					if model == "" {
						model, effort = selectedModel, selectedEffort
					}
					response = worker.ChatModelsResponse{Models: models, Model: model, ReasoningEffort: effort}
				}
			}
		}
	case "chat.steer":
		var input struct {
			TurnID string `json:"turnId"`
			Text   string `json:"text"`
		}
		err = decodePayload(request.Payload, &input)
		if err == nil {
			steerer, ok := s.ChatRunner.(interface {
				Steer(context.Context, string, string) error
			})
			if !ok || s.iface.Current() != InterfaceChat {
				err = errors.New("chat steering is unavailable")
			} else {
				steerCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				err = steerer.Steer(steerCtx, input.TurnID, input.Text)
				cancel()
			}
			response = map[string]bool{"injected": err == nil}
		}
	case "terminal.open":
		var input worker.TerminalCommand
		err = decodePayload(request.Payload, &input)
		if err == nil {
			err = s.openTerminal(ctx, input)
			response = map[string]bool{"open": err == nil}
		}
	case "terminal.input":
		var input worker.TerminalCommand
		err = decodePayload(request.Payload, &input)
		if err == nil {
			if input.TerminalID == s.AgentTerminalID {
				err = s.writeAgentPrompt(input.TerminalID, input.Data)
			} else if input.Review {
				err = s.writeAgentPrompt(input.TerminalID, input.Data)
			} else {
				err = s.writeTerminal(input)
			}
			response = map[string]bool{"accepted": err == nil}
		}
	case "terminal.resize":
		var input worker.TerminalCommand
		err = decodePayload(request.Payload, &input)
		if err == nil && input.Repaint && input.Columns == 0 && input.Rows == 0 {
			err = s.repaintTerminal(input.TerminalID)
			response = map[string]bool{"repainted": err == nil}
		} else if err == nil {
			err = s.resizeTerminal(input)
			response = map[string]bool{"resized": err == nil}
		}
	case "terminal.close":
		var input worker.TerminalCommand
		err = decodePayload(request.Payload, &input)
		if err == nil {
			s.closeTerminal(input.TerminalID)
			response = map[string]bool{"closed": true}
		}
	case "interface.inspect", "interface.interrupt", "interface.stop",
		"interface.native-id", "interface.start", "interface.ready":
		var input interfacePayload
		err = decodePayload(request.Payload, &input)
		if err == nil {
			response, err = s.handleInterface(ctx, input, request.Kind)
		}
	default:
		err = errors.New("unsupported worker transport request")
	}
	s.respondTransport(ctx, request, response, err)
}

// respondTransport completes a transport request with its response, or fails
// it with err's transport error code.
func (s *Supervisor) respondTransport(ctx context.Context, request *worker.TransportRequest, response any, err error) {
	if err == nil {
		if completeErr := s.Control.CompleteTransport(
			ctx, request.ID, request.Attempt, response,
		); completeErr != nil {
			s.Logger.Warn("complete worker transport request", "error", completeErr, "kind", request.Kind)
		}
		return
	}
	code, message := transportError(err)
	if failErr := s.Control.FailTransport(
		ctx, request.ID, request.Attempt, code, message,
	); failErr != nil {
		s.Logger.Warn("fail worker transport request", "error", failErr, "kind", request.Kind)
	}
}

func (s *Supervisor) openTerminal(ctx context.Context, input worker.TerminalCommand) error {
	if input.TerminalID == "" ||
		(input.Kind != "workspace" && input.Kind != "agent" && input.Kind != "reviewer") {
		return errors.New("invalid terminal open request")
	}
	s.mu.Lock()
	if _, exists := s.terminals[input.TerminalID]; exists {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	processCtx, cancel := context.WithCancel(ctx)
	command, cleanup, err := s.terminalCommand(processCtx, input)
	if err != nil {
		cancel()
		return err
	}
	// Before the PTY starts, so only what this agent writes is new.
	var turnEnds *transcriptTail
	// A worker that re-attaches to an agent still running under dtach sees no
	// new start-up output: that agent took input long ago.
	reattached := false
	if s.persistsAgent(input) {
		_, reattached = s.persistentAgent().running()
	}
	if input.Kind == "agent" && !input.Review {
		switch s.Harness {
		case "codex":
			if home := environmentValue(command.Env, "CODEX_HOME"); home != "" {
				s.mu.Lock()
				conversationID := s.AgentCommand.ConversationID
				s.mu.Unlock()
				turnEnds = newCodexRolloutTail(home, conversationID, time.Now())
			}
		case "claude-code":
			if configDir := environmentValue(command.Env, "CLAUDE_CONFIG_DIR"); configDir != "" {
				s.mu.Lock()
				conversationID := claudeTerminalConversationID(s.AgentCommand)
				s.mu.Unlock()
				turnEnds = newClaudeTranscriptTail(configDir, conversationID, s.DataDir, time.Now())
			}
		}
	}
	s.mu.Lock()
	if _, exists := s.terminals[input.TerminalID]; exists {
		s.mu.Unlock()
		cancel()
		cleanup()
		return nil
	}
	columns, rows := input.Columns, input.Rows
	if columns == 0 {
		columns = fallbackTerminalColumns
	}
	if rows == 0 {
		rows = fallbackTerminalRows
	}
	terminalPTY, err := pty.StartWithSize(command, &pty.Winsize{
		Cols: columns,
		Rows: rows,
	})
	if err != nil {
		cancel()
		cleanup()
		s.mu.Unlock()
		return err
	}
	terminal := &terminalProcess{
		cancel:     cancel,
		pty:        terminalPTY,
		cleanup:    cleanup,
		done:       make(chan struct{}),
		persistent: s.persistsAgent(input),
	}
	if input.Kind == "workspace" && command.Process != nil {
		terminal.shellPID = command.Process.Pid
	}
	if input.NextOutputSequence > 1 {
		terminal.outputID.Store(input.NextOutputSequence - 1)
	}
	s.terminals[input.TerminalID] = terminal
	if input.Kind == "agent" && !input.Review {
		terminal.input.start(s.Harness, reattached)
	}
	if input.Kind == "agent" {
		s.tuiStartedAt = time.Now()
		s.lastTUIInputAt = time.Time{}
		s.lastTUISubmitAt = time.Time{}
		s.tuiHandoffClosing = false
	}
	s.mu.Unlock()

	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		s.copyTerminalOutput(processCtx, input.TerminalID, terminal, input.Kind == "agent" && input.NextOutputSequence > 1)
	}()
	if s.Streams != nil {
		go s.runTerminalStream(processCtx, input.TerminalID, terminal)
	}
	if turnEnds != nil {
		go s.watchTerminalTurnEnds(processCtx, s.Harness, turnEnds, environmentValue(command.Env, "AO_CHECKPOINT_SOCKET"), terminalTurnPollInterval)
	}
	go func() {
		_ = command.Wait()
		// Signal process exit before removing the terminal or publishing its exit.
		// Interface handoff uses this channel to fence the next controller from
		// starting while the TUI may still own the provider's thread writer. If
		// PublishTerminalExit delays this signal, a concurrent stop can observe an
		// already-removed terminal and incorrectly conclude that shutdown finished.
		close(terminal.done)
		if input.Kind == "reviewer" {
			// A reviewer can reject its launch arguments immediately. Preserve
			// that diagnostic before canceling the output upload or closing the
			// PTY; otherwise the UI only receives "reviewer terminal finished".
			select {
			case <-outputDone:
			case <-processCtx.Done():
			case <-time.After(3 * time.Second):
			}
		}
		s.mu.Lock()
		current := s.terminals[input.TerminalID]
		if current == terminal {
			delete(s.terminals, input.TerminalID)
		}
		handoff := terminal.interfaceHandoffClose
		s.mu.Unlock()
		if current != nil {
			_ = current.pty.Close()
			current.cancel()
			current.cleanup()
		}
		exitCode := command.ProcessState.ExitCode()
		if terminal.persistent {
			if _, alive := s.persistentAgent().running(); alive {
				// Only this worker's client ended (the worker is shutting down);
				// the agent keeps running for the next worker to attach to.
				return
			}
			if code, ok := s.persistentAgent().exitCode(); ok {
				exitCode = code
			}
		}
		exitCtx, exitCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer exitCancel()
		if err := s.Control.PublishTerminalExit(
			exitCtx,
			input.TerminalID,
			exitCode,
			handoff,
		); err != nil && exitCtx.Err() == nil {
			s.Logger.Warn("publish terminal exit", "error", err, "terminal_id", input.TerminalID)
		}
	}()
	return nil
}

func (s *Supervisor) terminalCommand(
	ctx context.Context,
	input worker.TerminalCommand,
) (*exec.Cmd, func(), error) {
	if input.Kind == "agent" || input.Kind == "reviewer" {
		// openTerminal holds s.mu while this command is created.
		commandConfig := s.AgentCommand
		if input.Kind == "reviewer" {
			if s.ProjectReviewCommand == nil || input.Reviewer == nil || input.ReviewRunID == "" {
				return nil, func() {}, errors.New("reviewer command is unavailable")
			}
			var err error
			commandConfig, err = s.ProjectReviewCommand(ctx, input)
			if err != nil {
				return nil, func() {}, err
			}
		} else if input.Review {
			if s.ReviewCommandFactory != nil {
				var err error
				commandConfig, err = s.ReviewCommandFactory(ctx, input.Harness)
				if err != nil {
					return nil, func() {}, err
				}
			} else {
				commandConfig = s.ReviewCommand
			}
		}
		if commandConfig.Path == "" {
			return nil, func() {}, errors.New("interactive agent command is unavailable")
		}
		args := append([]string(nil), commandConfig.Args...)
		if input.Review && len(input.Data) > 0 {
			// Codex accepts an initial positional prompt. Supplying it at process
			// startup avoids racing its interactive TUI initialization, which can
			// drop a prompt typed immediately after the PTY opens.
			args = append(args, string(input.Data))
		}
		command := exec.CommandContext(ctx, commandConfig.Path, args...)
		environment := commandConfig.Env
		if s.persistsAgent(input) {
			persist := s.persistentAgent()
			command = persist.command(ctx, commandConfig.Path, args)
			environment = make(map[string]string, len(commandConfig.Env)+2)
			for key, value := range commandConfig.Env {
				environment[key] = value
			}
			for key, value := range persist.environment() {
				environment[key] = value
			}
		}
		command.Dir = commandConfig.Dir
		command.Env = terminalEnvironment(environment)
		cleanup := commandConfig.Cleanup
		if cleanup == nil {
			cleanup = func() {}
		}
		return command, cleanup, nil
	}
	command := exec.CommandContext(ctx, s.Shell)
	command.Dir = s.Workspace
	command.Env = terminalEnvironment(nil)
	return command, func() {}, nil
}

func terminalEnvironment(extra map[string]string) []string {
	environment := append([]string{}, os.Environ()...)
	environment = append(environment, "TERM=xterm-256color", "COLORTERM=truecolor")
	for key, value := range extra {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func (s *Supervisor) copyTerminalOutput(
	ctx context.Context,
	terminalID string,
	terminal *terminalProcess,
	clearPreviousDisplay bool,
) {
	buffer := make([]byte, 16<<10)
	var startup bracketedPasteScan
	for {
		count, err := terminal.pty.Read(buffer)
		if count > 0 {
			s.activity.noteOutput(time.Now())
			if terminal.input.waiting() && startup.enabled(buffer[:count]) {
				s.agentInputBecameReady(terminal)
			}
			data := append([]byte(nil), buffer[:count]...)
			if clearPreviousDisplay {
				// A handoff resumes the conversation in a new PTY but reuses its
				// durable output stream. Clear the old screen and scrollback at
				// that process boundary, both live and when replayed on reconnect.
				// Prefix actual output so the reset never reveals an empty screen.
				data = append([]byte("\x1b[3J\x1b[H\x1b[2J"), data...)
				clearPreviousDisplay = false
			}
			id := terminal.outputID.Add(1)
			s.publishTerminalOutput(ctx, terminalID, terminal, id, data)
		}
		if err != nil {
			return
		}
	}
}

// promptEnterDelay mirrors the desktop runtimes' paste-then-Enter pause (tmux
// defaultEnterDelay, conpty ptyInputEnterDelay): a harness TUI that receives
// message text and the trailing carriage return in one write treats the whole
// burst as a paste and leaves the prompt unsubmitted (issue #2342). Splitting
// the Enter off and pausing makes it a distinct submit keypress.
const promptEnterDelay = 300 * time.Millisecond

// writeAgentPrompt delivers an injected message to the agent terminal: body
// first, a beat, then the submitting carriage return. Single keystrokes and
// data without a trailing return pass through unchanged.
func (s *Supervisor) writeAgentPrompt(terminalID string, data []byte) error {
	if len(data) < 2 || data[len(data)-1] != '\r' {
		return s.writeTerminal(worker.TerminalCommand{TerminalID: terminalID, Data: data})
	}
	if err := s.writeTerminal(worker.TerminalCommand{
		TerminalID: terminalID, Data: data[:len(data)-1],
	}); err != nil {
		return err
	}
	time.Sleep(promptEnterDelay)
	return s.writeTerminal(worker.TerminalCommand{
		TerminalID: terminalID, Data: []byte("\r"),
	})
}

func (s *Supervisor) writeTerminal(input worker.TerminalCommand) error {
	if input.TerminalID == "" || len(input.Data) == 0 || len(input.Data) > 16<<10 {
		return errors.New("invalid terminal input request")
	}
	s.mu.Lock()
	if input.TerminalID == s.AgentTerminalID {
		if s.tuiHandoffClosing {
			s.mu.Unlock()
			return errors.New("agent terminal is switching interfaces")
		}
		s.noteAgentTerminalInput(input.Data)
	}
	if s.agentStarting && input.TerminalID == s.AgentTerminalID {
		s.pendingAgentTerminalData = append(s.pendingAgentTerminalData, append([]byte(nil), input.Data...))
		s.mu.Unlock()
		return nil
	}
	terminal := s.terminals[input.TerminalID]
	s.mu.Unlock()
	if terminal == nil {
		return errors.New("terminal is not open")
	}
	_, err := terminal.pty.Write(input.Data)
	return err
}

func (s *Supervisor) resizeTerminal(input worker.TerminalCommand) error {
	if input.TerminalID == "" || input.Columns == 0 || input.Rows == 0 {
		return errors.New("invalid terminal resize request")
	}
	s.mu.Lock()
	if s.agentStarting && input.TerminalID == s.AgentTerminalID {
		pending := input
		s.pendingAgentTerminalSize = &pending
		s.mu.Unlock()
		return nil
	}
	terminal := s.terminals[input.TerminalID]
	s.mu.Unlock()
	if terminal == nil {
		return errors.New("terminal is not open")
	}
	terminal.sizeMu.Lock()
	defer terminal.sizeMu.Unlock()
	return pty.Setsize(terminal.pty, &pty.Winsize{
		Cols: input.Columns,
		Rows: input.Rows,
	})
}

// terminalRepaintNudge is how long a repaint holds the off-by-one width
// before restoring the real one, so a program that reads the size some time
// after the signal sees a change rather than a size it may treat as unchanged.
const terminalRepaintNudge = 50 * time.Millisecond

// repaintTerminal makes the terminal's program redraw its screen, for a viewer
// whose replay skipped output. A program redraws on SIGWINCH, but neither the
// kernel nor dtach signals a resize to the size the PTY already has, so the
// width steps one column away and back. A full-screen program repaints; a
// line-mode shell at most redraws its prompt.
func (s *Supervisor) repaintTerminal(terminalID string) error {
	if terminalID == "" {
		return errors.New("invalid terminal repaint request")
	}
	s.mu.Lock()
	if s.agentStarting && terminalID == s.AgentTerminalID {
		// The agent has not drawn anything yet; its first frame is complete.
		s.mu.Unlock()
		return nil
	}
	terminal := s.terminals[terminalID]
	s.mu.Unlock()
	if terminal == nil {
		return errors.New("terminal is not open")
	}
	terminal.sizeMu.Lock()
	defer terminal.sizeMu.Unlock()
	size, err := pty.GetsizeFull(terminal.pty)
	if err != nil {
		return err
	}
	nudged := *size
	if nudged.Cols > 1 {
		nudged.Cols--
	} else {
		nudged.Cols++
	}
	if err := pty.Setsize(terminal.pty, &nudged); err != nil {
		return err
	}
	time.Sleep(terminalRepaintNudge)
	return pty.Setsize(terminal.pty, size)
}

// flushReadyAgentTerminal delivers the input and viewport collected while the
// agent terminal was reserved but its checkout/PTY was not ready. Keep only the
// newest size: intermediate resizes are stale by definition and sending them
// would needlessly redraw the TUI before its first prompt.
func (s *Supervisor) flushReadyAgentTerminal() {
	s.mu.Lock()
	if !s.workspaceReady || !s.agentStarted {
		s.mu.Unlock()
		return
	}
	terminal := s.terminals[s.AgentTerminalID]
	if terminal == nil {
		s.mu.Unlock()
		return
	}
	pendingSize := s.pendingAgentTerminalSize
	pendingData := s.pendingAgentTerminalData
	s.pendingAgentTerminalSize = nil
	s.pendingAgentTerminalData = nil
	s.mu.Unlock()
	if pendingSize != nil {
		if err := pty.Setsize(terminal.pty, &pty.Winsize{
			Cols: pendingSize.Columns,
			Rows: pendingSize.Rows,
		}); err != nil {
			s.Logger.Warn("flush queued agent terminal resize", "error", err)
		}
	}
	for _, data := range pendingData {
		if _, err := terminal.pty.Write(data); err != nil {
			s.Logger.Warn("flush queued agent terminal input", "error", err)
			return
		}
	}
}

func (s *Supervisor) closeTerminal(id string) {
	s.closeTerminalWithReason(id, false)
}

// closeTerminalForInterfaceHandoff closes the source TUI without reporting the
// whole Cloud session as exited. The Chat controller takes ownership next.
func (s *Supervisor) closeTerminalForInterfaceHandoff(ctx context.Context, id string) error {
	terminal := s.detachTerminal(id, true)
	if terminal == nil {
		return nil
	}
	// Ending the dtach client would leave the agent running and still owning
	// the provider's thread. End the agent itself first; the wait below then
	// covers its exit as before.
	s.stopPersistentTerminal(terminal)
	_ = terminal.pty.Close()
	terminal.cancel()
	terminal.cleanup()
	// Codex serializes thread writers. Do not acknowledge the source stop until
	// the interactive process has actually exited; otherwise the Chat runner can
	// resume the same thread while the TUI still owns its writer.
	select {
	case <-terminal.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Supervisor) closeTerminalWithReason(id string, interfaceHandoff bool) {
	terminal := s.detachTerminal(id, interfaceHandoff)
	if terminal != nil {
		// Closing the agent terminal means ending the agent, not just this
		// client of it.
		s.stopPersistentTerminal(terminal)
		_ = terminal.pty.Close()
		terminal.cancel()
		terminal.cleanup()
	}
}

func (s *Supervisor) detachTerminal(id string, interfaceHandoff bool) *terminalProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	terminal := s.terminals[id]
	delete(s.terminals, id)
	if terminal != nil && interfaceHandoff {
		terminal.interfaceHandoffClose = true
	}
	return terminal
}

func (s *Supervisor) closeAllTerminals() {
	s.mu.Lock()
	terminals := s.terminals
	s.terminals = make(map[string]*terminalProcess)
	s.mu.Unlock()
	for _, terminal := range terminals {
		_ = terminal.pty.Close()
		terminal.cancel()
		// The worker is shutting down, not ending the agent: a persisted agent
		// keeps running and still needs its launch files (the next worker
		// rewrites them before attaching).
		if !terminal.persistent {
			terminal.cleanup()
		}
	}
}

// persistentAgent returns the dtach wrapper when agent persistence is on and
// available, or nil.
func (s *Supervisor) persistentAgent() *persistentAgent {
	s.persistOnce.Do(func() {
		if s.PersistAgent {
			s.persist = newPersistentAgent(s.DataDir)
		}
	})
	return s.persist
}

// PersistsAgent reports whether this worker keeps the interactive agent
// running under dtach, so the agent can outlive the worker (and a later worker
// adopts it, along with the notifications it queued meanwhile).
func (s *Supervisor) PersistsAgent() bool {
	return s.persistentAgent() != nil
}

// persistsAgent reports whether a terminal launch runs the interactive agent
// under dtach. Review and reviewer terminals are always plain processes.
func (s *Supervisor) persistsAgent(input worker.TerminalCommand) bool {
	return input.Kind == "agent" && !input.Review && s.persistentAgent() != nil
}

// persistentAgentStopGrace bounds how long a deliberate stop waits for the
// agent to exit on SIGTERM before killing its process group.
const persistentAgentStopGrace = 10 * time.Second

func (s *Supervisor) stopPersistentTerminal(terminal *terminalProcess) {
	if !terminal.persistent {
		return
	}
	if err := s.persistentAgent().stop(persistentAgentStopGrace); err != nil && s.Logger != nil {
		s.Logger.Warn("stop persistent agent", "error", err)
	}
}

// StopPersistentAgent ends an agent left running by a previous worker. A
// worker that starts in the Chat interface calls it before anything else, so a
// TUI agent that survived an interrupted handoff cannot keep owning the
// conversation the Chat runner is about to resume.
func (s *Supervisor) StopPersistentAgent() {
	if persist := s.persistentAgent(); persist != nil {
		if err := persist.stop(persistentAgentStopGrace); err != nil && s.Logger != nil {
			s.Logger.Warn("stop persistent agent", "error", err)
		}
	}
}

func decodePayload(payload any, target any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func claudeCatalogHasModel(models []worker.ChatModel, id string) bool {
	if id == "" {
		return false
	}
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func transportError(err error) (string, string) {
	switch {
	case errors.Is(err, ErrWorkspaceSnapshotStale):
		return "WORKSPACE_SNAPSHOT_STALE", "The workspace changed while it was being reviewed."
	case errors.Is(err, ErrWorkspaceFingerprintStale):
		return "WORKSPACE_FILE_STALE", "The file changed after it was opened."
	case errors.Is(err, ErrWorkspaceCommitNotFound):
		return "WORKSPACE_COMMIT_NOT_FOUND", "The requested commit is outside the workspace review range."
	case errors.Is(err, errUnknownRepository):
		return "WORKSPACE_REPOSITORY_NOT_FOUND", "The requested repository is not checked out in this session."
	case errors.Is(err, errUnsafePath):
		return "INVALID_WORKSPACE_PATH", "The requested path is outside the workspace."
	case errors.Is(err, os.ErrNotExist):
		return "WORKSPACE_NOT_FOUND", "The requested workspace path does not exist."
	case errors.Is(err, os.ErrPermission):
		return "WORKSPACE_PERMISSION_DENIED", "The worker denied access to the workspace path."
	case errors.Is(err, workerexec.ErrChatAdapterInstalling):
		return "CHAT_ADAPTER_INSTALLING", "Setting up Chat UI on this workspace. Try again in a minute."
	case errors.Is(err, workerexec.ErrChatAdapterUnavailable):
		return "CHAT_ADAPTER_UNAVAILABLE", "Chat UI for Claude needs the Claude ACP adapter (@agentclientprotocol/claude-agent-acp) on this workspace, and AO could not install it. Install it in the workspace template, or use the terminal."
	default:
		return "WORKER_OPERATION_FAILED", "The worker could not complete the operation."
	}
}

// Busy reports whether anything happened in the sandbox since the previous
// call: terminal output, a process running under a workspace shell, or CPU
// use beyond the worker's own. The worker sends it with each heartbeat; the
// control plane only idle-pauses a session that stayed un-busy for the whole
// idle threshold.
func (s *Supervisor) Busy() bool {
	s.mu.Lock()
	shells := make([]int, 0, len(s.terminals))
	for _, terminal := range s.terminals {
		if terminal.shellPID > 0 {
			shells = append(shells, terminal.shellPID)
		}
	}
	s.mu.Unlock()
	return s.activity.busySince(time.Now(), shells)
}
