package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/wakeguard"
)

// A message from a trusted sender (ports.MessageDeliveryOptions.WakeExitedAgent)
// to a session whose agent exited, the session itself still open, restarts the
// agent and then delivers the message, as if the agent had been idle.
//
//   - Chat: the message is queued durably first; the Chat service restarts the
//     agent once and hands it the queue. A failed restart keeps the message
//     queued for the next start (HoldChatWake on every resume).
//   - Terminal: there is no durable queue, so the restart runs first, once per
//     session however many senders wait, and the message is written after the
//     agent can take input. A failed restart returns the reason; the message
//     was not written and the sender still has it.
//
// AO does not record whether an exit was the user's `exit-agent`, a desktop
// quit, or a crash. Restarting is therefore opt-in per sender: only the user,
// another AO session (`ao send`), and worker reports set the option. Lifecycle
// nudges (CI, review comments, merge conflicts, tracker), cues, and the
// orchestrator-replacement notice never restart an agent.

// exitedWakeTimeout bounds one terminal restart plus its input-readiness wait,
// short enough that the CLI's two-minute call limit still sees the answer.
const exitedWakeTimeout = 90 * time.Second

// exitedWakeResumePoll paces the wait for a resume someone else started.
const exitedWakeResumePoll = 200 * time.Millisecond

// AgentWake reports what a send did about an exited agent.
type AgentWake string

const (
	// AgentWakeNone means the agent was running and the message went straight to it.
	AgentWakeNone AgentWake = ""
	// AgentWakeResumed means the agent had exited and AO restarted it and handed it the message.
	AgentWakeResumed AgentWake = "resumed"
	// AgentWakeResuming means the message is queued and the agent has not taken
	// it yet: the restart is still running, or the agent restarted and the
	// hand-off of its queue is outstanding.
	AgentWakeResuming AgentWake = "resuming"
)

// SendResult describes a delivered message.
type SendResult struct {
	AgentWake AgentWake
}

type chatWakeAwaiter interface {
	AwaitChatWake(ctx context.Context, id domain.SessionID) error
}

type chatWakeHolder interface {
	HoldChatWake(id domain.SessionID) (release func())
}

type exitedDelivery int

const (
	exitedDeliveryNone exitedDelivery = iota
	// exitedDeliveryChat: the send queues; wait for the Chat restart after it.
	exitedDeliveryChat
	// exitedDeliveryTerminalWoke: the terminal agent was restarted before the send.
	exitedDeliveryTerminalWoke
)

type terminalWake struct {
	done chan struct{}
	err  error
}

// SendWithResult is SendWithOptions that also reports whether the agent had to
// be restarted to take the message.
func (m *Manager) SendWithResult(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) (SendResult, error) {
	if attachment != nil {
		// Reuses StageAttachments rather than a bespoke writer: it already owns the
		// empty-workspace guard (refusing beats writing under the daemon's cwd),
		// randomized naming safe for a session sent to repeatedly, directory
		// creation, and the git-exclude step.
		refs, err := m.StageAttachments(ctx, id, []ports.SpawnAttachment{*attachment})
		if err != nil {
			return SendResult{}, fmt.Errorf("send %s: attachment: %w", id, err)
		}
		message = appendAttachmentReferences(message, refs)
	}
	delivery, err := m.beforeExitedDelivery(ctx, id, options)
	if err != nil {
		return SendResult{}, err
	}
	options.InteractionAt = m.clock()
	if err := m.send(ctx, id, message, "", options); err != nil {
		return SendResult{}, err
	}
	return m.afterExitedDelivery(ctx, id, delivery)
}

// beforeExitedDelivery decides how a send reaches an exited agent and, for a
// terminal session, restarts it. It does nothing unless the sender may restart
// the agent and the session is open with its agent exited.
func (m *Manager) beforeExitedDelivery(ctx context.Context, id domain.SessionID, options ports.MessageDeliveryOptions) (exitedDelivery, error) {
	if !options.WakeExitedAgent {
		return exitedDeliveryNone, nil
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return exitedDeliveryNone, fmt.Errorf("send %s: %w", id, err)
	}
	if !ok || rec.IsTerminated || rec.Activity.State != domain.ActivityExited ||
		rec.ProvisionState.WithDefault() != domain.SessionProvisionReady {
		// Missing, terminated, and running sessions keep their usual answers.
		return exitedDeliveryNone, nil
	}
	// An interface or account move stops the agent on purpose and queues
	// sends in its own outbox; it starts the agent itself.
	active, err := m.hasActiveInterfaceTransition(ctx, id)
	if err != nil {
		return exitedDeliveryNone, fmt.Errorf("send %s: %w", id, err)
	}
	if active {
		return exitedDeliveryNone, nil
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat == nil || m.chat.HasLiveChatController(id) {
			return exitedDeliveryNone, nil
		}
		return exitedDeliveryChat, nil
	}
	if err := m.wakeExitedTerminal(ctx, id); err != nil {
		if ctx.Err() != nil {
			return exitedDeliveryNone, ctx.Err()
		}
		return exitedDeliveryNone, fmt.Errorf("send %s: %w", id, &ports.AgentWakeError{Reason: err})
	}
	return exitedDeliveryTerminalWoke, nil
}

