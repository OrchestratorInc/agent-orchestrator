package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ProjectAgentConfig struct {
	Model       string `json:"model,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Permissions string `json:"permissions,omitempty"`
}

type ProjectRoleConfig struct {
	Agent       string             `json:"agent"`
	AgentConfig ProjectAgentConfig `json:"agentConfig"`
}

type ProjectReviewer struct {
	Harness     string             `json:"harness"`
	AgentConfig ProjectAgentConfig `json:"agentConfig"`
}

type ProjectSettingsConfig struct {
	SessionPrefix string             `json:"sessionPrefix,omitempty"`
	Worker        *ProjectRoleConfig `json:"worker,omitempty"`
	Orchestrator  *ProjectRoleConfig `json:"orchestrator,omitempty"`
	Reviewers     []ProjectReviewer  `json:"reviewers,omitempty"`
	AutoReview    *bool              `json:"autoReview,omitempty"`
	// Coder is the project's Coder dev-kit config. Settings may patch only its
	// workspaceNamePrefix and template parameters; the rest is chosen when the
	// project is created.
	Coder *ProjectCoderConfig `json:"coder,omitempty"`
	// The project's Setup and Cleanup commands (postCreate and preRemove) are
	// read by ProjectScripts and checked on write (ValidateProjectScriptsConfig
	// and the settings patch); they are not decoded here, so a stored value of
	// an unexpected shape never blocks creating a session.
}

// Bounds for a project's Setup and Cleanup commands.
const (
	MaxProjectScriptCommands     = 20
	MaxProjectScriptCommandBytes = 4 << 10
)

// ValidateProjectScriptCommands checks one list of Setup or Cleanup commands:
// at most MaxProjectScriptCommands non-blank commands of at most
// MaxProjectScriptCommandBytes each.
func ValidateProjectScriptCommands(kind string, commands []string) error {
	if len(commands) > MaxProjectScriptCommands {
		return fmt.Errorf("%s must contain at most %d commands", kind, MaxProjectScriptCommands)
	}
	for _, command := range commands {
		if strings.TrimSpace(command) == "" {
			return fmt.Errorf("%s commands must not be empty", kind)
		}
		if len(command) > MaxProjectScriptCommandBytes {
			return fmt.Errorf("each %s command must be at most 4 KB", kind)
		}
		if strings.ContainsRune(command, 0) || !utf8.ValidString(command) {
			return fmt.Errorf("%s commands contain invalid characters", kind)
		}
	}
	return nil
}

// ValidateProjectScriptsConfig checks the postCreate and preRemove keys of a
// project config: each, when present, must be an array of commands that
// passes ValidateProjectScriptCommands.
func ValidateProjectScriptsConfig(config json.RawMessage) error {
	var values map[string]json.RawMessage
	if len(config) == 0 || json.Unmarshal(config, &values) != nil {
		return nil
	}
	for _, key := range []string{"postCreate", "preRemove"} {
		raw, ok := values[key]
		if !ok {
			continue
		}
		var commands []string
		if json.Unmarshal(raw, &commands) != nil {
			return fmt.Errorf("%s must be an array of strings", key)
		}
		if err := ValidateProjectScriptCommands(key, commands); err != nil {
			return err
		}
	}
	return nil
}

// ProjectScripts reads a project's Setup and Cleanup commands from its config
// for a worker launch. Like the role rules, a value of the wrong shape is
// ignored rather than making the session unbootable.
func ProjectScripts(config json.RawMessage) (postCreate, preRemove []string) {
	var values struct {
		PostCreate json.RawMessage `json:"postCreate"`
		PreRemove  json.RawMessage `json:"preRemove"`
	}
	if len(config) == 0 || json.Unmarshal(config, &values) != nil {
		return nil, nil
	}
	decode := func(raw json.RawMessage) []string {
		var commands []string
		if len(raw) == 0 || json.Unmarshal(raw, &commands) != nil {
			return nil
		}
		kept := make([]string, 0, len(commands))
		for _, command := range commands {
			if strings.TrimSpace(command) != "" {
				kept = append(kept, command)
			}
		}
		if len(kept) == 0 {
			return nil
		}
		return kept
	}
	return decode(values.PostCreate), decode(values.PreRemove)
}

// Config contains a partial object. Arrays replace; nested role objects merge.
type ProjectSettingsPatch struct {
	DisplayName   *string         `json:"displayName,omitempty"`
	DefaultBranch *string         `json:"defaultBranch,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
}

