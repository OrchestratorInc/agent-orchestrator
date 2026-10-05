package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

type startupCueStore interface {
	SelectStartupCue(context.Context, domain.ProjectID) (domain.Cue, bool, error)
	ClaimStartupCue(context.Context, domain.SessionID, domain.StartupCueRun) (bool, error)
	BeginStartupCue(context.Context, domain.SessionID, domain.StartupCueRun) (bool, error)
	FinishStartupCue(context.Context, domain.SessionID, domain.StartupCueRun) error
	EnqueueStartupCueMessage(context.Context, domain.SessionID, string, string) (bool, error)
	ListStartupCueMessages(context.Context, domain.SessionID) ([]domain.StartupCueMessage, error)
	MarkStartupCueMessageDelivered(context.Context, int64) error
}

type startupCueExecution struct {
	cancel          context.CancelFunc
	terminalHandle  string
	cancelRequested bool
	stopped         chan struct{}
}

// prepareStartupCue pins the definition before any controller can accept input.
func (m *Manager) prepareStartupCue(ctx context.Context, id domain.SessionID, project domain.ProjectID) error {
	store, ok := m.store.(startupCueStore)
	if !ok || project == "" {
		return nil
	}
	cue, found, err := store.SelectStartupCue(ctx, project)
	if err != nil || !found {
		return err
	}
	_, err = store.ClaimStartupCue(ctx, id, domain.StartupCueRun{
		CueID: cue.ID, Name: cue.Name, Command: cue.Command,
		State: "pending", StartedAt: m.clock(),
	})
	return err
}

