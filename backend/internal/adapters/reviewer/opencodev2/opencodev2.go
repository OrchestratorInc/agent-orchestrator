package opencodev2

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	workeragent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/agentrestore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const configContentPrefix = "OPENCODE_CONFIG_CONTENT="

type Reviewer struct {
	agent ports.Agent
}

func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerRestorer = (*Reviewer)(nil)

func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerOpenCodeV2
}

func (r *Reviewer) InvalidateBinaryResolution() {
	if invalidator, ok := r.agent.(ports.AgentBinaryResolutionInvalidator); ok {
		invalidator.InvalidateBinaryResolution()
	}
}

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	argv, err := r.agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		Config:           inv.Config,
		SessionID:        inv.ReviewerID,
		WorkspacePath:    inv.WorkspacePath,
		Prompt:           inv.Prompt,
		SystemPrompt:     inv.SystemPrompt,
		SystemPromptFile: inv.SystemPromptFile,
		Permissions:      ports.PermissionModeAcceptEdits,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	argv, content, err := applyReviewerPolicy(argv, inv.TaskPromptRoot)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{
		Argv: argv,
		Env:  map[string]string{"OPENCODE_CONFIG_CONTENT": content},
	}, nil
}

func (r *Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, ok, err := agentrestore.Command(ctx, r.agent, inv, agentrestore.Options{Permissions: ports.PermissionModeAcceptEdits})
	if err != nil || !ok {
		return cmd, ok, err
	}
	var content string
	cmd.Argv, content, err = applyReviewerPolicy(cmd.Argv, inv.TaskPromptRoot)
	if err != nil {
		return ports.ReviewCommandSpec{}, false, err
	}
	cmd.Env = map[string]string{"OPENCODE_CONFIG_CONTENT": content}
	return cmd, true, nil
}

func (r *Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{
		Mode:       ports.ReviewCancelInput,
		Inputs:     []string{"\x1b", "\x1b"},
		InputDelay: 150 * time.Millisecond,
	}, nil
}

type permissionRule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

func applyReviewerPolicy(argv []string, taskPromptRoot string) ([]string, string, error) {
	contentIndex := -1
	for i, arg := range argv {
		if strings.HasPrefix(arg, configContentPrefix) {
			contentIndex = i
			break
		}
	}
	if contentIndex < 0 {
		return nil, "", fmt.Errorf("opencode-v2 reviewer: worker command has no inline config")
	}

	config := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(argv[contentIndex], configContentPrefix)), &config); err != nil || config == nil {
		return nil, "", fmt.Errorf("opencode-v2 reviewer: worker inline config must be a JSON object")
	}
	var defaultAgent string
	if err := json.Unmarshal(config["default_agent"], &defaultAgent); err != nil || strings.TrimSpace(defaultAgent) == "" {
		return nil, "", fmt.Errorf("opencode-v2 reviewer: worker inline config has no default agent")
	}
	agents := map[string]json.RawMessage{}
	if err := json.Unmarshal(config["agents"], &agents); err != nil || agents == nil {
		return nil, "", fmt.Errorf("opencode-v2 reviewer: worker inline config agents must be an object")
	}
	agent := map[string]any{}
	if err := json.Unmarshal(agents[defaultAgent], &agent); err != nil || agent == nil {
		return nil, "", fmt.Errorf("opencode-v2 reviewer: default agent %q must be an object", defaultAgent)
	}
	agent["permissions"] = reviewerPermissions(taskPromptRoot)

	var err error
	agents[defaultAgent], err = json.Marshal(agent)
	if err != nil {
		return nil, "", fmt.Errorf("opencode-v2 reviewer: encode default agent: %w", err)
	}
	config["agents"], err = json.Marshal(agents)
	if err != nil {
		return nil, "", fmt.Errorf("opencode-v2 reviewer: encode agents: %w", err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, "", fmt.Errorf("opencode-v2 reviewer: encode inline config: %w", err)
	}
	content := string(data)
	argv[contentIndex] = configContentPrefix + content
	return argv, content, nil
}

func reviewerPermissions(taskPromptRoot string) []permissionRule {
	rules := []permissionRule{
		{Action: "*", Resource: "*", Effect: "deny"},
		{Action: "read", Resource: "*", Effect: "allow"},
		{Action: "glob", Resource: "*", Effect: "allow"},
		{Action: "grep", Resource: "*", Effect: "allow"},
	}
	if taskPromptRoot != "" {
		rules = append(rules, permissionRule{
			Action:   "external_directory",
			Resource: filepath.ToSlash(filepath.Join(taskPromptRoot, "**")),
			Effect:   "allow",
		})
	}
	for _, resource := range []string{
		"gh api repos/*",
		"git diff *",
		"git log *",
		"git show *",
		"git status *",
		"ao review submit *",
	} {
		rules = append(rules, permissionRule{Action: "shell", Resource: resource, Effect: "allow"})
	}
	for _, resource := range []string{
		"gh api -*",
		"gh api * -*",
		"gh api *>*",
		"git *>*",
		"ao review submit *>*",
		"gh api *<*",
		"git *<*",
		"ao review submit *<*",
		"gh api *|*",
		"git *|*",
		"ao review submit *|*",
		"gh api *;*",
		"git *;*",
		"ao review submit *;*",
		"gh api *&*",
		"git *&*",
		"ao review submit *&*",
		"gh api *`*",
		"git *`*",
		"ao review submit *`*",
		"gh api *$(*",
		"git *$(*",
		"ao review submit *$(*",
		"git diff *--output*",
		"git log *--output*",
		"git show *--output*",
	} {
		rules = append(rules, permissionRule{Action: "shell", Resource: resource, Effect: "deny"})
	}
	for _, resource := range []string{
		"gh api --method POST repos/*/pulls/*/reviews --input -",
		"gh api --method POST repos/*/pulls/*/reviews --input - --jq '.id'",
		"printf '%s' '*'",
	} {
		rules = append(rules, permissionRule{Action: "shell", Resource: resource, Effect: "allow"})
	}
	for _, resource := range []string{
		"printf '%s' '*' *>*",
		"printf '%s' '*' *<*",
		"printf '%s' '*' *|*",
		"printf '%s' '*' *;*",
		"printf '%s' '*' *&*",
	} {
		rules = append(rules, permissionRule{Action: "shell", Resource: resource, Effect: "deny"})
	}
	return rules
}
