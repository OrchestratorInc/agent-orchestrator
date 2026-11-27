package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/backgroundtasks"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	eventBuffer = 4096
	// approvalWait bounds an unanswered permission request. Questions have no
	// such bound (see requestInput).
	approvalWait = 30 * time.Minute
	// sessionCloseWait bounds the session/close sent before a provider is
	// terminated (quickSessionCloseWait for a QuickStop provider).
	sessionCloseWait      = 2 * time.Second
	quickSessionCloseWait = 500 * time.Millisecond
)

var (
	errConversationClosed = errors.New("ACP conversation closed")
	errClientCapability   = errors.New("ACP client capability not advertised")

	// ErrACPSetterUnsupported is returned by applyTurnSettings when the agent
	// does not implement session/set_mode or session/set_config_option
	// (JSON-RPC -32601). Start() and Resume() tolerate it because the initial
	// model and permission mode may have been applied via launch-time flags;
	// SendTurn propagates it so a runtime change that cannot be applied is
	// surfaced to the user instead of silently running with the wrong mode.
	ErrACPSetterUnsupported = errors.New("ACP agent does not support runtime session configuration")
)

type preparedTurn struct {
	id              string
	clientMessageID string
	prompt          []acpsdk.ContentBlock
	// endedCycle is the agent's own cycle this turn ended, settled when the
	// turn runs (see settleEndedCycle).
	endedCycle *providerCycle
}

type interruptAttempt struct {
	turnID string
	done   chan struct{}
	err    error
}

type parkedPermission struct {
	options map[string]json.RawMessage
	result  chan string
	ready   chan struct{}
}

type parkedInput struct {
	request ports.ChatInputRequest
	turnID  string
	result  chan ports.ChatInputResponse
	ready   chan struct{}
}

type toolState struct {
	id             string
	title          string
	kind           acpsdk.ToolKind
	status         acpsdk.ToolCallStatus
	locations      []acpsdk.ToolCallLocation
	content        []acpsdk.ToolCallContent
	rawInput       any
	rawOutput      any
	meta           map[string]any
	terminalOutput string
}

type nestedMessageState struct {
	text     string
	parentID string
}

type conversation struct {
	harness              domain.AgentHarness
	suppressSessionModes bool
	backgroundObserved   backgroundtasks.Tracker
	conn                 *acpsdk.ClientSideConnection
	legacyWire           *legacyACPTransport
	proc                 *process
	log                  *slog.Logger
	providerScopeID      string
	decodeConversationID func(string) (string, error)
	canDeleteFork        bool
	// task ID to reference-only native session alias; guarded by mu.
	backgroundAgentSessions map[string]string

	// handshake is the agent's initialize answer, for steering details that
	// vary by agent version.
	handshake       acpsdk.InitializeResponse
	unadoptedForkID string

	mu                sync.Mutex
	sessionID         string
	reportedSessionID string
	capabilities      ports.ChatCapabilities
	prepared          *preparedTurn
	activeTurn        string
	settlingTurn      string
	turnCancel        context.CancelFunc
	interrupt         *interruptAttempt
	pending           map[string]*parkedPermission
	pendingInputs     map[string]*parkedInput
	accepted          map[string]persistentInteractionCommand
	messages          map[string]string
	thoughts          map[string]string
	nestedMessages    map[string]nestedMessageState
	tools             map[string]*toolState
	turnDiffs         *turnDiffAccumulator
	turnDiffTurnID    string
	providerFailure   *ports.ChatEvent
	configOptions     []ports.ChatConfigOption
	skills            []ports.ChatSkill
	skillsKnown       bool
	// subagentRecords reads subagents the agent records only in its own store,
	// with the launch environment and workspace it reads them under;
	// recordFollower is what is known of them. See subagent_records.go.
	subagentRecords   *SubagentRecords
	subagentRecordEnv map[string]string
	subagentRecordCwd string
	recordFollower    *recordFollower
	// compactAdvertised is whether the agent's own list, before HideCommand,
	// named compact; start derives compaction from it, not from the shown skills.
	compactAdvertised bool
	closed            bool
	// sessionLost records that the agent stopped holding this conversation's
	// session and the provider was stopped for it. See providerSessionLost.
	sessionLost       bool
	modeFor           func(ports.PermissionMode) string
	optionsFor        func(ports.ChatTurnSettings) []SessionOption
	splitModelVariant func(string) (string, SessionOption, bool)
	// requireAdvertised refuses a config option the session did not advertise
	// instead of sending it. See Config.RequireAdvertisedOptions.
	requireAdvertised bool
	// reapplySettings makes the next applyTurnSettings send every setting even
	// where the catalog already names it current. See markSettingsApplied.
	reapplySettings bool
	// restoredFrom maps each option markSettingsApplied moved to the value the
	// snapshot had, until the provider accepts a value or reports its catalog.
	restoredFrom map[string]string
	// unofferedModel is the optional model the session was opened on but the
	// agent does not offer, so applyTurnSettings left the agent's own model in
	// place. It is the session's model, not a switch, on later sends.
	unofferedModel    string
	permissionMode    ports.PermissionMode
	permissionFor     PermissionPolicy
	initialPermission ports.PermissionMode
	validateSettings  TurnSettingsValidator
	extensionFor      ClientExtensionHandler
	extensionMethods  map[string]string
	// steering is how this agent takes mid-turn guidance. pendingSteers holds
	// guidance a drain agent has not collected yet; see SteerDrain.
	steering      steering
	pendingSteers []*pendingSteer
	// stoppingTurn is the turn Stop has begun to stop. Steers for it are
	// refused, so one that arrives while Stop waits for the dispatch lock
	// cannot join, or hold up, a turn the user is stopping.
	stoppingTurn string
	// stopAttempts counts the Stops of stoppingTurn that have withdrawn its
	// steers and not given them back. Steering returns only when the last
	// one that fails gives it back; see WithdrawPendingSteers.
	stopAttempts int
	// nudging is set while a drain nudge is in flight; see steerByDrain.
	nudging atomic.Bool
	// steerSegment counts steers the running turn has taken. Text the agent
	// streams without a message id after one goes to a new message, so it
	// renders after the steer instead of growing the reply above it.
	steerSegment int
	// turnCtx is the running turn's context. A SteerPrompt steer runs its prompt
	// under it, so cancelling the turn reaches every prompt in it.
	turnCtx context.Context
	// promptSeq numbers the running turn's prompts: 0 started it and each
	// SteerPrompt steer adds one. outstanding holds those still running; the
	// turn settles once every one of them has returned.
	promptSeq   int
	outstanding map[int]bool
	// adoptedPrompt means the running turn's prompt was sent by an earlier
	// daemon attachment (live reconnect). The host passes its answers on as
	// private notifications, and only the host's result ends the turn.
	adoptedPrompt bool
	// turnResult is the host's result for the running turn, the answer that
	// carries its receipt, once one of the turn's prompts has returned it.
	turnResult *promptAnswer
	// lastAnswer is the latest answer any of the running turn's prompts gave.
	// Without a host receipt the turn ends with it.
	lastAnswer *promptAnswer
	// promptSteers are SteerPrompt steers waiting to learn whether they took.
	promptSteers []*promptSteer
	// steerSettledTurn is the last turn a steer prompt settled. An earlier
	// prompt of that turn answering late is dropped, not a second completion.
	steerSettledTurn string
	// stragglers counts, per turn that finishAfterStragglers ended, the
	// prompts whose answers have not arrived. Each is dropped when it does,
	// however many turns have settled since.
	stragglers map[string]int
	// stragglerWait, when set by a test, replaces promptStragglerWait.
	stragglerWait time.Duration
	// promptReturned, when set by a test, runs as each of the turn's prompts
	// returns and before its answer is routed, to fix the order in which
	// overlapping answers are handled.
	promptReturned func(seq int)
	// promptSending, when set by a test, runs before a steer prompt checks
	// whether it may still be written, to let Stop overtake it.
	promptSending func(seq int)
	legacyModel   bool
	// The agent lists model among its own config options, as well as the older model list.
	modelConfigOption  bool
	legacyMode         bool
	liveState          *persistenthost.ACPState
	hasDurableTurns    bool
	detaching          bool
	terminalEventID    string
	ignorePromptResult bool
	// onAuthRejected corrects cached auth state when the provider rejects the
	// credential mid-turn. Nil when the binding does not supply one.
	onAuthRejected        func()
	promptResponseFailure func(acpsdk.PromptResponse, string) error
	promptFailureCode     func(error) domain.ConversationFailureCode
	// sessionPromptResponseFailure is promptResponseFailure given the session.
	sessionPromptResponseFailure func(string, acpsdk.PromptResponse, bool) error
	hibernationCheck             func(context.Context, *acpsdk.ClientSideConnection, acpsdk.SessionId) (bool, error)
	backgroundTasks              func(context.Context, *acpsdk.ClientSideConnection, acpsdk.SessionId) ([]ports.ChatBackgroundTask, error)
	stopBackgroundTask           func(context.Context, *acpsdk.ClientSideConnection, acpsdk.SessionId, string) error
	backgroundTaskOutput         func(context.Context, *acpsdk.ClientSideConnection, acpsdk.SessionId, string) (ports.ChatBackgroundTaskOutput, error)
	historyNotice                func(string) bool
	hideCommand                  func(acpsdk.AvailableCommand) bool
	// compactionMessage: see Config.CompactionMessage.
	compactionMessage func(string, map[string]any) (CompactionReport, bool)
	// promptErrorFailure: see Config.PromptErrorFailure.
	promptErrorFailure func(error, bool) error
	// historyUserChunkIsMessage: see Config.HistoryUserChunkIsMessage.
	historyUserChunkIsMessage bool
	// historyReconciler: see Config.HistoryReconciler.
	historyReconciler func([]ports.ChatEvent) []ports.ChatEvent
	// plansSpanTurns: see Config.PlansSpanTurns.
	plansSpanTurns bool
	// toolsOutliveTurn: see Config.ToolsOutliveTurn.
	toolsOutliveTurn bool
	// cycle is work the agent is running on its own between AO's turns; see
	// providerCycle. providerReportsState records that the agent reports its
	// own running and idle states, so its idle, not quiet, ends a cycle.
	cycle                *providerCycle
	providerReportsState bool
	// cycleStateMu orders a cycle's active and idle reports: it is held, before
	// mu, from opening a cycle through reporting it active, and from ending one
	// through reporting it idle, so a cycle that opens as another ends cannot
	// report active before the ended one reports idle.
	cycleStateMu sync.Mutex
	// preamble is standing context attached to the first prompt that actually
	// reaches the provider. It is the delivery channel for agents that accept no
	// system-prompt launch flag and ignore session/new _meta but do execute
	// prompt content blocks. It is consumed in StartDeferredTurn, not SendTurn,
	// so a rejected or discarded turn leaves it armed for the next one.
	preamble      []acpsdk.ContentBlock
	preambleSpent bool
	// standingPreamble is the launch's preamble before setSessionPreamble drops
	// it for a conversation with prior turns. A completed reset turn re-arms it:
	// the provider has just discarded whichever turn carried it.
	standingPreamble []acpsdk.ContentBlock
	// resetsConversation: see Config.ResetsConversation.
	resetsConversation func([]acpsdk.ContentBlock) bool
	// resetTurn is the provider turn id of the reset prompt in flight, if any.
	resetTurn string
	// servedModel reports the model that answered a completed turn; nil when the
	// binding cannot tell. lastServedModel suppresses repeating the same report.
	servedModel     func(ctx context.Context, sessionID, selectedModel string) (string, string)
	lastServedModel string
	// validateModel rejects a model the binding knows the agent cannot use;
	// nil when the binding has no such knowledge.
	validateModel func(ctx context.Context, model string) error
	// turnModel is the model selected when turn turnModelID started. The
	// selection can change while a turn runs, so turn-scoped facts (a version
	// rejection, the model that answered) are attributed to this value instead.
	turnModel   string
	turnModelID string

	contextTokens     int64
	contextWindow     int64
	compactingTurnID  string
	compactionBefore  int64
	compactionSummary string
	compactionFailure string
	// compactionSkipped is set when the agent found nothing to compact on AO's
	// own compaction turn, so the turn settles its row as such.
	compactionSkipped bool
	compactedTurn     string
	// providerCompaction is a compaction the agent started on its own inside a
	// turn, reported through its compaction status; providerCompactions numbers
	// them within providerCompactionTurn so each gets a row of its own.
	providerCompaction     *providerCompaction
	providerCompactions    int
	providerCompactionTurn string
	// settledCompaction is the last one that settled without the agent's own
	// figures, so figures that arrive just after it still reach its row.
	settledCompaction *providerCompaction
	// compactionReport holds the figures the agent gave for AO's own
	// compaction turn, which settleCompaction prefers to the token bracket.
	compactionReport *CompactionReport
	// usageReports counts the agent's token reports, so a finished compaction
	// can tell whether a newer context figure arrived.
	usageReports int
	// contextTurn is the turn the latest context figure was reported in.
	contextTurn string

	claudeRejectedLimits map[string]time.Time

	eventMu      sync.RWMutex
	events       chan ports.ChatEvent
	eventsClosed bool
	closeOnce    sync.Once
	closing      chan struct{}

	historyMu     sync.Mutex
	history       *historyCapture
	historyEvents []ports.ChatEvent
	historyErr    error
	historyLoaded bool
	// replayMu protects only the replay inbox and its transition to live
	// delivery; normalization of replay batches does not hold it.
	replayMu      sync.Mutex
	replaying     bool
	replayUpdates []acpsdk.SessionNotification
}

