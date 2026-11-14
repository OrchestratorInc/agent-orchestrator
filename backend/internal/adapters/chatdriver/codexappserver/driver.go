package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	clientName          = "agent-orchestrator"
	clientTitle         = "Agent Orchestrator"
	clientVersion       = "0.1.0"
	minimumCodexVersion = "0.146.0"
)

const handshakeTimeout = 60 * time.Second

type codexPlugin interface {
	ResolveBinary(ctx context.Context) (string, error)
	AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error)
}

type process struct {
	stdin         io.WriteCloser
	stdout        io.Reader
	reconnected   bool
	nextRequestID int64
	stop          func() error
	terminate     func() error
}

type spawnFunc func(ctx context.Context, bin, workdir string, env []string) (*process, error)

type versionProbeFunc func(context.Context, string) (string, error)
type persistentConnectFunc func(context.Context, persistenthost.Config) (*persistenthost.Transport, error)

type fixedCodexPlugin string

func (p fixedCodexPlugin) ResolveBinary(context.Context) (string, error) { return string(p), nil }
func (fixedCodexPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusUnknown, nil
}

type Driver struct {
	plugin       codexPlugin
	log          *slog.Logger
	spawn        spawnFunc
	versionProbe versionProbeFunc
	persistent   bool
	connectHost  persistentConnectFunc
}

func New(plugin codexPlugin, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Driver{
		plugin: plugin, log: log, spawn: spawnAppServer,
		versionProbe: installedCodexVersion, persistent: true, connectHost: persistenthost.ConnectOrStart,
	}
}

func DiscoverModels(ctx context.Context, binary, workdir string, env map[string]string) ([]ports.ChatModel, error) {
	driver := New(fixedCodexPlugin(binary), slog.New(slog.DiscardHandler))
	conv, err := driver.connect(ctx, workdir, env, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = conv.Close() }()
	return conv.ListModels(ctx)
}

var _ ports.ChatDriver = (*Driver)(nil)

func (d *Driver) Harness() domain.AgentHarness { return domain.HarnessCodex }

func capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming:   true,
		ports.ChatCapabilityTools:       true,
		ports.ChatCapabilityApprovals:   true,
		ports.ChatCapabilityInterrupt:   true,
		ports.ChatCapabilityResume:      true,
		ports.ChatCapabilityHistory:     true,
		ports.ChatCapabilityUsage:       true,
		ports.ChatCapabilityDiffs:       true,
		ports.ChatCapabilityPlans:       true,
		ports.ChatCapabilityPlanMode:    true,
		ports.ChatCapabilityInteractive: true,
		ports.ChatCapabilityElicitation: true,
		ports.ChatCapabilityModels:      true,
		ports.ChatCapabilityRateLimits:  true,
		ports.ChatCapabilityCompaction:  true,
		ports.ChatCapabilityRollback:    true,
		ports.ChatCapabilityFork:        true,
		ports.ChatCapabilityRename:      true,
		ports.ChatCapabilitySkills:      true,
		ports.ChatCapabilityMCPReload:   true,
		ports.ChatCapabilitySteer:       true,
	}
}

func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	bin, err := d.plugin.ResolveBinary(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}

	versionProbe := d.versionProbe
	if versionProbe == nil {
		versionProbe = installedCodexVersion
	}
	versionCtx, versionCancel := context.WithTimeout(ctx, 5*time.Second)
	versionOutput, versionErr := versionProbe(versionCtx, bin)
	versionCancel()
	if versionErr != nil {
		return nil, fmt.Errorf("%w: read Codex version: %w", ports.ErrChatDriverIncompatible, versionErr)
	}
	installed, ok := parseCodexVersion(versionOutput)
	if !ok {
		return nil, fmt.Errorf("%w: unrecognized Codex version %q (Orchestrator requires %s or newer)",
			ports.ErrChatDriverIncompatible, strings.TrimSpace(versionOutput), minimumCodexVersion)
	}
	minimum, _ := parseCodexVersion(minimumCodexVersion)
	if installed.less(minimum) {
		return nil, fmt.Errorf("%w: Codex %s is older than Orchestrator's tested minimum %s",
			ports.ErrChatDriverIncompatible, installed, minimumCodexVersion)
	}

	workdir, err := os.Getwd()
	if err != nil || !filepath.IsAbs(workdir) {
		workdir = os.TempDir()
	}
	probeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	conv, err := d.connect(probeCtx, workdir, nil, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = conv.Close() }()
	var models struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := conv.conn.request(probeCtx, "model/list", map[string]any{}, &models); err != nil {
		return nil, fmt.Errorf("%w: model/list: %w", ports.ErrChatDriverIncompatible, err)
	}

	return capabilities(), nil
}

