//go:build linux

package conpty

import (
	"os"
	"strconv"
	"strings"
)

// processArgv reads pid's NUL-separated argv from procfs.
func processArgv(pid int) []string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(raw) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
}

// ForegroundPGID reports the terminal's foreground process group.
func (c *linuxPTYConn) ForegroundPGID() (int, bool) { return ttyForegroundPGID(c.pty) }