// afterExitedDelivery waits for the Chat restart a queued message started.
func (m *Manager) afterExitedDelivery(ctx context.Context, id domain.SessionID, delivery exitedDelivery) (SendResult, error) {
	switch delivery {
	case exitedDeliveryTerminalWoke:
		return SendResult{AgentWake: AgentWakeResumed}, nil
	case exitedDeliveryChat:
		awaiter, ok := m.chat.(chatWakeAwaiter)
		if !ok {
			return SendResult{AgentWake: AgentWakeResuming}, nil
		}
		waitErr := awaiter.AwaitChatWake(ctx, id)
		if waitErr == nil {
			return SendResult{AgentWake: AgentWakeResumed}, nil
		}
		return chatWakeOutcome(ctx, id, waitErr)
	default:
		return SendResult{}, nil
	}
}

// chatWakeOutcome answers a sender whose wait for a Chat restart ended
// without the agent taking the message. The message is queued either way. A
// caller that stopped waiting, or an agent that restarted but has not taken
// the queue yet, gets "resuming"; a failed restart is reported.
func chatWakeOutcome(ctx context.Context, id domain.SessionID, waitErr error) (SendResult, error) {
	if errors.Is(waitErr, ports.ErrAgentWakeDeliveryPending) || callerStoppedWaiting(ctx) {
		return SendResult{AgentWake: AgentWakeResuming}, nil
	}
	return SendResult{}, fmt.Errorf("send %s: %w", id, &ports.AgentWakeError{Reason: waitErr, Queued: true})
}

// callerStoppedWaiting reports whether the sender's own request ended, apart
// from any error the restart returned: a restart that timed out on its own is
// a failure even while the sender still waits.
func callerStoppedWaiting(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// wakeExitedTerminal restarts a terminal session's exited agent once, however
// many senders arrive, and waits until it can take input. The restart runs
// apart from the first sender's request, so one sender giving up does not
// abort the restart the others are waiting for.
func (m *Manager) wakeExitedTerminal(ctx context.Context, id domain.SessionID) error {
	m.exitedWakeMu.Lock()
	wake := m.exitedWakes[id]
	if wake == nil {
		if m.exitedWakeBudget == nil {
			m.exitedWakeBudget = wakeguard.New(m.clock)
		}
		if err := m.exitedWakeBudget.Admit(id); err != nil {
			m.exitedWakeMu.Unlock()
			m.logger.Warn("exited agent not restarted for a message", "sessionID", id, "error", err)
			return err
		}
		if m.exitedWakes == nil {
			m.exitedWakes = make(map[domain.SessionID]*terminalWake)
		}
		wake = &terminalWake{done: make(chan struct{})}
		m.exitedWakes[id] = wake
		go m.runTerminalWake(context.WithoutCancel(ctx), id, wake)
	}
	m.exitedWakeMu.Unlock()
	select {
	case <-wake.done:
		return wake.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) runTerminalWake(ctx context.Context, id domain.SessionID, wake *terminalWake) {
	ctx, cancel := context.WithTimeout(ctx, exitedWakeTimeout)
	defer cancel()
	m.logger.Info("restarting an exited agent to deliver a message", "sessionID", id)
	err := m.resumeExitedForDelivery(ctx, id)
	if err != nil {
		m.logger.Warn("could not restart an exited agent for a message", "sessionID", id, "error", err)
	}
	m.exitedWakeMu.Lock()
	m.exitedWakeBudget.Record(id, err)
	wake.err = err
	delete(m.exitedWakes, id)
	close(wake.done)
	m.exitedWakeMu.Unlock()
}

// resumeExitedForDelivery is resume-agent followed by the input-readiness wait
// a freshly started terminal agent needs. A resume someone else already
// started (the user's Resume button) is waited for, not repeated.
func (m *Manager) resumeExitedForDelivery(ctx context.Context, id domain.SessionID) error {
	for {
		_, err := m.ResumeAgentWithMode(ctx, id)
		if errors.Is(err, ErrResumeInProgress) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(exitedWakeResumePoll):
			}
			continue
		}
		if err != nil && !errors.Is(err, ErrAgentNotExited) {
			return err
		}
		break
	}
	return m.WaitForMessageDeliveryReady(ctx, id)
}
