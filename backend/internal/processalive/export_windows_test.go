//go:build windows

package processalive

import (
	"testing"

	"golang.org/x/sys/windows"
)

// FailProbeForTest 只在测试构建中替换指定 PID 的系统观察；不改变业务调用接线。
// 调用者须串行执行，并在恢复真实观察后验证已退出子进程的回收。
func FailProbeForTest(t testing.TB, pid int, duringWait bool) func() {
	t.Helper()
	previousOpen, previousWait := openProcess, waitForSingleObject
	var target windows.Handle
	openProcess = func(access uint32, inherit bool, processID uint32) (windows.Handle, error) {
		if processID != uint32(pid) {
			return previousOpen(access, inherit, processID)
		}
		if !duringWait {
			return 0, windows.ERROR_NOT_ENOUGH_MEMORY
		}
		handle, err := previousOpen(access, inherit, processID)
		target = handle
		return handle, err
	}
	waitForSingleObject = func(handle windows.Handle, timeout uint32) (uint32, error) {
		if duringWait && handle == target {
			return windows.WAIT_FAILED, windows.ERROR_INVALID_HANDLE
		}
		return previousWait(handle, timeout)
	}
	restore := func() { openProcess, waitForSingleObject = previousOpen, previousWait }
	t.Cleanup(restore)
	return restore
}
