//go:build windows

package procmem

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
)

type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

func ReadSystem() (System, error) {
	sys := System{CPUCount: runtime.NumCPU(), Load1: -1}

	var mem memoryStatusEx
	mem.length = uint32(unsafe.Sizeof(mem))
	ret, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&mem)))
	if ret == 0 {
		return System{}, fmt.Errorf("procmem: GlobalMemoryStatusEx failed")
	}
	sys.TotalBytes = mem.totalPhys
	sys.AvailableBytes = mem.availPhys
	sys.SwapTotalBytes = mem.totalPageFile
	if mem.totalPageFile >= mem.availPageFile {
		sys.SwapUsedBytes = mem.totalPageFile - mem.availPageFile
	}

	var idle, kernel, user windows.Filetime
	ret, _, _ = procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if ret != 0 {
		idleTicks, kernelTicks, userTicks := filetimeTicks(idle), filetimeTicks(kernel), filetimeTicks(user)
		sys.CPUTotalTicks = kernelTicks + userTicks
		if sys.CPUTotalTicks >= idleTicks {
			sys.CPUBusyTicks = sys.CPUTotalTicks - idleTicks
		}
	}

	sys.PressureRaw, sys.PressureSource = availablePressure(sys), PressureSourceAvailablePct
	return sys, nil
}

func filetimeTicks(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}
