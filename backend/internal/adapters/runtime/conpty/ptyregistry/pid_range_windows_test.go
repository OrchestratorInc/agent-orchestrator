//go:build windows

package ptyregistry

import (
	"os"
	"strconv"
	"testing"
)

// TestPIDRangeRejectsTruncatedLiveProcess 使用自身 PID 的高位别名验证系统调用前的范围边界。
func TestPIDRangeRejectsTruncatedLiveProcess(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("32 位 int 无法表示 DWORD 以上的 PID")
	}
	pid := os.Getpid()
	if alive, err := defaultPidAlive(pid); !alive || err != nil {
		t.Fatalf("当前测试进程应存活: alive=%v err=%v", alive, err)
	}
	alias := int(uint64(pid) + 1<<32)
	if alive, err := defaultPidAlive(alias); alive || err != nil {
		t.Fatalf("超出 DWORD 的 PID 应确认无效: alive=%v err=%v", alive, err)
	}
}
