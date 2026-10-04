package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

const nativeCatalogTimeout = 15 * time.Second
const nativeCatalogOutputLimit = 4 << 20

type nativeModel struct {
	Value          string   `json:"value"`
	ResolvedModel  string   `json:"resolvedModel"`
	DisplayName    string   `json:"displayName"`
	Description    string   `json:"description"`
	SupportsEffort *bool    `json:"supportsEffort"`
	Efforts        []string `json:"supportedEffortLevels"`
	DefaultEffort  string   `json:"defaultEffort"`
}

// NativeCatalog reads the installed Claude Code picker through its initialize
// response without submitting a prompt or running an inference turn.
func NativeCatalog(ctx context.Context, binary, workingDir string, env map[string]string) (ports.AgentModelCatalog, error) {
	probeCtx, cancel := context.WithTimeout(ctx, nativeCatalogTimeout)
	defer cancel()
	catalog := ports.AgentModelCatalog{AgentID: "claude-code", Source: "native", FetchedAt: time.Now().UTC()}
	resolved := (&Plugin{}).resolveProviderContext(probeCtx, binary, workingDir, env, claudeModelAuthReport)
	catalog.InputFingerprint, _ = providerCatalogIdentity(probeCtx, ProviderCatalogFingerprint(ctx, binary, workingDir, env), resolved)
	if err := probeCtx.Err(); err != nil {
		return catalog, err
	}
	models, err := nativePickerModels(probeCtx, binary, workingDir, env)
	if err != nil {
		return catalog, err
	}
	catalog.Models = models
	return catalog, nil
}

