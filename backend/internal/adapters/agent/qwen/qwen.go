package qwen

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoff = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{
		Fields: []ports.ConfigField{
			{
				Key:         "model",
				Type:        ports.ConfigFieldString,
				Description: "Model override passed to `qwen --model`.",
			},
		},
	}, nil
}

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "qwen",
		Name:        "Qwen Code",
		Description: "Run Qwen Code worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.qwenBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	appendApprovalFlags(&cmd, cfg.Permissions)
	appendModelFlag(&cmd, cfg.Config)
	appendToolRestrictionFlags(&cmd, cfg.AllowedTools, cfg.DisallowedTools)

	systemPrompt, err := launchSystemPromptText(cfg)
	if err != nil {
		return nil, err
	}
	if systemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", systemPrompt)
	}

	if cfg.Prompt != "" && cfg.Kind == domain.KindWorker {
		if runtime.GOOS != "windows" {
			return qwenWorkerRemoteInputCommand(cmd, cfg)
		}
		cmd = append(cmd, "-i", cfg.Prompt)
	} else if cfg.Prompt != "" {
		cmd = append(cmd, "-p", cfg.Prompt)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.qwenBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = make([]string, 0, 3)
	cmd = append(cmd, binary)
	appendApprovalFlags(&cmd, cfg.Permissions)
	appendModelFlag(&cmd, cfg.Config)
	appendToolRestrictionFlags(&cmd, cfg.AllowedTools, cfg.DisallowedTools)
	systemPrompt, err := restoreSystemPromptText(cfg)
	if err != nil {
		return nil, false, err
	}
	if systemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", systemPrompt)
	}
	cmd = append(cmd, "--resume", agentSessionID)
	return cmd, true, nil
}

func (p *Plugin) NativeConversationID(
	ctx context.Context,
	session ports.SessionRef,
	currentMode domain.SessionMode,
	providerConversationID string,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if currentMode == domain.SessionModeChat {
		id := strings.TrimSpace(providerConversationID)
		return id, id != "", nil
	}
	id := strings.TrimSpace(session.Metadata[ports.MetadataKeyAgentSessionID])
	return id, id != "", nil
}

func (p *Plugin) NativeConversationExists(
	ctx context.Context,
	_ ports.SessionRef,
	nativeConversationID string,
	env map[string]string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id, valid := canonicalQwenSessionID(nativeConversationID)
	if !valid {
		return false, nil
	}
	qwenHome := strings.TrimSpace(env["QWEN_HOME"])
	if qwenHome == "" {
		qwenHome = strings.TrimSpace(os.Getenv("QWEN_HOME"))
	}
	if qwenHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, fmt.Errorf("qwen: resolve chats root: %w", err)
		}
		qwenHome = filepath.Join(home, ".qwen")
	}

	found := false
	projectsDir := filepath.Join(qwenHome, "projects")
	err := filepath.WalkDir(projectsDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !qwenTranscriptNameMatches(entry.Name(), id) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && info.Size() > 0 {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("qwen: inspect chats root %s: %w", projectsDir, err)
	}
	return found, nil
}

func canonicalQwenSessionID(value string) (string, bool) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func qwenTranscriptNameMatches(name, nativeConversationID string) bool {
	return name == nativeConversationID+".jsonl"
}

func launchSystemPromptText(cfg ports.LaunchConfig) (string, error) {
	return systemPromptTextFrom(cfg.SystemPrompt, cfg.SystemPromptFile)
}

func restoreSystemPromptText(cfg ports.RestoreConfig) (string, error) {
	return systemPromptTextFrom(cfg.SystemPrompt, cfg.SystemPromptFile)
}

func systemPromptTextFrom(inline, file string) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if file == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
	if err != nil {
		return "", fmt.Errorf("qwen: read system prompt file: %w", err)
	}
	return string(data), nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