// NormalizeProjectConfig reads legacy flat choices, preferring a nested agent
// when present. Callers persist the normalized object on their next write.
func NormalizeProjectConfig(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil || config == nil {
		return nil, fmt.Errorf("project config must be an object")
	}
	for _, role := range []string{"worker", "orchestrator"} {
		legacy := config[role+"Agent"]
		if len(legacy) > 0 {
			var agent string
			if err := json.Unmarshal(legacy, &agent); err != nil {
				return nil, fmt.Errorf("%sAgent must be a string", role)
			}
			var nested map[string]json.RawMessage
			if existing := config[role]; len(existing) > 0 {
				if err := json.Unmarshal(existing, &nested); err != nil || nested == nil {
					return nil, fmt.Errorf("%s must be an object", role)
				}
			}
			if nested == nil {
				nested = make(map[string]json.RawMessage)
			}
			if _, exists := nested["agent"]; !exists {
				nested["agent"] = legacy
			}
			config[role], _ = json.Marshal(nested)
			delete(config, role+"Agent")
		}
	}
	return json.Marshal(config)
}

func DecodeProjectSettings(raw json.RawMessage) (ProjectSettingsConfig, error) {
	normalized, err := NormalizeProjectConfig(raw)
	if err != nil {
		return ProjectSettingsConfig{}, err
	}
	var settings ProjectSettingsConfig
	if err := json.Unmarshal(normalized, &settings); err != nil {
		return settings, fmt.Errorf("invalid project settings: %w", err)
	}
	if err := validateSessionPrefix(settings.SessionPrefix); err != nil {
		return settings, err
	}
	if settings.Coder != nil {
		if err := ValidateCoderWorkspaceNamePrefix(settings.Coder.WorkspaceNamePrefix); err != nil {
			return settings, err
		}
		if err := ValidateCoderTemplateParameterValues(settings.Coder.Parameters); err != nil {
			return settings, err
		}
	}
	for _, role := range []*ProjectRoleConfig{settings.Worker, settings.Orchestrator} {
		if role != nil {
			if err := ValidateProjectAgent(role.Agent, role.AgentConfig); err != nil {
				return settings, err
			}
		}
	}
	if len(settings.Reviewers) > 1 {
		return settings, fmt.Errorf("Cloud supports one project reviewer")
	}
	for _, reviewer := range settings.Reviewers {
		if err := ValidateProjectAgent(reviewer.Harness, reviewer.AgentConfig); err != nil {
			return settings, err
		}
	}
	return settings, nil
}

// SessionAgentConfig applies role defaults only to that role's chosen harness.
// An explicit session model wins over the project's default.
func SessionAgentConfig(raw json.RawMessage, kind, harness, model string) (string, ProjectAgentConfig, error) {
	settings, err := DecodeProjectSettings(raw)
	if err != nil {
		return "", ProjectAgentConfig{}, err
	}
	role := settings.Worker
	if kind == "orchestrator" {
		role = settings.Orchestrator
	}
	config := ProjectAgentConfig{}
	if role != nil {
		if harness == "" {
			harness = role.Agent
		}
		if harness == role.Agent {
			config = role.AgentConfig
		}
	}
	if model != "" {
		config.Model = model
	}
	return harness, config, nil
}

// EffectiveReviewer preserves the session's agent when no reviewer is set.
// Permission defaults follow the session policy, independently of auto-inject.
func EffectiveReviewer(settings ProjectSettingsConfig, harness, model, sessionMode string, workerConfig ProjectAgentConfig) ProjectReviewer {
	reviewer := ProjectReviewer{Harness: harness, AgentConfig: workerConfig}
	reviewer.AgentConfig.Model = model
	if len(settings.Reviewers) > 0 {
		reviewer = settings.Reviewers[0]
	}
	if reviewer.AgentConfig.Permissions == "" {
		reviewer.AgentConfig.Permissions = "auto"
		if sessionMode == "trusted" {
			reviewer.AgentConfig.Permissions = "bypass-permissions"
		}
	}
	return reviewer
}

