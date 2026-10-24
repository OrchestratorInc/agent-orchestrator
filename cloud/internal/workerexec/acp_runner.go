package workerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	acp "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

type approvalControl interface {
	CreateChatApproval(context.Context, worker.ChatApproval) error
	ChatApprovalDecision(context.Context, string, int, string) (string, error)
}

type capabilityPublisher interface {
	PublishTurnCapabilities(context.Context, string, int, bool) error
}

type acpSession struct {
	conn      *acp.ClientSideConnection
	sessionID acp.SessionId
	turnID    string
	steering  bool
}

// lockedBuffer collects an agent's stderr, which os/exec copies in from its
// own goroutine while a failing turn reads it. The buffer is a field, not
// embedded: an embedded bytes.Buffer would lend it ReadFrom, which io.Copy
// prefers over Write, and the copy would then skip the lock.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }

func (s *acpSession) Steer(ctx context.Context, turnID, text string) error {
	if s == nil || turnID != s.turnID || !s.steering {
		return errors.New("the active provider turn does not support steering")
	}
	raw, err := s.conn.CallExtension(ctx, "_session/steering", map[string]any{
		"sessionId": s.sessionID,
		"prompt":    []acp.ContentBlock{{Text: &acp.ContentBlockText{Type: "text", Text: text}}},
		"_meta":     map[string]any{"steering": map[string]any{"idleBehavior": "promptRequired"}},
	})
	if err != nil {
		return fmt.Errorf("ACP steer: %w", err)
	}
	var result struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Outcome != "injected" {
		return fmt.Errorf("ACP did not inject guidance: %s", result.Outcome)
	}
	return nil
}

func acpSteeringSupported(meta map[string]any) bool {
	value, _ := meta["steering"].(map[string]any)
	supported, _ := value["supported"].(bool)
	return supported
}

