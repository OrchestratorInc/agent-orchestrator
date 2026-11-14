package cline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	clineHooksDirName = ".clinerules"
	clineHooksSubDir  = "hooks"

	clineHookCommandPrefix = "ao hooks cline "

	clineHookMarker = "# ao-managed-cline-hook"
)

type clineHookSpec struct {
	Event      string
	Subcommand string
}

var clineManagedHooks = []clineHookSpec{
	{Event: "TaskStart", Subcommand: "session-start"},
	{Event: "TaskResume", Subcommand: "session-start"},
	{Event: "UserPromptSubmit", Subcommand: "user-prompt-submit"},
	{Event: "PreToolUse", Subcommand: "permission-resolved"},
	{Event: "TaskComplete", Subcommand: "stop"},
	{Event: "TaskCancel", Subcommand: "stop"},
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("cline.GetAgentHooks: WorkspacePath is required")
	}

	hooksDir := clineHooksDir(cfg.WorkspacePath)
	if err := os.MkdirAll(hooksDir, 0o750); err != nil {
		return fmt.Errorf("cline.GetAgentHooks: create hook dir: %w", err)
	}

	written := make([]string, 0, len(clineManagedHooks))
	for _, spec := range clineManagedHooks {
		scriptPath := filepath.Join(hooksDir, clineHookScriptName(spec.Event))
		if hookutil.FileExists(scriptPath) && !isManagedClineHook(scriptPath) {
			continue
		}
		script := renderClineHookScript(spec.Subcommand)
		if err := hookutil.AtomicWriteFile(scriptPath, []byte(script), 0o700); err != nil {
			return fmt.Errorf("cline.GetAgentHooks: write %s: %w", spec.Event, err)
		}
		written = append(written, clineHookScriptName(spec.Event))
	}
	for _, spec := range clineManagedHooks {
		legacy := clineLegacyScriptName(spec.Event)
		if legacy == clineHookScriptName(spec.Event) {
			continue
		}
		legacyPath := filepath.Join(hooksDir, legacy)
		if hookutil.FileExists(legacyPath) && isManagedClineHook(legacyPath) {
			if err := os.Remove(legacyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("cline.GetAgentHooks: remove legacy %s: %w", spec.Event, err)
			}
		}
	}
	if err := hookutil.EnsureWorkspaceGitignore(hooksDir, written...); err != nil {
		return fmt.Errorf("cline.GetAgentHooks: gitignore: %w", err)
	}
	return nil
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("cline.UninstallHooks: workspacePath is required")
	}

	hooksDir := clineHooksDir(workspacePath)
	if _, err := os.Stat(hooksDir); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	for _, spec := range clineManagedHooks {
		for _, name := range clineAllScriptNames(spec.Event) {
			scriptPath := filepath.Join(hooksDir, name)
			if !hookutil.FileExists(scriptPath) || !isManagedClineHook(scriptPath) {
				continue
			}
			if err := os.Remove(scriptPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("cline.UninstallHooks: remove %s: %w", spec.Event, err)
			}
		}
	}
	return nil
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("cline.AreHooksInstalled: workspacePath is required")
	}

	hooksDir := clineHooksDir(workspacePath)
	if _, err := os.Stat(hooksDir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	for _, spec := range clineManagedHooks {
		for _, name := range clineAllScriptNames(spec.Event) {
			scriptPath := filepath.Join(hooksDir, name)
			if hookutil.FileExists(scriptPath) && isManagedClineHook(scriptPath) {
				return true, nil
			}
		}
	}
	return false, nil
}

func clineHooksDir(workspacePath string) string {
	return filepath.Join(workspacePath, clineHooksDirName, clineHooksSubDir)
}

func clineHookScriptName(event string) string {
	if runtime.GOOS == "windows" {
		return event + ".ps1"
	}
	return event
}

func clineLegacyScriptName(event string) string {
	if runtime.GOOS == "windows" {
		return event
	}
	return event + ".ps1"
}

func clineAllScriptNames(event string) []string {
	current := clineHookScriptName(event)
	legacy := clineLegacyScriptName(event)
	if legacy == current {
		return []string{current}
	}
	return []string{current, legacy}
}

func renderClineHookScript(subcommand string) string {
	if runtime.GOOS == "windows" {
		return renderPowerShellClineHookScript(subcommand)
	}
	return renderBashClineHookScript(subcommand)
}

func renderBashClineHookScript(subcommand string) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString(clineHookMarker + "\n")
	b.WriteString(clineHookCommandPrefix + subcommand + " || true\n")
	b.WriteString(`echo '{"cancel": false}'` + "\n")
	return b.String()
}

func renderPowerShellClineHookScript(subcommand string) string {
	var b strings.Builder
	b.WriteString(clineHookMarker + "\n")
	b.WriteString("try {\n")
	b.WriteString("  $psi = New-Object System.Diagnostics.ProcessStartInfo\n")
	b.WriteString("  $psi.FileName = 'ao'\n")
	b.WriteString("  $psi.Arguments = 'hooks cline " + subcommand + "'\n")
	b.WriteString("  $psi.UseShellExecute = $false\n")
	b.WriteString("  $psi.CreateNoWindow = $true\n")
	b.WriteString("  $psi.RedirectStandardInput = $true\n")
	b.WriteString("  $p = [System.Diagnostics.Process]::Start($psi)\n")
	b.WriteString("  if ([Console]::IsInputRedirected) { [Console]::OpenStandardInput().CopyTo($p.StandardInput.BaseStream) }\n")
	b.WriteString("  $p.StandardInput.Close()\n")
	b.WriteString("  $p.WaitForExit()\n")
	b.WriteString("} catch {}\n")
	b.WriteString(`Write-Output '{"cancel": false}'` + "\n")
	return b.String()
}

func isManagedClineHook(scriptPath string) bool {
	data, err := os.ReadFile(scriptPath) //nolint:gosec // path built from caller-owned workspace dir
	if err != nil {
		return false
	}
	return strings.Contains(string(data), clineHookMarker)
}
