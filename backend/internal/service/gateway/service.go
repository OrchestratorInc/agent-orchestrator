// Package gateway reads, writes, and probes the Anthropic-compatible gateway
// configuration. Entries persist through the same resolution chain agent
// processes already read — the user's Claude settings files — so the UI, the
// files, and the launch path can never disagree. A missing, malformed, or
// keyless file is an unconfigured scope, not an error.
package gateway

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// Scope selects which gateway entry a settings surface edits.
type Scope string

const (
	// ScopeApp is the app-wide entry every project inherits.
	ScopeApp Scope = "app"
	// ScopeProject is one project's overriding entry.
	ScopeProject Scope = "project"
)

// The three keys a gateway entry is made of, as the resolver reads them.
const (
	keyBaseURL = "ANTHROPIC_BASE_URL"
	// gosec: env key name, not a credential value.
	keyToken = "ANTHROPIC_AUTH_TOKEN" //nolint:gosec // env key name, not a credential value
	keyModel = "ANTHROPIC_MODEL"
)

// ScopeValue is one scope's stored gateway entry. The token is never
// returned, only whether one is stored.
type ScopeValue struct {
	BaseURL  string `json:"baseUrl,omitempty"`
	TokenSet bool   `json:"tokenSet"`
	Model    string `json:"model,omitempty"`
}

// Effective is what the resolution chain actually yields right now.
type Effective struct {
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
	// Source names the scope that wins under the resolution precedence:
	// "project", "app", or "" when no gateway is configured.
	Source string `json:"source,omitempty"`
}

// Config is the GET response: each scope's stored entry plus the effective
// resolution. Project is nil when no project was requested.
type Config struct {
	App       ScopeValue  `json:"app"`
	Project   *ScopeValue `json:"project,omitempty"`
	Effective Effective   `json:"effective"`
}

// SetInput is a PUT payload. Every key is tri-state: nil leaves the stored
// value untouched, a pointer to "" clears the key, a pointer to a value
// writes it. This is what makes a token-only rotation safe.
type SetInput struct {
	Scope     Scope
	ProjectID string
	BaseURL   *string
	Token     *string
	Model     *string
}

// ProbeResult mirrors the validator's verdict plus the gateway's own model
// list, so the settings screen can confirm the credential and offer models.
type ProbeResult struct {
	State  string  `json:"state"` // valid | invalid | unknown
	Detail string  `json:"detail,omitempty"`
	Models []Model `json:"models,omitempty"`
}

// Model is one model ID the gateway reported.
type Model struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}

// ProjectLookup resolves a project ID to its working directory.
type ProjectLookup func(ctx context.Context, projectID string) (string, error)

// Service implements the settings-screen surface on top of the Claude
// settings files and the shared credential validator.
type Service struct {
	// configDir reports Claude Code's config directory (the app-scope home).
	configDir func() (string, error)
	projects  ProjectLookup
}

// New builds a Service.
func New(configDir func() (string, error), projects ProjectLookup) *Service {
	return &Service{configDir: configDir, projects: projects}
}

// Get reports the stored entry per scope and the effective resolution.
func (s *Service) Get(ctx context.Context, projectID string) (Config, error) {
	cfg := Config{}
	appDir, err := s.configDir()
	if err != nil {
		return Config{}, err
	}
	cfg.App = scopeValue(readSettingsEnv(filepath.Join(appDir, "settings.json")))
	if strings.TrimSpace(projectID) == "" {
		cfg.Effective = effective(cfg.App, ScopeValue{})
		return cfg, nil
	}
	projectValue, err := s.projectValue(ctx, projectID)
	if err != nil {
		return Config{}, err
	}
	cfg.Project = &projectValue
	cfg.Effective = effective(cfg.App, projectValue)
	return cfg, nil
}

