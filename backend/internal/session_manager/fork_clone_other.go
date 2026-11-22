//go:build !darwin

package sessionmanager

func cloneTree(string, string) error { return errCloneUnsupported }
