//go:build !windows && !darwin && !linux

package conpty

import "errors"

func newConPTY(cwd, shellCmd string, shellArgs []string, cols, rows uint16) (ptyConn, error) {
	return nil, errors.New("conpty: unsupported on this OS")
}
