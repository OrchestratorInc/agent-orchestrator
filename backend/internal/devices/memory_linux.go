package devices

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// physicalMemory is the machine's RAM in bytes, or 0 when unknown.
func physicalMemory() int64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}
	return int64(info.Totalram) * int64(info.Unit) //nolint:gosec,unconvert // RAM sizes fit an int64; field types differ by architecture.
}

// readMemoryPressure reads the kernel's pressure stall information: the share
// of the last 10 s that tasks waited on memory.
func readMemoryPressure() MemoryPressure {
	raw, err := os.ReadFile("/proc/pressure/memory")
	if err != nil {
		return PressureNormal
	}
	avg10 := func(kind string) float64 {
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, kind+" ") {
				continue
			}
			for _, field := range strings.Fields(line) {
				if v, ok := strings.CutPrefix(field, "avg10="); ok {
					f, _ := strconv.ParseFloat(v, 64)
					return f
				}
			}
		}
		return 0
	}
	switch {
	case avg10("full") >= 10:
		return PressureCritical
	case avg10("some") >= 20:
		return PressureWarning
	}
	return PressureNormal
}
