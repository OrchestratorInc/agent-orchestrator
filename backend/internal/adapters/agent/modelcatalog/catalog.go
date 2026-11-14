package modelcatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/dockeragent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

const (
	commandTimeout = 20 * time.Second

	commandTerminationWait = 2 * time.Second
)

var commandInterruptSupported = runtime.GOOS != "windows"

type commandSpec struct {
	args              []string
	parser            func([]byte) ([]ports.AgentModelInfo, error)
	signIn            *signInCheck
	interruptOnCancel bool
	retryEmpty        bool
}

type signInCheck struct {
	args    []string
	envKeys []string
}

var kiroSignIn = &signInCheck{args: []string{"whoami", "--format", "json"}, envKeys: []string{"KIRO_API_KEY"}}

var signInCheckTimeout = 5 * time.Second

var signInProbe = func(ctx context.Context, binary string, args []string, workingDir string, env map[string]string) ([]byte, error) {
	return modelCommand(ctx, binary, args, workingDir, env).CombinedOutput()
}

func (c *signInCheck) check(ctx context.Context, agentID, binary, workingDir string, env map[string]string) error {
	for _, key := range c.envKeys {
		if strings.TrimSpace(env[key]) != "" || strings.TrimSpace(os.Getenv(key)) != "" {
			return nil
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, signInCheckTimeout)
	defer cancel()
	output, err := signInProbe(probeCtx, binary, c.args, workingDir, env)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s sign-in check canceled: %w", agentID, ctxErr)
	}
	if probeCtx.Err() != nil {
		return fmt.Errorf("%s sign-in check timed out after %s", agentID, signInCheckTimeout)
	}
	switch authprobe.StatusFromText(string(output)) {
	case ports.AgentAuthStatusAuthorized:
		return nil
	case ports.AgentAuthStatusUnauthorized:
		return fmt.Errorf("%s model discovery: %w", agentID, ports.ErrAgentModelDiscoverySignInRequired)
	}
	if err != nil {
		return fmt.Errorf("%s sign-in check failed: %w", agentID, err)
	}
	return nil
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[[:alpha:]]`)

var commandSpecs = map[string]commandSpec{
	"aider":        {args: []string{"--no-check-update", "--no-git", "--no-gitignore", "--no-analytics", "--list-models", "."}, parser: parseAiderModels},
	"opencode":     {args: []string{"models"}, parser: parseProviderIDLines},
	"opencode-v2":  {args: []string{"models"}, parser: parseProviderIDLines, retryEmpty: true},
	"grok":         {args: []string{"models"}, parser: parseGrokModels},
	"cursor":       {args: []string{"models"}, parser: parseCursorModels},
	"agy":          {args: []string{"models"}, parser: parseAgyModels},
	"kilocode":     {args: []string{"models"}, parser: parseProviderIDLines},
	"pi":           {args: []string{"--list-models"}, parser: parsePiModels},
	"kimchi":       {args: []string{"--list-models"}, parser: parsePiModels},
	"prime-agent":  {args: []string{"model", "list"}, parser: parsePiModels},
	"kimi":         {args: []string{"provider", "list", "--json"}, parser: parseJSONModels},
	"auggie":       {args: []string{"models", "list", "--json"}, parser: parseJSONModels},
	"kiro":         {args: []string{"chat", "--list-models", "--format", "json"}, parser: parseJSONModels, signIn: kiroSignIn},
	"omp":          {args: []string{"models", "--json"}, parser: parseJSONModels},
	"codewhale":    {args: []string{"models", "--json"}, parser: parseJSONModels},
	"copilot":      {args: []string{"help", "config"}, parser: parseCopilotConfigModels},
	"droid":        {args: []string{"exec", "--help"}, parser: parseDroidHelpModels},
	"crush":        {args: []string{"models"}, parser: parseProviderIDLines},
	"fx":           {args: []string{"models", "--json"}, parser: parseFXModels},
	"mimo-code":    {args: []string{"models"}, parser: parseMiMoModels},
	"command-code": {args: []string{"--list-models"}, parser: parseCommandCodeModels},
	"minimax-code": {args: []string{"provider", "list", "--json"}, parser: parseMiniMaxModels, interruptOnCancel: true},
}

func Accepts(agentID, model string) bool {
	if strings.TrimSpace(model) == "" {
		return true
	}
	catalog := Base(agentID)
	if catalog.AllowCustom || len(catalog.Models) == 0 {
		return true
	}
	for _, item := range catalog.Models {
		if strings.EqualFold(item.ID, model) {
			return true
		}
	}
	return false
}

func Base(agentID string) ports.AgentModelCatalog {
	now := time.Now().UTC()
	entryMode := customModelEntryMode(agentID)
	switch agentID {
	case "docker-agent":
		return catalog(agentID, "native-default", entryMode, now, model("default", "Agent default", true))
	case "muse":
		return catalog(agentID, "official-catalog", entryMode, now,
			model("muse-spark", "Muse Spark", true),
			model("muse-spark-1.1", "Muse Spark 1.1", false),
			model("muse-spark-1.2", "Muse Spark 1.2", false),
		)
	case "memcode":
		return catalog(agentID, "official-catalog", entryMode, now,
			model("grok-4.7", "Grok 4.7", false),
			model("gemini-pro", "Gemini 3.1 Pro", false),
			model("gemini-flash", "Gemini 3.8 Flash", false),
			model("opus", "Opus 5.5", false),
			model("fable", "Fable 5.1", false),
			model("sonnet", "Sonnet 5", false),
			model("haiku", "Haiku 4.5", false),
			model("astra", "GPT-6 Astra", false),
			model("sol", "GPT-5.6 Sol", false),
			model("terra", "GPT-5.6 Terra", false),
			model("luna", "GPT-5.6 Luna", false),
			model("glm-5p3", "GLM-5.3", false),
			model("glm-5p3-flash", "GLM-5.3 Flash", false),
			model("qwen3p8-max", "Qwen3.8 Max", false),
			model("kimi-k3", "Kimi K3", false),
			model("deepseek-v4-flash", "DeepSeek V4.1 Flash", false),
		)
	case "amp":
		c := catalog(agentID, "official-modes", entryMode, now,
			model("low", "Low", false),
			model("medium", "Medium", true),
			model("high", "High", false),
			model("ultra", "Ultra", false),
		)
		c.SelectionMode = ports.ModelSelectionModeList
		return c
	default:
		if hasDiscoverySource(agentID) {
			return ports.AgentModelCatalog{
				AgentID:          agentID,
				SelectionMode:    ports.ModelSelectionCatalog,
				Models:           []ports.AgentModelInfo{},
				CustomModelEntry: entryMode,
				AllowCustom:      entryMode == ports.CustomModelEntryDirect,
				Source:           "cli",
				FetchedAt:        now,
			}
		}
		return Manual(agentID)
	}
}

func Manual(agentID string) ports.AgentModelCatalog {
	entryMode := customModelEntryMode(agentID)
	selectionMode := ports.ModelSelectionCatalog
	if entryMode == ports.CustomModelEntryDirect {
		selectionMode = ports.ModelSelectionText
	}
	return ports.AgentModelCatalog{
		AgentID:          agentID,
		SelectionMode:    selectionMode,
		Models:           []ports.AgentModelInfo{},
		CustomModelEntry: entryMode,
		AllowCustom:      entryMode == ports.CustomModelEntryDirect,
		Source:           "manual",
		FetchedAt:        time.Now().UTC(),
	}
}

func customModelEntryMode(agentID string) ports.CustomModelEntryMode {
	switch agentID {
	case "claude-code", "codex", "opencode", "opencode-v2", "grok", "cursor", "qwen", "gemini",
		"kimi", "muse", "aider", "goose", "autohand", "fx", "unreal-agent", "codewhale", "mimo-code", "deepseek-harness", "openhands", "devin", "hermes", "stakpak":
		return ports.CustomModelEntryDirect
	case "docker-agent", "minimax-code", "continue", "cline", "kilocode", "vibe", "pi", "kimchi", "prime-agent":
		return ports.CustomModelEntryConfigured
	default:
		return ports.CustomModelEntryNone
	}
}

type Discoverer struct {
	CodexModels       CodexModelListFunc
	ACPOptions        map[string]ACPOptionListFunc
	ClaudeModels      ClaudeModelListFunc
	ClaudeFingerprint ClaudeFingerprintFunc
}

type CodexModelListFunc func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error)

type ACPOptionListFunc func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error)

type ClaudeModelListFunc func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error)

type ClaudeFingerprintFunc func(context.Context, ports.AgentModelDiscoveryRequest) string

func (d Discoverer) Discover(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	catalog, err := d.discover(ctx, request)
	if err == nil {
		markConfiguredProviders(ctx, &catalog, request)
	}
	return catalog, err
}

func (d Discoverer) discover(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	if request.AgentID == "fx" && request.CredentialType == "" {
		return discoverFXCatalog(ctx, request, func(ctx context.Context, binary string, args []string, dir string, env map[string]string) ([]byte, error) {
			return modelCommand(ctx, binary, args, dir, env).CombinedOutput()
		})
	}
	if request.AgentID == "claude-code" {
		return discoverClaudeCatalog(ctx, request, d.ClaudeModels)
	}
	if request.AgentID == "muse" || request.AgentID == "memcode" {
		return Base(request.AgentID), nil
	}
	if request.AgentID == "codex" {
		return discoverCodexCatalog(ctx, request, d.CodexModels)
	}
	if list := d.ACPOptions[request.AgentID]; list != nil {
		catalog, err := discoverACPOptionCatalog(ctx, request, list)
		if err == nil {
			return catalog, nil
		}
		if !hasConfigDiscoverySource(request.AgentID) {
			return catalog, err
		}
	}
	if request.AgentID == "opencode" && request.CredentialType != "" {
		return discoverCommandCatalog(ctx, request.AgentID, request.Binary, request.WorkingDir,
			withOpenCodeCredentialPresence(request.Env, request.CredentialType), false)
	}
	env := request.Env
	if request.AgentID == "opencode-v2" {
		dataHome, err := opencodev2.DataHome(ctx)
		if err != nil {
			return ports.AgentModelCatalog{}, fmt.Errorf("opencode-v2 model catalog: prepare data home: %w", err)
		}
		env = make(map[string]string, len(request.Env)+1)
		for key, value := range request.Env {
			env[key] = value
		}
		env["XDG_DATA_HOME"] = dataHome
	}
	return discoverCommandCatalog(ctx, request.AgentID, request.Binary, request.WorkingDir, env, request.CredentialType == "")
}

var opencodeCredentialEnv = map[string]string{
	"opencode_api_key":   "OPENCODE_API_KEY",
	"anthropic_api_key":  "ANTHROPIC_API_KEY",
	"openai_api_key":     "OPENAI_API_KEY",
	"openrouter_api_key": "OPENROUTER_API_KEY",
}

const modelDiscoveryPresenceValue = "ao-model-discovery-presence"

func withOpenCodeCredentialPresence(env map[string]string, credentialType string) map[string]string {
	envVar, ok := opencodeCredentialEnv[credentialType]
	if !ok {
		return env
	}
	next := make(map[string]string, len(env)+1)
	for key, value := range env {
		next[key] = value
	}
	next[envVar] = modelDiscoveryPresenceValue
	return next
}

func discoverClaudeCatalog(
	ctx context.Context,
	request ports.AgentModelDiscoveryRequest,
	list ClaudeModelListFunc,
) (ports.AgentModelCatalog, error) {
	base := Manual(request.AgentID)
	if list == nil {
		return base, errors.New("claude-code model discovery is unavailable")
	}
	models, err := list(ctx, request)
	if err != nil {
		return base, fmt.Errorf("claude-code model discovery: %w", err)
	}
	normalized := normalize(models)
	if len(normalized) == 0 {
		return base, errors.New("claude-code model discovery returned no models")
	}
	settings := agentcreds.ResolveClaudeSettings(ctx, request.WorkingDir, request.Env, agentcreds.ResolveOptions{})
	base.Models = LabelClaudeAliasVersions(applyClaudeConfiguredDefault(normalized, settings.Model), normalized)
	base.SelectionMode = ports.ModelSelectionCatalog
	base.Source = "provider"
	return base, nil
}

func applyClaudeConfiguredDefault(models []ports.AgentModelInfo, configured string) []ports.AgentModelInfo {
	if configured == "" {
		return models
	}
	matched := false
	for i := range models {
		models[i].IsDefault = strings.EqualFold(models[i].ID, configured)
		matched = matched || models[i].IsDefault
	}
	if !matched {
		models = append(models, ports.AgentModelInfo{ID: configured, Label: configured, IsDefault: true})
	}
	return models
}

func (d Discoverer) CatalogFingerprint(ctx context.Context, request ports.AgentModelDiscoveryRequest) string {
	fingerprint := CatalogFingerprint(ctx, request.AgentID, request.Binary, request.WorkingDir, request.Env)
	if request.AgentID == "fx" && request.CredentialType == "" {
		return fingerprint + ":shared-providers-v1:" + fingerprintConfigPaths([]string{fxSettingsPath(request.Env)})
	}
	if request.CredentialType == "" && (hasLocalProviderCatalog(request.AgentID) || browseProviderCredentials[request.AgentID] != nil) {
		if listsRunnableModels(request.AgentID) {
			return fingerprint + ":shared-providers-v1:runnable-v1"
		}
		if request.AgentID == "pi" || request.AgentID == "kimchi" {
			return fingerprint + ":shared-providers-v1:thinking-v2"
		}
		return fingerprint + ":shared-providers-v1"
	}
	if request.AgentID != "claude-code" || d.ClaudeFingerprint == nil {
		return fingerprint
	}
	extra := d.ClaudeFingerprint(ctx, request)
	hash := sha256.Sum256([]byte(fingerprint + "\x00" + extra + "\x00alias-context-v1"))
	return fmt.Sprintf("%x", hash[:8])
}

func (Discoverer) Manual(agentID string) ports.AgentModelCatalog { return Manual(agentID) }

func discoverCommandCatalog(ctx context.Context, agentID, binary, workingDir string, env map[string]string, includeLocal bool) (ports.AgentModelCatalog, error) {
	base := Base(agentID)
	if agentID == "unreal-agent" {
		return discoverUnrealCatalog(env), nil
	}
	if hasConfigDiscoverySource(agentID) {
		return discoverConfigCatalog(agentID, workingDir, env)
	}
	spec, ok := commandSpecs[agentID]
	if !ok {
		return base, nil
	}
	if strings.TrimSpace(binary) == "" {
		return base, errors.New("agent binary is not installed")
	}
	if spec.signIn != nil {
		if err := spec.signIn.check(ctx, agentID, binary, workingDir, env); err != nil {
			return base, err
		}
	}
	if spec.interruptOnCancel && !commandInterruptSupported {
		return base, errors.New("model discovery is unavailable on this platform because AO cannot stop the agent without leaving its profile locked")
	}
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := modelCommand(runCtx, binary, spec.args, workingDir, env)
	if spec.interruptOnCancel {
		aoprocess.InterruptOnCancel(cmd, commandTerminationWait)
	}
	output, err := cmd.CombinedOutput()
	if err == nil && spec.retryEmpty && len(bytes.TrimSpace(output)) == 0 {
		cancel()
		runCtx, cancel = context.WithTimeout(ctx, commandTimeout)
		defer cancel()
		output, err = modelCommand(runCtx, binary, spec.args, workingDir, env).CombinedOutput()
	}
	if err != nil {
		return base, modelDiscoveryError(runCtx, agentID, err, output)
	}
	models, err := spec.parser(output)
	if err != nil {
		return base, fmt.Errorf("%s model discovery: %w", agentID, err)
	}
	base.Providers = reportedJSONProviders(output)
	if includeLocal {
		if listsRunnableModels(agentID) {
			for _, model := range normalize(models) {
				base.RunnableModels = append(base.RunnableModels, model.ID)
			}
		}
		reported := localProviderCatalog(runCtx, agentID, binary, workingDir, env)
		models = append(models, reported.Models...)
		base.Providers = append(base.Providers, reported.Providers...)
	}
	models = normalize(models)
	if len(models) == 0 && len(base.Providers) == 0 {
		return base, fmt.Errorf("%s model discovery returned no models", agentID)
	}
	models = applyConfiguredDefault(models, configuredDefaultModel(agentID, workingDir, env))
	if agentID == "codewhale" {
		models = markCodewhaleResolvedDefault(ctx, binary, workingDir, env, models)
	}
	base.Models = models
	base.Source = "cli"
	base.FetchedAt = time.Now().UTC()
	return base, nil
}

var codewhaleResolvedModelPattern = regexp.MustCompile(`(?mi)^resolved:\s*(\S+)`)
var codewhaleResolvedProviderPattern = regexp.MustCompile(`(?mi)^provider:\s*(\S+)`)

func markCodewhaleResolvedDefault(ctx context.Context, binary, workingDir string, env map[string]string, models []ports.AgentModelInfo) []ports.AgentModelInfo {
	if err := ctx.Err(); err != nil {
		return models
	}
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := modelCommand(runCtx, binary, []string{"model", "resolve"}, workingDir, env).CombinedOutput()
	if err != nil {
		return models
	}
	return applyCodewhaleResolvedModel(models, output)
}

func applyCodewhaleResolvedModel(models []ports.AgentModelInfo, resolveOutput []byte) []ports.AgentModelInfo {
	match := codewhaleResolvedModelPattern.FindSubmatch(resolveOutput)
	if len(match) < 2 {
		return models
	}
	resolved := strings.TrimSpace(string(match[1]))
	if resolved == "" {
		return models
	}
	found := false
	for i := range models {
		if strings.EqualFold(models[i].ID, resolved) {
			models[i].IsDefault = true
			found = true
		}
	}
	if !found {
		entry := ports.AgentModelInfo{ID: resolved, Label: resolved, IsDefault: true}
		if provider := codewhaleResolvedProviderPattern.FindSubmatch(resolveOutput); len(provider) >= 2 {
			entry.Provider = strings.TrimSpace(string(provider[1]))
		}
		models = append(models, entry)
	}
	return models
}

func discoverUnrealCatalog(env map[string]string) ports.AgentModelCatalog {
	base := Base("unreal-agent")
	base.SelectionMode = ports.ModelSelectionCatalog
	base.Source = "config"
	_, selected := unrealConfiguredModel(env)
	if selected != "" {
		base.Models = []ports.AgentModelInfo{model(selected, selected, true)}
	}
	return base
}

func unrealConfiguredModel(env map[string]string) (provider, selected string) {
	value := func(key string) string {
		if configured, ok := env[key]; ok {
			return strings.TrimSpace(configured)
		}
		return strings.TrimSpace(os.Getenv(key))
	}
	provider = value("UNREAL_HARNESS_LLM_PROVIDER")
	if provider == "" {
		provider = "openai"
	}
	selected = value("UNREAL_HARNESS_LLM_MODEL")
	if selected == "" && provider == "openai" {
		selected = "gpt-6-astra"
	}
	return provider, selected
}

func discoverCodexCatalog(ctx context.Context, request ports.AgentModelDiscoveryRequest, list CodexModelListFunc) (ports.AgentModelCatalog, error) {
	base := Base(request.AgentID)
	if list == nil {
		return base, errors.New("codex app-server model discovery is unavailable")
	}
	models, err := list(ctx, request)
	if err != nil {
		return base, fmt.Errorf("codex model discovery: %w", err)
	}
	normalized := make([]ports.AgentModelInfo, 0, len(models))
	for _, item := range models {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		label := strings.TrimSpace(item.DisplayName)
		if label == "" {
			label = id
		}
		provider := ""
		if prefix, _, ok := strings.Cut(id, "/"); ok {
			provider = prefix
		}
		normalized = append(normalized, ports.AgentModelInfo{
			ID: id, Label: label, Provider: provider, IsDefault: item.Default,
			Efforts: append([]string(nil), item.Efforts...), DefaultEffort: item.DefaultEffort,
			SupportsFast: item.FastTier != "",
			Inputs:       modelInputs(item.Inputs),
		})
	}
	if len(normalized) == 0 {
		return base, errors.New("codex model discovery returned no models")
	}
	base.Models = sortNewestFirst(normalize(normalized))
	base.Source = "cli"
	base.FetchedAt = time.Now().UTC()
	return base, nil
}

var modelVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)*`)