func (d *Driver) DiscoverModels(ctx context.Context, workdir string, env map[string]string) ([]ports.ChatModel, error) {
	if !filepath.IsAbs(workdir) {
		var err error
		workdir, err = os.Getwd()
		if err != nil || !filepath.IsAbs(workdir) {
			workdir = os.TempDir()
		}
	}
	conv, err := d.connect(ctx, workdir, env, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = conv.Close() }()
	return listModels(ctx, conv.conn)
}

type codexVersion [3]int

var codexVersionPattern = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

func parseCodexVersion(output string) (codexVersion, bool) {
	match := codexVersionPattern.FindStringSubmatch(output)
	if len(match) != 4 {
		return codexVersion{}, false
	}
	var version codexVersion
	for i := range version {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			return codexVersion{}, false
		}
		version[i] = value
	}
	return version, true
}

func (v codexVersion) less(other codexVersion) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] < other[i]
		}
	}
	return false
}

func (v codexVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

func installedCodexVersion(ctx context.Context, bin string) (string, error) {
	cmd := aoprocess.CommandContext(ctx, bin, "--version")
	cmd.Env = codexProcessEnv(ctx, bin, nil)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func (d *Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if !cfg.ProviderIDsScoped {
		cfg.ProviderScopeID = ""
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", cfg.WorkspacePath)
	}

	conv, reconnected, err := d.connectSession(
		ctx, cfg.SessionID, cfg.DataDir, cfg.WorkspacePath, cfg.Env, cfg.PrepareEnv, cfg.ProviderScopeID, false,
	)
	if err != nil {
		return nil, err
	}
	if reconnected {
		_ = conv.Close()
		return nil, errors.New("persistent chat host already owns a provider conversation for a fresh session")
	}

	policy, sandbox, reviewer := launchApprovalSettings(cfg.Permissions, cfg.ReadOnly)
	conv.readOnly = cfg.ReadOnly
	conv.disableNetwork = cfg.DisableNetwork
	conv.launchMode = cfg.Permissions
	params := map[string]any{
		"cwd":               cfg.WorkspacePath,
		"approvalPolicy":    policy,
		"approvalsReviewer": reviewer,
		"sandbox":           sandbox,
	}
	if cfg.Ephemeral {
		params["ephemeral"] = true
	}
	if cfg.Model != "" {
		params["model"] = cfg.Model
	}
	if config := threadConfig(cfg.Effort, cfg.MCPServers); len(config) > 0 {
		params["config"] = config
	}
	if cfg.SystemPrompt != "" {
		params["developerInstructions"] = cfg.SystemPrompt
	}

	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoningEffort"`
	}
	openCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := conv.conn.request(openCtx, "thread/start", params, &resp); err != nil {
		_ = conv.Terminate()
		return nil, fmt.Errorf("thread/start: %w", err)
	}
	if resp.Thread.ID == "" {
		_ = conv.Terminate()
		return nil, errors.New("thread/start returned no thread id")
	}

	conv.start(resp.Thread.ID, resp.Model, resp.ReasoningEffort)
	return conv, nil
}

func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.ReconnectOnly = true
	return d.Resume(ctx, cfg)
}

