package legacyimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	yaml "gopkg.in/yaml.v3"
)

type legacyConfig struct {
	Projects map[string]legacyProjectConfig `yaml:"projects"`
}

type legacyProjectConfig struct {
	Path             string             `yaml:"path"`
	Name             string             `yaml:"name"`
	Repo             *yaml.Node         `yaml:"repo"`
	DefaultBranch    string             `yaml:"defaultBranch"`
	SessionPrefix    string             `yaml:"sessionPrefix"`
	Env              map[string]string  `yaml:"env"`
	Symlinks         []string           `yaml:"symlinks"`
	PostCreate       []string           `yaml:"postCreate"`
	AgentConfig      *legacyAgentConfig `yaml:"agentConfig"`
	Worker           *legacyRole        `yaml:"worker"`
	Orchestrator     *legacyRole        `yaml:"orchestrator"`
	AgentRules       *yaml.Node         `yaml:"agentRules"`
	AgentRulesFile   *yaml.Node         `yaml:"agentRulesFile"`
	OrchestratorRule *yaml.Node         `yaml:"orchestratorRules"`

	Tracker   *yaml.Node `yaml:"tracker"`
	SCM       *yaml.Node `yaml:"scm"`
	Runtime   *yaml.Node `yaml:"runtime"`
	Workspace *yaml.Node `yaml:"workspace"`
	Reactions *yaml.Node `yaml:"reactions"`
}

type legacyAgentConfig struct {
	Model       string `yaml:"model"`
	Permissions string `yaml:"permissions"`
}

type legacyRole struct {
	Agent       string             `yaml:"agent"`
	AgentConfig *legacyAgentConfig `yaml:"agentConfig"`
}

func loadLegacyConfig(root string) (legacyConfig, error) {
	data, err := os.ReadFile(globalConfigPath(root))
	if os.IsNotExist(err) {
		return legacyConfig{}, nil
	}
	if err != nil {
		return legacyConfig{}, fmt.Errorf("read legacy config: %w", err)
	}
	var cfg legacyConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		var typeErr *yaml.TypeError
		if !errors.As(err, &typeErr) {
			return legacyConfig{}, fmt.Errorf("parse legacy config.yaml: %w", err)
		}
	}
	return cfg, nil
}

type preferences struct {
	Projects map[string]struct {
		DisplayName string `json:"displayName"`
	} `json:"projects"`
}

func loadPreferences(root string) preferences {
	var p preferences
	data, err := os.ReadFile(preferencesPath(root))
	if err != nil {
		return p
	}
	_ = json.Unmarshal(data, &p)
	return p
}

type registeredManifest struct {
	Projects []struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		AddedAt string `json:"addedAt"`
	} `json:"projects"`
}

func loadRegistered(root string) registeredManifest {
	var m registeredManifest
	data, err := os.ReadFile(registeredPath(root))
	if err != nil {
		return m
	}
	_ = json.Unmarshal(data, &m)
	return m
}

func (m registeredManifest) addedAt(id, path string) string {
	for _, p := range m.Projects {
		if p.ID == id && p.AddedAt != "" {
			return p.AddedAt
		}
	}
	for _, p := range m.Projects {
		if p.Path == path && p.AddedAt != "" {
			return p.AddedAt
		}
	}
	return ""
}
