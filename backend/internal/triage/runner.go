package triage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	maxQueuedMessages = 8
	maxTerminalBytes  = 64 << 10
	terminalFrameSize = 16 << 10
)

var errTransitionRejected = errors.New("the investigation transition is no longer available")

type investigation struct {
	host       *host
	id         string
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	run        Run
	conv       ports.ChatConversation
	terminal   *TerminalAgent
	pending    *ports.ChatEvent
	done       chan struct{}
	replies    chan Command
	activation chan struct{}
	stopOnce   sync.Once

	controllerReady bool
	initialSent     bool
	queued          []string
}

func (i *investigation) snapshot() Run {
	i.mu.Lock()
	defer i.mu.Unlock()
	raw, _ := json.Marshal(i.run)
	var run Run
	_ = json.Unmarshal(raw, &run)
	return run
}

// commit persists the redacted snapshot before publishing the live projection.
// Native input details and the terminal replay exist only in memory.
func (i *investigation) commit(change func(*Run)) error {
	return i.commitIf(func(run *Run) bool {
		change(run)
		return true
	})
}

func (i *investigation) commitIf(change func(*Run) bool) error {
	i.mu.Lock()
	raw, err := json.Marshal(i.run)
	var next Run
	if err == nil {
		err = json.Unmarshal(raw, &next)
	}
	if err == nil {
		if !change(&next) {
			err = errTransitionRejected
		} else {
			next.UpdatedAt = i.host.deps.Now().UTC()
			raw, err = sanitizeJSON(next)
		}
	}
	var live Run
	if err == nil {
		err = json.Unmarshal(raw, &live)
	}
	if err == nil {
		live.PendingInput = next.PendingInput
		live.TerminalReplay, live.TerminalSequence = next.TerminalReplay, next.TerminalSequence
		err = i.host.store.save(live)
	}
	if err == nil {
		i.run = live
	}
	i.mu.Unlock()
	if err != nil {
		if !errors.Is(err, errTransitionRejected) {
			i.cancel()
		}
		return err
	}
	i.host.emit(Response{Type: "run", Run: &live})
	return nil
}

func (i *investigation) fail(err error) {
	if err == nil {
		err = errors.New("agent failed")
	}
	_ = i.commit(func(run *Run) {
		run.State = "failed"
		run.Activity = "The investigation stopped."
		run.Error = Redact(err.Error())
		run.PendingInput = nil
	})
}

func (i *investigation) stop() {
	i.cancel()
	go i.stopConversation()
}

func (i *investigation) stopConversation() {
	i.mu.Lock()
	conv, terminal, turnID := i.conv, i.terminal, i.run.ProviderTurnID
	i.mu.Unlock()
	if terminal != nil {
		_ = terminal.Close()
	}
	if conv != nil {
		i.stopOnce.Do(func() { _ = stopProviderConversation(conv, turnID) })
	}
}

func (i *investigation) finish() {
	i.cancel()
	i.stopConversation()
	run := i.snapshot()
	if runIsActive(run.State) || run.Preparation {
		_ = i.commit(func(run *Run) {
			run.State, run.PendingInput = "interrupted", nil
			if run.Preparation {
				run.State = "cancelled"
			}
			run.Activity = "Investigation stopped."
		})
	}
	close(i.done)
}

func (i *investigation) runProvider() {
	if i.ctx.Err() != nil {
		return
	}
	if i.host.deps.StartAgent == nil {
		i.fail(errors.New("no triage agent is configured"))
		return
	}
	transport, err := i.host.deps.StartAgent(i.ctx, i.snapshot())
	i.mu.Lock()
	i.conv, i.terminal = transport.Conversation, transport.Terminal
	i.mu.Unlock()
	if i.ctx.Err() != nil {
		return
	}
	if err != nil {
		i.fail(err)
		return
	}
	if (transport.Conversation == nil) == (transport.Terminal == nil) {
		i.fail(errors.New("agent returned an invalid transport"))
		return
	}
	if err := i.commit(func(run *Run) {
		run.Transport = "chat"
		run.State, run.Activity = "running", "Waiting for the selected agent."
		if transport.Terminal != nil {
			run.Transport = "terminal"
			run.Activity = "The selected agent is running in its native terminal."
		} else {
			run.ProviderConversationID = transport.Conversation.ProviderConversationID()
		}
	}); err != nil {
		return
	}
	if transport.Terminal != nil {
		i.runTerminal()
		return
	}
	i.runChat()
}