func sortNewestFirst(models []ports.AgentModelInfo) []ports.AgentModelInfo {
	versions := make(map[string][]int, len(models))
	for _, item := range models {
		versions[item.ID] = modelVersion(item)
	}
	sort.SliceStable(models, func(i, j int) bool {
		if cmp := compareVersions(versions[models[i].ID], versions[models[j].ID]); cmp != 0 {
			return cmp > 0
		}
		return strings.ToLower(models[i].Label) < strings.ToLower(models[j].Label)
	})
	return models
}

func modelVersion(item ports.AgentModelInfo) []int {
	for _, source := range []string{item.Label, item.ID} {
		match := modelVersionPattern.FindString(source)
		if match == "" {
			continue
		}
		parts := strings.Split(match, ".")
		version := make([]int, 0, len(parts))
		for _, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil {
				break
			}
			version = append(version, n)
		}
		return version
	}
	return nil
}

func compareVersions(a, b []int) int {
	if len(a) == 0 || len(b) == 0 {
		return len(a) - len(b)
	}
	for index := 0; index < max(len(a), len(b)); index++ {
		var left, right int
		if index < len(a) {
			left = a[index]
		}
		if index < len(b) {
			right = b[index]
		}
		if left != right {
			return left - right
		}
	}
	return 0
}