var _ ports.ChatConversation = (*conversation)(nil)
var _ ports.ChatHistoryReader = (*conversation)(nil)
var _ ports.ChatDeferredTurnStarter = (*conversation)(nil)
var _ ports.ChatConfigOptionController = (*conversation)(nil)
var _ ports.ChatModelVariantResolver = (*conversation)(nil)
var _ ports.ChatSkillLister = (*conversation)(nil)
var _ ports.ChatSteerer = (*conversation)(nil)
var _ ports.ChatInputResponder = (*conversation)(nil)
var _ ports.ChatProviderPreserver = (*conversation)(nil)
var _ ports.ChatProviderTerminator = (*conversation)(nil)
var _ ports.ChatProviderHibernator = (*conversation)(nil)
var _ ports.ChatLiveReconnector = (*conversation)(nil)
var _ ports.ChatLiveReconnectActivator = (*conversation)(nil)
var _ ports.ChatProviderEventAcknowledger = (*conversation)(nil)
var _ ports.ChatCompactor = (*conversation)(nil)
var _ acpsdk.Client = (*conversation)(nil)
var _ acpsdk.ClientExperimental = (*conversation)(nil)
var _ acpsdk.ExtensionMethodHandler = (*conversation)(nil)

func newConversation(
	proc *process,
	log *slog.Logger,
	providerScopeID string,
	extensionFor ClientExtensionHandler,
	extensionAliases map[string]string,
) *conversation {
	reverseAliases := make(map[string]string, len(extensionAliases))
	for method, alias := range extensionAliases {
		reverseAliases[alias] = method
	}
	c := &conversation{
		proc:             proc,
		log:              log,
		providerScopeID:  providerScopeID,
		pending:          make(map[string]*parkedPermission),
		pendingInputs:    make(map[string]*parkedInput),
		accepted:         make(map[string]persistentInteractionCommand),
		capabilities:     make(ports.ChatCapabilities),
		messages:         make(map[string]string),
		thoughts:         make(map[string]string),
		nestedMessages:   make(map[string]nestedMessageState),
		tools:            make(map[string]*toolState),
		events:           make(chan ports.ChatEvent, eventBuffer),
		closing:          make(chan struct{}),
		extensionFor:     extensionFor,
		extensionMethods: reverseAliases,
	}
	legacyWire, sdkWriter, sdkReader := newLegacyACPTransport(proc.stdin)
	c.legacyWire = legacyWire
	c.conn = acpsdk.NewClientSideConnection(
		c, sdkWriter, newExtensionMethodReader(sdkReader, extensionAliases),
	)
	c.conn.SetLogger(log)
	go legacyWire.forward(proc.stdout, c.handleNotification)
	go c.watchConnection()
	return c
}

// providerItemID makes ACP's session-scoped opaque item ids safe to use in
// AO's conversation-wide indexes. ACP only promises ids such as toolCallId are
// unique inside one provider session, while reconstructed branches deliberately
// create new sessions that may reuse those values.
func (c *conversation) providerItemID(id string) string {
	if id == "" || c.providerScopeID == "" {
		return id
	}
	return "acp:" + lengthPrefixedTuple(c.providerScopeID, id)
}

// lengthPrefixedTuple encodes opaque strings injectively. ACP identifiers are
// allowed to contain delimiters (including NUL), and AO provider scopes contain
// colons, so delimiter-joining cannot safely define a durable identity.
func lengthPrefixedTuple(parts ...string) string {
	var encoded strings.Builder
	for _, part := range parts {
		encoded.WriteString(strconv.Itoa(len(part)))
		encoded.WriteByte(':')
		encoded.WriteString(part)
	}
	return encoded.String()
}

func decodeLengthPrefixedTuple(encoded string, count int) ([]string, bool) {
	parts := make([]string, 0, count)
	for range count {
		separator := strings.IndexByte(encoded, ':')
		if separator <= 0 {
			return nil, false
		}
		lengthText := encoded[:separator]
		length, err := strconv.Atoi(lengthText)
		if err != nil || length < 0 || strconv.Itoa(length) != lengthText {
			return nil, false
		}
		encoded = encoded[separator+1:]
		if len(encoded) < length {
			return nil, false
		}
		parts = append(parts, encoded[:length])
		encoded = encoded[length:]
	}
	return parts, encoded == ""
}

func (c *conversation) legacyProviderItemAlias(id string) (string, bool) {
	if c.providerScopeID == "" || !strings.HasPrefix(id, "acp:") {
		return "", false
	}
	parts, ok := decodeLengthPrefixedTuple(strings.TrimPrefix(id, "acp:"), 2)
	if !ok || parts[0] != c.providerScopeID || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func (c *conversation) start(
	sessionID string,
	capabilities ports.ChatCapabilities,
	modeFor func(ports.PermissionMode) string,
	optionsFor func(ports.ChatTurnSettings) []SessionOption,
	permissionFor PermissionPolicy,
	initialPermission ports.PermissionMode,
	validateSettings TurnSettingsValidator,
	configOptions []acpsdk.SessionConfigOption,
	models *legacySessionModelState,
	modes *acpsdk.SessionModeState,
) {
	c.mu.Lock()
	c.sessionID = sessionID
	c.reportedSessionID = sessionID
	c.capabilities = capabilities
	// Preserve config options received via session/update during session/new.
	// An agent may send config_option_update before start() runs; only overwrite
	// the catalog when the response actually carries one, so an early update is
	// not lost to an empty response snapshot.
	if len(configOptions) > 0 || models != nil || modes != nil {
		c.configOptions = normalizeSessionOptions(configOptions, models, modes)
	}
	if len(c.configOptions) > 0 {
		c.capabilities[ports.ChatCapabilityConfigOptions] = true
	}
	if c.skillsKnown {
		c.capabilities[ports.ChatCapabilitySkills] = true
		c.capabilities[ports.ChatCapabilityCompaction] = c.compactAdvertised
	}
	c.modeFor = modeFor
	c.optionsFor = optionsFor
	c.permissionFor = permissionFor
	c.initialPermission = ports.NormalizePermissionMode(initialPermission)
	c.permissionMode = c.initialPermission
	c.validateSettings = validateSettings
	c.legacyModel = models != nil
	c.modelConfigOption = slices.ContainsFunc(configOptions, func(option acpsdk.SessionConfigOption) bool {
		return option.Select != nil && option.Select.Id == "model"
	})
	c.legacyMode = modes != nil
	c.mu.Unlock()
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerReady})
}

func (c *conversation) ProviderConversationID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reportedSessionID
}

func (c *conversation) setReportedProviderConversationID(sessionID string) {
	c.mu.Lock()
	c.reportedSessionID = sessionID
	c.mu.Unlock()
}

// setSessionPreamble arms standing context for the next prompt that actually
// reaches the provider. Any earlier preamble is dropped: the newest launch
// context is the one that should be delivered, which is how a recomputed system
// prompt replaces the original. It is consumed in StartDeferredTurn so a turn
// that never starts leaves it armed.
func (c *conversation) setSessionPreamble(blocks []acpsdk.ContentBlock) {
	// Standing context is only intended to lead the first prompt of a session.
	// A conversation that already has user turns received it then; repeating it
	// mid-conversation would read as a new user message. AO's durable history
	// covers a live reconnect, which replays nothing; session/load replay covers
	// turns made in the terminal.
	standing := blocks
	if c.hasPriorTurns() {
		blocks = nil
	}
	c.mu.Lock()
	c.standingPreamble = standing
	c.preamble = blocks
	c.preambleSpent = len(blocks) == 0
	c.mu.Unlock()
}

func (c *conversation) hasPriorTurns() bool {
	c.mu.Lock()
	durable := c.hasDurableTurns
	c.mu.Unlock()
	return durable || c.nativeHistoryHasUserTurns()
}

func (c *conversation) nativeHistoryHasUserTurns() bool {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	for _, event := range c.historyEvents {
		if event.Kind == ports.ChatEventUserMessageCompleted {
			return true
		}
	}
	return false
}

func (c *conversation) Capabilities() ports.ChatCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneCapabilities(c.capabilities)
}

func (c *conversation) Events() <-chan ports.ChatEvent { return c.events }

// PreservesProviderOnClose reports that Close detaches only when this
// conversation is backed by the daemon-independent host.
func (c *conversation) PreservesProviderOnClose() bool { return c.proc.terminate != nil }

// ReconnectedLive distinguishes the same initialized ACP connection from a
// replacement process recovered with session/load or session/resume.
func (c *conversation) ReconnectedLive() bool { return c.proc.reconnected }

