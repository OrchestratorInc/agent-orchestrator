//go:build windows

package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type providerChild struct {
	process windows.Handle
	thread  windows.Handle
	job     windows.Handle
}

var providerChildBirthForTest atomic.Pointer[func(int)]
var providerChildPhaseForTest atomic.Pointer[func(string, int)]

func startProviderChild(ctx context.Context, command *exec.Cmd, dataDir string, owner *providerOwner) (_ *providerChild, resultErr error) {
	started := false
	defer func() {
		if resultErr != nil && !started {
			if receiptErr := finishUnstartedProvider(dataDir, *owner); receiptErr != nil {
				resultErr = errors.Join(resultErr, receiptErr)
			}
		}
	}()
	if stdin, ok := command.Stdin.(*os.File); ok {
		defer func() { _ = stdin.Close() }()
	}
	if stdout, ok := command.Stdout.(*os.File); ok {
		defer func() { _ = stdout.Close() }()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := providerJobName(*owner)
	if err != nil {
		return nil, err
	}
	handle, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("CreateJobObjectW").Call(0, uintptr(unsafe.Pointer(name))) // #nosec G103 -- The call borrows the validated UTF-16 name only for its duration.
	if handle == 0 {
		return nil, errors.Join(ErrOwnershipInconclusive, callErr)
	}
	child := &providerChild{job: windows.Handle(handle)}
	if errors.Is(callErr, windows.ERROR_ALREADY_EXISTS) {
		child.close()
		return nil, ErrOwnershipInconclusive
	}
	defer func() {
		if resultErr != nil {
			_ = child.stop(context.WithoutCancel(ctx))
			if child.process != 0 {
				_ = child.wait()
			}
			child.close()
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(child.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil { // #nosec G103 -- Fixed Windows ABI structure and matching byte size.
		return nil, err
	}
	pid, err := child.create(command)
	if err != nil {
		return nil, err
	}
	started = true
	if barrier := providerChildBirthForTest.Load(); barrier != nil {
		(*barrier)(pid)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(child.process, &created, &exited, &kernel, &user); err != nil {
		return nil, err
	}
	owner.State, owner.Proof, owner.Group = "active", "windows-job-v1", pid
	owner.Members = []providerProcessIdentity{{PID: pid, Start: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)}}
	if !validProviderOwnerProof(*owner) {
		return nil, ErrOwnershipInconclusive
	}
	observeProviderChildPhase("before-publication", pid)
	if err := writeProviderOwner(dataDir, *owner); err != nil {
		return nil, err
	}
	observeProviderChildPhase("after-publication", pid)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if previous, err := windows.ResumeThread(child.thread); err != nil || previous != 1 {
		return nil, errors.Join(ErrOwnershipInconclusive, err)
	}
	observeProviderChildPhase("after-resume", pid)
	return child, nil
}

func observeProviderChildPhase(phase string, pid int) {
	if barrier := providerChildPhaseForTest.Load(); barrier != nil {
		(*barrier)(phase, pid)
	}
}

func (c *providerChild) create(command *exec.Cmd) (int, error) {
	if command.Err != nil {
		return 0, command.Err
	}
	stdin, stdinOK := command.Stdin.(*os.File)
	stdout, stdoutOK := command.Stdout.(*os.File)
	if !stdinOK || !stdoutOK {
		return 0, ErrOwnershipInconclusive
	}
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stderr.Close() }()
	var inherited [3]windows.Handle
	defer func() {
		for _, handle := range inherited {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
		}
	}()
	for i, file := range []*os.File{stdin, stdout, stderr} {
		if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &inherited[i], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return 0, err
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return 0, err
	}
	defer attributes.Delete()
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherited[0]), unsafe.Sizeof(inherited)); err != nil { // #nosec G103 -- The attribute container retains this handle array until Delete.
		return 0, err
	}
	// Assignment after creation leaves an uncontained child if the host dies.
	const jobListAttribute = 0x0002000d
	if err := attributes.Update(jobListAttribute, unsafe.Pointer(&c.job), unsafe.Sizeof(c.job)); err != nil { // #nosec G103 -- The attribute container retains this job handle until Delete.
		return 0, err
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = inherited[0], inherited[1], inherited[2]
	startup.ProcThreadAttributeList = attributes.List()
	path := command.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(command.Dir, path)
	}
	application, pathErr := windows.UTF16PtrFromString(path)
	arguments, argsErr := windows.UTF16PtrFromString(windows.ComposeCommandLine(command.Args))
	directory, dirErr := windows.UTF16PtrFromString(command.Dir)
	if err := errors.Join(pathErr, argsErr, dirErr); err != nil {
		return 0, err
	}
	environment := command.Environ()
	for _, entry := range environment {
		if strings.ContainsRune(entry, '\x00') {
			return 0, errors.New("provider environment contains a null byte")
		}
	}
	encodedEnv := utf16.Encode([]rune(strings.Join(environment, "\x00") + "\x00\x00"))
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	var process windows.ProcessInformation
	err = windows.CreateProcess(application, arguments, nil, nil, true, flags, &encodedEnv[0], directory, &startup.StartupInfo, &process)
	if err != nil {
		return 0, err
	}
	c.process, c.thread = process.Process, process.Thread
	return int(process.ProcessId), nil
}

func (c *providerChild) stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return windows.TerminateJobObject(c.job, 1)
}

func (c *providerChild) wait() error {
	if result, err := windows.WaitForSingleObject(c.process, windows.INFINITE); err != nil || result != windows.WAIT_OBJECT_0 {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(c.process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("provider exited with status %d", code)
	}
	return nil
}

func (c *providerChild) close() {
	for _, handle := range []windows.Handle{c.thread, c.process, c.job} {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
	}
}
