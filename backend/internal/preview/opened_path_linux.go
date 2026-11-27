//go:build linux

package preview

import (
	"os"
	"strconv"
	"strings"
)

// openedPath is the absolute path the open file f is at now, read from the
// handle itself (/proc/self/fd), so it names the file whose bytes f reads.
func openedPath(f *os.File) (string, bool) {
	conn, err := f.SyscallConn()
	if err != nil {
		return "", false
	}
	var target string
	var readErr error
	if err := conn.Control(func(fd uintptr) {
		target, readErr = os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(fd), 10))
	}); err != nil || readErr != nil {
		return "", false
	}
	// An unlinked file has no path to name.
	if strings.HasSuffix(target, " (deleted)") {
		return "", false
	}
	return target, true
}