// ActivateLiveReconnect installs the durable turn correlation before releasing
// replayed ACP updates to the SDK. This prevents a replacement daemon from
// attributing output to an empty turn or starting a second root prompt.
func (c *conversation) ActivateLiveReconnect(ctx context.Context, providerTurnID string) error {
	if !c.proc.reconnected || c.liveState == nil || c.proc.gate == nil {
		return nil
	}
	durableBusy := strings.TrimSpace(providerTurnID) != ""
	switch {
	case c.liveState.ActivePrompt && !durableBusy:
		return fmt.Errorf("%w: ACP host has an active prompt but no durable running turn",
			ports.ErrChatRecoveryInconclusive)
	case durableBusy && !c.liveState.ActivePrompt && c.liveState.PendingResultEventID == "":
		return fmt.Errorf("%w: durable turn %q is running but the ACP host is idle",
			ports.ErrChatRecoveryInconclusive, providerTurnID)
	case c.liveState.PendingResultEventID != "" && !durableBusy:
		// The old controller committed the terminal event but died before its ACK.
		// The replay is already queued on this socket, so acknowledge it and ignore
		// that one private completion after opening the reader gate.
		c.mu.Lock()
		c.ignorePromptResult = true
		c.terminalEventID = c.liveState.PendingResultEventID
		c.mu.Unlock()
		if err := c.conn.NotifyExtension(ctx, persistenthost.ACPPromptAckMethod, map[string]string{
			"eventId": c.liveState.PendingResultEventID,
		}); err != nil {
			return fmt.Errorf("acknowledge committed persistent ACP result: %w", err)
		}
	}
	c.mu.Lock()
	if durableBusy {
		c.activeTurn = providerTurnID
		if c.liveState.ActivePrompt && c.liveState.PendingResultEventID == "" && !c.liveState.ActiveCompaction {
			// The host still runs the turn's prompt, so a steer prompt can join
			// it. Its answers reach this attachment as the host's notifications.
			// A compaction takes no steers, as a local Compact does not.
			turnCtx, cancel := context.WithCancel(context.Background())
			c.turnCtx, c.turnCancel = turnCtx, cancel
			c.settlingTurn = ""
			c.resetPromptSteeringLocked()
			c.outstanding = map[int]bool{}
			c.adoptedPrompt = true
		}
		if c.liveState.ActiveCompaction {
			c.compactingTurnID = providerTurnID
			c.compactionBefore = c.contextTokens
		}
		c.messages = make(map[string]string)
		c.steerSegment = 0
		c.thoughts = make(map[string]string)
		c.nestedMessages = make(map[string]nestedMessageState)
		c.tools = make(map[string]*toolState)
		c.followRecordsOnReattachLocked()
	}
	c.mu.Unlock()
	c.pollSubagentRecords()
	c.proc.gate.Open()
	return nil
}

// SendTurn prepares the long-lived ACP prompt request. AO's controller starts it
// through StartDeferredTurn only after the provider turn id is durable.
func (c *conversation) SendTurn(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	if err := ctx.Err(); err != nil {
		return ports.ChatTurnRef{}, err
	}
	prompt, err := c.promptContent(msg)
	if err != nil {
		return ports.ChatTurnRef{}, err
	}
	c.mu.Lock()
	busy := c.closed || c.prepared != nil || c.activeTurn != ""
	c.mu.Unlock()
	if busy {
		return ports.ChatTurnRef{}, errors.New("ACP conversation already has a turn in flight")
	}
	if err := c.checkModelSwitch(ctx, msg.Settings.Model); err != nil {
		return ports.ChatTurnRef{}, c.providerSessionLost(err)
	}
	if err := c.applyTurnSettings(ctx, msg.Settings); err != nil {
		return ports.ChatTurnRef{}, c.providerSessionLost(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ports.ChatTurnRef{}, errConversationClosed
	}
	if c.prepared != nil || c.activeTurn != "" {
		return ports.ChatTurnRef{}, errors.New("ACP conversation already has a turn in flight")
	}
	id := uuid.NewString()
	c.prepared = &preparedTurn{id: id, clientMessageID: msg.ClientMessageID, prompt: prompt}
	return ports.ChatTurnRef{ProviderTurnID: id}, nil
}

func (c *conversation) DiscardDeferredTurn(providerTurnID string) {
	c.mu.Lock()
	if c.prepared != nil && c.prepared.id == providerTurnID {
		c.prepared = nil
	}
	c.mu.Unlock()
}

// checkModelSwitch refuses a switch to a model the binding knows the agent
// cannot use. An acknowledged current model is not rechecked, but a restored
// model may only be stored in AO and must be checked before reapplication.
func (c *conversation) checkModelSwitch(ctx context.Context, model string) error {
	if model == "" {
		return nil
	}
	c.mu.Lock()
	current := c.configOptionCurrentLocked("model")
	_, restored := c.restoredFrom["model"]
	c.mu.Unlock()
	if model == current && !restored {
		return nil
	}
	if c.validateModel != nil {
		if err := c.validateModel(ctx, model); err != nil {
			return fmt.Errorf("%w: %w", ports.ErrChatConfigOptionInvalid, err)
		}
	}
	return c.checkOptionalModelOffered(model)
}

// checkOptionalModelOffered refuses a switch to an optional model the session
// neither offers nor lists under its name. applyTurnSettings skips such a model
// so a session opened on it still starts; for a switch, skipping would keep the
// old model answering while AO shows the new one.
func (c *conversation) checkOptionalModelOffered(model string) error {
	c.mu.Lock()
	optionsFor := c.optionsFor
	legacyModel := c.legacyModel && !c.modelConfigOption
	unofferedModel := c.unofferedModel
	c.mu.Unlock()
	if optionsFor == nil || legacyModel {
		return nil
	}
	// A variant id whose base model the session offers is applied as that base
	// plus its reasoning level, so it is offered too.
	for _, option := range c.expandModelVariant(optionsFor(ports.ChatTurnSettings{Model: model})) {
		if option.ID != "model" || !option.Optional {
			continue
		}
		// The model the session was opened on is not a switch, even though
		// the agent kept its own model in its place.
		if unofferedModel != "" && option.Value == unofferedModel {
			return nil
		}
		offered, known := c.configOptionOffers(option.ID, option.Value)
		if !known || offered || c.configOptionValueNamed(option.ID, option.Name) != "" {
			return nil
		}
		// A new chat on this model would not run it either: the agent keeps its
		// own model there too, so the message points only at the models it lists.
		return fmt.Errorf("%w: this chat can't switch to %q because its agent doesn't offer that model; pick one it lists",
			ports.ErrChatConfigOptionInvalid, model)
	}
	return nil
}

// markSettingsApplied records settings the live session already runs with as
// the catalog's current values, without asking the provider.
//
// A live reconnect rebuilds the catalog from the host's session/new snapshot.
// That snapshot predates initial_settings and every change since, so it names
// the agent's launch defaults as current while the provider process is still
// in the state AO last applied (#6758). The restored settings normally are that
// state; a value the catalog does not offer is left where the snapshot had it,
// as applyTurnSettings would leave it.
//
// They can also be a choice stored after the last turn and never sent: the
// provider only receives settings with a turn. So nothing marked here counts
// as acknowledged, and the next turn sends every setting again.
func (c *conversation) markSettingsApplied(settings ports.ChatTurnSettings) {
	c.mu.Lock()
	modeFor, optionsFor := c.modeFor, c.optionsFor
	legacyModel := c.legacyModel && !c.modelConfigOption
	c.reapplySettings = true
	c.mu.Unlock()
	if modeFor != nil && settings.Approval != "" {
		if mode := modeFor(settings.Approval); mode != "" {
			if offered, known := c.configOptionOffers("mode", mode); known && offered {
				c.markRestored("mode", mode)
			}
		}
	}
	if optionsFor == nil {
		return
	}
	options := optionsFor(settings)
	if !legacyModel {
		options = c.expandModelVariant(options)
	}
	for _, option := range options {
		value := option.Value
		offered, known := c.configOptionOffers(option.ID, value)
		if known && !offered && option.Name != "" {
			if named := c.configOptionValueNamed(option.ID, option.Name); named != "" {
				value = named
				offered, known = c.configOptionOffers(option.ID, value)
			}
		}
		if known && offered {
			c.markRestored(option.ID, value)
		}
	}
}

// expandModelVariant maps a composite model onto the base model and separate
// variant option when that is how the live catalog offers it.
func (c *conversation) expandModelVariant(options []SessionOption) []SessionOption {
	// Agy also restores the separate effort saved after an opening fallback.
	// Its two servers use different IDs for the same thought-level control.
	if c.harness == domain.HarnessAgy {
		for i, option := range options {
			if option.ID == "thinking_level" {
				options[i] = c.variantOptionFor(option)
			}
		}
	}
	if c.splitModelVariant == nil {
		return options
	}
	for i, option := range options {
		if option.ID != "model" || option.Value == "" {
			continue
		}
		if offered, known := c.configOptionOffers("model", option.Value); !known || offered {
			break
		}
		if base, variant, split := c.splitModelVariant(option.Value); split {
			if offered, _ := c.configOptionOffers("model", base); offered {
				options[i].Value = base
				options[i].Name = ""
				options = append(options, c.variantOptionFor(variant))
			}
		}
		break
	}
	return options
}

// ModelVariantBase is the base model a composite model id runs as, when the
// live catalog offers that base instead of the id itself, as
// expandModelVariant applies it.
func (c *conversation) ModelVariantBase(model string) (string, bool) {
	if c.splitModelVariant == nil || model == "" {
		return "", false
	}
	if offered, known := c.configOptionOffers("model", model); !known || offered {
		return "", false
	}
	base, _, split := c.splitModelVariant(model)
	if !split {
		return "", false
	}
	if offered, _ := c.configOptionOffers("model", base); !offered {
		return "", false
	}
	return base, true
}

// markRestored names value current for option id and remembers the snapshot's
// value, so a provider that rejects the restored value gets it back.
func (c *conversation) markRestored(id, value string) {
	previous := c.configOptionCurrent(id)
	if previous == value {
		return
	}
	c.applyAcceptedConfigOption(id, ports.ChatConfigOptionValue{Select: value})
	c.mu.Lock()
	if c.restoredFrom == nil {
		c.restoredFrom = make(map[string]string)
	}
	c.restoredFrom[id] = previous
	c.mu.Unlock()
}

// revertRestored puts back the snapshot's value of an option whose restored
// value the provider has just rejected. The catalog then no longer claims the
// restored value, so a later turn asking for it sends it again.
func (c *conversation) revertRestored(id string) {
	c.mu.Lock()
	previous, ok := c.restoredFrom[id]
	c.mu.Unlock()
	if ok {
		c.applyAcceptedConfigOption(id, ports.ChatConfigOptionValue{Select: previous})
	}
}

func (c *conversation) applyTurnSettings(ctx context.Context, settings ports.ChatTurnSettings) error {
	c.mu.Lock()
	sessionID := c.sessionID
	modeFor := c.modeFor
	optionsFor := c.optionsFor
	initialPermission := c.initialPermission
	validateSettings := c.validateSettings
	// Prefer the modern selector when an agent (such as Grok) also advertises
	// legacy models, matching SetConfigOption and preserving rebuilt catalogs.
	legacyModel := c.legacyModel && !c.modelConfigOption
	legacyMode := c.legacyMode
	configOptions := cloneConfigOptions(c.configOptions)
	reapply := c.reapplySettings
	c.mu.Unlock()
	if sessionID == "" {
		return errors.New("ACP session is not open")
	}
	if validateSettings != nil {
		if err := validateSettings(initialPermission, settings); err != nil {
			return err
		}
	}
	var options []SessionOption
	if optionsFor != nil {
		options = optionsFor(settings)
	}
	// A setting the session already has is skipped (some agents take seconds
	// per change) unless session/set_model made the list stale, or a live
	// reconnect named current values the provider never acknowledged.
	catalogStale := reapply
	if legacyModel && settings.Model != "" {
		// A binding may name the model differently from the turn settings.
		model := settings.Model
		modelOptional := false
		for _, option := range options {
			if option.ID == "model" && option.Value != "" {
				model = option.Value
				modelOptional = option.Optional
			}
		}
		modelOptionFound, modelSkipped := false, false
		for _, option := range configOptions {
			if option.ID != "model" {
				continue
			}
			modelOptionFound = true
			resolved, ok := resolveModelChoice(c.harness, option.Choices, model)
			if !ok && c.splitModelVariant != nil {
				if base, variant, split := c.splitModelVariant(model); split {
					if resolved, ok = resolveModelChoice(c.harness, option.Choices, base); ok {
						options = append(options, c.variantOptionFor(variant))
					}
				}
			}
			if !ok && !modelOptional {
				return fmt.Errorf("%w: ACP session model does not offer %q", ports.ErrChatConfigOptionInvalid, model)
			}
			model, modelSkipped = resolved, !ok
			break
		}
		if !modelOptionFound {
			return fmt.Errorf("%w: ACP session does not advertise a model option", ports.ErrChatConfigOptionInvalid)
		}
		switch {
		case modelSkipped:
			c.log.Warn("ACP session does not offer the chosen model; keeping its default", "model", settings.Model)
		case !catalogStale && c.configOptionCurrent("model") == model:
			c.applyAcceptedConfigOption("model", ports.ChatConfigOptionValue{Select: model})
		default:
			if err := c.legacyWire.setModel(ctx, sessionID, model); err != nil {
				if isACPMethodNotFound(err) {
					return fmt.Errorf("%w: session/set_model %q", ErrACPSetterUnsupported, model)
				}
				return fmt.Errorf("set ACP session model %q: %w", model, err)
			}
			catalogStale = true
			c.applyAcceptedConfigOption("model", ports.ChatConfigOptionValue{Select: model})
		}
	}
	if !legacyModel {
		options = c.expandModelVariant(options)
	}
	applyOption := func(option SessionOption) error {
		if option.ID == "" || option.Value == "" {
			return nil
		}
		if (option.ID == "model" && legacyModel) || (option.ID == "mode" && legacyMode) {
			return nil
		}
		if offered, known := c.configOptionOffers(option.ID, option.Value); known && !offered && option.Name != "" {
			if value := c.configOptionValueNamed(option.ID, option.Name); value != "" {
				option.Value = value
			}
		}
		if !catalogStale && c.configOptionCurrent(option.ID) == option.Value {
			if option.ID == "model" {
				// The agent's own model was chosen: the session no longer runs on
				// the unoffered model it opened on.
				c.mu.Lock()
				c.unofferedModel = ""
				c.mu.Unlock()
			}
			return nil
		}
		// The models offered do not depend on the model chosen, so this is final.
		if offered, known := c.configOptionOffers(option.ID, option.Value); option.Optional && option.ID == "model" && known && !offered {
			c.mu.Lock()
			c.unofferedModel = option.Value
			c.mu.Unlock()
			return nil
		}
		if err := c.setSessionOption(ctx, sessionID, option); err != nil {
			return err
		}
		if option.ID == "model" {
			c.mu.Lock()
			c.unofferedModel = ""
			c.mu.Unlock()
		}
		return nil
	}
	// A model switch may change the modes an ACP agent offers. Apply a modern
	// model config option before session/set_mode so the requested mode is
	// validated against the selected model rather than the session's default.
	for _, option := range options {
		if option.ID == "model" {
			if err := applyOption(option); err != nil {
				return err
			}
		}
	}
	effectiveApproval := settings.Approval
	if modeFor != nil {
		if mode := modeFor(settings.Approval); mode != "" {
			// Some ACP agents rebuild their permission modes after a model switch.
			// Claude, for example, removes Auto for Haiku and clamps itself back to
			// Default. Respect that authoritative catalog instead of immediately
			// reapplying AO's default Auto choice and aborting session creation.
			if ports.NormalizePermissionMode(settings.Approval) == ports.PermissionModeAuto {
				if offered, known := c.configOptionOffers("mode", mode); known && !offered {
					if fallbackOffered, _ := c.configOptionOffers("mode", "default"); fallbackOffered {
						mode = "default"
						effectiveApproval = ports.PermissionModeDefault
					}
				}
			}
			if _, err := c.conn.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{
				SessionId: acpsdk.SessionId(sessionID), ModeId: acpsdk.SessionModeId(mode),
			}); err != nil {
				if isACPMethodNotFound(err) {
					return fmt.Errorf("%w: session/set_mode %q", ErrACPSetterUnsupported, mode)
				}
				return fmt.Errorf("set ACP session mode %q: %w", mode, err)
			}
		}
	}
	for _, option := range options {
		if option.ID != "model" {
			if err := applyOption(option); err != nil {
				return err
			}
		}
	}
	c.mu.Lock()
	if settings.Approval != "" {
		c.permissionMode = ports.NormalizePermissionMode(effectiveApproval)
	}
	if reapply {
		c.reapplySettings = false
	}
	c.mu.Unlock()
	return nil
}

