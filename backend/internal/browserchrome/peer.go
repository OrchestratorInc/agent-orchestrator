package browserchrome

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// PeerVerifier returns a check for connections to the Chrome runtime socket:
// the peer must run as the daemon's user and be the same ao executable the
// host wrapper runs. The socket itself is already owner-only; this pins the
// peer to AO's own binary on top of the endpoint token.
func PeerVerifier(executable string) func(net.Conn) error {
	return func(conn net.Conn) error {
		pid, uid, err := peerIdentity(conn)
		if errors.Is(err, errPeerUnsupported) {
			return nil
		}
		if err != nil {
			return err
		}
		if uid != os.Getuid() {
			return fmt.Errorf("peer runs as uid %d", uid)
		}
		path, err := processExecutable(pid)
		if err != nil {
			return fmt.Errorf("resolve peer executable: %w", err)
		}
		if !sameFile(path, executable) {
			return fmt.Errorf("peer executable %s is not AO", path)
		}
		return nil
	}
}

var errPeerUnsupported = errors.New("peer identity is unsupported on this platform")
