package codexappserver

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commanddetail"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const eventBuffer = 4096

const approvalWait = 30 * time.Minute

var errConversationClosed = errors.New("conversation closed")

var approvalMethods = map[string]domain.ActivityKind{
	codexproto.MethodItemCommandExecutionRequestApproval: domain.ActivityKindCommand,
	codexproto.MethodItemFileChangeRequestApproval:       domain.ActivityKindFileChange,
	codexproto.MethodItemPermissionsRequestApproval:      domain.ActivityKindApproval,
}

type conversation struct {
	conn *conn
	proc *process
	log  *slog.Logger

	threadID                  string
	historyParentID           string
	providerScopeID           string
	readOnly                  bool
	disableNetwork            bool
	launchMode                ports.PermissionMode
	events                    chan ports.ChatEvent
	threadModel, threadEffort string
	fastTier, planMode        bool
	tierUnknown, planUnknown  bool
	fastTiers                 map[string]string

	mu            sync.Mutex
	pending       map[string]*parkedRequest
	pendingInputs map[string]*parkedInput
	closed        bool

	sendMu sync.Mutex

	activeTurn string

	contextTokens      int64
	contextWindow      int64
	contextAtTurnStart int64
	compactedTurn      string

	pumpDone     chan struct{}
	eventsMu     sync.RWMutex
	eventsClosed bool
	closeOnce    sync.Once
	closeErr     error
}

var _ ports.ChatConversation = (*conversation)(nil)
var _ ports.ChatProviderHibernator = (*conversation)(nil)

var _ ports.ChatModelLister = (*conversation)(nil)

var _ ports.ChatUsageReporter = (*conversation)(nil)

var _ ports.ChatCompactor = (*conversation)(nil)

var _ ports.ChatMCPReloader = (*conversation)(nil)

func newConversation(proc *process, log *slog.Logger, providerScopeID string) *conversation {
	c := &conversation{
		proc:            proc,
		log:             log,
		events:          make(chan ports.ChatEvent, eventBuffer),
		pending:         make(map[string]*parkedRequest),
		pumpDone:        make(chan struct{}),
		providerScopeID: providerScopeID,
	}
	c.conn = newConnAt(proc.stdin, proc.stdout, log, c.handleServerRequest, proc.nextRequestID)
	return c
}

func (c *conversation) start(threadID, model, effort string) {
	c.threadID = threadID
	c.threadModel = model
	c.threadEffort = effort
	go c.pump()
}

func (c *conversation) ProviderConversationID() string { return c.threadID }

func (c *conversation) PreservesProviderOnClose() bool { return c.proc.terminate != nil }

func (c *conversation) ReconnectedLive() bool { return c.proc.reconnected }

func (c *conversation) Capabilities() ports.ChatCapabilities { return capabilities() }

func (c *conversation) Events() <-chan ports.ChatEvent { return c.events }

