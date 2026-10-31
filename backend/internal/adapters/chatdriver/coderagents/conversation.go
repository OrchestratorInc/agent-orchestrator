package coderagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commanddetail"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const eventBuffer = 4096

// forbiddenTools are Coder built-ins that would move the work to a Coder
// workspace (or a sub-agent that provisions one). AO local mode refuses them:
// the turn is interrupted and the conversation fails closed.
var forbiddenTools = []string{"create_workspace", "start_workspace", "spawn_agent"}

type conversationConfig struct {
	key          string
	sessionID    string
	dataDir      string
	workspace    string
	additional   []string
	env          map[string]string
	model        string
	effort       string
	permissions  ports.PermissionMode
	readOnly     bool
	disableTools bool
	systemPrompt string
	// chat is the existing Coder chat on resume; nil until the first turn.
	chat *coderapi.Chat
}

type turnState struct {
	id          string
	approval    ports.PermissionMode
	ctx         context.Context
	cancel      context.CancelFunc
	sawRunning  bool
	interrupted bool
	failure     error
	seq         int
	textItem    string
	reasonItem  string
	reasonText  strings.Builder
	started     map[string]bool
	completed   map[string]bool
}

type pendingApproval struct {
	turnID  string
	decided chan bool
}

type conversation struct {
	log     *slog.Logger
	cfg     conversationConfig
	client  *coderapi.Client
	orgID   string
	journal *toolJournal
	tools   *toolRunner

	ctx    context.Context
	cancel context.CancelFunc
	events chan ports.ChatEvent
	wg     sync.WaitGroup
	// emitMu orders turn-start events before any stream event of that turn.
	emitMu sync.Mutex
	// toolMu runs one tool batch at a time.
	toolMu sync.Mutex

	mu            sync.Mutex
	closed        bool
	models        []coderapi.ChatModel
	chatID        string
	chatReady     chan struct{}
	lastMessageID int64
	turn          *turnState
	starting      bool
	deferred      [][]coderapi.StreamEvent
	sentMessages  map[string]string
	broken        error
	approvals     map[string]*pendingApproval
	inflight      map[string]bool
	submitted     map[string]bool
}

var _ ports.ChatConversation = (*conversation)(nil)
var _ ports.ChatProviderTerminator = (*conversation)(nil)
var _ ports.ChatModelLister = (*conversation)(nil)

func newConversation(log *slog.Logger, s session, cfg conversationConfig) (*conversation, error) {
	cfg.permissions = ports.NormalizePermissionMode(cfg.permissions)
	c := &conversation{
		log: log, cfg: cfg, client: s.client, orgID: s.org.ID, models: s.models,
		journal: newToolJournal(cfg.dataDir, cfg.key),
		tools: &toolRunner{
			root: cfg.workspace, extraRoots: cfg.additional,
			env: mergedToolEnv(cfg.env), readOnly: cfg.readOnly,
		},
		events: make(chan ports.ChatEvent, eventBuffer), chatReady: make(chan struct{}),
		sentMessages: map[string]string{}, approvals: map[string]*pendingApproval{},
		inflight: map[string]bool{}, submitted: map[string]bool{},
	}
	if _, _, err := c.resolveSettings(ports.ChatTurnSettings{Model: cfg.model, Effort: cfg.effort}); err != nil {
		return nil, err
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	var pending []coderapi.ToolCall
	if cfg.chat != nil {
		requestCtx, cancel := coderapi.WithRequestTimeout(c.ctx)
		page, err := c.client.ChatMessages(requestCtx, cfg.chat.ID, 0)
		cancel()
		if err != nil {
			c.cancel()
			return nil, fmt.Errorf("%w: read Coder chat messages: %w", ports.ErrChatResumeFailed, classify(err))
		}
		// Without after_id the page is the newest messages, newest first.
		for _, message := range page.Messages {
			c.lastMessageID = max(c.lastMessageID, message.ID)
		}
		c.chatID = cfg.chat.ID
		close(c.chatReady)
		switch cfg.chat.Status {
		case coderapi.ChatStatusRunning, coderapi.ChatStatusRequiresAction, coderapi.ChatStatusInterrupting:
			// Coder kept working while AO was away. Adopt that run as a turn so
			// its output and completion are projected instead of dropped.
			c.turn = c.newTurn("coder-agents-adopted:"+uuid.NewString(), cfg.permissions)
			c.turn.sawRunning = true
			c.events <- ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderConversationID: cfg.key, ProviderTurnID: c.turn.id}
			c.events <- ports.ChatEvent{Kind: ports.ChatEventControllerState, ProviderConversationID: cfg.key, ProviderTurnID: c.turn.id, ControllerState: ports.ChatControllerBusy}
			if cfg.chat.Status == coderapi.ChatStatusRequiresAction {
				pending = pendingCalls(page.Messages)
			}
		}
	}
	c.wg.Add(1)
	go c.pump()
	go func() {
		<-c.ctx.Done()
		c.wg.Wait()
		close(c.events)
	}()
	if len(pending) > 0 {
		c.startCalls(pending)
	}
	return c, nil
}

