//go:build !darwin && !linux

package skillbridge

import "errors"

// defaultTotalBytes bounds one session's copies, which are full copies here.
const defaultTotalBytes = 64 << 20

func cloneFile(string, string) error { return errors.ErrUnsupported }
