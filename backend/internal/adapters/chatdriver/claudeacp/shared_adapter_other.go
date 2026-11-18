//go:build !unix

package claudeacp

import (
	"context"
	"errors"
)

// ensureSharedAdapter is Unix-only; elsewhere each chat starts its own adapter.
func ensureSharedAdapter(context.Context, string, runtimeLaunch, map[string]string) error {
	return errors.New("shared Claude adapter needs unix sockets")
}
