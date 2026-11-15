package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	workeragent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/agentrestore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Reviewer struct {
	agent ports.Agent
}

func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerClaudeCode
}

func (r *Reviewer) InvalidateBinaryResolution() {
	if invalidator, ok := r.agent.(ports.AgentBinaryResolutionInvalidator); ok {
		invalidator.InvalidateBinaryResolution()
	}
}

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerRestorer = (*Reviewer)(nil)
var _ ports.ReviewerChatProfile = (*Reviewer)(nil)

func (r *Reviewer) ReviewChatHarness() domain.AgentHarness { return domain.HarnessClaudeCode }

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	agentSessionID, err := r.freshSessionID(ctx, inv)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	argv, err := r.agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		Config:           inv.Config,
		SessionID:        inv.ReviewerID,
		NativeSessionID:  agentSessionID,
		WorkspacePath:    inv.WorkspacePath,
		Prompt:           inv.Prompt,
		SystemPrompt:     inv.SystemPrompt,
		SystemPromptFile: inv.SystemPromptFile,
		Permissions:      ports.PermissionModeDefault,
		AllowedTools:     workeragent.ReviewerAllowedTools,
		DisallowedTools:  workeragent.ReviewerDisallowedTools,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	if argv, err = pinDefaultMode(argv); err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{Argv: argv, AgentSessionID: agentSessionID}, nil
}

func pinDefaultMode(argv []string) ([]string, error) {
	argv = slices.Clone(argv)
	end := slices.Index(argv, "--")
	if end < 0 {
		end = len(argv)
	}
	extra := []string{"--permission-mode", "default"}
	if i := slices.Index(argv[:end], "--settings"); i >= 0 && i+1 < end {
		settings := map[string]any{}
		if err := json.Unmarshal([]byte(argv[i+1]), &settings); err != nil {
			return nil, fmt.Errorf("claude-code reviewer: read launch settings: %w", err)
		}
		permissions, _ := settings["permissions"].(map[string]any)
		if permissions == nil {
			permissions = map[string]any{}
		}
		permissions["defaultMode"] = "default"
		settings["permissions"] = permissions
		encoded, err := json.Marshal(settings)
		if err != nil {
			return nil, fmt.Errorf("claude-code reviewer: write launch settings: %w", err)
		}
		argv[i+1] = string(encoded)
	} else {
		extra = append(extra, "--settings", `{"permissions":{"defaultMode":"default"}}`)
	}
	return slices.Insert(argv, end, extra...), nil
}

func (r *Reviewer) freshSessionID(ctx context.Context, inv ports.ReviewInvocation) (string, error) {
	derived := workeragent.SessionUUID(inv.ReviewerID)
	probe, canProbe := r.agent.(ports.AgentInterfaceHandoffHistoryProbe)
	minter, canMint := r.agent.(ports.AgentFreshNativeSessionIDProvider)
	if !canProbe || !canMint || inv.ReviewerID == "" {
		return derived, nil
	}
	session := ports.SessionRef{ID: inv.ReviewerID, WorkspacePath: inv.WorkspacePath}
	taken, err := probe.NativeConversationExists(ctx, session, derived, nil)
	if err != nil {
		return "", err
	}
	if taken {
		return minter.NewNativeSessionID(), nil
	}
	return derived, nil
}

func (r *Reviewer) PreLaunch(ctx context.Context, inv ports.ReviewInvocation) error {
	if hooks, ok := r.agent.(interface {
		GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error
	}); ok {
		if err := hooks.GetAgentHooks(ctx, ports.WorkspaceHookConfig{WorkspacePath: inv.WorkspacePath}); err != nil {
			return err
		}
	}
	pl, ok := r.agent.(interface {
		PreLaunch(context.Context, ports.LaunchConfig) error
	})
	if !ok {
		return nil
	}
	return pl.PreLaunch(ctx, ports.LaunchConfig{
		Config:        inv.Config,
		SessionID:     workeragent.SessionUUID(inv.ReviewerID),
		WorkspacePath: inv.WorkspacePath,
		Permissions:   ports.PermissionModeAuto,
	})
}

func (r *Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	if migratedID, ok, err := r.restoreSessionID(ctx, inv); err != nil {
		return ports.ReviewCommandSpec{}, false, err
	} else if !ok {
		return ports.ReviewCommandSpec{}, false, nil
	} else if migratedID != "" {
		inv.AgentSessionID = migratedID
	}
	cmd, ok, err := agentrestore.Command(ctx, r.agent, inv, agentrestore.Options{
		Permissions:     ports.PermissionModeDefault,
		AllowedTools:    workeragent.ReviewerAllowedTools,
		DisallowedTools: workeragent.ReviewerDisallowedTools,
	})
	if err != nil || !ok {
		return cmd, ok, err
	}
	if cmd.Argv, err = pinDefaultMode(cmd.Argv); err != nil {
		return ports.ReviewCommandSpec{}, false, err
	}
	if cmd.AgentSessionID == "" {
		cmd.AgentSessionID = workeragent.SessionUUID(inv.ReviewerID)
	}
	return cmd, true, nil
}

func (r *Reviewer) restoreSessionID(ctx context.Context, inv ports.ReviewInvocation) (string, bool, error) {
	persistedID := strings.TrimSpace(inv.AgentSessionID)
	probe, ok := r.agent.(ports.AgentInterfaceHandoffHistoryProbe)
	if !ok {
		return persistedID, true, nil
	}
	session := ports.SessionRef{ID: inv.ReviewerID, WorkspacePath: inv.WorkspacePath}
	if persistedID == "" {
		fallbackID := workeragent.SessionUUID(inv.ReviewerID)
		exists, err := probe.NativeConversationExists(ctx, session, fallbackID, nil)
		if err != nil {
			return "", false, err
		}
		if exists {
			return fallbackID, true, nil
		}
		return "", false, nil
	}
	exists, err := probe.NativeConversationExists(ctx, session, persistedID, nil)
	if err != nil {
		return "", false, err
	}
	if exists {
		return persistedID, true, nil
	}
	if persistedID != workeragent.SessionUUID(inv.ReviewerID) {
		return "", false, nil
	}
	legacyID := workeragent.SessionUUID(persistedID)
	exists, err = probe.NativeConversationExists(ctx, session, legacyID, nil)
	if err != nil {
		return "", false, err
	}
	if exists {
		return legacyID, true, nil
	}
	return "", false, nil
}

func (r *Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{
		Mode:       ports.ReviewCancelInput,
		Inputs:     []string{"\x1b", "\x1b"},
		InputDelay: 150 * time.Millisecond,
	}, nil
}