func (c *conversation) newTurn(id string, approval ports.PermissionMode) *turnState {
	ctx, cancel := context.WithCancel(c.ctx)
	return &turnState{
		id: id, approval: approval, ctx: ctx, cancel: cancel,
		started: map[string]bool{}, completed: map[string]bool{},
	}
}

func (c *conversation) ProviderConversationID() string     { return c.cfg.key }
func (*conversation) Capabilities() ports.ChatCapabilities { return capabilities() }
func (c *conversation) Events() <-chan ports.ChatEvent     { return c.events }

// enter registers an operation that may emit events. It fails once the
// conversation is closed, so nothing can send on the closed event channel.
func (c *conversation) enter() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.wg.Add(1)
	return true
}

func (c *conversation) emit(event ports.ChatEvent) {
	if event.ProviderConversationID == "" {
		event.ProviderConversationID = c.cfg.key
	}
	switch event.Kind {
	case ports.ChatEventMessageDelta, ports.ChatEventReasoningDelta:
		select {
		case c.events <- event:
		default:
		}
	default:
		select {
		case c.events <- event:
		case <-c.ctx.Done():
		}
	}
}

// resolveSettings validates a turn's model and effort against the
// deployment's enabled models. Empty values defer to Coder's defaults.
func (c *conversation) resolveSettings(settings ports.ChatTurnSettings) (model, effort string, err error) {
	model = strings.TrimSpace(settings.Model)
	if model == "" {
		model = strings.TrimSpace(c.cfg.model)
	}
	effort = strings.TrimSpace(settings.Effort)
	if effort == "" {
		effort = strings.TrimSpace(c.cfg.effort)
	}
	c.mu.Lock()
	models := c.models
	c.mu.Unlock()
	var selected *coderapi.ChatModel
	for i := range models {
		candidate := &models[i]
		if (model != "" && candidate.ID == model) || (model == "" && candidate.IsDefault) {
			selected = candidate
			break
		}
	}
	if model != "" && selected == nil {
		return "", "", fmt.Errorf("%w: Coder Agents model config %q is not enabled for this deployment", ports.ErrChatConfigOptionInvalid, model)
	}
	if effort != "" && selected != nil && len(selected.ReasoningEfforts) > 0 && !slices.Contains(selected.ReasoningEfforts, effort) {
		return "", "", fmt.Errorf("%w: Coder model %q does not accept reasoning effort %q", ports.ErrChatConfigOptionInvalid, coderapi.ModelLabel(*selected), effort)
	}
	return model, effort, nil
}

