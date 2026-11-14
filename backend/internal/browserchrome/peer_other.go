//go:build !darwin && !linux

package browserchrome

import "net"

func peerIdentity(net.Conn) (int, int, error) { return 0, 0, errPeerUnsupported }

func processExecutable(int) (string, error) { return "", errPeerUnsupported }
