//go:build linux

package browserchrome

import (
	"net"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func peerIdentity(conn net.Conn) (int, int, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, 0, errPeerUnsupported
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var cred *unix.Ucred
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return 0, 0, err
	}
	if sockErr != nil {
		return 0, 0, sockErr
	}
	return int(cred.Pid), int(cred.Uid), nil
}

func processExecutable(pid int) (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
}
