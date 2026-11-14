//go:build !windows

package browserchrome

import (
	"context"
	"net"
)

func DialRuntime(ctx context.Context, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", address)
}