// startStartupCue waits for command completion inside a daemon-owned worker.
// Session creation stays responsive; provider delivery is gated durably.
func (m *Manager) startStartupCue(id domain.SessionID, project domain.ProjectRecord, workspace string, release func(context.Context) error) {
	m.runInBackground(func() {
		var execution *startupCueExecution
		defer func() {
			if execution == nil {
				return
			}
			m.startupCueExecMu.Lock()
			if current := m.startupCueExec[id]; current == execution {
				delete(m.startupCueExec, id)
			}
			m.startupCueExecMu.Unlock()
			close(execution.stopped)
		}()
		ctx := m.backgroundContext
		// Admission and cancellation share this lock, so cancellation cannot
		// observe a running row before its worker is registered.
		releaseAdmission := m.lockStartupDelivery(id)
		rec, found, err := m.store.GetSession(ctx, id)
		if err != nil || !found || !rec.StartupCue.HoldsInput() {
			releaseAdmission()
			return
		}
		store, ok := m.store.(startupCueStore)
		if !ok {
			releaseAdmission()
			return
		}
		run := *rec.StartupCue
		run.State = "running"
		run.StartedAt = m.clock()
		began, err := store.BeginStartupCue(ctx, id, run)
		if err != nil {
			releaseAdmission()
			m.logger.Error("startup cue: record execution", "sessionID", id, "error", err)
			return
		}
		if !began {
			releaseAdmission()
			return
		}
		commandCtx, cancel := context.WithCancel(ctx)
		execution = &startupCueExecution{cancel: cancel, stopped: make(chan struct{})}
		m.startupCueExecMu.Lock()
		if m.startupCueExec == nil {
			m.startupCueExec = make(map[domain.SessionID]*startupCueExecution)
		}
		m.startupCueExec[id] = execution
		m.startupCueExecMu.Unlock()
		releaseAdmission()
		// Kill can arrive while a command is running, independently of startup's API request.
		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-commandCtx.Done():
					return
				case <-ticker.C:
					current, exists, readErr := m.store.GetSession(commandCtx, id)
					if readErr == nil && (!exists || current.IsTerminated) {
						cancel()
						return
					}
				}
			}
		}()
		m.shellTerminalsMu.Lock()
		runner := m.startupCueRunner
		m.shellTerminalsMu.Unlock()
		var output string
		var code *int
		var commandErr error
		if runner != nil {
			started, err := runner.RunStartupCue(commandCtx, shellterm.RunStartupCueInput{ProjectID: domain.ProjectID(project.ID), SessionID: id, Command: run.Command})
			if err != nil {
				commandErr = err
			} else {
				run.TerminalHandle = started.Terminal.HandleID
				m.startupCueExecMu.Lock()
				execution.terminalHandle = run.TerminalHandle
				m.startupCueExecMu.Unlock()
				// Persist the handle immediately. Cancellation must be able to
				// close the terminal before queued input is released.
				if err := store.FinishStartupCue(ctx, id, run); err != nil {
					commandErr = err
				}
				for commandErr == nil {
					select {
					case <-commandCtx.Done():
						commandErr = commandCtx.Err()
					default:
					}
					if commandErr != nil {
						break
					}
					var outputErr error
					output, outputErr = runner.GetOutput(commandCtx, started.Terminal.HandleID, 200)
					if outputErr != nil {
						commandErr = outputErr
						break
					}
					if len(output) > 64<<10 {
						output = output[len(output)-(64<<10):]
					}
					if marker, ok := startupCueMarkerResult(output, started.Marker); ok {
						code = &marker
						if marker != 0 {
							commandErr = fmt.Errorf("command exited with code %d", marker)
						}
						break
					}
					time.Sleep(250 * time.Millisecond)
				}
			}
		} else {
			result := runWorkspaceCommand(commandCtx, run.Command, "", workspace, m.runtimeEnv(id, domain.ProjectID(project.ID), "", project.Config.Env), 64<<10)
			output, code, commandErr = result.Output, result.ExitCode, result.Err
		}
		close(done)
		deadlineErr := commandCtx.Err()
		cancel()
		m.startupCueExecMu.Lock()
		cancelRequested := execution != nil && execution.cancelRequested
		m.startupCueExecMu.Unlock()
		// Context cancellation stops polling, not the terminal process. Confirm
		// shutdown before recording a terminal outcome or admitting delivery.
		if runner != nil && run.TerminalHandle != "" && (cancelRequested || commandErr != nil && code == nil) {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			stopErr := closeStartupCueTerminal(stopCtx, runner, run.TerminalHandle)
			stopCancel()
			if stopErr != nil {
				run.DeliveryHeld, run.Output = true, output
				run.Error = fmt.Sprintf("startup cue terminal shutdown: %v", stopErr)
				persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer persistCancel()
				if err := store.FinishStartupCue(persistCtx, id, run); err != nil {
					m.logger.Error("startup cue: persist shutdown failure", "sessionID", id, "error", err)
				}
				if _, err := m.setProvisionState(persistCtx, id, domain.SessionProvisionFailed, run.Error); err != nil {
					m.logger.Error("startup cue: record shutdown failure", "sessionID", id, "error", err)
				}
				return
			}
		}
		if current, exists, readErr := m.store.GetSession(ctx, id); readErr == nil && exists && current.StartupCue != nil && current.StartupCue.State == "cancelled" {
			if err := m.releaseStartupCueDelivery(ctx, id, *current.StartupCue, release); err != nil {
				m.logger.Warn("startup cue: resume delivery after cancellation", "sessionID", id, "error", err)
			}
			return
		}
		now := m.clock()
		run.CompletedAt, run.Output, run.ExitCode = &now, output, code
		run.State = "succeeded"
		if commandErr != nil {
			run.State, run.Error = "failed", commandErr.Error()
			if cancelRequested {
				run.State, run.Error = "cancelled", "Startup cue cancelled by user"
			} else if errors.Is(deadlineErr, context.Canceled) {
				run.State, run.Error = "cancelled", "Startup cue was interrupted"
			}
		}
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer persistCancel()
		run.DeliveryHeld = true
		if err := store.FinishStartupCue(persistCtx, id, run); err != nil {
			m.logger.Error("startup cue: persist result", "sessionID", id, "error", err)
			return
		}
		current, exists, err := m.store.GetSession(ctx, id)
		if err != nil || !exists || current.IsTerminated || ctx.Err() != nil {
			return
		}
		if err := m.releaseStartupCueDelivery(ctx, id, run, release); err != nil {
			m.logger.Warn("startup cue: resume delivery", "sessionID", id, "error", err)
		}
	})
}

// releaseStartupCueDelivery preserves the pre-cue queue semantics: queued
// messages remain gated until the complete delivery callback succeeds. A
// failed drain leaves the session retryable and never permits later input to
// bypass undelivered rows.
func (m *Manager) releaseStartupCueDelivery(ctx context.Context, id domain.SessionID, run domain.StartupCueRun, release func(context.Context) error) error {
	releaseDelivery := m.lockStartupDelivery(id)
	defer releaseDelivery()
	current, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !found || current.IsTerminated {
		return ErrTerminated
	}
	if current.StartupCue == nil || !current.StartupCue.DeliveryHeld {
		return nil
	}
	if current.StartupCue.State == "pending" || current.StartupCue.State == "running" {
		return fmt.Errorf("startup cue command has not stopped; cancel setup before retrying delivery")
	}
	run = *current.StartupCue

	if err := release(ctx); err != nil {
		run.DeliveryHeld = true
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if store, ok := m.store.(startupCueStore); ok {
			if persistErr := store.FinishStartupCue(persistCtx, id, run); persistErr != nil {
				err = errors.Join(err, persistErr)
			}
		}
		if _, provisionErr := m.setProvisionState(persistCtx, id, domain.SessionProvisionFailed, fmt.Sprintf("startup cue delivery: %v", err)); provisionErr != nil {
			err = errors.Join(err, provisionErr)
		}
		return err
	}

	run.DeliveryHeld = false
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if store, ok := m.store.(startupCueStore); ok {
		if err := store.FinishStartupCue(persistCtx, id, run); err != nil {
			return err
		}
	}
	_, err = m.setProvisionState(persistCtx, id, domain.SessionProvisionReady, "")
	return err
}