func discoverACPOptionCatalog(
	ctx context.Context,
	request ports.AgentModelDiscoveryRequest,
	list ACPOptionListFunc,
) (ports.AgentModelCatalog, error) {
	base := Base(request.AgentID)
	options, err := list(ctx, request)
	if err != nil {
		return base, fmt.Errorf("%s ACP model discovery: %w", request.AgentID, err)
	}
	var models []ports.AgentModelInfo
	for _, option := range options {
		if option.Type != ports.ChatConfigOptionSelect ||
			(option.Category != "model" && option.ID != "model") {
			continue
		}
		for _, choice := range option.Choices {
			id := strings.TrimSpace(choice.Value)
			if id == "" {
				continue
			}
			label := strings.TrimSpace(choice.Name)
			if label == "" {
				label = id
			}
			provider := strings.TrimSpace(choice.Group)
			if provider == "" && request.AgentID != "docker-agent" {
				if prefix, _, ok := strings.Cut(id, "/"); ok {
					provider = prefix
				}
			}
			models = append(models, ports.AgentModelInfo{
				ID: id, Label: label, Provider: provider,
				IsDefault: id == strings.TrimSpace(option.Current.Select),
			})
		}
	}
	models = normalize(models)
	if len(models) == 0 {
		return base, fmt.Errorf("%s ACP model discovery returned no models", request.AgentID)
	}
	base.Models = models
	base.Source = "acp"
	base.FetchedAt = time.Now().UTC()
	return base, nil
}

