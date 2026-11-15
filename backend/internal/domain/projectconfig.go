package domain

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
)

type ProjectConfig struct {
	CanonicalRepoURL string `json:"canonicalRepoURL,omitempty"`
	DefaultBranch    string `json:"defaultBranch,omitempty"`
	SessionPrefix    string `json:"sessionPrefix,omitempty"`

	Env        map[string]string `json:"env,omitempty"`
	Symlinks   []string          `json:"symlinks,omitempty"`
	PostCreate []string          `json:"postCreate,omitempty"`
	PreRemove  []string          `json:"preRemove,omitempty"`

	AgentRules            string `json:"agentRules,omitempty"`
	AgentRulesFile        string `json:"agentRulesFile,omitempty"`
	OrchestratorRules     string `json:"orchestratorRules,omitempty"`
	OrchestratorRulesFile string `json:"orchestratorRulesFile,omitempty"`

	AgentConfig  AgentConfig  `json:"agentConfig,omitempty"`
	Worker       RoleOverride `json:"worker,omitempty"`
	Orchestrator RoleOverride `json:"orchestrator,omitempty"`

	Reviewers     []ReviewerConfig    `json:"reviewers,omitempty"`
	TrackerIntake TrackerIntakeConfig `json:"trackerIntake,omitempty"`

	ContainerReap ContainerReapConfig `json:"containerReap,omitempty"`

	AutoReview bool `json:"autoReview,omitempty"`

	WorkersRequestReview bool `json:"workersRequestReview,omitempty"`
}

type ContainerReapConfig struct {
	Disabled bool `json:"disabled,omitempty"`
}

type ReviewerConfig struct {
	Harness     ReviewerHarness `json:"harness"`
	AgentConfig AgentConfig     `json:"agentConfig,omitempty"`
}

const FallbackReviewerHarness = ReviewerClaudeCode

func (c ProjectConfig) ResolveReviewerHarness(worker AgentHarness) ReviewerHarness {
	if len(c.Reviewers) > 0 {
		return c.Reviewers[0].Harness
	}
	switch worker {
	case HarnessClaudeCode:
		return ReviewerClaudeCode
	case HarnessCodex:
		return ReviewerCodex
	case HarnessOpenCode:
		return ReviewerOpenCode
	case HarnessMuse:
		return ReviewerMuse
	case HarnessKimchi:
		return ReviewerKimchi
	}
	return FallbackReviewerHarness
}

func (c ProjectConfig) DefaultWorkerReviewer() (ReviewerHarness, AgentConfig) {
	worker := c.Worker.Harness
	if worker == "" {
		return "", AgentConfig{}
	}
	harness := c.ResolveReviewerHarness(worker)
	if string(harness) != string(worker) {
		return "", AgentConfig{}
	}
	config := AgentConfig{Model: c.AgentConfig.Model, Effort: c.AgentConfig.Effort}
	if c.Worker.AgentConfig.Model != "" {
		config.Model = c.Worker.AgentConfig.Model
	}
	if c.Worker.AgentConfig.Effort != "" {
		config.Effort = c.Worker.AgentConfig.Effort
	}
	return harness, config
}

type RoleOverride struct {
	Harness     AgentHarness `json:"agent,omitempty"`
	AgentConfig AgentConfig  `json:"agentConfig,omitempty"`
}

const (
	DefaultBranchAuto = "auto"
	DefaultBranchName = "main"
)

func DefaultProjectConfig() ProjectConfig {
	return ProjectConfig{
		DefaultBranch: DefaultBranchAuto,
	}
}

func (c ProjectConfig) WithDefaults() ProjectConfig {
	def := DefaultProjectConfig()
	if c.DefaultBranch == "" {
		c.DefaultBranch = def.DefaultBranch
	}
	c.TrackerIntake = c.TrackerIntake.WithDefaults()
	return c
}

func (c ProjectConfig) WorktreeBaseBranch() string {
	branch := c.WithDefaults().DefaultBranch
	if branch == DefaultBranchAuto {
		return ""
	}
	return branch
}

func (c ProjectConfig) IsZero() bool {
	return reflect.DeepEqual(c, ProjectConfig{})
}

func (c ProjectConfig) Validate() error {
	if c.CanonicalRepoURL != "" {
		if err := c.ValidateCanonicalRepository(c.CanonicalRepoURL); err != nil {
			return err
		}
	}
	if err := c.AgentConfig.Validate(); err != nil {
		return err
	}
	if err := validateNameComponent("sessionPrefix", c.SessionPrefix); err != nil {
		return err
	}
	seenEnv := make(map[string]bool, len(c.Env))
	for key, value := range c.Env {
		if !validEnvName(key) || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("env %q: invalid variable name or value", key)
		}
		folded := strings.ToUpper(key)
		if strings.HasPrefix(folded, "AO_") {
			return fmt.Errorf("env %q: AO_ prefix is reserved", key)
		}
		if seenEnv[folded] {
			return fmt.Errorf("env %q: duplicate variable name", key)
		}
		seenEnv[folded] = true
	}
	for role, ro := range map[string]RoleOverride{"worker": c.Worker, "orchestrator": c.Orchestrator} {
		if ro.Harness != "" && !ro.Harness.IsKnown() {
			return fmt.Errorf("%s.agent: unknown harness %q", role, ro.Harness)
		}
		if err := ro.AgentConfig.Validate(); err != nil {
			return fmt.Errorf("%s.%w", role, err)
		}
	}
	for _, s := range c.Symlinks {
		if err := validateRepoRelative(s); err != nil {
			return fmt.Errorf("symlink %q: %w", s, err)
		}
	}
	if err := validateScriptSteps("postCreate", c.PostCreate); err != nil {
		return err
	}
	if err := validateScriptSteps("preRemove", c.PreRemove); err != nil {
		return err
	}
	if err := validateRepoRelative(c.AgentRulesFile); err != nil {
		return fmt.Errorf("agentRulesFile %q: %w", c.AgentRulesFile, err)
	}
	if err := validateRepoRelative(c.OrchestratorRulesFile); err != nil {
		return fmt.Errorf("orchestratorRulesFile %q: %w", c.OrchestratorRulesFile, err)
	}
	for i, rv := range c.Reviewers {
		if !rv.Harness.IsKnown() {
			return fmt.Errorf("reviewers[%d].harness: unknown harness %q", i, rv.Harness)
		}
		if err := rv.AgentConfig.Validate(); err != nil {
			return fmt.Errorf("reviewers[%d].agentConfig: %w", i, err)
		}
	}
	if err := c.TrackerIntake.Validate(); err != nil {
		return err
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func validateNoWhitespaceField(name, value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s: must not have leading or trailing whitespace", name)
	}
	return nil
}

func validateNameComponent(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.ContainsAny(trimmed, `/\`) || trimmed == "." || trimmed == ".." {
		return fmt.Errorf("%s: must not contain path separators or traversal components", name)
	}
	return nil
}

func validateRepoRelative(p string) error {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return nil
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return fmt.Errorf("path must be repo-relative and must not escape the project root")
	}
	clean := filepath.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path must be repo-relative and must not escape the project root")
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return fmt.Errorf("path must be repo-relative and must not escape the project root")
		}
	}
	return nil
}
