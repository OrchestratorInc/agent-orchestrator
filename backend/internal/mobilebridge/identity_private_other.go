//go:build !windows

package mobilebridge

import (
	"context"
	"os"
)

// identityNeedsPrivacyRepair 保持现有 POSIX 读取行为，写入仍使用 0600。
func identityNeedsPrivacyRepair(ctx context.Context, _ string) (bool, error) {
	return false, ctx.Err()
}

// writePrivateIdentityFile 保留 POSIX 文件创建时的 0600 权限与原写入错误。
func writePrivateIdentityFile(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
