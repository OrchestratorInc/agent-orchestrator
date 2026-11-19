//go:build !unix

package claudeacp

import (
	"context"
	"errors"
)

// The shared adapter needs unix sockets; elsewhere each chat starts its own adapter.
const sharedAdapterAvailable = false

// ensureSharedAdapter is not reached when sharedAdapterAvailable is false.
func ensureSharedAdapter(context.Context, string, runtimeLaunch, map[string]string) error {
	return errors.New("shared Claude adapter needs unix sockets")
}