func hasDiscoverySource(agentID string) bool {
	switch agentID {
	case "claude-code", "codex", "deepseek-harness", "devin", "docker-agent":
		return true
	}
	if hasConfigDiscoverySource(agentID) {
		return true
	}
	_, hasCommand := commandSpecs[agentID]
	return hasCommand
}

func modelCommand(ctx context.Context, binary string, args []string, workingDir string, env map[string]string) *exec.Cmd {
	cmd := aoprocess.CommandContext(ctx, binary, args...) //nolint:gosec // binary is adapter-resolved, args are static
	cmd.WaitDelay = commandTerminationWait
	if strings.TrimSpace(workingDir) != "" {
		cmd.Dir = workingDir
	}
	cmd.Env = mergedEnvironment(os.Environ(), env)
	return cmd
}

func mergedEnvironment(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(base)+len(keys))
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := overrides[key]; !replaced {
			out = append(out, item)
		}
	}
	for _, key := range keys {
		out = append(out, key+"="+overrides[key])
	}
	return out
}

func modelDiscoveryError(runCtx context.Context, agentID string, commandErr error, output []byte) error {
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s model discovery timed out after %s", agentID, commandTimeout)
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		return fmt.Errorf("%s model discovery canceled: %w", agentID, context.Canceled)
	}
	if detail := discoveryErrorDetail(output); detail != "" {
		return fmt.Errorf("%s model discovery: %w: %s", agentID, commandErr, detail)
	}
	return fmt.Errorf("%s model discovery: %w", agentID, commandErr)
}

