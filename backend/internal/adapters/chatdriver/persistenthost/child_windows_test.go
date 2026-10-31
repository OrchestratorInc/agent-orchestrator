//go:build windows

package persistenthost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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

func TestReadDescriptorRetriesSharingViolation(t *testing.T) {
	dataDir := t.TempDir()
	d := Descriptor{Version: ProtocolVersion, SessionID: "locked", Address: "127.0.0.1:1", Token: "token"}
	if err := writeDescriptor(dataDir, d); err != nil {
		t.Fatal(err)
	}
	path, err := descriptorPath(dataDir, d.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = windows.CloseHandle(handle)
		close(released)
	}()
	defer func() { <-released }()
	if _, err := os.ReadFile(path); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("locked descriptor read error = %v, want sharing violation", err)
	}
	got, err := readDescriptor(dataDir, d.SessionID)
	if err != nil {
		t.Fatalf("read shutdown descriptor: %v", err)
	}
	if got.SessionID != d.SessionID || got.Token != d.Token {
		t.Fatalf("descriptor = %+v, want session %q and original token", got, d.SessionID)
	}
}

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

// Shutdown polls host.json while the exiting host deletes it. Each must still
// succeed while the other holds the file, or the descriptor outlives its host
// (Shutdown waits out its deadline) or the poll fails with a sharing violation.
func TestWindowsHostDescriptorReadAndRemovalDoNotBlockEachOther(t *testing.T) {
	dataDir := t.TempDir()
	d := Descriptor{Version: ProtocolVersion, SessionID: "busy", Address: "127.0.0.1:1", Token: "token", PID: os.Getpid()}
	if err := writeDescriptor(dataDir, d); err != nil {
		t.Fatal(err)
	}
	path, _ := descriptorPath(dataDir, d.SessionID)

	// A reader that denies deletion, as os.Open does, makes the host's delete fail.
	plain, err := os.Open(path) //nolint:gosec // Test-owned temp path.
	if err != nil {
		t.Fatal(err)
	}
	removeErr := os.Remove(path)
	_ = plain.Close()
	if !errors.Is(removeErr, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("Remove under an os.Open reader = %v, want a sharing violation", removeErr)
	}

	reader, err := openHostFile(path)
	if err != nil {
		t.Fatal(err)
	}
	removeErr = os.Remove(path)
	_ = reader.Close()
	if removeErr != nil {
		t.Fatalf("Remove under a host-file reader: %v", removeErr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("host.json after Remove: %v, want it gone", err)
	}

	// A handle opened for deletion, as DeleteFile and MoveFileEx hold one,
	// denies an os.Open reader but not readDescriptor.
	if err := writeDescriptor(dataDir, d); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	deleting, err := windows.CreateFile(name, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(deleting) }()
	if plain, err := os.Open(path); err == nil { //nolint:gosec // Test-owned temp path.
		_ = plain.Close()
		t.Fatal("os.Open under a delete handle succeeded, want a sharing violation")
	} else if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) || !hostFileBusy(err) {
		t.Fatalf("os.Open under a delete handle = %v, want a busy sharing violation", err)
	}
	got, err := readDescriptor(dataDir, d.SessionID)
	if err != nil || got.Token != d.Token {
		t.Fatalf("readDescriptor under a delete handle = (%+v, %v), want the descriptor", got, err)
	}
}

func TestWindowsHostDescriptorReadsPastMaxPath(t *testing.T) {
	// A data directory deep enough that host.json passes MAX_PATH. os writes
	// it with the extended-length prefix; the share-delete reader must too.
	dataDir := t.TempDir()
	for len(dataDir) < windows.MAX_PATH {
		dataDir = filepath.Join(dataDir, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	d := Descriptor{Version: ProtocolVersion, SessionID: "long-path", Address: "127.0.0.1:1", Token: "token", PID: os.Getpid()}
	if err := writeDescriptor(dataDir, d); err != nil {
		t.Fatal(err)
	}
	path, _ := descriptorPath(dataDir, d.SessionID)
	if len(path) <= windows.MAX_PATH {
		t.Fatalf("descriptor path is %d characters, want past MAX_PATH", len(path))
	}

	got, err := readDescriptor(dataDir, d.SessionID)
	if err != nil || got.Token != d.Token {
		t.Fatalf("readDescriptor past MAX_PATH = (%+v, %v), want the descriptor", got, err)
	}
	reader, err := openHostFile(path)
	if err != nil {
		t.Fatal(err)
	}
	removeErr := os.Remove(path)
	_ = reader.Close()
	if removeErr != nil {
		t.Fatalf("Remove past MAX_PATH under a host-file reader: %v", removeErr)
	}
}

func TestExtendedLengthPathMatchesOS(t *testing.T) {
	long := strings.Repeat("d", maxShortPath)
	for _, tc := range []struct{ name, in, want string }{
		{"short", `C:\ao\host.json`, `C:\ao\host.json`},
		{"already extended", `\\?\C:\` + long, `\\?\C:\` + long},
		{"device", `\\.\pipe\` + long, `\\.\pipe\` + long},
		{"long drive path, normalized", `C:/ao/x/../` + long, `\\?\C:\ao\` + long},
		{"long UNC path", `\\server\share\` + long, `\\?\UNC\server\share\` + long},
	} {
		if got := extendedLengthPath(tc.in); got != tc.want {
			t.Errorf("%s: extendedLengthPath(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
