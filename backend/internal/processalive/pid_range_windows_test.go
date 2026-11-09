//go:build windows

package processalive

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
	if !Alive(pid) {
		t.Fatal("当前测试进程应存活")
	}
	alias := int(uint64(pid) + 1<<32)
	if Alive(alias) {
		t.Fatal("超出 DWORD 的 PID 被截断为存活进程")
	}
}