func (i *investigation) runChat() {
	for {
		select {
		case <-i.ctx.Done():
			return
		case cmd := <-i.replies:
			i.handleReply(cmd)
		case event, ok := <-i.conv.Events():
			if !ok {
				if i.ctx.Err() == nil {
					i.fail(errors.New("agent event stream ended"))
				}
				return
			}
			if err := i.event(event); err != nil {
				if i.snapshot().State != "failed" {
					i.fail(err)
				}
				return
			}
		}
	}
}

func (i *investigation) sendTurn(text string, human bool) error {
	i.controllerReady = false
	run := i.snapshot()
	ref, err := i.conv.SendTurn(i.ctx, ports.ChatUserMessage{
		Text: Redact(text), ClientMessageID: run.ID + "/" + uuid.NewString(), AuthoredByUser: human,
		Settings: ports.ChatTurnSettings{Model: run.Request.Model, Effort: run.Request.Effort, Approval: run.Request.Permissions},
	})
	if err != nil {
		return err
	}
	if err := i.commit(func(run *Run) {
		run.ProviderTurnID = ref.ProviderTurnID
		run.State, run.Activity = "running", "The selected agent is investigating."
	}); err != nil {
		if deferred, ok := i.conv.(ports.ChatDeferredTurnStarter); ok {
			deferred.DiscardDeferredTurn(ref.ProviderTurnID)
		}
		return err
	}
	if deferred, ok := i.conv.(ports.ChatDeferredTurnStarter); ok {
		return deferred.StartDeferredTurn(ref.ProviderTurnID)
	}
	return nil
}

func (i *investigation) event(event ports.ChatEvent) error {
	run := i.snapshot()
	if event.ProviderConversationID != "" && run.ProviderConversationID != "" && event.ProviderConversationID != run.ProviderConversationID {
		return nil
	}
	if event.ProviderTurnID != "" && run.ProviderTurnID != "" && event.ProviderTurnID != run.ProviderTurnID {
		return nil
	}
	if event.Kind == ports.ChatEventApprovalRequested && (event.RequestID == "" || len(event.Decisions) == 0) {
		return errors.New("the provider requested approval without actionable decisions")
	}
	if event.Kind == ports.ChatEventInputRequested && (event.RequestID == "" || event.Input == nil) {
		return errors.New("the provider requested input without a supported native request")
	}
	var failure error
	switch {
	case event.Kind == ports.ChatEventError:
		failure = event.Err
		if failure == nil {
			failure = errors.New("the selected agent reported an error")
		}
	case event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerStopped:
		failure = errors.New("the selected agent stopped")
	case event.Kind == ports.ChatEventTurnCompleted && event.TurnState != domain.TurnStateCompleted && event.TurnState != domain.TurnStateRecovered:
		failure = errors.New("the selected agent ended its turn unexpectedly")
	}
	var pending *ports.ChatEvent
	if err := i.commit(func(run *Run) {
		if failure != nil {
			run.State, run.Activity, run.Error, run.PendingInput = "failed", "The selected agent stopped.", Redact(failure.Error()), nil
			return
		}
		switch event.Kind {
		case ports.ChatEventMessageDelta, ports.ChatEventMessageCompleted:
			id := event.ProviderItemID
			if id == "" {
				id = "assistant-" + run.ProviderTurnID
			}
			index := slices.IndexFunc(run.Messages, func(message Message) bool { return message.ID == id && message.Role == "assistant" })
			if index < 0 {
				run.Messages = append(run.Messages, Message{ID: id, Role: "assistant", At: i.host.deps.Now().UTC()})
				index = len(run.Messages) - 1
			}
			if event.Kind == ports.ChatEventMessageDelta {
				run.Messages[index].Text += Redact(event.Delta)
			} else if event.Text != "" {
				run.Messages[index].Text = Redact(event.Text)
			}
		case ports.ChatEventActivityStarted:
			run.Activity = "The selected agent is checking evidence."
		case ports.ChatEventActivityCompleted:
			run.Activity = "The evidence check completed."
		case ports.ChatEventApprovalRequested:
			pending = &event
			run.PendingInput = approvalPending(event)
			run.State, run.Activity = "needs_input", "The selected agent needs your approval."
		case ports.ChatEventInputRequested:
			pending = &event
			run.PendingInput = inputPending(event)
			run.State, run.Activity = "needs_input", "The selected agent needs your input."
		case ports.ChatEventInputResolved, ports.ChatEventApprovalResolved:
			run.PendingInput = nil
		case ports.ChatEventTurnCompleted:
			run.ProviderTurnID = ""
			run.State, run.Activity = "running", "The selected agent is ready for your message."
		}
	}); err != nil {
		return err
	}
	if ack, ok := i.conv.(ports.ChatProviderEventAcknowledger); ok && event.ProviderEventID != "" {
		if err := ack.AcknowledgeProviderEvent(i.ctx, event.ProviderEventID); err != nil {
			return err
		}
	}
	if failure != nil {
		return failure
	}
	if pending != nil {
		i.pending = pending
	}
	if event.Kind == ports.ChatEventInputResolved || event.Kind == ports.ChatEventApprovalResolved {
		i.pending = nil
	}
	if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady {
		i.controllerReady = true
		return i.dispatchReady()
	}
	return nil
}