func (c *conversation) pump() {
	defer close(c.pumpDone)
	defer func() {
		c.eventsMu.Lock()
		defer c.eventsMu.Unlock()
		c.eventsClosed = true
		close(c.events)
	}()
	retries := make(map[string]ports.ChatEvent)
	citations := newCitationFormatter()
	var usageLimits usageLimitTracker

	for n := range c.conn.notifs() {
		c.trackContext(n)
		now := time.Now()
		usageLimits.observe(n, c.threadID, now)
		var scope struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(n.Params, &scope)
		citations.observeNotification(n, c.threadID)

		for _, ev := range normalizeNotification(n, now) {
			ev = usageLimits.decorate(ev, now)
			threadID := ev.ProviderConversationID
			if threadID == "" {
				threadID = scope.ThreadID
			}
			if threadID == "" {
				threadID = c.threadID
			}
			var visible bool
			ev, visible = citations.formatEvent(threadID, ev)
			if !visible {
				continue
			}
			key := threadID + ":" + ev.ProviderTurnID
			if ev.Kind == ports.ChatEventError {
				delete(retries, key)
			}
			isRetry := ev.Kind == ports.ChatEventActivityStarted && strings.HasPrefix(ev.ProviderItemID, "codex-retry:")
			if isRetry {
				if active, ok := retries[key]; ok {
					ev.ProviderItemID = active.ProviderItemID
				} else {
					ev.ProviderItemID += ":" + uuid.NewString()
				}
				retries[key] = ev
			}
			completed := ev.Kind == ports.ChatEventTurnCompleted
			recovered := !isRetry && (ev.Kind == ports.ChatEventMessageDelta || ev.Kind == ports.ChatEventMessageCompleted ||
				ev.Kind == ports.ChatEventActivityStarted || ev.Kind == ports.ChatEventReasoningDelta || ev.Kind == ports.ChatEventPlanUpdated)
			if completed || recovered {
				if retry, ok := retries[key]; ok {
					retry.Kind = ports.ChatEventActivityCompleted
					retry.ActivityStatus = domain.ActivityStatusCompleted
					if !completed || (ev.TurnState != domain.TurnStateFailed && ev.Err == nil) {
						c.emit(retry)
					}
					delete(retries, key)
				}
			}
			rootConversation := ev.ProviderConversationID == "" || ev.ProviderConversationID == c.threadID
			if ev.Kind == ports.ChatEventTurnStarted && ev.ProviderTurnID != "" && rootConversation {
				c.mu.Lock()
				c.activeTurn = ev.ProviderTurnID
				c.contextAtTurnStart = c.contextTokens
				c.mu.Unlock()
			}
			if ev.Kind == ports.ChatEventTurnCompleted && ev.ProviderTurnID != "" && rootConversation {
				c.mu.Lock()
				if c.activeTurn == ev.ProviderTurnID {
					c.activeTurn = ""
				}
				c.mu.Unlock()
			}
			if ev.Kind == ports.ChatEventCompacted {
				settled, ok := c.settleCompaction(ev)
				if !ok {
					continue
				}
				ev = settled
			}
			if ev.Kind == ports.ChatEventApprovalResolved {
				c.discardPending(ev.RequestID)
				c.mu.Lock()
				if input := c.pendingInputs[ev.RequestID]; input != nil {
					delete(c.pendingInputs, ev.RequestID)
					if input.resolved {
						c.mu.Unlock()
						continue
					}
					input.result <- inputResult{err: ports.ErrChatRequestNotPending}
					ev.Kind = ports.ChatEventInputResolved
				}
				c.mu.Unlock()
			}
			c.emit(ev)
		}
	}
	state := ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped}
	if err := c.conn.err(); err != nil {
		state.Err = err
	}
	c.emit(state)
	c.failPendingApprovals()
}

func (c *conversation) emit(ev ports.ChatEvent) {
	c.eventsMu.RLock()
	defer c.eventsMu.RUnlock()
	if c.eventsClosed {
		return
	}
	ev = c.scopedEvent(ev)
	select {
	case c.events <- ev:
		return
	default:
	}

	switch ev.Kind {
	case ports.ChatEventMessageDelta, ports.ChatEventReasoningDelta:
		c.log.Warn("dropped chat delta: consumer behind",
			"kind", ev.Kind, "item", ev.ProviderItemID)
		return
	}

	select {
	case c.events <- ev:
	case <-time.After(5 * time.Second):
		c.log.Error("dropped chat lifecycle event: consumer stalled", "kind", ev.Kind)
	}
}

func (c *conversation) configuredServiceTier(ctx context.Context) string {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var resp struct {
		Config struct {
			ServiceTier string `json:"service_tier"`
		} `json:"config"`
	}
	if err := c.conn.request(readCtx, "config/read", map[string]any{}, &resp); err != nil {
		return ""
	}
	return resp.Config.ServiceTier
}

const codexFastServiceTier = "priority"

