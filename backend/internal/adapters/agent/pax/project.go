package pax

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Pax reads a repository's `.clai/settings.json` from the launch directory up
// to the folder holding `.git`, so a worktree root's file is the one it uses.
const (
	projectDir      = ".clai"
	projectSettings = "settings.json"
)

// projectFile is the part of Pax's project settings AO writes. Pax applies a
// `permissions` value no wider than the user's own at once, and sends
// `instructions` it has not approved to the model as the repository's text.
// Pax reports unknown keys at startup, so the file carries no AO marker; the
// AO-written `.clai/.gitignore` that hides it marks it as AO's.
type projectFile struct {
	Instructions string `json:"instructions,omitempty"`
	Permissions  string `json:"permissions,omitempty"`
}

// paxPermissions maps an AO permission mode onto the Pax mode a project file
// may set, never a wider one than requested. Default and bypass leave the
// user's own mode: a project file cannot widen it. Pax has no edits-only
// mode, and its `auto` runs commands without asking, so accept-edits asks
// before every write and command.
func paxPermissions(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAuto:
		return "auto"
	case ports.PermissionModeAcceptEdits:
		return "read-only"
	default:
		return ""
	}
}

// writeProjectSettings writes AO's instructions and permission mode for the
// session into the worktree, or removes AO's earlier file when there is
// nothing to write. A file the repository owns is never changed: launching
// would then run without AO's instructions, so it fails instead.
func writeProjectSettings(workspace, instructions string, mode ports.PermissionMode) error {
	content := projectFile{Instructions: strings.TrimSpace(instructions), Permissions: paxPermissions(mode)}
	empty := content == projectFile{}
	if strings.TrimSpace(workspace) == "" {
		if empty {
			return nil
		}
		return errors.New("pax: instructions and permission modes need the session workspace")
	}
	dir := filepath.Join(workspace, projectDir)
	path := filepath.Join(dir, projectSettings)
	owned, err := aoOwnsProjectSettings(dir)
	if err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("pax: stat %s: %w", path, statErr)
	}
	if exists && !owned {
		if empty {
			return nil
		}
		return fmt.Errorf("pax: this repository has its own %s/%s, so AO cannot give Pax its session instructions there", projectDir, projectSettings)
	}
	if empty {
		if exists {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("pax: remove %s: %w", path, err)
			}
		}
		return nil
	}
	if err := hookutil.EnsureWorkspaceGitignore(dir, projectSettings); err != nil {
		return fmt.Errorf("pax: %w", err)
	}
	if owned, err = aoOwnsProjectSettings(dir); err != nil {
		return err
	}
	if !owned {
		// The repository's own .clai/.gitignore is left alone, and an
		// unmarked settings file would read as the repository's next time.
		return fmt.Errorf("pax: this repository has its own %s/.gitignore, so AO cannot keep a session %s/%s there", projectDir, projectDir, projectSettings)
	}
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("pax: write %s: %w", path, err)
	}
	return nil
}

// aoOwnsProjectSettings reports whether AO's self-ignoring .gitignore in dir
// covers settings.json.
func aoOwnsProjectSettings(dir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore")) //nolint:gosec // caller-owned workspace path
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("pax: read %s/.gitignore: %w", projectDir, err)
	}
	text := string(data)
	if !strings.Contains(text, hookutil.GitignoreSentinel) {
		return false, nil
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "/"+projectSettings {
			return true, nil
		}
	}
	return false, nil
}

// standingInstructions resolves AO's standing instructions: inline wins, else
// the file is read. Empty means none.
func standingInstructions(inline, file string) (string, error) {
	if strings.TrimSpace(inline) != "" {
		return inline, nil
	}
	if strings.TrimSpace(file) == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // AO-owned launch config path
	if err != nil {
		return "", fmt.Errorf("pax: read system prompt file: %w", err)
	}
	return string(data), nil
}