func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if !cfg.ProviderIDsScoped {
		cfg.ProviderScopeID = ""
	}
	if cfg.ProviderConversationID == "" {
		return nil, fmt.Errorf("%w: no stored thread id", ports.ErrChatResumeFailed)
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", cfg.WorkspacePath)
	}

	conv, reconnected, err := d.connectSession(
		ctx, cfg.SessionID, cfg.DataDir, cfg.WorkspacePath, cfg.Env, cfg.PrepareEnv, cfg.ProviderScopeID, cfg.ReconnectOnly,
	)
	if err != nil {
		return nil, err
	}
	if reconnected {
		conv.readOnly = cfg.ReadOnly
		conv.launchMode = cfg.Permissions
		conv.tierUnknown, conv.planUnknown = true, true
		conv.start(cfg.ProviderConversationID, cfg.Model, cfg.Effort)
		return conv, nil
	}

	policy, sandbox, reviewer := launchApprovalSettings(cfg.Permissions, cfg.ReadOnly)
	conv.readOnly = cfg.ReadOnly
	conv.launchMode = cfg.Permissions
	params := map[string]any{
		"threadId":          cfg.ProviderConversationID,
		"cwd":               cfg.WorkspacePath,
		"approvalPolicy":    policy,
		"approvalsReviewer": reviewer,
		"sandbox":           sandbox,
	}
	if cfg.Model != "" {
		params["model"] = cfg.Model
	}
	if config := threadConfig(cfg.Effort, cfg.MCPServers); len(config) > 0 {
		params["config"] = config
	}
	if cfg.SystemPrompt != "" {
		params["developerInstructions"] = cfg.SystemPrompt
	}
	resumeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	var resp struct {
		Model           string  `json:"model"`
		ReasoningEffort string  `json:"reasoningEffort"`
		ServiceTier     *string `json:"serviceTier"`
	}
	err = conv.conn.request(resumeCtx, "thread/resume", params, &resp)
	if err != nil && cfg.FreshIfMissing && codexThreadMissing(err) {
		delete(params, "threadId")
		var started struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoningEffort"`
		}
		if err = conv.conn.request(resumeCtx, "thread/start", params, &started); err == nil && started.Thread.ID == "" {
			err = errors.New("thread/start returned no thread id")
		}
		if err != nil {
			_ = conv.Terminate()
			return nil, fmt.Errorf("%w: start in place of a missing thread: %w", ports.ErrChatResumeFailed, err)
		}
		conv.start(started.Thread.ID, started.Model, started.ReasoningEffort)
		return conv, nil
	}
	if err != nil {
		_ = conv.Terminate()
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}

	conv.fastTier = resp.ServiceTier != nil && conv.configuredServiceTier(ctx) == ""
	conv.planUnknown = true
	conv.start(cfg.ProviderConversationID, resp.Model, resp.ReasoningEffort)
	return conv, nil
}

func codexThreadMissing(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no rollout found") ||
		strings.Contains(message, "thread not found") ||
		(strings.Contains(message, "rollout") && strings.Contains(message, "is empty"))
}

const aoToolTimeoutSec = 150

func threadConfig(effort string, servers []ports.ChatMCPServerConfig) map[string]any {
	config := map[string]any{}
	if effort != "" {
		config["model_reasoning_effort"] = effort
	}
	if len(servers) == 0 {
		return config
	}
	mcpServers := make(map[string]any, len(servers))
	for _, server := range servers {
		entry := map[string]any{}
		if server.Name == "ao" {
			entry["default_tools_approval_mode"] = "approve"
			entry["tool_timeout_sec"] = aoToolTimeoutSec
		}
		if server.Type == "http" {
			entry["url"] = server.URL
			if len(server.Headers) > 0 {
				entry["http_headers"] = server.Headers
			}
		} else {
			entry["command"] = server.Command
			if len(server.Args) > 0 {
				entry["args"] = server.Args
			}
			if len(server.Env) > 0 {
				entry["env"] = server.Env
			}
		}
		mcpServers[server.Name] = entry
	}
	config["mcp_servers"] = mcpServers
	return config
}

func (d *Driver) connect(ctx context.Context, workdir string, env map[string]string, providerScopeID string) (*conversation, error) {
	bin, err := d.plugin.ResolveBinary(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}

	proc, err := d.spawn(ctx, bin, workdir, codexProcessEnv(ctx, bin, env))
	if err != nil {
		return nil, fmt.Errorf("%w: launch app-server: %w", ports.ErrChatDriverUnavailable, err)
	}

	conv := newConversation(proc, d.log, providerScopeID)
	if err := d.initialize(ctx, conv); err != nil {
		_ = conv.Close()
		return nil, err
	}
	return conv, nil
}

