package zcode

import (
	"context"
	"fmt"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"strings"
)

// Historical unsupported TUI recipe, retained solely to reproduce the original
// invalid-flag/private-context failures. It is never a production launch path.
// GetLaunchCommand builds `zcode [--mode <mode>] [--disallowed-tools <list>]`
// and leaves the prompt for AO's after-start terminal delivery. Zcode's
// --prompt/-p flags are headless single-turn modes, and a bare positional
// argument is parsed as a subcommand, so the TUI must start empty.
func (p *Plugin) legacyGetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	if err := validateStandingInstructions(ctx, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return nil, err
	}
	binary, err := p.zcodeBinary(ctx)
	if err != nil {
		return nil, err
	}

	if len(cfg.AllowedTools) != 0 {
		return nil, fmt.Errorf("zcode: tool allowlists are unsupported")
	}
	cmd = make([]string, 0, 8)
	cmd = append(cmd, binary)
	if err := appendModeFlags(&cmd, cfg.Permissions, cfg.Config.Mode); err != nil {
		return nil, err
	}
	if err := appendDisallowedTools(&cmd, cfg.DisallowedTools); err != nil {
		return nil, err
	}
	cmd = append(cmd, "--trust-workspace-hooks")

	if err := p.prepareHookTrust(ctx, binary, cfg.WorkspacePath, cfg.Env); err != nil {
		return nil, err
	}
	return cmd, nil
}

// GetRestoreCommand resumes a prior ZCode session by its captured id, building
// `zcode [--mode <mode>] --resume <agentSessionId>` when we have a captured
// native session id. ok=false otherwise, so the restore manager falls back to
// a fresh launch. `-c/--continue` (latest session for the directory) is
// deliberately not used as that fallback: it could resume an unrelated
// session that happens to share the worktree.
func (p *Plugin) legacyGetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := validateStandingInstructions(ctx, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}
	if !nativeIDPattern.MatchString(agentSessionID) {
		return nil, false, fmt.Errorf("zcode: invalid native session ID")
	}
	if len(cfg.AllowedTools) != 0 {
		return nil, false, fmt.Errorf("zcode: tool allowlists are unsupported")
	}

	binary, err := p.zcodeBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = make([]string, 0, 5)
	cmd = append(cmd, binary)
	if err := appendModeFlags(&cmd, cfg.Permissions, cfg.Config.Mode); err != nil {
		return nil, false, err
	}
	if err := appendDisallowedTools(&cmd, cfg.DisallowedTools); err != nil {
		return nil, false, err
	}
	cmd = append(cmd, "--trust-workspace-hooks")
	validate := p.validateRestore
	if validate == nil {
		validate = validateNativeRestore
	}
	if err := validate(ctx, cfg.Session.WorkspacePath, agentSessionID, cfg.Env); err != nil {
		return nil, false, err
	}
	if err := p.prepareHookTrust(ctx, binary, cfg.Session.WorkspacePath, cfg.Env); err != nil {
		return nil, false, err
	}
	cmd = append(cmd, "--resume", agentSessionID)
	return cmd, true, nil
}

func appendModeFlags(cmd *[]string, permissions ports.PermissionMode, configMode string) error {
	if mode := strings.TrimSpace(configMode); mode != "" {
		// ZCode's own vocabulary — a config written by another path must
		// fail as a clean input error, not reach argv and kill the terminal
		// session at launch.
		switch mode {
		case "build", "edit", "plan", "yolo":
		default:
			return fmt.Errorf("invalid zcode mode %q: want one of build, edit, plan, yolo", mode)
		}
		*cmd = append(*cmd, "--mode", mode)
		return nil
	}
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
		*cmd = append(*cmd, "--mode", "build") // Do not inherit a saved yolo mode.
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--mode", "edit")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--mode", "build")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--mode", "yolo")
	}
	return nil
}

// appendDisallowedTools emits the deny list as a single comma-joined
// --disallowed-tools value. Zcode 0.16.5 has no --allowed-tools flag, so
// allow lists are not forwarded. A rule containing a comma or whitespace is
// rejected: the flag's parser splits on BOTH separators ("Comma or
// space-separated list"), so a literal separator inside a rule would
// silently split it and weaken the deny list.
func appendDisallowedTools(cmd *[]string, disallowed []string) error {
	for _, rule := range disallowed {
		if strings.ContainsAny(rule, ", 	") {
			return fmt.Errorf("zcode: disallowed tool rule %q contains a comma or whitespace; tool rules are joined into a single flag value whose parser splits on both, so a literal separator would silently split the rule", rule)
		}
	}
	if len(disallowed) > 0 {
		*cmd = append(*cmd, "--disallowed-tools", strings.Join(disallowed, ","))
	}
	return nil
}
