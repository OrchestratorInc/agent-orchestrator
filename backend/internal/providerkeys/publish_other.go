//go:build !windows && !linux && !darwin

package providerkeys

import (
	"context"
	"errors"
)

func Publish(context.Context, string, string) error {
	return errors.New("device user environment is unsupported on this platform")
}
