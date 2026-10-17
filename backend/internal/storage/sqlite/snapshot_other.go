//go:build !darwin && !linux && !windows

package sqlite

import "errors"

// No clone and no free-space probe on this platform, so no snapshot is made:
// takeSnapshot refuses a full copy it cannot size, and Open logs that and
// migrates without one.

func cloneFile(string, string) error {
	return errors.ErrUnsupported
}

func freeBytes(string) (uint64, error) {
	return 0, errors.ErrUnsupported
}
