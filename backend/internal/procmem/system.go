package procmem

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// System is the host's total and available RAM in bytes.
type System struct {
	TotalBytes     uint64
	AvailableBytes uint64
}

// ReadSystem reads total and available host memory. It is Linux-only for
// now (parses /proc/meminfo); other platforms return ErrUnsupported.
func ReadSystem() (System, error) {
	if runtime.GOOS != "linux" {
		return System{}, ErrUnsupported
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return System{}, fmt.Errorf("procmem: open /proc/meminfo: %w", err)
	}
	defer f.Close()
	var sys System
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, kib, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(kib)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			sys.TotalBytes = n * 1024
		case "MemAvailable":
			sys.AvailableBytes = n * 1024
		}
	}
	if err := sc.Err(); err != nil {
		return System{}, fmt.Errorf("procmem: read /proc/meminfo: %w", err)
	}
	if sys.TotalBytes == 0 {
		return System{}, fmt.Errorf("procmem: /proc/meminfo missing MemTotal")
	}
	return sys, nil
}
