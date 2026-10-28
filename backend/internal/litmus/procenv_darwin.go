package litmus

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"

	"golang.org/x/sys/unix"
)

// macOS withholds Apple platform binaries' environments, returning an empty env without error, so markers cannot find them.
func processEnviron(pid int) ([]string, error) {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	if len(data) < 4 {
		return nil, io.ErrUnexpectedEOF
	}
	argc := binary.NativeEndian.Uint32(data[:4])
	data = data[4:]
	// Skip the executable path and its NUL padding, then exactly argc arguments.
	end := bytes.IndexByte(data, 0)
	if end < 0 {
		return nil, io.ErrUnexpectedEOF
	}
	data = bytes.TrimLeft(data[end:], "\x00")
	for range argc {
		end = bytes.IndexByte(data, 0)
		if end < 0 {
			return nil, io.ErrUnexpectedEOF
		}
		data = data[end+1:]
	}
	env := strings.Split(string(data), "\x00")
	for i, entry := range env {
		if entry == "" {
			return env[:i], nil
		}
	}
	return nil, io.ErrUnexpectedEOF
}
