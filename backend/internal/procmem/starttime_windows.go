//go:build windows

package procmem

import (
	"context"
	"fmt"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StartTimes reads every process's creation time, keyed by pid. A reused pid
// gets a new creation time, so pid plus creation time identifies one process.
// A process the daemon cannot open is left out. The value is opaque: compare
// it only with another StartTimes reading. run is unused on Windows.
func StartTimes(ctx context.Context, run Runner) (map[int]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("procmem: CreateToolhelp32Snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	starts := map[int]string{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		pid := int(entry.ProcessID)
		h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if openErr != nil {
			continue
		}
		var creation, exit, kernel, user windows.Filetime
		if windows.GetProcessTimes(h, &creation, &exit, &kernel, &user) == nil {
			starts[pid] = strconv.FormatInt(creation.Nanoseconds(), 10)
		}
		_ = windows.CloseHandle(h)
	}
	return starts, nil
}
