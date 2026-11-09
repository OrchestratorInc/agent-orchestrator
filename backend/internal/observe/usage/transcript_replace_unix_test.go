//go:build !windows

package usage

import "os"

// openReplaceableTranscript 在 Unix 保持普通只读句柄的并发 rename 语义。
func openReplaceableTranscript(path string) (*os.File, error) {
	return os.Open(path)
}

// replaceTranscript 使用 Unix 的原子 rename，旧描述符继续指向原文件。
func replaceTranscript(source, destination string) error {
	return os.Rename(source, destination)
}