func ValidateProjectAgent(harness string, config ProjectAgentConfig) error {
	if !slices.Contains([]string{"claude-code", "codex", "cursor", "opencode"}, harness) {
		return fmt.Errorf("unsupported Cloud agent %q", harness)
	}
	if utf8.RuneCountInString(config.Model) > 500 || strings.ContainsFunc(config.Model, unicode.IsControl) {
		return fmt.Errorf("model must be at most 500 characters without control characters")
	}
	if !slices.Contains([]string{"", "default", "auto", "accept-edits", "bypass-permissions"}, config.Permissions) {
		return fmt.Errorf("invalid agent permissions")
	}
	if harness == "opencode" && config.Permissions == "accept-edits" {
		return fmt.Errorf("OpenCode does not support accept-edits permissions")
	}
	if config.Effort != "" {
		levels := []string{"low", "medium", "high", "xhigh", "max"}
		if harness == "claude-code" {
			levels = []string{"low", "medium", "high", "max"}
		}
		if (harness != "codex" && harness != "claude-code") || !slices.Contains(levels, config.Effort) {
			return fmt.Errorf("unsupported effort for %s", harness)
		}
	}
	if config.Mode != "" && !(harness == "cursor" && slices.Contains([]string{"plan", "ask"}, config.Mode)) {
		return fmt.Errorf("unsupported mode for %s", harness)
	}
	return nil
}

func ParseProjectSettingsPatch(raw json.RawMessage) (ProjectSettingsPatch, error) {
	var patch ProjectSettingsPatch
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		return patch, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return patch, fmt.Errorf("settings must be an object")
	}
	for key, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return patch, fmt.Errorf("%s cannot be null", key)
		}
	}
	if patch.DisplayName != nil {
		*patch.DisplayName = strings.TrimSpace(*patch.DisplayName)
		if n := utf8.RuneCountInString(*patch.DisplayName); n < 1 || n > 120 {
			return patch, fmt.Errorf("project name must contain 1-120 characters")
		}
	}
	if patch.DefaultBranch != nil {
		*patch.DefaultBranch = strings.TrimSpace(*patch.DefaultBranch)
		if n := utf8.RuneCountInString(*patch.DefaultBranch); n < 1 || n > 255 || strings.ContainsFunc(*patch.DefaultBranch, unicode.IsControl) {
			return patch, fmt.Errorf("default branch must contain 1-255 characters without control characters")
		}
	}
	if len(patch.Config) > 0 {
		if err := validateSettingsPatchObject(patch.Config, "config"); err != nil {
			return patch, err
		}
	}
	return patch, nil
}

