package agentlaunch

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var pinMu sync.Mutex

// pinDirectory uses the install directory only when it contains no sibling
// executables. Shared installs get a shim so pinning ao cannot promote node,
// git, or other tools ahead of the agent's selected runtime. Windows also
// prefers a shim for a single-executable install so the pinned directory has
// the user profile's ACLs rather than a per-user install's owner-only ones.
func pinDirectory(exe, dataDir string) (string, error) {
	dir := pinnedDirForExecutable(exe)
	if dir == "" {
		return "", nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return dir, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect Orchestrator install directory: %w", err)
	}
	shared := false
	for _, entry := range entries {
		if entry.Name() == filepath.Base(exe) || entry.IsDir() {
			continue
		}
		info, statErr := os.Stat(filepath.Join(dir, entry.Name()))
		if statErr != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if runtime.GOOS == "windows" {
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".exe", ".com", ".cmd", ".bat":
				shared = true
			}
		} else if info.Mode()&0o111 != 0 {
			shared = true
		}
	}
	// Windows prefers a shim even when the install directory holds no sibling
	// executables. A per-user install lives under %LOCALAPPDATA%\Programs, whose
	// ACLs grant only the installing user, so a harness that runs agent commands
	// as another local identity (Codex's Windows sandbox) cannot read or execute
	// that directory. A shim under the data directory inherits the profile's
	// ACLs, which those sandboxes can read (issue #7171).
	if !shared && runtime.GOOS != "windows" {
		return dir, nil
	}
	shimDir, err := ensureShimDir(exe, dataDir)
	if err != nil {
		if shared {
			// A shared install must not be pinned directly: its siblings would
			// land ahead of the agent's own runtime on PATH.
			return "", err
		}
		// A single-executable install directory is still a usable pin for any
		// caller that can read it, and losing the pin breaks `ao hooks`. This is
		// also the path that brings #7171 back, so it cannot fail silently.
		slog.Warn("could not create the ao shim; pinning the install directory instead, which sandboxed agents may not be able to read", "dir", dir, "error", err)
		return dir, nil
	}
	return shimDir, nil
}

// ensureShimDir creates (or refreshes) the directory holding the pinned ao shim
// and returns its path.
func ensureShimDir(exe, dataDir string) (string, error) {
	if !filepath.IsAbs(dataDir) {
		return "", fmt.Errorf("the Orchestrator shim data directory must be absolute, got %q", dataDir)
	}
	absolute, err := filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absolute))
	shimDir := filepath.Join(dataDir, "runtime", "ao-path", fmt.Sprintf("%x", sum[:16]))
	name := "ao"
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(absolute, "'", `'"'"'`) + "' \"$@\"\n"
	if runtime.GOOS == "windows" {
		name = "ao.cmd"
		// The batch sibling runs the copy in the shim directory. It needs the
		// absolute path, not %~dp0: when cmd resolves a bare quoted `"ao.cmd"`
		// from PATH it expands %0 against the working directory, which pointed at
		// a nonexistent <cwd>\ao.exe. %PATHEXT% prefers ao.exe, so only an
		// explicit `ao.cmd` reaches this file.
		script = "@echo off\r\n\"" + strings.ReplaceAll(filepath.Join(shimDir, "ao.exe"), "%", "%%") + "\" %*\r\n"
	}
	pinMu.Lock()
	defer pinMu.Unlock()
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(shimDir, name)
	if content, err := os.ReadFile(target); err == nil && string(content) == script {
		if runtime.GOOS == "windows" {
			if err := ensureWindowsAOExecutable(shimDir, absolute); err != nil {
				return "", err
			}
		}
		return shimDir, nil
	}
	temp, err := os.CreateTemp(shimDir, ".ao-*")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.WriteString(script); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tempPath, 0o700); err != nil { //nolint:gosec // executable AO shim
		return "", err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		if err := ensureWindowsAOExecutable(shimDir, absolute); err != nil {
			return "", err
		}
	}
	return shimDir, nil
}

// ensureWindowsAOExecutable installs the shim's ao.exe as a copy of the daemon
// executable. It copies rather than hard-links because a hard link shares its
// target's security descriptor: linking into the shim directory would keep the
// install directory's ACLs, which sandboxes that run agent commands as another
// local identity cannot read (issue #7171). A fresh copy inherits the shim
// directory's profile-derived ACLs instead.
func ensureWindowsAOExecutable(shimDir, executable string) error {
	target := filepath.Join(shimDir, "ao.exe")
	sourceInfo, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("stat Orchestrator executable: %w", err)
	}
	if targetInfo, targetErr := os.Stat(target); targetErr == nil && targetInfo.Size() == sourceInfo.Size() && targetInfo.ModTime().Equal(sourceInfo.ModTime()) && !os.SameFile(sourceInfo, targetInfo) {
		// Same size and mtime means same bytes. A shim left by an older AO build
		// can be a hard link, which reports exactly the source's metadata while
		// still carrying the install directory's ACLs, so that one is copied
		// through instead of trusted.
		removeStaleShimExecutables(shimDir)
		return nil
	}
	temp, err := os.CreateTemp(shimDir, ".ao-exe-*")
	if err != nil {
		return fmt.Errorf("create Orchestrator executable shim: %w", err)
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	if err := os.Remove(tempPath); err != nil {
		return err
	}
	if err := copyExecutable(executable, tempPath, sourceInfo.ModTime()); err != nil {
		return fmt.Errorf("copy Orchestrator executable: %w", err)
	}
	// A running shim (for example `ao mcp` reading stdin) keeps its image open,
	// so removing or replacing the file in place fails with access denied and
	// the pin silently drops back to the unreadable install directory. Renaming
	// the old file aside frees the name without disturbing the running image.
	aside := ""
	if _, statErr := os.Stat(target); statErr == nil {
		aside = filepath.Join(shimDir, fmt.Sprintf("ao.exe.old-%d", time.Now().UnixNano()))
		if err := os.Rename(target, aside); err != nil {
			return fmt.Errorf("replace Orchestrator executable shim: %w", err)
		}
	}
	if err := os.Rename(tempPath, target); err != nil {
		if aside != "" {
			_ = os.Rename(aside, target)
		}
		return fmt.Errorf("install Orchestrator executable shim: %w", err)
	}
	removeStaleShimExecutables(shimDir)
	return nil
}

// removeStaleShimExecutables drops copies that an earlier pin renamed aside
// while their process still held the image open. Removal keeps failing until
// that process exits, so every pass retries and ignores the failures.
func removeStaleShimExecutables(shimDir string) {
	matches, err := filepath.Glob(filepath.Join(shimDir, "ao.exe.old-*"))
	if err != nil {
		return
	}
	for _, match := range matches {
		_ = os.Remove(match)
	}
}

func copyExecutable(source, target string, modTime time.Time) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700) //nolint:gosec // executable AO shim
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(target, modTime, modTime)
}