// applySessionOptions sets config options that are fixed for the whole session
// rather than chosen per turn. See Config.LaunchSessionOptions.
func (c *conversation) applySessionOptions(ctx context.Context, options []SessionOption) error {
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	if sessionID == "" {
		return errors.New("ACP session is not open")
	}
	for _, option := range options {
		if err := c.setSessionOption(ctx, sessionID, option); err != nil {
			return err
		}
	}
	return nil
}

// setSessionOption sends one session/set_config_option and adopts any catalog
// the agent returns, preserving the current catalog when it omits one.
func (c *conversation) setSessionOption(ctx context.Context, sessionID string, option SessionOption) error {
	if option.ID == "" || option.Value == "" {
		return nil
	}
	c.mu.Lock()
	requireAdvertised := c.requireAdvertised
	c.mu.Unlock()
	if requireAdvertised {
		// An agent that acknowledges an option it never offered may simply drop
		// it, which would leave the user believing a choice took effect.
		offered, known := c.configOptionOffers(option.ID, option.Value)
		if !known {
			return fmt.Errorf("%w: ACP session does not advertise option %q",
				ports.ErrChatConfigOptionInvalid, option.ID)
		}
		if !offered {
			return fmt.Errorf("%w: ACP session option %q does not offer %q",
				ports.ErrChatConfigOptionInvalid, option.ID, option.Value)
		}
	}
	resp, err := c.conn.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: acpsdk.SessionId(sessionID), ConfigId: acpsdk.SessionConfigId(option.ID),
			Value: acpsdk.SessionConfigValueId(option.Value),
		},
	})
	if err != nil {
		// An optional setting the chosen model lacks is skipped; session/set_model
		// does not return the new model's options to check in advance.
		if option.Optional && isACPInvalidParams(err) {
			c.revertRestored(option.ID)
			return nil
		}
		if isACPMethodNotFound(err) {
			return fmt.Errorf("%w: session/set_config_option %q", ErrACPSetterUnsupported, option.ID)
		}
		return fmt.Errorf("set ACP session option %q: %w", option.ID, err)
	}
	if len(resp.ConfigOptions) == 0 {
		c.applyAcceptedConfigOption(option.ID, ports.ChatConfigOptionValue{Select: option.Value})
	} else {
		c.replaceConfigOptions(resp.ConfigOptions)
	}
	return nil
}

func (c *conversation) StartDeferredTurn(providerTurnID string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errConversationClosed
	}
	if c.prepared == nil || c.prepared.id != providerTurnID {
		c.mu.Unlock()
		return errors.New("ACP prepared turn not found")
	}
	turn := *c.prepared
	c.prepared = nil
	// Standing context leads the first prompt so the provider reads it as
	// context rather than as part of what the user asked. It is consumed here,
	// not in SendTurn, because this is the first point at which a prompt is
	// guaranteed to reach the provider: settings validation, a busy conversation
	// and a discarded prepared turn all fail in SendTurn, and spending the
	// preamble on any of them would silently drop AO's instructions from the
	// session's first real turn. A reset prompt never carries it: the agent
	// recognizes the command only when the prompt is the command alone, and the
	// preamble is re-armed for the prompt after it (see finishPrompt).
	if c.resetsConversation != nil && c.resetsConversation(turn.prompt) {
		c.resetTurn = turn.id
	} else if !c.preambleSpent && len(c.preamble) > 0 {
		turn.prompt = append(append([]acpsdk.ContentBlock(nil), c.preamble...), turn.prompt...)
		c.preambleSpent = true
	}
	// A cycle the agent was still running on its own ends here: what it wrote
	// so far stays its own, and the turn's output starts fresh items.
	turn.endedCycle = c.detachProviderCycleLocked()
	c.activeTurn = turn.id
	c.turnModelID = turn.id
	c.turnModel = c.configOptionCurrentLocked("model")
	c.settlingTurn = ""
	turnCtx, cancel := context.WithCancel(context.Background())
	c.turnCancel = cancel
	c.turnCtx = turnCtx
	c.resetPromptSteeringLocked()
	c.outstanding = map[int]bool{0: true}
	sessionID := c.sessionID
	c.messages = make(map[string]string)
	c.steerSegment = 0
	c.thoughts = make(map[string]string)
	c.nestedMessages = make(map[string]nestedMessageState)
	c.tools = make(map[string]*toolState)
	c.turnDiffs = nil
	c.turnDiffTurnID = ""
	c.providerFailure = nil
	c.mu.Unlock()

	// The ended cycle settles in runTurn: its settle can wait on an update
	// still being emitted, and this caller may be the one that drains events.
	go c.runTurn(turnCtx, sessionID, turn)
	return nil
}

func (c *conversation) runTurn(ctx context.Context, sessionID string, turn preparedTurn) {
	c.settleEndedCycle(turn.endedCycle)
	c.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.id})
	// A subagent an earlier turn launched can take more work in this one.
	c.pollSubagentRecords()
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerBusy})
	// ACP message ids are opaque idempotency/correlation keys. Preserve AO's
	// durable client id when possible so an agent that echoes it from session/load
	// can be reconciled without provider-specific knowledge. Some agents (notably
	// claude-agent-acp) assign their own persisted user uuid; the service's history
	// reconciliation is the fallback for those conforming-but-different agents.
	messageID := strings.TrimSpace(turn.clientMessageID)
	if messageID == "" {
		messageID = uuid.NewString()
	}
	resp, err := c.conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId(sessionID),
		MessageId: &messageID,
		Prompt:    turn.prompt,
	})

	if c.promptReturned != nil {
		c.promptReturned(0)
	}
	c.finishTurnPrompt(turn.id, 0, resp, err)
}

// reportServedModel tells the client which model actually answered when the
// binding can see it and it differs from the selection, such as an alias that
// the installed CLI resolves to an older concrete model. Each distinct
// (selection, served) pair is reported once rather than on every turn.
// selected is the model the turn started with, not the current selection.
func (c *conversation) reportServedModel(turnID, selected string) {
	if c.servedModel == nil {
		return
	}
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	if selected == "" || sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	served, reason := c.servedModel(ctx, sessionID, selected)
	if served == "" || served == selected {
		return
	}
	key := selected + "\x00" + served
	c.mu.Lock()
	if c.lastServedModel == key {
		c.mu.Unlock()
		return
	}
	c.lastServedModel = key
	c.mu.Unlock()
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventModelRerouted, ProviderTurnID: turnID,
		Reroute: &ports.ChatModelReroute{FromModel: selected, ToModel: served, Reason: reason},
	})
}

