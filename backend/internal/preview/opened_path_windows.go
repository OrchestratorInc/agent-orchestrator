//go:build windows

package preview

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// openedPath is the absolute path the open file f is at now, read from the
// handle itself (GetFinalPathNameByHandle), so it names the file whose bytes
// f reads.
func openedPath(f *os.File) (string, bool) {
	handle := windows.Handle(f.Fd())
	size := uint32(256)
	for {
		buf := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", false
		}
		if n < uint32(len(buf)) {
			name := windows.UTF16ToString(buf[:n])
			if strings.HasPrefix(name, `\\?\UNC\`) {
				name = `\\` + strings.TrimPrefix(name, `\\?\UNC\`)
			} else {
				name = strings.TrimPrefix(name, `\\?\`)
			}
			return filepath.Clean(name), true
		}
		size = n + 1
	}
}