func approvalPending(event ports.ChatEvent) *PendingInput {
	detail := map[string]any{}
	if json.Unmarshal(event.Detail, &detail) != nil || detail == nil {
		detail = map[string]any{}
	}
	decisions := make([]map[string]string, 0, len(event.Decisions))
	for _, option := range event.Decisions {
		decision := map[string]string{"id": option.ID, "label": option.Label}
		if option.Kind != "" {
			decision["kind"] = string(option.Kind)
		}
		decisions = append(decisions, decision)
	}
	detail["requestId"], detail["decisions"] = event.RequestID, decisions
	raw, _ := json.Marshal(detail)
	return &PendingInput{ID: event.RequestID, Kind: "approval", Summary: Redact(event.Summary), Detail: raw}
}

func inputPending(event ports.ChatEvent) *PendingInput {
	detail, _ := json.Marshal(map[string]any{"requestId": event.RequestID, "inputMode": event.Input.Mode,
		"message": event.Input.Message, "schema": event.Input.Schema, "url": event.Input.URL, "elicitationId": event.Input.ElicitationID})
	if !json.Valid(detail) {
		detail = json.RawMessage(`{}`)
	}
	return &PendingInput{ID: event.RequestID, Kind: "user_input", Summary: Redact(event.Summary), Detail: detail}
}

func (i *investigation) dispatchReady() error {
	if err := i.ctx.Err(); err != nil {
		return err
	}
	if !i.controllerReady || i.pending != nil {
		return nil
	}
	if !i.initialSent {
		i.initialSent = true
		return i.sendTurn(agentStartupPrompt(), false)
	}
	if len(i.queued) == 0 {
		return nil
	}
	text := i.queued[0]
	i.queued[0] = ""
	i.queued = i.queued[1:]
	return i.sendTurn(text, true)
}

func (i *investigation) handleReply(cmd Command) {
	var err error
	switch cmd.Type {
	case "terminal-input":
		err = i.terminalInput(cmd)
	case "terminal-resize":
		err = i.terminalResize(cmd)
	default:
		err = i.reply(cmd)
	}
	if err != nil {
		i.host.fail(cmd.ID, err)
		return
	}
	i.host.emit(Response{ID: cmd.ID, Type: "ack", RunID: i.id})
}

func (i *investigation) reply(cmd Command) error {
	run := i.snapshot()
	if i.terminal != nil {
		return errors.New("send input through the native terminal")
	}
	if i.conv == nil {
		return errors.New("the selected agent is not ready for a reply")
	}
	if cmd.RequestID != "" {
		pending := run.PendingInput
		if i.pending == nil || pending == nil || cmd.RequestID != pending.ID {
			return errors.New("the agent request is no longer pending")
		}
		event := *i.pending
		if event.Kind == ports.ChatEventApprovalRequested {
			if cmd.DecisionID == "" {
				return errors.New("choose one of the agent's offered approval decisions")
			}
			var raw []byte
			found := false
			for _, decision := range event.Decisions {
				if decision.ID == cmd.DecisionID {
					raw, found = decision.Raw, true
					break
				}
			}
			if !found {
				return errors.New("that approval decision was not offered by the agent")
			}
			if err := i.conv.ResolveRequest(i.ctx, cmd.RequestID, ports.ChatDecision{ID: cmd.DecisionID, Raw: raw}); err != nil {
				return err
			}
		} else {
			if event.Input == nil || !cmd.Action.Valid() {
				return errors.New("choose a valid action for the agent's input request")
			}
			responder, ok := i.conv.(ports.ChatInputResponder)
			if !ok {
				return errors.New("the selected agent does not support structured input")
			}
			if err := responder.ResolveInput(i.ctx, cmd.RequestID, ports.ChatInputResponse{Action: cmd.Action, Content: cmd.Content}); err != nil {
				return err
			}
		}
		i.pending = nil
		return i.commit(func(run *Run) {
			run.PendingInput = nil
			run.State, run.Activity = "running", "The selected agent is continuing."
		})
	}
	text := strings.TrimSpace(cmd.Text)
	if text == "" || len(cmd.Text) > maxReplyBytes {
		return errors.New("a message must contain text and fit within 8 KiB")
	}
	if len(i.queued) >= maxQueuedMessages {
		return errors.New("the message queue is full; wait for the agent to be ready")
	}
	if err := i.commit(func(run *Run) {
		run.Messages = append(run.Messages, Message{ID: newMessageID(len(run.Messages) + 1), Role: "user", Text: Redact(cmd.Text), At: i.host.deps.Now().UTC()})
		run.Activity = "Your message is queued for the selected agent."
	}); err != nil {
		return err
	}
	i.queued = append(i.queued, cmd.Text)
	return i.dispatchReady()
}

