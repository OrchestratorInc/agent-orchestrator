//go:build !linux && !darwin

package providerkeys

import (
	"context"
	"errors"
)

// DeviceSupported reports that Publish can reach programs started outside AO.
// Windows would need a persistent HKCU\Environment write outside AO's data
// directory, which AO does not make.
const DeviceSupported = false

func Publish(context.Context, string, string, string) error {
	return errors.New("device user environment is unsupported on this platform")
}

// Unpublish has nothing to remove where Publish is unsupported.
func Unpublish(context.Context, string, string) error { return nil }
