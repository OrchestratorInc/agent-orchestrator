//go:build !darwin && !linux

package conpty

func processArgv(int) []string { return nil }
