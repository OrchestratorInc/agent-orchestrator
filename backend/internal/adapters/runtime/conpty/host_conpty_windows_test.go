//go:build windows

package conpty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestMain 复用生产 pty-host 入口，让真实分离启动检查经过 READY 和父端资源释放。
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "pty-host" {
		os.Exit(RunHost(os.Args[2:], os.Stdout))
	}
	os.Exit(m.Run())
}

// TestWindowsDefaultSpawnHostEndToEnd 验证 READY 后父端关闭/释放不终止宿主，取消启动请求后仍能交互并清理。
func TestWindowsDefaultSpawnHostEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addr, hostPID, err := defaultSpawnHost(ctx, "spawn-e2e", t.TempDir(), []string{
		filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/q", "/k", "echo AO_START",
	}, map[string]string{"AO_TEST_END": "READY"}, HostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	t.Cleanup(func() {
		_ = clientKill(addr)
		if pidAlive(hostPID) {
			if process, findErr := os.FindProcess(hostPID); findErr == nil {
				_ = process.Kill()
				_ = process.Release()
			}
		}
	})
	sent := false
	deadline := time.Now().Add(5 * time.Second)
	for {
		// ConPTY 的初次整屏重绘会带尾部空行，不能只取最后 20 行而截掉就绪标记。
		output, err := clientGetOutput(context.Background(), addr, 200)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output, "AO_DETACHED_READY") {
			break
		}
		// READY 证明宿主可连接，shell 自己的标记才证明初始化结束、能够接收命令。
		if !sent && strings.Contains(output, "AO_START") {
			if err := clientSendInput(addr, "echo AO_DETACHED_%AO_TEST_END%\r\n"); err != nil {
				t.Fatalf("宿主释放后的输入: %v", err)
			}
			sent = true
		}
		if time.Now().After(deadline) {
			t.Fatalf("宿主释放后未执行命令: %q", output)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := clientKill(addr); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for pidAlive(hostPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pidAlive(hostPID) {
		t.Fatal("已终止的宿主仍残留进程")
	}
}

func TestConPTYChildExitClosesOutputAndRejectsResize(t *testing.T) {
	cmdPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	conn, err := newConPTY(t.TempDir(), cmdPath, []string{
		"/d", "/s", "/c", "echo ao-conpty-exit",
	}, initialConPTYColumns, initialConPTYRows)
	if err != nil {
		t.Fatalf("newConPTY: %v", err)
	}
	defer conn.Close()

	type readResult struct {
		output []byte
		err    error
	}
	resultC := make(chan readResult, 1)
	go func() {
		output, readErr := io.ReadAll(conn)
		resultC <- readResult{output: output, err: readErr}
	}()

	select {
	case result := <-resultC:
		if result.err != nil {
			t.Fatalf("read ConPTY output: %v", result.err)
		}
		if !bytes.Contains(result.output, []byte("ao-conpty-exit")) {
			t.Fatalf("output %q does not contain completion marker", result.output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ConPTY output did not close after the child exited")
	}
	if err := conn.Resize(120, 40); err == nil {
		t.Fatal("Resize succeeded after the pseudoconsole closed")
	}

	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("Done was not closed after the child exited")
	}
	if code, exited := conn.ExitCode(); !exited || code != 0 {
		t.Fatalf("ExitCode() = (%d, %v), want (0, true)", code, exited)
	}
}

// TestConPTYStartsAtRequestedGrid 从真实子进程读取控制台尺寸，避免 mode con 的本地化标签影响断言；
// 删除 newConPTY 的初始 Resize 会使读到的列/行与请求尺寸不符。
func TestConPTYStartsAtRequestedGrid(t *testing.T) {
	if os.Getenv("AO_TEST_CONPTY_GRID_PROBE") == "1" {
		var info windows.ConsoleScreenBufferInfo
		if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("AO_CONPTY_GRID(%d,%d)\n", info.Size.X, info.Size.Y)
		return
	}

	cols, rows, deferred := hostStartGrid(HostOptions{StartOnAttach: true, Cols: 146, Rows: 37})
	if deferred {
		t.Fatal("an explicit grid must start the ConPTY immediately")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_TEST_CONPTY_GRID_PROBE", "1")
	conn, err := newConPTY(t.TempDir(), exe, []string{"-test.run=^TestConPTYStartsAtRequestedGrid$"}, cols, rows)
	if err != nil {
		t.Fatalf("newConPTY: %v", err)
	}
	defer conn.Close()

	resultC := make(chan []byte, 1)
	go func() {
		output, _ := io.ReadAll(conn)
		resultC <- output
	}()
	var output []byte
	select {
	case output = <-resultC:
	case <-time.After(10 * time.Second):
		t.Fatal("控制台尺寸探测未结束")
	}
	if !bytes.Contains(output, []byte("AO_CONPTY_GRID(146,37)")) {
		t.Fatalf("控制台尺寸输出 %q，预期列 146、行 37", output)
	}
	if code, exited := conn.ExitCode(); !exited || code != 0 {
		t.Fatalf("控制台尺寸探测 exit=%d exited=%v", code, exited)
	}
}
