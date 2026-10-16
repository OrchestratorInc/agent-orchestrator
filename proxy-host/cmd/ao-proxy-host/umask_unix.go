//go:build !windows

package main

import "syscall"

// The SDK saves a new Claude or Codex sign-in with os.Create; what the helper writes is the user's alone.
func init() { syscall.Umask(0o077) }