func (s *Supervisor) runACP(ctx context.Context, turn worker.Turn, command Command, publish func(Output) error, identity func(string) error) error {
	if len(turn.DeniedCommands) > 0 {
		return fmt.Errorf("%w: ACP cannot enforce command-prefix deny rules", ErrUnsupportedPolicy)
	}
	control, ok := s.Control.(approvalControl)
	if !ok {
		return errors.New("Cloud approval control is unavailable")
	}
	path, args, env, err := acpLaunch(ctx, turn, command)
	if err != nil {
		return err
	}
	process := exec.CommandContext(ctx, path, args...)
	configureProviderProcess(process)
	process.Dir = command.Dir
	process.Env = mergedEnvironment(env)
	stdin, err := process.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr lockedBuffer
	process.Stderr = &stderr
	if err := process.Start(); err != nil {
		return fmt.Errorf("start ACP agent: %w", err)
	}
	defer func() { _ = stopProviderProcess(process); _ = process.Wait() }()
	client := &cloudACPClient{control: control, turn: turn, cursorMode: command.CursorMode, publish: publish}
	conn := acp.NewClientSideConnection(client, stdin, stdout)
	handshakeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	initialized, err := conn.Initialize(handshakeCtx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	if err != nil {
		return fmt.Errorf("initialize ACP agent: %w: %s", err, boundedError(stderr.String()))
	}
	var meta map[string]any
	if turn.Harness == "claude-code" && command.SystemPrompt != "" {
		meta = map[string]any{"systemPrompt": map[string]any{
			"type": "preset", "preset": "claude_code", "append": command.SystemPrompt,
		}}
	}
	// Claude opens the sibling checkouts as additional directories, so it
	// loads their CLAUDE.md as a terminal's --add-dir does. An agent that does
	// not advertise the capability keeps the settings' directory access only.
	var additionalDirectories []string
	if turn.Harness == "claude-code" && initialized.AgentCapabilities.SessionCapabilities.AdditionalDirectories != nil {
		additionalDirectories = command.AdditionalDirectories
	}
	var sessionID acp.SessionId
	var configOptions []acp.SessionConfigOption
	var modes *acp.SessionModeState
	if turn.AgentSessionID != "" {
		loaded, loadErr := conn.LoadSession(handshakeCtx, acp.LoadSessionRequest{Meta: meta, Cwd: command.Dir, AdditionalDirectories: additionalDirectories, McpServers: []acp.McpServer{}, SessionId: acp.SessionId(turn.AgentSessionID)})
		if loadErr != nil {
			var requestErr *acp.RequestError
			// The CLI builder's missing-transcript check only changes CLI args;
			// ACP must resolve the original durable identity with the provider.
			// Only an explicit missing Claude session permits a replacement.
			if turn.Harness != "claude-code" || !errors.As(loadErr, &requestErr) || requestErr.Code != -32002 {
				return fmt.Errorf("restore ACP session: %w", loadErr)
			}
		} else {
			sessionID = acp.SessionId(turn.AgentSessionID)
			configOptions, modes = loaded.ConfigOptions, loaded.Modes
		}
	}
	if sessionID == "" {
		created, createErr := conn.NewSession(handshakeCtx, acp.NewSessionRequest{Meta: meta, Cwd: command.Dir, AdditionalDirectories: additionalDirectories, McpServers: []acp.McpServer{}})
		if createErr != nil {
			return fmt.Errorf("create ACP session: %w: %s", createErr, boundedError(stderr.String()))
		}
		sessionID = created.SessionId
		configOptions, modes = created.ConfigOptions, created.Modes
		if err := identity(string(sessionID)); err != nil {
			return err
		}
	}
	if err := configureACPSession(ctx, conn, sessionID, turn, command.CursorMode, configOptions, modes); err != nil {
		return err
	}
	active := &acpSession{conn: conn, sessionID: sessionID, turnID: turn.ID, steering: acpSteeringSupported(initialized.Meta)}
	if publisher, ok := s.Control.(capabilityPublisher); ok {
		if err := publisher.PublishTurnCapabilities(ctx, turn.ID, turn.Attempt, active.steering); err != nil {
			return err
		}
	}
	s.activeMu.Lock()
	s.activeACP = active
	s.activeMu.Unlock()
	defer func() {
		s.activeMu.Lock()
		if s.activeACP == active {
			s.activeACP = nil
		}
		s.activeMu.Unlock()
	}()
	// The LoadSession/NewSession handshake above is complete, so any replayed
	// history has already been dropped; record from here on — this turn's output.
	client.live.Store(true)
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: sessionID, Prompt: []acp.ContentBlock{{Text: &acp.ContentBlockText{Type: "text", Text: turn.Prompt}}}})
	if err != nil {
		return fmt.Errorf("ACP prompt: %w: %s", err, boundedError(stderr.String()))
	}
	return nil
}

func acpLaunch(ctx context.Context, turn worker.Turn, command Command) (string, []string, map[string]string, error) {
	env := make(map[string]string, len(command.Env)+1)
	for key, value := range command.Env {
		env[key] = value
	}
	switch turn.Harness {
	case "claude-code":
		if command.Path == claudeCodeBinary {
			ensureCtx, cancel := context.WithTimeout(ctx, claudeCodeTurnWait)
			command.Path = EnsureClaudeCode(ensureCtx)
			cancel()
		}
		env = claudeModelEnvironment(command, turn.Model, nil)
		adapter, err := EnsureClaudeACPAdapter(ctx)
		if err != nil {
			return "", nil, nil, err
		}
		return adapter, nil, env, nil
	case "cursor":
		if err := validateCursorMode(command.CursorMode); err != nil {
			return "", nil, nil, err
		}
		// The project's Cursor mode is not a flag here: cursor-agent's acp
		// command ignores --mode, so configureACPSession sets it as the ACP
		// session mode. A restricted mode launches without --force or
		// --auto-review (cursorACPApprovalMode).
		args := []string{"--trust"}
		switch cursorACPApprovalMode(turn, command.CursorMode) {
		case "auto":
			args = append(args, "--auto-review")
		case "bypass-permissions":
			args = append(args, "--force")
		}
		return command.Path, append(args, "acp"), env, nil
	default:
		return "", nil, nil, fmt.Errorf("ACP is unavailable for %s", turn.Harness)
	}
}

