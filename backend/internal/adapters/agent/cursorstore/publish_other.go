//go:build !linux && !darwin && !windows

package cursorstore

import "errors"

func publishDirectory(_, _ string) error {
	return errors.New("atomic cursor directory publication is unsupported on this platform")
}
