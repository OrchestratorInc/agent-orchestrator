//go:build !windows

package pricing

import (
	"crypto/rand"
	"fmt"
	"os"
)

// ensurePrivateDir 用 POSIX 权限创建并收紧缓存目录。
func ensurePrivateDir(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create private pricing directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil { //nolint:gosec // 私有目录需要所有者遍历权限。
		return fmt.Errorf("protect pricing directory: %w", err)
	}
	return nil
}

// protectCacheFile 修复内容匹配的旧 blob 权限，不重复写入价格数据。
func protectCacheFile(path string) error { return os.Chmod(path, 0o600) }

// createPrivateCacheTemp 通过固定父目录独占创建随机私有文件。
func createPrivateCacheTemp(root *os.Root) (*os.File, error) {
	name := ".pricing-" + rand.Text()
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = root.Remove(name)
		return nil, err
	}
	return file, nil
}
