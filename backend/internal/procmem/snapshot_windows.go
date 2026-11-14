//go:build windows

package procmem

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	psapi                    = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

func getProcessMemoryInfo(h windows.Handle) (workingSetSize uint64, ok bool) {
	var counters processMemoryCounters
	counters.cb = uint32(unsafe.Sizeof(counters))
	ret, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&counters)), uintptr(counters.cb))
	if ret == 0 {
		return 0, false
	}
	return uint64(counters.workingSetSize), true
}

func Snapshot(ctx context.Context, run Runner) (*Table, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("procmem: CreateToolhelp32Snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	t := &Table{byPID: map[int]Process{}, children: map[int][]int{}}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		pid, ppid := int(entry.ProcessID), int(entry.ParentProcessID)
		p := Process{PID: pid, PPID: ppid, Command: windows.UTF16ToString(entry.ExeFile[:])}
		if len(p.Command) > maxCommandLen {
			p.Command = p.Command[:maxCommandLen]
		}
		if h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)); openErr == nil {
			if rss, ok := getProcessMemoryInfo(h); ok {
				p.RSSBytes = rss
			}
			var creation, exit, kernel, user windows.Filetime
			if windows.GetProcessTimes(h, &creation, &exit, &kernel, &user) == nil {
				p.CPUSeconds = filetimeSeconds(kernel) + filetimeSeconds(user)
			}
			_ = windows.CloseHandle(h)
		}
		t.byPID[pid] = p
		t.children[ppid] = append(t.children[ppid], pid)
	}
	return t, nil
}

func filetimeSeconds(ft windows.Filetime) float64 {
	return float64(filetimeTicks(ft)) / 1e7
}