func (c *conversation) SendTurn(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	if !c.enter() {
		return ports.ChatTurnRef{}, io.ErrClosedPipe
	}
	defer c.wg.Done()
	if len(msg.Content) != 0 {
		return ports.ChatTurnRef{}, errors.New("coder agents does not accept image or resource attachments through AO")
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return ports.ChatTurnRef{}, errors.New("coder agents needs a non-empty message")
	}
	approval := c.cfg.permissions
	if msg.Settings.Approval != "" {
		approval = ports.NormalizePermissionMode(msg.Settings.Approval)
	}
	if !(&Driver{}).SupportsPermissionMode(approval) {
		return ports.ChatTurnRef{}, fmt.Errorf("%w: %s", ports.ErrChatPermissionModeUnsupported, approval)
	}
	model, effort, err := c.resolveSettings(msg.Settings)
	if err != nil {
		return ports.ChatTurnRef{}, err
	}

	c.mu.Lock()
	if c.broken != nil {
		err := c.broken
		c.mu.Unlock()
		return ports.ChatTurnRef{}, err
	}
	if id := strings.TrimSpace(msg.ClientMessageID); id != "" {
		if turnID, ok := c.sentMessages[id]; ok {
			c.mu.Unlock()
			return ports.ChatTurnRef{ProviderTurnID: turnID}, nil
		}
	}
	if c.turn != nil {
		c.mu.Unlock()
		return ports.ChatTurnRef{}, errors.New("a Coder Agents turn is already running")
	}
	turnID := strings.TrimSpace(msg.ClientMessageID)
	if turnID == "" {
		turnID = uuid.NewString()
	}
	turn := c.newTurn(turnID, approval)
	c.turn = turn
	c.starting = true
	chatID := c.chatID
	c.mu.Unlock()

	var created *coderapi.Chat
	// started records that Coder began a run for this message, so a
	// "waiting" status seen first (for example in the snapshot of a stream
	// that connected after a fast run) still completes the turn.
	started := false
	if chatID == "" {
		var chat coderapi.Chat
		chat, err = c.createChat(ctx, text, model, effort)
		if err == nil {
			created, started = &chat, true
		}
	} else {
		requestCtx, cancel := coderapi.WithRequestTimeout(ctx)
		var res coderapi.CreateChatMessageResponse
		res, err = c.client.CreateChatMessage(requestCtx, chatID, coderapi.CreateChatMessageRequest{
			Content: coderapi.TextInput(text), ModelConfigID: model, ReasoningEffort: effort,
			BusyBehavior: "queue",
		})
		cancel()
		started = err == nil && !res.Queued
	}

	c.emitMu.Lock()
	defer c.emitMu.Unlock()
	c.mu.Lock()
	c.starting = false
	deferred := c.deferred
	c.deferred = nil
	if err != nil {
		turn.cancel()
		if c.turn == turn {
			c.turn = nil
		}
		c.mu.Unlock()
		for _, batch := range deferred {
			c.handleBatch(batch)
		}
		return ports.ChatTurnRef{}, classify(err)
	}
	if created != nil {
		c.chatID = created.ID
		close(c.chatReady)
	}
	turn.sawRunning = started
	if id := strings.TrimSpace(msg.ClientMessageID); id != "" {
		c.sentMessages[id] = turnID
	}
	// An interrupt that settled the turn while the message was in flight has
	// already reported its completion; starting it again would reopen it.
	active := c.turn == turn
	c.mu.Unlock()
	if active {
		c.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turnID})
		c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ProviderTurnID: turnID, ControllerState: ports.ChatControllerBusy})
	}
	for _, batch := range deferred {
		c.handleBatch(batch)
	}
	return ports.ChatTurnRef{ProviderTurnID: turnID}, nil
}

// createChat creates the Coder chat with its first message. A failure that
// may have been applied server-side is resolved by looking the chat up by
// its conversation label, so a lost response never creates a second chat.
func (c *conversation) createChat(ctx context.Context, text, model, effort string) (coderapi.Chat, error) {
	requestCtx, cancel := coderapi.WithRequestTimeout(ctx)
	defer cancel()
	chat, err := c.client.CreateChat(requestCtx, coderapi.CreateChatRequest{
		OrganizationID: c.orgID, Content: coderapi.TextInput(text),
		SystemPrompt: systemPrompt(c.cfg), ModelConfigID: model, ReasoningEffort: effort,
		Labels:             map[string]string{labelConversation: c.cfg.key, labelSession: c.cfg.sessionID},
		UnsafeDynamicTools: declaredTools(c.cfg.readOnly, c.cfg.disableTools),
		ClientType:         "api",
	})
	if status := coderapi.StatusCode(err); err != nil && (status == 0 || status >= 500) {
		lookupCtx, lookupCancel := coderapi.WithRequestTimeout(context.WithoutCancel(ctx))
		chats, lookupErr := c.client.ListChatsByLabels(lookupCtx, map[string]string{labelConversation: c.cfg.key})
		lookupCancel()
		if lookupErr == nil && len(chats) == 1 && chats[0].Labels[labelSession] == c.cfg.sessionID {
			chat, err = chats[0], nil
		}
	}
	if err != nil {
		return coderapi.Chat{}, err
	}
	if chat.HasWorkspace() {
		return coderapi.Chat{}, errors.New("coder bound a workspace to a new AO chat; refusing to run tools remotely")
	}
	return chat, nil
}

