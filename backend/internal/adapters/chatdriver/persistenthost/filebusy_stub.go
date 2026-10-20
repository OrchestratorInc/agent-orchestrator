//go:build !windows

package persistenthost

func isTransientHostFileBusy(error) bool { return false }
