//go:build windows

// Package processalive probes whether an operating-system process id still
// maps to a live process.
package processalive

import (
	"errors"
	"fmt"
	"math"

	"golang.org/x/sys/windows"
)

var (
	openProcess         = windows.OpenProcess
	waitForSingleObject = windows.WaitForSingleObject
)

// Alive 是保留所有权的保守判断：未知也返回 true，只有确认死亡才返回 false。
// 需要确认存活或向用户报告观察失败的调用者必须使用 Probe。
func Alive(pid int) bool {
	alive, err := Probe(pid)
	return alive || err != nil
}

// Probe 区分存活、死亡和查询失败；错误不能授权夺锁、清理或报告已经停止。
// AccessDenied 保留原有存在性语义；非法 PID、无此进程及已触发的进程句柄表示死亡。
func Probe(pid int) (bool, error) {
	// 外部 PID 超出 DWORD 时拒绝，防止截断后查询其他进程。
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return false, nil
	}
	handle, err := openProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return true, nil
		case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
			return false, nil
		default:
			return false, fmt.Errorf("observe process %d: %w", pid, err)
		}
	}
	// 关闭只读等待句柄不会改变已观察的进程状态。
	defer func() { _ = windows.CloseHandle(handle) }()

	status, err := waitForSingleObject(handle, 0)
	if err != nil {
		return false, fmt.Errorf("wait for process %d: %w", pid, err)
	}
	switch status {
	case uint32(windows.WAIT_TIMEOUT):
		return true, nil
	case windows.WAIT_OBJECT_0:
		return false, nil
	default:
		return false, fmt.Errorf("observe process %d: unexpected wait status %#x", pid, status)
	}
}
