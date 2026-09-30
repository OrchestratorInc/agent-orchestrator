//go:build darwin

package procmem

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"
)

// darwinCPUSamplePeriod is the fixed weight ReadSystem adds to the running
// total on every call. Mach's real per-processor ticks are reachable only
// through cgo, which this package avoids, so `top -l 1 -n 0` stands in with a
// single instantaneous percentage instead. Accumulating it here — rather than
// adding a separate instantaneous-percent field — lets MemoryReader.SystemMemory
// keep computing CPU the one way it already does on every platform: the
// busy/total delta between two reads. Each read adds this fixed amount to the
// total and percent*(period/100) to busy, so a delta spanning exactly one read
// comes out to exactly the percentage that read reported.
const darwinCPUSamplePeriod = 1000

var (
	darwinCPUMu    sync.Mutex
	darwinCPUTotal uint64
	darwinCPUBusy  uint64
)

// ReadSystem reads host memory, swap, load and CPU on macOS. RAM size, swap
// and the load average are plain sysctls; the page counts behind "available"
// come from vm_stat, and CPU from top — both the same shape: macOS exposes
// them as text, not as a number a sysctl or an ordinary syscall can hand back.
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
	sys.SwapPages = stat.SwapIns + stat.SwapOuts
	sys.SwapPageBytes = stat.PageSize

	// Swap and load are refinements: a Mac that hides them still gets its
	// memory reading rather than an error.
	if raw, err := unix.SysctlRaw("vm.swapusage"); err == nil {
		sys.SwapTotalBytes, sys.SwapUsedBytes = parseSwapUsage(raw)
	}
	if raw, err := unix.SysctlRaw("vm.loadavg"); err == nil {
		sys.Load1 = parseLoadavg(raw)
	}

	// CPU is a refinement too: a Mac where `top` fails (sandboxed, missing,
	// unexpected output) still gets its memory reading rather than an error,
	// same as swap and load above.
	if out, err := execRunner(context.Background(), "top", "-l", "1", "-n", "0"); err == nil {
		if pct, ok := ParseTopCPU(string(out)); ok {
			darwinCPUMu.Lock()
			darwinCPUTotal += darwinCPUSamplePeriod
			darwinCPUBusy += uint64(pct / 100 * darwinCPUSamplePeriod)
			sys.CPUTotalTicks, sys.CPUBusyTicks = darwinCPUTotal, darwinCPUBusy
			darwinCPUMu.Unlock()
		}
	}

	// macOS has no PSI, but it does have its own kernel pressure verdict —
	// the same one Activity Monitor's gauge reads. Prefer that over the
	// available-percent fallback: macOS deliberately runs with little "Free"
	// memory (it fills spare RAM with reclaimable cache), so the fallback's
	// Linux-calibrated thresholds read as tight almost all the time here.
	if level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		sys.PressureRaw, sys.PressureSource = float64(level), PressureSourceMemoryStatus
	} else {
		sys.PressureRaw, sys.PressureSource = availablePressure(sys), PressureSourceAvailablePct
	}
	return sys, nil
}
