//go:build windows

package procmem

import (
	"context"
	"fmt"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Identities reads every process's parent from one Toolhelp snapshot and its
// creation time from the process itself. Those are two readings, so a pid the
// snapshot listed may have been reused before the creation time was read: a
// process created after the snapshot cannot be the one it listed, so it is
// left out as exited. A process the daemon cannot open is kept with an
// unknown identity.
func Identities(ctx context.Context) (map[int]Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("procmem: CreateToolhelp32Snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	snapshotAt := time.Now()
	out := map[int]Identity{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		pid, ppid := int(entry.ProcessID), int(entry.ParentProcessID)
		identity := Identity{PPID: ppid}
		h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if openErr == nil {
			var creation, exit, kernel, user windows.Filetime
			if windows.GetProcessTimes(h, &creation, &exit, &kernel, &user) == nil {
				created := time.Unix(0, creation.Nanoseconds())
				if created.After(snapshotAt) {
					_ = windows.CloseHandle(h)
					continue // the pid was reused after the snapshot
				}
				identity.Start = strconv.FormatInt(creation.Nanoseconds(), 10)
				identity.StartedAt = created
			}
			_ = windows.CloseHandle(h)
		}
		out[pid] = identity
	}
	return out, nil
}
