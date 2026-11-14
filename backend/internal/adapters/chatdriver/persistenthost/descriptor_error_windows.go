//go:build windows

package persistenthost

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isDescriptorSharingViolation(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
