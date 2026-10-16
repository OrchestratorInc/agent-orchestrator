//go:build !windows && !darwin && !linux

package gitworktree

import "errors"

func moveTaskDirectory(source, target string) error {
	return errors.New("gitworktree: atomic task naming is unsupported on this platform")
}
