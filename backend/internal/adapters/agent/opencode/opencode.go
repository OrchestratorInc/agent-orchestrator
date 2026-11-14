package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"

	_ "modernc.org/sqlite"
)

const (
	adapterID = "opencode"

	opencodeAgentSessionIDMetadataKey = "agentSessionId"
)

var opencodeUnixPaths = []string{
	"/usr/local/bin/opencode",
	"/opt/homebrew/bin/opencode",
}

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
	importBinary   string
}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.SemanticMessageAcceptanceSignaler = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "opencode",
		Description: "Run opencode worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `opencode --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := ResolveBinaryForMajor(ctx, 1)
	if err != nil {
		return nil, err
	}

	envPrefix, agentName, err := opencodeConfigEnvPrefix(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.SessionID)
	if err != nil {
		return nil, err
	}
	cmd = envPrefix
	cmd = append(cmd, binary)
	appendPermissionFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if agentName != "" {
		cmd = append(cmd, "--agent", agentName)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
	}
	return cmd, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[opencodeAgentSessionIDMetadataKey])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := ResolveBinaryForMajor(ctx, 1)
	if err != nil {
		return nil, false, err
	}

	envPrefix, agentName, err := opencodeConfigEnvPrefix(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Session.ID)
	if err != nil {
		return nil, false, err
	}
	cmd = envPrefix
	cmd = append(cmd, binary)
	appendPermissionFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if agentName != "" {
		cmd = append(cmd, "--agent", agentName)
	}
	cmd = append(cmd, "--session", agentSessionID)
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
	}
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.opencodeBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	status, ok, storedErr := opencodeStoredAuthStatus(ctx)
	if storedErr == nil && ok && status.HasCredential() {
		return status, nil
	}
	if storedErr == nil {
		listed, err := opencodeAuthListStatus(ctx, binary)
		if err != nil {
			return ports.AgentAuthStatusUnknown, err
		}
		if listed == ports.AgentAuthStatusAuthorized {
			return listed, nil
		}
	}
	if envStatus, envOK := opencodeEnvAuthStatus(ctx); envOK {
		return envStatus, nil
	}
	if storedErr != nil {
		return ports.AgentAuthStatusUnknown, storedErr
	}
	if ok {
		return status, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func opencodeAuthListStatus(ctx context.Context, binary string) (status ports.AgentAuthStatus, resultErr error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	started := time.Now()
	out, err := authprobe.CmdRunner(probeCtx, binary, "auth", "list")
	noCredentials := false
	defer func() {
		step := authprobe.CommandStep(binary, []string{"auth", "list"}, status, err, probeCtx.Err(), started)
		if noCredentials && probeCtx.Err() == nil {
			source := step.Source
			step = domain.NewAgentAuthDiagnostic(step.Method, "credential_missing")
			step.Source = source
		}
		ports.RecordAuthDiagnostic(ctx, step)
	}()
	if probeCtx.Err() != nil {
		if probeCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return ports.AgentAuthStatusUnknown, nil
		}
		return ports.AgentAuthStatusUnknown, probeCtx.Err()
	}
	text := strings.ToLower(string(out))
	if strings.Contains(text, "0 credentials") || strings.Contains(text, "no credentials") || strings.Contains(text, "not authenticated") {
		noCredentials = true
		return ports.AgentAuthStatusUnknown, nil
	}
	if strings.Contains(text, "credential") && err == nil {
		ports.RecordAuthEvidence(ctx, domain.CommandSource(binary, "auth", "list"))
		return ports.AgentAuthStatusAuthorized, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

var opencodeAPIKeyEnvVars = []string{
	"OPENCODE_API_KEY",
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"OPENROUTER_API_KEY",
	"DEEPSEEK_API_KEY",
	"GROQ_API_KEY",
	"XAI_API_KEY",
	"MISTRAL_API_KEY",
	"COHERE_API_KEY",
}

func opencodeEnvAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool) {
	if authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, opencodeAPIKeyEnvVars...) != "" {
		return ports.AgentAuthStatusAuthorized, true
	}
	return ports.AgentAuthStatusUnknown, false
}

func opencodeStoredAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	dataDir, ok := opencodeDataDir()
	if !ok {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	authPath := filepath.Join(dataDir, "auth.json")
	jsonStatus, jsonOK, data, err := opencodeAuthJSONStatus(ctx, authPath)
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if jsonOK && jsonStatus == ports.AgentAuthStatusAuthorized {
		evidence := authprobe.FileSource(authPath).WithMethod(authprobe.JSONProviderMethod(data))
		evidence.Provider = authprobe.JSONProviderNames(data)
		ports.RecordAuthEvidence(ctx, evidence)
		return jsonStatus, true, nil
	}
	dbPath := filepath.Join(dataDir, "opencode.db")
	if status, ok, err := opencodeDBAuthStatus(ctx, dbPath); err != nil || ok {
		if status == ports.AgentAuthStatusAuthorized {
			ports.RecordAuthEvidence(ctx, authprobe.FileSource(dbPath).WithMethod(domain.AgentAccountMethodAccount))
		}
		return status, ok, err
	}
	if jsonOK {
		return jsonStatus, true, nil
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

func opencodeDataDir() (string, bool) {
	if dataDir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); dataDir != "" {
		return dataDir, true
	}
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return filepath.Join(dataHome, "opencode"), true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".local", "share", "opencode"), true
}

