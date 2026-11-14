package agentruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type Harness string

const (
	HarnessClaudeCode Harness = "claude-code"
	HarnessCodex      Harness = "codex"
	HarnessCursor     Harness = "cursor"
	HarnessOpenCode   Harness = "opencode"
)

type PermissionPolicy string

const (
	PermissionDefault           PermissionPolicy = "default"
	PermissionAcceptEdits       PermissionPolicy = "accept-edits"
	PermissionAuto              PermissionPolicy = "auto"
	PermissionBypassPermissions PermissionPolicy = "bypass-permissions"
)

type SessionMode string

const (
	SessionModeReadOnly SessionMode = "read-only"
	SessionModeStandard SessionMode = "standard"
	SessionModeTrusted  SessionMode = "trusted"
)

const MetadataKeyAgentSessionID = "agentSessionId"

var claudeSessionNamespace = uuid.MustParse("a1f0c3d2-7b54-4e96-8a2b-0d9e1f2a3b4c")

type LaunchConfig struct {
	Harness          Harness
	Binary           string
	SessionID        string
	NativeSessionID  string
	WorkspacePath    string
	Model            string
	Effort           string
	Prompt           string
	SystemPrompt     string
	SystemPromptFile string
	Permission       PermissionPolicy
	AllowedTools     []string
	DisallowedTools  []string
	ProviderArgs     []string
}

type RestoreConfig struct {
	Harness          Harness
	Binary           string
	SessionID        string
	Metadata         map[string]string
	WorkspacePath    string
	Model            string
	Effort           string
	Prompt           string
	SystemPrompt     string
	SystemPromptFile string
	Permission       PermissionPolicy
	AllowedTools     []string
	DisallowedTools  []string
	ProviderArgs     []string
}

func BuildLaunchCommand(cfg LaunchConfig) ([]string, error) {
	if strings.TrimSpace(cfg.Binary) == "" {
		return nil, errors.New("agentruntime: binary is required")
	}
	switch cfg.Harness {
	case HarnessClaudeCode:
		return buildClaudeLaunch(cfg)
	case HarnessCodex:
		return buildCodexLaunch(cfg), nil
	case HarnessCursor:
		return buildCursorLaunch(cfg), nil
	case HarnessOpenCode:
		return buildOpencodeLaunch(cfg), nil
	default:
		return nil, fmt.Errorf("agentruntime: unsupported harness %q", cfg.Harness)
	}
}

func BuildRestoreCommand(cfg RestoreConfig) ([]string, bool, error) {
	if strings.TrimSpace(cfg.Binary) == "" {
		return nil, false, errors.New("agentruntime: binary is required")
	}
	identity, ok := RestoreIdentity(cfg.Harness, cfg.SessionID, cfg.Metadata)
	if !ok {
		return nil, false, nil
	}
	switch cfg.Harness {
	case HarnessClaudeCode:
		cmd, err := buildClaudeRestore(cfg, identity)
		return cmd, err == nil, err
	case HarnessCodex:
		return buildCodexRestore(cfg, identity), true, nil
	case HarnessCursor:
		return buildCursorRestore(cfg, identity), true, nil
	case HarnessOpenCode:
		return buildOpencodeRestore(cfg, identity), true, nil
	default:
		return nil, false, fmt.Errorf("agentruntime: unsupported harness %q", cfg.Harness)
	}
}

func RestoreIdentity(harness Harness, sessionID string, metadata map[string]string) (string, bool) {
	if identity := strings.TrimSpace(metadata[MetadataKeyAgentSessionID]); identity != "" {
		return identity, true
	}
	if harness == HarnessClaudeCode && strings.TrimSpace(sessionID) != "" {
		return ClaudeSessionID(sessionID), true
	}
	return "", false
}

func ClaudeSessionID(sessionID string) string {
	return uuid.NewSHA1(claudeSessionNamespace, []byte(sessionID)).String()
}

func NormalizePermissionPolicy(policy PermissionPolicy) PermissionPolicy {
	switch policy {
	case PermissionDefault, PermissionAcceptEdits, PermissionAuto, PermissionBypassPermissions:
		return policy
	default:
		return PermissionDefault
	}
}

