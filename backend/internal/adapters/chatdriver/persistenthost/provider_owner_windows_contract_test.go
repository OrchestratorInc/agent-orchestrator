//go:build windows

package persistenthost

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestProviderOwnerWindowsKernelLimitContract(t *testing.T) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(job) })
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		class int32
		size  uint32
	}{
		{"extended", windows.JobObjectExtendedLimitInformation, uint32(unsafe.Sizeof(limits))},
		{"basic", windows.JobObjectBasicLimitInformation, uint32(unsafe.Sizeof(limits.BasicLimitInformation))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observed windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
			if err := windows.QueryInformationJobObject(job, tc.class, uintptr(unsafe.Pointer(&observed)), tc.size, nil); err != nil {
				t.Fatal("query rejected a locally created owned job", err)
			}
			if observed.BasicLimitInformation.LimitFlags != limits.BasicLimitInformation.LimitFlags {
				t.Errorf("kernel query lost acknowledged job limits: want=%x got=%x", limits.BasicLimitInformation.LimitFlags, observed.BasicLimitInformation.LimitFlags)
			}
			if err := windows.QueryInformationJobObject(windows.InvalidHandle, tc.class, uintptr(unsafe.Pointer(&observed)), tc.size, nil); err == nil {
				t.Error("kernel query accepted an invalid job handle")
			}
		})
	}
}

func TestProviderOwnerWindowsKernelMembershipContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		access uint32
	}{
		{"limited-query", windows.PROCESS_QUERY_LIMITED_INFORMATION},
		{"full-query", windows.PROCESS_QUERY_INFORMATION},
	} {
		t.Run(tc.name, func(t *testing.T) {
			process, err := windows.OpenProcess(tc.access, false, windows.GetCurrentProcessId())
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(process)
			var member int32
			result, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob").Call(uintptr(process), 0, uintptr(unsafe.Pointer(&member)))
			if result == 0 {
				t.Error("documented process query right did not permit membership observation", callErr)
			}
		})
	}
}