func opencodeAuthJSONStatus(ctx context.Context, path string) (ports.AgentAuthStatus, bool, []byte, error) {
	data, err := authprobe.ReadCredentialFile(ctx, path)
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, false, nil, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return ports.AgentAuthStatusUnknown, true, data, nil
	}

	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_file_invalid")
		return ports.AgentAuthStatusUnknown, false, nil, err
	}
	if len(entries) == 0 {
		return ports.AgentAuthStatusUnknown, true, data, nil
	}
	for key, value := range entries {
		if strings.TrimSpace(key) == "" {
			continue
		}
		trimmed := strings.TrimSpace(string(value))
		if trimmed != "" && trimmed != "null" && trimmed != "{}" {
			return ports.AgentAuthStatusAuthorized, true, data, nil
		}
	}
	return ports.AgentAuthStatusUnknown, true, data, nil
}

func opencodeDBAuthStatus(ctx context.Context, path string) (status ports.AgentAuthStatus, ok bool, err error) {
	defer func() {
		if err != nil && ctx.Err() == nil {
			authprobe.RecordFileDiagnostic(ctx, path, "credential_store_unreadable")
		}
	}()
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_file_missing")
		return ports.AgentAuthStatusUnknown, false, nil
	} else if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	defer func() {
		_ = db.Close()
	}()

	authorized, known, err := opencodeDBHasAuthorizedAccount(ctx, db)
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if !known {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if authorized {
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return ports.AgentAuthStatusUnknown, true, nil
}

func opencodeDBHasAuthorizedAccount(ctx context.Context, db *sql.DB) (authorized, known bool, err error) {
	for _, query := range []string{
		`SELECT COUNT(*) FROM credential WHERE active = 1 AND trim(value) != ''`,
		`SELECT COUNT(*) FROM account_state WHERE active_account_id IS NOT NULL AND trim(active_account_id) != ''`,
		`SELECT COUNT(*) FROM account WHERE trim(access_token) != ''`,
		`SELECT COUNT(*) FROM control_account WHERE active = 1 AND trim(access_token) != ''`,
	} {
		count, err := opencodeDBCount(ctx, db, query)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "no such table") {
				continue
			}
			return false, false, err
		}
		known = true
		if count > 0 {
			return true, true, nil
		}
	}
	return false, known, nil
}

func opencodeDBCount(ctx context.Context, db *sql.DB, query string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func appendPermissionFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--auto")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--dangerously-skip-permissions")
	}
}

const opencodeConfigEnvVar = "OPENCODE_CONFIG"

type opencodeInlineConfig struct {
	Schema     string                           `json:"$schema,omitempty"`
	Permission map[string]string                `json:"permission,omitempty"`
	Agent      map[string]opencodeAgentSettings `json:"agent,omitempty"`
}