func configureACPSession(ctx context.Context, conn *acp.ClientSideConnection, sessionID acp.SessionId, turn worker.Turn, cursorMode string, options []acp.SessionConfigOption, modes *acp.SessionModeState) error {
	setOption := func(id, value string) error {
		if value == "" {
			return nil
		}
		if !acpOptionOffered(options, id, value) {
			return fmt.Errorf("ACP session does not offer %s %q", id, value)
		}
		response, err := conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sessionID, ConfigId: acp.SessionConfigId(id), Value: acp.SessionConfigValueId(value),
		}})
		if err != nil {
			return fmt.Errorf("set ACP %s: %w", id, err)
		}
		options = response.ConfigOptions
		return nil
	}
	// A model change can alter the available permission modes and effort levels.
	if err := setOption("model", acpModelValue(options, turn.Model)); err != nil {
		return err
	}
	if turn.Harness == "claude-code" {
		mode := claudeACPOfferedMode(turn, options, modes)
		if !acpModeOffered(options, modes, mode) {
			return fmt.Errorf("ACP session does not offer approval mode %q", mode)
		}
		if _, err := conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: sessionID, ModeId: acp.SessionModeId(mode)}); err != nil {
			return fmt.Errorf("set Claude approval mode: %w", err)
		}
	}
	if turn.Harness == "cursor" && cursorMode != "" {
		// The project's plan or ask mode, set on every turn so a restored
		// conversation runs in it too. A session that does not offer it fails
		// the turn rather than running it as the full agent.
		if !acpModeOffered(options, modes, cursorMode) {
			return fmt.Errorf("%w: ACP session does not offer Cursor mode %q", ErrUnsupportedPolicy, cursorMode)
		}
		if _, err := conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: sessionID, ModeId: acp.SessionModeId(cursorMode)}); err != nil {
			return fmt.Errorf("set Cursor mode: %w", err)
		}
	}
	return setOption("effort", turn.ReasoningEffort)
}

// claudeACPOfferedMode is the turn's approval mode adjusted to what the adapter
// offers: auto falls back to default, and a mode taken from the session rather
// than chosen for the turn steps down to the closest offered one instead of
// failing the turn. A mode chosen for the turn is never silently weakened.
func claudeACPOfferedMode(turn worker.Turn, options []acp.SessionConfigOption, modes *acp.SessionModeState) string {
	mode := claudeACPApprovalMode(turn)
	if mode == "auto" && !acpModeOffered(options, modes, mode) && acpModeOffered(options, modes, "default") {
		return "default"
	}
	if turn.ApprovalMode != "" || turn.Mode == "read-only" {
		return mode
	}
	for _, candidate := range []string{mode, "acceptEdits", "default"} {
		if acpModeOffered(options, modes, candidate) {
			return candidate
		}
	}
	return mode
}

// claudeACPApprovalMode is the Claude approval mode a Chat turn runs with. A
// turn's own approval mode wins; a turn without one (the session's queued
// first prompt, or a message sent without a pick) follows the session mode as
// a headless Chat turn does (claudeArgs): trusted skips permission prompts and
// standard accepts edits. The interactive terminal runs standard with
// --permission-mode auto instead (agentruntime.PermissionPolicyForMode).
func claudeACPApprovalMode(turn worker.Turn) string {
	if turn.Mode == "read-only" {
		return "plan"
	}
	switch turn.ApprovalMode {
	case "default":
		return "default"
	case "accept-edits":
		return "acceptEdits"
	case "auto":
		return "auto"
	case "bypass-permissions":
		return "bypassPermissions"
	}
	switch turn.Mode {
	case "trusted":
		return "bypassPermissions"
	case "standard":
		return "acceptEdits"
	}
	return "default"
}

