//go:build !darwin && !linux

package gitworktree

import "errors"

// cloneFile is unavailable here; callers fall back to a byte copy.
func cloneFile(string, string) error {
	return errors.New("file clone not supported on this platform")
}