func (c *conversation) SendTurn(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	if strings.TrimSpace(msg.Text) == "" {
		return ports.ChatTurnRef{}, errors.New("chat message text is empty")
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	params := map[string]any{
		"threadId": c.threadID,
		"input":    []any{map[string]any{"type": "text", "text": msg.Text}},
	}
	if msg.ClientMessageID != "" {
		params["clientUserMessageId"] = msg.ClientMessageID
	}
	applyTurnSettings(params, msg.Settings, c.readOnly, c.disableNetwork)
	if msg.Settings.Fast {
		params["serviceTier"] = cmp.Or(c.fastTiers[msg.Settings.Model], c.fastTiers[c.threadModel], codexFastServiceTier)
	} else if c.fastTier || c.tierUnknown && c.configuredServiceTier(ctx) == "" {
		params["serviceTier"] = nil
	}

	planMode := c.planMode
	if model := cmp.Or(msg.Settings.Model, c.threadModel); (msg.Settings.Plan != c.planMode || c.planUnknown) && model != "" {
		mode := "default"
		if msg.Settings.Plan {
			mode = "plan"
		}
		settings := map[string]any{"model": model, "developer_instructions": nil}
		if effort := cmp.Or(msg.Settings.Effort, c.threadEffort); effort != "" {
			settings["reasoning_effort"] = effort
		}
		params["collaborationMode"] = map[string]any{"mode": mode, "settings": settings}
		planMode = msg.Settings.Plan
	}
	planKnown := params["collaborationMode"] != nil

	var resp struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := c.conn.request(ctx, "turn/start", params, &resp); err != nil {
		return ports.ChatTurnRef{}, fmt.Errorf("turn/start: %w", err)
	}
	c.fastTier = msg.Settings.Fast
	c.planMode = planMode
	c.tierUnknown, c.planUnknown = false, c.planUnknown && !planKnown

	c.mu.Lock()
	c.activeTurn = resp.Turn.ID
	c.mu.Unlock()

	return ports.ChatTurnRef{ProviderTurnID: c.scopedID(resp.Turn.ID)}, nil
}

func applyTurnSettings(params map[string]any, settings ports.ChatTurnSettings, readOnly, disableNetwork bool) {
	if settings.Model != "" {
		params["model"] = settings.Model
	}
	if settings.Effort != "" {
		params["effort"] = settings.Effort
	}
	if settings.Approval != "" {
		policy, sandbox := approvalSettings(settings.Approval)
		params["approvalPolicy"] = policy
		params["approvalsReviewer"] = approvalReviewer(settings.Approval)
		params["sandboxPolicy"] = turnSandboxPolicy(sandbox)
	}
	if readOnly {
		params["approvalPolicy"] = "never"
		params["approvalsReviewer"] = "user"
		params["sandboxPolicy"] = map[string]any{"type": "readOnly", "networkAccess": !disableNetwork}
	}
}

func turnSandboxPolicy(sandbox string) map[string]any {
	switch sandbox {
	case "workspace-write":
		return map[string]any{"type": "workspaceWrite"}
	case "read-only":
		return map[string]any{"type": "readOnly"}
	default:
		return map[string]any{"type": "dangerFullAccess"}
	}
}

func (c *conversation) ListModels(ctx context.Context) ([]ports.ChatModel, error) {
	models, err := listModels(ctx, c.conn)
	if err != nil {
		return nil, err
	}
	fastTiers := make(map[string]string, len(models))
	for _, model := range models {
		if model.FastTier != "" {
			fastTiers[model.ID] = model.FastTier
		}
	}
	c.sendMu.Lock()
	c.fastTiers = fastTiers
	c.sendMu.Unlock()
	for i := range models {
		if c.threadModel != "" {
			models[i].Default = models[i].ID == c.threadModel
		}
		if models[i].ID == c.threadModel && c.threadEffort != "" {
			models[i].DefaultEffort = c.threadEffort
		}
	}
	return models, nil
}

func listModels(ctx context.Context, connection *conn) ([]ports.ChatModel, error) {
	type modelListResponse struct {
		Data []struct {
			ID          string   `json:"id"`
			Model       string   `json:"model"`
			DisplayName string   `json:"displayName"`
			Description string   `json:"description"`
			IsDefault   bool     `json:"isDefault"`
			Hidden      bool     `json:"hidden"`
			DefaultEff  string   `json:"defaultReasoningEffort"`
			Inputs      []string `json:"inputModalities"`
			Efforts     []struct {
				ReasoningEffort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
			ServiceTiers []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"serviceTiers"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}

	var models []ports.ChatModel
	var cursor string
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var resp modelListResponse
		if err := connection.request(ctx, "model/list", params, &resp); err != nil {
			return nil, fmt.Errorf("model/list: %w", err)
		}
		for _, entry := range resp.Data {
			if entry.Hidden {
				continue
			}
			id := entry.ID
			if id == "" {
				id = entry.Model
			}
			if id == "" {
				continue
			}
			efforts := make([]string, 0, len(entry.Efforts))
			for _, effort := range entry.Efforts {
				if effort.ReasoningEffort != "" {
					efforts = append(efforts, effort.ReasoningEffort)
				}
			}
			display := entry.DisplayName
			if display == "" {
				display = id
			}
			fastTier := ""
			for _, tier := range entry.ServiceTiers {
				if strings.EqualFold(tier.Name, "fast") || tier.ID == "fast" {
					fastTier = tier.ID
					break
				}
			}
			models = append(models, ports.ChatModel{
				ID: id, DisplayName: display, Description: entry.Description,
				Default: entry.IsDefault, Efforts: efforts, DefaultEffort: entry.DefaultEff,
				FastTier: fastTier,
				Inputs:   entry.Inputs,
			})
		}
		if resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = *resp.NextCursor
	}
	return models, nil
}

func (c *conversation) ReadRateLimits(ctx context.Context) (ports.ChatRateLimits, error) {
	var resp capacityReadEnvelope
	if err := c.conn.request(ctx, "account/rateLimits/read", map[string]any{}, &resp); err != nil {
		return ports.ChatRateLimits{}, fmt.Errorf("account/rateLimits/read: %w", err)
	}
	observedAt := time.Now().UTC()
	return chatRateLimitsFromCapacity(capacityObservationFromEnvelope(resp, observedAt, false), observedAt), nil
}

func (c *conversation) Compact(ctx context.Context) (ports.ChatCompactionResult, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	c.mu.Lock()
	before := c.contextTokens
	c.mu.Unlock()

	if err := c.conn.request(ctx, "thread/compact/start", map[string]any{
		"threadId": c.threadID,
	}, nil); err != nil {
		return ports.ChatCompactionResult{}, fmt.Errorf("thread/compact/start: %w", err)
	}

	return ports.ChatCompactionResult{TokensBefore: before}, nil
}

func (c *conversation) trackContext(n notification) {
	used, window, ok := contextPositionFrom(n)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contextTokens = used
	if window > 0 {
		c.contextWindow = window
	}
}

func (c *conversation) settleCompaction(ev ports.ChatEvent) (ports.ChatEvent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ev.ProviderTurnID != "" && ev.ProviderTurnID == c.compactedTurn {
		return ports.ChatEvent{}, false
	}
	c.compactedTurn = ev.ProviderTurnID

	before, after, window := c.contextAtTurnStart, c.contextTokens, c.contextWindow
	ev.Summary = compactionSummary(before, after)

	detail := map[string]any{}
	if before > 0 {
		detail["tokensBefore"] = before
	}
	if after > 0 {
		detail["tokensAfter"] = after
	}
	if before > after && after > 0 {
		detail["tokensReclaimed"] = before - after
	}
	if window > 0 {
		detail["contextWindow"] = window
	}
	if encoded, err := json.Marshal(detail); err == nil {
		ev.Detail = encoded
	}
	return ev, true
}

func compactionSummary(before, after int64) string {
	if before <= 0 || after <= 0 || after >= before {
		return "Compacted the conversation history"
	}
	return fmt.Sprintf("Compacted history, freeing %s of context", formatTokens(before-after))
}

func formatTokens(tokens int64) string {
	if tokens < 1000 {
		return fmt.Sprintf("%d tokens", tokens)
	}
	return fmt.Sprintf("%.1fk tokens", float64(tokens)/1000)
}

func (c *conversation) Interrupt(ctx context.Context, providerTurnID string) error {
	providerTurnID = c.nativeID(providerTurnID)
	if providerTurnID == "" {
		c.mu.Lock()
		providerTurnID = c.activeTurn
		c.mu.Unlock()
	}
	if providerTurnID == "" {
		return ports.ErrChatNoActiveTurn
	}
	if err := c.conn.request(ctx, "turn/interrupt", map[string]any{
		"threadId": c.threadID,
		"turnId":   providerTurnID,
	}, nil); err != nil {
		if isNoActiveTurn(err) {
			return ports.ErrChatNoActiveTurn
		}
		return fmt.Errorf("turn/interrupt: %w", err)
	}
	return nil
}

func isNoActiveTurn(err error) bool {
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) {
		return false
	}
	return strings.Contains(strings.ToLower(rpcErr.Message), "no active turn")
}

func (c *conversation) ResolveRequest(ctx context.Context, requestID string, decision ports.ChatDecision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	requestID = c.nativeID(requestID)
	c.mu.Lock()
	parked, ok := c.pending[requestID]
	closed := c.closed
	if closed {
		c.mu.Unlock()
		return errConversationClosed
	}
	if !ok {
		input := c.pendingInputs[requestID] != nil
		c.mu.Unlock()
		if input {
			return ports.ErrChatDecisionNotOffered
		}
		return fmt.Errorf("%w: %q", ports.ErrChatRequestNotPending, requestID)
	}
	reply, err := parked.reply(decision)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	delete(c.pending, requestID)
	c.mu.Unlock()

	select {
	case parked.ch <- reply:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type parkedRequest struct {
	ch      chan ports.ChatDecision
	offered map[string]json.RawMessage
}

func (p *parkedRequest) reply(decision ports.ChatDecision) (ports.ChatDecision, error) {
	raw, ok := p.offered[decision.ID]
	if !ok {
		return ports.ChatDecision{}, fmt.Errorf("%w: %q (offered: %s)",
			ports.ErrChatDecisionNotOffered, decision.ID, strings.Join(p.offeredIDs(), ", "))
	}
	decision.Raw = raw
	return decision, nil
}

func (p *parkedRequest) offeredIDs() []string {
	ids := make([]string, 0, len(p.offered))
	for id := range p.offered {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *conversation) handleServerRequest(ctx context.Context, req serverRequest) (any, error) {
	switch req.Method {
	case codexproto.MethodAccountChatgptAuthTokensRefresh:
		return nil, c.reportAuthRefreshRequest(req.Params)
	case codexproto.MethodItemToolCall:
		return nil, c.refuseDynamicToolCall(req.Params)
	case codexproto.MethodItemToolRequestUserInput:
		return c.requestUserInput(ctx, req)
	}

	kind, known := approvalMethods[req.Method]
	if !known {
		c.log.Warn("refusing unmodelled app-server request", "method", req.Method)
		return nil, fmt.Errorf("unsupported request %s", req.Method)
	}
	if c.disableNetwork {
		return nil, errors.New("background task does not permit approval or input requests")
	}

	requestID := rawID(req.ID)
	if requestID == "" {
		return nil, errors.New("server request carried no id")
	}

	decisions, summary, detail, turnID := parseApproval(req.Method, req.Params)

	offered := make(map[string]json.RawMessage, len(decisions))
	for _, option := range decisions {
		offered[option.ID] = option.Raw
	}

	ch := make(chan ports.ChatDecision, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errConversationClosed
	}
	if c.pending[requestID] != nil || c.pendingInputs[requestID] != nil {
		c.mu.Unlock()
		return nil, errors.New("server request id is already pending")
	}
	c.pending[requestID] = &parkedRequest{ch: ch, offered: offered}
	c.mu.Unlock()

	c.emit(ports.ChatEvent{
		Kind:           ports.ChatEventApprovalRequested,
		ProviderTurnID: turnID,
		ProviderItemID: requestID,
		RequestID:      requestID,
		ActivityKind:   kind,
		ActivityStatus: domain.ActivityStatusPending,
		Summary:        summary,
		Detail:         detail,
		Decisions:      decisions,
	})

	select {
	case decision := <-ch:
		return approvalReply(decision), nil

	case <-time.After(approvalWait):
		c.discardPending(requestID)
		return nil, errors.New("approval timed out without a decision")

	case <-ctx.Done():
		c.discardPending(requestID)
		return nil, ctx.Err()
	}
}

func (c *conversation) reportAuthRefreshRequest(params json.RawMessage) error {
	var p codexproto.ChatgptAuthTokensRefreshParams
	_ = json.Unmarshal(params, &p)

	reason := string(p.Reason)
	if reason == "" {
		reason = "unauthorized"
	}
	c.log.Warn("app-server asked for ChatGPT auth tokens AO does not hold", "reason", reason)
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventAccountChanged,
		Account: &ports.ChatAccount{
			ReauthRequired: true,
			ReauthReason:   reason,
		},
	})
	return fmt.Errorf("%w: this client does not supply ChatGPT auth tokens (reason: %s)",
		ports.ErrChatAuthRequired, reason)
}

func (c *conversation) refuseDynamicToolCall(params json.RawMessage) error {
	var p codexproto.DynamicToolCallParams
	_ = json.Unmarshal(params, &p)
	c.log.Warn("refusing dynamic tool call: AO declares no client-side tools",
		"tool", p.Tool, "callId", p.CallID)
	return fmt.Errorf("client declares no tools; %q is not available", p.Tool)
}

func (c *conversation) ReloadMCPServers(ctx context.Context) (ports.ChatMCPReloadResult, error) {
	if err := c.conn.request(ctx, codexproto.MethodConfigMcpServerReload, map[string]any{}, nil); err != nil {
		return ports.ChatMCPReloadResult{}, fmt.Errorf("%s: %w", codexproto.MethodConfigMcpServerReload, err)
	}

	detail := codexproto.McpServerStatusDetailToolsAndAuthOnly
	threadID := c.threadID
	var cursor *string
	servers := make([]ports.ChatMCPServer, 0)
	seen := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	for {
		var resp codexproto.ListMcpServerStatusResponse
		params := codexproto.ListMcpServerStatusParams{
			Cursor:   cursor,
			Detail:   &detail,
			ThreadID: &threadID,
		}
		if err := c.conn.request(ctx, codexproto.MethodMcpServerStatusList, params, &resp); err != nil {
			c.log.Debug("mcp server list after reload failed", "error", err)
			return ports.ChatMCPReloadResult{}, nil
		}
		for _, entry := range resp.Data {
			if entry.Name == "" || entry.ServerInfo == nil {
				continue
			}
			if _, duplicate := seen[entry.Name]; duplicate {
				continue
			}
			seen[entry.Name] = struct{}{}
			servers = append(servers, ports.ChatMCPServer{
				Name:   entry.Name,
				Status: string(codexproto.McpServerStartupStateReady),
			})
		}
		if resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		if _, repeated := seenCursors[*resp.NextCursor]; repeated {
			c.log.Debug("mcp server list after reload repeated its cursor", "cursor", *resp.NextCursor)
			return ports.ChatMCPReloadResult{}, nil
		}
		seenCursors[*resp.NextCursor] = struct{}{}
		cursor = resp.NextCursor
	}
	return ports.ChatMCPReloadResult{Servers: servers, Authoritative: true}, nil
}

func (c *conversation) discardPending(requestID string) {
	c.mu.Lock()
	delete(c.pending, requestID)
	c.mu.Unlock()
}

func (c *conversation) failPendingApprovals() {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]*parkedRequest{}
	c.closed = true
	for id, input := range c.pendingInputs {
		delete(c.pendingInputs, id)
		if !input.resolved {
			input.result <- inputResult{err: errConversationClosed}
		}
	}
	c.mu.Unlock()
	for _, parked := range pending {
		close(parked.ch)
	}
}

func (c *conversation) Close() error {
	c.closeOnce.Do(func() {
		if c.proc.terminate == nil {
			c.failPendingApprovals()
		}
		if c.proc.stop != nil {
			c.closeErr = c.proc.stop()
		}
	})
	return c.closeErr
}

func (c *conversation) Terminate() error {
	c.closeOnce.Do(func() {
		c.failPendingApprovals()
		if c.proc.terminate != nil {
			c.closeErr = c.proc.terminate()
		} else if c.proc.stop != nil {
			c.closeErr = c.proc.stop()
		}
	})
	return c.closeErr
}

func (c *conversation) CanHibernate(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var resp struct {
		Data       []json.RawMessage `json:"data"`
		NextCursor *string           `json:"nextCursor"`
	}
	if err := c.conn.request(ctx, "thread/backgroundTerminals/list", map[string]any{"threadId": c.threadID, "limit": 1}, &resp); err != nil {
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			return false, nil
		}
		return false, err
	}
	if resp.Data == nil {
		return false, errors.New("background-terminal response is missing data")
	}
	return len(resp.Data) == 0 && (resp.NextCursor == nil || *resp.NextCursor == ""), nil
}

