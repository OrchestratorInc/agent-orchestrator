//go:build darwin

package preview

import (
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openedPath is the absolute path the open file f is at now, read from the
// handle itself (fcntl F_GETPATH), so it names the file whose bytes f reads.
func openedPath(f *os.File) (string, bool) {
	conn, err := f.SyscallConn()
	if err != nil {
		return "", false
	}
	buf := make([]byte, unix.PathMax)
	var getErr error
	if err := conn.Control(func(fd uintptr) {
		// F_GETPATH writes at most PathMax bytes into buf, kept alive below.
		_, getErr = unix.FcntlInt(fd, unix.F_GETPATH, int(uintptr(unsafe.Pointer(&buf[0])))) //nolint:gosec // fcntl takes the buffer's address; buf is PathMax bytes and outlives the call
	}); err != nil || getErr != nil {
		return "", false
	}
	runtime.KeepAlive(buf)
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	if n == 0 {
		return "", false
	}
	return string(buf[:n]), true
}
