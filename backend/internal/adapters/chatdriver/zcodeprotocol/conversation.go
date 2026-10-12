package zcodeprotocol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/zcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type frame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}
type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *wireError) Error() string {
	return fmt.Sprintf("ZCode protocol error %d: %s", e.Code, e.Message)
}

type nativeSnapshot struct {
	Protocol struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	} `json:"protocol"`
	Session struct {
		SessionID  string `json:"sessionId"`
		Mode       string `json:"mode"`
		ArchivedAt *int64 `json:"archivedAt"`
		Workspace  struct {
			WorkspacePath string `json:"workspacePath"`
		} `json:"workspace"`
	} `json:"session"`
	Runtime struct {
		PendingRequestIDs []string `json:"pendingRequestIds"`
		ActiveTurnID      string   `json:"activeTurnId"`
	} `json:"runtime"`
}
type nativeEvent struct {
	EventID   string          `json:"eventId"`
	SessionID string          `json:"sessionId"`
	TurnID    string          `json:"turnId"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}
type eventPayload struct {
	Delta           string `json:"delta"`
	InputID         string `json:"inputId"`
	QueryID         string `json:"queryId"`
	Input           string `json:"input"`
	InputVisibility string `json:"inputVisibility"`
	MessageID       string `json:"messageId"`
	Response        string `json:"response"`
	ResultType      string `json:"resultType"`
	ToolCallID      string `json:"toolCallId"`
	ToolName        string `json:"toolName"`
	Kind            string `json:"kind"`
	RequestID       string `json:"requestId"`
	Error           struct {
		Message string `json:"message"`
	} `json:"error"`
}
type nativeOption struct {
	ID       string          `json:"optionId"`
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}
type permissionParams struct {
	RequestID string          `json:"requestId"`
	SessionID string          `json:"sessionId"`
	TurnID    string          `json:"turnId"`
	ToolName  string          `json:"toolName"`
	Reason    string          `json:"reason"`
	Input     json.RawMessage `json:"input"`
	Options   []nativeOption  `json:"options"`
}
type pendingPermission struct {
	Claimed bool             `json:"claimed,omitempty"`
	WireID  json.RawMessage  `json:"wireId"`
	Params  permissionParams `json:"params"`
}
type permissionLedger struct {
	PID       int                          `json:"pid"`
	StartedAt time.Time                    `json:"startedAt"`
	Pending   map[string]pendingPermission `json:"pending"`
}

type conversation struct {
	cfg          ports.ChatStartConfig
	transport    *persistenthost.Transport
	log          *slog.Logger
	writeMu      sync.Mutex
	mu           sync.Mutex
	nativeID     string
	pending      map[string]chan frame
	permissions  map[string]pendingPermission
	turns        map[string]string
	activeTurn   string
	sent         map[string]string
	events       chan ports.ChatEvent
	done         chan struct{}
	closeOnce    sync.Once
	closed       bool
	eventsClosed bool
}

var _ ports.ChatConversation = (*conversation)(nil)
var _ ports.ChatHistoryReader = (*conversation)(nil)
var _ ports.ChatProviderPreserver = (*conversation)(nil)
var _ ports.ChatProviderTerminator = (*conversation)(nil)
var _ ports.ChatLiveReconnector = (*conversation)(nil)

func newConversation(cfg ports.ChatStartConfig, t *persistenthost.Transport, log *slog.Logger) *conversation {
	c := &conversation{cfg: cfg, transport: t, log: log, pending: make(map[string]chan frame), permissions: make(map[string]pendingPermission), turns: make(map[string]string), sent: make(map[string]string), events: make(chan ports.ChatEvent, 1024), done: make(chan struct{})}
	go c.pump()
	return c
}
func (c *conversation) ProviderConversationID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nativeID
}
func (*conversation) Capabilities() ports.ChatCapabilities { return capabilities() }
func (c *conversation) Events() <-chan ports.ChatEvent     { return c.events }
func (*conversation) PreservesProviderOnClose() bool       { return true }
func (c *conversation) ReconnectedLive() bool              { return c.transport.Reconnected }
func (c *conversation) Close() error {
	var err error
	c.closeOnce.Do(func() { c.mu.Lock(); c.closed = true; c.mu.Unlock(); err = c.transport.Stdin.Close() })
	return err
}
func (c *conversation) Terminate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := persistenthost.Shutdown(ctx, c.cfg.DataDir, string(c.cfg.SessionID))
	_ = c.Close()
	return err
}
func (c *conversation) write(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return json.NewEncoder(c.transport.Stdin).Encode(value)
}
func (c *conversation) call(ctx context.Context, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id := "ao-zcode-" + uuid.NewString()
	result := make(chan frame, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return io.ErrClosedPipe
	}
	c.pending[id] = result
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case f := <-result:
		if f.Error != nil {
			return f.Error
		}
		if out != nil {
			return json.Unmarshal(f.Result, out)
		}
		return nil
	case <-c.done:
		return io.ErrUnexpectedEOF
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *conversation) emit(event ports.ChatEvent) {
	c.mu.Lock()
	if c.closed || c.eventsClosed {
		c.mu.Unlock()
		return
	}
	overflow := false
	select {
	case c.events <- event:
	default:
		overflow = true
	}
	c.mu.Unlock()
	if overflow {
		_ = c.Close()
	}
}
func (c *conversation) pump() {
	defer close(c.done)
	defer func() { c.mu.Lock(); c.eventsClosed = true; close(c.events); c.mu.Unlock() }()
	scanner := bufio.NewScanner(c.transport.Stdout)
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	for scanner.Scan() {
		var f frame
		if err := json.Unmarshal(scanner.Bytes(), &f); err != nil {
			c.emit(ports.ChatEvent{Kind: ports.ChatEventError, Err: ports.ErrChatDriverIncompatible})
			_ = c.Close()
			return
		}
		if f.Method != "" {
			if len(f.ID) > 0 {
				c.handleRequest(f)
			} else if f.Method == "session/event" {
				var event nativeEvent
				if json.Unmarshal(f.Params, &event) == nil {
					c.handleEvent(event)
				}
			}
			continue
		}
		var id string
		if json.Unmarshal(f.ID, &id) != nil {
			continue
		}
		c.mu.Lock()
		waiting := c.pending[id]
		c.mu.Unlock()
		if waiting != nil {
			select {
			case waiting <- f:
			default:
			}
		}
	}
	if err := scanner.Err(); err != nil {
		c.emit(ports.ChatEvent{Kind: ports.ChatEventError, Err: fmt.Errorf("ZCode protocol stream: %w", err)})
	}
}
func (c *conversation) handleRequest(f frame) {
	if f.Method == "interaction/requestPermission" {
		var p permissionParams
		if json.Unmarshal(f.Params, &p) != nil || p.RequestID == "" || len(p.Options) == 0 || len(f.Params) > 512<<10 {
			_ = c.write(map[string]any{"id": f.ID, "error": &wireError{Code: -32602, Message: "invalid or oversized permission request"}})
			return
		}
		c.mu.Lock()
		c.permissions[p.RequestID] = pendingPermission{WireID: f.ID, Params: p}
		err := c.savePendingLocked()
		c.mu.Unlock()
		if err != nil {
			_ = c.write(map[string]any{"id": f.ID, "error": &wireError{Code: -32603, Message: "AO cannot durably retain this permission request"}})
			return
		}
		c.emitApproval(p)
		return
	}
	// Native explicitly supports -32601 for host preference compatibility.
	// Unknown host tools, user-input and auth delegation are never fabricated.
	_ = c.write(map[string]any{"id": f.ID, "error": &wireError{Code: -32601, Message: "host method unsupported by AO ZCode driver"}})
}
func optionKind(raw json.RawMessage) ports.ChatDecisionKind {
	var value struct {
		Decision string            `json:"decision"`
		Updates  []json.RawMessage `json:"permissionUpdates"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	switch value.Decision {
	case "allow":
		if len(value.Updates) > 0 {
			return ports.ChatDecisionAllowAlways
		}
		return ports.ChatDecisionAllowOnce
	case "deny":
		if len(value.Updates) > 0 {
			return ports.ChatDecisionRejectAlways
		}
		return ports.ChatDecisionRejectOnce
	default:
		return ""
	}
}
func (c *conversation) emitApproval(p permissionParams) {
	options := make([]ports.ChatDecisionOption, 0, len(p.Options))
	for _, o := range p.Options {
		if kind := optionKind(o.Response); kind != "" {
			options = append(options, ports.ChatDecisionOption{ID: o.ID, Label: o.Name, Kind: kind, Raw: o.Response})
		}
	}
	c.mu.Lock()
	turn := c.turns[p.TurnID]
	if turn == "" {
		turn = c.activeTurn
	}
	c.mu.Unlock()
	c.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalRequested, ProviderTurnID: turn, RequestID: p.RequestID, Summary: p.ToolName + ": " + p.Reason, Detail: p.Input, Decisions: options})
}
func (c *conversation) ResolveRequest(ctx context.Context, id string, decision ports.ChatDecision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.permissions[id]
	if !ok || p.Claimed {
		return ports.ErrChatRequestNotPending
	}
	var response json.RawMessage
	for _, o := range p.Params.Options {
		if o.ID == decision.ID && optionKind(o.Response) != "" {
			if len(decision.Raw) > 0 && string(decision.Raw) != string(o.Response) {
				return ports.ErrChatDecisionNotOffered
			}
			response = o.Response
			break
		}
	}
	if response == nil {
		return ports.ErrChatDecisionNotOffered
	}
	p.Claimed = true
	c.permissions[id] = p
	if err := c.savePendingLocked(); err != nil {
		p.Claimed = false
		c.permissions[id] = p
		return err
	}
	// Keep the ledger until native permission.resolved confirms consumption.
	// A reconnect can safely resend only while native still lists it as pending.
	if err := c.write(map[string]any{"id": p.WireID, "result": response}); err != nil {
		return err
	}
	return nil
}
func (c *conversation) SendTurn(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	// Recheck current private input/config before each admission, including a
	// live host reconnect. Never silently truncate a refreshed instruction file.
	if _, err := zcode.ReadStandingInstructions(ctx, "", filepath.Join(c.cfg.DataDir, "prompts", string(c.cfg.SessionID), "system.md")); err != nil {
		return ports.ChatTurnRef{}, err
	}
	if err := zcode.ValidateProtocolHookBudget(ctx, ports.WorkspaceHookConfig{WorkspacePath: c.cfg.WorkspacePath, Env: c.cfg.Env}); err != nil {
		return ports.ChatTurnRef{}, err
	}
	if len(msg.Content) > 0 || len(msg.Excerpts) > 0 || msg.Settings.Model != "" || msg.Settings.Effort != "" {
		return ports.ChatTurnRef{}, ports.ErrChatCapabilityUnavailable
	}
	if msg.Settings.Approval != "" && ports.NormalizePermissionMode(msg.Settings.Approval) != ports.NormalizePermissionMode(c.cfg.Permissions) {
		return ports.ChatTurnRef{}, ports.ErrChatPermissionModeUnsupported
	}
	id := msg.ClientMessageID
	if id == "" {
		id = uuid.NewString()
	}
	c.mu.Lock()
	if prior, ok := c.sent[id]; ok {
		c.mu.Unlock()
		if prior != msg.Text {
			return ports.ChatTurnRef{}, errors.New("zcode: client message identity was reused for different content")
		}
		return ports.ChatTurnRef{ProviderTurnID: id}, nil
	}
	if c.activeTurn != "" || len(c.permissions) > 0 {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, errors.New("zcode: a native turn or permission request is pending")
	}
	c.activeTurn = id
	c.sent[id] = msg.Text
	c.mu.Unlock()
	err := c.call(ctx, "session/send", map[string]any{"sessionId": c.ProviderConversationID(), "inputId": id, "queryId": id, "content": msg.Text}, nil)
	if err != nil {
		c.mu.Lock()
		c.activeTurn = ""
		delete(c.sent, id)
		c.mu.Unlock()
		return ports.ChatTurnRef{}, err
	}
	return ports.ChatTurnRef{ProviderTurnID: id}, nil
}
func (c *conversation) Interrupt(ctx context.Context, id string) error {
	c.mu.Lock()
	active := c.activeTurn
	c.mu.Unlock()
	if active == "" || active != id {
		return ports.ErrChatNoActiveTurn
	}
	return c.call(ctx, "session/stop", map[string]any{"sessionId": c.ProviderConversationID()}, nil)
}
func (c *conversation) handleEvent(n nativeEvent) {
	c.mu.Lock()
	id := c.nativeID
	c.mu.Unlock()
	if id != "" && n.SessionID != id {
		return
	}
	var p eventPayload
	if json.Unmarshal(n.Payload, &p) != nil {
		return
	}
	c.mu.Lock()
	if n.Type == "turn.started" && p.InputVisibility != "model-only" {
		turn := p.InputID
		if turn == "" {
			turn = p.QueryID
		}
		if turn == "" {
			turn = n.TurnID
		}
		c.turns[n.TurnID] = turn
		c.activeTurn = turn
	}
	turn := c.turns[n.TurnID]
	if turn == "" {
		turn = p.InputID
	}
	if turn == "" {
		turn = n.TurnID
	}
	if n.Type == "turn.completed" || n.Type == "turn.failed" {
		if c.activeTurn == turn {
			c.activeTurn = ""
		}
	}
	if n.Type == "permission.resolved" {
		delete(c.permissions, p.RequestID)
		_ = c.savePendingLocked()
	}
	c.mu.Unlock()
	for _, event := range normalizeEvent(n, p, turn, false) {
		c.emit(event)
	}
}
func normalizeEvent(n nativeEvent, p eventPayload, turn string, history bool) []ports.ChatEvent {
	event := ports.ChatEvent{ProviderConversationID: n.SessionID, ProviderTurnID: turn, NativeTurnID: n.TurnID, ProviderEventID: n.EventID}
	switch n.Type {
	case "turn.started":
		if p.InputVisibility == "model-only" {
			return nil
		}
		event.Kind = ports.ChatEventTurnStarted
		result := []ports.ChatEvent{event}
		if history {
			event.Kind = ports.ChatEventUserMessageCompleted
			event.ProviderEventID = n.EventID + ":user"
			event.ProviderItemID = p.MessageID
			event.NativeUserMessageID = p.MessageID
			event.ClientMessageID = p.InputID
			event.Text = p.Input
			result = append(result, event)
		}
		return result
	case "turn.completed":
		event.Kind = ports.ChatEventTurnCompleted
		event.TurnState = domain.TurnStateCompleted
		if p.ResultType == "cancelled" {
			event.TurnState = domain.TurnStateInterrupted
		} else if p.ResultType != "success" {
			event.TurnState = domain.TurnStateFailed
		}
		result := []ports.ChatEvent{}
		if p.Response != "" {
			message := event
			message.Kind = ports.ChatEventMessageCompleted
			message.ProviderEventID = n.EventID + ":message"
			message.ProviderItemID = turn + ":response"
			message.Text = p.Response
			result = append(result, message)
		}
		return append(result, event)
	case "turn.failed":
		event.Kind = ports.ChatEventTurnCompleted
		event.TurnState = domain.TurnStateFailed
		event.Err = ports.NewChatProviderFailure("ZCode turn failed", p.Error.Message, nil)
	case "model.streaming":
		if history || p.Kind != "text_delta" || p.Delta == "" {
			return nil
		}
		event.Kind = ports.ChatEventMessageDelta
		event.ProviderItemID = turn + ":response"
		event.Delta = p.Delta
	case "tool.updated":
		event.ProviderItemID = p.ToolCallID
		event.Summary = p.ToolName
		event.Detail = n.Payload
		event.ActivityKind = domain.ActivityKindSystem
		switch p.Kind {
		case "started":
			event.Kind = ports.ChatEventActivityStarted
			event.ActivityStatus = domain.ActivityStatusRunning
		case "result":
			event.Kind = ports.ChatEventActivityCompleted
			event.ActivityStatus = domain.ActivityStatusCompleted
		case "error":
			event.Kind = ports.ChatEventActivityCompleted
			event.ActivityStatus = domain.ActivityStatusFailed
		default:
			return nil
		}
	case "permission.resolved":
		event.Kind = ports.ChatEventApprovalResolved
		event.RequestID = p.RequestID
	default:
		return nil
	}
	return []ports.ChatEvent{event}
}
func (c *conversation) ReadHistory(ctx context.Context) ([]ports.ChatEvent, error) {
	var result struct {
		Events []nativeEvent `json:"events"`
	}
	if err := c.call(ctx, "session/events", map[string]any{"sessionId": c.ProviderConversationID()}, &result); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatHistoryLoadFailed, err)
	}
	turns := make(map[string]string)
	history := make([]ports.ChatEvent, 0, len(result.Events))
	for _, n := range result.Events {
		if n.SessionID != c.ProviderConversationID() {
			return nil, ports.ErrChatResumeFailed
		}
		var p eventPayload
		if json.Unmarshal(n.Payload, &p) != nil {
			continue
		}
		if n.Type == "turn.started" {
			turn := p.InputID
			if turn == "" {
				turn = p.QueryID
			}
			if turn == "" {
				turn = n.TurnID
			}
			turns[n.TurnID] = turn
		}
		turn := turns[n.TurnID]
		if turn == "" {
			turn = p.InputID
		}
		if turn == "" {
			turn = n.TurnID
		}
		history = append(history, normalizeEvent(n, p, turn, true)...)
	}
	return history, nil
}
func (c *conversation) ledgerPath() string {
	return filepath.Join(c.cfg.DataDir, "chat-hosts", string(c.cfg.SessionID), "zcode-pending.json")
}
func (c *conversation) hostIdentity() (persistenthost.Descriptor, error) {
	var d persistenthost.Descriptor
	data, err := os.ReadFile(filepath.Join(c.cfg.DataDir, "chat-hosts", string(c.cfg.SessionID), "host.json")) //nolint:gosec // persistenthost validates session/data paths.
	if err != nil {
		return d, err
	}
	err = json.Unmarshal(data, &d)
	return d, err
}
func (c *conversation) savePendingLocked() error {
	d, err := c.hostIdentity()
	if err != nil {
		return err
	}
	data, err := json.Marshal(permissionLedger{PID: d.PID, StartedAt: d.StartedAt, Pending: c.permissions})
	if err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(c.ledgerPath(), data, 0o600)
}
func (c *conversation) clearPending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permissions = make(map[string]pendingPermission)
	_ = c.savePendingLocked()
}
func (c *conversation) restorePending(ids []string) error {
	if len(ids) == 0 {
		c.clearPending()
		return nil
	}
	d, err := c.hostIdentity()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(c.ledgerPath())
	if err != nil {
		return fmt.Errorf("%w: pending native interactions have no AO ledger", ports.ErrChatRecoveryInconclusive)
	}
	var ledger permissionLedger
	if json.Unmarshal(data, &ledger) != nil || ledger.PID != d.PID || !ledger.StartedAt.Equal(d.StartedAt) {
		return ports.ErrChatRecoveryInconclusive
	}
	c.mu.Lock()
	c.permissions = make(map[string]pendingPermission)
	for _, id := range ids {
		p, ok := ledger.Pending[id]
		if !ok {
			c.mu.Unlock()
			return ports.ErrChatRecoveryInconclusive
		}
		// The native snapshot proves this request is still pending. A prior
		// attachment may have lost its response; expose its real options again.
		p.Claimed = false
		c.permissions[id] = p
	}
	c.mu.Unlock()
	for _, id := range ids {
		c.emitApproval(ledger.Pending[id].Params)
	}
	return nil
}

func (c *conversation) rehydrate(ctx context.Context, activeNativeTurn string) error {
	var result struct {
		Events []nativeEvent `json:"events"`
	}
	if err := c.call(ctx, "session/events", map[string]any{"sessionId": c.ProviderConversationID()}, &result); err != nil {
		return fmt.Errorf("%w: %w", ports.ErrChatHistoryLoadFailed, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range result.Events {
		if n.SessionID != c.nativeID {
			return ports.ErrChatResumeFailed
		}
		if n.Type != "turn.started" {
			continue
		}
		var p eventPayload
		if json.Unmarshal(n.Payload, &p) != nil || p.InputVisibility == "model-only" {
			continue
		}
		id := p.InputID
		if id == "" {
			id = p.QueryID
		}
		if id == "" {
			id = n.TurnID
		}
		c.turns[n.TurnID] = id
		if p.InputID != "" {
			c.sent[p.InputID] = p.Input
		}
	}
	if activeNativeTurn != "" {
		id, ok := c.turns[activeNativeTurn]
		if !ok {
			return fmt.Errorf("%w: active native turn lacks durable input correlation", ports.ErrChatRecoveryInconclusive)
		}
		c.activeTurn = id
	}
	return nil
}
