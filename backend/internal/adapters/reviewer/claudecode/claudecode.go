// Package claudecode is the claude-code reviewer adapter. claude-code is a
// prompt-driven agent, so this reviewer feeds AO's review prompt (authored
// centrally and passed in ReviewInvocation.Prompt) to the worker claude-code
// adapter's launch-command construction (binary resolution, flags). The reviewer
// contract stays prompt-agnostic, so a one-shot CLI reviewer (e.g. greptile) can
// ignore the prompt entirely.
package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	// executable resolves the AO binary the review-guard hook runs; nil means
	// os.Executable (the daemon is the ao binary).
	executable func() (string, error)
}

// New builds the claude-code reviewer adapter.
func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

// Harness identifies this reviewer in the reviewer registry.
func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerClaudeCode
}

// ReviewEnforcement reports how the reviewer policy is enforced: a PreToolUse
// hook passed through AO's own launch settings asks reviewpolicy before every
// tool call.
func (*Reviewer) ReviewEnforcement() domain.ReviewerEnforcement {
	return domain.ReviewerEnforcedHook
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

// reviewerAllowedTools pre-approves the common read-only calls so the
// headless reviewer does not prompt for them. They are a convenience, not the
// boundary: every tool call first passes the review-guard PreToolUse hook (see
// reviewerLaunchSettings), which applies the shared reviewer policy
// (reviewpolicy) in every permission mode. Commands outside this list that the
// policy admits prompt, and the PermissionRequest hook answers those with the
// same policy (see cli.reviewerPermissionDecision), so the pane never stalls.
var reviewerAllowedTools = []string{
	"Read",
	"Grep",
	"Glob",
	"Bash(printf:*)",
	"Bash(gh pr view:*)",
	"Bash(gh pr diff:*)",
	"Bash(gh pr checks:*)",
	"Bash(git diff:*)",
	"Bash(git log:*)",
	"Bash(git show:*)",
	"Bash(git status:*)",
	"Bash(ao review submit:*)",
}

// reviewerDisallowedTools hard-denies the write paths as defense in depth
// behind the review-guard hook.
var reviewerDisallowedTools = []string{
	"Edit",
	"Write",
	"NotebookEdit",
	"Bash(git push:*)",
	"Bash(git commit:*)",
	// The reviewer must never merge. No allow rule admits it, so this only
	// documents the intent and survives a future broader gh allow entry.
	"Bash(gh pr merge:*)",
}

// reviewHookTimeoutSeconds bounds each AO hook call, matching the worker hooks.
const reviewHookTimeoutSeconds = 30

// reviewerLaunchSettings is the AO-owned Claude flag settings every reviewer
// launch carries. They pin default mode, forbid switching into
// bypassPermissions, and register the hooks that enforce the shared reviewer
// policy: PreToolUse (a deny there holds in every mode and for subagents) and
// PermissionRequest (answers the prompts default mode raises). The hooks run
// the absolute AO binary and live in the launch flags rather than the
// checkout's .claude/settings.local.json, which the code under review controls.
func (r *Reviewer) reviewerLaunchSettings() (map[string]any, error) {
	resolve := r.executable
	if resolve == nil {
		resolve = os.Executable
	}
	executable, err := resolve()
	if err != nil {
		return nil, fmt.Errorf("claude-code reviewer: resolve AO executable for the review guard: %w", err)
	}
	if !filepath.IsAbs(executable) {
		if executable, err = filepath.Abs(executable); err != nil {
			return nil, fmt.Errorf("claude-code reviewer: make AO executable absolute: %w", err)
		}
	}
	hook := func(event string) []any {
		return []any{map[string]any{"hooks": []any{map[string]any{
			"type":    "command",
			"command": quoteHookExecutable(executable) + " hooks claude-code " + event,
			"timeout": reviewHookTimeoutSeconds,
		}}}}
	}
	return map[string]any{
		"permissions": map[string]any{
			"defaultMode":                  "default",
			"disableBypassPermissionsMode": "disable",
		},
		"hooks": map[string]any{
			"PreToolUse":        hook("review-guard"),
			"PermissionRequest": hook("permission-request"),
		},
	}, nil
}

// quoteHookExecutable quotes the AO path for the shell Claude Code runs hook
// commands through.
func quoteHookExecutable(path string) string {
	if runtime.GOOS == "windows" {
		return `"` + path + `"`
	}
	return `'` + strings.ReplaceAll(path, `'`, `'"'"'`) + `'`
}

// ReviewCommand builds a claude-code invocation that reviews the worker's
// checkout for the PR. Production launches provide the standing instructions
// through an AO-owned prompt file so only the short task-file reference is
// terminal-visible.
func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	settings, err := r.reviewerLaunchSettings()
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
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
		// Default mode keeps every decision with AO's hooks and allow/deny
		// lists: auto mode lets Claude's classifier approve commands before the
		// PermissionRequest hook is consulted (#6842). The flag settings pin the
		// mode so a user's own defaultMode cannot override it.
		Permissions:      ports.PermissionModeDefault,
		AllowedTools:     reviewerAllowedTools,
		DisallowedTools:  reviewerDisallowedTools,
		ProviderSettings: settings,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{Argv: argv, AgentSessionID: agentSessionID}, nil
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
		// The mode ReviewCommand launches with.
		Permissions: ports.PermissionModeDefault,
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
	settings, err := r.reviewerLaunchSettings()
	if err != nil {
		return ports.ReviewCommandSpec{}, false, err
	}
	cmd, ok, err := agentrestore.Command(ctx, r.agent, inv, agentrestore.Options{
		Permissions:      ports.PermissionModeDefault,
		AllowedTools:     reviewerAllowedTools,
		DisallowedTools:  reviewerDisallowedTools,
		ProviderSettings: settings,
	})
	if err != nil || !ok {
		return cmd, ok, err
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
