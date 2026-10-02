package runner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const runtimeFileName = "runtime.json"

// RuntimeRecord is the non-secret rendezvous record used by a replacement AO
// daemon to find and authenticate an existing runner.
type RuntimeRecord struct {
	PID             int       `json:"pid"`
	Port            int       `json:"port"`
	InstanceID      string    `json:"instanceId"`
	RunnerVersion   string    `json:"runnerVersion"`
	UpstreamVersion string    `json:"upstreamVersion"`
	StartedAt       time.Time `json:"startedAt"`
}

// NewInstanceID distinguishes runner replacements independently of reusable process IDs.
func NewInstanceID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate instance ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// WriteRuntimeRecord atomically replaces runtime.json without following an
// existing symlink or broadening the state directory's permissions.
func WriteRuntimeRecord(root string, record RuntimeRecord) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("state directory must be absolute")
	}
	root = filepath.Clean(root)
	if err := requirePrivateDirectory(root); err != nil {
		return fmt.Errorf("state directory: %w", err)
	}
	target := filepath.Join(root, runtimeFileName)
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("runtime record must be a regular file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("runtime record permissions must be private")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect runtime record: %w", err)
	}

	b, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode runtime record: %w", err)
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(root, ".runtime-*.json")
	if err != nil {
		return fmt.Errorf("create runtime record: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write runtime record: %w", err)
	}
	if err = os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("replace runtime record: %w", err)
	}
	return nil
}