const discoveryErrorDetailMax = 500

func discoveryErrorDetail(output []byte) string {
	plain := ansiPattern.ReplaceAllString(string(output), "")
	if line := thrownErrorLine(plain); line != "" {
		plain = line
	}
	cleaned := strings.Join(strings.Fields(plain), " ")
	if cleaned == "" {
		return ""
	}
	if runes := []rune(cleaned); len(runes) > discoveryErrorDetailMax {
		cleaned = "…" + string(runes[len(runes)-discoveryErrorDetailMax:])
	}
	return cleaned
}

var thrownErrorPattern = regexp.MustCompile(`(?m)^\s*(?:Uncaught )?(?:[A-Z][A-Za-z]*)?(?:Error|Exception): .+$`)

var stackFramePattern = regexp.MustCompile(`(?m)^\s+at \S`)

func thrownErrorLine(output string) string {
	if !stackFramePattern.MatchString(output) {
		return ""
	}
	matches := thrownErrorPattern.FindAllString(output, -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.TrimSpace(matches[len(matches)-1])
}

func BinaryVersion(ctx context.Context, binary string) string {
	if ctx.Err() != nil || strings.TrimSpace(binary) == "" {
		return ""
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return ""
	}
	if evaluated, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = evaluated
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(resolved))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatInt(info.Size(), 10)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatInt(info.ModTime().UnixNano(), 10)))
	return fmt.Sprintf("%x", hash.Sum(nil)[:8])
}

var catalogSchemaVersions = map[string]string{"memcode": "native-picker-v1"}

func CatalogFingerprint(ctx context.Context, agentID, binary, workingDir string, env map[string]string) string {
	binaryVersion := BinaryVersion(ctx, binary)
	if version := catalogSchemaVersions[agentID]; version != "" {
		binaryVersion += ";" + version
	}
	config := discoveryConfigInputs(ctx, agentID, workingDir, env)
	switch agentID {
	case "opencode", "opencode-v2", "pi", "omp", "kimchi", "prime-agent", "kimi", "auggie", "kiro", "codewhale", "kilocode", "crush", "mimo-code", "command-code":
		config += ";providers-v2=" + strings.Join(configuredProviders(ctx, agentID, env), ",") + providerCatalogFingerprint(agentID, env)
	case "aider":
		config += ";providers-v1"
	}
	if config == "" {
		return binaryVersion
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(binaryVersion))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(config))
	return fmt.Sprintf("%x", hash.Sum(nil)[:8])
}