// cursorACPApprovalMode is the approval mode a Cursor Chat turn runs with, as
// claudeACPApprovalMode is Claude's: a turn's own approval mode wins, and a
// turn without one follows the session mode (trusted bypasses permission
// prompts, standard accepts edits). A read-only turn approves nothing: it
// launches without --force and every permission request is cancelled
// (cloudACPClient.RequestPermission). A turn in the project's plan or ask
// mode (cursorMode) asks for every approval: a restricted mode is never
// combined with --force, --auto-review or an automatic approval.
func cursorACPApprovalMode(turn worker.Turn, cursorMode string) string {
	if turn.Mode == "read-only" || cursorMode != "" {
		return "default"
	}
	if turn.ApprovalMode != "" {
		return turn.ApprovalMode
	}
	switch turn.Mode {
	case "trusted":
		return "bypass-permissions"
	case "standard":
		return "accept-edits"
	}
	return "default"
}

// acpModelValue maps a model alias to the choice the session lists for it.
// Once a session runs an alias, Claude ACP lists that model under its full ID
// ("claude-fable-5-1", described "Fable 5.1 · ...") instead of the alias
// ("fable"), so a later turn that names the alias would otherwise be refused.
// Any other value, including one with no single match, is returned unchanged.
func acpModelValue(options []acp.SessionConfigOption, value string) string {
	if value == "" || acpOptionOffered(options, "model", value) {
		return value
	}
	match := ""
	for _, option := range options {
		if option.Select == nil || option.Select.Id != "model" {
			continue
		}
		var choices []acp.SessionConfigSelectOption
		if option.Select.Options.Ungrouped != nil {
			choices = append(choices, *option.Select.Options.Ungrouped...)
		}
		if option.Select.Options.Grouped != nil {
			for _, group := range *option.Select.Options.Grouped {
				choices = append(choices, group.Options...)
			}
		}
		for _, choice := range choices {
			if choice.Description == nil || choice.Value == "default" {
				continue
			}
			resolved, _, described := strings.Cut(*choice.Description, "·")
			if !described {
				continue
			}
			family, _, _ := strings.Cut(strings.TrimSpace(resolved), " ")
			if !strings.EqualFold(family, value) {
				continue
			}
			if match != "" && match != string(choice.Value) {
				return value
			}
			match = string(choice.Value)
		}
	}
	if match == "" {
		return value
	}
	return match
}

func acpOptionOffered(options []acp.SessionConfigOption, id, value string) bool {
	for _, option := range options {
		if option.Select == nil || string(option.Select.Id) != id {
			continue
		}
		if option.Select.Options.Ungrouped != nil {
			for _, choice := range *option.Select.Options.Ungrouped {
				if string(choice.Value) == value {
					return true
				}
			}
		}
		if option.Select.Options.Grouped != nil {
			for _, group := range *option.Select.Options.Grouped {
				for _, choice := range group.Options {
					if string(choice.Value) == value {
						return true
					}
				}
			}
		}
	}
	return false
}

func acpModeOffered(options []acp.SessionConfigOption, modes *acp.SessionModeState, mode string) bool {
	for _, option := range options {
		if option.Select != nil && option.Select.Id == "mode" {
			return acpOptionOffered(options, "mode", mode)
		}
	}
	if modes != nil {
		for _, offered := range modes.AvailableModes {
			if string(offered.Id) == mode {
				return true
			}
		}
	}
	return false
}

type cloudACPClient struct {
	control approvalControl
	turn    worker.Turn
	// cursorMode is the turn's Command.CursorMode.
	cursorMode string
	publish    func(Output) error
	// live gates recording of session updates. LoadSession replays the entire
	// prior conversation back through SessionUpdate before it returns; recording
	// that replay would re-emit every earlier turn's assistant text at the head
	// of the current turn. It stays false across the LoadSession/NewSession
	// handshake and is flipped true just before Prompt, so only the live turn's
	// output is published.
	live atomic.Bool
	// mu guards the tool calls and reasoning run below, which turn ACP tool
	// calls and thoughts into Chat activity for the live turn.
	mu        sync.Mutex
	tools     map[string]*acpToolState
	thoughtID string
	thoughts  int
	// textID is the assistant message the current run of text chunks adds to.
	// A tool call or thought ends it, so the agent's sentence before each step
	// is its own message, placed where it was said.
	textID string
	texts  int
	// published is each activity's last snapshot, so an update that changes
	// nothing the timeline shows is not stored again.
	published map[string]string
}

