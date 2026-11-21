//go:build !darwin && !linux

package process

import "context"

// ListenersWithEnv lists nothing on platforms without a listener table AO reads.
func ListenersWithEnv(context.Context, string, string, string) ([]Listener, error) {
	return nil, nil
}