func (d *Driver) connectSession(
	ctx context.Context,
	sessionID domain.SessionID,
	dataDir, workdir string,
	env map[string]string,
	prepareEnv func(context.Context) (map[string]string, error),
	providerScopeID string,
	reconnectOnly bool,
) (*conversation, bool, error) {
	if !d.persistent {
		if reconnectOnly {
			return nil, false, ports.ErrChatHostNotRunning
		}
		if prepareEnv != nil {
			var err error
			env, err = prepareEnv(ctx)
			if err != nil {
				return nil, false, err
			}
		}
		conv, err := d.connect(ctx, workdir, env, providerScopeID)
		return conv, false, err
	}
	var bin string
	if !reconnectOnly {
		var err error
		bin, err = d.plugin.ResolveBinary(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
		}
	}
	hostConfig := persistenthost.Config{
		SessionID:     string(sessionID),
		ReconnectOnly: reconnectOnly,
		DataDir:       dataDir,
		Workdir:       workdir,
		Env:           envSlice(env),
		Argv:          []string{bin, "app-server"},
	}
	if prepareEnv != nil {
		hostConfig.Prepare = func(prepareCtx context.Context) (persistenthost.PreparedProvider, error) {
			preparedEnv, prepareErr := prepareEnv(prepareCtx)
			if prepareErr != nil {
				return persistenthost.PreparedProvider{}, prepareErr
			}
			return persistenthost.PreparedProvider{
				Env: envSlice(preparedEnv), Argv: []string{bin, "app-server"},
			}, nil
		}
	}
	transport, err := d.connectHost(ctx, hostConfig)
	if err != nil {
		if errors.Is(err, persistenthost.ErrNotRunning) {
			return nil, false, ports.ErrChatHostNotRunning
		}
		if errors.Is(err, persistenthost.ErrOwnershipInconclusive) ||
			errors.Is(err, persistenthost.ErrAttached) ||
			errors.Is(err, persistenthost.ErrIncompatible) ||
			errors.Is(err, persistenthost.ErrUnauthorized) {
			return nil, false, fmt.Errorf("%w: persistent host: %w", ports.ErrChatRecoveryInconclusive, err)
		}
		return nil, false, fmt.Errorf("%w: persistent host: %w", ports.ErrChatDriverUnavailable, err)
	}
	proc := &process{
		stdin:         transport.Stdin,
		stdout:        transport.Stdout,
		reconnected:   transport.Reconnected,
		nextRequestID: transport.NextRequestID,
		stop:          transport.Stdin.Close,
		terminate: func() error {
			_ = transport.Stdin.Close()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return persistenthost.Shutdown(shutdownCtx, dataDir, string(sessionID))
		},
	}
	conv := newConversation(proc, d.log, providerScopeID)
	if transport.Reconnected {
		fresh, err := initializeIfNeeded(ctx, conv.conn)
		if err != nil {
			_ = conv.Close()
			return nil, false, err
		}
		if !fresh {
			return conv, true, nil
		}
		proc.reconnected = false
		return conv, false, nil
	}
	if err := d.initialize(ctx, conv); err != nil {
		_ = conv.Terminate()
		return nil, false, err
	}
	return conv, false, nil
}

func (d *Driver) initialize(ctx context.Context, conv *conversation) error {
	return initializeConnection(ctx, conv.conn)
}

func initializeIfNeeded(ctx context.Context, connection *conn) (bool, error) {
	err := initializeConnection(ctx, connection)
	var refused *rpcError
	if errors.As(err, &refused) && refused.Code == -32600 && strings.EqualFold(refused.Message, "Already initialized") {
		return false, nil
	}
	return err == nil, err
}

func initializeConnection(ctx context.Context, connection *conn) error {
	initCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := connection.request(initCtx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": clientName, "title": clientTitle, "version": clientVersion},
		"capabilities": map[string]any{"experimentalApi": true, "optOutNotificationMethods": nil},
	}, nil); err != nil {
		return fmt.Errorf("%w: initialize: %w", ports.ErrChatDriverIncompatible, err)
	}
	if err := connection.notify("initialized", nil); err != nil {
		return fmt.Errorf("notify initialized: %w", err)
	}
	return nil
}

func approvalSettings(mode ports.PermissionMode) (policy, sandbox string) {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		return "on-request", "workspace-write"
	default:
		return "never", "danger-full-access"
	}
}

func approvalReviewer(mode ports.PermissionMode) string {
	if ports.NormalizePermissionMode(mode) == ports.PermissionModeAuto {
		return "auto_review"
	}
	return "user"
}

