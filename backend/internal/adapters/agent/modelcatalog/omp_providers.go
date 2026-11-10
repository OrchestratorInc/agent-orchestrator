package modelcatalog

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ompConfiguredProviderSet returns the providers this OMP installation holds
// configuration for, and whether any store answered: every provider with an
// active login in agent.db plus every provider models.yml declares, since an
// API-key provider needs no database row. Only provider names are read; keys,
// URLs and model overrides stay local.
func ompConfiguredProviderSet(ctx context.Context, env map[string]string) ([]string, bool) {
	if dir := piCatalogDir("omp", env); dir != "" {
		return ompProviderConfiguration(ctx, dir)
	}
	return nil, false
}

// ompProviderConfiguration reads the provider set from an OMP configuration
// directory. ok is false when neither store could be read, which says nothing
// about which providers the installation has.
func ompProviderConfiguration(ctx context.Context, dir string) ([]string, bool) {
	providers, hasDatabase := ompConfiguredProviders(ctx, filepath.Join(dir, "agent.db"))
	declared, hasConfig := ompDeclaredProviders(filepath.Join(dir, "models.yml"))
	if !hasDatabase && !hasConfig {
		return nil, false
	}
	configured := uniqueProviders(append(providers, declared...))
	sort.Strings(configured)
	return configured, true
}

// ompDeclaredProviders returns the provider sections of OMP's models.yml. A
// declared section is a provider the user enabled: OMP lists its models from
// that section's endpoint and credential.
func ompDeclaredProviders(path string) ([]string, bool) {
	raw, err := readModelConfig(path)
	if err != nil {
		return nil, false
	}
	var config struct {
		Providers map[string]any `yaml:"providers"`
	}
	if yaml.Unmarshal(raw, &config) != nil {
		return nil, false
	}
	providers := make([]string, 0, len(config.Providers))
	for provider := range config.Providers {
		if trimmed := strings.TrimSpace(provider); trimmed != "" {
			providers = append(providers, trimmed)
		}
	}
	sort.Strings(providers)
	return providers, true
}

// ompLimitCatalog drops the cached models of every provider OMP is not set up
// for. OMP's model cache is the models.dev snapshot for every provider its build
// ships, while `omp models` lists only what this installation can run and
// models.yml/agent.db name the providers the user enabled; those two are the
// catalog the pickers should offer. Models the native command returned are always
// kept, and when no configuration store answered the catalog is left alone
// rather than guessed at.
func ompLimitCatalog(ctx context.Context, env map[string]string, native, models []ports.AgentModelInfo, providers []string) ([]ports.AgentModelInfo, []string) {
	enabled, known := ompConfiguredProviderSet(ctx, env)
	if !known {
		return models, providers
	}
	for _, model := range native {
		if model.Provider != "" {
			enabled = append(enabled, model.Provider)
		}
	}
	keep := make(map[string]bool, len(enabled))
	for _, provider := range enabled {
		keep[provider] = true
	}
	keptModels := models[:0]
	for _, model := range models {
		if keep[model.Provider] {
			keptModels = append(keptModels, model)
		}
	}
	keptProviders := providers[:0]
	for _, provider := range providers {
		if keep[provider] {
			keptProviders = append(keptProviders, provider)
		}
	}
	return keptModels, keptProviders
}