func (m *Manager) releasePersistedStartupCueDelivery(ctx context.Context, id domain.SessionID) error {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !found || rec.StartupCue == nil {
		return ErrNotFound
	}
	var release func(context.Context) error
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat && m.chat != nil {
		release = func(releaseCtx context.Context) error { return m.drainStartupChatQueue(releaseCtx, id) }
	} else {
		release = func(releaseCtx context.Context) error { return m.drainStartupCueMessages(releaseCtx, id) }
	}
	return m.releaseStartupCueDelivery(ctx, id, *rec.StartupCue, release)
}

func (m *Manager) drainStartupChatQueue(ctx context.Context, id domain.SessionID) error {
	if drainer, ok := m.chat.(interface {
		DrainStartupChatQueue(context.Context, domain.SessionID) error
	}); ok {
		return drainer.DrainStartupChatQueue(ctx, id)
	}
	return m.chat.DrainChatQueue(ctx, id)
}

// CancelStartupCue stops a running startup terminal and releases delivery.
func (m *Manager) CancelStartupCue(ctx context.Context, id domain.SessionID) (domain.StartupCueRun, error) {
	releaseAdmission := m.lockStartupDelivery(id)
	admissionHeld := true
	defer func() {
		if admissionHeld {
			releaseAdmission()
		}
	}()
	store, ok := m.store.(startupCueStore)
	if !ok {
		return domain.StartupCueRun{}, fmt.Errorf("startup cue store unavailable")
	}
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.StartupCueRun{}, err
	}
	if !found || rec.StartupCue == nil {
		return domain.StartupCueRun{}, ErrNotFound
	}
	run := *rec.StartupCue
	if !run.HoldsInput() {
		return run, nil
	}
	m.startupCueExecMu.Lock()
	execution := m.startupCueExec[id]
	if execution != nil {
		execution.cancelRequested = true
		execution.cancel()
	}
	terminalHandle := run.TerminalHandle
	if execution != nil && execution.terminalHandle != "" {
		terminalHandle = execution.terminalHandle
	}
	m.startupCueExecMu.Unlock()
	if execution != nil {
		// The worker owns terminal shutdown and result persistence. It also
		// handles cancellation while RunStartupCue is still publishing a handle.
		releaseAdmission()
		admissionHeld = false
		select {
		case <-execution.stopped:
		case <-ctx.Done():
			return run, ctx.Err()
		}
		updated, found, readErr := m.store.GetSession(ctx, id)
		if readErr != nil {
			return run, readErr
		}
		if !found || updated.StartupCue == nil {
			return run, ErrNotFound
		}
		if updated.StartupCue.State == "running" {
			return *updated.StartupCue, fmt.Errorf("%s", updated.StartupCue.Error)
		}
		if err := m.releasePersistedStartupCueDelivery(ctx, id); err != nil {
			return *updated.StartupCue, err
		}
		updated, readErr = m.getRecord(ctx, id)
		if readErr != nil {
			return run, readErr
		}
		return *updated.StartupCue, nil
	}
	if terminalHandle != "" {
		m.shellTerminalsMu.Lock()
		runner := m.startupCueRunner
		m.shellTerminalsMu.Unlock()
		if runner == nil {
			return run, fmt.Errorf("startup cue terminal runner unavailable")
		}
		if err := closeStartupCueTerminal(ctx, runner, terminalHandle); err != nil {
			return run, err
		}
	}
	now := m.clock()
	run.State, run.Error, run.CompletedAt, run.DeliveryHeld = "cancelled", "Startup cue cancelled by user", &now, true
	if err := store.FinishStartupCue(ctx, id, run); err != nil {
		return domain.StartupCueRun{}, err
	}
	releaseAdmission()
	admissionHeld = false
	if err := m.releasePersistedStartupCueDelivery(ctx, id); err != nil {
		return run, err
	}
	if updated, found, err := m.store.GetSession(ctx, id); err == nil && found && updated.StartupCue != nil {
		return *updated.StartupCue, nil
	}
	return run, nil
}

