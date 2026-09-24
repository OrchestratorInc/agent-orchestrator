//go:build darwin

package procmem

import (
	"context"
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

// ReadSystem reads host memory, swap and load on macOS. RAM size, swap and the
// load average are plain sysctls; the page counts behind "available" come from
// vm_stat, the one thing macOS exposes as text rather than as a number.
//
// CPU is missing here on purpose. macOS reports per-processor ticks only
// through Mach, which needs cgo, so the reader leaves CPUTotalTicks zero and
// the usage service derives the machine's busy share from the process table it
// already samples. See MemoryReader.SystemMemory.
func ReadSystem() (System, error) {
	sys := System{CPUCount: runtime.NumCPU()}
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return System{}, fmt.Errorf("procmem: read hw.memsize: %w", err)
	}
	sys.TotalBytes = total

	out, err := execRunner(context.Background(), "vm_stat")
	if err != nil {
		return System{}, fmt.Errorf("procmem: vm_stat: %w", err)
	}
	stat, err := ParseVMStat(string(out))
	if err != nil {
		return System{}, err
	}
	sys.AvailableBytes = min(stat.AvailableBytes(), total)
	sys.SwapPages = stat.PageIns + stat.PageOuts

	// Swap and load are refinements: a Mac that hides them still gets its
	// memory reading rather than an error.
	if raw, err := unix.SysctlRaw("vm.swapusage"); err == nil {
		sys.SwapTotalBytes, sys.SwapUsedBytes = parseSwapUsage(raw)
	}
	if raw, err := unix.SysctlRaw("vm.loadavg"); err == nil {
		sys.Load1 = parseLoadavg(raw)
	}
	// No PSI on macOS, so pressure is how little is available.
	sys.PressureRaw, sys.PressureSource = availablePressure(sys), PressureSourceAvailablePct
	return sys, nil
}
