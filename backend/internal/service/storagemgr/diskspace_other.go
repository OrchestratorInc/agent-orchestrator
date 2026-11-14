//go:build !darwin && !linux && !windows

package storagemgr

import "errors"

func diskSpace(string) (total, free uint64, err error) {
	return 0, 0, errors.New("disk space is not available on this platform")
}