func (c *cloudACPClient) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	if !c.live.Load() {
		return nil
	}
	update := notification.Update
	if chunk := update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
		return c.publish(Output{Stream: "stdout", Text: chunk.Content.Text.Text, ItemID: c.text()})
	}
	if chunk := update.AgentThoughtChunk; chunk != nil && chunk.Content.Text != nil && chunk.Content.Text.Text != "" {
		// Consecutive thought chunks are one reasoning row, as with Codex.
		var outputs []Output
		id := c.thought()
		for _, text := range codexChunks(chunk.Content.Text.Text) {
			outputs = append(outputs, Output{Activity: &worker.ChatActivity{ID: id, Kind: "reasoning", Status: "running",
				Summary: "Reasoning", Detail: map[string]any{"textDelta": text}}})
		}
		for _, output := range outputs {
			c.publishActivity(output)
		}
		return nil
	}
	if activity := c.toolActivity(update); activity != nil {
		c.publishActivity(Output{Activity: activity})
	}
	return nil
}

// publishActivity records a tool call or reasoning row on a best-effort basis:
// the timeline is a view of the turn, so a rejected activity must never fail
// it. A worker that lost its turn finds out from the assistant text it
// publishes. A tool snapshot identical to the last one sent is skipped.
func (c *cloudACPClient) publishActivity(output Output) {
	if activity := output.Activity; activity != nil && activity.Kind != "reasoning" {
		raw, err := json.Marshal(activity)
		if err == nil {
			c.mu.Lock()
			if c.published == nil {
				c.published = map[string]string{}
			}
			same := c.published[activity.ID] == string(raw)
			c.published[activity.ID] = string(raw)
			c.mu.Unlock()
			if same {
				return
			}
		}
	}
	_ = c.publish(output)
}

// text is the id of the assistant message the current run of text chunks adds
// to, starting a new one after a tool call or a thought.
func (c *cloudACPClient) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.thoughtID = ""
	if c.textID == "" {
		c.texts++
		c.textID = fmt.Sprintf("acp-text-%d", c.texts)
	}
	return c.textID
}

// toolActivity folds an ACP tool_call or tool_call_update into the call's
// remembered state and returns its full activity snapshot, or nil for any
// other update.
func (c *cloudACPClient) toolActivity(update acp.SessionUpdate) *worker.ChatActivity {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tools == nil {
		c.tools = map[string]*acpToolState{}
	}
	switch {
	case update.ToolCall != nil:
		call := update.ToolCall
		id := string(call.ToolCallId)
		if id == "" {
			return nil
		}
		c.thoughtID, c.textID = "", ""
		tool := &acpToolState{title: call.Title, kind: call.Kind, status: call.Status, content: call.Content,
			rawInput: call.RawInput, rawOutput: call.RawOutput, meta: call.Meta}
		c.tools[id] = tool
		return acpToolActivity(id, tool)
	case update.ToolCallUpdate != nil:
		call := update.ToolCallUpdate
		id := string(call.ToolCallId)
		if id == "" {
			return nil
		}
		c.thoughtID, c.textID = "", ""
		tool := c.tools[id]
		if tool == nil {
			tool = &acpToolState{}
			c.tools[id] = tool
		}
		if call.Title != nil {
			tool.title = *call.Title
		}
		if call.Kind != nil {
			tool.kind = *call.Kind
		}
		if call.Status != nil {
			tool.status = *call.Status
		}
		if call.Content != nil {
			tool.content = call.Content
		}
		if call.RawInput != nil {
			tool.rawInput = call.RawInput
		}
		if call.RawOutput != nil {
			tool.rawOutput = call.RawOutput
		}
		if call.Meta != nil {
			tool.meta = call.Meta
		}
		return acpToolActivity(id, tool)
	}
	return nil
}