// systemPrompt is AO's standing instructions plus the local-tools contract.
// It travels as Coder's hidden per-chat system prompt, never as user text.
func systemPrompt(cfg conversationConfig) string {
	var b strings.Builder
	if prompt := strings.TrimSpace(cfg.systemPrompt); prompt != "" {
		b.WriteString(prompt)
		b.WriteString("\n\n")
	}
	b.WriteString("## Local AO worktree\n")
	b.WriteString("You are running through AO (Agent Orchestrator) on the user's machine. The working directory is ")
	b.WriteString(cfg.workspace)
	b.WriteString(".\n")
	if cfg.disableTools {
		b.WriteString("No tools are available in this conversation; answer from the conversation only.\n")
		return b.String()
	}
	b.WriteString("Use only the ao_* tools to read files, list directories")
	if !cfg.readOnly {
		b.WriteString(", edit files and run commands")
	}
	b.WriteString(" there. Paths are relative to the working directory. ")
	b.WriteString("Do not create, start or use a Coder workspace and do not spawn sub-agents: this conversation must act only on the local worktree.\n")
	if cfg.readOnly {
		b.WriteString("This conversation is read-only.\n")
	}
	return b.String()
}

func (c *conversation) Interrupt(ctx context.Context, providerTurnID string) error {
	if !c.enter() {
		return io.ErrClosedPipe
	}
	defer c.wg.Done()
	c.mu.Lock()
	t := c.turn
	if t == nil || (strings.TrimSpace(providerTurnID) != "" && providerTurnID != t.id) {
		c.mu.Unlock()
		return ports.ErrChatNoActiveTurn
	}
	t.interrupted = true
	t.cancel()
	chatID := c.chatID
	c.mu.Unlock()
	if chatID == "" {
		return nil
	}
	requestCtx, cancel := coderapi.WithRequestTimeout(ctx)
	defer cancel()
	chat, err := c.client.InterruptChat(requestCtx, chatID)
	if err != nil {
		return classify(err)
	}
	if chat.Status == coderapi.ChatStatusWaiting {
		// The run had already settled (for example while it waited for a tool
		// result), so no further status event may arrive for this turn.
		c.emitMu.Lock()
		events := c.finishLocked(t, nil)
		for _, event := range events {
			c.emit(event)
		}
		c.emitMu.Unlock()
	}
	return nil
}

func (c *conversation) ResolveRequest(ctx context.Context, requestID string, decision ports.ChatDecision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	pending, ok := c.approvals[requestID]
	if !ok {
		c.mu.Unlock()
		return ports.ErrChatRequestNotPending
	}
	var allow bool
	switch decision.ID {
	case decisionAllow:
		allow = true
	case decisionReject:
	default:
		c.mu.Unlock()
		return ports.ErrChatDecisionNotOffered
	}
	delete(c.approvals, requestID)
	c.mu.Unlock()
	pending.decided <- allow
	return nil
}

// ListModels returns the deployment's enabled model configs.
func (c *conversation) ListModels(ctx context.Context) ([]ports.ChatModel, error) {
	requestCtx, cancel := coderapi.WithRequestTimeout(ctx)
	defer cancel()
	models, err := c.client.ChatModels(requestCtx, c.orgID)
	c.mu.Lock()
	if err == nil {
		c.models = coderapi.EnabledModels(models)
	}
	current := slices.Clone(c.models)
	c.mu.Unlock()
	out := make([]ports.ChatModel, 0, len(current))
	for _, model := range current {
		out = append(out, ports.ChatModel{
			ID: model.ID, DisplayName: coderapi.ModelLabel(model), Description: model.Model,
			Default: model.IsDefault, Efforts: slices.Clone(model.ReasoningEfforts),
		})
	}
	return out, nil
}

// Close detaches from the chat. Coder keeps its history; a run in progress
// continues server-side and is adopted by the next Resume.
func (c *conversation) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	return nil
}

// Terminate stops Coder's in-flight run before detaching, so killing an AO
// session does not leave the agent working on.
func (c *conversation) Terminate() error {
	c.mu.Lock()
	chatID, active := c.chatID, c.turn != nil
	c.mu.Unlock()
	var err error
	if chatID != "" && active {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = c.client.InterruptChat(ctx, chatID)
		cancel()
	}
	return errors.Join(err, c.Close())
}

