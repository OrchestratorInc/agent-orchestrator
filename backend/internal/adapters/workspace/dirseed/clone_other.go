//go:build !darwin

package dirseed

// CloneTree is unsupported off macOS; callers copy instead.
func CloneTree(string, string) error { return ErrCloneUnsupported }
