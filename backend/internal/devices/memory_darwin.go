package devices

import "golang.org/x/sys/unix"

// physicalMemory is the machine's RAM in bytes, or 0 when unknown.
func physicalMemory() int64 {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(total) //nolint:gosec // RAM sizes fit an int64.
}

// readMemoryPressure is macOS's own memory pressure level.
func readMemoryPressure() MemoryPressure {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return PressureNormal
	}
	switch {
	case level >= 4:
		return PressureCritical
	case level >= 2:
		return PressureWarning
	}
	return PressureNormal
}