// pump streams the chat's events, reconnecting with after_id so a dropped
// socket neither loses settled messages nor replays history.
func (c *conversation) pump() {
	defer c.wg.Done()
	select {
	case <-c.chatReady:
	case <-c.ctx.Done():
		return
	}
	backoff := 500 * time.Millisecond
	for c.ctx.Err() == nil {
		c.mu.Lock()
		chatID, after := c.chatID, c.lastMessageID
		c.mu.Unlock()
		stream, err := c.client.StreamChat(c.ctx, chatID, after)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			switch status := coderapi.StatusCode(err); {
			case status == http.StatusUnauthorized, status == http.StatusForbidden, status == http.StatusNotFound,
				errors.Is(err, coderapi.ErrCrossOriginRedirect):
				c.stop(classify(err))
				return
			}
			c.log.Debug("coder agents stream unavailable; retrying", "error", err)
			select {
			case <-time.After(backoff):
			case <-c.ctx.Done():
				return
			}
			backoff = min(backoff*2, 10*time.Second)
			continue
		}
		backoff = 500 * time.Millisecond
		for {
			batch, err := stream.Next(c.ctx)
			if err != nil {
				_ = stream.Close()
				break
			}
			c.dispatch(batch)
		}
	}
}

func (c *conversation) stop(err error) {
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped, Err: err})
	_ = c.Close()
}

