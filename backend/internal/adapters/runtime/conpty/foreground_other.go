//go:build !darwin && !linux

package conpty

// processArgv is unavailable where no PTY reports a foreground group.
func processArgv(int) []string { return nil }