func PermissionPolicyForMode(mode SessionMode) PermissionPolicy {
	switch mode {
	case SessionModeStandard:
		return PermissionAuto
	case SessionModeTrusted:
		return PermissionBypassPermissions
	default:
		return PermissionDefault
	}
}

func ClaudePermissionArgs(policy PermissionPolicy) []string {
	switch NormalizePermissionPolicy(policy) {
	case PermissionAcceptEdits:
		return []string{"--permission-mode", "acceptEdits"}
	case PermissionAuto:
		return []string{"--permission-mode", "auto"}
	case PermissionBypassPermissions:
		return []string{"--permission-mode", "bypassPermissions"}
	default:
		return nil
	}
}

func CodexPermissionArgs(policy PermissionPolicy) []string {
	switch NormalizePermissionPolicy(policy) {
	case PermissionAcceptEdits:
		return []string{"--ask-for-approval", "on-request"}
	case PermissionAuto:
		return []string{"--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
	default:
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	}
}

func CursorPermissionArgs(policy PermissionPolicy) []string {
	switch NormalizePermissionPolicy(policy) {
	case PermissionAuto:
		return []string{"--force"}
	case PermissionBypassPermissions:
		return []string{"--yolo"}
	default:
		return nil
	}
}

func buildClaudeLaunch(cfg LaunchConfig) ([]string, error) {
	cmd := []string{cfg.Binary}
	if cfg.NativeSessionID != "" {
		nativeID, err := uuid.Parse(strings.TrimSpace(cfg.NativeSessionID))
		if err != nil {
			return nil, fmt.Errorf("claude-code: invalid native session id: %w", err)
		}
		cmd = append(cmd, "--session-id", nativeID.String())
	} else if cfg.SessionID != "" {
		cmd = append(cmd, "--session-id", ClaudeSessionID(cfg.SessionID))
	}
	cmd = append(cmd, ClaudePermissionArgs(cfg.Permission)...)
	cmd = appendClaudeToolArgs(cmd, cfg.AllowedTools, cfg.DisallowedTools)
	cmd = append(cmd, cfg.ProviderArgs...)
	if model := strings.TrimSpace(cfg.Model); model != "" {
		cmd = append(cmd, "--model", model)
	}
	if effort := strings.TrimSpace(cfg.Effort); effort != "" {
		cmd = append(cmd, "--effort", effort)
	}
	var err error
	cmd, err = appendClaudeSystemPrompt(cmd, cfg.SystemPromptFile, cfg.SystemPrompt)
	if err != nil {
		return nil, err
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd, nil
}

func buildClaudeRestore(cfg RestoreConfig, identity string) ([]string, error) {
	cmd := []string{cfg.Binary}
	cmd = append(cmd, ClaudePermissionArgs(cfg.Permission)...)
	cmd = appendClaudeToolArgs(cmd, cfg.AllowedTools, cfg.DisallowedTools)
	cmd = append(cmd, cfg.ProviderArgs...)
	if model := strings.TrimSpace(cfg.Model); model != "" {
		cmd = append(cmd, "--model", model)
	}
	if effort := strings.TrimSpace(cfg.Effort); effort != "" {
		cmd = append(cmd, "--effort", effort)
	}
	var err error
	cmd, err = appendClaudeSystemPrompt(cmd, cfg.SystemPromptFile, cfg.SystemPrompt)
	if err != nil {
		return nil, err
	}
	cmd = append(cmd, "--resume", identity)
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd, nil
}

func appendClaudeToolArgs(cmd, allowed, disallowed []string) []string {
	if len(allowed) > 0 {
		cmd = append(cmd, "--allowedTools", strings.Join(allowed, ","))
	}
	if len(disallowed) > 0 {
		cmd = append(cmd, "--disallowedTools", strings.Join(disallowed, ","))
	}
	return cmd
}

func appendClaudeSystemPrompt(cmd []string, promptFile, prompt string) ([]string, error) {
	if promptFile != "" {
		info, err := os.Stat(promptFile)
		if err != nil {
			return nil, fmt.Errorf("claude-code: inspect system prompt file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("claude-code: system prompt file %q is not a regular file", promptFile)
		}
		file, err := os.Open(promptFile) //nolint:gosec // path is host-owned launch config
		if err != nil {
			return nil, fmt.Errorf("claude-code: open system prompt file: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("claude-code: close system prompt file: %w", err)
		}
		return append(cmd, "--append-system-prompt-file", promptFile), nil
	}
	if prompt != "" {
		cmd = append(cmd, "--append-system-prompt", prompt)
	}
	return cmd, nil
}

func buildCodexLaunch(cfg LaunchConfig) []string {
	cmd := codexBaseCommand(cfg.Binary, cfg.Permission, cfg.ProviderArgs)
	cmd = appendCodexCommon(cmd, cfg.WorkspacePath, cfg.Model, cfg.SystemPromptFile, cfg.SystemPrompt)
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd
}

func buildCodexRestore(cfg RestoreConfig, identity string) []string {
	cmd := append([]string{cfg.Binary, "resume"}, codexBaseArgs(cfg.Permission, cfg.ProviderArgs)...)
	cmd = appendCodexCommon(cmd, cfg.WorkspacePath, cfg.Model, cfg.SystemPromptFile, cfg.SystemPrompt)
	cmd = append(cmd, identity)
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd
}

func codexBaseCommand(binary string, policy PermissionPolicy, providerArgs []string) []string {
	return append([]string{binary}, codexBaseArgs(policy, providerArgs)...)
}

func codexBaseArgs(policy PermissionPolicy, providerArgs []string) []string {
	args := []string{
		"-c", "check_for_update_on_startup=false",
		"-c", "notice.hide_rate_limit_model_nudge=true",
		"--dangerously-bypass-hook-trust",
	}
	args = append(args, CodexPermissionArgs(policy)...)
	return append(args, providerArgs...)
}

func appendCodexCommon(cmd []string, workspace, model, promptFile, prompt string) []string {
	cmd = append(cmd, CodexWorkspaceTrustArgs(workspace)...)
	if model = strings.TrimSpace(model); model != "" {
		cmd = append(cmd, "--model", model)
	}
	if promptFile != "" {
		return append(cmd, "-c", "model_instructions_file="+promptFile)
	}
	if prompt != "" {
		cmd = append(cmd, "-c", "developer_instructions="+codexTOMLString(prompt))
	}
	return cmd
}

func CodexWorkspaceTrustArgs(workspace string) []string {
	path := strings.TrimSpace(workspace)
	if path == "" {
		return nil
	}
	keys := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		keys = append(keys, resolved)
	}
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, codexTOMLString(key)+`={trust_level="trusted"}`)
	}
	return []string{"-c", "projects={" + strings.Join(entries, ",") + "}"}
}

func codexTOMLString(value string) string {
	if !containsTOMLControl(value) && !strings.Contains(value, "'") {
		return "'" + value + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func containsTOMLControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func buildCursorLaunch(cfg LaunchConfig) []string {
	cmd := []string{cfg.Binary}
	cmd = append(cmd, CursorPermissionArgs(cfg.Permission)...)
	cmd = append(cmd, cfg.ProviderArgs...)
	if model := strings.TrimSpace(cfg.Model); model != "" {
		cmd = append(cmd, "--model", model)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd
}

func buildCursorRestore(cfg RestoreConfig, identity string) []string {
	cmd := []string{cfg.Binary}
	cmd = append(cmd, CursorPermissionArgs(cfg.Permission)...)
	cmd = append(cmd, cfg.ProviderArgs...)
	if model := strings.TrimSpace(cfg.Model); model != "" {
		cmd = append(cmd, "--model", model)
	}
	return append(cmd, "--resume", identity)
}

func OpenCodePermissionArgs(policy PermissionPolicy) []string {
	switch NormalizePermissionPolicy(policy) {
	case PermissionAuto, PermissionBypassPermissions:
		return []string{"--auto"}
	default:
		return nil
	}
}

func buildOpencodeLaunch(cfg LaunchConfig) []string {
	cmd := []string{cfg.Binary}
	cmd = append(cmd, OpenCodePermissionArgs(cfg.Permission)...)
	cmd = append(cmd, cfg.ProviderArgs...)
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
	}
	return cmd
}

func buildOpencodeRestore(cfg RestoreConfig, identity string) []string {
	cmd := []string{cfg.Binary}
	cmd = append(cmd, OpenCodePermissionArgs(cfg.Permission)...)
	cmd = append(cmd, cfg.ProviderArgs...)
	cmd = append(cmd, "--session", identity)
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
	}
	return cmd
}
