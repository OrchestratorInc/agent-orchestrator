package claudeacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const minimumNodeMajor = 22

const claudeTodoToolsEnv = "CLAUDE_CODE_ENABLE_TODO_TOOLS"

const (
	claudeEntrypointEnv = "CLAUDE_CODE_ENTRYPOINT"
	claudeEntrypoint    = "agent-orchestrator"
	claudeArtifactEnv   = "CLAUDE_CODE_ARTIFACT"
)

const claudePlanGuidance = "When you make a to-do list or plan, or do work with several steps, track it with " +
	"the TaskCreate and TaskUpdate tools (load them with ToolSearch if needed) and keep the tasks updated as " +
	"you go. Do not write the list only as text."

func claudeTodoToolsChoice(input map[string]string) (string, bool) {
	if value := strings.TrimSpace(input[claudeTodoToolsEnv]); value != "" {
		return value, true
	}
	if value := strings.TrimSpace(os.Getenv(claudeTodoToolsEnv)); value != "" {
		return value, true
	}
	return "", false
}

func claudeTodoToolsEnabled(input map[string]string) bool {
	value, chosen := claudeTodoToolsChoice(input)
	if !chosen {
		return true
	}
	switch strings.ToLower(value) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

type claudePlugin interface {
	ResolveBinary(context.Context) (string, error)
}

type claudeLaunchAuthenticator interface {
	ValidateLaunchAuth(context.Context, string, map[string]string) (ports.AgentAuthStatus, error)
}

func New(plugin claudePlugin, log *slog.Logger, onAuthRejected func()) ports.ChatDriver {
	return &checkpointDriver{plugin: plugin, ChatDriver: acpdriver.New(acpdriver.Config{
		Harness:               domain.HarnessClaudeCode,
		OnAuthRejected:        claudeAuthRejected(onAuthRejected),
		PromptResponseFailure: claudePromptResponseFailure,
		CanHibernate:          claudeCanHibernate,
		HistoryNotice:         claudeTranscriptNotice,
		PlansSpanTurns:        true,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming:    true,
			ports.ChatCapabilityTools:        true,
			ports.ChatCapabilityApprovals:    true,
			ports.ChatCapabilityInterrupt:    true,
			ports.ChatCapabilityResume:       true,
			ports.ChatCapabilityPromptReplay: true,
			ports.ChatCapabilityUsage:        true,
			ports.ChatCapabilityDiffs:        true,
			ports.ChatCapabilityPlans:        true,
			ports.ChatCapabilityCompaction:   true,
		},
		Probe: func(ctx context.Context) error {
			if _, err := resolveRuntime(ctx); err != nil {
				return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			claudeBinary, err := plugin.ResolveBinary(ctx)
			if err != nil {
				return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			if err := validateClaudeACPExecutable(claudeBinary, runtime.GOOS); err != nil {
				return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			return nil
		},
		Launch: func(ctx context.Context, cfg acpdriver.LaunchConfig) (acpdriver.Launch, error) {
			runtimeLaunch, err := resolveRuntime(ctx)
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			claudeBinary, err := plugin.ResolveBinary(ctx)
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			if err := validateClaudeACPExecutable(claudeBinary, runtime.GOOS); err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			authStarted := time.Now()
			if err := validateClaudeLaunchAuth(ctx, plugin, cfg.WorkspacePath, cfg.Env, log); err != nil {
				return acpdriver.Launch{}, err
			}
			authDuration := time.Since(authStarted)
			var models []ports.AgentModelInfo
			modelsStarted := time.Now()
			if _, preserve := claudeACPModelConfig(cfg.Env); !preserve {
				var modelErr error
				models, modelErr = claudecode.ProviderModels(ctx, claudeBinary, cfg.WorkspacePath, cfg.Env)
				if modelErr != nil && log != nil {
					log.Debug("Claude provider model discovery unavailable; using ACP defaults", "error", modelErr)
				}
			}
			if log != nil {
				log.Debug("Claude launch prepared", "sessionID", cfg.SessionID,
					"authDuration", authDuration, "modelsDuration", time.Since(modelsStarted))
			}
			env := claudeACPLaunchEnv(cfg.Env, claudeBinary, cfg.Model, models)
			return acpdriver.Launch{
				Command: runtimeLaunch.command,
				Args:    runtimeLaunch.args,
				Env:     env,
			}, nil
		},
		ServedModel:    claudeServedModel,
		SessionMeta:    claudeSessionMeta,
		SessionMode:    claudeSessionMode,
		SessionOptions: claudeSessionOptions,
		ValidateModel: func(ctx context.Context, _ acpdriver.LaunchConfig, model string) error {
			return claudeValidateModel(ctx, plugin, model)
		},
		ReadOnly: claudeReadOnlySession,
	}, log)}
}

func claudeReadOnlySession(cfg acpdriver.LaunchConfig) acpdriver.ReadOnlySession {
	return acpdriver.ReadOnlySession{
		Mode:             "default",
		PermissionPolicy: reviewerPermissionPolicy(strings.TrimSpace(cfg.Env["AO_REVIEW_WORKER_SESSION_ID"])),
	}
}

func reviewerPermissionPolicy(workerSessionID string) acpdriver.PermissionPolicy {
	return func(_ ports.PermissionMode, params acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
		if params.ToolCall.Kind != nil && *params.ToolCall.Kind == acpsdk.ToolKindExecute {
			input, _ := params.ToolCall.RawInput.(map[string]any)
			if command, _ := input["command"].(string); claudecode.ReviewerSubmitCommandAllowed(command, workerSessionID) {
				if id, ok := permissionOption(params.Options, acpsdk.PermissionOptionKindAllowOnce); ok {
					return id, true
				}
			}
		}
		for _, kind := range []acpsdk.PermissionOptionKind{acpsdk.PermissionOptionKindRejectOnce, acpsdk.PermissionOptionKindRejectAlways} {
			if id, ok := permissionOption(params.Options, kind); ok {
				return id, true
			}
		}
		return acpdriver.CancelPermission, true
	}
}

func permissionOption(options []acpsdk.PermissionOption, kind acpsdk.PermissionOptionKind) (acpsdk.PermissionOptionId, bool) {
	for _, option := range options {
		if option.Kind == kind {
			return option.OptionId, true
		}
	}
	return "", false
}

func claudeCanHibernate(ctx context.Context, conn *acpsdk.ClientSideConnection, id acpsdk.SessionId) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := conn.CallExtension(ctx, "_ao/session/can_hibernate", map[string]any{"sessionId": id})
	if err != nil {
		var requestErr *acpsdk.RequestError
		if errors.As(err, &requestErr) && requestErr.Code == -32601 {
			return false, nil
		}
		return false, err
	}
	var result struct {
		CanHibernate *bool `json:"canHibernate"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return false, err
	}
	if result.CanHibernate == nil {
		return false, errors.New("claude background-task check is missing canHibernate")
	}
	return *result.CanHibernate, nil
}

func validateClaudeLaunchAuth(ctx context.Context, plugin claudePlugin, workingDir string, env map[string]string, log *slog.Logger) error {
	validator, ok := plugin.(claudeLaunchAuthenticator)
	if !ok {
		return nil
	}
	status, err := validator.ValidateLaunchAuth(ctx, workingDir, env)
	if err != nil {
		if log != nil {
			log.Debug("Claude launch auth probe inconclusive; continuing", "error", err)
		}
		return nil
	}
	if status == ports.AgentAuthStatusUnauthorized {
		return ports.ErrAgentAuthRequired
	}
	return nil
}

func claudeAuthRejected(notifyDaemon func()) func() {
	return func() {
		claudecode.InvalidateAuthCache()
		if notifyDaemon != nil {
			notifyDaemon()
		}
	}
}

func claudePromptResponseFailure(response acpsdk.PromptResponse, model string) error {
	if response.StopReason != acpsdk.StopReasonEndTurn {
		return nil
	}
	air := claudeNestedMap(claudeNestedMap(response.Meta, "jetbrains"), "air")
	version, versionOK := air["version"].(float64)
	failure := claudeNestedMap(air, "sessionFailure")
	id, _ := failure["id"].(string)
	title, _ := failure["title"].(string)
	if !versionOK || version < 1 || strings.TrimSpace(id) == "" || strings.TrimSpace(title) == "" || failure["severity"] != "error" {
		return nil
	}
	details, _ := failure["details"].(string)
	if tooOld := claudeVersionTooOldFailure(title, details, model); tooOld != nil {
		return tooOld
	}
	var cause error
	if actions, ok := failure["actions"].([]any); ok {
		for _, action := range actions {
			if action == "login" {
				cause = ports.ErrChatAuthRequired
				break
			}
		}
	}
	return ports.NewChatProviderFailure(strings.TrimSpace(title), strings.TrimSpace(details), cause)
}

func claudeVersionTooOldFailure(title, details, model string) error {
	text := title + "\n" + details
	parsed, ok := claudecode.ParseVersionTooOld(text)
	if !ok {
		if !claudecode.IsVersionTooOld(text) {
			return nil
		}
		return ports.NewChatProviderFailure(
			"The installed Claude Code is too old for this model. Run `claude update`, then try again.",
			"", ports.ErrAgentVersionTooOld)
	}
	model = strings.TrimSpace(model)
	if model != "" && !isClaudeNativeModelAlias(model) {
		_ = claudecode.DefaultModelVersionRequirements().Record(model, parsed.Required)
	}
	subject := "this model"
	if model != "" {
		subject = model
	}
	return ports.NewChatProviderFailure(
		fmt.Sprintf("Claude Code %s does not support %s; version %s or newer is required.",
			parsed.Installed, subject, parsed.Required),
		"Run `claude update` (or update the Claude desktop app), then try again.",
		ports.ErrAgentVersionTooOld)
}

func claudeValidateModel(ctx context.Context, plugin claudePlugin, model string) error {
	if isClaudeNativeModelAlias(model) {
		return nil
	}
	if binary, err := plugin.ResolveBinary(ctx); err == nil {
		if reason := claudecode.UnsupportedModelReason(ctx, binary, model); reason != "" {
			return fmt.Errorf("%w: model %q: %s", ports.ErrAgentVersionTooOld, model, reason)
		}
	}
	return nil
}

func claudeServedModel(ctx context.Context, cfg acpdriver.LaunchConfig, sessionID, selected string) (string, string) {
	if !isClaudeNativeModelAlias(selected) {
		return "", ""
	}
	served, err := claudecode.LastAssistantModel(ctx, cfg.Env, sessionID)
	if err != nil || served == "" {
		return "", ""
	}
	return served, fmt.Sprintf("The installed Claude Code resolves %q to %s", selected, served)
}

func claudeNestedMap(meta map[string]any, key string) map[string]any {
	if meta == nil {
		return nil
	}
	value, _ := meta[key].(map[string]any)
	return value
}

func claudeACPLaunchEnv(
	input map[string]string,
	binary string,
	selectedModel string,
	models []ports.AgentModelInfo,
) map[string]string {
	env := make(map[string]string, len(input)+2)
	for key, value := range input {
		env[key] = value
	}
	env["CLAUDE_CODE_EXECUTABLE"] = binary
	if _, chosen := claudeTodoToolsChoice(input); !chosen {
		env[claudeTodoToolsEnv] = "1"
	}
	if strings.TrimSpace(input[claudeEntrypointEnv]) == "" {
		env[claudeEntrypointEnv] = claudeEntrypoint
	}
	if strings.TrimSpace(input[claudeArtifactEnv]) == "" {
		if strings.TrimSpace(os.Getenv(claudeArtifactEnv)) == "" {
			env[claudeArtifactEnv] = "0"
		} else {
			delete(env, claudeArtifactEnv)
		}
	}
	selected := strings.TrimSpace(selectedModel)
	if _, configured := input["ANTHROPIC_CUSTOM_MODEL_OPTION"]; selected != "" &&
		!isClaudeNativeModelAlias(selected) && !configured &&
		strings.TrimSpace(os.Getenv("ANTHROPIC_CUSTOM_MODEL_OPTION")) == "" {
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] = selected
	}
	config, preserve := claudeACPModelConfig(input)
	if preserve {
		return env
	}

	ids := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 && isClaudeNativeModelAlias(selected) {
		return env
	}
	if selected != "" {
		if _, exists := seen[selected]; !exists {
			ids = append(ids, selected)
		}
	}
	if len(ids) == 0 {
		return env
	}

	encodedIDs, err := json.Marshal(ids)
	if err != nil {
		return env
	}
	config["availableModels"] = encodedIDs
	encodedConfig, err := json.Marshal(config)
	if err != nil {
		return env
	}
	env["CLAUDE_MODEL_CONFIG"] = string(encodedConfig)
	return env
}

func isClaudeNativeModelAlias(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "default", "sonnet", "opus", "haiku", "fable", "opus[1m]":
		return true
	default:
		return false
	}
}

func claudeACPModelConfig(input map[string]string) (map[string]json.RawMessage, bool) {
	raw, configured := input["CLAUDE_MODEL_CONFIG"]
	if !configured {
		raw = os.Getenv("CLAUDE_MODEL_CONFIG")
	}
	config := make(map[string]json.RawMessage)
	if strings.TrimSpace(raw) == "" {
		return config, false
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, true
	}
	if config == nil {
		return nil, true
	}
	_, userRestricted := config["availableModels"]
	return config, userRestricted
}

func validateClaudeACPExecutable(binary, goos string) error {
	if goos != "windows" {
		return nil
	}
	switch strings.ToLower(filepath.Ext(binary)) {
	case ".cmd", ".bat":
		return fmt.Errorf("resolved Claude Code command shim %q, but chat requires the native claude.exe; reinstall or update Claude Code", binary)
	default:
		return nil
	}
}

func claudeSessionMeta(cfg acpdriver.LaunchConfig) map[string]any {
	meta := map[string]any{}
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if !cfg.DisableTools && claudeTodoToolsEnabled(cfg.Env) {
		if standing != "" {
			standing += "\n\n"
		}
		standing += claudePlanGuidance
	}
	if standing != "" {
		meta["systemPrompt"] = map[string]any{
			"type": "preset", "preset": "claude_code", "append": standing,
		}
	}
	claudeCode := map[string]any{
		"emitRawSDKMessages": []map[string]string{{"type": "system", "subtype": "session_state_changed"}},
	}
	meta["claudeCode"] = claudeCode
	options := map[string]any{}
	if window := cfg.Env[claudeAutoCompactWindowEnv]; window != "" {
		options["env"] = map[string]string{claudeAutoCompactWindowEnv: window}
	}
	if cfg.DisableTools {
		options["tools"] = []string{}
		options["strictMcpConfig"] = true
		options["disallowedTools"] = []string{"mcp__*"}
	} else if cfg.ReadOnly {
		options["allowedTools"] = claudecode.ReviewerAllowedTools
		options["disallowedTools"] = claudecode.ReviewerDisallowedTools
	}
	if len(options) > 0 {
		claudeCode["options"] = options
	}
	return meta
}

const claudeAutoCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

func claudeSessionMode(permission ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeAcceptEdits:
		return "acceptEdits"
	case ports.PermissionModeAuto:
		return "auto"
	case ports.PermissionModeBypassPermissions:
		return "bypassPermissions"
	default:
		return ""
	}
}

var claudeModelFamily = regexp.MustCompile(`^claude-(opus|sonnet|haiku|fable)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?$`)

func claudeModelName(id string) string {
	match := claudeModelFamily.FindStringSubmatch(id)
	if match == nil {
		return ""
	}
	name := strings.ToUpper(match[1][:1]) + match[1][1:] + " " + match[2]
	if match[3] != "" {
		name += "." + match[3]
	}
	return name
}

func claudeSessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	options := make([]acpdriver.SessionOption, 0, 3)
	if claudeModelFamily.MatchString(settings.Model) {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: settings.Model, Optional: true, Name: claudeModelName(settings.Model)})
	} else if settings.Model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: settings.Model})
	}
	if settings.Effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "effort", Value: settings.Effort})
	}
	return options
}

type runtimeLaunch struct {
	command string
	args    []string
}

func resolveRuntime(ctx context.Context) (runtimeLaunch, error) {
	if err := ctx.Err(); err != nil {
		return runtimeLaunch{}, err
	}
	if command := strings.TrimSpace(os.Getenv("AO_CLAUDE_ACP_COMMAND")); command != "" {
		resolved, err := exec.LookPath(command)
		if err != nil {
			return runtimeLaunch{}, fmt.Errorf("resolve AO_CLAUDE_ACP_COMMAND %q: %w", command, err)
		}
		return runtimeLaunch{command: resolved}, nil
	}

	runtimeDir := strings.TrimSpace(os.Getenv("AO_ACP_RUNTIME_DIR"))
	if runtimeDir == "" {
		runtimeDir = runtimeDirectoryBesideExecutable()
	}
	if runtimeDir == "" {
		return runtimeLaunch{}, errors.New("the Orchestrator ACP runtime is not installed")
	}
	node := filepath.Join(runtimeDir, "node", "bin", "node")
	if runtime.GOOS == "windows" {
		node = filepath.Join(runtimeDir, "node", "node.exe")
	}
	entry := filepath.Join(runtimeDir, "node_modules", "@agentclientprotocol", "claude-agent-acp", "dist", "index.js")
	if err := requireFile(node, "packaged Node runtime"); err != nil {
		return runtimeLaunch{}, err
	}
	if err := requireFile(entry, "claude-agent-acp entrypoint"); err != nil {
		return runtimeLaunch{}, err
	}
	if err := requireNodeVersion(ctx, node); err != nil {
		return runtimeLaunch{}, err
	}
	node, err := jitCapableNode(ctx, node)
	if err != nil {
		return runtimeLaunch{}, err
	}
	return runtimeLaunch{command: node, args: []string{entry}}, nil
}

func runtimeDirectoryBesideExecutable() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	resources := filepath.Dir(filepath.Dir(executable))
	for _, candidate := range []string{
		filepath.Join(resources, "acp-runtime"),
		filepath.Join(resources, "resources", "acp-runtime"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func requireFile(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s at %s: %w", label, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s at %s is a directory", label, path)
	}
	return nil
}

var nodeVersionsChecked sync.Map

func requireNodeVersion(ctx context.Context, node string) error {
	if _, ok := nodeVersionsChecked.Load(node); ok {
		return nil
	}
	out, err := aoprocess.CommandContext(ctx, node, "--version").Output() //nolint:gosec // Resolved local executable, not provider input.
	if err != nil {
		return fmt.Errorf("run packaged Node: %w", err)
	}
	version := strings.TrimSpace(strings.TrimPrefix(string(out), "v"))
	majorText, _, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil || major < minimumNodeMajor {
		return fmt.Errorf("claude-agent-acp requires Node %d+; found %q", minimumNodeMajor, strings.TrimSpace(string(out)))
	}
	nodeVersionsChecked.Store(node, struct{}{})
	return nil
}
