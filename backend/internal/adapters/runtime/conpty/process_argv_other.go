//go:build !darwin && !linux

package conpty

import (
	"context"
	"errors"
)

// processArgv is unavailable here, so a host whose supervision was never
// recorded cannot be identified and stays inconclusive.
func processArgv(context.Context, int) ([]string, error) {
	return nil, errors.New("conpty: reading process argv is not supported on this platform")
}
