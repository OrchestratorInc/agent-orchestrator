//go:build windows

package persistenthost

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const providerOwnerProofSupported = true

func providerJobName(owner providerOwner) (*uint16, error) {
	identity, err := hex.DecodeString(owner.Identity)
	if err != nil || len(identity) != 32 || hex.EncodeToString(identity) != owner.Identity {
		return nil, ErrOwnershipInconclusive
	}
	return windows.UTF16PtrFromString("Global\\AOProvider-" + owner.Identity)
}

func openProviderJob(owner providerOwner, access uint32) (windows.Handle, error) {
	if !validProviderOwnerProof(owner) || owner.Proof != "windows-job-v1" {
		return 0, ErrOwnershipInconclusive
	}
	name, err := providerJobName(owner)
	if err != nil {
		return 0, err
	}
	handle, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW").Call(uintptr(access), 0, uintptr(unsafe.Pointer(name))) // #nosec G103 -- The call borrows the validated UTF-16 name only for its duration.
	if handle == 0 {
		if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) {
			return 0, nil
		}
		return 0, errors.Join(ErrOwnershipInconclusive, callErr)
	}
	job := windows.Handle(handle)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	err = windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil) // #nosec G103 -- Fixed Windows ABI structure and matching byte size.
	flags := limits.BasicLimitInformation.LimitFlags
	if err != nil || flags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 || flags&(windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK|windows.JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK) != 0 {
		_ = windows.CloseHandle(job)
		return 0, errors.Join(ErrOwnershipInconclusive, err)
	}
	return job, nil
}

func providerJobEmpty(job windows.Handle) (bool, error) {
	var info struct {
		TotalUserTime, TotalKernelTime, PeriodUserTime, PeriodKernelTime int64
		PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
	}
	err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil) // #nosec G103 -- Four LARGE_INTEGER and four DWORD fields match the 48-byte Windows ABI.
	return err == nil && info.ActiveProcesses == 0, err
}

func providerGroupStopped(owner providerOwner) (bool, error) {
	job, err := openProviderJob(owner, 0x0004) // JOB_OBJECT_QUERY
	if err != nil || job == 0 {
		return err == nil, err
	}
	defer func() { _ = windows.CloseHandle(job) }()
	return providerJobEmpty(job)
}

func stopProviderOwner(ctx context.Context, dataDir string, owner providerOwner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	job, err := openProviderJob(owner, 0x0004|0x0008) // QUERY | TERMINATE
	if err != nil {
		return err
	}
	if job != 0 {
		defer func() { _ = windows.CloseHandle(job) }()
		empty, err := providerJobEmpty(job)
		if err != nil {
			return errors.Join(ErrOwnershipInconclusive, err)
		}
		if !empty {
			if err := confirmProviderJobLeader(job, owner); err != nil {
				return err
			}
			if err := windows.TerminateJobObject(job, 1); err != nil {
				return errors.Join(ErrOwnershipInconclusive, err)
			}
		}
		for !empty {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
			empty, err = providerJobEmpty(job)
			if err != nil {
				return errors.Join(ErrOwnershipInconclusive, err)
			}
		}
	}
	owner.State = "stopped"
	return writeProviderOwner(dataDir, owner)
}

func confirmProviderJobLeader(job windows.Handle, owner providerOwner) error {
	pid := int64(owner.Group)
	if pid <= 1 || pid > math.MaxUint32 {
		return ErrOwnershipInconclusive
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	if uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != owner.Members[0].Start {
		return ErrOwnershipInconclusive
	}
	var member int32
	result, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob").Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&member))) // #nosec G103 -- The call writes one Windows BOOL into the matching int32 destination.
	state, err := windows.WaitForSingleObject(process, 0)
	if result == 0 {
		return errors.Join(ErrOwnershipInconclusive, callErr)
	}
	if member == 0 || err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	return nil
}

func withProviderOwnerLock(ctx context.Context, dataDir, sessionID, identity string, work func() error) error {
	path, err := providerOwnerPath(dataDir, sessionID, identity)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	defer func() { _ = file.Close() }()
	var overlapped windows.Overlapped
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return errors.Join(ErrOwnershipInconclusive, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer func() { _ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped) }()
	return work()
}