func discoveryConfigInputs(ctx context.Context, agentID, workingDir string, env map[string]string) string {
	if agentID == "unreal-agent" {
		provider, selected := unrealConfiguredModel(env)
		return "provider=" + provider + ";model=" + selected
	}
	if agentID == "claude-code" {
		return "config=" + claudeCodeDiscoveryFingerprint(ctx, workingDir, env)
	}
	if agentID == "docker-agent" {
		return "config=" + dockerAgentDiscoveryFingerprint(env)
	}
	if agentID == "deepseek-harness" {
		return "config=" + fingerprintConfigPaths(modelConfigPaths(agentID, workingDir, env))
	}
	if agentID == "minimax-code" {
		return "config=" + fingerprintConfigPaths(modelConfigPaths(agentID, workingDir, env))
	}
	if config := configDiscoveryFingerprint(agentID, workingDir, env); config != "" {
		if agentID == "stakpak" {
			config += ";profile=" + stakpakProfile(env)
		}
		return "config=" + config
	}
	if configured := configuredDefaultModel(agentID, workingDir, env); configured != "" {
		return "default=" + configured
	}
	return ""
}

func dockerAgentDiscoveryFingerprint(env map[string]string) string {
	config, ok := env[dockeragent.ConfigEnv]
	if !ok {
		config = os.Getenv(dockeragent.ConfigEnv)
	}
	if config = strings.TrimSpace(config); config != "" {
		return "file:" + config + ":" + fingerprintConfigPaths([]string{config})
	}
	names := make([]string, 0, 4)
	for _, m := range dockeragent.DefaultModels(env) {
		names = append(names, m.Provider+"/"+m.Name)
	}
	return "default:" + strings.Join(names, ",")
}

func claudeCodeDiscoveryFingerprint(ctx context.Context, workingDir string, env map[string]string) string {
	settings := agentcreds.ResolveClaudeSettings(ctx, workingDir, env, agentcreds.ResolveOptions{})
	hash := sha256.New()
	_, _ = hash.Write([]byte("model\x00" + settings.Model + "\x00"))
	keys := make([]string, 0, len(settings.Env))
	for key := range settings.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = hash.Write([]byte(key + "\x00" + settings.Env[key] + "\x00"))
	}
	if raw, err := readModelConfig(settings.Env["GOOGLE_APPLICATION_CREDENTIALS"]); err == nil {
		_, _ = hash.Write(raw)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)[:8])
}

func catalog(agentID, source string, entryMode ports.CustomModelEntryMode, at time.Time, models ...ports.AgentModelInfo) ports.AgentModelCatalog {
	return ports.AgentModelCatalog{
		AgentID:          agentID,
		SelectionMode:    ports.ModelSelectionCatalog,
		Models:           models,
		CustomModelEntry: entryMode,
		AllowCustom:      entryMode == ports.CustomModelEntryDirect,
		Source:           source,
		FetchedAt:        at,
	}
}

func model(id, label string, isDefault bool) ports.AgentModelInfo {
	return ports.AgentModelInfo{ID: id, Label: label, IsDefault: isDefault}
}

func parseProviderIDLines(output []byte) ([]ports.AgentModelInfo, error) {
	models := parseIDLines(output)
	for i := range models {
		splitProviderID(&models[i])
	}
	return models, nil
}

func splitProviderID(item *ports.AgentModelInfo) bool {
	provider, name, ok := strings.Cut(item.ID, "/")
	if !ok || provider == "" || name == "" {
		return false
	}
	item.Provider, item.Label = provider, name
	return true
}

var bedrockModelIDPattern = regexp.MustCompile(`^(?:(?:us|eu|apac|au|jp|ca|global|us-gov)\.)?(?:ai21|amazon|anthropic|cohere|deepseek|meta|minimax|mistral|moonshot|nvidia|openai|qwen|twelvelabs|writer)\.[a-z0-9]`)

func parseAiderModels(output []byte) ([]ports.AgentModelInfo, error) {
	models, _ := parseProviderIDLines(output)
	for i := range models {
		if models[i].Provider == "" && bedrockModelIDPattern.MatchString(models[i].ID) {
			models[i].Provider = "bedrock"
		}
	}
	return models, nil
}

func parseIDLines(output []byte) []ports.AgentModelInfo {
	text := ansiPattern.ReplaceAllString(string(output), "")
	var models []ports.AgentModelInfo
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "•*-✓>│├└ "))
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 1 || !looksLikeModelID(fields[0]) {
			continue
		}
		id := strings.Trim(fields[0], "`\"'[](),:")
		models = append(models, ports.AgentModelInfo{ID: id, Label: id})
	}
	return normalize(models)
}

func parseMiMoModels(output []byte) ([]ports.AgentModelInfo, error) {
	text := ansiPattern.ReplaceAllString(string(output), "")
	var models []ports.AgentModelInfo
	for _, line := range strings.Split(text, "\n") {
		id, _, found := strings.Cut(strings.TrimSpace(line), " — ")
		item := ports.AgentModelInfo{ID: id}
		if !found || !looksLikeModelID(id) || !splitProviderID(&item) {
			continue
		}
		models = append(models, item)
	}
	return normalize(models), nil
}

