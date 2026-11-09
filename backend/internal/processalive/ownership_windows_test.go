//go:build windows

package processalive_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// TestObservationChild 是只等待 stdin 关闭的受控子进程，不运行 Agent 或 Provider。
func TestObservationChild(t *testing.T) {
	if os.Getenv("AO_OBSERVATION_CHILD") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
}

// observationChild 以真实进程退出验证恢复，清理始终等待 Wait 收回句柄。
func observationChild(t *testing.T) (int, func()) {
	t.Helper()
	cmd := aoprocess.Command(os.Args[0], "-test.run=^TestObservationChild$")
	cmd.Env = append(os.Environ(), "AO_OBSERVATION_CHILD=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		t.Fatal(err)
	}
	// 保留一个只读句柄，使退出后的进程对象仍可 OpenProcess，实际走 WAIT_OBJECT_0 恢复。
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = stdin.Close()
			if err := cmd.Wait(); err != nil {
				t.Errorf("fixture exit: %v", err)
			}
		}
	}
	t.Cleanup(stop)
	return cmd.Process.Pid, stop
}

// TestObservationFailurePreservesHostOwnership 覆盖共享 OpenProcess 失败传入宿主的副作用边界。
// 若把未知压成死亡，竞争 Run 会夺锁，重连会准备新进程，Reconcile 会删描述符。
func TestObservationFailurePreservesHostOwnership(t *testing.T) {
	pid, stop := observationChild(t)
	dataDir := t.TempDir()
	cfg := persistenthost.Config{SessionID: "observation", DataDir: dataDir, Workdir: dataDir,
		Argv: []string{filepath.Join(dataDir, "never-start.exe")}}
	dir := filepath.Join(dataDir, "chat-hosts", cfg.SessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, err := json.Marshal(persistenthost.Descriptor{Version: persistenthost.ProtocolVersion,
		SessionID: cfg.SessionID, Address: "127.0.0.1:0", Token: "fixture-only", PID: pid})
	if err != nil {
		t.Fatal(err)
	}
	lock := []byte(fmt.Sprintf("%d\n", pid))
	seed := func() {
		t.Helper()
		for name, data := range map[string][]byte{"host.lock": lock, "host.json": descriptor} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	unchanged := func() {
		t.Helper()
		for name, want := range map[string][]byte{"host.lock": lock, "host.json": descriptor} {
			got, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("unknown observation changed %s: %q, %v", name, got, err)
			}
		}
	}
	seed()
	if got, ok := persistenthost.HostPID(dataDir, cfg.SessionID); got != pid || !ok {
		t.Fatalf("confirmed live host PID = %d, %v", got, ok)
	}
	restore := processalive.FailProbeForTest(t, pid, false)
	if err := persistenthost.Run(context.Background(), cfg); !errors.Is(err, persistenthost.ErrHostExists) {
		t.Errorf("competing Run = %v, want ErrHostExists", err)
	}
	unchanged()
	seed()
	prepared := false
	cfg.Prepare = func(context.Context) (persistenthost.PreparedProvider, error) {
		prepared = true
		return persistenthost.PreparedProvider{}, errors.New("fixture: forbidden second start")
	}
	if _, err := persistenthost.ConnectOrStart(context.Background(), cfg); !errors.Is(err, persistenthost.ErrOwnershipInconclusive) || prepared {
		t.Errorf("ConnectOrStart = %v, prepare=%v; unknown must block restart", err, prepared)
	}
	if err := persistenthost.Shutdown(context.Background(), dataDir, cfg.SessionID); err == nil {
		t.Error("Shutdown reported success for an unreachable owner with unknown liveness")
	}
	if got, ok := persistenthost.HostPID(dataDir, cfg.SessionID); got != 0 || ok {
		t.Errorf("memory accounting claimed confirmed live PID: %d, %v", got, ok)
	}
	_ = persistenthost.Reconcile(context.Background(), dataDir, nil)
	unchanged()

	seed()
	stop()
	restore()
	if err := persistenthost.Shutdown(context.Background(), dataDir, cfg.SessionID); err != nil {
		t.Errorf("confirmed dead Shutdown: %v", err)
	}
	cfg.ReconnectOnly = true
	if _, err := persistenthost.ConnectOrStart(context.Background(), cfg); !errors.Is(err, persistenthost.ErrNotRunning) {
		t.Errorf("confirmed dead reconnect: %v", err)
	}
	if err := persistenthost.Reconcile(context.Background(), dataDir, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"host.lock", "host.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("confirmed dead %s not reclaimed: %v", name, err)
		}
	}
}
