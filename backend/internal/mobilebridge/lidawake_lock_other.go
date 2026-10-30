//go:build !darwin && !linux

package mobilebridge

import (
	"context"
	"errors"
)

func lockWakeSetup(context.Context, string) (func(), error) {
	return nil, errors.New("wake helper setup is unsupported on this platform")
}