// thought is the id of the reasoning row the current run of thought chunks
// adds to, starting a new one after a message or a tool call.
func (c *cloudACPClient) thought() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.textID = ""
	if c.thoughtID == "" {
		c.thoughts++
		c.thoughtID = fmt.Sprintf("acp-thought-%d", c.thoughts)
	}
	return c.thoughtID
}

func (c *cloudACPClient) RequestPermission(ctx context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cancelled := acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}
	if ctx.Err() != nil || c.turn.Mode == "read-only" {
		return cancelled, nil
	}
	if selected, ok := automaticACPDecision(c.turn, c.cursorMode, request); ok {
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(selected)}, nil
	}
	options := make([]map[string]string, 0, len(request.Options))
	for _, option := range request.Options {
		options = append(options, map[string]string{"id": string(option.OptionId), "label": option.Name, "kind": string(option.Kind)})
	}
	decisions, err := json.Marshal(options)
	if err != nil {
		return acp.RequestPermissionResponse{}, err
	}
	requestID := uuid.NewString()
	summary := "Permission required"
	if request.ToolCall.Title != nil && strings.TrimSpace(*request.ToolCall.Title) != "" {
		summary = *request.ToolCall.Title
	}
	toolKind := ""
	if request.ToolCall.Kind != nil {
		toolKind = string(*request.ToolCall.Kind)
	}
	if err := c.control.CreateChatApproval(ctx, worker.ChatApproval{
		RequestID: requestID, TurnID: c.turn.ID, Attempt: c.turn.Attempt,
		Summary: summary, ToolKind: toolKind, Decisions: decisions,
	}); err != nil {
		if ctx.Err() != nil {
			return cancelled, nil
		}
		return acp.RequestPermissionResponse{}, err
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		decision, err := c.control.ChatApprovalDecision(ctx, c.turn.ID, c.turn.Attempt, requestID)
		if err != nil {
			if ctx.Err() != nil {
				return cancelled, nil
			}
			return acp.RequestPermissionResponse{}, err
		}
		if ctx.Err() != nil {
			return cancelled, nil
		}
		if decision != "" {
			for _, option := range request.Options {
				if string(option.OptionId) == decision {
					return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
				}
			}
			return acp.RequestPermissionResponse{}, errors.New("approval decision was not offered by the agent")
		}
		select {
		case <-ctx.Done():
			return cancelled, nil
		case <-ticker.C:
		}
	}
}

func automaticACPDecision(turn worker.Turn, cursorMode string, request acp.RequestPermissionRequest) (acp.PermissionOptionId, bool) {
	approval := turn.ApprovalMode
	if turn.Harness == "cursor" {
		approval = cursorACPApprovalMode(turn, cursorMode)
	}
	allow := false
	if approval == "bypass-permissions" {
		allow = true
	}
	if turn.Harness == "cursor" && approval == "accept-edits" && request.ToolCall.Kind != nil {
		switch *request.ToolCall.Kind {
		case acp.ToolKindEdit, acp.ToolKindDelete, acp.ToolKindMove:
			allow = true
		}
	}
	if !allow {
		return "", false
	}
	kinds := []acp.PermissionOptionKind{acp.PermissionOptionKindAllowOnce}
	if approval == "bypass-permissions" {
		kinds = append([]acp.PermissionOptionKind{acp.PermissionOptionKindAllowAlways}, kinds...)
	}
	for _, kind := range kinds {
		for _, option := range request.Options {
			if option.Kind == kind {
				return option.OptionId, true
			}
		}
	}
	return "", false
}

var errACPClientCapability = errors.New("ACP client capability is unavailable")

func (*cloudACPClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errACPClientCapability
}
func (*cloudACPClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errACPClientCapability
}
func (*cloudACPClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errACPClientCapability
}
func (*cloudACPClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errACPClientCapability
}
func (*cloudACPClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errACPClientCapability
}
func (*cloudACPClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errACPClientCapability
}
func (*cloudACPClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errACPClientCapability
}

var _ acp.Client = (*cloudACPClient)(nil)
