//go:build !windows

package reviewgateway

import (
	"errors"
	"fmt"
	"os"
)

// ensurePrivateDir 保留 Unix 的真实目录与 0700 权限检查。
func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("review gateway: create private directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("review gateway: private path is not a real directory")
	}
	if err := os.Chmod(path, 0o700); err != nil { //nolint:gosec // directories require execute permission and remain owner-only
		return fmt.Errorf("review gateway: secure private directory: %w", err)
	}
	return nil
}

// openPrivateManifest 保留 Unix 的独占创建和只读复用语义。
func openPrivateManifest(path string, create bool) (*os.File, error) {
	if create {
		return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	return os.Open(path)
}