func (c *conversation) finishPrompt(
	turnID string,
	resp acpsdk.PromptResponse,
	err error,
) {
	eventID := hostResultEventID(resp, err)
	// A transport error may win the race with watchConnection. The host still
	// owns this prompt; its journal, not an SDK disconnect, supplies the outcome.
	if err != nil && eventID == "" && c.providerDetaching() {
		return
	}
	c.mu.Lock()
	if c.detaching {
		c.mu.Unlock()
		return
	}
	c.settlingTurn = turnID
	interrupt := c.interrupt
	isCompaction := c.compactingTurnID != "" && c.compactingTurnID == turnID
	compactionFailure := c.compactionFailure
	answered := len(c.messages) > 0 || len(c.tools) > 0
	// "" when the turn started before this controller (a reattached durable
	// turn): its model is unknown, and the current selection may not be it.
	turnModel := ""
	if c.turnModelID == turnID {
		turnModel = c.turnModel
	}
	c.mu.Unlock()
	interruptRequested := interrupt != nil && interrupt.turnID == turnID
	// The children's last steps and reports belong to this turn: file them
	// before it settles.
	c.drainSubagentRecords()
	c.settleOpenItems(turnID, c.keepToolsOutlivingTurn(interruptRequested, resp, err))
	interruptedLocally := false
	if interrupt != nil && interrupt.turnID == turnID {
		// ACP cancellation and Prompt completion can race. Wait for the sender's
		// definitive result before classifying the turn so a failed notification
		// cannot look interrupted and an accepted one cannot look failed.
		<-interrupt.done
		interruptedLocally = interrupt.err == nil
	}
	var state domain.TurnState
	var turnErr error
	var failureCode domain.ConversationFailureCode
	sessionLost := false
	if err != nil {
		if isACPSessionMissing(err) {
			// The prompt never reached a session. Fail this turn with the cause,
			// then stop the provider below instead of announcing readiness. This
			// is checked before interruption: an accepted session/cancel does not
			// bring the session back, and reading the turn as interrupted would
			// leave the dead provider attached.
			state = domain.TurnStateFailed
			turnErr = c.markSessionLost(fmt.Errorf("ACP session/prompt: %w", err))
			sessionLost = true
		} else if interruptedLocally || errors.Is(err, context.Canceled) {
			state = domain.TurnStateInterrupted
		} else {
			state = domain.TurnStateFailed
			turnErr = normalizeACPError("ACP session/prompt", err)
			if c.promptErrorFailure != nil && !errors.Is(turnErr, ports.ErrChatAuthRequired) && !errors.Is(turnErr, ports.ErrChatPromptStalled) {
				if failure := c.promptErrorFailure(err, answered); failure != nil {
					turnErr = failure
				}
			}
			if c.promptFailureCode != nil {
				failureCode = c.promptFailureCode(err)
			}
		}
	} else {
		state = turnState(resp.StopReason)
		if c.promptResponseFailure != nil && state != domain.TurnStateInterrupted && !interruptedLocally {
			if failure := c.promptResponseFailure(resp, turnModel); failure != nil {
				state = domain.TurnStateFailed
				turnErr = failure
			}
		}
		if c.sessionPromptResponseFailure != nil && turnErr == nil && state != domain.TurnStateInterrupted && !interruptedLocally {
			c.mu.Lock()
			sessionID := c.sessionID
			c.mu.Unlock()
			if failure := c.sessionPromptResponseFailure(sessionID, resp, answered); failure != nil {
				state = domain.TurnStateFailed
				turnErr = failure
			}
		}
		if isCompaction && compactionFailure != "" && turnErr == nil && state == domain.TurnStateCompleted && !interruptedLocally {
			state = domain.TurnStateFailed
			turnErr = errors.New(compactionFailure)
		}
		if resp.Usage != nil {
			cached := 0
			if resp.Usage.CachedReadTokens != nil {
				cached += *resp.Usage.CachedReadTokens
			}
			if resp.Usage.CachedWriteTokens != nil {
				cached += *resp.Usage.CachedWriteTokens
			}
			c.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
				InputTokens: int64(resp.Usage.InputTokens), OutputTokens: int64(resp.Usage.OutputTokens),
				CachedTokens: int64(cached), TotalTokens: int64(resp.Usage.TotalTokens),
				TotalsKnown: true,
			}})
		}
	}
	var usageLimitResetsAt time.Time
	if turnErr != nil && domain.ParseConversationUsageLimit(turnErr.Error(), nil) != nil {
		usageLimitResetsAt, _ = c.claudeUsageLimitReset(time.Now())
	}
	if errors.Is(turnErr, ports.ErrChatAuthRequired) {
		// The provider has just contradicted whatever the readiness cache holds.
		// Correct it for both request errors and structured prompt failures.
		if c.onAuthRejected != nil {
			c.onAuthRejected()
		}
	} else if turnErr == nil && state == domain.TurnStateCompleted {
		// A successful provider turn is affirmative recovery evidence, including
		// after a daemon restart where this conversation has no in-memory memory
		// of the earlier rejection. The projector ignores this when no durable
		// reauthentication demand exists.
		c.emit(ports.ChatEvent{Kind: ports.ChatEventAccountChanged, Account: &ports.ChatAccount{ReauthRecovered: true}})
		c.reportServedModel(turnID, turnModel)
	}
	c.mu.Lock()
	if c.resetTurn == turnID {
		c.resetTurn = ""
		// Only a completed reset cleared the provider's history. A failed or
		// interrupted one left the turn that carried the preamble in place.
		if turnErr == nil && state == domain.TurnStateCompleted && len(c.standingPreamble) > 0 {
			c.preamble = c.standingPreamble
			c.preambleSpent = false
		}
	}
	if c.settledCompaction != nil && c.settledCompaction.turnID == turnID {
		c.settledCompaction = nil
	}
	settleFinished := false
	var unreported *providerCompaction
	if c.providerCompaction != nil && c.providerCompaction.turnID == turnID {
		if c.providerCompaction.finished {
			settleFinished = true
		} else {
			unreported = c.providerCompaction
			c.providerCompaction = nil
		}
	}
	c.mu.Unlock()
	if settleFinished {
		c.settleFinishedCompaction(c.emit)
	}
	if unreported != nil && turnErr == nil && state == domain.TurnStateCompleted {
		// A compaction still open when its turn ends never reported how it went.
		// A stopped or failed turn settles its row as such; one that ended
		// normally would settle it as a finished compaction, so it says so here.
		c.settleUnreportedCompaction(unreported, c.emit)
	}
	if isCompaction {
		if state == domain.TurnStateCompleted {
			c.settleCompaction(turnID)
		} else {
			c.mu.Lock()
			c.compactingTurnID = ""
			c.compactionBefore = 0
			c.compactionSummary = ""
			c.compactionFailure = ""
			c.compactionSkipped = false
			c.compactionReport = nil
			c.mu.Unlock()
			switch {
			case turnErr == nil:
				// Stopped: settling the turn settles the running row with it.
			case errors.Is(turnErr, ports.ErrChatAuthRequired):
				// Needs its own row: the account, not the compaction, has to be fixed.
				c.emit(ports.ChatEvent{Kind: ports.ChatEventError, ProviderTurnID: turnID, Err: turnErr})
			default:
				c.emit(ports.ChatEvent{
					Kind: ports.ChatEventCompacted, ProviderTurnID: turnID,
					ProviderItemID: c.compactionItemID(turnID),
					ActivityStatus: domain.ActivityStatusFailed, Summary: turnErr.Error(),
				})
			}
		}
	}
	// Only a durable host receipt replaces the outstanding one. A host-local
	// rejection carries no event ID; erasing the receipt here would make the
	// controller's later ACK of that receipt a no-op and leave the host refusing
	// every prompt until restart.
	if eventID != "" {
		c.mu.Lock()
		c.terminalEventID = eventID
		c.mu.Unlock()
	}
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderEventID: eventID,
		ProviderTurnID: turnID, TurnState: state, FailureCode: failureCode, Err: turnErr,
		UsageLimitResetsAt: usageLimitResetsAt,
	})
	// Release the turn before announcing readiness, not after. A consumer that
	// reacts to ChatControllerReady by sending the next turn — AO's own queue
	// drain does exactly that — would otherwise race this unlock and be refused
	// with "already has a turn in flight", depending purely on which goroutine
	// the scheduler picks. Ready has to mean ready.
	c.mu.Lock()
	if c.activeTurn == turnID {
		c.activeTurn = ""
		c.settlingTurn = ""
		c.turnCancel = nil
		c.turnCtx = nil
		c.resetPromptSteeringLocked()
		c.providerFailure = nil
		if c.interrupt == interrupt {
			c.interrupt = nil
		}
		c.releasePendingSteersLocked()
	}
	c.mu.Unlock()
	if sessionLost {
		// Ready would let the queue drain into a provider that cannot serve it.
		// Stopping ends the stream after the failed turn above, so the controller
		// settles it and reports the session exited. The failed turn is already
		// on the stream, so a failed stop can only be logged.
		if stopErr := c.stopLostProvider(); stopErr != nil {
			c.log.Error("chat: failed to stop ACP provider after its session was lost",
				"turn", turnID, "error", stopErr)
		}
		return
	}
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerReady})
}

func (c *conversation) Compact(ctx context.Context) (ports.ChatCompactionResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.ChatCompactionResult{}, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ports.ChatCompactionResult{}, errConversationClosed
	}
	if !c.capabilities.Has(ports.ChatCapabilityCompaction) {
		c.mu.Unlock()
		return ports.ChatCompactionResult{}, errors.New("chat driver cannot compact history")
	}
	if c.prepared != nil || c.activeTurn != "" || c.compactingTurnID != "" {
		c.mu.Unlock()
		return ports.ChatCompactionResult{}, errors.New("ACP conversation already has a turn in flight")
	}
	sessionID := c.sessionID
	if sessionID == "" {
		c.mu.Unlock()
		return ports.ChatCompactionResult{}, errors.New("ACP session is not open")
	}
	before := c.contextTokens
	window := c.contextWindow
	id := uuid.NewString()
	cycle := c.detachProviderCycleLocked()
	c.activeTurn = id
	c.turnModelID = id
	c.turnModel = c.configOptionCurrentLocked("model")
	c.compactingTurnID = id
	c.compactionBefore = before
	c.compactionSummary = ""
	c.compactionFailure = ""
	c.compactionSkipped = false
	c.settlingTurn = ""
	turnCtx, cancel := context.WithCancel(context.Background())
	c.turnCancel = cancel
	c.messages = make(map[string]string)
	c.steerSegment = 0
	c.thoughts = make(map[string]string)
	c.nestedMessages = make(map[string]nestedMessageState)
	c.tools = make(map[string]*toolState)
	c.turnDiffs = nil
	c.turnDiffTurnID = ""
	c.providerFailure = nil
	c.mu.Unlock()

	start := func() {
		c.settleEndedCycle(cycle)
		c.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: id})
		c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerBusy})
		// The row says the compaction is under way; settling the turn settles
		// the same row with what was reclaimed, or why it could not be.
		c.emit(ports.ChatEvent{
			Kind: ports.ChatEventCompacted, ProviderTurnID: id, ProviderItemID: c.compactionItemID(id),
			ActivityStatus: domain.ActivityStatusRunning, Detail: compactionFigures(before, 0, window),
		})
	}
	if cycle == nil {
		start()
		go c.runCompactionTurn(turnCtx, sessionID, id)
	} else {
		// The ended cycle settles off this call, as in runTurn: its settle can
		// wait on an update blocked emitting, and the caller may hold a lock the
		// event drain needs. Its items still settle before the turn starts.
		go func() {
			start()
			c.runCompactionTurn(turnCtx, sessionID, id)
		}()
	}
	return ports.ChatCompactionResult{TokensBefore: before}, nil
}

