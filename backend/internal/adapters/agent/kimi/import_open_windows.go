//go:build windows

package kimi

import "os"

func openKimiNativeFile(root *os.Root, name string, _ bool) (*os.File, error) { return root.Open(name) }
