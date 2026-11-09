//go:build windows

package process_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// TestCommandTreeCancellationRejectsReleasedRoot 用真实已回收对象和可控 PID 复用验证身份失效后不得杀旁路。
func TestCommandTreeCancellationRejectsReleasedRoot(t *testing.T) {
	dir := t.TempDir()
	finished := aoprocess.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
	finished.Env = append(os.Environ(), "AO_TREE_TEST_ROLE=finished", "AO_TREE_TEST_DIR="+dir)
	aoprocess.ConfigureTreeCancellation(finished)
	if err := finished.Run(); err != nil {
		t.Fatalf("完成根进程: %v", err)
	}
	if err := finished.Process.Signal(syscall.Signal(0)); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("已 Wait 的根状态错误 = %v，期望 EINVAL", err)
	}
	unrelated := aoprocess.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
	unrelated.Env = append(os.Environ(), "AO_TREE_TEST_ROLE=unrelated", "AO_TREE_TEST_DIR="+dir)
	if err := unrelated.Start(); err != nil {
		t.Fatalf("启动旁路进程: %v", err)
	}
	t.Cleanup(func() {
		_ = unrelated.Process.Kill()
		_ = unrelated.Wait()
	})
	waitWindowsTreeHelperPID(t.Context(), t, filepath.Join(dir, "unrelated-ready"))
	unrelatedHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(unrelated.Process.Pid))
	if err != nil {
		t.Fatalf("观察旁路进程: %v", err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(unrelatedHandle) })
	// 不依赖系统 PID 分配时机；让已释放对象的标识指向另一真实内核对象。
	originalPID := finished.Process.Pid
	finished.Process.Pid = unrelated.Process.Pid
	defer func() { finished.Process.Pid = originalPID }()
	err = finished.Cancel()
	state, waitErr := windows.WaitForSingleObject(unrelatedHandle, 100)
	if waitErr != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("已释放根误杀旁路进程 %d；state=%d wait=%v cancel=%v", unrelated.Process.Pid, state, waitErr, err)
	}
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("已释放根取消错误 = %v，期望身份未确认且保留 EINVAL", err)
	}
	t.Log("已释放根拒绝取消，另一真实进程仍存活")
}

// TestCommandTreeCancellationReapsExitedRoot 保留原始根句柄，核验自然退出后的持管道子进程仍被同步回收。
func TestCommandTreeCancellationReapsExitedRoot(t *testing.T) {
	dir := t.TempDir()
	root := aoprocess.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
	root.Env = append(os.Environ(), "AO_TREE_TEST_ROLE=exited-parent", "AO_TREE_TEST_DIR="+dir)
	aoprocess.ConfigureTreeCancellation(root)
	root.WaitDelay = time.Second
	var output bytes.Buffer
	root.Stdout, root.Stderr = &output, &output
	if err := root.Start(); err != nil {
		t.Fatalf("启动根进程: %v", err)
	}
	t.Cleanup(func() {
		_ = root.Process.Kill()
		_ = root.Wait()
	})
	rootHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(root.Process.Pid))
	if err != nil {
		t.Fatalf("观察原始根: %v", err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(rootHandle) })
	state, err := windows.WaitForSingleObject(rootHandle, 10000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("根未自然退出: state=%d err=%v", state, err)
	}
	if err := root.Process.Signal(syscall.Signal(0)); !errors.Is(err, syscall.EWINDOWS) {
		t.Fatalf("自然退出但尚未 Wait 的原始句柄状态 = %v，期望 EWINDOWS", err)
	}
	childPID := waitWindowsTreeHelperPID(t.Context(), t, filepath.Join(dir, "ready"))
	childHandle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(childPID))
	if err != nil {
		t.Fatalf("观察持管道子进程: %v", err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(childHandle, 1)
		_, _ = windows.WaitForSingleObject(childHandle, 5000)
		_ = windows.CloseHandle(childHandle)
	})
	started := time.Now()
	cancelErr := root.Cancel()
	state, err = windows.WaitForSingleObject(childHandle, 0)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("根已退出后的子进程 %d 仍存活: state=%d err=%v cancel=%v", childPID, state, err, cancelErr)
	}
	if cancelErr != nil {
		t.Fatalf("原始根已确认退出仍未完成整树回收: %v", cancelErr)
	}
	if err := root.Wait(); err != nil {
		t.Fatalf("子进程结束后输出管道未正常回收: %v", err)
	}
	t.Logf("根已自然退出，子进程和输出管道在 %s 内回收", time.Since(started))
}

// TestCommandTreeCancellationDoesNotHideUnconfirmedTree 经真实 watchCtx 核验身份缺失不能被当作正常退出。
func TestCommandTreeCancellationDoesNotHideUnconfirmedTree(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	root := aoprocess.CommandContext(ctx, os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
	root.Env = append(os.Environ(), "AO_TREE_TEST_ROLE=finished")
	aoprocess.ConfigureTreeCancellation(root)
	root.WaitDelay = time.Second
	treeCancel := root.Cancel
	cancelResult := make(chan error, 1)
	root.Cancel = func() error {
		err := treeCancel()
		cancelResult <- err
		return err
	}
	if err := root.Start(); err != nil {
		t.Fatalf("启动根进程: %v", err)
	}
	t.Cleanup(func() {
		_ = root.Process.Kill()
		_ = root.Wait()
	})
	rootHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(root.Process.Pid))
	if err != nil {
		t.Fatalf("观察退出根: %v", err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(rootHandle) })
	state, err := windows.WaitForSingleObject(rootHandle, 10000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("根未自然退出: state=%d err=%v", state, err)
	}
	// 原始句柄仍有效，但标识已不可查询；不借用任何其他真实进程的身份。
	root.Process.Pid = 0
	cancel()
	var cancelErr error
	select {
	case cancelErr = <-cancelResult:
	case <-time.After(10 * time.Second):
		t.Fatal("取消未返回")
	}
	if err := root.Wait(); err == nil {
		t.Fatalf("整树身份未确认被 watchCtx 当作成功: cancel=%v", cancelErr)
	}
	if cancelErr == nil || errors.Is(cancelErr, os.ErrProcessDone) {
		t.Fatalf("整树未确认错误仍带正常退出哨兵: %v", cancelErr)
	}
	t.Logf("watchCtx 保留整树未确认失败: %v", cancelErr)
}

// waitWindowsTreeHelperPID 在调用方取消预算内等待真实 helper 就绪，供两个生命周期用例复用。
func waitWindowsTreeHelperPID(ctx context.Context, t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil && pid > 0 {
				return pid
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("等待 helper 已取消: %v", ctx.Err())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper 未就绪: %s", path)
	return 0
}