type opencodeAgentSettings struct {
	Mode       string `json:"mode,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Permission any    `json:"permission,omitempty"`
}

func opencodePermissionConfig(mode ports.PermissionMode) map[string]string {
	if ports.NormalizePermissionMode(mode) == ports.PermissionModeAcceptEdits {
		return map[string]string{"edit": "allow"}
	}
	return nil
}

func opencodeConfigEnvPrefix(permissions ports.PermissionMode, inlinePrompt, promptFile, sessionID string) ([]string, string, error) {
	permission := opencodePermissionConfig(permissions)
	var agentPermission any
	if ports.NormalizePermissionMode(permissions) == ports.PermissionModeBypassPermissions {
		agentPermission, permission = "allow", nil
	}
	if promptFile == "" {
		if inlinePrompt != "" {
			return nil, "", fmt.Errorf("opencode: system prompt file required to build agent config")
		}
		return nil, "", nil
	}
	dir := filepath.Dir(promptFile)
	configPath := filepath.Join(dir, "opencode.json")
	agentName := opencodeAOAgentName(sessionID)
	prompt := inlinePrompt
	if prompt == "" {
		prompt = "{file:./" + filepath.Base(promptFile) + "}"
	}
	config := opencodeInlineConfig{
		Schema:     "https://opencode.ai/config.json",
		Permission: permission,
		Agent: map[string]opencodeAgentSettings{
			agentName: {Mode: "primary", Prompt: prompt, Permission: agentPermission},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, "", err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("opencode: create prompt config dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(configPath, data, 0o600); err != nil {
		return nil, "", fmt.Errorf("opencode: write prompt config: %w", err)
	}
	return []string{"env", opencodeConfigEnvVar + "=" + configPath}, agentName, nil
}

func acpAgentForPermissions(permissions ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeAcceptEdits:
		return "ao-accept-edits"
	case ports.PermissionModeAuto:
		return "ao-auto"
	case ports.PermissionModeBypassPermissions:
		return "ao-bypass"
	default:
		return "ao-default"
	}
}

func PrepareACPConfigContent(
	existing, systemPrompt string,
	permissions ports.PermissionMode,
) (string, error) {
	config := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &config); err != nil {
			return "", fmt.Errorf("opencode: decode OPENCODE_CONFIG_CONTENT: %w", err)
		}
	}
	if _, ok := config["$schema"]; !ok {
		config["$schema"] = "https://opencode.ai/config.json"
	}
	agents, ok := config["agent"].(map[string]any)
	if config["agent"] != nil && !ok {
		return "", fmt.Errorf("opencode: OPENCODE_CONFIG_CONTENT agent must be an object")
	}
	if agents == nil {
		agents = map[string]any{}
	}
	for _, mode := range []ports.PermissionMode{
		ports.PermissionModeDefault, ports.PermissionModeAcceptEdits,
		ports.PermissionModeAuto, ports.PermissionModeBypassPermissions,
	} {
		agent := opencodeAgentSettings{Mode: "primary", Prompt: systemPrompt}
		if mode == ports.PermissionModeBypassPermissions {
			agent.Permission = "allow"
		}
		agents[acpAgentForPermissions(mode)] = agent
	}
	config["agent"] = agents
	config["default_agent"] = acpAgentForPermissions(permissions)
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("opencode: encode ACP agent config: %w", err)
	}
	return string(data), nil
}

func opencodeAOAgentName(sessionID string) string {
	const fallback = "ao-system-prompt"
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return fallback
	}
	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-',
			r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-_")
	if name == "" {
		return fallback
	}
	return "ao-" + name
}

func ResolveOpenCodeBinary(ctx context.Context) (string, error) {
	candidates, err := BinaryCandidates(ctx)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("opencode: %w", ports.ErrAgentBinaryNotFound)
	}
	return candidates[0], nil
}

func BinaryCandidates(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var found []string
	seen := map[string]bool{}
	add := func(path string) {
		if path == "" {
			return
		}
		key := filepath.Clean(path)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		found = append(found, path)
	}

	for _, name := range []string{"opencode", "opencode-v2"} {
		if managed, ok := binaryutil.ManagedBinary(name); ok {
			add(managed)
		}
	}
	names := []string{"opencode"}
	if runtime.GOOS == "windows" {
		names = []string{"opencode.cmd", "opencode.exe", "opencode"}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			add(path)
			break
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if hookutil.IsExecutableFile(candidate) {
				add(candidate)
				break
			}
		}
	}

	var candidates []string
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates,
				filepath.Join(appData, "npm", "opencode.cmd"),
				filepath.Join(appData, "npm", "opencode.exe"),
			)
		}
		candidates = append(candidates, binaryutil.WindowsPackageManagerBinCandidates("opencode")...)
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".opencode", "bin", "opencode.exe"))
		}
	} else {
		candidates = append(candidates, opencodeUnixPaths...)
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				filepath.Join(home, ".npm-global", "bin", "opencode"),
				filepath.Join(home, ".npm", "bin", "opencode"),
				filepath.Join(home, ".local", "bin", "opencode"),
				filepath.Join(home, ".opencode", "bin", "opencode"),
			)
			candidates = append(candidates, binaryutil.UnixPackageManagerBinCandidates(home, "opencode")...)
			nodeManagerCandidates, err := binaryutil.UnixNodeManagerBinCandidates(ctx, home, "opencode")
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, nodeManagerCandidates...)
		}
	}
	if dir, err := V2NPMBinDir(); err == nil {
		if runtime.GOOS == "windows" {
			candidates = append(candidates, filepath.Join(dir, "opencode.cmd"), filepath.Join(dir, "opencode.exe"))
		} else {
			candidates = append(candidates, filepath.Join(dir, "opencode"))
		}
	}
	if runtime.GOOS != "windows" {
		if dir, err := V2InstallerBinDir(); err == nil {
			candidates = append(candidates, filepath.Join(dir, "opencode"))
		}
		candidates = append(candidates, v2HomebrewCandidates()...)
		candidates = append(candidates, v1HomebrewCandidates()...)
	}
	for _, candidate := range candidates {
		if hookutil.IsExecutableFile(candidate) {
			add(candidate)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return found, nil
}

func (p *Plugin) opencodeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveOpenCodeBinary(ctx)
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

func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

var _ ports.AgentTerminalVersionRefresher = (*Plugin)(nil)