func closeStartupCueTerminal(ctx context.Context, runner StartupCueRunner, handle string) error {
	err := runner.CloseShellTerminal(ctx, handle)
	var apiError *apierr.Error
	if errors.As(err, &apiError) && apiError.Code == "SHELL_TERMINAL_NOT_FOUND" {
		return nil // Confirmed terminal deletion makes repeated cancellation safe.
	}
	return err
}

func startupCueMarkerResult(output, marker string) (int, bool) {
	output = stripTerminalControlSequences(output)
	idx := strings.LastIndex(output, marker)
	if idx < 0 {
		return 0, false
	}
	value := strings.TrimSpace(output[idx+len(marker):])
	line := strings.Fields(value)
	if len(line) == 0 {
		return 0, false
	}
	code, err := strconv.Atoi(line[0])
	return code, err == nil
}

// stripTerminalControlSequences keeps completion detection independent of the
// PTY renderer. ConPTY and tmux may include cursor/colour sequences between
// otherwise adjacent bytes, especially while a prompt is being redrawn.
func stripTerminalControlSequences(value string) string {
	var out strings.Builder
	escaped := false
	csi := false
	osc := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if osc {
			if ch == 0x07 {
				osc = false
			}
			continue
		}
		if csi {
			if ch >= 0x40 && ch <= 0x7e {
				csi = false
			}
			continue
		}
		if escaped {
			switch {
			case ch == '[':
				csi, escaped = true, false
			case ch == ']':
				osc, escaped = true, false
			case ch >= 0x40 && ch <= 0x7e:
				escaped = false
			case ch == 0x1b:
				escaped = true
			}
			continue
		}
		if ch == 0x1b {
			escaped = true
			continue
		}
		if ch != '\r' {
			out.WriteByte(ch)
		}
	}
	return out.String()
}

func (m *Manager) drainStartupCueMessages(ctx context.Context, id domain.SessionID) error {
	store, ok := m.store.(startupCueStore)
	if !ok {
		return nil
	}
	messages, err := store.ListStartupCueMessages(ctx, id)
	if err != nil {
		return err
	}
	for _, msg := range messages {
		rec, found, err := m.store.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if !found || rec.IsTerminated {
			return ErrTerminated
		}
		if rec.StartupCue != nil && rec.StartupCue.DeliveryHeld {
			message, err := m.prepareOutboundMessage(ctx, id, msg.Message)
			if err != nil {
				return err
			}
			// Startup owns delivery admission while raw terminal input stays held.
			outcome, err := m.messenger.DeliverUnderMutation(ctx, id, message)
			if err != nil {
				return err
			}
			if outcome != sessionguard.Sent {
				return fmt.Errorf("startup cue: queued delivery suppressed: %s", outcome.String())
			}
		} else if err := m.send(ctx, id, msg.Message, msg.ClientMessageID, true); err != nil {
			return err
		}
		if err := store.MarkStartupCueMessageDelivered(ctx, msg.ID); err != nil {
			return err
		}
	}
	return nil
}

// recoverStartupCues releases a hold that belonged to the previous daemon.
// The command outcome is uncertain, so recovery never reruns it.
func (m *Manager) recoverStartupCues(ctx context.Context, records []domain.SessionRecord) error {
	store, ok := m.store.(startupCueStore)
	if !ok {
		return nil
	}
	for i := range records {
		rec := &records[i]
		if !rec.StartupCue.HoldsInput() {
			continue
		}
		run := *rec.StartupCue
		now := m.clock()
		if run.State == "pending" || run.State == "running" {
			run.State, run.Error, run.CompletedAt = "interrupted", "AO restarted while the startup cue was running; it was not rerun", &now
		}
		run.DeliveryHeld = true
		if err := store.FinishStartupCue(ctx, rec.ID, run); err != nil {
			return err
		}
		rec.StartupCue = &run
		if rec.Metadata.WorkspacePath == "" && rec.Metadata.RuntimeHandleID == "" && rec.Metadata.ProviderConversationID == "" {
			run.DeliveryHeld = false
			if err := store.FinishStartupCue(ctx, rec.ID, run); err != nil {
				return err
			}
			rec.StartupCue = &run
			continue
		}
		// Delivery is intentionally deferred until background reconciliation,
		// after any Chat controller has been reattached. Startup safety must not
		// call a controller-dependent drain before the daemon can serve.
	}
	return nil
}

func (m *Manager) lockStartupDelivery(id domain.SessionID) func() {
	value, _ := m.startupDeliveryLocks.LoadOrStore(id, &sync.Mutex{})
	lock, ok := value.(*sync.Mutex)
	if !ok {
		panic("startup delivery lock has an invalid type")
	}
	lock.Lock()
	return lock.Unlock
}