func (c *conversation) dispatch(batch []coderapi.StreamEvent) {
	c.emitMu.Lock()
	defer c.emitMu.Unlock()
	c.mu.Lock()
	if c.starting {
		c.deferred = append(c.deferred, batch)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.handleBatch(batch)
}

// handleBatch projects one stream batch. The caller holds emitMu.
func (c *conversation) handleBatch(batch []coderapi.StreamEvent) {
	for _, event := range batch {
		effects := c.mapEvent(event)
		for _, out := range effects.events {
			c.emit(out)
		}
		if effects.violation != "" {
			c.failClosed(effects.violation)
		}
		if len(effects.calls) > 0 {
			c.startCalls(effects.calls)
		}
		if effects.finish != nil {
			c.completeTurn(effects.finish)
		}
	}
}

type eventEffects struct {
	events    []ports.ChatEvent
	calls     []coderapi.ToolCall
	finish    *turnState
	violation string
}

func (c *conversation) mapEvent(event coderapi.StreamEvent) eventEffects {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out eventEffects
	t := c.turn
	switch event.Type {
	case coderapi.EventMessagePart:
		if t == nil || event.MessagePart == nil {
			return out
		}
		part := event.MessagePart.Part
		role := event.MessagePart.Role
		switch part.Type {
		case coderapi.PartText:
			if (role == "" || role == coderapi.RoleAssistant) && part.Text != "" {
				out.events = append(out.events, ports.ChatEvent{
					Kind: ports.ChatEventMessageDelta, ProviderTurnID: t.id, ProviderItemID: t.text(), Delta: part.Text,
				})
			}
		case coderapi.PartReasoning:
			if part.Text != "" {
				out.events = append(out.events, t.reasoningStart()...)
				t.reasonText.WriteString(part.Text)
				out.events = append(out.events, ports.ChatEvent{
					Kind: ports.ChatEventReasoningDelta, ProviderTurnID: t.id, ProviderItemID: t.reasonItem, Delta: part.Text,
				})
			}
		case coderapi.PartToolCall:
			if part.ToolCallID != "" && len(part.Args) > 0 {
				if slices.Contains(forbiddenTools, part.ToolName) {
					out.violation = part.ToolName
				}
				out.events = append(out.events, t.toolStarted(part.ToolCallID, part.ToolName, string(part.Args))...)
			}
		case coderapi.PartToolResult:
			out.events = append(out.events, t.serverToolCompleted(part)...)
		}
	case coderapi.EventMessage:
		message := event.Message
		if message == nil || message.ID <= c.lastMessageID {
			return out
		}
		c.lastMessageID = message.ID
		if t == nil {
			return out
		}
		switch message.Role {
		case coderapi.RoleAssistant:
			var text, reasoning strings.Builder
			for _, part := range message.Content {
				switch part.Type {
				case coderapi.PartText:
					text.WriteString(part.Text)
				case coderapi.PartReasoning:
					reasoning.WriteString(part.Text)
				case coderapi.PartToolCall:
					if part.ToolCallID != "" {
						if slices.Contains(forbiddenTools, part.ToolName) {
							out.violation = part.ToolName
						}
						out.events = append(out.events, t.toolStarted(part.ToolCallID, part.ToolName, string(part.Args))...)
					}
				}
			}
			if reasoning.Len() > 0 || t.reasonItem != "" {
				out.events = append(out.events, t.reasoningStart()...)
				settled := reasoning.String()
				if settled == "" {
					settled = t.reasonText.String()
				}
				out.events = append(out.events, ports.ChatEvent{
					Kind: ports.ChatEventActivityCompleted, ProviderTurnID: t.id, ProviderItemID: t.reasonItem,
					ActivityKind: domain.ActivityKindReasoning, ActivityStatus: domain.ActivityStatusCompleted,
					Summary: "Reasoning", Text: settled,
				})
				t.reasonItem = ""
				t.reasonText.Reset()
			}
			if text.Len() > 0 || t.textItem != "" {
				out.events = append(out.events, ports.ChatEvent{
					Kind: ports.ChatEventMessageCompleted, ProviderTurnID: t.id, ProviderItemID: t.text(), Text: text.String(),
				})
				t.textItem = ""
			}
			if message.Usage != nil {
				out.events = append(out.events, ports.ChatEvent{Kind: ports.ChatEventUsage, ProviderTurnID: t.id, Usage: usage(message.Usage)})
			}
		case coderapi.RoleTool:
			for _, part := range message.Content {
				if part.Type == coderapi.PartToolResult {
					out.events = append(out.events, t.serverToolCompleted(part)...)
				}
			}
		}
	case coderapi.EventStatus:
		if event.Status == nil || t == nil {
			return out
		}
		switch event.Status.Status {
		case coderapi.ChatStatusRunning, coderapi.ChatStatusRequiresAction, coderapi.ChatStatusInterrupting:
			t.sawRunning = true
		case coderapi.ChatStatusWaiting:
			if t.sawRunning {
				out.finish = t
			}
		case coderapi.ChatStatusError:
			if t.failure == nil {
				t.failure = ports.NewChatProviderFailure("Coder Agents", "the chat stopped with an error", nil)
			}
			out.finish = t
		}
	case coderapi.EventError:
		if event.Error == nil {
			return out
		}
		err := chatError(event.Error)
		turnID := ""
		if t != nil {
			t.failure = err
			turnID = t.id
		}
		out.events = append(out.events, ports.ChatEvent{Kind: ports.ChatEventError, ProviderTurnID: turnID, Err: err})
	case coderapi.EventActionRequired:
		if event.ActionRequired == nil || len(event.ActionRequired.ToolCalls) == 0 {
			return out
		}
		if t == nil {
			// Coder asks for tool results outside a turn AO started (for
			// example a run that resumed server-side). Adopt it so approvals
			// and results attach to a visible turn.
			t = c.newTurn("coder-agents-adopted:"+uuid.NewString(), c.cfg.permissions)
			t.sawRunning = true
			c.turn = t
			out.events = append(out.events,
				ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: t.id},
				ports.ChatEvent{Kind: ports.ChatEventControllerState, ProviderTurnID: t.id, ControllerState: ports.ChatControllerBusy})
		}
		out.calls = event.ActionRequired.ToolCalls
	case coderapi.EventRetry:
		if event.Retry != nil {
			c.log.Debug("coder agents retrying model call", "attempt", event.Retry.Attempt, "kind", event.Retry.Kind)
		}
	}
	return out
}

// completeTurn settles a turn once Coder is waiting again. Before reporting
// success it re-reads the chat: a workspace bound during the turn means the
// agent may have acted outside the AO worktree, so the turn fails closed.
func (c *conversation) completeTurn(t *turnState) {
	c.mu.Lock()
	chatID := c.chatID
	current := c.turn == t
	c.mu.Unlock()
	if !current {
		return
	}
	var bindingErr error
	if chatID != "" {
		requestCtx, cancel := coderapi.WithRequestTimeout(c.ctx)
		chat, err := c.client.GetChat(requestCtx, chatID)
		cancel()
		if err == nil && chat.HasWorkspace() {
			bindingErr = errWorkspaceBound
		}
	}
	for _, event := range c.finishLocked(t, bindingErr) {
		c.emit(event)
	}
}

