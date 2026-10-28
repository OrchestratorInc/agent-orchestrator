//go:build windows

package browserchrome

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

// DialRuntime connects to the daemon's Chrome runtime pipe.
func DialRuntime(ctx context.Context, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}
