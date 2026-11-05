//go:build !windows

package mobilebridge

import "os"

// readConfigFile 使用标准库读取完整配置，保持非 Windows 平台的读取行为。
func readConfigFile(path string) ([]byte, error) { return os.ReadFile(path) }

// openConfigFile 在允许替换已打开文件的平台上使用标准只读句柄。
func openConfigFile(path string) (*os.File, error) { return os.Open(path) }

// replaceAtomicFile 使用平台的原子 rename 发布完整文件。
func replaceAtomicFile(source, destination string) error { return os.Rename(source, destination) }