func (c *conversation) runCompactionTurn(ctx context.Context, sessionID, turnID string) {
	messageID := uuid.NewString()
	prompt := []acpsdk.ContentBlock{
		acpsdk.TextBlock("/compact"),
	}
	resp, err := c.conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId(sessionID),
		MessageId: &messageID,
		Prompt:    prompt,
	})

	c.finishPrompt(turnID, resp, err)
}

func (c *conversation) settleCompaction(turnID string) {
	c.mu.Lock()
	before := c.compactionBefore
	after := c.contextTokens
	window := c.contextWindow
	summary := c.compactionSummary
	reported := c.compactionReport
	skipped := c.compactionSkipped && reported == nil
	c.compactingTurnID = ""
	c.compactionBefore = 0
	c.compactionSummary = ""
	c.compactionFailure = ""
	c.compactionSkipped = false
	c.compactionReport = nil
	if !skipped {
		c.compactedTurn = turnID
	}
	if reported != nil {
		// The agent's own figures, when it gave them, beat the bracket.
		after = reported.TokensAfter
		if reported.TokensBefore > 0 {
			before = reported.TokensBefore
		}
	}
	c.mu.Unlock()

	if skipped {
		// The agent found nothing to compact. The history did not change, so the
		// row says so rather than claim a compaction, as one the agent ran on its
		// own does.
		c.emit(ports.ChatEvent{
			Kind: ports.ChatEventCompacted, ProviderTurnID: turnID, ProviderItemID: c.compactionItemID(turnID),
			ActivityStatus: domain.ActivityStatusFailed, Summary: "Nothing to compact",
			Detail: compactionFigures(before, 0, window),
		})
		return
	}
	if before > after && after > 0 {
		summary = compactionSummary(before, after)
	} else if summary == "" {
		summary = "Compacted the conversation history"
	}

	c.emit(ports.ChatEvent{
		Kind:           ports.ChatEventCompacted,
		ProviderTurnID: turnID,
		ProviderItemID: c.compactionItemID(turnID),
		Summary:        summary,
		Detail:         compactionFigures(before, after, window),
	})
}

// compactionItemID names the row of AO's own compaction turn. It is derived
// from the turn so a controller that reattaches to a running compaction
// settles the row the first one opened.
func (c *conversation) compactionItemID(turnID string) string {
	return c.providerItemID("compaction:" + turnID)
}

// providerCompaction is a compaction the agent started inside a turn.
type providerCompaction struct {
	turnID string
	itemID string
	before int64
	// measured is set when before was reported inside this turn. Only such a
	// figure decides when the compaction settles: an attachment that reconnects
	// to the turn gets the turn's updates replayed, not those of earlier turns,
	// and must settle it on the same update, so the events it emits line up.
	measured bool
	// finished is set when the agent said it finished without saying what it
	// freed. Claude reports its result just before the boundary that carries
	// its figures. The row settles on those figures, or on the next token
	// report, the agent's next output or the turn's end, whichever comes first.
	finished bool
	// usageAtFinish is usageReports when it finished.
	usageAtFinish int
}

// CompactionReport is one step of a compaction the agent reported.
type CompactionReport struct {
	// Status is "started", "completed" or "failed", or "skipped" when the
	// agent found nothing to compact: a row already under way says so, and
	// with none under way there is nothing to report.
	Status string
	// TokensBefore and TokensAfter are the agent's own figures, when it gives
	// them. Without them the row brackets the compaction from the token reports
	// either side of it.
	TokensBefore int64
	TokensAfter  int64
	// Reason is why a failed compaction gave up.
	Reason string
}

// trackCompactionStatus follows the compaction status an agent reports in
// `_meta`: OpenCode as "opencode/compaction", Claude (patched by AO's ACP
// runtime build) as "ao/compaction", both {status, error: {message}}. Claude's
// also sends a second "completed" with tokensBefore/tokensAfter, its own
// figures, from the compaction's boundary.
func (c *conversation) trackCompactionStatus(turnID string, meta map[string]any, emit func(ports.ChatEvent)) {
	compaction := compactionStatusMeta(meta)
	if compaction == nil {
		return
	}
	report := CompactionReport{}
	if before, ok := compaction["tokensBefore"].(float64); ok && before > 0 {
		report.TokensBefore = int64(before)
	}
	if after, ok := compaction["tokensAfter"].(float64); ok && after > 0 {
		report.TokensAfter = int64(after)
	}
	switch status, _ := compaction["status"].(string); status {
	case "started", "completed":
		report.Status = status
	case "failed":
		report.Status = status
		report.Reason, _ = nestedMap(compaction, "error")["message"].(string)
	default:
		// Only the three statuses above are sent. Leave the recorded outcome
		// alone rather than guess what a new one means.
		return
	}
	c.reportCompaction(turnID, report, emit)
}

// compactionStatusMeta is the compaction status an update's `_meta` carries,
// or nil.
func compactionStatusMeta(meta map[string]any) map[string]any {
	if compaction := nestedMap(meta, "ao/compaction"); compaction != nil {
		return compaction
	}
	return nestedMap(meta, "opencode/compaction")
}

// reportCompaction applies one step of a compaction the agent reported.
//
// On AO's own compaction turn it only records a failure, or that there was
// nothing to compact: OpenCode ends a failed compaction with a normal end_turn,
// so the status is the only sign that the history was left as it was. Inside
// any other turn the agent compacted on its own, and the steps drive a row that
// runs and then settles in place.
func (c *conversation) reportCompaction(turnID string, report CompactionReport, emit func(ports.ChatEvent)) bool {
	failure := ""
	if report.Status == "failed" {
		failure = compactionFailureText(report.Reason)
	}
	hasFigures := report.Status == "completed" && report.TokensAfter > 0
	replaying := c.historyReplayActive()
	c.mu.Lock()
	if c.compactingTurnID == turnID {
		c.compactionFailure = failure
		// The latest step decides: a start after "nothing to compact" is a new try.
		c.compactionSkipped = report.Status == "skipped"
		if hasFigures {
			c.compactionReport = &report
		}
		c.mu.Unlock()
		return true
	}
	if replaying {
		// The live run already stored this compaction's row.
		c.mu.Unlock()
		return true
	}
	open := c.providerCompaction
	if report.Status == "failed" && open == nil && strings.HasPrefix(turnID, providerCyclePrefix) {
		// A failure between prompts with nothing open is the late report of a
		// compaction whose turn already ended, as when Stop cancels AO's own
		// /compact: that turn's row already says so.
		c.mu.Unlock()
		return true
	}
	if open != nil && open.turnID != turnID {
		open = nil
	}
	switch {
	case report.Status == "started":
		if open != nil && open.finished {
			// A new compaction starts while the finished one still waits to hear
			// what it freed. That one settles now, and this one gets its own row.
			c.mu.Unlock()
			c.settleFinishedCompaction(emit)
			return c.reportCompaction(turnID, report, emit)
		}
		if open != nil {
			// Claude repeats the status while it works.
			c.mu.Unlock()
			return true
		}
		open = &providerCompaction{
			turnID:   turnID,
			itemID:   c.nextProviderCompactionIDLocked(turnID),
			before:   c.contextTokens,
			measured: c.contextTurn == turnID && c.contextTokens > 0,
		}
		if report.TokensBefore > 0 {
			open.before = report.TokensBefore
			open.measured = true
		}
		c.providerCompaction = open
		c.settledCompaction = nil
		window := c.contextWindow
		c.mu.Unlock()
		emit(ports.ChatEvent{
			Kind: ports.ChatEventCompacted, ProviderTurnID: compactionRowTurn(turnID), ProviderItemID: open.itemID,
			ActivityStatus: domain.ActivityStatusRunning, Detail: compactionFigures(open.before, 0, window),
		})
		return true
	case report.Status == "skipped":
		if open == nil {
			// Nothing was under way, so there is nothing to report: the
			// agent's own words stay in the reply.
			c.mu.Unlock()
			return false
		}
		// The agent started, then found nothing to compact. The history did
		// not change, so the row says so instead of claiming a compaction.
		c.providerCompaction = nil
		c.settledCompaction = nil
		window := c.contextWindow
		c.mu.Unlock()
		emit(ports.ChatEvent{
			Kind: ports.ChatEventCompacted, ProviderTurnID: compactionRowTurn(turnID), ProviderItemID: open.itemID,
			ActivityStatus: domain.ActivityStatusFailed, Summary: "Nothing to compact",
			Detail: compactionFigures(open.before, 0, window),
		})
		return true
	case report.Status == "completed" && !hasFigures && open != nil && !c.contextDroppedSince(open):
		// Done, but the context figure has not dropped yet: wait for what it
		// freed rather than settle "freed nothing". One that already dropped
		// (the agent reported it first) settles below.
		if !open.finished {
			open.finished = true
			open.usageAtFinish = c.usageReports
		}
		if report.TokensBefore > 0 {
			open.before = report.TokensBefore
		}
		c.mu.Unlock()
		return true
	case report.Status == "completed" && !hasFigures && open == nil:
		// Done, with no start seen (an agent that reports only the outcome, or a
		// reattach mid-compaction) and no figures: the context figure here is
		// from before it. Wait for a newer one, as a compaction that started
		// does, and claim none if the agent moves on without one.
		c.providerCompaction = &providerCompaction{
			turnID:        turnID,
			itemID:        c.nextProviderCompactionIDLocked(turnID),
			before:        report.TokensBefore,
			finished:      true,
			usageAtFinish: c.usageReports,
		}
		c.settledCompaction = nil
		c.mu.Unlock()
		return true
	}
	c.providerCompaction = nil
	if open == nil && hasFigures && c.settledCompaction != nil && c.settledCompaction.turnID == turnID {
		// The figures for the row that just settled without them.
		open = c.settledCompaction
	}
	if open == nil {
		// No start was seen, and the outcome needs no newer figure: it failed,
		// or the agent gave its own. It still gets a row of its own.
		open = &providerCompaction{
			turnID: turnID,
			itemID: c.nextProviderCompactionIDLocked(turnID),
		}
	}
	// A row settled without the agent's own figures stays known, so figures
	// that arrive just after it still reach the same row.
	c.settledCompaction = nil
	if report.Status == "completed" && !hasFigures {
		c.settledCompaction = open
	}
	before, after, window := open.before, c.contextTokens, c.contextWindow
	if report.TokensBefore > 0 {
		before = report.TokensBefore
	}
	if report.TokensAfter > 0 {
		after = report.TokensAfter
	}
	c.mu.Unlock()
	if report.Status == "failed" {
		emit(ports.ChatEvent{
			Kind: ports.ChatEventCompacted, ProviderTurnID: compactionRowTurn(turnID), ProviderItemID: open.itemID,
			ActivityStatus: domain.ActivityStatusFailed, Summary: failure,
			Detail: compactionFigures(before, 0, window),
		})
		return true
	}
	emit(ports.ChatEvent{
		Kind: ports.ChatEventCompacted, ProviderTurnID: compactionRowTurn(turnID), ProviderItemID: open.itemID,
		Summary: compactionSummary(before, after),
		Detail:  compactionFigures(before, after, window),
	})
	return true
}

// nextProviderCompactionIDLocked names the next compaction the agent reported
// in turnID. They are counted per turn: an attachment that reconnects to a
// running turn gets every update of that turn replayed by the host, so it
// numbers the turn's compactions as the attachment before it did. A replayed
// one reaches the row already stored, and a new one gets a number no earlier
// attachment used. c.mu must be held.
func (c *conversation) nextProviderCompactionIDLocked(turnID string) string {
	if c.providerCompactionTurn != turnID {
		c.providerCompactionTurn = turnID
		c.providerCompactions = 0
	}
	c.providerCompactions++
	return c.providerItemID(fmt.Sprintf("compaction:%s:%d", turnID, c.providerCompactions))
}

