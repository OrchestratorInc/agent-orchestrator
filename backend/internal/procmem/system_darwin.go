//go:build darwin

package procmem

import (
	"context"
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

func ReadPressure() (Pressure, error) {
	if level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		return Pressure{Raw: float64(level), Source: PressureSourceMemoryStatus}, nil
	}
	sys, err := ReadSystem()
	if err != nil {
		return Pressure{}, err
	}
	return Pressure{Raw: sys.PressureRaw, Source: sys.PressureSource}, nil
}

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
	sys.AvailableBytes = stat.AvailableBytes(total)
	sys.SwapPages = stat.SwapIns + stat.SwapOuts
	sys.SwapPageBytes = stat.PageSize

	if raw, err := unix.SysctlRaw("vm.swapusage"); err == nil {
		sys.SwapTotalBytes, sys.SwapUsedBytes = parseSwapUsage(raw)
	}
	if raw, err := unix.SysctlRaw("vm.loadavg"); err == nil {
		sys.Load1 = parseLoadavg(raw)
	}

	if level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		sys.PressureRaw, sys.PressureSource = float64(level), PressureSourceMemoryStatus
	} else {
		sys.PressureRaw, sys.PressureSource = availablePressure(sys), PressureSourceAvailablePct
	}
	return sys, nil
}
