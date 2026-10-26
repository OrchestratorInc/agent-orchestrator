// Package claudecode is the claude-code reviewer adapter. claude-code is a
// prompt-driven agent, so this reviewer feeds AO's review prompt (authored
// centrally and passed in ReviewInvocation.Prompt) to the worker claude-code
// adapter's launch-command construction (binary resolution, flags). The reviewer
// contract stays prompt-agnostic, so a one-shot CLI reviewer (e.g. greptile) can
// ignore the prompt entirely.
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

// Reviewer is the claude-code code-review adapter.
type Reviewer struct {
	agent ports.Agent
}

// New builds the claude-code reviewer adapter.
func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

// Harness identifies this reviewer in the reviewer registry.
func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerClaudeCode
}

// InvalidateBinaryResolution forgets the worker plugin's cached executable, so
// the next review runs the release an install just switched to.
func (r *Reviewer) InvalidateBinaryResolution() {
	if invalidator, ok := r.agent.(ports.AgentBinaryResolutionInvalidator); ok {
		invalidator.InvalidateBinaryResolution()
	}
}

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerRestorer = (*Reviewer)(nil)
var _ ports.ReviewerChatProfile = (*Reviewer)(nil)

// ReviewChatHarness runs review passes in Claude Code's typed Chat. The review
// conversation is read-only, so the Chat launches with the same tool lists and
// answers permission prompts with the same submit-command rule as the pane.
func (r *Reviewer) ReviewChatHarness() domain.AgentHarness { return domain.HarnessClaudeCode }

// ReviewCommand builds a claude-code invocation that reviews the worker's
// checkout for the PR. Production launches provide the standing instructions
// through an AO-owned prompt file so only the short task-file reference is
// terminal-visible.
func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	agentSessionID := workeragent.SessionUUID(inv.ReviewerID)
	argv, err := r.agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		Config: inv.Config,
		// Pin the same deterministic reviewer-native id we persist. Hooks can
		// later replace it with Claude's reported id, but restore must never start
		// from an id that the process was not launched with.
		SessionID:        inv.ReviewerID,
		NativeSessionID:  agentSessionID,
		WorkspacePath:    inv.WorkspacePath,
		Prompt:           inv.Prompt,
		SystemPrompt:     inv.SystemPrompt,
		SystemPromptFile: inv.SystemPromptFile,
		// AO's default mode adds no flag; pinDefaultMode then pins Claude's.
		Permissions:     ports.PermissionModeDefault,
		AllowedTools:    workeragent.ReviewerAllowedTools,
		DisallowedTools: workeragent.ReviewerDisallowedTools,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	if argv, err = pinDefaultMode(argv); err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{Argv: argv, AgentSessionID: agentSessionID}, nil
}

// pinDefaultMode starts the reviewer pane in Claude's default ("Manual") mode,
// whatever the user's own defaultMode is. Not bypassPermissions, which ignores
// the allow list; not auto, whose classifier approves shell commands no rule
// names (touch, printf > file) without raising the PermissionRequest prompt the
// reviewer hook answers. In default mode every such call reaches that hook,
// which denies all but the exact submit commands. The flag-settings defaultMode
// keeps Claude's one-time auto-mode dialog out of the pane, as AO does for its
// explicit modes (see the worker adapter's autoDefaultNudgeGuardArgs).
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

// PreLaunch runs reviewer-specific preflight. For Claude Code this installs the
// selected reviewer's hooks and records the worker checkout as trusted before
// the headless reviewer pane starts.
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
		// Any explicit mode: the pane pins its mode in flag settings
		// (pinDefaultMode), so the user's global config must not be marked.
		Permissions: ports.PermissionModeAuto,
	})
}

// ReviewMessage is the text injected into an already-running reviewer pane to
// review a new commit — AO's central review prompt.
func (r *Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

// ReviewRestoreCommand resumes the reviewer Claude Code conversation captured
// from hooks, reapplying the same read-only tool policy as a fresh review launch.
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

// restoreSessionID verifies an explicitly persisted Claude conversation before
// asking Claude to resume it. Reviewer builds affected by #4658 persisted the
// single-derived UUID but launched Claude with that UUID derived a second time.
// Prefer the persisted identity when its transcript exists; otherwise migrate
// only that exact legacy shape when the double-derived transcript is present.
// Returning ok=false lets the launcher recreate an idle reviewer instead of
// starting a doomed `claude --resume` command.
func (r *Reviewer) restoreSessionID(ctx context.Context, inv ports.ReviewInvocation) (string, bool, error) {
	persistedID := strings.TrimSpace(inv.AgentSessionID)
	probe, ok := r.agent.(ports.AgentInterfaceHandoffHistoryProbe)
	if !ok {
		return persistedID, true, nil
	}
	session := ports.SessionRef{ID: inv.ReviewerID, WorkspacePath: inv.WorkspacePath}
	if persistedID == "" {
		// No native id was ever captured — e.g. the daemon restarted before the
		// hook fired on a first-ever pass. agentrestore.Command's caller falls
		// back to deriving the same deterministic id from inv.ReviewerID
		// unconditionally, so probe that id here too rather than reporting a
		// resume for a transcript that may never have been created.
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

// ReviewCancel stops the active Claude Code reviewer turn while preserving the
// terminal pane for inspection. Claude Code treats Ctrl-C as an exit path in
// common TUI states, so use Escape to interrupt active execution without
// tearing down the harness.
func (r *Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{
		Mode:       ports.ReviewCancelInput,
		Inputs:     []string{"\x1b", "\x1b"},
		InputDelay: 150 * time.Millisecond,
	}, nil
}