func (c *conversation) SandboxAllowsNetwork(turnMode ports.PermissionMode) bool {
	if c.disableNetwork {
		return false
	}
	if c.readOnly {
		return true
	}
	if turnMode == "" {
		turnMode = c.launchMode
	}
	_, sandbox := approvalSettings(turnMode)
	return sandbox == "danger-full-access"
}

func launchApprovalSettings(mode ports.PermissionMode, readOnly bool) (policy, sandbox, reviewer string) {
	if readOnly {
		return "never", "read-only", "user"
	}
	policy, sandbox = approvalSettings(mode)
	return policy, sandbox, approvalReviewer(mode)
}

func spawnAppServer(ctx context.Context, bin, workdir string, env []string) (*process, error) {
	args := []string{"app-server"}
	cmd := aoprocess.Command(bin, args...)
	cmd.Dir = workdir
	if len(env) > 0 {
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s app-server: %w", bin, err)
	}

	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	var stopped bool
	return &process{
		stdin:  stdin,
		stdout: stdout,
		stop: func() error {
			if stopped {
				return nil
			}
			stopped = true
			_ = stdin.Close()
			done := make(chan struct{})
			go func() { _, _ = cmd.Process.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
			}
			return nil
		},
	}, nil
}

func envSlice(env map[string]string) []string {
	return processenv.Merge(env)
}

func codexProcessEnv(ctx context.Context, bin string, env map[string]string) []string {
	overlay := make(map[string]string, len(env)+1)
	for key, value := range env {
		overlay[key] = value
	}
	if _, ok := overlay["PATH"]; !ok {
		overlay["PATH"] = os.Getenv("PATH")
	}
	agentlaunch.AugmentRuntimePATHForLaunchBinary(ctx, overlay, []string{bin}, exec.LookPath, agentlaunch.PinnedDir(agentlaunch.Executable, overlay["AO_DATA_DIR"]))
	return envSlice(overlay)
}

func (d *Driver) ForkIntoHost(ctx context.Context, sourceProviderConversationID, lastProviderTurnID string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if sourceProviderConversationID == "" || lastProviderTurnID == "" {
		return nil, errors.New("source thread and exact turn anchor are required")
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", cfg.WorkspacePath)
	}
	conv, reconnected, err := d.connectSession(ctx, cfg.SessionID, cfg.DataDir, cfg.WorkspacePath, cfg.Env, cfg.PrepareEnv, cfg.ProviderScopeID, false)
	if err != nil {
		return nil, err
	}
	if reconnected {
		_ = conv.Close()
		return nil, errors.New("side provider host already owns a conversation")
	}
	policy, sandbox, reviewer := launchApprovalSettings(cfg.Permissions, cfg.ReadOnly)
	conv.readOnly = cfg.ReadOnly
	conv.disableNetwork = cfg.DisableNetwork
	conv.launchMode = cfg.Permissions
	params := map[string]any{
		"threadId":          sourceProviderConversationID,
		"cwd":               cfg.WorkspacePath,
		"approvalPolicy":    policy,
		"approvalsReviewer": reviewer,
		"sandbox":           sandbox,
	}
	params["lastTurnId"] = conv.nativeID(lastProviderTurnID)
	params["excludeTurns"] = true
	if cfg.Model != "" {
		params["model"] = cfg.Model
	}
	if config := threadConfig(cfg.Effort, cfg.MCPServers); len(config) > 0 {
		params["config"] = config
	}
	if cfg.SystemPrompt != "" {
		params["developerInstructions"] = cfg.SystemPrompt
	}
	openCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoningEffort"`
	}
	if err := conv.conn.request(openCtx, "thread/fork", params, &resp); err != nil {
		_ = conv.Terminate()
		return nil, fmt.Errorf("thread/fork in side host: %w", err)
	}
	if resp.Thread.ID == "" || resp.Thread.ID == sourceProviderConversationID {
		_ = conv.Terminate()
		return nil, errors.New("thread/fork returned no independent thread id")
	}
	if cfg.RecordFork != nil {
		if err := cfg.RecordFork(context.WithoutCancel(ctx), resp.Thread.ID); err != nil {
			_ = conv.Terminate()
			return nil, fmt.Errorf("record Codex fork: %w", err)
		}
	}
	conv.start(resp.Thread.ID, resp.Model, resp.ReasoningEffort)
	return conv, nil
}