func parseAgyModels(output []byte) ([]ports.AgentModelInfo, error) {
	text := ansiPattern.ReplaceAllString(string(output), "")
	var models []ports.AgentModelInfo
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		id := strings.Trim(fields[0], "`\"'[](),:")
		if !looksLikeModelID(id) || !strings.ContainsAny(id, "-./:_") {
			continue
		}
		label := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if label == "" {
			label = id
		}
		models = append(models, ports.AgentModelInfo{ID: id, Label: label})
	}
	return normalize(models), nil
}

func parseCommandCodeModels(output []byte) ([]ports.AgentModelInfo, error) {
	text := ansiPattern.ReplaceAllString(string(output), "")
	var models []ports.AgentModelInfo
	var sectionProvider string
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		switch line {
		case "Anthropic":
			sectionProvider = "anthropic"
			continue
		case "OpenAI":
			sectionProvider = "openai"
			continue
		case "Open Source":
			sectionProvider = ""
			continue
		}
		if strings.HasPrefix(line, "Pass the full id") || strings.HasPrefix(line, "Docs:") {
			break
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := strings.Trim(fields[0], "`\"'[](),:")
		if !looksLikeModelID(id) || !strings.ContainsAny(id, "-./:_") {
			if len(fields) == 1 {
				sectionProvider = ""
			}
			continue
		}
		provider := sectionProvider
		if prefix, name, ok := strings.Cut(id, "/"); ok && prefix != "" && name != "" {
			provider = prefix
		}
		models = append(models, ports.AgentModelInfo{
			ID:        id,
			Label:     id,
			Provider:  provider,
			IsDefault: strings.Contains(strings.ToLower(line), "(default)"),
		})
	}
	return normalize(models), nil
}

func parseGrokModels(output []byte) ([]ports.AgentModelInfo, error) {
	return parseSectionModels(string(output), "Available models:", "")
}

func parseCursorModels(output []byte) ([]ports.AgentModelInfo, error) {
	return parseSectionModels(string(output), "Available models", "Tip:")
}

func parseCopilotConfigModels(output []byte) ([]ports.AgentModelInfo, error) {
	text := ansiPattern.ReplaceAllString(string(output), "")
	inModels := false
	var models []ports.AgentModelInfo
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if !inModels {
			if strings.HasPrefix(line, "`model`:") {
				inModels = true
			}
			continue
		}
		if strings.HasPrefix(line, "`contextTier`:") {
			break
		}
		if !strings.HasPrefix(line, "-") {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		id = strings.Trim(id, "`\"'")
		if looksLikeModelID(id) {
			models = append(models, ports.AgentModelInfo{ID: id, Label: id})
		}
	}
	return normalize(models), nil
}

func parseDroidHelpModels(output []byte) ([]ports.AgentModelInfo, error) {
	text := ansiPattern.ReplaceAllString(string(output), "")
	inModels := false
	var models []ports.AgentModelInfo
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if !inModels {
			if line == "Available Models:" {
				inModels = true
			}
			continue
		}
		if line == "" {
			if len(models) > 0 {
				break
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !looksLikeModelID(fields[0]) {
			break
		}
		id := fields[0]
		label := strings.TrimSpace(strings.TrimPrefix(line, id))
		isDefault := strings.HasSuffix(strings.ToLower(label), "(default)")
		if isDefault {
			label = strings.TrimSpace(label[:len(label)-len("(default)")])
		}
		models = append(models, ports.AgentModelInfo{ID: id, Label: label, IsDefault: isDefault})
	}
	return normalize(models), nil
}

func parseSectionModels(output, startMarker, stopMarker string) ([]ports.AgentModelInfo, error) {
	output = ansiPattern.ReplaceAllString(output, "")
	inModels := false
	var models []ports.AgentModelInfo
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if !inModels {
			if line == startMarker {
				inModels = true
			}
			continue
		}
		if stopMarker != "" && strings.HasPrefix(line, stopMarker) {
			break
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "•*-✓>│├└ "))
		fields := strings.Fields(line)
		if len(fields) == 0 || !looksLikeModelID(fields[0]) {
			continue
		}
		id := strings.Trim(fields[0], "`\"'[](),:")
		label := id
		if before, after, ok := strings.Cut(line, " - "); ok && strings.TrimSpace(before) == id {
			label = strings.TrimSpace(after)
			if suffix, _, found := strings.Cut(label, " ("); found {
				label = strings.TrimSpace(suffix)
			}
		}
		models = append(models, ports.AgentModelInfo{
			ID:        id,
			Label:     label,
			IsDefault: strings.Contains(strings.ToLower(line), "(default)"),
		})
	}
	return normalize(models), nil
}

func parsePiModels(output []byte) ([]ports.AgentModelInfo, error) {
	contextColumn := -1
	output = []byte(ansiPattern.ReplaceAllString(string(output), ""))
	var models []ports.AgentModelInfo
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(fields[0], "provider") {
			contextColumn = -1
			for i, field := range fields {
				if strings.EqualFold(field, "context") {
					contextColumn = i
				}
			}
			continue
		}
		if len(fields) < 2 {
			continue
		}
		provider := strings.TrimSpace(fields[0])
		modelID := strings.TrimSpace(fields[1])
		if strings.HasPrefix(provider, "[") || !looksLikeModelID(provider) || !looksLikeModelID(modelID) {
			continue
		}
		id := provider + "/" + modelID
		item := ports.AgentModelInfo{ID: id, Label: modelID, Provider: provider}
		if contextColumn >= 0 && contextColumn < len(fields) {
			item.ContextWindow = catalogTokenCount(fields[contextColumn])
		}
		models = append(models, item)
	}
	return normalize(models), nil
}

