package systeminstall

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// vendorRemovalPaths lists the program files a vendor's documented uninstall
// removes for its native install. With binaryPath empty it describes the
// documented layout; otherwise it returns nil unless binaryPath is that
// layout's binary, so AO never removes files for a copy placed elsewhere.
// Only program files are listed: settings and history stay, as with every
// other uninstall.
func vendorRemovalPaths(target Target, home, goos, binaryPath string) []string {
	switch target {
	case TargetClaudeCode:
		// https://code.claude.com/docs/en/installation#uninstall-claude-code
		share := filepath.Join(home, ".local", "share", "claude")
		launcher := filepath.Join(home, ".local", "bin", "claude")
		if goos == "windows" {
			launcher += ".exe"
		}
		paths := []string{launcher, share}
		if binaryPath == "" {
			return paths
		}
		if goos == "windows" {
			if samePath(binaryPath, launcher) {
				return paths
			}
			return nil
		}
		// The launcher is a symlink into versions/; the binary may also be a
		// version file itself.
		resolved, err := filepath.EvalSymlinks(binaryPath)
		if err != nil {
			return nil
		}
		if owned, err := pathWithin(filepath.Join(share, "versions"), resolved); err != nil || !owned {
			return nil
		}
		return paths
	default:
		return nil
	}
}

// confirmVendorRemoval binds a documented removal to the binary sessions run,
// refusing it when that binary is not the vendor's native layout.
func (s *Service) confirmVendorRemoval(plan Plan, target Target, baseline *installedBaseline) Plan {
	home, err := os.UserHomeDir()
	if err == nil && baseline != nil && baseline.path != "" {
		if paths := vendorRemovalPaths(target, home, s.goos, baseline.path); paths != nil && allWithinHome(home, paths) {
			plan.Remove = paths
			return plan
		}
	}
	plan.Remove = nil
	plan.Unsupported = true
	plan.Reason = "AO could not confirm the installed copy is the vendor's native install, so it removes nothing. Follow the vendor's uninstall guide."
	return plan
}

// removeProgramFiles deletes each path, a file, symlink or directory, without
// following symlinks out of it. Missing paths are already removed.
func removeProgramFiles(paths []string, out io.Writer) error {
	for _, path := range paths {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		_, _ = fmt.Fprintf(out, "Removed %s\n", path)
	}
	return nil
}

// allWithinHome reports whether every path is strictly inside home, a last
// guard that a removal never reaches outside the user's own files.
func allWithinHome(home string, paths []string) bool {
	for _, path := range paths {
		relative, err := filepath.Rel(home, path)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}
