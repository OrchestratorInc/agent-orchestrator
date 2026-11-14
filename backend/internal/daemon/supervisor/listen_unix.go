//go:build !windows

package supervisor

import (
	"net"
	"os"
	"path/filepath"
)

func Listen(runFilePath string) (net.Listener, string, error) {
	sockPath := filepath.Join(filepath.Dir(runFilePath), "supervise.sock")
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, "", err
	}
	return ln, sockPath, nil
}