func (c *conversation) Hibernate() error { return c.Terminate() }

type approvalPayload struct {
	ThreadID           string            `json:"threadId"`
	TurnID             string            `json:"turnId"`
	ItemID             string            `json:"itemId"`
	Command            string            `json:"command"`
	Cwd                string            `json:"cwd"`
	Reason             string            `json:"reason"`
	AvailableDecisions []json.RawMessage `json:"availableDecisions"`
}

func parseApproval(method string, params json.RawMessage) ([]ports.ChatDecisionOption, string, []byte, string) {
	var p approvalPayload
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, "Approval required", nil, ""
	}

	options := make([]ports.ChatDecisionOption, 0, len(p.AvailableDecisions))
	for _, raw := range p.AvailableDecisions {
		if opt, ok := decisionOption(raw); ok {
			options = append(options, opt)
		}
	}

	summary := "Approval required"
	switch {
	case p.Command != "":
		summary = "Run " + commandSummary(p.Command)
	case method == "item/fileChange/requestApproval":
		summary = "Apply file changes"
	case p.Reason != "":
		summary = p.Reason
	}

	detail := map[string]any{"method": method}
	if p.Command != "" {
		detail["command"] = commanddetail.UnwrapShell(p.Command)
		detail["rawCommand"] = p.Command
	}
	if p.Cwd != "" {
		detail["cwd"] = p.Cwd
	}
	if p.ItemID != "" {
		detail["itemId"] = p.ItemID
	}
	if p.Reason != "" {
		detail["reason"] = p.Reason
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		encoded = nil
	}
	return options, summary, encoded, p.TurnID
}

