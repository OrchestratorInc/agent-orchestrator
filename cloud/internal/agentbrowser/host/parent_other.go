//go:build !linux

package host

import "syscall"

// DieWithParent is Linux-only (Pdeathsig); sandboxes are Linux.
func DieWithParent(*syscall.SysProcAttr, syscall.Signal) {}
