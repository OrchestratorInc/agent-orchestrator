//go:build !darwin

package webhook

func deskActive() bool { return false }