func decisionOption(raw json.RawMessage) (ports.ChatDecisionOption, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ports.ChatDecisionOption{}, false
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if asString == "" {
			return ports.ChatDecisionOption{}, false
		}
		return ports.ChatDecisionOption{
			ID: asString, Label: decisionLabel(asString), Kind: codexDecisionKind(asString), Raw: raw,
		}, true
	}

	var asObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asObject); err != nil || len(asObject) != 1 {
		return ports.ChatDecisionOption{}, false
	}
	for key := range asObject {
		return ports.ChatDecisionOption{
			ID: key, Label: decisionLabel(key), Kind: codexDecisionKind(key), Raw: raw,
		}, true
	}
	return ports.ChatDecisionOption{}, false
}

func codexDecisionKind(id string) ports.ChatDecisionKind {
	switch id {
	case "accept":
		return ports.ChatDecisionAllowOnce
	case "acceptForSession", "acceptWithExecpolicyAmendment":
		return ports.ChatDecisionAllowAlways
	case "decline", "cancel":
		return ports.ChatDecisionRejectOnce
	default:
		return ""
	}
}

func decisionLabel(id string) string {
	switch id {
	case "accept":
		return "Approve"
	case "acceptForSession":
		return "Approve for this session"
	case "acceptWithExecpolicyAmendment":
		return "Approve and remember this command"
	case "decline":
		return "Decline"
	case "cancel":
		return "Cancel"
	default:
		return id
	}
}

func approvalReply(decision ports.ChatDecision) any {
	if len(decision.Raw) > 0 {
		return map[string]any{"decision": json.RawMessage(decision.Raw)}
	}
	return map[string]any{"decision": decision.ID}
}
