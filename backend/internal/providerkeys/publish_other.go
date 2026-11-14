//go:build !linux && !darwin

package providerkeys

import (
	"context"
	"errors"
)

const DeviceSupported = false

func Publish(context.Context, string, string, string) error {
	return errors.New("device user environment is unsupported on this platform")
}

func Unpublish(context.Context, string, string) error { return nil }
