package dockersandbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The container controls every host dir mounted read-write into it and can
// plant symlinks, FIFOs or files there at any time. Host code touches those
// dirs only through an os.Root opened from a parent the container cannot
// replace, so a planted link can never move a read or write outside them.

// ensureStateDirs makes the session's state dirs real directories under
// projectDir, component by component. projectDir itself sits under
// <dataDir>/sandbox/projects, which no container can touch.
func ensureStateDirs(projectDir string, session domain.SessionID) error {
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	sessionDir := filepath.Join("sessions", string(session))
	relay := filepath.Join(sessionDir, "relay")
	for _, dir := range []string{"home", "claude", "sessions", sessionDir, relay,
		filepath.Join(relay, "req"), filepath.Join(relay, "work"), filepath.Join(relay, "resp")} {
		if err := ensureRootDir(root, dir); err != nil {
			return err
		}
	}
	return nil
}

// ensureRootDir makes name a real directory inside root. Anything else there
// (a planted symlink, file or FIFO) is removed, never followed, and the
// directory is recreated 0700.
func ensureRootDir(root *os.Root, name string) error {
	fi, err := root.Lstat(name)
	switch {
	case err == nil && fi.IsDir():
		return nil
	case err == nil:
		if err := root.Remove(name); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// writeSessionEnv installs the session env file through a root opened from
// the project dir, so a symlinked session dir, env or temp name cannot
// redirect the write. The temp name is random and created exclusively.
func writeSessionEnv(target ports.SandboxTarget, data []byte) (err error) {
	rel, err := filepath.Rel(target.ProjectDir, target.SessionDir)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return fmt.Errorf("%w: session dir %s is not under %s", ports.ErrSandboxMisconfigured, target.SessionDir, target.ProjectDir)
	}
	project, err := os.OpenRoot(target.ProjectDir)
	if err != nil {
		return fmt.Errorf("write sandbox env: %w", err)
	}
	defer func() { _ = project.Close() }()
	root, err := project.OpenRoot(rel)
	if err != nil {
		return fmt.Errorf("write sandbox env: %w", err)
	}
	defer func() { _ = root.Close() }()
	tmp, err := randomName("env-")
	if err != nil {
		return fmt.Errorf("write sandbox env: %w", err)
	}
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("write sandbox env: %w", err)
	}
	defer func() {
		if err != nil {
			_ = root.Remove(tmp)
		}
	}()
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write sandbox env: %w", werr)
	}
	if fi, lerr := root.Lstat("env"); lerr == nil && !fi.Mode().IsRegular() {
		if fi.IsDir() {
			err = root.RemoveAll("env")
		} else {
			err = root.Remove("env")
		}
		if err != nil {
			return fmt.Errorf("install sandbox env: %w", err)
		}
	}
	if err = root.Rename(tmp, "env"); err != nil {
		return fmt.Errorf("install sandbox env: %w", err)
	}
	return nil
}

// sharedWorkRoot returns the project's managed worktree root as an ancestor
// of workspace, spelled as workspace spells it so in-container paths match.
// Containment is checked on physical paths, so a data dir reached through a
// symlink (/tmp -> /private/tmp) still matches.
func sharedWorkRoot(projectRoot, workspace string) (string, error) {
	rel, err := filepath.Rel(physicalPath(projectRoot), physicalPath(workspace))
	if err == nil && rel != "." && filepath.IsLocal(rel) {
		work := filepath.Clean(workspace)
		for range strings.Split(rel, string(filepath.Separator)) {
			work = filepath.Dir(work)
		}
		if physicalPath(work) == physicalPath(projectRoot) {
			return work, nil
		}
	}
	return "", fmt.Errorf("%w: shared sandbox needs an AO-managed worktree under %s, got %s",
		ports.ErrSandboxMisconfigured, projectRoot, workspace)
}

// physicalPath resolves symlinks in the longest existing prefix of path.
func physicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	rest := ""
	for dir := abs; ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		if parent := filepath.Dir(dir); parent == dir {
			return abs
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

func randomName(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}
