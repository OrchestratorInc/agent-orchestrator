//go:build !windows

package browserchrome

import (
	"context"
	"net"
)

// DialRuntime connects to the daemon's Chrome runtime socket.
func DialRuntime(ctx context.Context, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", address)
}
