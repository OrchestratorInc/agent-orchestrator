//go:build windows

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configureProcessGroup 保留调用方的命令行属性，并隐藏可整树取消的 Windows 进程。
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP
	cmd.SysProcAttr.HideWindow = true
}

// windowsTreeProcess 用句柄固定进程身份；保留已退出父进程的句柄可防止 PID 被复用。
type windowsTreeProcess struct {
	pid     uint32
	handle  windows.Handle
	created uint64
}

// killProcessTree 先固定原始根身份，再同步终止整树并补捕获取消期间出生的后代。
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root, err := captureTreeRoot(ctx, cmd.Process)
	if err != nil {
		return unconfirmedTreeError(err, cmd.Process.Kill())
	}
	processes := []windowsTreeProcess{*root}
	defer func() {
		for _, process := range processes {
			_ = windows.CloseHandle(process.handle)
		}
	}()
	inspectionErr := collectTreeProcesses(ctx, &processes)
	pending, waiting := processes, processes
	var killErrors []error
	if inspectionErr != nil {
		killErrors = append(killErrors, fmt.Errorf("cannot confirm process tree: %w", inspectionErr))
	}
	for {
		for _, process := range pending {
			if err := windows.TerminateProcess(process.handle, 1); err != nil {
				state, waitErr := windows.WaitForSingleObject(process.handle, 0)
				if waitErr != nil || state != windows.WAIT_OBJECT_0 {
					killErrors = append(killErrors, fmt.Errorf("terminate process %d: %w", process.pid, err))
				}
			}
		}
		if err := waitTreeProcesses(ctx, waiting); err != nil {
			return errors.Join(append(killErrors, err)...)
		}
		previous := len(processes)
		if err := collectTreeProcesses(ctx, &processes); err != nil {
			return errors.Join(append(killErrors, err)...)
		}
		if len(processes) == previous {
			return errors.Join(killErrors...)
		}
		pending = processes[previous:]
		waiting = pending
	}
}

// unconfirmedTreeError 保留诊断及其他错误身份，但剥离两个原因中的 ErrProcessDone 哨兵。
// Go watchCtx 会把该哨兵当作正常完成；身份观察失败或安全停止失败都不能获得这种成功语义。
func unconfirmedTreeError(captureErr, stopErr error) error {
	causes := []error{captureErr, stopErr}
	for index, cause := range causes {
		if errors.Is(cause, os.ErrProcessDone) {
			causes[index] = errors.New(cause.Error())
		}
	}
	return errors.Join(fmt.Errorf("cannot confirm process tree: %w", causes[0]), causes[1])
}

// captureTreeRoot 借用原始 Go 句柄的生命周期窗口绑定根身份。
// Go 1.27.1 Windows 的 Signal(0) 先获取原始句柄，只有 statusOK 才返回 EWINDOWS，且不发送信号。
// 状态只能从 statusOK 转为 released/done；夹住 OpenProcess 的两次检查均为 EWINDOWS，
// 证明整个借用窗口原始内核引用未释放。新句柄因此固定同一进程对象及 PID，后续只通过该句柄操作。
// 已释放对象及借用期间失效的对象都拒绝绑定，不能再按其保留的裸 PID 终止进程。
func captureTreeRoot(ctx context.Context, process *os.Process) (*windowsTreeProcess, error) {
	var root *windowsTreeProcess
	for probe := 0; probe < 2; probe++ {
		if err := process.Signal(syscall.Signal(0)); !errors.Is(err, syscall.EWINDOWS) {
			if root != nil {
				_ = windows.CloseHandle(root.handle)
			}
			if err == nil {
				err = errors.New("unexpected Windows process lifetime state")
			}
			return nil, err
		}
		if probe == 0 {
			var err error
			root, err = openTreeProcess(ctx, uint32(process.Pid), 0)
			if err != nil {
				return nil, err
			}
			if root == nil {
				return nil, os.ErrProcessDone
			}
		}
	}
	return root, nil
}

// openTreeProcess 在同一原生句柄上核对父 PID 与创建时间，排除快照后退出或复用的进程。
func openTreeProcess(ctx context.Context, pid, parent uint32) (*windowsTreeProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if parent != 0 {
		var info windows.PROCESS_BASIC_INFORMATION
		err = windows.NtQueryInformationProcess(handle, windows.ProcessBasicInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil)
		if err != nil || info.InheritedFromUniqueProcessId != uintptr(parent) {
			_ = windows.CloseHandle(handle)
			return nil, err
		}
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return &windowsTreeProcess{pid: pid, handle: handle, created: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)}, nil
}

// collectTreeProcesses 从已固定身份的父节点扩展整树；先存关系再遍历，不依赖快照顺序。
func collectTreeProcesses(ctx context.Context, processes *[]windowsTreeProcess) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	children := make(map[uint32][]uint32)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	known := make(map[uint32]bool, len(*processes))
	for _, process := range *processes {
		known[process.pid] = true
	}
	for index := 0; index < len(*processes); index++ {
		parent := (*processes)[index]
		for _, pid := range children[parent.pid] {
			if known[pid] {
				continue
			}
			child, err := openTreeProcess(ctx, pid, parent.pid)
			if err != nil {
				return fmt.Errorf("inspect descendant %d: %w", pid, err)
			}
			if child == nil {
				continue
			}
			if child.created < parent.created {
				_ = windows.CloseHandle(child.handle)
				continue
			}
			known[pid] = true
			*processes = append(*processes, *child)
		}
	}
	return nil
}

// waitTreeProcesses 以同一取消预算等待内核确认退出，句柄在等待期间保持有效。
func waitTreeProcesses(ctx context.Context, processes []windowsTreeProcess) error {
	deadline, _ := ctx.Deadline()
	for _, process := range processes {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		state, err := windows.WaitForSingleObject(process.handle, uint32((remaining+time.Millisecond-1)/time.Millisecond))
		if err != nil {
			return fmt.Errorf("wait for process %d: %w", process.pid, err)
		}
		if state != windows.WAIT_OBJECT_0 {
			return context.DeadlineExceeded
		}
	}
	return nil
}