func nativePickerModels(ctx context.Context, binary, workingDir string, env map[string]string) ([]ports.AgentModelInfo, error) {
	cmd := aoprocess.CommandContext(ctx, binary, "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--settings", `{"disableAllHooks":true}`, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`)
	cmd.Dir = workingDir
	cmd.Env = processenv.Merge(env)
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stopReading := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer stopReading()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude-code native model discovery: %w", err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	request := map[string]any{"type": "control_request", "request_id": "ao-model-catalog", "request": map[string]string{"subtype": "initialize"}}
	if err = json.NewEncoder(stdin).Encode(request); err != nil {
		return nil, fmt.Errorf("claude-code initialize: %w", err)
	}
	reader := io.LimitReader(stdout, nativeCatalogOutputLimit+1)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), nativeCatalogOutputLimit)
	for scanner.Scan() {
		var frame struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string `json:"subtype"`
				RequestID string `json:"request_id"`
				Response  struct {
					Models []nativeModel `json:"models"`
				} `json:"response"`
			} `json:"response"`
		}
		if err = json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return nil, errors.New("claude-code initialize returned malformed JSON")
		}
		if frame.Type == "control_request" {
			return nil, errors.New("claude-code initialize requires interaction")
		}
		if frame.Type != "control_response" || frame.Response.RequestID != "ao-model-catalog" {
			continue
		}
		if frame.Response.Subtype != "success" {
			return nil, errors.New("claude-code initialize failed")
		}
		models := normalizeNativeModels(frame.Response.Response.Models)
		if len(models) == 0 {
			return nil, errors.New("claude-code native picker returned no models")
		}
		settings := agentcreds.ResolveClaudeSettings(ctx, workingDir, env, agentcreds.ResolveOptions{})
		return appendNativeConfiguredModels(models, frame.Response.Response.Models, settings, env), nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err = scanner.Err(); err != nil {
		return nil, fmt.Errorf("claude-code initialize output: %w", err)
	}
	return nil, errors.New("claude-code exited without model initialization")
}

func normalizeNativeModels(models []nativeModel) []ports.AgentModelInfo {
	defaultID, defaultLabel := "", ""
	defaultMatched := false
	for _, model := range models {
		if model.Value != "default" {
			continue
		}
		defaultID = strings.TrimSpace(model.ResolvedModel)
		defaultLabel = nativeDefaultLabel(model)
		for _, candidate := range models {
			if candidate.Value != "default" && defaultID != "" && candidate.ResolvedModel == defaultID {
				defaultID = candidate.Value
				defaultMatched = true
				break
			}
		}
		break
	}
	out := make([]ports.AgentModelInfo, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		id := strings.TrimSpace(model.Value)
		label := nativeModelLabel(model)
		if id == "default" {
			if defaultMatched {
				continue
			}
			id = strings.TrimSpace(model.ResolvedModel)
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		isDefault := id == defaultID
		if isDefault && defaultLabel != "" {
			label = defaultLabel
		}
		if label == "" {
			label = id
		}
		efforts := model.Efforts
		if model.SupportsEffort != nil && !*model.SupportsEffort {
			efforts = []string{}
		}
		out = append(out, ports.AgentModelInfo{ID: id, Label: label, IsDefault: isDefault, Efforts: efforts, DefaultEffort: model.DefaultEffort})
	}
	return out
}

func nativeDefaultLabel(model nativeModel) string {
	if _, after, ok := strings.Cut(model.Description, "(currently "); ok {
		if name, _, closed := strings.Cut(after, ")"); closed {
			return strings.TrimSpace(name)
		}
	}
	return strings.TrimSpace(model.ResolvedModel)
}

func nativeModelLabel(model nativeModel) string {
	label := strings.TrimSpace(model.DisplayName)
	if label == "" {
		return ""
	}
	pattern := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(label) + `\s+([0-9]{1,2}(?:\.[0-9]{1,2})*)(?:\b|$)`)
	if match := pattern.FindStringSubmatch(strings.TrimSpace(model.Description)); match != nil {
		return label + " " + match[1]
	}
	pattern = regexp.MustCompile(`(?i)(?:^|[./])claude-` + regexp.QuoteMeta(strings.ToLower(label)) + `-([0-9]{1,2})(?:-([0-9]{1,2}))?(?:$|[-@\[])`)
	if match := pattern.FindStringSubmatch(model.ResolvedModel); match != nil {
		version := match[1]
		if match[2] != "" {
			version += "." + match[2]
		}
		return label + " " + version
	}
	return label
}

func appendNativeConfiguredModels(models []ports.AgentModelInfo, native []nativeModel, settings agentcreds.ClaudeSettings, env map[string]string) []ports.AgentModelInfo {
	known := make(map[string]int, len(models)+len(native))
	configuredDefault := strings.TrimSpace(settings.Model)
	if configuredDefault == "default" {
		configuredDefault = ""
	}
	for index, model := range models {
		known[model.ID] = index
		if configuredDefault != "" {
			models[index].IsDefault = false
		}
	}
	for _, row := range native {
		if index, ok := known[row.Value]; ok && row.ResolvedModel != "" {
			known[row.ResolvedModel] = index
		}
	}
	configured := []string{settings.Model, settings.Env["ANTHROPIC_DEFAULT_OPUS_MODEL"], settings.Env["ANTHROPIC_DEFAULT_SONNET_MODEL"], settings.Env["ANTHROPIC_DEFAULT_HAIKU_MODEL"], settings.Env["ANTHROPIC_SMALL_FAST_MODEL"], nativeLaunchEnv(env, "ANTHROPIC_CUSTOM_MODEL_OPTION")}
	raw := nativeLaunchEnv(env, "CLAUDE_MODEL_CONFIG")
	if len(raw) <= nativeCatalogOutputLimit {
		var config struct {
			AvailableModels []string `json:"availableModels"`
		}
		if json.Unmarshal([]byte(raw), &config) == nil {
			configured = append(configured, config.AvailableModels...)
		}
	}
	for _, candidate := range configured {
		id := strings.TrimSpace(candidate)
		if id == "" || id == "default" {
			continue
		}
		if index, exists := known[id]; exists {
			if id == configuredDefault {
				models[index].IsDefault = true
			}
			continue
		}
		known[id] = len(models)
		models = append(models, ports.AgentModelInfo{ID: id, Label: id, IsDefault: id == configuredDefault})
	}
	return models
}

func nativeLaunchEnv(env map[string]string, key string) string {
	if value, supplied := env[key]; supplied {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(os.Getenv(key))
}