func validateSettingsPatchObject(raw json.RawMessage, kind string) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return fmt.Errorf("%s must be an object", kind)
	}
	allowed := map[string][]string{
		"config":      {"worker", "orchestrator", "reviewers", "autoReview", "sessionPrefix", "coder", "postCreate", "preRemove"},
		"coder":       {"workspaceNamePrefix", "parameters"},
		"role":        {"agent", "agentConfig"},
		"reviewer":    {"harness", "agentConfig"},
		"agentConfig": {"model", "mode", "effort", "permissions"},
	}
	for key, value := range values {
		if !slices.Contains(allowed[kind], key) {
			return fmt.Errorf("unsupported or null %s.%s", kind, key)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			if kind == "config" && (key == "worker" || key == "orchestrator") {
				continue // Remove the role override and use the session selection.
			}
			return fmt.Errorf("unsupported or null %s.%s", kind, key)
		}
		switch key {
		case "worker", "orchestrator":
			if err := validateSettingsPatchObject(value, "role"); err != nil {
				return err
			}
		case "agentConfig":
			if err := validateSettingsPatchObject(value, "agentConfig"); err != nil {
				return err
			}
		case "coder":
			if err := validateSettingsPatchObject(value, "coder"); err != nil {
				return err
			}
		case "parameters":
			var parameters map[string]string
			if json.Unmarshal(value, &parameters) != nil || parameters == nil {
				return fmt.Errorf("coder.parameters must be an object of string values")
			}
			if err := ValidateCoderTemplateParameterValues(parameters); err != nil {
				return err
			}
		case "workspaceNamePrefix":
			var prefix string
			if json.Unmarshal(value, &prefix) != nil {
				return fmt.Errorf("coder.workspaceNamePrefix must be a string")
			}
			if err := ValidateCoderWorkspaceNamePrefix(prefix); err != nil {
				return err
			}
		case "reviewers":
			var reviewers []json.RawMessage
			if json.Unmarshal(value, &reviewers) != nil || len(reviewers) > 1 {
				return fmt.Errorf("reviewers must be an array containing at most one reviewer")
			}
			for _, reviewer := range reviewers {
				if err := validateSettingsPatchObject(reviewer, "reviewer"); err != nil {
					return err
				}
			}
		case "sessionPrefix":
			var prefix string
			if json.Unmarshal(value, &prefix) != nil {
				return fmt.Errorf("sessionPrefix must be a string")
			}
			if err := validateSessionPrefix(prefix); err != nil {
				return err
			}
		case "autoReview":
			var enabled bool
			if json.Unmarshal(value, &enabled) != nil {
				return fmt.Errorf("autoReview must be a boolean")
			}
		case "postCreate", "preRemove":
			var commands []string
			if json.Unmarshal(value, &commands) != nil {
				return fmt.Errorf("%s must be an array of strings", key)
			}
			if err := ValidateProjectScriptCommands(key, commands); err != nil {
				return err
			}
		default:
			var setting string
			if json.Unmarshal(value, &setting) != nil {
				return fmt.Errorf("%s must be a string", key)
			}
		}
	}
	return nil
}

func MergeProjectSettingsConfig(existing, patch json.RawMessage) (json.RawMessage, error) {
	normalized, err := NormalizeProjectConfig(existing)
	if err != nil {
		return nil, err
	}
	if len(patch) > 0 {
		if err := validateSettingsPatchObject(patch, "config"); err != nil {
			return nil, err
		}
		normalized = mergeSettingsObjects(normalized, patch)
	}
	if _, err := DecodeProjectSettings(normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func mergeSettingsObjects(existing, patch json.RawMessage) json.RawMessage {
	var result, updates map[string]json.RawMessage
	_ = json.Unmarshal(existing, &result)
	_ = json.Unmarshal(patch, &updates)
	if result == nil {
		result = make(map[string]json.RawMessage)
	}
	for key, value := range updates {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(result, key)
		} else if key == "parameters" {
			// Template parameters replace as a whole, so a value reset to the
			// template default (and therefore omitted) is removed.
			result[key] = value
		} else if bytes.HasPrefix(bytes.TrimSpace(value), []byte("{")) {
			result[key] = mergeSettingsObjects(result[key], value)
		} else {
			result[key] = value
		}
	}
	raw, _ := json.Marshal(result)
	return raw
}

// Cloud branches use <prefix>/<session-id>. Empty keeps the existing ao prefix.
func validateSessionPrefix(prefix string) error {
	if strings.HasPrefix(prefix, "-") {
		return fmt.Errorf("sessionPrefix cannot start with a hyphen")
	}
	if len(prefix) > 40 {
		return fmt.Errorf("sessionPrefix must contain at most 40 ASCII letters, digits, hyphens or underscores")
	}
	for _, ch := range prefix {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return fmt.Errorf("sessionPrefix must contain only ASCII letters, digits, hyphens or underscores")
		}
	}
	return nil
}

func ProjectSessionPrefix(raw json.RawMessage) (string, error) {
	settings, err := DecodeProjectSettings(raw)
	if err != nil {
		return "", err
	}
	if settings.SessionPrefix == "" {
		return "ao", nil
	}
	return settings.SessionPrefix, nil
}