var errWorkspaceBound = errors.New("coder bound a Coder workspace to this chat, so its tools no longer act on the AO worktree; start a new AO session")

// finishLocked ends t if it is still the active turn and returns the events
// to emit. The caller holds emitMu.
func (c *conversation) finishLocked(t *turnState, bindingErr error) []ports.ChatEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turn != t {
		return nil
	}
	c.turn = nil
	t.cancel()
	var out []ports.ChatEvent
	for id, pending := range c.approvals {
		if pending.turnID == t.id {
			delete(c.approvals, id)
			out = append(out, ports.ChatEvent{Kind: ports.ChatEventApprovalResolved, ProviderTurnID: t.id, RequestID: id})
		}
	}
	state := domain.TurnStateCompleted
	switch {
	case bindingErr != nil:
		c.broken = bindingErr
		state = domain.TurnStateFailed
		out = append(out, ports.ChatEvent{Kind: ports.ChatEventError, ProviderTurnID: t.id, Err: bindingErr})
	case c.broken != nil:
		state = domain.TurnStateFailed
	case t.interrupted:
		state = domain.TurnStateInterrupted
	case t.failure != nil:
		state = domain.TurnStateFailed
	}
	return append(out,
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: t.id, TurnState: state},
		ports.ChatEvent{Kind: ports.ChatEventControllerState, ProviderTurnID: t.id, ControllerState: ports.ChatControllerReady},
	)
}

// failClosed reacts to a forbidden built-in tool call: it marks the
// conversation broken and interrupts the run so the call does not proceed.
func (c *conversation) failClosed(tool string) {
	err := fmt.Errorf("coder agents called %s, which would move work to a Coder workspace outside the AO worktree; the run was interrupted", tool)
	c.mu.Lock()
	if c.broken != nil {
		c.mu.Unlock()
		return
	}
	c.broken = err
	turnID, chatID := "", c.chatID
	if c.turn != nil {
		turnID = c.turn.id
		c.turn.failure = err
		c.turn.cancel()
	}
	c.mu.Unlock()
	c.emit(ports.ChatEvent{Kind: ports.ChatEventError, ProviderTurnID: turnID, Err: err})
	if chatID != "" && c.enter() {
		go func() {
			defer c.wg.Done()
			ctx, cancel := coderapi.WithRequestTimeout(c.ctx)
			defer cancel()
			if _, err := c.client.InterruptChat(ctx, chatID); err != nil {
				c.log.Warn("coder agents: interrupt after forbidden tool failed", "error", err)
			}
		}()
	}
}

func chatError(e *coderapi.ChatError) error {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "Coder Agents reported an error"
	}
	return ports.NewChatProviderFailure("Coder Agents", strings.TrimSpace(message+" "+e.Detail), nil)
}

func usage(u *coderapi.ChatMessageUsage) *ports.ChatUsage {
	out := &ports.ChatUsage{}
	if u.InputTokens != nil {
		out.InputTokens = *u.InputTokens
	}
	if u.OutputTokens != nil {
		out.OutputTokens = *u.OutputTokens
	}
	if u.CacheReadTokens != nil {
		out.CachedTokens = *u.CacheReadTokens
	}
	if u.TotalTokens != nil {
		out.TotalTokens = *u.TotalTokens
		out.ContextUsed = *u.TotalTokens
	}
	if u.ContextLimit != nil {
		out.ContextWindow = *u.ContextLimit
	}
	// Coder reports per-request usage, not cumulative totals.
	out.ContextKnown = u.TotalTokens != nil && u.ContextLimit != nil
	return out
}

func (t *turnState) text() string {
	if t.textItem == "" {
		t.seq++
		t.textItem = fmt.Sprintf("%s:text:%d", t.id, t.seq)
	}
	return t.textItem
}

