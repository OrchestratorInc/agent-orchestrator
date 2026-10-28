//go:build windows

package persistenthost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func TestConfigureProviderProcessPreservesHiddenAttributes(t *testing.T) {
	cmd := aoprocess.Command("provider.exe")

	configureProviderProcess(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr = nil, want configured Windows process attributes")
	}
	for _, flag := range []uint32{
		windows.CREATE_NO_WINDOW,
		windows.CREATE_NEW_PROCESS_GROUP,
		windows.CREATE_SUSPENDED,
	} {
		if got := cmd.SysProcAttr.CreationFlags; got&flag == 0 {
			t.Fatalf("CreationFlags = %#x, want flag %#x", got, flag)
		}
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow = false, want hidden attribute preserved")
	}
}

func TestWindowsProviderBridge(t *testing.T) {
	if os.Getenv("AO_WINDOWS_JOB_BRIDGE") != "1" {
		return
	}
	child := exec.Command(os.Args[0], "-test.run=^TestWindowsProviderGrandchild$")
	child.Env = append(os.Environ(), "AO_WINDOWS_JOB_GRANDCHILD=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("AO_WINDOWS_JOB_PID_FILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "exit" {
			return
		}
	}
}

func TestWindowsProviderGrandchild(t *testing.T) {
	if os.Getenv("AO_WINDOWS_JOB_GRANDCHILD") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func TestWindowsProviderJobReapsGrandchild(t *testing.T) {
	for _, tc := range []struct {
		name         string
		providerExit bool
	}{
		{name: "explicit shutdown"},
		{name: "bridge exits first", providerExit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			pidFile := filepath.Join(dataDir, "grandchild.pid")
			cfg := Config{
				SessionID: "windows-job-" + strings.ReplaceAll(tc.name, " ", "-"),
				DataDir:   dataDir, Workdir: t.TempDir(), Protocol: ProtocolRaw,
				Env:  append(os.Environ(), "AO_WINDOWS_JOB_BRIDGE=1", "AO_WINDOWS_JOB_PID_FILE="+pidFile),
				Argv: []string{os.Args[0], "-test.run=^TestWindowsProviderBridge$"},
			}
			done := make(chan error, 1)
			go func() { done <- Run(context.Background(), cfg) }()
			t.Cleanup(func() {
				_ = Shutdown(context.Background(), dataDir, cfg.SessionID)
			})
			d := awaitDescriptor(t, dataDir, cfg.SessionID)
			var pid int
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				raw, err := os.ReadFile(pidFile)
				if err == nil {
					// WriteFile can be visible before its bytes are, so an empty
					// read is a retry, not a malformed pid.
					parsed, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
					if parseErr == nil && parsed > 0 {
						pid = parsed
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			if pid <= 0 {
				t.Fatal("grandchild did not publish its pid")
			}
			grandchild, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE,
				false, uint32(pid))
			if err != nil {
				t.Fatalf("open grandchild %d: %v", pid, err)
			}
			t.Cleanup(func() {
				if status, err := windows.WaitForSingleObject(grandchild, 0); err == nil && status == uint32(windows.WAIT_TIMEOUT) {
					_ = windows.TerminateProcess(grandchild, 1)
				}
				_ = windows.CloseHandle(grandchild)
			})
			if status, err := windows.WaitForSingleObject(grandchild, 0); err != nil || status != uint32(windows.WAIT_TIMEOUT) {
				t.Fatalf("grandchild was not running, pid=%d status=%d err=%v", pid, status, err)
			}
			if tc.providerExit {
				transport := awaitAttach(t, d)
				if _, err := fmt.Fprintln(transport.Stdin, "exit"); err != nil {
					t.Fatal(err)
				}
			} else if err := Shutdown(context.Background(), dataDir, cfg.SessionID); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil && !(tc.providerExit && errors.Is(err, io.EOF)) {
					t.Fatal(err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("host did not exit")
			}
			if status, err := windows.WaitForSingleObject(grandchild, 2000); err != nil || status != windows.WAIT_OBJECT_0 {
				t.Fatalf("provider grandchild %d survived host exit: status=%d err=%v", pid, status, err)
			}
		})
	}
}

// TestWindowsShutdownWaitBudgetCoversStopSequence pins the shutdown wait
// budget above the host's serialized stop sequence on Windows: stdin grace,
// stop deadline, reap wait, and the taskkill kill. The Unix 10 s budget left
// under 2 s of margin over the 8 s chain, and -race CI load pushed real
// hibernate shutdowns past it (PR #6635, windows-workspace, October 2026).
func TestWindowsShutdownWaitBudgetCoversStopSequence(t *testing.T) {
	chain := providerStdinGrace + providerStopDeadline + providerReapWait + providerKillTimeout
	if shutdownWaitBudget < chain+5*time.Second {
		t.Fatalf("shutdown wait budget %s does not cover the %s serialized stop sequence plus load margin", shutdownWaitBudget, chain)
	}
}

// TestWindowsShutdownToleratesSlowHostCleanup is the behavioral regression
// for the wait budget: a host that acknowledges the shutdown request but
// releases its descriptor and lock only after the old 10 s budget — exactly
// what the bounded stop sequence does under load — must still shut down
// cleanly instead of failing with a deadline error.
func TestWindowsShutdownToleratesSlowHostCleanup(t *testing.T) {
	const slowCleanup = 12 * time.Second

	dir := t.TempDir()
	sessionID := "slow-cleanup"
	hostDirPath := filepath.Join(dir, "chat-hosts", sessionID)
	if err := os.MkdirAll(hostDirPath, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	descriptorPath := filepath.Join(hostDirPath, "host.json")
	descriptor, err := json.Marshal(Descriptor{
		Version: ProtocolVersion, SessionID: sessionID, Protocol: ProtocolRaw,
		Address: listener.Addr().String(), Token: "test-token", PID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(descriptorPath, descriptor, 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(hostDirPath, "host.lock")
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			var request hello
			if decodeErr := json.NewDecoder(conn).Decode(&request); decodeErr != nil {
				_ = conn.Close()
				continue
			}
			if request.Action != "shutdown" {
				_ = conn.Close()
				continue
			}
			_ = json.NewEncoder(conn).Encode(helloResponse{OK: true})
			_ = conn.Close()
			// The host process is gone from the shutdown's point of view; it
			// just has not released its files yet.
			time.AfterFunc(slowCleanup, func() {
				_ = os.Remove(descriptorPath)
				_ = os.Remove(lockPath)
			})
			return
		}
	}()

	if err := Shutdown(context.Background(), dir, sessionID); err != nil {
		t.Fatalf("Shutdown did not tolerate a host that cleaned up after %s: %v", slowCleanup, err)
	}
}