// unreportedCompaction is why a compaction whose agent never said how it went
// did not settle as one.
const unreportedCompaction = "No result reported"

// settleUnreportedCompaction settles a compaction whose agent never said how it
// went before its turn, or its cycle, ended. Left to the turn's settling, its row
// would read as a finished compaction, though nothing marked the conversation
// compacted.
func (c *conversation) settleUnreportedCompaction(open *providerCompaction, emit func(ports.ChatEvent)) {
	c.mu.Lock()
	window := c.contextWindow
	c.mu.Unlock()
	emit(ports.ChatEvent{
		Kind: ports.ChatEventCompacted, ProviderTurnID: compactionRowTurn(open.turnID), ProviderItemID: open.itemID,
		ActivityStatus: domain.ActivityStatusFailed, Summary: unreportedCompaction,
		Detail: compactionFigures(open.before, 0, window),
	})
}

// abandonCycleCompaction returns the events that settle a compaction the agent
// was running between prompts when it went away. Only the cycle's end settles
// such a row, as it has no turn, so without this it would read "Compacting
// conversation" for good.
func (c *conversation) abandonCycleCompaction() []ports.ChatEvent {
	c.mu.Lock()
	open := c.providerCompaction
	if open == nil || !strings.HasPrefix(open.turnID, providerCyclePrefix) {
		c.mu.Unlock()
		return nil
	}
	if !open.finished {
		c.providerCompaction = nil
	}
	c.mu.Unlock()
	var settled []ports.ChatEvent
	collect := func(event ports.ChatEvent) { settled = append(settled, event) }
	if open.finished {
		// It finished and only waited to hear what it freed.
		c.settleFinishedCompaction(collect)
	} else {
		c.settleUnreportedCompaction(open, collect)
	}
	return settled
}

// contextDroppedSince reports whether the agent's context figure, measured in
// this turn, is already below where the compaction started. c.mu must be held.
func (c *conversation) contextDroppedSince(open *providerCompaction) bool {
	return open.measured && c.contextBelow(open.before)
}

// contextBelow reports whether the latest context figure is below before.
// c.mu must be held.
func (c *conversation) contextBelow(before int64) bool {
	return c.contextTokens > 0 && c.contextTokens < before
}

// settleFinishedCompaction settles a compaction that finished without saying
// what it freed, once a newer context figure arrives or the agent moves on
// without one. A compaction still under way is left alone.
func (c *conversation) settleFinishedCompaction(emit func(ports.ChatEvent)) {
	c.mu.Lock()
	open := c.providerCompaction
	if open == nil || !open.finished {
		c.mu.Unlock()
		return
	}
	c.providerCompaction = nil
	// Settled without the agent's own figures, whatever a usage report gave:
	// figures that still follow reach this row instead of opening another.
	c.settledCompaction = open
	after, window := c.contextTokens, c.contextWindow
	if c.usageReports == open.usageAtFinish && !c.contextBelow(open.before) {
		// Nothing newer than the finish, and nothing lower than the start: the
		// figure is from before it.
		after = 0
	}
	c.mu.Unlock()
	emit(ports.ChatEvent{
		Kind: ports.ChatEventCompacted, ProviderTurnID: compactionRowTurn(open.turnID), ProviderItemID: open.itemID,
		Summary: compactionSummary(open.before, after),
		Detail:  compactionFigures(open.before, after, window),
	})
}

// compactionFigures is what a compaction reclaimed, as far as the token reports
// either side of it show. Zero means unknown and is left out.
func compactionFigures(before, after, window int64) []byte {
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
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	return encoded
}

func (c *conversation) trackContext(turnID string, used, window int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usageReports++
	c.contextTokens = used
	c.contextTurn = turnID
	if window > 0 {
		c.contextWindow = window
	}
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

func turnState(reason acpsdk.StopReason) domain.TurnState {
	switch reason {
	case acpsdk.StopReasonCancelled:
		return domain.TurnStateInterrupted
	case acpsdk.StopReasonEndTurn:
		return domain.TurnStateCompleted
	default:
		return domain.TurnStateFailed
	}
}

func (c *conversation) Interrupt(ctx context.Context, providerTurnID string) error {
	c.mu.Lock()
	active := c.activeTurn
	sessionID := c.sessionID
	if active == "" || c.settlingTurn == active || (providerTurnID != "" && providerTurnID != active) {
		c.mu.Unlock()
		return ports.ErrChatNoActiveTurn
	}
	if prior := c.interrupt; prior != nil {
		c.mu.Unlock()
		// A repeated Stop for the turn already being cancelled is the same request,
		// not a new one: ACP's session/cancel is a notification with no turn id, so
		// sending it again cannot be scoped and adds nothing. Answer with the first
		// attempt's outcome. The turn stays busy until its Prompt returns either way.
		select {
		case <-prior.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		c.mu.Lock()
		err := prior.err
		c.mu.Unlock()
		if err != nil {
			return fmt.Errorf("ACP session/cancel: %w", err)
		}
		return nil
	}
	attempt := &interruptAttempt{turnID: active, done: make(chan struct{})}
	c.interrupt = attempt
	c.mu.Unlock()

	err := c.conn.Cancel(ctx, acpsdk.CancelNotification{SessionId: acpsdk.SessionId(sessionID)})
	c.mu.Lock()
	attempt.err = err
	close(attempt.done)
	if err != nil && c.interrupt == attempt {
		// The notification never crossed the connection, so keep the local
		// Prompt alive and allow a later retry to make a fresh attempt.
		c.interrupt = nil
	}
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("ACP session/cancel: %w", err)
	}
	// Not every provider withdraws its open questions on session/cancel (ACP
	// leaves answering them to the client), so close them here: otherwise
	// Stop leaves the turn waiting on a question nobody can answer.
	c.cancelTurnInputs(active)
	// The provider owns the root Prompt until it returns a terminal result. Keep
	// AO busy after accepting session/cancel so a restart cannot start a second
	// root prompt while the first one is still executing downstream.
	return nil
}

// turnInterruptedLocked reports whether session/cancel was sent for turnID.
// The caller must hold c.mu.
func (c *conversation) turnInterruptedLocked(turnID string) bool {
	attempt := c.interrupt
	if attempt == nil || attempt.turnID != turnID {
		return false
	}
	select {
	case <-attempt.done:
		return attempt.err == nil
	default:
		return false
	}
}

func (c *conversation) ResolveRequest(
	ctx context.Context,
	requestID string,
	decision ports.ChatDecision,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	request, ok := c.pending[requestID]
	if !ok {
		c.mu.Unlock()
		return ports.ErrChatRequestNotPending
	}
	offeredRaw, offered := request.options[decision.ID]
	if !offered {
		c.mu.Unlock()
		return ports.ErrChatDecisionNotOffered
	}
	if len(decision.Raw) > 0 {
		if !bytes.Equal(bytes.TrimSpace(decision.Raw), bytes.TrimSpace(offeredRaw)) {
			c.mu.Unlock()
			return ports.ErrChatDecisionNotOffered
		}
	}
	delete(c.pending, requestID)
	c.mu.Unlock()
	command := persistentInteractionCommand{
		RequestID: requestID, Kind: persistentInteractionApproval,
		Decision: &persistentDecision{ID: decision.ID},
	}
	eventID, err := c.recordPersistentInteraction(ctx, command)
	if err != nil {
		c.mu.Lock()
		if !c.closed {
			c.pending[requestID] = request
		}
		c.mu.Unlock()
		return err
	}
	command.EventID = eventID

	c.emit(persistentInteractionEvent(command))
	request.result <- decision.ID
	return nil
}

// discard rolls back setup without leaving a newly created host behind.
// A failed adoption has no authority to terminate an existing provider.
func (c *conversation) discard() {
	if c.unadoptedForkID != "" && c.canDeleteFork {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := c.conn.UnstableDeleteSession(ctx, acpsdk.UnstableDeleteSessionRequest{SessionId: acpsdk.SessionId(c.unadoptedForkID)})
		if err != nil {
			c.log.Warn("failed to delete unadopted ACP fork", "error", err)
		}
		cancel()
		c.unadoptedForkID = ""
	}
	if c.proc.reconnected {
		_ = c.Close()
	} else {
		_ = c.Terminate()
	}
}

func (c *conversation) Close() error {
	return c.closeProvider(false, false)
}

// Terminate destroys the provider host. Close deliberately only detaches during
// daemon shutdown or updater replacement.
func (c *conversation) Terminate() error {
	return c.closeProvider(true, true)
}

// staleBackgroundTask bounds how long an event-derived running task can keep a
// session from hibernating after its last event. ACP cannot enumerate native
// jobs, so a completion the provider never sends must not block it forever.
const staleBackgroundTask = 30 * time.Minute

func (c *conversation) CanHibernate(ctx context.Context) (bool, error) {
	if c.providerCycleActive(time.Now()) {
		// The agent is working on its own; hibernating would kill that work.
		return false, nil
	}
	if c.hibernationCheck == nil {
		return !c.backgroundObserved.Active(time.Now(), staleBackgroundTask), nil
	}
	c.mu.Lock()
	id := c.sessionID
	c.mu.Unlock()
	return c.hibernationCheck(ctx, c.conn, acpsdk.SessionId(id))
}

// Hibernate releases the bridge and its provider without closing the native
// ACP session. session/close can delete the resume state on some agents.
func (c *conversation) Hibernate() error {
	return c.closeProvider(true, false)
}

// providerSessionLost classifies err from a session-scoped request. When the
// agent reports that it no longer holds this conversation's session, the
// provider can never serve it again, and a persistent host would otherwise keep
// it reattachable across daemon restarts: every send fails while resume-agent
// refuses because the host is still alive. Stop the provider without
// session/close, as hibernation does, so the controller's stream ends, the
// session reads as exited, and resuming reloads the saved transcript in a fresh
// process. Any other error is returned unchanged.
func (c *conversation) providerSessionLost(err error) error {
	if !isACPSessionMissing(err) {
		return err
	}
	lost := c.markSessionLost(err)
	if stopErr := c.stopLostProvider(); stopErr != nil {
		return fmt.Errorf("%w; stopping the provider failed: %w", lost, stopErr)
	}
	return lost
}

// markSessionLost records a lost session and wraps err without stopping the
// provider yet, so a caller can still emit the turn's outcome on the stream
// that stopping would close. Callers check isACPSessionMissing first.
func (c *conversation) markSessionLost(err error) error {
	c.mu.Lock()
	sessionID := c.sessionID
	c.sessionLost = true
	c.mu.Unlock()
	c.log.Warn("chat: ACP provider no longer holds the session; stopping it so the session can be resumed",
		"providerSession", sessionID, "reconnected", c.proc.reconnected, "error", err)
	return fmt.Errorf("%w: %w", ports.ErrChatProviderSessionLost, err)
}

// stopLostProvider stops the provider without session/close: the agent has no
// session to close, and the saved transcript must survive for resume.
//
// A concurrent Close may already have used closeOnce to detach, which only
// releases the transport and keeps a persistent host alive for the next
// daemon. A detach assumes the host can still serve the conversation, which a
// lost session rules out, so escalate it to termination. The host's terminate
// is idempotent and reaches the host through its own shutdown channel, not the
// detached transport.
func (c *conversation) stopLostProvider() error {
	if err := c.closeProvider(true, false); err != nil {
		return err
	}
	c.mu.Lock()
	detached := c.detaching
	c.mu.Unlock()
	if detached && c.proc.terminate != nil {
		return c.proc.terminate()
	}
	return nil
}

func (c *conversation) closeProvider(terminate, closeSession bool) error {
	var closeErr error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		if c.cycle != nil && c.cycle.timer != nil {
			c.cycle.timer.Stop()
		}
		close(c.closing)
		c.releasePendingSteersLocked()
		persistent := c.proc.terminate != nil
		c.detaching = persistent && !terminate
		cancel := c.turnCancel
		sessionID := c.sessionID
		c.mu.Unlock()
		if persistent && !terminate {
			closeErr = c.proc.stop()
			return
		}
		if cancel != nil {
			cancel()
		}
		c.failPendingPermissions()
		c.failPendingInputs()
		if closeSession && sessionID != "" {
			// The provider may save its session while answering; an answered close
			// lets the host stop it sooner.
			wait := sessionCloseWait
			if c.proc != nil && c.proc.quickStop.Load() {
				wait = quickSessionCloseWait
			}
			closeCtx, cancelClose := context.WithTimeout(context.Background(), wait)
			if _, err := c.conn.CloseSession(closeCtx, acpsdk.CloseSessionRequest{SessionId: acpsdk.SessionId(sessionID)}); err == nil && c.proc != nil {
				c.proc.quickStop.Store(true)
			}
			cancelClose()
		}
		if persistent {
			closeErr = c.proc.terminate()
		} else {
			closeErr = c.proc.stop()
		}
	})
	return closeErr
}

