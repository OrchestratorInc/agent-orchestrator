//go:build windows

package gitworktree

func repairRemovePermissions(string) bool { return false }

func isPermissionRemoveError(error) bool { return false }
