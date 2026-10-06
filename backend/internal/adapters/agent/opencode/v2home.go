package opencode

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const v2DataHomeDirName = "opencode-v2-home"

// V2DataHome returns the XDG_DATA_HOME OpenCode 2 runs with: a sibling of the
// user's own data home, so OpenCode 1 and 2 never share a database.
func V2DataHome() (string, error) {
	parent := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if parent != "" && !filepath.IsAbs(parent) {
		return "", fmt.Errorf("opencode: XDG_DATA_HOME must be absolute, got %q", parent)
	}
	if filepath.Base(parent) == v2DataHomeDirName {
		return filepath.Clean(parent), nil
	}
	if parent == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("opencode: resolve absolute user data home: %w", err)
		}
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("opencode: user data home must be absolute, got %q", home)
		}
		parent = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(parent, v2DataHomeDirName), nil
}

// V2NPMPrefix is the private npm prefix OpenCode 2 installs into so its
// `opencode` executable never replaces the OpenCode 1 one on PATH.
func V2NPMPrefix() (string, error) {
	home, err := V2DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "npm"), nil
}

// V2NPMBinDir is where npm places executables for V2NPMPrefix.
func V2NPMBinDir() (string, error) {
	prefix, err := V2NPMPrefix()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return prefix, nil
	}
	return filepath.Join(prefix, "bin"), nil
}

var v2DataMigrationMu sync.Mutex

// PrepareV2DataHome returns the isolated OpenCode 2 data home after copying a
// pre-isolation OpenCode store into it once. The legacy store is left intact
// for OpenCode 1; an existing isolated store is never overwritten.
func PrepareV2DataHome() (string, error) {
	v2DataMigrationMu.Lock()
	defer v2DataMigrationMu.Unlock()

	destinationHome, err := V2DataHome()
	if err != nil {
		return "", err
	}
	legacyHome := filepath.Dir(destinationHome)
	if filepath.Base(legacyHome) == v2DataHomeDirName {
		return destinationHome, nil
	}
	source := filepath.Join(legacyHome, "opencode")
	destination := filepath.Join(destinationHome, "opencode")
	if _, err := os.Stat(destination); err == nil {
		return destinationHome, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("opencode: inspect isolated data store: %w", err)
	}
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return destinationHome, nil
	} else if err != nil {
		return "", fmt.Errorf("opencode: inspect legacy data store: %w", err)
	}
	if err := os.MkdirAll(destinationHome, 0o700); err != nil {
		return "", fmt.Errorf("opencode: create isolated data home: %w", err)
	}
	staging, err := os.MkdirTemp(destinationHome, ".opencode-migration-")
	if err != nil {
		return "", fmt.Errorf("opencode: stage legacy data migration: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := copyTree(source, staging); err != nil {
		return "", fmt.Errorf("opencode: copy legacy data store: %w", err)
	}
	if err := os.Rename(staging, destination); err != nil {
		if _, statErr := os.Stat(destination); statErr == nil {
			return destinationHome, nil
		}
		return "", fmt.Errorf("opencode: activate migrated data store: %w", err)
	}
	return destinationHome, nil
}

func copyTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			if relative == "." {
				return nil
			}
			return os.Mkdir(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported legacy data entry %q", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}
