package browserchrome

import (
	"errors"
	"fmt"
	"net"
	"os"
)

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
