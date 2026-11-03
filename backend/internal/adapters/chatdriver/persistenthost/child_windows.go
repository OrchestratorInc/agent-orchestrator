//go:build windows

package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func configureProviderProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED
}

// startProviderProcess puts the suspended bridge in a kill-on-close Job Object
// before its first instruction can spawn descendants. The host is not in the
// job. Closing it after Run's child.Wait requests termination of descendants
// whose bridge already exited without interrupting host cleanup.
func startProviderProcess(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create provider job: %w", err)
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("set provider job limit: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	abort := func(err error) (func(), error) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		return abort(fmt.Errorf("open provider process for job: %w", err))
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if assignErr != nil {
		return abort(fmt.Errorf("assign provider to job: %w", assignErr))
	}
	if err := resumeProviderProcess(cmd.Process.Pid); err != nil {
		return abort(fmt.Errorf("resume provider process: %w", err))
	}
	return func() { _ = windows.CloseHandle(job) }, nil
}

func resumeProviderProcess(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			return openErr
		}
		previous, resumeErr := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if resumeErr != nil {
			return resumeErr
		}
		if previous != 1 {
			return fmt.Errorf("initial provider thread suspension count was %d, want 1", previous)
		}
		return nil
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return errors.New("initial provider thread not found")
	}
	return err
}

func killProviderProcess(ctx context.Context, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	kill := aoprocess.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

// terminateProviderProcess has no graceful equivalent on Windows; the stop
// deadline's kill reaps the job.
func terminateProviderProcess(*exec.Cmd) error { return nil }

// openHostFile opens a chat host file for reading without denying deletion.
// os.Open omits FILE_SHARE_DELETE, so while Shutdown polls host.json the
// exiting host's DeleteFile fails with a sharing violation (and host.json then
// outlives its host), and a poll that lands while that delete holds the file
// fails the same way.
func openHostFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(extendedLengthPath(path))
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// maxShortPath is the length below which os leaves a Windows path as it is;
// longer paths only open with the extended-length prefix unless the process
// opted in to long paths (MAX_PATH 260 minus room for an 8.3 name).
const maxShortPath = 248

// extendedLengthPath adds the \\?\ prefix that os adds before a long path
// reaches the Windows API, so a host file that os.WriteFile could create is
// readable through CreateFile too. Like os, it leaves short, already prefixed
// and device paths alone, and expands the path first because the prefix turns
// off Windows' own normalization of separators and dot segments.
func extendedLengthPath(path string) string {
	if strings.HasPrefix(path, `\??\`) ||
		len(path) >= 4 && os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) &&
			(path[2] == '?' || path[2] == '.') && os.IsPathSeparator(path[3]) {
		return path
	}
	full, err := filepath.Abs(path)
	if err != nil || len(full) < maxShortPath {
		return path
	}
	if strings.HasPrefix(full, `\\`) {
		return `\\?\UNC\` + full[2:]
	}
	return `\\?\` + full
}

// hostFileBusy reports a host file another handle still holds: a sharing
// violation, or access denied while a delete is pending until that handle
// closes. Either clears without any action from the reader.
func hostFileBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
