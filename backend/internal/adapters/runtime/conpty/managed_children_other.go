//go:build !darwin && !linux && !windows

package conpty

import (
	"context"
	"errors"
)

func snapshotManagedChildren(context.Context, int) (managedChildren, error) {
	return managedChildren{}, errors.New("conpty: process inspection is not supported on this platform")
}
