//go:build !windows

package persistenthost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A provider can start a helper in its own session that inherits its output,
// as Codex does for npx MCP servers. Signalling the provider's process group
// misses that helper, which keeps the pipe open. Shutdown must still end the
// host: a host left alive keeps Codex's thread writer, so waking a hibernated
// chat fails with "already has an active writer" (#8632).
func TestShutdownEndsTheHostWhenAProviderHelperEscapesItsGroup(t *testing.T) {
	pidFile := t.TempDir() + "/escaped.pid"
	cfg := Config{
		SessionID: "escaping-helper", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env:  append(os.Environ(), "AO_CHAT_HOST_ESCAPING_HELPER=1", "AO_CHAT_HOST_ESCAPED_PID_FILE="+pidFile),
		Argv: []string{os.Args[0], "-test.run=TestEscapingProviderHelper"},
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		_ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID)
	})
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	d, err := readDescriptor(cfg.DataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the provider never started its helper")
		}
	}
	_ = transport.Stdin.Close()
	shutdownErr := Shutdown(context.Background(), cfg.DataDir, cfg.SessionID)
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if err := syscall.Kill(d.PID, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("chat host %d still running after Shutdown (Shutdown returned %v)", d.PID, shutdownErr)
		}
	}
	if shutdownErr != nil {
		t.Fatalf("Shutdown = %v, want the host stopped", shutdownErr)
	}
}

// TestEscapingProviderHelper is the provider for the test above: it ignores
// stdin, starts a helper in a new session holding its stdout and stderr, and
// waits to be killed.
func TestEscapingProviderHelper(t *testing.T) {
	if os.Getenv("AO_CHAT_HOST_ESCAPING_HELPER") != "1" {
		return
	}
	helper := exec.Command("sleep", "300")
	helper.Stdout, helper.Stderr = os.Stdout, os.Stderr
	helper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := helper.Start(); err != nil {
		os.Exit(3)
	}
	_ = os.WriteFile(os.Getenv("AO_CHAT_HOST_ESCAPED_PID_FILE"), []byte(strconv.Itoa(helper.Process.Pid)), 0o600)
	select {}
}