func (i *investigation) terminalInput(cmd Command) error {
	if i.terminal == nil {
		return errors.New("the selected agent does not use a native terminal")
	}
	if cmd.Text == "" || len(cmd.Text) > maxTerminalBytes {
		return errors.New("terminal input must contain at most 64 KiB")
	}
	written, err := i.terminal.Write([]byte(cmd.Text))
	if err == nil && written != len(cmd.Text) {
		return io.ErrShortWrite
	}
	return err
}

func (i *investigation) terminalResize(cmd Command) error {
	if i.terminal == nil {
		return errors.New("the selected agent does not use a native terminal")
	}
	if cmd.Rows < 1 || cmd.Rows > 1000 || cmd.Cols < 1 || cmd.Cols > 1000 {
		return errors.New("terminal size must be between 1 and 1000 rows and columns")
	}
	return i.terminal.Resize(uint16(cmd.Rows), uint16(cmd.Cols))
}

func (i *investigation) runTerminal() {
	startup := i.terminal.startup
	done := make(chan error, 1)
	go func() {
		_, err := io.CopyBuffer(terminalOutputWriter{i}, i.terminal, make([]byte, terminalFrameSize))
		if err == nil {
			probeCtx, cancel := context.WithTimeout(i.ctx, 5*time.Second)
			alive, probeErr := i.terminal.runtime.IsChildAlive(probeCtx, i.terminal.handle)
			cancel()
			if probeErr != nil || alive {
				err = errors.Join(errors.New("native terminal disconnected before agent exit could be confirmed"), probeErr)
			}
		}
		done <- err
	}()
	for {
		select {
		case <-i.ctx.Done():
			return
		case cmd := <-i.replies:
			i.handleReply(cmd)
		case err := <-startup:
			startup = nil
			if err != nil {
				if i.ctx.Err() == nil {
					i.fail(fmt.Errorf("native terminal startup: %w", err))
				}
				return
			}
		case err := <-done:
			if i.ctx.Err() == nil {
				if err != nil {
					i.fail(fmt.Errorf("native terminal ended: %w", err))
				} else {
					_ = i.commit(func(run *Run) { run.State, run.Activity = "completed", "The selected agent exited." })
				}
			}
			return
		}
	}
}

type terminalOutputWriter struct{ run *investigation }

func (w terminalOutputWriter) Write(data []byte) (int, error) {
	// runTerminal's copy buffer already bounds each write to terminalFrameSize.
	w.run.emitTerminal(data)
	return len(data), nil
}

func (i *investigation) emitTerminal(data []byte) {
	i.mu.Lock()
	replay, _ := base64.StdEncoding.DecodeString(i.run.TerminalReplay)
	replay = append(replay, data...)
	if len(replay) > maxTerminalBytes {
		replay = append([]byte(nil), replay[len(replay)-maxTerminalBytes:]...)
	}
	i.run.TerminalSequence += int64(len(data))
	sequence := i.run.TerminalSequence
	i.run.TerminalReplay = base64.StdEncoding.EncodeToString(replay)
	i.run.UpdatedAt = i.host.deps.Now().UTC()
	i.mu.Unlock()
	i.host.emit(Response{Type: "terminal-output", RunID: i.id, Data: base64.StdEncoding.EncodeToString(data), Sequence: sequence})
}