// AcknowledgeProviderEvent lets the host discard a prompt journal only after
// the terminal event was committed by the controller.
func (c *conversation) AcknowledgeProviderEvent(ctx context.Context, providerEventID string) error {
	c.mu.Lock()
	terminal := c.terminalEventID
	lost := c.sessionLost
	c.mu.Unlock()
	// A provider stopped for a lost session took its host and prompt journal
	// with it, so there is nothing left to release. Failing this ACK would make
	// the controller preserve a provider that is already gone.
	if lost || providerEventID == "" || providerEventID != terminal || c.proc.terminate == nil {
		return nil
	}
	return c.conn.NotifyExtension(ctx, persistenthost.ACPPromptAckMethod, map[string]string{
		"eventId": providerEventID,
	})
}

func (c *conversation) watchConnection() {
	<-c.conn.Done()
	c.mu.Lock()
	detaching := c.detaching
	preserve := !c.closed && c.proc.terminate != nil
	c.mu.Unlock()
	// SDK Done cancels only its logical connection. Release both the forwarding
	// pipe and the actual socket, including a host blocked writing replay under
	// its ownership lock. Stopping this transport must not terminate the host.
	_ = c.legacyWire.reader.Close()
	_ = c.proc.stop()
	var settled []ports.ChatEvent
	if !detaching && !preserve {
		c.failPendingPermissions()
		c.failPendingInputs()
		settled = c.abandonCycleCompaction()
	}
	event := ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped}
	if preserve {
		event.Err = ports.ErrChatRecoveryInconclusive
	}
	c.eventMu.Lock()
	if !c.eventsClosed {
		for _, row := range settled {
			select {
			case c.events <- row:
			case <-c.closing:
			}
		}
		// Deliver the recovery boundary even though conn.Done is already closed;
		// emit's disconnect select could otherwise drop this ownership signal.
		select {
		case c.events <- event:
		case <-c.closing:
		}
		c.eventsClosed = true
		close(c.events)
	}
	c.eventMu.Unlock()
}

func (c *conversation) emit(event ports.ChatEvent) {
	if c.captureHistoryEvent(event) {
		return
	}
	c.eventMu.RLock()
	defer c.eventMu.RUnlock()
	if c.eventsClosed {
		return
	}
	if c.proc != nil && c.proc.terminate != nil {
		// Host-journaled frames must reach durable projection; dropping one here
		// would make a successful replay look exactly-once while losing content.
		select {
		case c.events <- event:
		case <-c.conn.Done():
		case <-c.closing:
		}
		return
	}
	select {
	case c.events <- event:
		return
	default:
	}
	if event.Kind == ports.ChatEventMessageDelta || event.Kind == ports.ChatEventReasoningDelta {
		c.log.Warn("dropped ACP chat delta: consumer behind", "kind", event.Kind, "item", event.ProviderItemID)
		return
	}
	select {
	case c.events <- event:
	case <-time.After(5 * time.Second):
		c.log.Error("dropped ACP lifecycle event: consumer stalled", "kind", event.Kind)
	}
}

// keepOpen names tools that outlive the turn (see keepToolsOutlivingTurn);
// they stay running instead of failing with it.
func (c *conversation) settleOpenItems(turnID string, keepOpen map[string]bool, turnState ...domain.TurnState) {
	// Copy what is settled while holding the lock. A late session/update for the
	// same turn can still write these maps and tool states (processUpdate) while
	// the items below are emitted, so iterating the live ones raced.
	c.mu.Lock()
	messages := maps.Clone(c.messages)
	thoughts := maps.Clone(c.thoughts)
	nestedMessages := maps.Clone(c.nestedMessages)
	liveTools := c.tools
	tools := make(map[string]toolState, len(liveTools))
	for id, tool := range liveTools {
		tools[id] = *tool
	}
	c.mu.Unlock()
	recovered := len(turnState) > 0 && turnState[0] == domain.TurnStateRecovered
	activityStatus := domain.ActivityStatusCompleted
	if recovered {
		activityStatus = domain.ActivityStatusRecovered
	}
	messageIDs := sortedKeys(messages)
	for _, id := range messageIDs {
		text := messages[id]
		c.emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: turnID, ProviderItemID: id, Text: text})
	}
	thoughtIDs := sortedKeys(thoughts)
	for _, id := range thoughtIDs {
		text := thoughts[id]
		c.emit(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turnID,
			ProviderItemID: id, ActivityKind: domain.ActivityKindReasoning,
			ActivityStatus: activityStatus, Summary: "Reasoning", Text: text})
	}
	nestedIDs := sortedKeys(nestedMessages)
	for _, id := range nestedIDs {
		item := nestedMessages[id]
		detail, _ := json.Marshal(map[string]any{"parentProviderItemId": item.parentID, "nestedAgent": true})
		c.emit(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turnID,
			ProviderItemID: id, ActivityKind: domain.ActivityKindMCPTool,
			ActivityStatus: activityStatus, Summary: "Subagent response",
			Text: item.text, Detail: detail})
	}
	toolIDs := sortedKeys(tools)
	for _, id := range toolIDs {
		tool := tools[id]
		if toolStatusOpen(tool.status) && !keepOpen[id] && c.toolStillOpen(liveTools, id) {
			snapshot := tool
			snapshot.status = acpsdk.ToolCallStatusFailed
			event := c.toolEvent(turnID, &snapshot, true)
			if recovered {
				event.ActivityStatus = domain.ActivityStatusRecovered
			}
			c.emit(event)
		}
	}
}

func toolStatusOpen(status acpsdk.ToolCallStatus) bool {
	return status == acpsdk.ToolCallStatusPending || status == acpsdk.ToolCallStatusInProgress || status == ""
}

// toolStillOpen reports whether the settled turn's tool is still open in the
// map it was snapshotted from. A late session/update may complete it after
// the snapshot; settling must not then replace that completion with a
// synthetic failure.
func (c *conversation) toolStillOpen(tools map[string]*toolState, id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	tool := tools[id]
	return tool == nil || toolStatusOpen(tool.status)
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c *conversation) failPendingPermissions() {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[string]*parkedPermission)
	c.mu.Unlock()
	for _, request := range pending {
		select {
		case request.result <- "":
		default:
		}
	}
}

// cancelTurnInputs answers the turn's open questions with Cancel.
func (c *conversation) cancelTurnInputs(turnID string) {
	c.mu.Lock()
	var cancelled map[string]*parkedInput
	for id, input := range c.pendingInputs {
		if input.turnID != turnID {
			continue
		}
		if cancelled == nil {
			cancelled = make(map[string]*parkedInput)
		}
		cancelled[id] = input
		delete(c.pendingInputs, id)
	}
	c.mu.Unlock()
	for id, input := range cancelled {
		// requestInput emits InputRequested after it registers the question;
		// wait for that so the resolution never comes first.
		<-input.ready
		c.emit(ports.ChatEvent{Kind: ports.ChatEventInputResolved, RequestID: id})
		select {
		case input.result <- ports.ChatInputResponse{Action: ports.ChatInputActionCancel}:
		default:
		}
	}
}

func (c *conversation) failPendingInputs() {
	c.mu.Lock()
	pending := c.pendingInputs
	c.pendingInputs = make(map[string]*parkedInput)
	c.mu.Unlock()
	for _, request := range pending {
		select {
		case request.result <- ports.ChatInputResponse{Action: ports.ChatInputActionCancel}:
		default:
		}
	}
}

func (c *conversation) promptContent(message ports.ChatUserMessage) ([]acpsdk.ContentBlock, error) {
	c.mu.Lock()
	caps := cloneCapabilities(c.capabilities)
	c.mu.Unlock()
	prompt := make([]acpsdk.ContentBlock, 0, 1+len(message.Content))
	if strings.TrimSpace(message.Text) != "" {
		prompt = append(prompt, acpsdk.TextBlock(message.Text))
	}
	for _, item := range message.Content {
		switch item.Type {
		case "image":
			if !caps.Has(ports.ChatCapabilityImages) {
				return nil, fmt.Errorf("ACP agent does not support image prompts")
			}
			if item.Data == "" || item.MIMEType == "" {
				return nil, errors.New("image content requires data and MIME type")
			}
			prompt = append(prompt, acpsdk.ImageBlock(item.Data, item.MIMEType))
		case "resource_link":
			if item.URI == "" || item.Name == "" {
				return nil, errors.New("resource link requires a URI and name")
			}
			prompt = append(prompt, acpsdk.ResourceLinkBlock(item.Name, item.URI))
		case "resource":
			if !caps.Has(ports.ChatCapabilityEmbeddedContext) {
				return nil, fmt.Errorf("ACP agent does not support embedded context")
			}
			if item.URI == "" {
				return nil, errors.New("embedded resource requires a URI")
			}
			mimeType := (*string)(nil)
			if item.MIMEType != "" {
				mimeType = pointer(item.MIMEType)
			}
			resource := &acpsdk.TextResourceContents{
				Uri: item.URI, Text: item.Text, MimeType: mimeType,
			}
			if item.Internal {
				resource.Meta = map[string]any{aoInternalReplayMetaKey: true}
			}
			prompt = append(prompt, acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
				TextResourceContents: resource,
			}))
		default:
			return nil, fmt.Errorf("unsupported chat content type %q", item.Type)
		}
	}
	if len(prompt) == 0 {
		return nil, errors.New("chat message has no content")
	}
	return prompt, nil
}

// sdkErrorCode matches a bare error code such as "too_few_groups".
var sdkErrorCode = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)+$`)

// compactionFailureText is the reason a failed compaction shows: the agent's
// own words, a readable line for a known SDK code, never the raw code.
func compactionFailureText(reason string) string {
	reason = strings.TrimSpace(reason)
	switch {
	case reason == "too_few_groups":
		return "Not enough messages to compact."
	case reason == "" || sdkErrorCode.MatchString(reason):
		return "Compaction failed"
	}
	return reason
}
