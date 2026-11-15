//go:build windows

package browserchrome

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func DialRuntime(ctx context.Context, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}
