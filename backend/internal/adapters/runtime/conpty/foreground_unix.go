//go:build darwin || linux

package conpty

import (
	"os"

	"golang.org/x/sys/unix"
)

// ttyForegroundPGID reads the foreground process group of the terminal whose
// master side is f: tcgetpgrp on the master, as tmux does to name a pane. It
// goes through SyscallConn rather than Fd, because Fd switches the file to
// blocking mode and the host's read loop relies on the runtime poller.
func ttyForegroundPGID(f *os.File) (int, bool) {
	rc, err := f.SyscallConn()
	if err != nil {
		return 0, false
	}
	pgid := 0
	var ioctlErr error
	if err := rc.Control(func(fd uintptr) {
		pgid, ioctlErr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	}); err != nil || ioctlErr != nil || pgid <= 0 {
		return 0, false
	}
	return pgid, true
}