var qwenBinarySpec = binaryutil.BinarySpec{
	Label:         "qwen",
	Names:         []string{"qwen"},
	WinNames:      []string{"qwen.cmd", "qwen.exe", "qwen"},
	UnixPaths:     []string{"/usr/local/bin/qwen", "/opt/homebrew/bin/qwen"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("qwen"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "qwen.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "qwen.exe"}},
	},
}

func ResolveQwenBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, qwenBinarySpec)
}

func (p *Plugin) qwenBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveQwenBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--approval-mode", "auto-edit")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--approval-mode", "auto")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--approval-mode", "yolo")
	}
}

func appendModelFlag(cmd *[]string, cfg ports.AgentConfig) {
	if model := strings.TrimSpace(cfg.Model); model != "" {
		*cmd = append(*cmd, "--model", model)
	}
}

func appendToolRestrictionFlags(cmd *[]string, allowed, disallowed []string) {
	for _, tool := range allowed {
		if tool = strings.TrimSpace(tool); tool != "" {
			*cmd = append(*cmd, "--core-tools", tool)
		}
	}
	for _, tool := range disallowed {
		if tool = strings.TrimSpace(tool); tool != "" {
			*cmd = append(*cmd, "--exclude-tools", tool)
		}
	}
}

func AppendSessionFlags(cmd *[]string, permissions ports.PermissionMode, model string) {
	appendApprovalFlags(cmd, permissions)
	appendModelFlag(cmd, ports.AgentConfig{Model: model})
}

type qwenSubmitCommand struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func qwenWorkerRemoteInputCommand(qwenCmd []string, cfg ports.LaunchConfig) ([]string, error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("qwen: data dir is required for worker remote input")
	}
	sessionKey := safeQwenSessionKey(cfg.SessionID)
	if sessionKey == "" {
		sessionKey = "worker"
	}
	dir := filepath.Join(cfg.DataDir, "agent-runtime", "qwen", sessionKey)
	inputPath := filepath.Join(dir, sessionKey+".input.jsonl")
	outputPath := filepath.Join(dir, sessionKey+".output.jsonl")
	submitPath := filepath.Join(dir, sessionKey+".submit.jsonl")

	submit, err := json.Marshal(qwenSubmitCommand{Type: "submit", Text: cfg.Prompt})
	if err != nil {
		submit = []byte(`{"type":"submit","text":""}`)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("qwen: create remote input dir: %w", err)
	}
	if err := os.WriteFile(submitPath, append(submit, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("qwen: stage remote input task: %w", err)
	}

	args := append([]string{}, qwenCmd...)
	args = append(args, "--json-file", outputPath, "--input-file", inputPath)

	var script strings.Builder
	script.WriteString("umask 077; : > ")
	script.WriteString(qwenShellQuote(inputPath))
	script.WriteString("; : > ")
	script.WriteString(qwenShellQuote(outputPath))
	script.WriteString("; ( for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40; do ")
	script.WriteString("if grep -q '\"session_start\"' ")
	script.WriteString(qwenShellQuote(outputPath))
	script.WriteString("; then mv -f ")
	script.WriteString(qwenShellQuote(submitPath))
	script.WriteString(" ")
	script.WriteString(qwenShellQuote(inputPath))
	script.WriteString("; exit 0; fi; sleep 0.25; done; mv -f ")
	script.WriteString(qwenShellQuote(submitPath))
	script.WriteString(" ")
	script.WriteString(qwenShellQuote(inputPath))
	script.WriteString(" ) & exec")
	for _, arg := range args {
		script.WriteString(" ")
		script.WriteString(qwenShellQuote(arg))
	}
	return []string{"sh", "-c", script.String()}, nil
}

func safeQwenSessionKey(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range sessionID {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_',
			r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	label := strings.Trim(b.String(), "_")
	if label == "" {
		label = "session"
	}
	return label + "-" + hex.EncodeToString([]byte(sessionID))
}

func qwenShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
