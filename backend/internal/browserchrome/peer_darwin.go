//go:build darwin

package browserchrome

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"

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
	var pid, uid int
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			sockErr = err
			return
		}
		uid = int(cred.Uid)
		pid, sockErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	})
	if err != nil {
		return 0, 0, err
	}
	return pid, uid, sockErr
}

// processExecutable reads the exec path the kernel recorded for pid
// (KERN_PROCARGS2: argc, then the NUL-terminated executable path).
func processExecutable(pid int) (string, error) {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", err
	}
	if len(data) < 4 {
		return "", errors.New("short process arguments")
	}
	_ = binary.LittleEndian.Uint32(data[:4])
	rest := data[4:]
	end := bytes.IndexByte(rest, 0)
	if end <= 0 {
		return "", errors.New("missing executable path")
	}
	return string(rest[:end]), nil
}
