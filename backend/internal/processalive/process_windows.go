//go:build windows

// Package processalive probes whether an operating-system process id still
// maps to a live process.
package processalive

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Alive reports whether pid exists. Access denied counts as alive: the process
// exists even if the current user cannot wait on it.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(handle)

	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false
	}
	return status == uint32(windows.WAIT_TIMEOUT)
}

// startTime is pid's creation time from the system process list. The list
// covers every process without opening it, so it also dates a service's pid:
// a normal user can't open one, and a reused pid often belongs to one.
func startTime(pid int) (time.Time, bool) {
	for size := uint32(256 << 10); ; {
		buf := make([]byte, size)
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &size)
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			size += 64 << 10 // room for processes started since the size was read
			continue
		}
		if err != nil {
			return time.Time{}, false
		}
		for offset := uint32(0); ; {
			entry := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[offset]))
			if entry.UniqueProcessID == uintptr(pid) {
				created := windows.Filetime{LowDateTime: uint32(entry.CreateTime), HighDateTime: uint32(entry.CreateTime >> 32)}
				return time.Unix(0, created.Nanoseconds()), true
			}
			if entry.NextEntryOffset == 0 {
				return time.Time{}, false
			}
			offset += entry.NextEntryOffset
		}
	}
}
