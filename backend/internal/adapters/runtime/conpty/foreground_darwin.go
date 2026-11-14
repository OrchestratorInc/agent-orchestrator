//go:build darwin

package conpty

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

func processArgv(pid int) []string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	argv := make([]string, 0, min(argc, maxForegroundArgs))
	for len(argv) < argc && len(argv) < maxForegroundArgs && len(rest) > 0 {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			argv = append(argv, string(rest))
			break
		}
		argv = append(argv, string(rest[:end]))
		rest = rest[end+1:]
	}
	return argv
}

func (c *darwinPTYConn) ForegroundPGID() (int, bool) { return ttyForegroundPGID(c.pty) }