// projectValue reads one project's workspace settings: settings.json with
// settings.local.json overlaid, mirroring the resolver's precedence.
func (s *Service) projectValue(ctx context.Context, projectID string) (ScopeValue, error) {
	dir, err := s.projects(ctx, projectID)
	if err != nil {
		return ScopeValue{}, err
	}
	dir = filepath.Join(strings.TrimSpace(dir), ".claude")
	merged := readSettingsEnv(filepath.Join(dir, "settings.json"))
	for key, value := range readSettingsEnv(filepath.Join(dir, "settings.local.json")) {
		if value != "" {
			merged[key] = value
		}
	}
	return scopeValue(merged), nil
}

// effective folds two scope entries the way the launch path does: project
// values win, and an entry wins only if it configures anything at all.
func effective(app, project ScopeValue) Effective {
	for _, entry := range []struct {
		scope  string
		values ScopeValue
	}{{"project", project}, {"app", app}} {
		if entry.values.BaseURL != "" || entry.values.TokenSet || entry.values.Model != "" {
			return Effective{BaseURL: entry.values.BaseURL, Model: entry.values.Model, Source: entry.scope}
		}
	}
	return Effective{}
}

func scopeValue(env map[string]string) ScopeValue {
	return ScopeValue{BaseURL: env[keyBaseURL], TokenSet: env[keyToken] != "", Model: env[keyModel]}
}

// Set writes the entry into the scope's Claude settings file and returns the
// refreshed config for the same scope.
func (s *Service) Set(ctx context.Context, in SetInput) (Config, error) {
	if in.Scope != ScopeApp && in.Scope != ScopeProject {
		return Config{}, fmt.Errorf("gateway: unknown scope %q", in.Scope)
	}
	if in.Scope == ScopeProject && strings.TrimSpace(in.ProjectID) == "" {
		return Config{}, fmt.Errorf("gateway: projectId is required for the project scope")
	}
	if in.BaseURL != nil && strings.TrimSpace(*in.BaseURL) != "" {
		parsed, err := url.Parse(strings.TrimSpace(*in.BaseURL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return Config{}, fmt.Errorf("gateway: base URL must be an http(s) URL")
		}
	}
	// The empty string means "clear the key": a written empty value would
	// shadow inherited settings the way the resolver reads them, so clearing
	// is a deletion, not a write.
	path, err := s.scopePath(ctx, in.Scope, in.ProjectID)
	if err != nil {
		return Config{}, err
	}
	updates := map[string]*string{keyBaseURL: in.BaseURL, keyToken: in.Token, keyModel: in.Model}
	for key, update := range updates {
		if update != nil {
			*update = strings.TrimSpace(*update)
		}
		if err := applyToSettingsFile(path, key, update); err != nil {
			return Config{}, err
		}
	}
	return s.Get(ctx, in.ProjectID)
}

// scopePath resolves the settings file one scope writes: the app scope edits
// Claude Code's config directory, the project scope edits the workspace's
// .claude/settings.json.
func (s *Service) scopePath(ctx context.Context, scope Scope, projectID string) (string, error) {
	if scope == ScopeApp {
		dir, err := s.configDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "settings.json"), nil
	}
	dir, err := s.projects(ctx, projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(strings.TrimSpace(dir), ".claude", "settings.json"), nil
}

// Probe validates a base URL + token the way the resolver will use them,
// before anything is saved.
func (s *Service) Probe(ctx context.Context, baseURL, token string) (ProbeResult, error) {
	baseURL = strings.TrimSpace(baseURL)
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return ProbeResult{}, fmt.Errorf("gateway: base URL and token are required to probe")
	}
	if parsed, err := url.Parse(baseURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ProbeResult{}, fmt.Errorf("gateway: base URL must be an http(s) URL")
	}
	result := agentcreds.New(nil).Validate(ctx, agentcreds.Credential{
		Kind:     agentcreds.KindAuthToken,
		Source:   keyToken,
		Provider: agentcreds.ProviderGateway,
		Secret:   token,
		BaseURL:  baseURL,
	})
	models := make([]Model, 0, len(result.Models))
	for _, model := range result.Models {
		models = append(models, Model{ID: model.ID, DisplayName: model.DisplayName})
	}
	return ProbeResult{State: string(result.State), Detail: result.Detail, Models: models}, nil
}