func (t *turnState) reasoningStart() []ports.ChatEvent {
	if t.reasonItem != "" {
		return nil
	}
	t.seq++
	t.reasonItem = fmt.Sprintf("%s:reasoning:%d", t.id, t.seq)
	return []ports.ChatEvent{{
		Kind: ports.ChatEventActivityStarted, ProviderTurnID: t.id, ProviderItemID: t.reasonItem,
		ActivityKind: domain.ActivityKindReasoning, ActivityStatus: domain.ActivityStatusRunning, Summary: "Reasoning",
	}}
}

func toolItemID(callID string) string { return "tool:" + callID }

func (t *turnState) toolStarted(callID, name, args string) []ports.ChatEvent {
	if t.started[callID] {
		return nil
	}
	t.started[callID] = true
	kind, summary, detail := toolPresentation(name, args)
	return []ports.ChatEvent{{
		Kind: ports.ChatEventActivityStarted, ProviderTurnID: t.id, ProviderItemID: toolItemID(callID),
		ActivityKind: kind, ActivityStatus: domain.ActivityStatusRunning, Summary: summary, Detail: detail,
	}}
}

// serverToolCompleted settles a tool Coder ran itself. AO's own tools are
// settled where AO runs them.
func (t *turnState) serverToolCompleted(part coderapi.ChatMessagePart) []ports.ChatEvent {
	if part.ToolCallID == "" || t.completed[part.ToolCallID] {
		return nil
	}
	if _, ours := specFor(part.ToolName); ours {
		return nil
	}
	out := t.toolStarted(part.ToolCallID, part.ToolName, "")
	t.completed[part.ToolCallID] = true
	kind, summary, _ := toolPresentation(part.ToolName, "")
	status := domain.ActivityStatusCompleted
	if part.IsError {
		status = domain.ActivityStatusFailed
	}
	detail, _ := json.Marshal(map[string]any{
		"protocol": "coder-agents", "providerToolName": part.ToolName,
		"content": commanddetail.TruncatedJSON(part.Result),
	})
	return append(out, ports.ChatEvent{
		Kind: ports.ChatEventActivityCompleted, ProviderTurnID: t.id, ProviderItemID: toolItemID(part.ToolCallID),
		ActivityKind: kind, ActivityStatus: status, Summary: summary, Detail: detail,
	})
}

// toolPresentation is the activity kind, one-line summary and typed detail
// AO's timeline renders for a tool call.
func toolPresentation(name, args string) (domain.ActivityKind, string, []byte) {
	var parsed toolArgs
	_ = json.Unmarshal([]byte(args), &parsed)
	detail := map[string]any{"protocol": "coder-agents", "providerToolName": name}
	if args != "" {
		detail["input"] = commanddetail.TruncatedJSON(json.RawMessage(args))
	}
	spec, ours := specFor(name)
	if !ours {
		encoded, _ := json.Marshal(detail)
		return domain.ActivityKindMCPTool, name, encoded
	}
	kind := activityKindFor(spec)
	summary := name
	switch spec.name {
	case toolExecute:
		detail["command"] = parsed.Command
		detail["rawCommand"] = parsed.Command
		summary = "Ran " + commanddetail.Head(parsed.Command, 120)
	case toolWrite, toolEdit:
		detail["files"] = []map[string]any{{"path": parsed.Path, "status": "modified"}}
		summary = "Edited " + parsed.Path
	case toolReadFile:
		summary = "Read " + parsed.Path
	case toolListDir:
		summary = "Listed " + parsed.Path
	}
	encoded, _ := json.Marshal(detail)
	return kind, summary, encoded
}

// pendingCalls finds AO tool calls without a result in a newest-first page.
func pendingCalls(messages []coderapi.ChatMessage) []coderapi.ToolCall {
	answered := map[string]bool{}
	for _, message := range messages {
		for _, part := range message.Content {
			if part.Type == coderapi.PartToolResult && part.ToolCallID != "" {
				answered[part.ToolCallID] = true
			}
		}
	}
	var calls []coderapi.ToolCall
	for i := len(messages) - 1; i >= 0; i-- {
		for _, part := range messages[i].Content {
			if part.Type != coderapi.PartToolCall || part.ToolCallID == "" || answered[part.ToolCallID] {
				continue
			}
			if _, ours := specFor(part.ToolName); !ours {
				continue
			}
			calls = append(calls, coderapi.ToolCall{ToolCallID: part.ToolCallID, ToolName: part.ToolName, Args: string(part.Args)})
		}
	}
	return calls
}
