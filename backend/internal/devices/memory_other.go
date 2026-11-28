//go:build !darwin && !linux

package devices

// physicalMemory is the machine's RAM in bytes, or 0 when unknown.
func physicalMemory() int64 { return 0 }

// readMemoryPressure has no signal to read here.
func readMemoryPressure() MemoryPressure { return PressureNormal }