func parseFXModels(output []byte) ([]ports.AgentModelInfo, error) {
	var response struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, err
	}
	models := make([]ports.AgentModelInfo, 0, len(response.IDs))
	for _, id := range response.IDs {
		if id == "" {
			continue
		}
		models = append(models, ports.AgentModelInfo{ID: id, Label: id})
	}
	return models, nil
}

func looksLikeModelID(value string) bool {
	value = strings.Trim(value, "`\"'[](),:")
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("./:_-@+", r):
		default:
			return false
		}
	}
	switch strings.ToLower(value) {
	case "model", "models", "provider":
		return false
	default:
		return true
	}
}

func parseJSONModels(output []byte) ([]ports.AgentModelInfo, error) {
	var root any
	if err := json.Unmarshal(output, &root); err != nil {
		return nil, err
	}
	var models []ports.AgentModelInfo
	var walk func(any, string)
	walk = func(value any, inheritedProvider string) {
		switch node := value.(type) {
		case []any:
			for _, item := range node {
				walk(item, inheritedProvider)
			}
		case map[string]any:
			provider := firstString(node, "provider", "providerId", "provider_id")
			if provider == "" {
				provider = inheritedProvider
			}
			if _, container := node["models"]; container && provider == "" {
				provider = firstString(node, "id")
			}
			id := firstString(node, "selector", "modelId", "model_id", "model_uid", "slug", "model")
			if id == "" {
				if _, isProviderContainer := node["models"]; !isProviderContainer {
					id = firstString(node, "id")
				}
			}
			if id != "" {
				label := firstString(node, "displayName", "display_name", "model_name", "label", "name")
				if label == "" {
					label = id
				}
				models = append(models, ports.AgentModelInfo{
					ID:            id,
					Label:         label,
					Provider:      provider,
					IsDefault:     firstBool(node, "isDefault", "is_default", "default"),
					ContextWindow: catalogContextWindow(node),
					Inputs:        catalogInputs(node),
				})
			}
			for key, child := range node {
				if key == "models" {
					if modelMap, ok := child.(map[string]any); ok {
						for alias, item := range modelMap {
							if modelNode, ok := item.(map[string]any); ok && strings.TrimSpace(alias) != "" && looksLikeModelAliasRecord(modelNode) {
								modelProvider := firstString(modelNode, "provider", "providerId", "provider_id")
								if modelProvider == "" {
									modelProvider = provider
								}
								label := firstString(modelNode, "displayName", "display_name", "model_name", "label", "name")
								if label == "" {
									label = alias
								}
								models = append(models, ports.AgentModelInfo{
									ID:            strings.TrimSpace(alias),
									Label:         label,
									Provider:      modelProvider,
									IsDefault:     firstBool(modelNode, "isDefault", "is_default", "default"),
									ContextWindow: catalogContextWindow(modelNode),
									Inputs:        catalogInputs(modelNode),
								})
								continue
							}
							walk(item, provider)
						}
						continue
					}
				}
				walk(child, provider)
			}
		}
	}
	walk(root, "")
	return normalize(models), nil
}

func looksLikeModelAliasRecord(node map[string]any) bool {
	if _, isModelContainer := node["models"]; isModelContainer {
		return false
	}
	return firstString(node,
		"modelId", "model_id", "model_uid", "slug", "model",
		"provider", "providerId", "provider_id",
	) != ""
}

func firstString(node map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := node[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstBool(node map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := node[key].(bool); ok {
			return value
		}
	}
	return false
}

func normalize(models []ports.AgentModelInfo) []ports.AgentModelInfo {
	byID := make(map[string]ports.AgentModelInfo, len(models))
	for _, item := range models {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" {
			continue
		}
		if strings.TrimSpace(item.Label) == "" {
			item.Label = item.ID
		}
		if strings.HasSuffix(strings.ToLower(item.Label), " (default)") {
			item.Label = strings.TrimSpace(item.Label[:len(item.Label)-len(" (default)")])
			item.IsDefault = true
		} else if strings.EqualFold(item.Label, "default") || strings.EqualFold(item.Label, "default (recommended)") {
			item.Label = item.ID
			item.IsDefault = true
		}
		if previous, ok := byID[item.ID]; ok {
			if previous.Label == previous.ID && item.Label != item.ID {
				previous.Label = item.Label
			}
			if previous.Provider == "" {
				previous.Provider = item.Provider
			}
			if previous.ContextWindow == 0 {
				previous.ContextWindow = item.ContextWindow
			}
			if len(previous.Inputs) == 0 {
				previous.Inputs = item.Inputs
			}
			if len(previous.Efforts) == 0 {
				previous.Efforts = item.Efforts
			}
			previous.IsDefault = previous.IsDefault || item.IsDefault
			byID[item.ID] = previous
			continue
		}
		byID[item.ID] = item
	}
	out := make([]ports.AgentModelInfo, 0, len(byID))
	for _, item := range byID {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		left, right := strings.ToLower(out[i].Label), strings.ToLower(out[j].Label)
		if left != right {
			return left < right
		}
		return out[i].ID < out[j].ID
	})
	return out
}
