//go:build !darwin && !linux

package browserchrome

import "net"

// Windows protects the runtime pipe with an owner-only DACL; the endpoint
// token remains the second factor.
func peerIdentity(net.Conn) (int, int, error) { return 0, 0, errPeerUnsupported }

func processExecutable(int) (string, error) { return "", errPeerUnsupported }
